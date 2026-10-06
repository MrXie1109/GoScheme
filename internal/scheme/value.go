// SPDX-License-Identifier: MIT

// Package scheme implements an interpreter for the Scheme programming
// language as specified by the R7RS report (small language).
//
// The interpreter is organised as follows:
//
//	value.go     – the interpreter's half of the value representation
//	number.go    – the interpreter's half of the numeric tower
//	reader.go    – lexer and datum reader          (internal/re)
//	printer.go   – write / display                 (internal/re)
//	env.go       – environments and syntactic environments
//	machine.go   – the CEK abstract machine (evaluation core)
//	eval.go      – special forms
//	macro.go     – syntax-rules hygienic pattern matcher
//	library.go   – R7RS library system (define-library / import)
//	port.go      – textual and binary ports        (internal/re)
//	builtin*.go  – the standard procedures
//
// The values themselves — the runtime representation of Scheme data and the
// whole numeric tower, which is what value.go and number.go used to hold — are
// in internal/re, and this package dot-imports it.  The dependency goes one
// way: re knows nothing about the machine, and the file below is the machine's
// half of the representation, the few places where a value has to name
// something only the interpreter has.
package scheme

import (
	"sync"

	. "github.com/MrXie1109/GoScheme/internal/re"
)

// The dot-import is deliberate: the names in re are the names this package
// used before the split, so every call site goes on writing Value, Cons and
// Pair unchanged, and the reader sees which names moved only by looking at
// where they are declared.  The alternative — a prefix on several thousand
// references — would have been a diff that hides the move instead of showing
// it.

// ---------------------------------------------------------------------------
// The environment a mark was created in
//
// A symbol's name and its mark are the runtime environment's (see
// re.Symbol); the environment a mark came from is the interpreter's, because
// it is an *Env and the runtime environment must not know what one is.  So the
// table lives here and re only hands out the marks.
// ---------------------------------------------------------------------------

// markInfo records the environment in which a mark was created.  Identifiers
// introduced by a macro template are renamed with the mark of that expansion;
// a marked identifier that is not bound locally resolves through the
// environment captured here, which is the environment of the macro definition.
type markInfo struct {
	env  *Env
	syms map[*Symbol]*Symbol
}

var markTable = map[uint64]*markInfo{}

// newMark allocates a fresh mark identifier bound to env.
func newMark(env *Env) uint64 {
	Mark.Lock()
	defer Mark.Unlock()
	*NextMark++
	markTable[*NextMark] = &markInfo{env: env, syms: map[*Symbol]*Symbol{}}
	return *NextMark
}

// markEnvOf returns the environment attached to a mark (nil when unknown).
func markEnvOf(mark uint64) *Env {
	Mark.Lock()
	defer Mark.Unlock()
	if mi, ok := markTable[mark]; ok {
		return mi.env
	}
	return nil
}

// renameSymbol builds (and caches) the marked counterpart of sym.
func renameSymbol(sym *Symbol, mark uint64) *Symbol {
	Mark.Lock()
	defer Mark.Unlock()
	mi, ok := markTable[mark]
	if !ok {
		mi = &markInfo{syms: map[*Symbol]*Symbol{}}
		markTable[mark] = mi
	}
	if s, ok := mi.syms[sym]; ok {
		return s
	}
	s := NewMarkedSymbol(sym, mark)
	mi.syms[sym] = s
	return s
}

// ---------------------------------------------------------------------------
// The interpreter's types, as the runtime environment names them
//
// Each method below is the whole of what re means by the interface of the same
// name.  They are one line each because the interfaces are deliberately empty:
// they exist so that a value can point at something the runtime environment
// does not understand.
// ---------------------------------------------------------------------------

// SchemeEnvValue marks *Env as re.SchemeEnv: the interface exists so that a closure can
// hold the environment it closed over without re naming *Env.
func (*Env) SchemeEnvValue() {}

// VmFrameValue marks *vmEnv as re's activation frame.
func (*vmEnv) VmFrameValue() {}

// SchemeMachineValue marks *Machine as re.SchemeMachine.
func (*Machine) SchemeMachineValue() {}

// CompiledBodyValue marks *Code as re.CompiledBody.
func (*Code) CompiledBodyValue() {}

// contState is the interpreter's captured continuation state: everything
// call/cc took a copy of.  re.Continuation holds one behind its
// ContinuationState interface, which this implements, so that the frame types
// stay in this package.
type contState struct {
	stack []frame
	winds []*windFrame
	hands []*handlerFrame
}

func (*contState) ContinuationStateValue() {}

// The interpreter's view of the opaque slots a value can hold.  re stores them
// as empty interfaces because it cannot name these types; these accessors are
// the one place that asserts them back, so no call site has to spell the
// assertion out and a wrong one fails here.

// envOf returns the environment a closure or continuation was created in.
func envOf(e SchemeEnv) *Env {
	if e == nil {
		return nil
	}
	return e.(*Env)
}

// vmOf returns the activation frame a compiled closure was created in.
func vmOf(v VmFrame) *vmEnv {
	if v == nil {
		return nil
	}
	return v.(*vmEnv)
}

// codeOf returns the bytecode of a compiled clause.
func codeOf(b CompiledBody) *Code {
	if b == nil {
		return nil
	}
	return b.(*Code)
}

// primitiveFn returns the Go function behind a primitive.
func primitiveFn(p *Primitive) func(*Machine, []Value) {
	if p.Fn == nil {
		return nil
	}
	return p.Fn.(func(*Machine, []Value))
}

// captureContinuation copies this machine's control state and returns the
// Scheme continuation object over it.  It is the one place that knows what a
// continuation's state is made of, so call/cc and the guard escape — which
// capture the same three stacks for different reasons — both come through here,
// and re never has to see a frame.
func (m *Machine) captureContinuation() *Continuation {
	return &Continuation{
		State: &contState{
			stack: append([]frame(nil), m.stack...),
			winds: append([]*windFrame(nil), m.winds...),
			hands: append([]*handlerFrame(nil), m.hands...),
		},
		Owner: m,
	}
}

// captureContinuationFrom is captureContinuation over stacks the caller
// already copied, which is what guard needs: it captured them before the body
// ran, so the escape has to reinstate those and not the current ones.
func (m *Machine) captureContinuationFrom(stack []frame, winds []*windFrame, hands []*handlerFrame) *Continuation {
	return &Continuation{
		State: &contState{
			stack: append([]frame(nil), stack...),
			winds: append([]*windFrame(nil), winds...),
			hands: append([]*handlerFrame(nil), hands...),
		},
		Owner: m,
	}
}

// kState returns the captured stacks of a continuation.  This package is the
// only thing that ever puts a contState into a Continuation, so the assertion
// cannot fail.
func kState(k *Continuation) *contState { return k.State.(*contState) }

// ---------------------------------------------------------------------------
// The hooks re calls back into
//
// A dot-imported package cannot add methods to this package's types and cannot
// write a literal that names them, so the constructors whose result carries an
// interpreter-owned value are installed from here.  Everything else about these
// values is in re.
// ---------------------------------------------------------------------------

func init() {
	ClosureEnv = func(c *Closure) SchemeEnv { return c.Env.(*Env) }
	ClosureVm = func(c *Closure) VmFrame { return c.Vm }
}

// ---------------------------------------------------------------------------
// FFI availability
// ---------------------------------------------------------------------------

// ffiAvailable reports whether the (goscheme ffi) procedures can load shared
// libraries; the cgo and non-cgo builds set it.
var ffiAvailable bool

var _ = sync.Mutex{}
