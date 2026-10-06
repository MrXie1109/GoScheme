// SPDX-License-Identifier: MIT

package main

/*
#include <stdlib.h>
#include <stdint.h>

// gs_val is how a Scheme value crosses the boundary between generated code and
// the runtime: a machine word, and a tag saying what the word means.
//
// It is a struct rather than two arguments because it is one value, and because
// LLVM passes and returns a two-word struct in registers on every platform this
// targets — the generated code writes `{ i64, i64 }` and cgo agrees on the
// layout, so neither side has to say anything more.
typedef struct { int64_t bits; int64_t tag; } gs_val;

// Tags.  A fixnum's bits are the number; a handle's bits index the runtime's
// value table, which is where a value that does not fit a machine word lives.
#define GS_FIXNUM 0
#define GS_HANDLE 1
#define GS_BOOLEAN 2
#define GS_NULL 3
#define GS_UNSPECIFIED 4

// The operations gs_arith knows, matching arithCode in the generator.
#define GS_ADD 0
#define GS_SUB 1
#define GS_MUL 2

// A native body, as the runtime sees it: a count, the tagged arguments, and a
// tagged result.
//
// Every compiled procedure has this signature whatever its arity, which is what
// lets one function-pointer type stand for all of them: the generated code
// bitcasts its own definition to this shape when it registers it.
typedef gs_val (*gs_native_fn)(int64_t, gs_val*);

// gs_call_native calls a native body.
//
// It is a C shim because cgo will not call a C function pointer directly: the
// value that arrives from the generated code is plain data to Go, and the call
// has to be made where a call is a call.  Keeping it here also pins the
// signature in one place, which is what the generated code's bitcast has to
// agree with.
static inline gs_val gs_call_native(gs_native_fn fn, int64_t n, gs_val *args) {
	return fn(n, args);
}

// gs_call_addr is gs_call_native for a code pointer that arrived as an integer.
//
// A closure's code pointer is data on the Go side — it came out of the generated
// code, which has no type for "function" that cgo would accept — so turning it
// back into something callable has to happen where a cast to a function pointer
// is legal.  Keeping it beside gs_call_native pins both spellings of the call in
// one place, which is what the generated code's bitcast has to agree with.
static inline gs_val gs_call_addr(uintptr_t addr, int64_t n, gs_val *args) {
	return ((gs_native_fn)addr)(n, args);
}
*/
import "C"

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/MrXie1109/GoScheme/internal/scheme"

	"github.com/MrXie1109/GoScheme/internal/re"
)

// unsafeAdd moves a pointer by n elements, which is how the argv array is
// walked: cgo hands over **C.char and its elements are not addressable from Go.
func unsafeAdd(p **C.char, n uintptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(unsafe.Pointer(p)) + n*unsafe.Sizeof(uintptr(0)))
}

// The C ABI of the runtime.
//
// The surface is small and has two halves.  One half is how a compiled program
// runs Scheme that was not compiled: it hands the runtime the **source of a
// form** and gets back a handle, and everything a form can do is the
// interpreter's business.  The other half is how the runtime and the machine
// code hand values back and forth: a tagged word in each direction, which is
// what lets native arithmetic stay in registers while a bignum still exists.
//
// Handles rather than pointers throughout: a Scheme value is a Go interface
// value and cannot be a C pointer, so the runtime keeps a table and hands out an
// index.  The table is append-only within a run, which is what makes a handle
// stable.
//
// What is deliberately *not* here is the reverse of the first half — no
// cons/car/cdr/closure across the boundary.  Exposing the data representation
// would mean the generated code implementing the language, which is the
// compiler this is not.

var (
	mu      sync.Mutex
	handles []re.Value
	mach    *scheme.Machine
	native  int
	// natives maps a procedure name to its compiled body, which is what makes
	// the machine code reachable: the interpreter looks a name up here before
	// it walks a closure.
	natives = map[string]nativesEntry{}
)

// nativesEntry is one registered native procedure.
type nativesEntry struct {
	fn    C.gs_native_fn
	arity int
}

func store(v re.Value) int64 {
	mu.Lock()
	defer mu.Unlock()
	handles = append(handles, v)
	return int64(len(handles) - 1)
}

func load(h int64) re.Value {
	mu.Lock()
	defer mu.Unlock()
	if h < 0 || int(h) >= len(handles) {
		return re.UnspecifiedValue
	}
	return handles[h]
}

// fixnum is a tagged value holding a machine integer.
func fixnum(n int64) C.gs_val { return C.gs_val{bits: C.int64_t(n), tag: C.GS_FIXNUM} }

// handle is a tagged value holding an index into the value table.
func handle(i int64) C.gs_val { return C.gs_val{bits: C.int64_t(i), tag: C.GS_HANDLE} }

// boolean is a tagged value holding #t or #f.
func boolean(b bool) C.gs_val {
	if b {
		return C.gs_val{bits: 1, tag: C.GS_BOOLEAN}
	}
	return C.gs_val{bits: 0, tag: C.GS_BOOLEAN}
}

// tagged boxes a Scheme value: a machine integer travels as itself, a boolean
// as its own tag, and anything else — a bignum, a rational, a non-number —
// travels as a handle.
//
// This is the whole reason the tag exists.  A native body computes in a machine
// word, so a value that is not one machine word has to be named rather than
// carried; naming it costs a table entry and loses nothing.
//
// A boolean gets a tag of its own rather than travelling as 0 or 1 because the
// two are different values: `(= 1 1)` prints as `#t` and 1 prints as `1`, and a
// comparison result that arrived as a fixnum would print the wrong one.
func tagged(v re.Value) C.gs_val {
	if n, ok := v.(*re.Integer); ok {
		if small, fits := n.Int64(); fits {
			return fixnum(small)
		}
	}
	if b, ok := v.(re.Boolean); ok {
		return boolean(bool(b))
	}
	if v == re.Value(re.Nil) {
		return C.gs_val{bits: 0, tag: C.GS_NULL}
	}
	if _, ok := v.(re.Unspecified); ok {
		return C.gs_val{bits: 0, tag: C.GS_UNSPECIFIED}
	}
	return handle(store(v))
}

// untagged recovers the Scheme value a tagged word names.
func untagged(v C.gs_val) re.Value {
	switch int64(v.tag) {
	case int64(C.GS_HANDLE):
		return load(int64(v.bits))
	case int64(C.GS_BOOLEAN):
		return re.BooleanOf(int64(v.bits) != 0)
	case int64(C.GS_NULL):
		return re.Nil
	case int64(C.GS_UNSPECIFIED):
		return re.UnspecifiedValue
	}
	return re.Value(re.Int(int64(v.bits)))
}

func goString(p *C.char, n int64) string {
	if p == nil || n <= 0 {
		return ""
	}
	return C.GoStringN(p, C.int(n))
}

//export gs_init
func gs_init(argc C.int, argv **C.char) {
	mach = scheme.NewMachine()
	args := make([]string, 0, int(argc))
	for i := 0; i < int(argc); i++ {
		args = append(args, C.GoString(*argv))
		argv = (**C.char)(unsafeAdd(argv, 1))
	}
	mach.Args = args
}

//export gs_finish
func gs_finish() {}

//export gs_eval_source
func gs_eval_source(src *C.char, n C.longlong, name *C.char) C.longlong {
	source := goString(src, int64(n))
	if mach == nil {
		gs_init(0, nil)
	}
	r := re.NewStringReader(source)
	if name != nil {
		r.Source = C.GoString(name)
	}
	forms, err := r.ReadAll()
	if err != nil {
		report(err)
		return -1
	}
	v, err := mach.RunFormsCompiled(forms, mach.Global)
	if err != nil {
		report(err)
		return -1
	}
	// A form may have defined a procedure whose compiled body was registered
	// before the program started, so this is the moment the two meet.
	attachNatives(mach)
	return C.longlong(store(v))
}

//export gs_note_native
func gs_note_native(label *C.char, n C.int) { native = int(n) }

// gs_box_literal builds the value a numeric literal too large for a machine word
// denotes, and returns its handle.
//
// A compiled body cannot construct a bignum: that would mean the generated code
// knowing how one is stored, and knowing it in a way that stays true as the
// runtime changes.  Handing the text to the runtime and getting a handle back
// keeps the representation on one side of the boundary.
//
//export gs_box_literal
func gs_box_literal(text *C.char, n C.int64_t) C.int64_t {
	src := goString(text, int64(n))
	if src == "" {
		return C.int64_t(store(re.UnspecifiedValue))
	}
	forms, err := re.NewStringReader(src).ReadAll()
	if err != nil || len(forms) != 1 {
		return C.int64_t(store(re.UnspecifiedValue))
	}
	return C.int64_t(store(forms[0]))
}

// gs_walk runs a whole list walk in one call.
//
// The generated code recognises the loop and asks for it here instead of
// crossing the boundary once per element.  kind is one of the walks, and the
// two arguments are the list and the starting accumulator.
//
// This is the entry point that makes a compiled list walk faster than the
// interpreter rather than slower: the loop runs where the data already is, and
// nothing crosses per element.
//
//export gs_walk
func gs_walk(kind C.int32_t, pred C.int32_t, list C.gs_val, acc C.gs_val) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	return tagged(scheme.RunListWalkPred(int(int32(kind)), int(int32(pred)), untagged(list), untagged(acc)))
}

// gs_vecwalk runs a whole vector walk in one call.
//
// The vector counterpart of gs_walk, and there for the same reason: a loop that
// indexes a vector element by element would cross the boundary per element.
//
// The arguments come through an array rather than as four tagged values.  Four
// struct arguments plus the kind exceed what the platform passes in registers,
// and the generated code and cgo then disagree about where the rest goes — the
// call returned the accumulator unchanged, which looks like a loop that ran
// zero times.  An array has one address whatever the arity, which is the same
// reason the compiled bodies take their arguments that way.
//
//export gs_vecwalk
func gs_vecwalk(kind C.int32_t, pred C.int32_t, args *C.gs_val) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	return tagged(scheme.RunVecWalkPred(int(int32(kind)), int(int32(pred)),
		untagged(*argsAt(args, 0)), untagged(*argsAt(args, 1)),
		untagged(*argsAt(args, 2)), untagged(*argsAt(args, 3))))
}

// gs_countloop runs a counting loop in one call.
//
// The shape `(define (build n acc) (if (= n 0) acc (build (- n 1) (cons n acc))))`
// — count down, fold the counter in — which is how make-list, iota and range
// are written.  There is no sequence to walk, so only the count and the
// accumulator are handed over.
//
//export gs_countloop
func gs_countloop(kind C.int32_t, args *C.gs_val, nextra C.int32_t, inclusive C.int32_t) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	n := untagged(*argsAt(args, 0))
	acc := untagged(*argsAt(args, 1))
	var extras []re.Value
	for i := 0; i < int(nextra); i++ {
		extras = append(extras, untagged(*argsAt(args, 2+i)))
	}
	return tagged(scheme.RunCountLoopFull(int(int32(kind)), n, acc, extras, int32(inclusive) != 0))
}

// gs_uploop runs a loop that counts up to a bound in one call.
//
// The shape a `do` loop is written in — an index from a lower bound to an upper
// one, folding as it goes — which is the mirror of gs_countloop and needs its
// own entry point because the bound is a value rather than zero.
//
//export gs_uploop
func gs_uploop(kind C.int32_t, args *C.gs_val) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	return tagged(scheme.RunUpLoop(int(int32(kind)),
		untagged(*argsAt(args, 0)), untagged(*argsAt(args, 1)), untagged(*argsAt(args, 2))))
}

// gs_search walks a list and returns the first element a test accepts.
//
// The search is the other thing a loop over a sequence does, and like the folds
// it runs in one call: nothing crosses per element.  The two answers travel in
// with the list because either can be anything the program wrote — a literal, a
// value from an enclosing scope — and the runtime cannot invent them.
//
//export gs_search
func gs_search(pred C.int32_t, args *C.gs_val, wantElement C.int32_t) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	return tagged(scheme.RunSearch(int(int32(pred)),
		untagged(*argsAt(args, 0)), untagged(*argsAt(args, 1)), untagged(*argsAt(args, 2)),
		int(int32(wantElement))))
}

// gs_merge merges two lists in one call.
//
// The core of every sort written in Scheme, and like the other walks it runs
// where the data is rather than crossing per element.  The comparison is a code
// rather than a procedure because a comparison written by the programmer would
// have to be called back into Scheme for every element.
//
//export gs_merge
func gs_merge(cmp C.int32_t, a C.gs_val, b C.gs_val) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	return tagged(scheme.RunMerge(int(int32(cmp)), untagged(a), untagged(b)))
}

// gs_build copies a list, maps a builtin over it, or filters it, in one call.
//
// The shape whose recursive call is *not* in tail position — `(cons (car a)
// (f (cdr a)))` — which is how append, copy-list, map and filter are written.
// A Scheme version recurses once per element and pays a frame for each; the
// loop here builds the result in one pass.
//
// mode says which of the three it is, because they are the same shape and mean
// different things: copy keeps every element, map keeps what the test returns,
// filter keeps the elements the test accepts.
//
//export gs_build
func gs_build(pred C.int32_t, args *C.gs_val, mode C.int32_t) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	return tagged(scheme.RunBuild(int(int32(pred)),
		untagged(*argsAt(args, 0)), untagged(*argsAt(args, 1)), int(int32(mode))))
}

// gs_global reads a top-level binding by name, as a tagged value.
//
// A compiled body reads a global where the name appears, not once at entry,
// because a global is mutable: anything the body calls may assign it, and a
// value cached at entry would be the value from before the call.
//
// An unbound name is reported through the runtime's own error path rather than
// here, so that it reads the same as it would interpreted — including the case
// where the name is defined by a later form, which the interpreter allows and a
// compiler that resolved names eagerly would not.
//
//export gs_global
func gs_global(name *C.char) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	n := C.GoString(name)
	v, ok := mach.Global.Lookup(re.Intern(n))
	if !ok {
		report(fmt.Errorf("%s: undefined", n))
		return handle(store(re.UnspecifiedValue))
	}
	return tagged(v)
}

// gs_set_global assigns a top-level binding by name.
//
// The counterpart of gs_global: a compiled body reads a global through the
// runtime and writes one through the runtime, for the same reason.  The global
// environment is the runtime's, the binding may not exist yet, and creating it
// is not something generated code can do.
//
// It returns nothing, because a `set!` evaluates to the unspecified value and
// the compiler emits that itself rather than passing it back across the
// boundary.
//
//export gs_set_global
func gs_set_global(name *C.char, n C.int64_t, v C.gs_val) {
	if mach == nil {
		gs_init(0, nil)
	}
	sym := re.Intern(goString(name, int64(n)))
	mach.Global.SetGlobal(sym, untagged(v))
}

// A compiled closure, as a Scheme procedure.
//
// When a compiled `lambda` captures a variable from an enclosing scope, the
// capture has to live somewhere the generated code can reach later, and that
// somewhere cannot be a Go pointer: cgo forbids handing one to C, and the
// collector moves it anyway.  So the captures live here, in Go, and the generated
// code holds a handle — the same arrangement every other crossing uses.
//
// **The value is a real re.Closure.** That is what makes it a procedure to
// everything else in the runtime: `procedure?` is true of it, `display` prints
// it, `map` accepts it, and the interpreter's own `apply` calls it without
// knowing a compiler was involved.  The native body is hung on the clause as a
// NativeProc, which is the same mechanism a top-level compiled procedure uses —
// so a compiled closure and an interpreted closure are the same kind of object
// with a different body, rather than a new kind of object everything has to
// learn about.
//
// An earlier version kept its own table with handles of its own, and the two
// namespaces collided: a closure handle was read back through the value table
// and returned whatever unrelated value was at that index.
type nativeClosure struct {
	// code is the C function pointer the generated code produced, kept as an
	// integer because that is how it crossed the boundary.  A Go field cannot
	// hold a function pointer cgo will call; the call is made in C, by
	// gs_call_addr.
	code uintptr
	// n is how many values were captured, which gs_closure_apply needs in order
	// to build the argument list.
	n int
	// captures are the captured values, in the order the emitting compiler
	// chose.  The names are gone by then: the closure body refers to them by
	// position, which is what makes reading a capture free of a symbol lookup.
	captures []re.Value
	// arity is how many arguments the procedure takes, which is what the
	// interpreter checks a call against when it applies this closure itself.
	arity int
	// handle is this closure's own index in the value table, which the body needs
	// in order to assign a capture: assigning the parameter it was given would
	// only change that call's copy.
	handle int64
	// needsSelf is whether the body assigns a capture at all, and so wants the
	// handle passed to it.
	needsSelf bool
}

// Call runs the compiled closure with Scheme values, which is what makes it a
// NativeProc and lets the interpreter apply it.
//
// The arguments are tagged into an array for the generated body, and the
// captures are appended — the same order gs_closure_apply builds.  The two
// paths have to agree, and they do so by both calling callCompiled.
func (c *nativeClosure) Call(args []re.Value) (re.Value, bool) {
	if len(args) != c.arity {
		return nil, false // the interpreter reports the arity error
	}
	total := len(args) + c.n
	taggedArgs := make([]C.gs_val, total)
	for i, a := range args {
		taggedArgs[i] = tagged(a)
	}
	for i, cap := range c.captures {
		taggedArgs[len(args)+i] = tagged(cap)
	}
	if c.needsSelf {
		taggedArgs = append(taggedArgs, handle(c.handle))
	}
	out := C.gs_call_addr(C.uintptr_t(c.code), C.int64_t(len(taggedArgs)), &taggedArgs[0])
	return untagged(out), true
}

//export gs_closure_new
func gs_closure_new(code C.uint64_t, n C.int64_t, arity C.int64_t, needsSelf C.int64_t) C.int64_t {
	c := &nativeClosure{code: uintptr(code), n: int(n), arity: int(arity), needsSelf: needsSelf != 0}
	if n > 0 {
		c.captures = make([]re.Value, int(n))
	}
	// The Scheme procedure the captures belong to.  A closure made by the
	// compiler has no source form to fall back on, so its clause carries the
	// native body and nothing else — a call it declines is an arity error
	// rather than a slower answer, and returning false is how it says so.
	proc := &re.Closure{
		Name: "",
		Clauses: []re.ClosureClause{{
			Params: make([]*re.Symbol, c.arity),
			Native: c,
		}},
	}
	h := store(proc)
	c.handle = h
	return C.int64_t(h)
}

// gs_closure_set writes one of a closure's captures.
//
// It is called twice in a closure's life and both are the same operation: once
// when the closure is made, to store the value of each captured variable, and
// again whenever the body assigns one.  A capture has to be assignable because a
// closure that counts is the reason closures are worth having —
// `(let ((n 0)) (lambda () (set! n (+ n 1)) n))` — and handing the capture in as
// a *parameter* is what makes reading one free.  A parameter cannot be assigned
// through, so the body writes here instead, and reads the parameter it was
// given.  The two agree because the parameter is loaded from here at the start
// of every call.
//
//export gs_closure_set
func gs_closure_set(h C.int64_t, i C.int64_t, v C.gs_val) {
	c, ok := closureAt(int64(h))
	if !ok || int64(i) < 0 || int64(i) >= int64(c.n) {
		report(fmt.Errorf("closure capture index out of range"))
		return
	}
	c.captures[i] = untagged(v)
}

//export gs_closure_ref
func gs_closure_ref(h C.int64_t, i C.int64_t) C.gs_val {
	c, ok := closureAt(int64(h))
	if !ok || int64(i) < 0 || int64(i) >= int64(c.n) {
		report(fmt.Errorf("closure capture index out of range"))
		return handle(store(re.UnspecifiedValue))
	}
	return tagged(c.captures[i])
}

// closureAt finds the native closure a handle names, if it names one.
func closureAt(h int64) (*nativeClosure, bool) {
	proc, ok := load(h).(*re.Closure)
	if !ok || len(proc.Clauses) != 1 {
		return nil, false
	}
	c, ok := proc.Clauses[0].Native.(*nativeClosure)
	return c, ok
}

//export gs_closure_apply
func gs_closure_apply(h C.int64_t, n C.int64_t, args *C.gs_val) C.gs_val {
	v := load(int64(h))
	c, isCompiled := closureAt(int64(h))
	if !isCompiled {
		// Not a compiled closure: an interpreted procedure, a primitive, a
		// continuation.  The interpreter's own apply knows what to do with it,
		// which is the point of a closure being an ordinary procedure.
		vals := make([]re.Value, int(n))
		for i := 0; i < int(n); i++ {
			vals[i] = untagged(*argsAt(args, i))
		}
		out, err := mach.ApplySync(v, vals)
		if err != nil {
			report(err)
			return handle(store(re.UnspecifiedValue))
		}
		return tagged(out)
	}
	// The caller's arguments, then the captures, which is the order the emitted
	// body expects: its own parameters first and its captures after them.
	//
	// An array on this frame rather than a heap slice, because this is on the
	// path of every call to a computed procedure and allocating per call is what
	// the argument arrays elsewhere exist to avoid.
	var argv [8]C.gs_val
	var heap []C.gs_val
	vals := argv[:0]
	total := int(n) + c.n
	if total > len(argv) {
		heap = make([]C.gs_val, total)
		vals = heap
	}
	for i := 0; i < int(n); i++ {
		vals = append(vals, *argsAt(args, i))
	}
	for i := 0; i < c.n; i++ {
		vals = append(vals, tagged(c.captures[i]))
	}
	// Then the closure's own handle, when the body assigns a capture and so needs
	// to be able to write one back.  It is one more argument rather than a
	// separate register because the ABI has one way to pass a value and adding a
	// second would mean every emitted body had to agree about it.
	if c.needsSelf {
		vals = append(vals, handle(int64(h)))
	}
	if len(vals) == 0 {
		return C.gs_call_addr(C.uintptr_t(c.code), 0, nil)
	}
	return C.gs_call_addr(C.uintptr_t(c.code), C.int64_t(len(vals)), &vals[0])
}

// gs_call applies a Scheme procedure by name, with tagged arguments.
//
// This is how a compiled body reaches a procedure the compiler could not emit:
// display, cons, anything in the library.  Refusing the whole body instead would
// give up the machine code around the call for nothing, since the call means the
// same thing either way.
//
// The name is looked up rather than the procedure passed in, because the
// generated code has no way to hold a Scheme value that is not a number — which
// is the whole reason the value it carries is tagged.
//
//export gs_call
func gs_call(name *C.char, n C.int64_t, args *C.gs_val, cache *C.int64_t) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	proc, ok := cachedProc(name, cache)
	if !ok {
		report(fmt.Errorf("%s: undefined", C.GoString(name)))
		return handle(store(re.UnspecifiedValue))
	}
	// The arguments go into an array on this frame rather than a slice on the
	// heap.  A compiled loop that calls a builtin once per iteration was
	// allocating once per iteration for something it immediately threw away,
	// which is a large part of why such a loop ran slower than the interpreter.
	//
	// An array rather than a shared buffer, because gs_call can be re-entered:
	// a builtin that calls back into Scheme — `map`, `call-with-values` — reaches
	// a compiled procedure, which calls gs_call again.  A package-level buffer
	// would be silently corrupted by that, and the saving would not be worth it.
	var argv [8]re.Value
	var heap []re.Value
	vals := argv[:0]
	if int(n) > len(argv) {
		heap = make([]re.Value, int(n))
		vals = heap
	}
	for i := 0; i < int(n); i++ {
		vals = append(vals, untagged(*argsAt(args, i)))
	}
	v, err := mach.ApplySync(proc, vals)
	if err != nil {
		report(err)
		return handle(store(re.UnspecifiedValue))
	}
	return tagged(v)
}

// cachedProc finds the procedure a call site names, remembering it in the slot
// the generated code gave us.
//
// The slot is what makes a call in a loop cheap.  Without it every call pays a
// C string conversion, a symbol interning under a mutex, and a walk up the
// environment chain — and a compiled loop whose body is one call therefore ran
// slower than the interpreter, which is the opposite of the point.
//
// The slot holds a *handle*, not a pointer: a Scheme value is a Go interface
// value and cannot be a C pointer, so what is remembered is an index into the
// same table every other crossing uses.  Zero means "nothing cached yet", and
// the table's first entry is never a procedure the compiler would call, so zero
// is unambiguous.
//
// A cached binding is never invalidated, which is a real limit and a deliberate
// one: a global procedure redefined after a call site has run will not be seen
// by that site.  Redefining a procedure in a running program is rare, and paying
// a generation check on every call to catch it would cost more than it saves —
// the same trade the interpreter declines and the compiler accepts, which is why
// the cache is per call site and why it was measured before it was added.
func cachedProc(name *C.char, cache *C.int64_t) (re.Value, bool) {
	if cache != nil && *cache != 0 {
		return load(int64(*cache) - 1), true
	}
	procName := C.GoString(name)
	proc, ok := mach.Global.Lookup(re.Intern(procName))
	if !ok {
		return nil, false
	}
	if cache != nil {
		// +1 so that a stored handle is never zero, which is the empty marker.
		*cache = C.int64_t(store(proc) + 1)
	}
	return proc, true
}

// argsAt indexes a gs_val array, whose elements are not addressable from Go.
func argsAt(args *C.gs_val, i int) *C.gs_val {
	return (*C.gs_val)(unsafe.Pointer(uintptr(unsafe.Pointer(args)) +
		uintptr(i)*unsafe.Sizeof(C.gs_val{})))
}

// gs_register makes a compiled procedure reachable from Scheme.
//
// The generated functions are otherwise dead code: `main` hands the top-level
// forms to the interpreter one at a time, so nothing in the program calls them.
// Registering the name and the body is what connects the two — a call the
// interpreter is about to make to a named procedure goes to the machine code
// instead of walking that procedure's closure.
//
//export gs_register
func gs_register(name *C.char, arity C.int64_t, fn unsafe.Pointer) {
	if name == nil || fn == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	natives[C.GoString(name)] = nativesEntry{
		fn:    C.gs_native_fn(fn),
		arity: int(arity),
	}
}

// attachNatives gives every registered procedure its compiled body.
//
// It runs after a top-level form has been evaluated, because registration
// happens first: `main` registers every compiled procedure before it hands over
// the first form, since registering is what makes the *later* definitions
// reachable.  A name that is not a procedure — or not defined yet — is simply
// passed over, and a `define` that comes later is caught by the next call.
//
// The global environment is walked rather than every environment, because a
// native body is only attached to the top-level binding a `define` created.
// That is the case the compiler produces, and a procedure redefined locally
// keeps its interpreted body, which is correct rather than merely safe.
func attachNatives(m *scheme.Machine) {
	mu.Lock()
	pending := make(map[string]nativesEntry, len(natives))
	for k, v := range natives {
		pending[k] = v
	}
	mu.Unlock()
	if len(pending) == 0 {
		return
	}
	for name, e := range pending {
		v, ok := m.Global.Lookup(re.Intern(name))
		if !ok {
			continue
		}
		c, ok := v.(*re.Closure)
		if !ok {
			continue
		}
		np := nativeFunc{fn: e.fn, arity: e.arity}
		for i := range c.Clauses {
			if len(c.Clauses[i].Params) == e.arity && !c.Clauses[i].HasRest {
				c.Clauses[i].Native = np
			}
		}
	}
}

// nativeFunc calls a registered body, doing the tagging at the boundary.
type nativeFunc struct {
	fn    C.gs_native_fn
	arity int
}

// Call boxes the arguments, calls the machine code, and unboxes the result.
//
// The arguments go over as an array because every compiled body has the same
// signature: `gs_val f(int64_t n, gs_val *args)` fits every arity, so one
// function-pointer type covers them all and this does not have to know how many
// arguments there are.
//
// ok is false when the call cannot be taken natively, and the interpreter then
// runs the body it still has.  Two cases reach that: an argument list that does
// not match, and a value the runtime cannot unbox — a handle that is not a
// number, which a pure body cannot produce but a hand-written call can supply.
func (f nativeFunc) Call(args []re.Value) (re.Value, bool) {
	if len(args) != f.arity {
		return re.UnspecifiedValue, false
	}
	boxed := make([]C.gs_val, len(args))
	for i, a := range args {
		boxed[i] = tagged(a)
	}
	var p *C.gs_val
	if len(boxed) > 0 {
		p = &boxed[0]
	}
	return untagged(C.gs_call_native(f.fn, C.int64_t(len(boxed)), p)), true
}

// gs_arith is the arithmetic a native body falls back to when the machine word
// is not enough.
//
// The generated code computes in i64 and detects overflow with LLVM's own
// intrinsics; when one fires it calls here with the operands, boxed.  Coming back
// is a tagged value, so a result too large for a machine word becomes a handle
// instead of being truncated — which is what makes the native path total rather
// than merely fast.
//
// The runtime's own arithmetic is used rather than a second implementation in
// C: whatever the language says + means, it says it in NumAdd, and answering the
// same question twice is how the two answers start to differ.
//
//export gs_arith
func gs_arith(op C.int32_t, a C.gs_val, b C.gs_val) C.gs_val {
	av, bv := untagged(a), untagged(b)
	var out re.Value
	switch int32(op) {
	case int32(C.GS_ADD):
		out = re.NumAdd(av, bv)
	case int32(C.GS_SUB):
		out = re.NumSub(av, bv)
	case int32(C.GS_MUL):
		out = re.NumMul(av, bv)
	default:
		out = re.UnspecifiedValue
	}
	return tagged(out)
}

// gs_truthy is the Scheme truth of a tagged value, as 0 or 1.
//
// A native `if` tests its condition in machine code, and every value the
// accepted bodies produce is a number — but the condition may still be a handle,
// so the question is asked here rather than assumed.
//
//export gs_truthy
func gs_truthy(v C.gs_val) C.int64_t {
	if re.IsTrue(untagged(v)) {
		return 1
	}
	return 0
}

// gs_num_eq, gs_num_lt and gs_num_le are the comparisons a native body uses
// when one of its operands is a handle.
//
// They are separate entry points rather than an operation code because a
// comparison answers a question, not an arithmetic result: the generated code
// wants a 0 or 1 it can branch on, and boxing that would be pointless.
//
// Beware: gs_num_le is `<=` only.  `>=` is `gs_num_le(b, a)`, which the
// generator emits by swapping, and getting that backwards is a silent wrong
// answer rather than a crash — so the swap is done once, in emitCompare.

//export gs_num_eq
func gs_num_eq(a C.gs_val, b C.gs_val) C.int64_t {
	return boolWord(re.NumEq(untagged(a), untagged(b)))
}

//export gs_num_lt
func gs_num_lt(a C.gs_val, b C.gs_val) C.int64_t {
	return boolWord(re.NumCmp(untagged(a), untagged(b)) < 0)
}

//export gs_num_le
func gs_num_le(a C.gs_val, b C.gs_val) C.int64_t {
	return boolWord(re.NumCmp(untagged(a), untagged(b)) <= 0)
}

func boolWord(b bool) C.int64_t {
	if b {
		return 1
	}
	return 0
}

func report(err error) {
	if err == nil {
		return
	}
	if ee, ok := err.(*scheme.ExitError); ok {
		os.Exit(ee.Code)
	}
	os.Stderr.WriteString("goscheme: " + err.Error() + "\n")
}

// gs_guard runs a compiled body under a `guard`.
//
// `guard` is the one derived form that cannot be rewritten into core syntax,
// because what it means is a handler installed on the machine plus a
// continuation to escape to — and a compiled body has neither.  So the *shape*
// becomes this call: the body is emitted as a compiled thunk, which is the part
// worth compiling, and the clauses are passed as the text of a `guard` form the
// interpreter runs with that thunk as its body.
//
// The interpreter's own `guard` therefore does the work — installing the handler,
// escaping, unwinding `dynamic-wind` — and there is no second implementation to
// disagree with it.  A body that returns normally never reaches the handler.
//
// The thunk is bound to a name before the form is built, because a Scheme call
// names its operator: `(thunk)` would ask the runtime for a binding called
// `thunk` rather than call the closure that was passed in.  An earlier version
// wrote exactly that and the closure was never called, so its captured variables
// never existed — the interpreter reported them unbound.
//
//export gs_guard
func gs_guard(clauses *C.char, n C.int64_t, thunk C.gs_val) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	src := goString(clauses, int64(n))
	r := re.NewStringReader(src)
	r.Source = "guard"
	forms, err := r.ReadAll()
	if err != nil || len(forms) != 1 {
		report(fmt.Errorf("guard: malformed clause list"))
		return handle(store(re.UnspecifiedValue))
	}
	name := re.Intern("guard-thunk")
	mach.Global.Define(name, untagged(thunk))
	form := re.List(re.Intern("guard"), forms[0], re.List(name))
	v, err := mach.RunFormsCompiled([]re.Value{form}, mach.Global)
	if err != nil {
		report(err)
		return handle(store(re.UnspecifiedValue))
	}
	return tagged(v)
}

// gs_delay builds a promise from a compiled thunk.
//
// A `delay` is a thunk and nothing else — the body is not run until the promise
// is forced — so the body compiles and only the wrapping is the runtime's.  This
// is the same division gs_guard uses: the part that is arithmetic becomes machine
// code and the part that is about the machine's own machinery stays where that
// machinery lives.
//
// force is written in the interpreter because forcing walks a chain of promises
// and may run Scheme code, which is not something generated code does.
//
//export gs_delay
func gs_delay(thunk C.gs_val, force C.int64_t) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	// The same round trip gs_guard makes, and for the same reason: the promise
	// has to hold a *Scheme* procedure, and what crossed the boundary is a
	// compiled closure that the interpreter applies.
	newPro, ok := mach.Global.Lookup(re.Intern("make-promise-from-thunk"))
	if !ok {
		report(fmt.Errorf("delay: make-promise-from-thunk is not bound"))
		return handle(store(re.UnspecifiedValue))
	}
	v, err := mach.ApplySync(newPro, []re.Value{untagged(thunk), re.BooleanOf(force != 0)})
	if err != nil {
		report(err)
		return handle(store(re.UnspecifiedValue))
	}
	return tagged(v)
}
