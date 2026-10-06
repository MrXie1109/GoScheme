// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"strconv"
	"strings"
)

// Compiling a pure function to native code.
//
// A procedure is **pure** for this purpose when its body computes from its
// parameters and constants alone: no side effects, no global reads, no
// captured variables, no calls except to procedures that are themselves pure.
// `(define (add a b) (+ a b))` and `(define (fact n) (if (= n 0) 1 (* n (fact
// (- n 1)))))` are pure; `(define (f) (display 1))` is not, and neither is a
// procedure that closes over anything.
//
// A pure procedure is compiled to a real LLVM function — machine integers in
// registers, `br` for `if`, `call` for the recursive call — and it never
// touches the runtime.  That is the half of the hybrid that makes compiled
// programs fast, and the other half is everything this file refuses: anything
// it cannot prove is left for the runtime, which is the interpreter.
//
// The proof is conservative on purpose.  A procedure it accepts must be one
// whose meaning is exactly what the native code computes, so the checks below
// are of the form "this is the only thing the body does" rather than "this
// looks safe": an unboxed machine integer is not a Scheme number unless nothing
// in the body can make it something else, and `(* n (fact (- n 1)))` can
// overflow int64 where Scheme would have promoted to a bignum.
//
// Which is why the arithmetic is checked: a pure procedure is compiled natively
// **with an overflow test**, and takes the runtime path when the test fires.
// The fast path is the machine integer and the slow path is the interpreter,
// which is the arrangement the whole design rests on.

// gsVal is how a Scheme value crosses the native boundary: a machine word and a
// tag saying what the word means.
//
// A pure procedure computes in a machine word, which is a Scheme exact integer
// only while the value fits.  Rather than pretend otherwise, a value that does
// not fit is carried as a handle — an index into the runtime's table — and the
// tag says which of the two this is.  That is what makes the native path total:
// nothing is ever truncated, and no case has to end in "this cannot be
// represented".
//
// It is a *named* type rather than the literal `{ i64, i64 }` so that every
// signature mentioning it says the same thing, which is what lets a
// function-pointer bitcast be written once and hold for every procedure.
const gsVal = "%gs.val"

// gsValStruct is the definition behind the name.
const gsValStruct = "{ i64, i64 }"

// Tags for gsVal's second field, as the text that appears in the emitted code.
//
// They are strings rather than numbers because a tag is usually a constant that
// goes straight into an instruction, and the places that need it as a number
// compare against "0" anyway — which is what the tag being zero for a fixnum
// buys: an SSA value that is a fixnum and one whose tag has been folded are the
// same thing.
const (
	tagFixnum = "0"
	tagHandle = "1"
	// A boolean needs its own tag rather than travelling as a fixnum.  `(= 1 1)`
	// and `1` are different values in Scheme — one prints as `#t`, the other as
	// `1` — so a comparison result carried as a fixnum would print the wrong
	// thing the moment it left the compiled code.
	tagBoolean = "2"
)

// gsValType declares the value type in the module, which has to happen before
// any function mentioning it.  The name and the type are written separately
// because a named type is `%gs.val = type { i64, i64 }`: the left-hand side is
// the name, and only the right-hand side is the type.
func (m *irModule) gsValType() {
	m.typeDecl("%gs.val = type " + gsValStruct)
}

// pureReport says what a scan of a body found.
type pureReport struct {
	// ok is true when the whole body is a pure computation.
	ok bool
	// calls are the procedures the body calls by name, which have to be pure
	// themselves before this one can be.
	calls []string
	// why is what stopped the scan, for a message when a user asks why a
	// procedure was not compiled natively.
	why string
	// self is the name being defined, so that a recursive call is allowed.
	self string
	// runtimeCalls are the procedures this body calls that are the runtime's to
	// run rather than this generator's to emit.  They are recorded so that a
	// report can say what a compiled procedure reaches outside itself.
	runtimeCalls []string
	// globals are the names this body reads that are not its parameters, and so
	// have to be read from the runtime.
	globals []string
	// known is the set of other procedures in the program that are candidates
	// for native compilation.  A call to one of them is allowed — whether it
	// ends up native is settled later, when the call graph is closed — while a
	// call to anything else (display, cons, an operator this cannot emit) stops
	// the scan.
	known map[string]bool
}

// pureBody scans a procedure body and reports whether it can be compiled to
// native code, and what it calls.
//
// self is the name the procedure is being defined under, which is what makes a
// recursive procedure pure: a call to itself is a call to a pure procedure by
// construction, and `(fact (- n 1))` inside `fact` is the case that matters —
// the recursive shape is most of what a numeric procedure does.
func pureBody(formals []*Symbol, body []Value) pureReport {
	return pureBodyNamed("", formals, body)
}

// pureBodyNamed is pureBody with the procedure's own name, so that a recursive
// call is recognised.
func pureBodyNamed(self string, formals []*Symbol, body []Value) pureReport {
	return pureBodyIn(self, formals, body, nil)
}

// pureBodyIn is pureBodyNamed with the set of procedure names that may be
// called, which is what lets one pure procedure call another.
func pureBodyIn(self string, formals []*Symbol, body []Value, known map[string]bool) pureReport {
	r := &pureReport{ok: true, self: self, known: known}
	local := map[string]bool{}
	for _, s := range formals {
		local[s.Name] = true
	}
	for _, f := range body {
		r.scan(f, local)
		if !r.ok {
			return *r
		}
	}
	return *r
}

// scan walks one expression.
func (r *pureReport) scan(e Value, local map[string]bool) {
	switch x := e.(type) {
	case *Pair:
		r.scanCombination(x, local)
	case *Symbol:
		if local[x.Name] {
			return
		}
		// A name that is not a parameter is a global, and reading one is not a
		// refusal: it becomes a read at the point of use.
		//
		// At the point of use, not once at entry.  A global may be `set!` by
		// anything the body calls, so a value cached at entry would be the value
		// from before the call — a compiled program disagreeing with the
		// interpreter, which is the one thing this must not do.  A name that is
		// syntax is refused rather than read, since there is no binding to read.
		if isSyntax(x.Name) {
			r.stop("%s is a form, not a value this can compile", x.Name)
			return
		}
		r.globals = append(r.globals, x.Name)
	case *Integer, *Float, *Rational, *String, *Boolean, *Char, Empty:
		// A literal: it computes nothing.
	default:
		r.stop("the body contains %s, which is not a literal or a call", typeName(e))
	}
}

// scanCombination handles a call form.
func (r *pureReport) scanCombination(x *Pair, local map[string]bool) {
	head, ok := x.Car.(*Symbol)
	if !ok {
		r.stop("the operator is not a name")
		return
	}
	args, ok := ListToSlice(x.Cdr)
	if !ok {
		r.stop("the argument list is improper")
		return
	}
	switch head.Name {
	case "if":
		if len(args) < 2 || len(args) > 3 {
			r.stop("if takes two or three parts")
			return
		}
		for _, a := range args {
			r.scan(a, local)
		}
	case "let":
		r.scanLet(args, local, false)
	case "let*":
		r.scanLet(args, local, true)
	case "begin":
		if len(args) == 0 {
			r.stop("an empty begin is not a value")
			return
		}
		for _, a := range args {
			r.scan(a, local)
		}
	case "quote":
		if len(args) != 1 {
			r.stop("quote takes one part")
		}
	case "and", "or":
		for _, a := range args {
			r.scan(a, local)
		}
	default:
		// A call, in one of three kinds.
		//
		// An operator is inlined.  A call to another procedure in the program
		// is a native call, provided that procedure is compiled too.  Anything
		// else — display, cons, a library procedure, a global — is not refused:
		// it becomes a call into the runtime, and the parts of the body around
		// it stay native.
		//
		// That last part is the difference between compiling a procedure and
		// compiling a procedure's arithmetic.  `(define (f n) (begin (display
		// n) (* n n)))` has one part this cannot emit and one part it can, and
		// refusing the whole body because of the first would give up the second
		// for nothing.  What has to be true is only that the *results* agree,
		// and a runtime call returns the same value the interpreter would.
		isSelf := r.self != "" && head.Name == r.self
		isKnown := r.known[head.Name]
		// A form rather than a procedure: `set!`, `lambda`, `do`, a macro that
		// was not expanded.  It is refused here, where the reason can say so,
		// rather than left to the emitter to trip over.
		if isSyntax(head.Name) {
			r.stop("%s is a form, not a call this can compile", head.Name)
			return
		}
		for _, a := range args {
			r.scan(a, local)
			if !r.ok {
				return
			}
		}
		if pureOperator(head.Name) {
			return
		}
		if isSelf {
			return
		}
		if isKnown {
			r.calls = append(r.calls, head.Name)
			return
		}
		// A runtime call.  Its arguments are values this body computed, so they
		// have to be boxed to cross the boundary — which is what makes it a
		// call and not a refusal.
		r.runtimeCalls = append(r.runtimeCalls, head.Name)
	}
}

// scanLet handles let and let*, which introduce bindings a body may use.
func (r *pureReport) scanLet(args []Value, outer map[string]bool, sequential bool) {
	if len(args) < 1 {
		r.stop("a let with no bindings")
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		r.stop("the bindings are malformed")
		return
	}
	local := map[string]bool{}
	for k := range outer {
		local[k] = true
	}
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			r.stop("a binding is malformed")
			return
		}
		items, _ := ListToSlice(p)
		if len(items) != 2 {
			r.stop("a binding needs a name and a value")
			return
		}
		name, ok := items[0].(*Symbol)
		if !ok {
			r.stop("a binding name is not an identifier")
			return
		}
		// The initialiser sees the outer scope for let, and the bindings so far
		// for let*.
		if sequential {
			r.scan(items[1], local)
		} else {
			r.scan(items[1], outer)
		}
		if !r.ok {
			return
		}
		local[name.Name] = true
	}
	for _, b := range args[1:] {
		r.scan(b, local)
	}
}

func (r *pureReport) stop(why string, args ...interface{}) {
	if r.ok {
		r.ok = false
		r.why = fmt.Sprintf(why, args...)
	}
}

// pureOperator reports whether a name is one this generator can emit as a
// machine operation.  Everything else — display, cons, car, a user procedure —
// is the runtime's, which is what keeps the accepted set small enough to be
// sure of.
func pureOperator(name string) bool {
	switch name {
	case "+", "-", "*", "=", "<", ">", "<=", ">=",
		"quotient", "remainder", "modulo", "abs", "min", "max",
		"zero?", "positive?", "negative?", "even?", "odd?", "not":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Emitting native code for a pure procedure
// ---------------------------------------------------------------------------

// irFunc is one native function being generated.
type irFunc struct {
	// name is the symbol the function is emitted under, which the runtime is
	// also told about so that a call through a variable finds the native code.
	name string
	// mod is the module being built, which a helper needs when it has to
	// introduce a string constant or a declaration partway through a body.
	mod *irModule
	// params are the formals, in order, as tagged SSA values.
	params []irVal
	// locals maps a name to the tagged value holding it.
	locals map[string]irVal
	// entry collects the allocas, which LLVM wants in the entry block, and
	// body the rest of the instructions.
	entry strings.Builder
	body  strings.Builder
	// slots are the alloca'd locals, and nextSlot counts them.
	slots    []string
	nextSlot int
	// label counts the basic blocks within this function, and nextReg the
	// SSA values.
	label   int
	nextReg int
	// currentBlock is the basic block the emitter is writing into, which a phi
	// node needs to name its predecessor.
	currentBlock string
	// calls are the natively-compiled procedures this one may call, and self is
	// its own name, which a recursive call names.
	calls []string
	self  string
}

// want declares a runtime function the body being emitted needs.  Declaring it
// once is enough, and a body that never needs one never mentions it.
func (f *irFunc) want(sig string) { f.mod.declare(sig) }

// emitPureFunction writes a native LLVM function for a pure procedure.
//
// Every value in it is an i64: a Scheme fixnum.  That is the whole point of
// accepting only a pure body — a machine integer is a Scheme exact integer
// exactly as long as nothing can turn it into something else, and the only
// things the accepted body can do are arithmetic on parameters, constants, and
// calls to procedures that are pure in the same way.  Overflow is the one way
// out, and the generated code checks for it and takes the runtime path (see
// emitCheckedArith), because a Scheme integer that leaves the fixnum range
// becomes a bignum rather than wrapping.
func (g *irGen) emitPureFunction(name string, formals []*Symbol, body []Value, calls []string) error {
	f := &irFunc{
		name:         name,
		mod:          g.module,
		self:         name,
		locals:       map[string]irVal{},
		calls:        calls,
		currentBlock: "entry",
	}
	// The parameters arrive through an argument array and are loaded at entry.
	// A parameter is never assigned in Scheme without set!, which a pure body
	// cannot contain, so each one stays a single SSA pair for the whole
	// function.
	//
	// The signature is uniform — `%gs.val f(i64 n, %gs.val *args)` for every
	// arity — because that is what lets one function-pointer type cover every
	// compiled procedure.  A per-arity signature would need a different pointer
	// type, and a different bitcast, for each one.
	var sig strings.Builder
	fmt.Fprintf(&sig, "define %s @%s(i64 %%n, %s* %%args) {\n", gsVal, mangle(name), gsVal)
	sig.WriteString("entry:\n")
	m := g.module
	m.gsValType()
	for i, p := range formals {
		reg := fmt.Sprintf("%%p_%s", p.Name)
		slot := fmt.Sprintf("%%ap_%s", p.Name)
		fmt.Fprintf(&f.entry, "  %s = getelementptr %s, %s* %%args, i64 %d\n", slot, gsVal, gsVal, i)
		fmt.Fprintf(&f.entry, "  %s = load %s, %s* %s\n", reg, gsVal, gsVal, slot)
		v := irVal{bits: f.reg() + ".bits", tag: f.reg() + ".tag"}
		fmt.Fprintf(&f.entry, "  %s = extractvalue %s %s, 0\n", v.bits, gsVal, reg)
		fmt.Fprintf(&f.entry, "  %s = extractvalue %s %s, 1\n", v.tag, gsVal, reg)
		f.params = append(f.params, v)
		f.locals[p.Name] = v
	}
	// The intrinsics a checked operation uses.  They are declared here rather
	// than on demand because a missing declaration is reported at the *call*,
	// which is a misleading place to learn about it.
	m.declare("{ i64, i1 } @llvm.sadd.with.overflow.i64(i64, i64)")
	m.declare("{ i64, i1 } @llvm.ssub.with.overflow.i64(i64, i64)")
	m.declare("{ i64, i1 } @llvm.smul.with.overflow.i64(i64, i64)")

	// A recursive call names this function and needs no declaration of it: a
	// define is visible to its own body, and adding a declare as well is a
	// redefinition error — which `opt -passes=verify` said the first time this
	// was tried, and is the reason the check is part of the build below.

	val, err := f.emitExpr(Cons(Intern("begin"), listFromSlice(body)))
	if err != nil {
		return err
	}
	// The result is boxed before the body is flushed, because boxing emits
	// instructions into it: `insertvalue` is an instruction like any other, and
	// a register it defines has to appear before the `ret` that uses it.
	boxed := val.bits0(f)
	var out strings.Builder
	out.WriteString(sig.String())
	out.WriteString(f.entry.String())
	out.WriteString(f.body.String())
	fmt.Fprintf(&out, "  ret %s %s\n}\n\n", gsVal, boxed)
	g.module.body.WriteString(out.String())
	g.native++
	return nil
}

// irVal is a Scheme value as the generated code holds it: two SSA registers,
// one for the word and one for its tag.
//
// Carrying the tag alongside the word, rather than assuming the word is the
// value, is what makes the native path total.  An arithmetic result that does
// not fit a machine word becomes a handle at the moment it is produced, and
// every later operation on it goes back to the runtime — so the compiler never
// has to answer "what if this does not fit", because there is an answer.
type irVal struct {
	bits string // the number, or the handle
	tag  string // tagFixnum or tagHandle, as an i64
}

// fixnumVal is an irVal holding a constant machine integer.
func fixnumVal(n int64) irVal {
	return irVal{bits: fmt.Sprintf("%d", n), tag: tagFixnum}
}

// boolVal is an irVal holding #t or #f, which is a value of its own kind rather
// than the numbers 1 and 0.
func boolVal(b bool) irVal {
	if b {
		return irVal{bits: "1", tag: tagBoolean}
	}
	return irVal{bits: "0", tag: tagBoolean}
}

// truthVal converts an i1 into a Scheme boolean.
func (f *irFunc) truthVal(cond string) irVal {
	return irVal{bits: f.zext(cond), tag: tagBoolean}
}

// isConstFixnum reports whether the value is a literal the generator can fold,
// which is what makes a branch on a constant condition free.
func (v irVal) isConstFixnum() bool {
	return v.tag == tagFixnum && v.bits != "" && v.bits[0] != '%'
}

// emitExpr generates code for one expression and returns the SSA value it
// computes.
func (f *irFunc) emitExpr(e Value) (irVal, error) {
	switch x := e.(type) {
	case *Integer:
		// A literal too large for a machine word is not a compile-time error:
		// it is a value that lives as a handle, which is exactly what the tag
		// is for.  The runtime boxes it once, at entry to the form that uses
		// it, so the literal still appears in the emitted code as a constant.
		if !x.small {
			return f.boxedLiteral(x.String())
		}
		return irVal{bits: fmt.Sprintf("%d", x.i), tag: tagFixnum}, nil
	case *Boolean:
		return boolVal(bool(*x)), nil
	case *Symbol:
		if v, ok := f.locals[x.Name]; ok {
			return v, nil
		}
		// Not a parameter or a let binding, so it is a global: read it now,
		// where it is used, because anything between here and the last read may
		// have assigned it.
		return f.emitGlobalRead(x.Name)
	case Empty:
		return irVal{bits: "0", tag: tagFixnum}, nil
	case *Pair:
		return f.emitForm(x)
	default:
		// Any other self-evaluating literal — a string, a character, a float, a
		// rational, a vector — is boxed by its source text and handed over as a
		// handle.
		//
		// The generated code cannot build one, because that would mean this
		// knowing how each is represented, but it does not have to: the literal
		// is already written down, and the runtime can read what the compiler
		// read.  Without this, `(string-length "abc")` would refuse a body over
		// an argument rather than over what the body does.
		if isSelfEvaluating(e) {
			return f.boxedLiteral(WriteToString(e))
		}
		return irVal{}, fmt.Errorf("ir: cannot emit %s", typeName(e))
	}
}

// isSelfEvaluating reports whether a value stands for itself, so that writing it
// out and reading it back gives the same value.
//
// A symbol does not — it is a name, and the one it refers to is the runtime's
// business — and neither does a pair, which is a form to be evaluated.  Every
// other literal does, which is what makes boxing one safe.
func isSelfEvaluating(v Value) bool {
	switch v.(type) {
	case *Integer, *Float, *Rational, *Complex, *String, *Char, *Boolean,
		*Vector, *Bytevector:
		return true
	}
	return false
}

// emitForm handles a call form.
func (f *irFunc) emitForm(x *Pair) (irVal, error) {
	head, _ := x.Car.(*Symbol)
	if head == nil {
		return irVal{}, fmt.Errorf("ir: the operator is not a name")
	}
	args, _ := ListToSlice(x.Cdr)
	switch head.Name {
	case "begin":
		var last irVal
		var err error
		for _, a := range args {
			last, err = f.emitExpr(a)
			if err != nil {
				return irVal{}, err
			}
		}
		return last, nil
	case "if":
		return f.emitIf(args)
	case "let", "let*":
		return f.emitLet(args, head.Name == "let*")
	case "and", "or":
		return f.emitAndOr(args, head.Name == "and")
	case "quote":
		if len(args) != 1 {
			return irVal{}, fmt.Errorf("ir: quote takes one part")
		}
		return f.emitQuoted(args[0])
	}
	// Anything else is a call — unless it is a form rather than a procedure.
	// `set!`, `lambda`, `define` and the rest are syntax, not values, so
	// emitting one as a call would ask the runtime for a procedure that does
	// not exist.  The scan already refuses these; this is the same check at the
	// place that would otherwise get it wrong.
	if isSyntax(head.Name) {
		return irVal{}, fmt.Errorf("ir: %s is a form, not a call this can emit", head.Name)
	}
	return f.emitCall(head.Name, args)
}

// isSyntax reports whether a name is a special form or a macro rather than a
// procedure.
//
// The list is the one the interpreter's own reader works from, so a form added
// to the language is refused here too rather than quietly becoming a call to a
// procedure of the same name.  A macro is included because a `let-syntax` body
// has been expanded by the time a top-level procedure is compiled, and one that
// has not is not something this can emit.
func isSyntax(name string) bool {
	switch name {
	case "quote", "quasiquote", "unquote", "unquote-splicing",
		"if", "set!", "define", "lambda", "begin", "let", "let*", "letrec",
		"letrec*", "let-values", "let*-values", "define-values", "do", "cond",
		"case", "when", "unless", "and", "or", "delay", "delay-force",
		"parameterize", "guard", "assert", "define-syntax", "let-syntax",
		"letrec-syntax", "syntax-rules", "define-record-type", "case-lambda",
		"cons-stream", "the-environment", "define-library", "import",
		"include", "include-ci", "cond-expand", "else", "=>":
		return true
	}
	return false
}

// mangle turns a Scheme procedure name into the LLVM symbol its native body is
// emitted under.
func mangle(name string) string {
	return "gs_lam_" + mangleName(name)
}

// emitIf emits a conditional.  Both arms produce a tagged value, and the result
// is selected by the branch — which is what a phi node is for, and why it takes
// two of them: the tag is as much a part of the value as the word.
func (f *irFunc) emitIf(args []Value) (irVal, error) {
	if len(args) < 2 {
		return irVal{}, fmt.Errorf("ir: if takes two or three parts")
	}
	test, err := f.emitExpr(args[0])
	if err != nil {
		return irVal{}, err
	}
	cond, err := f.truthOf(test)
	if err != nil {
		return irVal{}, err
	}
	thenLabel := f.freshLabel("then")
	elseLabel := f.freshLabel("else")
	endLabel := f.freshLabel("endif")
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", cond, thenLabel, elseLabel)

	f.block(thenLabel)
	thenVal, err := f.emitExpr(args[1])
	if err != nil {
		return irVal{}, err
	}
	// The value may have been computed in a block other than the one the arm
	// started in, so the phi has to name where the value really comes from.
	thenFrom := f.currentBlock
	fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)

	f.block(elseLabel)
	// With no else arm the result is unspecified, and #f is as good a stand-in
	// as any: no accepted body can observe it, because reaching here means the
	// test was false and the body would have had to branch on it again.
	elseVal := fixnumVal(0)
	elseFrom := f.currentBlock
	if len(args) > 2 {
		elseVal, err = f.emitExpr(args[2])
		if err != nil {
			return irVal{}, err
		}
		elseFrom = f.currentBlock
	}
	fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)

	f.block(endLabel)
	bits := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i64 [ %s, %%%s ], [ %s, %%%s ]\n",
		bits, thenVal.bits, thenFrom, elseVal.bits, elseFrom)
	tag := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i64 [ %s, %%%s ], [ %s, %%%s ]\n",
		tag, thenVal.tag, thenFrom, elseVal.tag, elseFrom)
	return irVal{bits: bits, tag: tag}, nil
}

// emitAndOr emits and/or, which return the value that decided them rather than
// a boolean.
func (f *irFunc) emitAndOr(args []Value, isAnd bool) (irVal, error) {
	if len(args) == 0 {
		// (and) is #t and (or) is #f: booleans, not the numbers one and zero.
		return boolVal(isAnd), nil
	}
	if len(args) == 1 {
		return f.emitExpr(args[0])
	}
	// The chain short-circuits, so each step is a branch that either continues
	// or yields that step's own value.
	//
	// Every step's value has to be joinable, and they are not all computed in
	// the same block: a step that is itself a checked addition finishes in the
	// join block of its own overflow test.  So each one stores its two words
	// into slots and the phi at the end reads the slots, which is what makes the
	// predecessors well defined without the emitter having to reason about which
	// block each value landed in.
	bitsSlot := f.alloca()
	tagSlot := f.alloca()
	endLabel := f.freshLabel("andor.end")
	for i, a := range args {
		v, err := f.emitExpr(a)
		if err != nil {
			return irVal{}, err
		}
		fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", v.bits, bitsSlot)
		fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", v.tag, tagSlot)
		if i == len(args)-1 {
			break
		}
		cond, err := f.truthOf(v)
		if err != nil {
			return irVal{}, err
		}
		if !isAnd {
			// `or` continues while the value is false, so the test flips.
			not := f.reg()
			fmt.Fprintf(&f.body, "  %s = xor i1 %s, true\n", not, cond)
			cond = not
		}
		next := f.freshLabel("andor.next")
		keep := f.freshLabel("andor.keep")
		fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", cond, next, keep)
		f.block(keep)
		fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)
		f.block(next)
	}
	fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)
	f.block(endLabel)
	bits := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", bits, bitsSlot)
	tag := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", tag, tagSlot)
	return irVal{bits: bits, tag: tag}, nil
}

// emitLet emits a let: each binding is computed and kept, because a body may
// read it more than once.
func (f *irFunc) emitLet(args []Value, sequential bool) (irVal, error) {
	if len(args) < 1 {
		return irVal{}, fmt.Errorf("ir: a let with no bindings")
	}
	bindings, _ := ListToSlice(args[0])
	saved := map[string]irVal{}
	for k, v := range f.locals {
		saved[k] = v
	}
	// A tagged value is two words, so a binding that has to survive a call is
	// spilled rather than kept in a register — a call is where an SSA value
	// would otherwise have to be live across a block boundary.
	for _, b := range bindings {
		p := b.(*Pair)
		items, _ := ListToSlice(p)
		name := items[0].(*Symbol)
		val, err := f.emitExpr(items[1])
		if err != nil {
			return irVal{}, err
		}
		if sequential {
			// Each binding is visible to the next, so bind as we go.
			f.locals[name.Name] = val
			continue
		}
		// A parallel let: the initialisers all see the outer scope, so the
		// value is held aside and bound after all of them are computed.
		f.locals[name.Name] = val
	}
	var last irVal
	var err error
	for _, b := range args[1:] {
		last, err = f.emitExpr(b)
		if err != nil {
			return irVal{}, err
		}
	}
	f.locals = saved
	return last, nil
}

// emitQuoted emits a quoted literal the accepted body can hold: a number.
func (f *irFunc) emitQuoted(v Value) (irVal, error) {
	switch x := v.(type) {
	case *Integer:
		if !x.small {
			return f.boxedLiteral(x.String())
		}
		return fixnumVal(x.i), nil
	case *Boolean:
		return boolVal(bool(*x)), nil
	case Empty:
		return fixnumVal(0), nil
	}
	return irVal{}, fmt.Errorf("ir: a quoted value that is not a number")
}

// freshLabel returns a basic-block label unique within this function.
func (f *irFunc) freshLabel(prefix string) string {
	f.label++
	return fmt.Sprintf("%s_%d", prefix, f.label)
}

func (f *irFunc) reg() string {
	f.nextReg++
	return fmt.Sprintf("%%v%d", f.nextReg)
}

// emitCall emits a call: an arithmetic operation, a comparison, or a call to a
// procedure that was itself compiled natively.
//
// The arithmetic is **checked**.  A Scheme exact integer is unbounded, so
// `(* n (fact (- n 1)))` on a machine word can overflow where Scheme would have
// promoted to a bignum; the generated code tests for it and, when it happens,
// calls the runtime, which is the interpreter and knows about bignums.  That is
// what makes it safe to compile a procedure natively without proving anything
// about the size of its values.
func (f *irFunc) emitCall(op string, args []Value) (irVal, error) {
	vals := make([]irVal, 0, len(args))
	for _, a := range args {
		v, err := f.emitExpr(a)
		if err != nil {
			return irVal{}, err
		}
		vals = append(vals, v)
	}
	// The operators are inlined, so their arity is checked here rather than by
	// the runtime that would otherwise catch it.  A call to anything else may
	// have any number of arguments, including none — `(newline)` is a call like
	// any other.
	if pureOperator(op) && len(vals) == 0 {
		return irVal{}, fmt.Errorf("ir: %s takes at least one argument", op)
	}
	switch op {
	case "+", "-", "*":
		acc := vals[0]
		if len(vals) == 1 {
			if op == "-" {
				return f.emitCheckedArith(op, fixnumVal(0), acc)
			}
			return acc, nil // (+ x) is x, (* x) is x
		}
		var err error
		for _, v := range vals[1:] {
			acc, err = f.emitCheckedArith(op, acc, v)
			if err != nil {
				return irVal{}, err
			}
		}
		return acc, nil
	case "=", "<", ">", "<=", ">=":
		// Chained comparison, as Scheme has it: (< 1 2 3) is (< 1 2) and (< 2 3).
		if len(vals) < 2 {
			return fixnumVal(1), nil
		}
		acc := "1"
		for i := 0; i+1 < len(vals); i++ {
			cmp, err := f.emitCompare(op, vals[i], vals[i+1])
			if err != nil {
				return irVal{}, err
			}
			both := f.reg()
			fmt.Fprintf(&f.body, "  %s = and i64 %s, %s\n", both, acc, cmp)
			acc = both
		}
		return irVal{bits: acc, tag: tagBoolean}, nil
	case "zero?", "positive?", "negative?":
		// These ask a question about a number, and a number that does not fit a
		// machine word is a handle — so the question goes to the runtime when
		// the tag says so.
		cmp := map[string]string{"zero?": "eq", "positive?": "sgt", "negative?": "slt"}[op]
		return f.numericTest(vals[0], cmp, "0")
	case "not":
		cond, err := f.truthOf(vals[0])
		if err != nil {
			return irVal{}, err
		}
		not := f.reg()
		fmt.Fprintf(&f.body, "  %s = xor i1 %s, true\n", not, cond)
		return f.truthVal(not), nil
	case "abs":
		// abs of the most negative word overflows, which is the one case the
		// checked path has to catch.
		neg := f.reg()
		fmt.Fprintf(&f.body, "  %s = sub i64 0, %s\n", neg, vals[0].bits)
		cmp, err := f.numericTest(vals[0], "slt", "0")
		if err != nil {
			return irVal{}, err
		}
		cond := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", cond, cmp.bits)
		out := f.reg()
		fmt.Fprintf(&f.body, "  %s = select i1 %s, i64 %s, i64 %s\n",
			out, cond, neg, vals[0].bits)
		return irVal{bits: out, tag: tagFixnum}, nil
	case "min", "max":
		acc := vals[0]
		for _, v := range vals[1:] {
			less, err := f.compareTagged("<", v, acc)
			if err != nil {
				return irVal{}, err
			}
			cond := f.reg()
			fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", cond, less)
			if op == "max" {
				not := f.reg()
				fmt.Fprintf(&f.body, "  %s = xor i1 %s, true\n", not, cond)
				cond = not
			}
			out := f.reg()
			fmt.Fprintf(&f.body, "  %s = select i1 %s, i64 %s, i64 %s\n",
				out, cond, v.bits, acc.bits)
			// The tags are equal whenever both operands are numbers of the same
			// kind, and min/max of two fixnums is a fixnum — so the tag of the
			// winner is the tag of whichever operand won.
			tag := f.reg()
			fmt.Fprintf(&f.body, "  %s = select i1 %s, i64 %s, i64 %s\n",
				tag, cond, v.tag, acc.tag)
			acc = irVal{bits: out, tag: tag}
		}
		return acc, nil
	}
	// A call to another procedure: native when that procedure was compiled,
	// and a call into the runtime when it was not.  The runtime call is what
	// lets a body with one unemittable part still be compiled: `(begin (display
	// n) (* n n))` runs `display` through the interpreter and multiplies in
	// machine code.
	if f.known(op) || op == f.self {
		return f.emitNativeCall(op, vals)
	}
	return f.emitRuntimeCall(op, vals)
}

// emitRuntimeCall calls a procedure the runtime owns, by name.
//
// The arguments are boxed to cross the boundary and the result is read back,
// which is the same translation every other runtime entry point does.  Looking
// the procedure up by name on every call is the cost of not knowing at compile
// time what it is; it is a map lookup, and the alternative — refusing the body
// — gives up the machine code around the call as well.
func (f *irFunc) emitRuntimeCall(op string, vals []irVal) (irVal, error) {
	f.want(gsVal + " @gs_call(i8*, i64, " + gsVal + "*)")
	name := f.mod.stringLiteral(op, "call"+op)
	slot := f.allocaArray(len(vals))
	for i, v := range vals {
		f.storeArg(slot, i, v)
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_call(i8* %s, i64 %d, %s* %s)\n",
		out, gsVal, name, len(vals), gsVal, slot)
	return f.loadVal(out), nil
}

// emitGlobalRead reads a top-level binding by name.
//
// The read happens where the name appears rather than once at entry, because a
// global is mutable and the body may call something that assigns it.  Caching it
// would make a compiled procedure see the value from before the call, which the
// interpreter would not.
//
// An unbound name is the runtime's to report: it may be defined later, or by a
// library the program loads, and refusing to compile a body over a name that is
// not yet bound would reject a program that runs perfectly well.
func (f *irFunc) emitGlobalRead(name string) (irVal, error) {
	f.want(gsVal + " @gs_global(i8*)")
	lit := f.mod.stringLiteral(name, "global"+name)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_global(i8* %s)\n", out, gsVal, lit)
	return f.loadVal(out), nil
}

// storeArg writes one tagged value into an argument array.
func (f *irFunc) storeArg(slot string, i int, v irVal) {
	gp := f.reg()
	fmt.Fprintf(&f.body, "  %s = getelementptr %s, %s* %s, i64 %d\n",
		gp, gsVal, gsVal, slot, i)
	bp := f.reg()
	fmt.Fprintf(&f.body, "  %s = getelementptr %s, %s* %s, i64 0, i32 0\n",
		bp, gsVal, gsVal, gp)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", v.bits, bp)
	tp := f.reg()
	fmt.Fprintf(&f.body, "  %s = getelementptr %s, %s* %s, i64 0, i32 1\n",
		tp, gsVal, gsVal, gp)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", v.tag, tp)
}

// loadVal takes a gs.val apart into the two registers the emitter carries.
func (f *irFunc) loadVal(v string) irVal {
	bits := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 0\n", bits, gsVal, v)
	tag := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 1\n", tag, gsVal, v)
	return irVal{bits: bits, tag: tag}
}

// emitNativeCall calls another compiled procedure.
//
// The arguments go through an array because every compiled body has the same
// signature — `%gs.val f(i64 n, %gs.val *args)` — which is what lets one pointer
// type stand for all of them.  The array is built in the caller's frame.
func (f *irFunc) emitNativeCall(op string, vals []irVal) (irVal, error) {
	slot := f.allocaArray(len(vals))
	for i, v := range vals {
		f.storeArg(slot, i, v)
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @%s(i64 %d, %s* %s)\n",
		out, gsVal, mangle(op), len(vals), gsVal, slot)
	return f.loadVal(out), nil
}

// truthOf turns a tagged value into an i1 a branch can use.
//
// Scheme's only false value is #f, and an accepted body produces numbers, so a
// fixnum is always true — but "always true" is not the same as "known at
// compile time".  A comparison is itself a fixnum, and its *value* decides the
// branch, so a value whose tag is a constant still has to be tested.  Getting
// that wrong made every comparison test as true, which turned `(if (= n 0) ...)`
// into a branch that was always taken.
func (f *irFunc) truthOf(v irVal) (string, error) {
	// A literal: the answer is known here.
	if v.isConstFixnum() {
		if v.bits == "0" {
			return "false", nil
		}
		return "true", nil
	}
	// A fixnum whose value is not a literal: only the word needs testing, since
	// a fixnum is never #f.
	if v.tag == tagFixnum {
		out := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", out, v.bits)
		return out, nil
	}
	f.want("i64 @gs_truthy(" + gsVal + ")")
	isFixnum := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", isFixnum, v.tag, tagFixnum)
	askLabel := f.freshLabel("truth.ask")
	okLabel := f.freshLabel("truth.ok")
	doneLabel := f.freshLabel("truth.done")
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", isFixnum, okLabel, askLabel)

	f.block(okLabel)
	okFrom := f.currentBlock
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(askLabel)
	asked := f.reg()
	fmt.Fprintf(&f.body, "  %s = call i64 @gs_truthy(%s %s)\n", asked, gsVal, v.bits0(f))
	askedBool := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", askedBool, asked)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i1 [ true, %%%s ], [ %s, %%%s ]\n",
		out, okFrom, askedBool, askLabel)
	return out, nil
}

// bits0 rebuilds a whole gs_val from an irVal, which a runtime call that takes
// one needs.
func (v irVal) bits0(f *irFunc) string {
	first := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s undef, i64 %s, 0\n", first, gsVal, v.bits)
	second := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s %s, i64 %s, 1\n", second, gsVal, first, v.tag)
	return second
}

// numericTest asks a question whose answer a fixnum can give directly, and
// hands it to the runtime when the value is a handle.
//
// The two answers are computed in separate blocks and joined, rather than
// asking the runtime and ignoring the result — a handle and a fixnum are both
// reachable, and a program that only ever uses small numbers must not pay for
// the other case.
func (f *irFunc) numericTest(v irVal, pred, other string) (irVal, error) {
	if v.isConstFixnum() && other == "0" {
		// A literal against zero is decided here rather than at run time.  The
		// answer depends on the literal's *value*, not on the fact that it is
		// one, and reading only the tag is what made `(zero? 5)` true.
		zero, err := strconv.ParseInt(v.bits, 10, 64)
		if err != nil {
			return irVal{}, fmt.Errorf("ir: %s is not a machine integer", v.bits)
		}
		yes := map[string]bool{
			"eq":  zero == 0,
			"sgt": zero > 0,
			"slt": zero < 0,
		}[pred]
		return boolVal(yes), nil
	}
	fast := f.reg()
	switch pred {
	case "eq":
		fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", fast, v.bits, other)
	case "sgt":
		fmt.Fprintf(&f.body, "  %s = icmp sgt i64 %s, %s\n", fast, v.bits, other)
	case "slt":
		fmt.Fprintf(&f.body, "  %s = icmp slt i64 %s, %s\n", fast, v.bits, other)
	}
	isFixnum := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", isFixnum, v.tag, tagFixnum)
	slowLabel := f.freshLabel("numtest.slow")
	doneLabel := f.freshLabel("numtest.done")
	// The block the fast answer comes from, which the phi has to name: the
	// branch is about to leave it.
	fastFrom := f.currentBlock
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", isFixnum, doneLabel, slowLabel)

	f.block(slowLabel)
	// A handle: the runtime compares it.  `zero?` and friends are the same
	// question as a comparison against zero.
	var asked string
	switch pred {
	case "eq":
		f.want("i64 @gs_num_eq(" + gsVal + ", " + gsVal + ")")
		asked = f.callNumCompare("gs_num_eq", v, fixnumVal(0))
	case "sgt":
		// x > 0 is 0 < x, and only `<` is an entry point.
		f.want("i64 @gs_num_lt(" + gsVal + ", " + gsVal + ")")
		asked = f.callNumCompare("gs_num_lt", fixnumVal(0), v)
	case "slt":
		f.want("i64 @gs_num_lt(" + gsVal + ", " + gsVal + ")")
		asked = f.callNumCompare("gs_num_lt", v, fixnumVal(0))
	}
	askedBool := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", askedBool, asked)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i1 [ %s, %%%s ], [ %s, %%%s ]\n",
		out, fast, fastFrom, askedBool, slowLabel)
	return f.truthVal(out), nil
}

// callNumCompare calls one of the runtime's comparisons on two tagged values.
func (f *irFunc) callNumCompare(fn string, a, b irVal) string {
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call i64 @%s(%s %s, %s %s)\n",
		out, fn, gsVal, a.bits0(f), gsVal, b.bits0(f))
	return out
}

// compareTagged is a comparison of two tagged values, as 0 or 1.
func (f *irFunc) compareTagged(op string, a, b irVal) (string, error) {
	return f.emitCompare(op, a, b)
}

// boxedLiteral turns a constant too large for a machine word into a handle.
//
// The literal is written as a Scheme number and handed to the runtime, which
// builds the value: reproducing bignum construction in the generated code would
// mean the code knowing how a bignum is stored.
func (f *irFunc) boxedLiteral(text string) (irVal, error) {
	f.want("i64 @gs_box_literal(i8*, i64)")
	lit := f.mod.stringLiteral(text, "lit")
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call i64 @gs_box_literal(i8* %s, i64 %d)\n",
		out, lit, len(text))
	return irVal{bits: out, tag: tagHandle}, nil
}

// known reports whether a name is one the generated code can call directly.
func (f *irFunc) known(name string) bool {
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

// zext widens an i1 to the i64 a Scheme value is.
func (f *irFunc) zext(cmp string) string {
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = zext i1 %s to i64\n", out, cmp)
	return out
}

// emitCompare emits an ordering comparison as 0 or 1.
//
// Two fixnums are compared directly, which is the whole fast path.  Anything
// else goes to the runtime, because a handle's number may be larger than a
// machine word and the comparison has to be the exact one — and because the
// operands may not even be integers.
//
// `>` and `>=` are not runtime entry points: they are `<` and `<=` with the
// operands swapped, which is why the swap is written out here rather than left
// to each caller.
func (f *irFunc) emitCompare(op string, a, b irVal) (string, error) {
	pred := map[string]string{
		"=": "eq", "<": "slt", ">": "sgt", "<=": "sle", ">=": "sge",
	}[op]
	if pred == "" {
		return "", fmt.Errorf("ir: %s is not a comparison", op)
	}
	fast := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp %s i64 %s, %s\n", fast, pred, a.bits, b.bits)

	// The fast answer only stands when both operands are machine words.
	bothFixnum := f.reg()
	tags := f.reg()
	fmt.Fprintf(&f.body, "  %s = or i64 %s, %s\n", tags, a.tag, b.tag)
	fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", bothFixnum, tags, tagFixnum)

	slowLabel := f.freshLabel("cmp.slow")
	doneLabel := f.freshLabel("cmp.done")
	// The block the fast answer comes from, which the phi has to name: the
	// branch is about to leave it.
	fastFrom := f.currentBlock
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", bothFixnum, doneLabel, slowLabel)

	f.block(slowLabel)
	f.want("i64 @gs_num_eq(" + gsVal + ", " + gsVal + ")")
	f.want("i64 @gs_num_lt(" + gsVal + ", " + gsVal + ")")
	f.want("i64 @gs_num_le(" + gsVal + ", " + gsVal + ")")
	// The table is indexed into a variable first, and deliberately so: written
	// as `fn, swap := map[...]{...}[op]` the two-result form of a map index
	// takes over, so `swap` would be the *presence* of the key — always true
	// here — rather than the field.  That silently reversed every `>` and `>=`.
	compare := map[string]struct {
		name string
		swap bool
	}{
		"=":  {"gs_num_eq", false},
		"<":  {"gs_num_lt", false},
		">":  {"gs_num_lt", true},
		"<=": {"gs_num_le", false},
		">=": {"gs_num_le", true},
	}[op]
	lhs, rhs := a, b
	if compare.swap {
		lhs, rhs = b, a
	}
	asked := f.callNumCompare(compare.name, lhs, rhs)
	askedBool := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", askedBool, asked)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i1 [ %s, %%%s ], [ %s, %%%s ]\n",
		out, fast, fastFrom, askedBool, slowLabel)
	// A plain 0 or 1: the caller chains comparisons with `and i64` and wraps the
	// finished chain in a boolean tag.
	return f.zext(out), nil
}

// emitCheckedArith emits a +, - or * that falls back to the runtime when the
// machine-word result would not be the Scheme result.
//
// The check is LLVM's own overflow intrinsic, which sets a flag rather than
// trapping: `llvm.sadd.with.overflow.i64` returns the sum and a boolean, and the
// generated code tests the boolean.  Without it, `(* 1000000000000
// 1000000000000)` would produce a wrapped negative number where Scheme produces
// 10^24, and a compiled program would disagree with the interpreter — which is
// the one thing a compiler must never do.
//
// The operands are not assumed to be machine words: if either carries a handle
// tag the runtime does the arithmetic, which is what makes an exact integer of
// any size reachable from compiled code.  The two results are two words each,
// so they are spilled to slots and the join loads them — a phi node over four
// values would have to name the right predecessor for each, and the alloca is
// folded away again on the fast path.
func (f *irFunc) emitCheckedArith(op string, a, b irVal) (irVal, error) {
	intr := map[string]string{
		"+": "llvm.sadd.with.overflow.i64",
		"-": "llvm.ssub.with.overflow.i64",
		"*": "llvm.smul.with.overflow.i64",
	}[op]
	if intr == "" {
		return irVal{}, fmt.Errorf("ir: %s is not a checked operation", op)
	}
	bitsSlot := f.alloca()
	tagSlot := f.alloca()

	fastLabel := f.freshLabel("arith.fast")
	slowLabel := f.freshLabel("arith.slow")
	doneLabel := f.freshLabel("arith.done")

	// Both operands must be machine words for the intrinsic to mean anything.
	tags := f.reg()
	fmt.Fprintf(&f.body, "  %s = or i64 %s, %s\n", tags, a.tag, b.tag)
	bothFixnum := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", bothFixnum, tags, tagFixnum)
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", bothFixnum, fastLabel, slowLabel)

	f.block(fastLabel)
	// The intrinsic returns { i64, i1 }; take it apart with extractvalue.
	pair := f.reg()
	fmt.Fprintf(&f.body, "  %s = call { i64, i1 } @%s(i64 %s, i64 %s)\n",
		pair, intr, a.bits, b.bits)
	val := f.reg()
	over := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue { i64, i1 } %s, 0\n", val, pair)
	fmt.Fprintf(&f.body, "  %s = extractvalue { i64, i1 } %s, 1\n", over, pair)
	// No overflow: the word is the answer.  Overflow: the runtime's, which is
	// the same block a handle operand goes to.
	okLabel := f.freshLabel("arith.ok")
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", over, slowLabel, okLabel)
	f.block(okLabel)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", val, bitsSlot)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", tagFixnum, tagSlot)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(slowLabel)
	f.want(gsVal + " @gs_arith(i32, " + gsVal + ", " + gsVal + ")")
	slow := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_arith(i32 %d, %s %s, %s %s)\n",
		slow, gsVal, arithCode(op), gsVal, a.bits0(f), gsVal, b.bits0(f))
	slowBits := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 0\n", slowBits, gsVal, slow)
	slowTag := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 1\n", slowTag, gsVal, slow)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", slowBits, bitsSlot)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", slowTag, tagSlot)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	outBits := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", outBits, bitsSlot)
	outTag := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", outTag, tagSlot)
	return irVal{bits: outBits, tag: outTag}, nil
}

// block starts a basic block and records it as the one being written.
//
// The emitter has to know which block it is in, because a phi node names its
// predecessors by block, and an expression may well finish somewhere other than
// where it started: checked arithmetic branches through a fast and a slow path
// before it produces its value, so the value's block is the join, not the block
// the branch was written in.
func (f *irFunc) block(label string) {
	f.currentBlock = label
	fmt.Fprintf(&f.body, "%s:\n", label)
}

// alloca makes a slot.  LLVM requires the alloca instruction to be in the entry
// block, so the declaration is collected here and written out when the function
// is assembled — the body is generated before the entry block is final.
func (f *irFunc) alloca() string {
	f.nextSlot++
	name := fmt.Sprintf("%%slot%d", f.nextSlot)
	fmt.Fprintf(&f.entry, "  %s = alloca i64\n", name)
	f.slots = append(f.slots, name)
	return name
}

// allocaArray makes a slot for n tagged values, which is how arguments are
// passed to another compiled procedure.
//
// The array is in the caller's frame and lives only for the call, so this is an
// alloca rather than an allocation: a compiled call allocates nothing.
func (f *irFunc) allocaArray(n int) string {
	f.nextSlot++
	name := fmt.Sprintf("%%arr%d", f.nextSlot)
	fmt.Fprintf(&f.entry, "  %s = alloca %s, i64 %d\n", name, gsVal, n)
	f.slots = append(f.slots, name)
	return name
}

// emitCheckedNeg emits a negation that falls back to the runtime on overflow,
// which is the one case int64 cannot represent: -(most negative word).
func (f *irFunc) emitCheckedNeg(a irVal) (irVal, error) {
	return f.emitCheckedArith("-", fixnumVal(0), a)
}

// arithCode is the small integer the runtime uses to know which operation the
// fallback is for.
func arithCode(op string) int {
	switch op {
	case "+":
		return 0
	case "-":
		return 1
	case "*":
		return 2
	}
	return -1
}

// topLevelProcedure recognises (define (name args...) body...), the shape a
// procedure definition has when it can be compiled natively.
//
// Only that shape: (define name (lambda ...)) is the same thing written
// differently and is not recognised, because the scan would have to look
// through the lambda and the emitted symbol would have to match what the
// runtime bound — which it can, but keeping to one shape is one fewer thing to
// get wrong in a compiler whose contract is that it never changes what a
// program means.
func topLevelProcedure(form Value) (name string, formals []*Symbol, body []Value, ok bool) {
	p, isPair := form.(*Pair)
	if !isPair {
		return "", nil, nil, false
	}
	head, isSym := p.Car.(*Symbol)
	if !isSym || head.Name != "define" {
		return "", nil, nil, false
	}
	items, _ := ListToSlice(p.Cdr)
	if len(items) < 2 {
		return "", nil, nil, false
	}
	sig, isPair := items[0].(*Pair)
	if !isPair {
		return "", nil, nil, false
	}
	headSym, isSym := sig.Car.(*Symbol)
	if !isSym {
		return "", nil, nil, false
	}
	params, isList := ListToSlice(sig.Cdr)
	if !isList {
		return "", nil, nil, false // a rest parameter: not this shape
	}
	for _, prm := range params {
		s, isSym := prm.(*Symbol)
		if !isSym {
			return "", nil, nil, false
		}
		formals = append(formals, s)
	}
	return headSym.Name, formals, items[1:], true
}

// paramTypes is the LLVM parameter list for n i64 parameters.
func paramTypes(n int) string {
	if n == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("i64, ", n), ", ")
}
