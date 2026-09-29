// SPDX-License-Identifier: MIT

// Package scheme implements an interpreter for the Scheme programming
// language as specified by the R7RS report (small language).
//
// The interpreter is organised as follows:
//
//	value.go     – the runtime representation of Scheme data
//	number.go    – the full numeric tower (exact/inexact, rational, complex)
//	reader.go    – lexer and datum reader
//	printer.go   – write / display
//	env.go       – environments and syntactic environments
//	machine.go   – the CEK abstract machine (evaluation core)
//	eval.go      – special forms
//	macro.go     – syntax-rules hygienic pattern matcher
//	library.go   – R7RS library system (define-library / import)
//	port.go      – textual and binary ports
//	builtin*.go  – the standard procedures
package scheme

import (
	"fmt"
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
type Symbol struct {
	Name string
	Mark uint64
	env  *Env
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
	markMu.Lock()
	defer markMu.Unlock()
	markSeq++
	markTable[markSeq] = &markInfo{env: env, syms: map[*Symbol]*Symbol{}}
	return markSeq
}

// markEnvOf returns the environment attached to a mark (nil when unknown).
func markEnvOf(mark uint64) *Env {
	markMu.Lock()
	defer markMu.Unlock()
	if mi, ok := markTable[mark]; ok {
		return mi.env
	}
	return nil
}

// renameSymbol builds (and caches) the marked counterpart of sym.
func renameSymbol(sym *Symbol, mark uint64) *Symbol {
	markMu.Lock()
	defer markMu.Unlock()
	mi, ok := markTable[mark]
	if !ok {
		mi = &markInfo{syms: map[*Symbol]*Symbol{}}
		markTable[mark] = mi
	}
	if s, ok := mi.syms[sym]; ok {
		return s
	}
	s := &Symbol{Name: sym.Name, Mark: mark, env: mi.env, orig: sym}
	mi.syms[sym] = s
	return s
}

// FreshSymbol returns a symbol that is guaranteed not to collide with any
// source identifier (used for the temporaries introduced by derived forms).
func FreshSymbol(hint string) *Symbol {
	markMu.Lock()
	defer markMu.Unlock()
	markSeq++
	s := &Symbol{Name: hint + ".g" + itoa(markSeq), Mark: markSeq}
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

// Closure is a compound procedure produced by lambda / case-lambda.
type Closure struct {
	// Clauses holds one entry per case-lambda clause; a plain lambda has a
	// single clause with a nil Params.
	Clauses []ClosureClause
	Name    string
	Env     *Env
}

// ClosureClause is one arity case of a procedure.
type ClosureClause struct {
	Params  []*Symbol
	Rest    *Symbol // nil when the parameter list is proper
	HasRest bool
	Body    []Value
	// BodyNames lists the identifiers introduced by internal definitions so
	// that they can be pre-bound (letrec* semantics).
	BodyNames []*Symbol
}

// Primitive is a procedure implemented in Go.
type Primitive struct {
	Name string
	// Fn runs the primitive.  It either sets the machine's control to the
	// returned value (m.Return) or pushes continuation frames of its own.
	Fn func(m *Machine, args []Value)
	// MinArgs / MaxArgs document the arity; MaxArgs < 0 means unbounded.
	MinArgs int
	MaxArgs int
}

// Continuation is a first class continuation captured by call/cc.
type Continuation struct {
	Name  string
	stack []frame
	winds []*windFrame
	hands []*handlerFrame
	// owner is the interpreter thread the continuation belongs to; jumping
	// between threads is not supported.
	owner *Machine
}

// Parameter is a parameter object as created by make-parameter.
type Parameter struct {
	Name      string
	Converter Value // #f for the identity converter
	IsPort    bool

	mu     sync.RWMutex
	values []Value
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

func wrongType(want string, got Value) *ErrorObject {
	return NewError(fmt.Sprintf("expected %s but got %s", want, typeName(got)), got)
}

func wrongTypeName(want string, got Value, name string) *ErrorObject {
	return NewError(fmt.Sprintf("%s: expected %s but got %s", name, want, typeName(got)), got)
}

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
	case *Env:
		return "environment"
	case *Hashtable:
		return "hashtable"
	case *Channel:
		return "channel"
	}
	return fmt.Sprintf("%T", v)
}

// ---------------------------------------------------------------------------
// Hashtables (extension; not part of R7RS-small but widely used)
// ---------------------------------------------------------------------------

// Channel is a Go channel exposed to Scheme; see b_concurrent.go.
type Channel struct {
	Name     string
	capacity int
	ch       chan Value
	mu       sync.Mutex
	closed   bool
}

// Hashtable is a hash table keyed by Scheme values.  It is an extension:
// see b_hashtable.go.
type Hashtable struct {
	// Kind is "eq", "eqv" or "equal" and selects the key equivalence.
	Kind    string
	Mutable bool
	keys    []Value
	vals    []Value
	dead    []bool
	index   map[interface{}][]int
	count   int
}

// ---------------------------------------------------------------------------
// Small helpers used across the interpreter
// ---------------------------------------------------------------------------

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

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
func cdddr(v Value) Value { return cdr(cddr(v)) }
