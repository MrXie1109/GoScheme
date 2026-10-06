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
*/
import "C"

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/MrXie1109/GoScheme/internal/scheme"
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
	handles []scheme.Value
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

func store(v scheme.Value) int64 {
	mu.Lock()
	defer mu.Unlock()
	handles = append(handles, v)
	return int64(len(handles) - 1)
}

func load(h int64) scheme.Value {
	mu.Lock()
	defer mu.Unlock()
	if h < 0 || int(h) >= len(handles) {
		return scheme.UnspecifiedValue
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
func tagged(v scheme.Value) C.gs_val {
	if n, ok := v.(*scheme.Integer); ok {
		if small, fits := n.Int64(); fits {
			return fixnum(small)
		}
	}
	if b, ok := v.(scheme.Boolean); ok {
		return boolean(bool(b))
	}
	if v == scheme.Value(scheme.Nil) {
		return C.gs_val{bits: 0, tag: C.GS_NULL}
	}
	return handle(store(v))
}

// untagged recovers the Scheme value a tagged word names.
func untagged(v C.gs_val) scheme.Value {
	switch int64(v.tag) {
	case int64(C.GS_HANDLE):
		return load(int64(v.bits))
	case int64(C.GS_BOOLEAN):
		return scheme.BooleanOf(int64(v.bits) != 0)
	case int64(C.GS_NULL):
		return scheme.Nil
	}
	return scheme.Value(scheme.Int(int64(v.bits)))
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
	r := scheme.NewStringReader(source)
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
		return C.int64_t(store(scheme.UnspecifiedValue))
	}
	forms, err := scheme.NewStringReader(src).ReadAll()
	if err != nil || len(forms) != 1 {
		return C.int64_t(store(scheme.UnspecifiedValue))
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
func gs_countloop(kind C.int32_t, args *C.gs_val, nextra C.int32_t) C.gs_val {
	if mach == nil {
		gs_init(0, nil)
	}
	n := untagged(*argsAt(args, 0))
	acc := untagged(*argsAt(args, 1))
	var extras []scheme.Value
	for i := 0; i < int(nextra); i++ {
		extras = append(extras, untagged(*argsAt(args, 2+i)))
	}
	return tagged(scheme.RunCountLoopExtras(int(int32(kind)), n, acc, extras))
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
	v, ok := mach.Global.Lookup(scheme.Intern(n))
	if !ok {
		report(fmt.Errorf("%s: undefined", n))
		return handle(store(scheme.UnspecifiedValue))
	}
	return tagged(v)
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
		return handle(store(scheme.UnspecifiedValue))
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
	var argv [8]scheme.Value
	var heap []scheme.Value
	vals := argv[:0]
	if int(n) > len(argv) {
		heap = make([]scheme.Value, int(n))
		vals = heap
	}
	for i := 0; i < int(n); i++ {
		vals = append(vals, untagged(*argsAt(args, i)))
	}
	v, err := mach.ApplySync(proc, vals)
	if err != nil {
		report(err)
		return handle(store(scheme.UnspecifiedValue))
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
func cachedProc(name *C.char, cache *C.int64_t) (scheme.Value, bool) {
	if cache != nil && *cache != 0 {
		return load(int64(*cache) - 1), true
	}
	procName := C.GoString(name)
	proc, ok := mach.Global.Lookup(scheme.Intern(procName))
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
		v, ok := m.Global.Lookup(scheme.Intern(name))
		if !ok {
			continue
		}
		c, ok := v.(*scheme.Closure)
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
func (f nativeFunc) Call(args []scheme.Value) (scheme.Value, bool) {
	if len(args) != f.arity {
		return scheme.UnspecifiedValue, false
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
	var out scheme.Value
	switch int32(op) {
	case int32(C.GS_ADD):
		out = scheme.NumAdd(av, bv)
	case int32(C.GS_SUB):
		out = scheme.NumSub(av, bv)
	case int32(C.GS_MUL):
		out = scheme.NumMul(av, bv)
	default:
		out = scheme.UnspecifiedValue
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
	if scheme.IsTrue(untagged(v)) {
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
	return boolWord(scheme.NumEq(untagged(a), untagged(b)))
}

//export gs_num_lt
func gs_num_lt(a C.gs_val, b C.gs_val) C.int64_t {
	return boolWord(scheme.NumCmp(untagged(a), untagged(b)) < 0)
}

//export gs_num_le
func gs_num_le(a C.gs_val, b C.gs_val) C.int64_t {
	return boolWord(scheme.NumCmp(untagged(a), untagged(b)) <= 0)
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
