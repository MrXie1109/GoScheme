// SPDX-License-Identifier: MIT

package re

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Value is the interface implemented by every Scheme object.  It is declared
// as an empty interface on purpose: the interpreter uses a closed set of Go
// types, which keeps the dispatch in the machine fast and the code readable.
type Value interface{}

// ---------------------------------------------------------------------------
// Booleans, characters, the empty list, eof, unspecified
// ---------------------------------------------------------------------------

// Boolean is the Scheme boolean type.
type Boolean bool

// True and False are the two canonical boolean objects.
const (
	True  Boolean = true
	False Boolean = false
)

// BooleanOf converts a Go bool into a Scheme boolean.
func BooleanOf(b bool) Boolean {
	if b {
		return True
	}
	return False
}

// Char is a Scheme character (a Unicode scalar value).
type Char rune

// Empty is the unique empty list object.
type Empty struct{}

// Nil is the canonical empty list.
var Nil = Empty{}

// EOF is the unique end-of-file object.
type EOF struct{}

// EOFObject is the canonical eof object.
var EOFObject = EOF{}

// Unspecified is the value returned by side-effecting procedures; the R7RS
// report does not specify the value, we print it as #!unspecified.
type Unspecified struct{}

// UnspecifiedValue is the canonical unspecified value.
var UnspecifiedValue = Unspecified{}

// ---------------------------------------------------------------------------
// Symbols
// ---------------------------------------------------------------------------

// Symbol is an interned identifier.  A symbol with Mark 0 is an ordinary
// source symbol; symbols created by the macro expander carry a non-zero mark
// and know the environment in which they were introduced, which is what makes
// syntax-rules hygienic.
//
// The environment a mark was created in belongs to the interpreter, not to the
// runtime environment, so it is not a field here: the interpreter keeps it in
// its own table, keyed by the mark (see mark.go in internal/scheme).  What the
// runtime environment needs is the identity of the identifier, and that is the
// name and the mark.
type Symbol struct {
	Name string
	Mark uint64
	// orig is the identifier this one was created from by the macro
	// expander; it is the fallback identity used for hygiene resolution so
	// that marks compose correctly.
	orig *Symbol
}

var (
	symMu    sync.Mutex
	symTable = map[string]*Symbol{}
	markMu   sync.Mutex
	markSeq  uint64
)

// Mark is the guard over the mark sequence, and NextMark is the counter the
// interpreter's hygiene table draws from.  They are exposed because the table
// itself belongs to the interpreter — a mark records the environment it was
// created in, and that is an *Env — while the sequence has to be the one
// FreshSymbol above also uses, so that a temporary and a template rename can
// never collide.  Lock Mark around the read and the increment of NextMark.
var (
	Mark     = &markMu
	NextMark = &markSeq
)

// Intern returns the unique unmarked symbol with the given name.
func Intern(name string) *Symbol {
	symMu.Lock()
	defer symMu.Unlock()
	if s, ok := symTable[name]; ok {
		return s
	}
	s := &Symbol{Name: name}
	symTable[name] = s
	return s
}

// Base returns the unmarked symbol with the same name.
func (s *Symbol) Base() *Symbol { return Intern(s.Name) }

// IsMarked reports whether the symbol was introduced by the macro expander.
func (s *Symbol) IsMarked() bool { return s.Mark != 0 }

// Orig returns the identifier this one was created from by the macro expander,
// or nil for an ordinary source symbol.  The interpreter asks for it when it
// resolves a marked identifier through the environment the mark was created in.
func (s *Symbol) Orig() *Symbol { return s.orig }

// NewMarkedSymbol builds the marked counterpart of sym under the given mark.
// The interpreter's hygiene table is what decides which mark a template
// identifier gets and what environment it resolves in, so this only allocates:
// it copies the name, records the origin, and leaves the environment to the
// caller (see renameSymbol in internal/scheme).
func NewMarkedSymbol(sym *Symbol, mark uint64) *Symbol {
	return &Symbol{Name: sym.Name, Mark: mark, orig: sym}
}

// FreshSymbol returns a symbol that is guaranteed not to collide with any
// source identifier (used for the temporaries introduced by derived forms).
func FreshSymbol(hint string) *Symbol {
	markMu.Lock()
	defer markMu.Unlock()
	markSeq++
	s := &Symbol{Name: hint + ".g" + strconv.FormatUint(markSeq, 10), Mark: markSeq}
	return s
}

// ---------------------------------------------------------------------------
// Pairs and lists
// ---------------------------------------------------------------------------

// Pair is a Scheme cons cell.
type Pair struct {
	Car Value
	Cdr Value
}

// Cons builds a new pair.
func Cons(car, cdr Value) *Pair { return &Pair{Car: car, Cdr: cdr} }

// List builds a proper list from vals.
func List(vals ...Value) Value {
	var res Value = Nil
	for i := len(vals) - 1; i >= 0; i-- {
		res = Cons(vals[i], res)
	}
	return res
}

// ListToSlice converts a proper list to a Go slice.  An improper (dotted)
// list returns the leading elements and ok == false; a non-list returns
// ok == false with a nil slice.
func ListToSlice(v Value) (out []Value, ok bool) {
	for {
		switch x := v.(type) {
		case Empty:
			return out, true
		case *Pair:
			out = append(out, x.Car)
			v = x.Cdr
		default:
			return out, false
		}
	}
}

// ListLength returns the length of a proper list, or -1 when v is not a
// proper list.  It tolerates cyclic lists via Brent's algorithm.
func ListLength(v Value) int {
	slow, fast := v, v
	n := 0
	for {
		p, ok := fast.(*Pair)
		if !ok {
			if _, isNil := fast.(Empty); isNil {
				return n
			}
			return -1
		}
		fast = p.Cdr
		n++
		if _, isNil := fast.(Empty); isNil {
			return n
		}
		f2, ok := fast.(*Pair)
		if !ok {
			return -1
		}
		fast = f2.Cdr
		n++
		slow = slow.(*Pair).Cdr
		if slow == fast {
			return -1
		}
	}
}

// ---------------------------------------------------------------------------
// Strings, vectors, bytevectors
// ---------------------------------------------------------------------------

// String is a mutable Scheme string stored as a slice of runes.
type String struct {
	Runes []rune
}

// NewString builds a string from a Go string.
func NewString(s string) *String { return &String{Runes: []rune(s)} }

// NewStringFromRunes builds a string from runes (the slice is copied).
func NewStringFromRunes(r []rune) *String {
	cp := make([]rune, len(r))
	copy(cp, r)
	return &String{Runes: cp}
}

// Value returns the Go string representation.
func (s *String) Value() string { return string(s.Runes) }

// Len returns the number of characters.
func (s *String) Len() int { return len(s.Runes) }

// String implements fmt.Stringer.
func (s *String) String() string { return string(s.Runes) }

// Vector is a Scheme vector.
type Vector struct {
	Items []Value
}

// NewVector builds a vector of the given length.
func NewVector(n int) *Vector { return &Vector{Items: make([]Value, n)} }

// NewVectorFrom builds a vector from a slice.
func NewVectorFrom(items []Value) *Vector {
	cp := make([]Value, len(items))
	copy(cp, items)
	return &Vector{Items: cp}
}

// Bytevector is a Scheme bytevector.
type Bytevector struct {
	Bytes []byte
}

// NewBytevector builds a bytevector of the given length.
func NewBytevector(n int) *Bytevector { return &Bytevector{Bytes: make([]byte, n)} }

// NewBytevectorFrom builds a bytevector from a Go byte slice.
func NewBytevectorFrom(b []byte) *Bytevector {
	cp := make([]byte, len(b))
	copy(cp, b)
	return &Bytevector{Bytes: cp}
}

// ---------------------------------------------------------------------------
// Procedures
// ---------------------------------------------------------------------------

// SchemeEnv is the interpreter's lexical environment, as the runtime
// environment sees it.  Nothing in this package looks inside one — the
// bindings are the machine's — but a value has to be able to hold one, and
// naming it as an interface is how this package can do that without importing
// the package that defines it.  *scheme.Env implements it.
//
// The interface is called SchemeEnv rather than Env because the interpreter's
// concrete type is an Env, and this package is dot-imported there: two names
// would collide, and a struct that only ever holds one is not worth a
// qualified reference at every mention.
type SchemeEnv interface {
	// SchemeEnvValue marks the interpreter's environment type.  It is exported
	// because an interface with an unexported method cannot be satisfied from
	// another package, and the whole point of this one is that it is satisfied
	// from there.  It buys a compiler check that the value in a closure's Env
	// field really is an environment, which an empty interface would not.
	SchemeEnvValue()
}

// VmFrame is the interpreter's bytecode activation frame, as the runtime
// environment sees it.  It is an empty interface on purpose: the frame is
// *scheme.vmEnv, this package never looks inside one, and a marker method would
// only be a name for "some interpreter type" that the compiler could not check
// against anything anyway.  The machine asserts it back where it uses it.
type VmFrame interface{}

// Closure is a compound procedure produced by lambda / case-lambda.
type Closure struct {
	// Clauses holds one entry per case-lambda clause; a plain lambda has a
	// single clause with a nil Params.
	Clauses []ClosureClause
	Name    string
	// Env is the lexical environment the closure was created in, and Vm is
	// the frame its body was compiled in when it has been compiled (see
	// vm.go).  Both are the machine's, and are held here as the opaque
	// interfaces above: the machine stores and reads them through
	// ClosureInit, which is the one place that knows what the two fields
	// really are.  A side table keyed by the closure would keep the fields out
	// of this struct entirely, but closures are made in the interpreter's
	// inner loops and a map lookup on every call is not worth the tidiness.
	Env SchemeEnv
	Vm  VmFrame
}

// ClosureInit is how the interpreter reads back the two interpreter-owned
// fields of a Closure.  A dot-imported package cannot add methods to another
// package's types, so the interpreter supplies these at init time (see
// value.go there) and this package only calls them.
var (
	// ClosureEnv returns the environment a closure was created in.
	ClosureEnv func(c *Closure) SchemeEnv
	// ClosureVm returns the compiled frame a closure was created in, or nil.
	ClosureVm func(c *Closure) VmFrame
)

// CompiledBody is the bytecode of one compiled clause (an *scheme.Code).  It is
// an empty interface for the same reason VmFrame is: the instruction set is the
// interpreter's, nothing in this package executes or even looks at it, and the
// field exists only so that the machine can hang a compiled body on the clause
// it belongs to.  The machine asserts it back where it runs it.
type CompiledBody interface{}

// ClosureClause is one arity case of a procedure.
type ClosureClause struct {
	Params  []*Symbol
	Rest    *Symbol // nil when the parameter list is proper
	HasRest bool
	Body    []Value
	// BodyNames lists the identifiers introduced by internal definitions so
	// that they can be pre-bound (letrec* semantics).
	BodyNames []*Symbol
	// Code, when not nil, is the compiled body: applying this clause runs it
	// on the bytecode VM instead of walking Body.  It is the machine's
	// bytecode, so it is held as the opaque type above and read back through
	// ClauseCode, which the interpreter installs.
	Code CompiledBody
	// Native, when not nil, is machine code the compiler produced for this
	// clause.  It is tried first, and a call it declines falls back to
	// interpreting Body, which is still there and still correct.
	//
	// The field is an interface rather than a function pointer so that the
	// interpreter, which is what makes the call, does not have to know how a
	// compiled procedure is represented.  The runtime environment installs it
	// (see re/).
	Native NativeProc
}

// NativeProc is a procedure the compiler turned into machine code, as the
// interpreter sees it.
//
// It is deliberately Scheme values in and out.  The compiled code's own
// representation — tagged words in an argument array — is the runtime
// environment's business, and nothing above this interface needs to know about
// it, which is what keeps the interpreter free of the ABI.
type NativeProc interface {
	// Call runs the compiled body.  ok is false when the call cannot be taken
	// natively, which is what makes a fallback to the interpreted body
	// expressible rather than an error.
	Call(args []Value) (Value, bool)
}

// Primitive is a procedure implemented in Go.
type Primitive struct {
	Name string
	// Fn runs the primitive.  It either sets the machine's control to the
	// returned value (m.Return) or pushes continuation frames of its own.
	//
	// Its type is the interpreter's — func(*scheme.Machine, []Value) — and is
	// held here as an empty interface because this package cannot name it.  It
	// is never called through this package: the two places that apply a
	// primitive (machine.go and vm.go) assert it back to the real type, so the
	// assertion is written once, next to the call, rather than here.  Any other
	// value in this field is a bug in the interpreter that stored it.
	Fn interface{}
	// MinArgs / MaxArgs document the arity; MaxArgs < 0 means unbounded.
	MinArgs int
	MaxArgs int
	// Sync marks a primitive that always finishes within the call: it either
	// returns a value or raises, and never pushes a continuation frame,
	// invokes a continuation or suspends on a call back into Scheme.  That is
	// what lets the VM run it without having built a continuation frame for
	// the caller first.  defSimple's wrapper has exactly this shape — it has
	// no machine to do anything else with — so defSimple sets it.
	Sync bool
}

// SchemeMachine is the interpreter's abstract machine, as the runtime
// environment sees it: as the thing a primitive is handed.  The type itself
// belongs to the interpreter, so it is named here as an interface that only the
// interpreter's machine can satisfy — *scheme.Machine implements it.  A
// primitive's body is written against the real type and reaches this package
// through NewPrimitiveFn, which is what puts an *scheme.Machine into it.
//
// Like SchemeEnv, the name is qualified because the interpreter has a Machine
// of its own and this package is dot-imported there.
type SchemeMachine interface{}

// StateOf returns a continuation's captured state, as the machine stored it.
// It is the interpreter's own contState; the assertion is the machine's job, so
// this returns the interface and the caller does the type switch.
func (k *Continuation) StateOf() ContinuationState { return k.State }

// Continuation is a first class continuation captured by call/cc.
//
// Its body — the frame stack, the dynamic-wind stack and the exception handler
// stack — is the machine's, and it is all one object to everyone else, so the
// interpreter keeps it behind a single opaque pointer here.  A continuation is
// only ever made, jumped to and discarded by the interpreter; nothing in this
// package has reason to look inside one, and putting the machine's frame types
// in this file would be the dependency the split exists to avoid.
type Continuation struct {
	Name string
	// State is the interpreter's captured state: an *scheme.contState, which
	// the machine allocates and reads back through its own constructors (see
	// captureContinuation in value.go there).
	State ContinuationState
	// Owner is the interpreter thread the continuation belongs to; jumping
	// between threads is not supported.  It is the machine as this package
	// sees it (see SchemeMachine).  It is exported because the interpreter is
	// what fills it in as well as what reads it.
	Owner SchemeMachine
}

// ContinuationState is the interpreter's captured continuation state, as this
// package sees it: opaque, for the same reason VmFrame is.  The machine owns
// the value and is the only thing that ever reads it back.
type ContinuationState interface{}

// Parameter is a parameter object as created by make-parameter.
type Parameter struct {
	Name      string
	Converter Value // #f for the identity converter
	IsPort    bool

	mu     sync.RWMutex
	values []Value
}

// Current, Set, Push and Pop are the parameter's own operations, in the
// interpreter's spelling: a parameter is runtime data with a value stack, and
// parameterize is the machine's, so the machine reaches in through these rather
// than through the fields.
func (p *Parameter) Current() Value { return p.current() }

// Set replaces the value in force.
func (p *Parameter) Set(v Value) { p.set(v) }

// Push adds a value to the top of the stack (parameterize).
func (p *Parameter) Push(v Value) { p.push(v) }

// Pop removes the top value (leaving parameterize).
func (p *Parameter) Pop() { p.pop() }

// Values returns the parameter's value stack, lowest first.  It is read-only
// use: the interpreter inspects it when it copies a machine's parameter state.
func (p *Parameter) Values() []Value {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.values
}

// NewParameter builds a parameter with an initial value stack.  The
// interpreter's make-parameter uses it so that the fields stay this package's.
func NewParameter(name string, converter Value, isPort bool, vals ...Value) *Parameter {
	return &Parameter{Name: name, Converter: converter, IsPort: isPort, values: vals}
}

// Reset replaces the whole value stack, which is what make-parameter does once
// its converter has run.
func (p *Parameter) Reset(vals ...Value) {
	p.mu.Lock()
	p.values = vals
	p.mu.Unlock()
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

// Promise is the object produced by delay / delay-force / make-promise.
type Promise struct {
	Done  bool
	Value Value
	Thunk Value // a procedure of no arguments
	// If IsDelayForce, forcing the thunk yields another promise which is
	// chained iteratively (see R7RS 4.2.5).
	IsDelayForce bool
	Forcing      bool
}

// ---------------------------------------------------------------------------
// Records
// ---------------------------------------------------------------------------

// RecordType is the descriptor created by define-record-type.
type RecordType struct {
	Name    string
	Fields  []*Symbol
	Mutable []bool
}

// Record is an instance of a record type.
type Record struct {
	Type   *RecordType
	Fields []Value
}

// RecordTypeDescriptor is the object bound by define-record-type.
type RecordTypeDescriptor struct {
	Type *RecordType
}

// ffiAvailable reports whether the (goscheme ffi) procedures can load shared
// libraries; the cgo and non-cgo builds set it.
var ffiAvailable bool

// ---------------------------------------------------------------------------
// Errors and conditions
// ---------------------------------------------------------------------------

// ErrorObject is a Scheme condition object as produced by `error` and the
// file/read errors of R7RS.
type ErrorObject struct {
	Message   string
	Irritants []Value
	Kind      errorKind
	// Incomplete marks input that ended in the middle of a datum.  A REPL
	// uses it to decide whether to read another line instead of reporting a
	// syntax error.
	Incomplete bool
}

type errorKind int

const (
	errUser errorKind = iota
	errFile
	errRead
)

// IsReadError reports whether the condition came from the reader, which is what
// read-error? asks.
func (e *ErrorObject) IsReadError() bool { return e.Kind == errRead }

// IsFileError reports whether the condition came from the file system, which is
// what file-error? asks.
func (e *ErrorObject) IsFileError() bool { return e.Kind == errFile }

// Error implements the Go error interface so that ErrorObject can travel
// through the Go call stack when the interpreter unwinds.
func (e *ErrorObject) Error() string {
	var b strings.Builder
	b.WriteString(e.Message)
	for _, ir := range e.Irritants {
		b.WriteString(" ")
		b.WriteString(WriteToString(ir))
	}
	return b.String()
}

// SchemeError is a Go error carrying an arbitrary Scheme condition.
type SchemeError struct {
	Condition Value
	// Continuable is set when raised by raise-continuable.
	Continuable bool
}

func (e *SchemeError) Error() string {
	if eo, ok := e.Condition.(*ErrorObject); ok {
		return eo.Error()
	}
	return WriteToString(e.Condition)
}

// NewError builds an ErrorObject.
func NewError(msg string, irritants ...Value) *ErrorObject {
	return &ErrorObject{Message: msg, Irritants: irritants}
}

// NewFileError builds a file error condition.
func NewFileError(msg string, irritants ...Value) *ErrorObject {
	return &ErrorObject{Message: msg, Irritants: irritants, Kind: errFile}
}

// NewReadError builds a read error condition.
func NewReadError(msg string, irritants ...Value) *ErrorObject {
	return &ErrorObject{Message: msg, Irritants: irritants, Kind: errRead}
}

// NewIncompleteError builds a read error that says the input stopped in the
// middle of a datum.
func NewIncompleteError(msg string) *ErrorObject {
	return &ErrorObject{Message: msg, Kind: errRead, Incomplete: true}
}

// IsIncomplete reports whether err means "the input ended in the middle of a
// datum", that is, whether more input would complete it.
func IsIncomplete(err error) bool {
	switch e := err.(type) {
	case *ErrorObject:
		return e.Incomplete
	case *SchemeError:
		if eo, ok := e.Condition.(*ErrorObject); ok {
			return eo.Incomplete
		}
	}
	return false
}

// WrongType builds the condition for an argument of the wrong type.
func WrongType(want string, got Value) *ErrorObject { return wrongType(want, got) }

func wrongType(want string, got Value) *ErrorObject {
	return NewError(fmt.Sprintf("expected %s but got %s", want, typeName(got)), got)
}

// WrongTypeName is WrongType with the procedure's name prefixed, which is what
// the interpreter's primitives raise.
func WrongTypeName(want string, got Value, name string) *ErrorObject {
	return wrongTypeName(want, got, name)
}

func wrongTypeName(want string, got Value, name string) *ErrorObject {
	return NewError(fmt.Sprintf("%s: expected %s but got %s", name, want, typeName(got)), got)
}

// TypeName returns a human readable name for the type of v.  It is what
// "expected X but got Y" is built from, so the interpreter names types too.
func TypeName(v Value) string { return typeName(v) }

// typeName returns a human readable name for the type of v.
func typeName(v Value) string {
	switch v.(type) {
	case Boolean:
		return "boolean"
	case *Symbol:
		return "symbol"
	case Char:
		return "character"
	case *String:
		return "string"
	case *Pair, Empty:
		return "list"
	case *Vector:
		return "vector"
	case *Bytevector:
		return "bytevector"
	case *Integer, *Rational, Float, *Complex:
		return "number"
	case *Closure, *Primitive, *Continuation, *Parameter:
		return "procedure"
	case *Port:
		return "port"
	case *Promise:
		return "promise"
	case *Record:
		return "record"
	case EOF:
		return "eof-object"
	case Unspecified:
		return "unspecified"
	case *ErrorObject:
		return "error-object"
	case SchemeEnv:
		return "environment"
	}
	return fmt.Sprintf("%T", v)
}

// ---------------------------------------------------------------------------
// Small helpers used across the interpreter
// ---------------------------------------------------------------------------

// IsTrue implements Scheme truthiness: only #f is false.
func IsTrue(v Value) bool {
	b, ok := v.(Boolean)
	return !ok || bool(b)
}

// IsFalse reports whether v is #f.
func IsFalse(v Value) bool {
	b, ok := v.(Boolean)
	return ok && !bool(b)
}

// IsList reports whether v is a proper list.
func IsList(v Value) bool { return ListLength(v) >= 0 }

// Car and Cdr are the pair accessors the interpreter uses in its own code; the
// tower and the printer use the unexported spellings below.
func Car(v Value) Value { return car(v) }
func Cdr(v Value) Value { return cdr(v) }

// Cadr, Cddr and Caddr are the two- and three-deep accessors.
func Cadr(v Value) Value  { return cadr(v) }
func Cddr(v Value) Value  { return cddr(v) }
func Caddr(v Value) Value { return caddr(v) }

// Caar … style helpers used by the reader and the printer.
func car(v Value) Value {
	if p, ok := v.(*Pair); ok {
		return p.Car
	}
	return nil
}

func cdr(v Value) Value {
	if p, ok := v.(*Pair); ok {
		return p.Cdr
	}
	return nil
}

func cadr(v Value) Value  { return car(cdr(v)) }
func cddr(v Value) Value  { return cdr(cdr(v)) }
func caddr(v Value) Value { return car(cddr(v)) }
