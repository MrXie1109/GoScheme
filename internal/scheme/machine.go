// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

	// libLoading holds the libraries currently being loaded, so that a cycle
	// is reported instead of recursing forever.
	libLoading map[string]bool

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

	// wg tracks the interpreter threads started by (go ...).
	wg *sync.WaitGroup
}

type frame interface {
	resume(m *Machine, v Value)
}

// Parameter values live in a stack so that parameterize can nest; the guard
// makes them safe to touch from several interpreter threads.
type windFrame struct {
	before Value
	after  Value
}

type handlerFrame struct {
	proc Value
}

// NewMachine builds a machine with the standard environment installed.
func NewMachine() *Machine {
	m := &Machine{Libraries: map[string]*Library{}, wg: &sync.WaitGroup{}}
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

// ApplyWith calls proc with args and hands its first value to fn.
func (m *Machine) ApplyWith(proc Value, args []Value, fn func(*Machine, Value)) {
	m.stack = append(m.stack, &fGeneric{fn: fn})
	m.apply(proc, args)
}

// ApplyWithMulti calls proc with args and hands every value to fn.
func (m *Machine) ApplyWithMulti(proc Value, args []Value, fn func(*Machine, []Value)) {
	m.stack = append(m.stack, &fMultiGeneric{fn: fn})
	m.apply(proc, args)
}

// EvalWithMulti evaluates expr and hands every value to fn.
func (m *Machine) EvalWithMulti(expr Value, env *Env, fn func(*Machine, []Value)) {
	m.stack = append(m.stack, &fMultiGeneric{fn: fn})
	m.Eval(expr, env)
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

// multiFrame marks the continuation frames that want every value of a
// multiple-value return.  Every other frame takes the first value, which is
// what makes (display (chan-recv! ch)) and (+ 1 (floor/ 7 2)) behave the way
// one expects.
type multiFrame interface {
	wantsMultipleValues()
}

// fGeneric is a continuation frame backed by a Go closure.
type fGeneric struct {
	fn func(m *Machine, v Value)
}

func (f *fGeneric) resume(m *Machine, v Value) { f.fn(m, v) }

// fMultiGeneric is a frame that receives all of the returned values.
type fMultiGeneric struct {
	fn func(m *Machine, vs []Value)
}

func (f *fMultiGeneric) resume(m *Machine, v Value) { f.fn(m, valueList(v)) }

func (f *fMultiGeneric) wantsMultipleValues() {}

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
func (m *Machine) Run(expr Value, env *Env) (Value, error) {
	return m.guardedRun(func() (Value, error) {
		// Run may be re-entered while the machine is already evaluating (a
		// library is loaded, say); the frames below base belong to the outer
		// evaluation and must survive.
		baseStack, baseWinds, baseHands := len(m.stack), len(m.winds), len(m.hands)
		m.Eval(expr, env)
		return m.runLoop(baseStack, baseWinds, baseHands)
	})
}

// guardedRun runs fn, converting the panic used for non-local exits and Go
// runtime errors into ordinary Scheme errors.
func (m *Machine) guardedRun(fn func() (Value, error)) (result Value, err error) {
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
	return fn()
}

// runLoop drives the machine until the continuation stack is back to the
// depth it had on entry.  baseStack, baseWinds and baseHands are that entry
// state: everything above them belongs to this run and is dropped on the way
// out, everything below belongs to the caller and is left alone.
func (m *Machine) runLoop(baseStack, baseWinds, baseHands int) (Value, error) {
	restore := func() {
		if len(m.stack) > baseStack {
			m.stack = m.stack[:baseStack]
		}
		if len(m.winds) > baseWinds {
			m.winds = m.winds[:baseWinds]
		}
		if len(m.hands) > baseHands {
			m.hands = m.hands[:baseHands]
		}
	}
	for {
		if m.pending != nil {
			if !m.dispatchError() {
				err := m.pending
				m.pending = nil
				m.returning = false
				restore()
				return nil, err
			}
			continue
		}
		if m.returning {
			v := m.retVal
			if len(m.stack) <= baseStack {
				m.returning = false
				restore()
				return v, nil
			}
			f := m.stack[len(m.stack)-1]
			m.stack = m.stack[:len(m.stack)-1]
			m.retVal = nil
			m.returning = false
			if _, wantsAll := f.(multiFrame); !wantsAll {
				v = single(v)
			}
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
		if p.owner != nil && p.owner != m {
			m.raiseErrorf("continuation invoked from a different interpreter thread")
			return
		}
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
	prepBody(env, clause.Body)
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
			m.ApplyWith(p.Converter, []Value{v}, func(m *Machine, cv Value) {
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
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.values) == 0 {
		return UnspecifiedValue
	}
	return p.values[len(p.values)-1]
}

func (p *Parameter) set(v Value) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.values) == 0 {
		p.values = append(p.values, v)
		return
	}
	p.values[len(p.values)-1] = v
}

func (p *Parameter) push(v Value) {
	p.mu.Lock()
	p.values = append(p.values, v)
	p.mu.Unlock()
}

func (p *Parameter) pop() {
	p.mu.Lock()
	if len(p.values) > 1 {
		p.values = p.values[:len(p.values)-1]
	}
	p.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Continuations and dynamic-wind
// ---------------------------------------------------------------------------

// transferTo reinstates a captured continuation, running the dynamic-wind
// after/before thunks needed to move between the current and target dynamic
// environments, and then returns v to it.
func (m *Machine) transferTo(c *Continuation, v Value) {
	m.transferToWith(c, func(m *Machine) { m.Return(v) })
}

// transferToWith is transferTo with a custom action performed once the wind
// transition has completed (used by guard, which continues evaluating rather
// than returning a value).
func (m *Machine) transferToWith(c *Continuation, action func(*Machine)) {
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
		action(m)
		return
	}
	// The frames must run in order: after thunks[0] returns the machine pops
	// the top frame, so thunks[1]'s frame has to be pushed last and the final
	// action first (it is reached only after every thunk has run).
	m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, _ Value) { action(m) }})
	for i := len(thunks) - 1; i >= 1; i-- {
		th := thunks[i]
		m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, _ Value) { m.apply(th, nil) }})
	}
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
	m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, thunkVal Value) {
		if len(m.winds) > 0 && m.winds[len(m.winds)-1] == w {
			m.winds = m.winds[:len(m.winds)-1]
		}
		m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, _ Value) { m.Return(thunkVal) }})
		m.apply(f.after, nil)
	}})
	m.apply(f.thunk, nil)
}

// ---------------------------------------------------------------------------
// Evaluation entry points used by primitives
// ---------------------------------------------------------------------------

// EvalString reads every datum in src and evaluates it in the global
// environment, returning the value of the last one.  It is a convenience for
// tests and for embedding the interpreter.
func (m *Machine) EvalString(src string) (Value, error) {
	forms, err := NewStringReader(src).ReadAll()
	if err != nil {
		return nil, err
	}
	var last Value = UnspecifiedValue
	for _, f := range forms {
		v, err := m.Run(f, m.Global)
		if err != nil {
			return nil, err
		}
		last = v
	}
	return last, nil
}

// RunForms evaluates a sequence of top level forms as a single unit, so that
// a continuation captured by one form remains valid for the forms that follow
// it (the same extent a file has).
func (m *Machine) RunForms(forms []Value, env *Env) (Value, error) {
	switch len(forms) {
	case 0:
		return UnspecifiedValue, nil
	case 1:
		return m.Run(forms[0], env)
	}
	body := append([]Value{Intern("begin")}, forms...)
	return m.Run(List(body...), env)
}

// DefineGoFunc binds name to a Go function, which is how a host program offers
// its own procedures to Scheme.  maxArgs may be -1 for no limit.  An error
// returned by fn becomes an ordinary Scheme condition.
func (m *Machine) DefineGoFunc(name string, minArgs, maxArgs int, fn func([]Value) (Value, error)) {
	m.defSimple(name, minArgs, maxArgs, fn)
}

// LookupGlobal reports the value bound to a top level name.
func (m *Machine) LookupGlobal(name string) (Value, bool) {
	return m.Global.Lookup(Intern(name))
}

// SetStandardInput replaces the current input port.
func (m *Machine) SetStandardInput(p *Port) {
	m.CurIn = p
	m.InParam.set(p)
}

// SetStandardOutput replaces the current output port.
func (m *Machine) SetStandardOutput(p *Port) {
	m.CurOut = p
	m.OutParam.set(p)
}

// SetStandardError replaces the current error port.
func (m *Machine) SetStandardError(p *Port) {
	m.CurErr = p
	m.ErrParam.set(p)
}

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
	if filepath.IsAbs(name) || (len(name) > 1 && name[1] == ':') {
		return name
	}
	for i := len(m.LoadPath) - 1; i >= 0; i-- {
		p := filepath.Join(m.LoadPath[i], name)
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
