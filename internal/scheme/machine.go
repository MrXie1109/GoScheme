package scheme

import (
	"fmt"
	"os"
	"strings"
)

// Machine is a CEK-style abstract machine.  The control component is either
// an expression to evaluate (returning == false) or a value being returned to
// the top continuation frame.  The continuation is an explicit stack of
// frames, which makes proper tail calls automatic (applying a procedure never
// pushes a frame) and lets call/cc capture and reinstate a continuation by
// copying the stack.
type Machine struct {
	stack     []frame
	evalExpr  Value
	env       *Env
	retVal    Value
	returning bool

	winds []*windFrame
	hands []*handlerFrame

	pending *SchemeError

	libExports map[string][]string

	Global    *Env
	Builtin   *Env
	Libraries map[string]*Library
	LoadPath  []string

	// Standard ports.
	CurIn    *Port
	CurOut   *Port
	CurErr   *Port
	InParam  *Parameter
	OutParam *Parameter
	ErrParam *Parameter

	Args []string

	// Depth counts active Run calls (used by include / load).
	depth int
}

type frame interface {
	resume(m *Machine, v Value)
}

type windFrame struct {
	before Value
	after  Value
}

type handlerFrame struct {
	proc Value
}

// NewMachine builds a machine with the standard environment installed.
func NewMachine() *Machine {
	m := &Machine{Libraries: map[string]*Library{}}
	m.CurIn = NewPortFromFile("stdin", os.Stdin, true, true)
	m.CurOut = NewPortFromFile("stdout", os.Stdout, false, true)
	m.CurErr = NewPortFromFile("stderr", os.Stderr, false, true)
	m.InParam = &Parameter{Name: "current-input-port", IsPort: true, values: []Value{m.CurIn}}
	m.OutParam = &Parameter{Name: "current-output-port", IsPort: true, values: []Value{m.CurOut}}
	m.ErrParam = &Parameter{Name: "current-error-port", IsPort: true, values: []Value{m.CurErr}}
	m.Builtin = NewEnvNamed(nil, "builtins")
	installBuiltins(m)
	m.finishLibraries()
	m.Global = NewEnvNamed(m.Builtin, "interaction")
	return m
}

// ---------------------------------------------------------------------------
// Control helpers
// ---------------------------------------------------------------------------

// Return makes v the value being returned.
func (m *Machine) Return(v Value) {
	if v == nil {
		v = UnspecifiedValue
	}
	m.retVal = v
	m.returning = true
}

// Eval schedules expr for evaluation in env.
func (m *Machine) Eval(expr Value, env *Env) {
	m.evalExpr = expr
	m.env = env
	m.returning = false
}

// ApplyWith calls proc with args and hands the result to fn.
func (m *Machine) ApplyWith(proc Value, args []Value, fn func(*Machine, Value)) {
	m.stack = append(m.stack, &fGeneric{fn: fn})
	m.apply(proc, args)
}

// EvalWith evaluates expr and hands the result to fn.
func (m *Machine) EvalWith(expr Value, env *Env, fn func(*Machine, Value)) {
	m.stack = append(m.stack, &fGeneric{fn: fn})
	m.Eval(expr, env)
}

// EvalSeq evaluates a sequence of expressions; the last one is in tail
// position, so a trailing procedure call does not grow the continuation.
func (m *Machine) EvalSeq(exprs []Value, env *Env) {
	switch len(exprs) {
	case 0:
		m.Return(UnspecifiedValue)
	case 1:
		m.Eval(exprs[0], env)
	default:
		m.stack = append(m.stack, &fSeq{exprs: exprs[1:], env: env})
		m.Eval(exprs[0], env)
	}
}

// fGeneric is a continuation frame backed by a Go closure.
type fGeneric struct {
	fn func(m *Machine, v Value)
}

func (f *fGeneric) resume(m *Machine, v Value) { f.fn(m, v) }

type fSeq struct {
	exprs []Value
	env   *Env
}

func (f *fSeq) resume(m *Machine, v Value) { m.EvalSeq(f.exprs, f.env) }

// fAppOp is waiting for the operator of a combination.
type fAppOp struct {
	args []Value
	env  *Env
}

func (f *fAppOp) resume(m *Machine, v Value) {
	if len(f.args) == 0 {
		m.apply(v, nil)
		return
	}
	m.stack = append(m.stack, &fAppArgs{op: v, rest: f.args[1:], env: f.env})
	m.Eval(f.args[0], f.env)
}

// fAppArgs is waiting for one operand of a combination.
type fAppArgs struct {
	op   Value
	rest []Value
	done []Value
	env  *Env
}

func (f *fAppArgs) resume(m *Machine, v Value) {
	done := append(f.done, v)
	if len(f.rest) == 0 {
		m.apply(f.op, done)
		return
	}
	m.stack = append(m.stack, &fAppArgs{op: f.op, rest: f.rest[1:], done: done, env: f.env})
	m.Eval(f.rest[0], f.env)
}

// ---------------------------------------------------------------------------
// The main loop
// ---------------------------------------------------------------------------

// Run evaluates expr in env until the continuation stack is exhausted.
func (m *Machine) Run(expr Value, env *Env) (result Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			m.stack = m.stack[:0]
			m.winds = m.winds[:0]
			m.hands = m.hands[:0]
			m.pending = nil
			m.returning = false
			switch e := r.(type) {
			case *SchemeError:
				result, err = nil, e
			case *ErrorObject:
				result, err = nil, &SchemeError{Condition: e}
			case *PortError:
				result, err = nil, &SchemeError{Condition: NewError(e.Error())}
			case *ExitError:
				result, err = nil, e
			default:
				panic(r)
			}
		}
	}()
	base := len(m.stack)
	m.Eval(expr, env)
	return m.runLoop(base)
}

func (m *Machine) runLoop(base int) (Value, error) {
	for {
		if m.pending != nil {
			if !m.dispatchError() {
				err := m.pending
				m.pending = nil
				m.stack = m.stack[:0]
				m.winds = m.winds[:0]
				m.hands = m.hands[:0]
				m.returning = false
				return nil, err
			}
			continue
		}
		if m.returning {
			v := m.retVal
			if len(m.stack) <= base {
				m.stack = m.stack[:0]
				m.returning = false
				return v, nil
			}
			f := m.stack[len(m.stack)-1]
			m.stack = m.stack[:len(m.stack)-1]
			m.retVal = nil
			m.returning = false
			f.resume(m, v)
			continue
		}
		m.evalStep()
	}
}

// ---------------------------------------------------------------------------
// Errors and conditions
// ---------------------------------------------------------------------------

// Raise signals a Scheme condition.
func (m *Machine) Raise(cond Value) {
	m.pending = &SchemeError{Condition: cond}
}

// RaiseError signals a Go error as a Scheme condition.
func (m *Machine) RaiseError(err error) {
	switch e := err.(type) {
	case *SchemeError:
		m.pending = e
	case *ErrorObject:
		m.pending = &SchemeError{Condition: e}
	default:
		m.pending = &SchemeError{Condition: NewError(err.Error())}
	}
}

// dispatchError routes the pending condition to the innermost exception
// handler.  It returns false when no handler is installed.
func (m *Machine) dispatchError() bool {
	cond := m.pending.Condition
	continuable := m.pending.Continuable
	m.pending = nil
	if len(m.hands) == 0 {
		m.pending = &SchemeError{Condition: cond, Continuable: continuable}
		return false
	}
	h := m.hands[len(m.hands)-1]
	m.hands = m.hands[:len(m.hands)-1]
	saved := len(m.hands)
	m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, v Value) {
		if continuable {
			if len(m.hands) < saved {
				m.hands = m.hands[:saved]
			}
			m.Return(v)
			return
		}
		m.Raise(NewError("exception handler returned from a non-continuable raise", cond))
	}})
	m.apply(h.proc, []Value{cond})
	return true
}

func (m *Machine) raiseErrorf(format string, args ...interface{}) {
	m.Raise(NewError(fmt.Sprintf(format, args...)))
}

// ---------------------------------------------------------------------------
// Procedure application
// ---------------------------------------------------------------------------

// apply calls proc with args, in tail position with respect to the current
// continuation.
func (m *Machine) apply(proc Value, args []Value) {
	switch p := proc.(type) {
	case *Closure:
		m.applyClosure(p, args)
	case *Primitive:
		if len(args) < p.MinArgs || (p.MaxArgs >= 0 && len(args) > p.MaxArgs) {
			m.raiseErrorf("%s: wrong number of arguments (got %d)", p.Name, len(args))
			return
		}
		p.Fn(m, args)
	case *Continuation:
		var v Value
		switch len(args) {
		case 0:
			v = UnspecifiedValue
		case 1:
			v = args[0]
		default:
			v = &MultipleValues{Values: args}
		}
		m.transferTo(p, v)
	case *Parameter:
		m.applyParameter(p, args)
	case *RecordTypeDescriptor:
		m.raiseErrorf("record type %s is not a procedure", p.Type.Name)
	default:
		m.raiseErrorf("attempt to apply non-procedure %s", WriteToString(proc))
	}
}

func (m *Machine) applyClosure(c *Closure, args []Value) {
	var clause *ClosureClause
	for i := range c.Clauses {
		if arityMatches(&c.Clauses[i], len(args)) {
			clause = &c.Clauses[i]
			break
		}
	}
	if clause == nil {
		if len(c.Clauses) == 1 {
			m.raiseErrorf("%s: wrong number of arguments (got %d)", procName(c), len(args))
		} else {
			m.raiseErrorf("%s: no matching clause for %d arguments", procName(c), len(args))
		}
		return
	}
	env := NewEnv(c.Env)
	if clause.HasRest {
		n := len(clause.Params)
		for i, s := range clause.Params {
			env.Define(s, args[i])
		}
		env.Define(clause.Rest, List(args[n:]...))
	} else {
		for i, s := range clause.Params {
			env.Define(s, args[i])
		}
	}
	for _, s := range c.BodyNames {
		if _, exists := env.vars[s]; !exists {
			env.Define(s, Unassigned)
		}
	}
	m.EvalSeq(clause.Body, env)
}

func procName(c *Closure) string {
	if c.Name != "" {
		return c.Name
	}
	return "procedure"
}

func arityMatches(c *ClosureClause, n int) bool {
	if c.HasRest {
		return n >= len(c.Params)
	}
	return n == len(c.Params)
}

func (m *Machine) applyParameter(p *Parameter, args []Value) {
	switch len(args) {
	case 0:
		m.Return(p.current())
	case 1:
		v := args[0]
		if p.Converter != nil && p.Converter != Value(False) {
			m.EvalWith(Value(&application{op: p.Converter, args: []Value{v}}), m.Global, func(m *Machine, cv Value) {
				p.set(cv)
				m.Return(UnspecifiedValue)
			})
			return
		}
		p.set(v)
		m.Return(UnspecifiedValue)
	default:
		m.raiseErrorf("%s: wrong number of arguments", p.Name)
	}
}

func (p *Parameter) current() Value {
	if len(p.values) == 0 {
		return UnspecifiedValue
	}
	return p.values[len(p.values)-1]
}

func (p *Parameter) set(v Value) {
	if len(p.values) == 0 {
		p.values = append(p.values, v)
		return
	}
	p.values[len(p.values)-1] = v
}

func (p *Parameter) push(v Value) { p.values = append(p.values, v) }

func (p *Parameter) pop() {
	if len(p.values) > 1 {
		p.values = p.values[:len(p.values)-1]
	}
}

// application is a tiny helper used when the machine needs to call a Scheme
// procedure from Go code.
type application struct {
	op   Value
	args []Value
}

// ---------------------------------------------------------------------------
// Continuations and dynamic-wind
// ---------------------------------------------------------------------------

func (m *Machine) transferTo(c *Continuation, v Value) {
	cur, tgt := m.winds, c.winds
	p := 0
	for p < len(cur) && p < len(tgt) && cur[p] == tgt[p] {
		p++
	}
	var thunks []Value
	for i := len(cur) - 1; i >= p; i-- {
		thunks = append(thunks, cur[i].after)
	}
	for i := p; i < len(tgt); i++ {
		thunks = append(thunks, tgt[i].before)
	}
	m.winds = append([]*windFrame(nil), tgt...)
	m.hands = append([]*handlerFrame(nil), c.hands...)
	m.stack = append([]frame(nil), c.stack...)
	if len(thunks) == 0 {
		m.Return(v)
		return
	}
	for i := len(thunks) - 1; i >= 1; i-- {
		th := thunks[i]
		m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, _ Value) { m.apply(th, nil) }})
	}
	m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, _ Value) { m.Return(v) }})
	m.apply(thunks[0], nil)
}

// fDynamicWindPush runs after `before` has returned: it records the wind frame
// and calls the thunk.
type fDynamicWindPush struct {
	before Value
	after  Value
	thunk  Value
}

func (f *fDynamicWindPush) resume(m *Machine, _ Value) {
	w := &windFrame{before: f.before, after: f.after}
	m.winds = append(m.winds, w)
	m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, v Value) {
		if len(m.winds) > 0 && m.winds[len(m.winds)-1] == w {
			m.winds = m.winds[:len(m.winds)-1]
		}
		m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, v Value) { m.Return(v) }})
		m.apply(f.after, nil)
	}})
	m.apply(f.thunk, nil)
}

// ---------------------------------------------------------------------------
// Evaluation entry points used by primitives
// ---------------------------------------------------------------------------

// AddLoadPath pushes a directory used to resolve include / load.
func (m *Machine) AddLoadPath(dir string) { m.LoadPath = append(m.LoadPath, dir) }

// PopLoadPath removes the most recently pushed directory.
func (m *Machine) PopLoadPath() {
	if len(m.LoadPath) > 0 {
		m.LoadPath = m.LoadPath[:len(m.LoadPath)-1]
	}
}

// resolvePath looks for name in the load path.
func (m *Machine) resolvePath(name string) string {
	if strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
		return name
	}
	for i := len(m.LoadPath) - 1; i >= 0; i-- {
		p := m.LoadPath[i] + "/" + name
		if fileExists(p) {
			return p
		}
	}
	return name
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
