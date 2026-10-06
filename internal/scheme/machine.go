// SPDX-License-Identifier: MIT

package scheme

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
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

	// libMu guards Libraries and libLoading.  Interpreter threads share both —
	// (go ...) makes that routine — and an unsynchronised map write is a Go
	// runtime fatal error, not something a Scheme handler can catch.
	libMu *sync.Mutex
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

	// Interpret turns the bytecode VM off, so that a program runs in the
	// tree-walker exactly as it did before compile.go existed.  It is how the
	// two execution paths are compared, and it is what `-interp` sets.
	Interpret bool

	// cancel is closed to interrupt this machine's evaluation, and watchCancel
	// says whether to look at it at all.  The REPL sets both when Ctrl-C should
	// be able to abandon a form: the evaluation runs in another goroutine, and
	// without this the keystroke printed "^C" and left the evaluation running,
	// so a form that had been abandoned would still take the value it was
	// waiting for — a (chan-send! ch x) that was "cancelled" showed up later in
	// a (chan-recv! ch).
	//
	// watchCancel is false everywhere else, and it is a plain bool so that the
	// hot loops pay one load and a branch that is almost never taken.  Running
	// a file cannot be interrupted — there is no keyboard watching it — so
	// there is nothing to check and no reason to check it per instruction.
	watchCancel bool
	cancel      <-chan struct{}

	// framesCopied records that some continuation has been captured, which
	// copies the frame stack.  From then on a compiled frame may be reachable
	// from two stacks, so the VM stops resuming frames in place and copies
	// their operand stacks instead.  Nothing sets it but a capture.
	framesCopied bool
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
	m := &Machine{Libraries: map[string]*Library{}, libMu: &sync.Mutex{}, wg: &sync.WaitGroup{}}
	m.CurIn = NewPortFromFile("stdin", os.Stdin, true, true)
	m.CurOut = NewPortFromFile("stdout", os.Stdout, false, true)
	m.CurErr = NewPortFromFile("stderr", os.Stderr, false, true)
	m.InParam = &Parameter{Name: "current-input-port", IsPort: true, values: []Value{m.CurIn}}
	m.OutParam = &Parameter{Name: "current-output-port", IsPort: true, values: []Value{m.CurOut}}
	m.ErrParam = &Parameter{Name: "current-error-port", IsPort: true, values: []Value{m.CurErr}}
	m.Builtin = NewEnvNamed(nil, "builtins")
	installBuiltins(m)
	// The arithmetic instructions stand for specific builtins, and the compiler
	// can only tell a call to the interpreter's + from a call to a program's
	// own + by comparing against them, so they are recorded here, where the
	// builtins have just been made.
	installArithmeticInstructions(m)
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

// ApplySync calls proc with args and returns its first value.
//
// It is the one call that does not go through a continuation, because its caller
// is not written in Scheme: a compiled procedure that reaches a procedure the
// compiler could not emit needs an answer before it can continue, and there is
// no frame to resume into.  The nested loop runs the call to completion and
// leaves the machine where it found it, which is what makes it safe to call from
// the middle of an evaluation.
//
// A continuation captured inside the call cannot escape it — the extent it would
// return to is this Go frame, and that frame is gone once the call returns.
// `call/cc` reaching a compiled caller is therefore not supported, and the
// procedures the compiler emits are the ones that cannot contain it.
func (m *Machine) ApplySync(proc Value, args []Value) (Value, error) {
	baseStack, baseWinds, baseHands := len(m.stack), len(m.winds), len(m.hands)
	m.apply(proc, args)
	return m.runLoop(baseStack, baseWinds, baseHands)
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

// fAppOp is waiting for the operator of a combination.  The operands are walked
// as a list: copying them into a slice for every call was one of the largest
// sources of allocation in the interpreter.
type fAppOp struct {
	args Value
	env  *Env
}

func (f *fAppOp) resume(m *Machine, v Value) {
	p, ok := f.args.(*Pair)
	if !ok {
		if _, isNil := f.args.(Empty); isNil {
			m.apply(v, nil)
			return
		}
		m.raiseErrorf("improper argument list")
		return
	}
	m.stack = append(m.stack, &fAppArgs{op: v, rest: p.Cdr, env: f.env})
	m.Eval(p.Car, f.env)
}

// inlineArgs is how many operands a combination may have before their values
// have to move to the heap.  Keeping them inside the frame removes a slice
// allocation per operand.
const inlineArgs = 4

// fAppArgs is waiting for one operand of a combination.
type fAppArgs struct {
	op   Value
	rest Value
	env  *Env
	n    int
	done [inlineArgs]Value
	more []Value
}

// collect adds a value to this frame's operands.
func (f *fAppArgs) collect(v Value) []Value {
	if f.more != nil {
		return append(f.more, v)
	}
	if f.n < inlineArgs {
		f.done[f.n] = v
		return f.done[:f.n+1]
	}
	f.more = make([]Value, inlineArgs, inlineArgs*2)
	copy(f.more, f.done[:])
	return append(f.more, v)
}

func (f *fAppArgs) resume(m *Machine, v Value) {
	done := f.collect(v)
	p, ok := f.rest.(*Pair)
	if !ok {
		if _, isNil := f.rest.(Empty); isNil {
			m.apply(f.op, done)
			return
		}
		m.raiseErrorf("improper argument list")
		return
	}
	next := &fAppArgs{op: f.op, rest: p.Cdr, env: f.env, n: len(done)}
	if f.more != nil {
		next.more = done
	} else {
		// The next frame gets its own copy of what has been collected, so this
		// one is left exactly as it was for a captured continuation.
		copy(next.done[:], done)
	}
	m.stack = append(m.stack, next)
	m.Eval(p.Car, f.env)
}

// ---------------------------------------------------------------------------
// The main loop
// ---------------------------------------------------------------------------

// Run evaluates expr in env until the continuation stack is exhausted.
// Run evaluates one form in env, with the **tree-walker**.
//
// This is the path the REPL takes, and it is deliberately the interpreter and
// not the VM.  A REPL evaluates one form at a time with no idea what comes
// next, and the tree-walker is the engine that is complete on its own: every
// form, every macro, every library, and — because it is what an unfinished
// form is resumed in — the same engine a `load` from inside the form uses.  A
// compiled form would be a second engine switching in and out between lines,
// and the one place it would differ is the one the user is looking at.
//
// Being the slower engine is the point rather than a cost: a person typing a
// line cannot tell 3× on a form that takes a millisecond, and a REPL is where
// being able to interrupt anything matters more than being quick.
//
// Everything else — a script, `-e`, a library being loaded — goes through
// RunFormsCompiled, which compiles what it can.
func (m *Machine) Run(expr Value, env *Env) (Value, error) {
	return m.runInterpreted(expr, env)
}

// runInterpreted evaluates expr in env with the tree-walker, whether or not it
// could be compiled.  It is what a form that teaches the compiler something has
// to do: a define-syntax or an import is run for its effect on the environment,
// and a compiled version of it would have no environment to have an effect on.
func (m *Machine) runInterpreted(expr Value, env *Env) (Value, error) {
	return m.guardedRun(func() (Value, error) {
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
		// The nil check comes first and is the whole cost for a machine that has
		// no cancel set — which is every machine but the one the REPL is
		// evaluating a form on — so the interruptible path costs the ordinary
		// one nothing.
		if m.interruptible() {
			restore()
			return nil, ErrInterrupted
		}
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

// ErrInterrupted is returned by an evaluation that was cancelled.  It is not a
// Scheme condition: the program did not fail, it was told to stop.
var ErrInterrupted = errors.New("interrupted")

// SetCancel gives this machine a channel to watch: closing it interrupts the
// evaluation with ErrInterrupted at its next step.  Passing nil removes the
// watch.  Only a caller that can actually deliver an interrupt should set this
// — the REPL, on the machine it is evaluating a form with — because every
// machine that watches pays for the check on its hot loops.
func (m *Machine) SetCancel(ch <-chan struct{}) {
	m.cancel = ch
	m.watchCancel = ch != nil
}

// sleepFor waits for d, and gives up early when the evaluation is interrupted.
// It is what every blocking primitive should use instead of time.Sleep: the
// REPL promises that anything can be abandoned with Ctrl-C, and a primitive
// that sleeps through it breaks that promise for as long as it sleeps — a
// (sleep 30000) left the prompt dead for thirty seconds, which is the one thing
// Ctrl-C is for.
//
// It returns true when the wait finished and false when it was cancelled, so
// that a caller can tell "slept" from "interrupted" and raise accordingly.
func (m *Machine) sleepFor(d time.Duration) bool {
	if d <= 0 {
		return true
	}
	if !m.watchCancel {
		time.Sleep(d)
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-m.cancel:
		return false
	}
}

// interruptedErr is what a blocking primitive raises when it is abandoned:
// the same ErrInterrupted the evaluation loop returns, so that the REPL treats
// it as the Ctrl-C it was rather than as a program error.
func interruptedErr() error { return ErrInterrupted }

// interruptible reports whether the evaluation should stop.  It is written to
// be one load of a bool in the usual case, which is why the flag exists
// separately from the channel.
func (m *Machine) interruptible() bool {
	return m.watchCancel && m.cancelled()
}

// cancelled reports whether the evaluation has been interrupted.
func (m *Machine) cancelled() bool {
	if m.cancel == nil {
		return false
	}
	select {
	case <-m.cancel:
		return true
	default:
		return false
	}
}

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
	// A compiled body is tried before anything else, and it is allowed to say
	// no: a native procedure computes pure arithmetic and cannot run a
	// continuation, call a procedure that was not compiled, or handle an
	// argument list it was not built for.  Declining falls through to the
	// interpreted body below, which is still present — that is what makes the
	// native path an optimization rather than a second implementation of the
	// language.
	if clause.Native != nil {
		if v, ok := clause.Native.Call(args); ok {
			m.Return(v)
			return
		}
	}
	if clause.Code != nil {
		m.applyCompiled(c, clause, args)
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
	// The names introduced by internal definitions were worked out when the
	// closure was built; binding them here is what gives the body letrec*
	// semantics, and rescanning the body on every call would be wasteful.
	for _, s := range clause.BodyNames {
		if !env.Has(s) {
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

// registerLibrary records a library.  Every write to the registry goes through
// here, because the map is shared by every interpreter thread.
func (m *Machine) registerLibrary(name string, lib *Library) {
	m.libMu.Lock()
	m.Libraries[name] = lib
	m.libMu.Unlock()
}

// lookupLibrary reads the registry under the same lock.
func (m *Machine) lookupLibrary(name string) (*Library, bool) {
	m.libMu.Lock()
	defer m.libMu.Unlock()
	lib, ok := m.Libraries[name]
	return lib, ok
}

// LibraryNames lists the registered libraries, sorted.
func (m *Machine) LibraryNames() []string {
	m.libMu.Lock()
	defer m.libMu.Unlock()
	out := make([]string, 0, len(m.Libraries))
	for name := range m.Libraries {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
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
