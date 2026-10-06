// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
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
	// The empty list needs a tag of its own as soon as a compiled body stores
	// one.  It used to travel as the fixnum 0, which is indistinguishable from
	// zero while every value only flows back to the interpreter — `0` and `()`
	// are both false, and neither is arithmetic.  A walk *keeps* its
	// accumulator, though, so `(build 5 '())` consed onto the number zero and
	// produced the improper list `(1 2 3 4 5 . 0)`.
	tagNull = "3"
	// The unspecified value needs a tag because the compiler can *produce* one:
	// `(if TEST THEN)` with no alternative, and a `cond` whose clauses all fail,
	// are both unspecified in R7RS, and a compiled body that fell off the end of
	// one used to yield the fixnum 0 — so `(display (cond (#f 1)))` printed `0`
	// where the interpreter printed `#!unspecified`.  It is not the empty list
	// and not #f: both of those are values a program can compare against, and
	// unspecified is what the report says a program must not rely on.
	tagUnspecified = "4"
)

// gsValType declares the value type in the module, which has to happen before
// any function mentioning it.  The name and the type are written separately
// because a named type is `%gs.val = type { i64, i64 }`: the left-hand side is
// the name, and only the right-hand side is the type.
func (m *irModule) gsValType() {
	m.typeDecl("%gs.val = type " + gsValStruct)
}

// notWorthCompiling reports why a body the generator understands should
// nevertheless be left to the interpreter, or "" when compiling it is worth
// doing.
//
// This is the one place the compiler decides against itself.  It exists because
// compiling is not free: a value crossing the boundary is boxed on the way out
// and unboxed on the way back, so a body that calls into the runtime pays that
// on every call, and if there is nothing else in the body then the interpreter
// would have made the same call with everything already unboxed.
//
// **The rule is narrow on purpose, and narrow is the point.**  It refuses only
// what cannot gain, and accepts everything else even when the gain is small.
// That is the opposite of what a cost model would do, and it is a deliberate
// choice: a model needs to know what each runtime procedure costs, which is a
// property of the interpreter rather than of this generator, and a rule that
// guessed wrong would refuse to compile code that is faster compiled.  Refusing
// something that would have gained is the worse failure, because it is the
// invisible one — the program still runs, just no faster, and nothing says why.
//
// What cannot gain is a body whose only work is the call.  `strings` is the
// example: its hot procedure is a counter, a test, and `string-append`, and
// compiled it ran at 0.40× of the interpreter's speed.  `vectors` (0.68×) is the
// same shape around `vector-ref`.
//
// Note what this does *not* claim to catch.  `vectors` still compiles: its body
// accumulates `(+ acc (vector-ref v i))`, so by the count below it does keep a
// value, and the count is all this rule can see.  Distinguishing "the accumulator
// is the work" from "the call is the work and the accumulator merely receives
// it" needs to know how expensive the call is, which is the cost model this
// deliberately does not have.  So `vectors` is compiled and is slower for it,
// and that is written down rather than papered over — see docs/performance.md.
func notWorthCompiling(r pureReport) string {
	if r.runtimeCost == 0 {
		return ""
	}
	if r.accumulates == 0 {
		return "it does no arithmetic of its own, so compiling it would only add " +
			"the cost of crossing into the runtime"
	}
	// Every kept value is something a call produced, so the loop's arithmetic is
	// only a counter and the work is the call — `(+ acc (vector-ref v i))` is
	// the shape, and it measured 0.70×.
	//
	// This is as far as a rule without a cost model can honestly go.  It reads
	// whether the arithmetic combines anything of its own with what the call
	// returned; what it cannot tell is whether the called procedure is cheap
	// (`vector-ref`) or expensive (`sort`), which is the difference between a
	// loss and a large win and is a property of the interpreter, not of this
	// generator.  A body that reaches here and is still compiled is one whose
	// call the compiler is betting is expensive.
	if r.accumulates <= r.fromCalls {
		return "everything it accumulates comes from a call, so compiling it adds " +
			"the cost of crossing into the runtime without doing the work"
	}
	return ""
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
	// nativeOps counts the operations this body does in machine code: the
	// arithmetic and comparisons that are the reason to compile it at all.
	nativeOps int
	// runtimeCost is what the body spends crossing the boundary: each runtime
	// call's arguments and its result, all of which have to be boxed.  Together
	// with nativeOps it decides whether compiling is worth doing; see
	// worthCompiling.
	runtimeCost int
	// accumulates counts the arithmetic results the body *keeps*: those passed
	// to a call or returned, as opposed to those that only decide control flow.
	// A loop counter is the second kind and an accumulator is the first, and the
	// difference is whether compiling the loop is worth anything.
	accumulates int
	// fromCalls counts the kept values that a runtime call produced, which is
	// what tells an accumulator the body computes from one it merely passes on.
	fromCalls int
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
//
// keep says whether the value of this expression is *used* by something that
// outlives the step — an accumulator, an argument to a recursive call, the
// result of the procedure — as opposed to only deciding control flow.  It is
// what distinguishes `(+ acc i)`, which is the reason to compile a loop, from
// `(- i 1)`, which is a counter.
func (r *pureReport) scan(e Value, local map[string]bool) {
	r.scanKeep(e, local, true)
}

// scanKeep is scan with the caller saying whether the value is kept.
func (r *pureReport) scanKeep(e Value, local map[string]bool, keep bool) {
	if keep {
		if p, ok := e.(*Pair); ok {
			if sym, ok := p.Car.(*Symbol); ok && pureOperator(sym.Name) && !isCounting(sym.Name, p) {
				if callsOut(p, r.known, r.self) {
					r.fromCalls++
				}
				// An operation whose *result is kept* is one the machine code
				// does for a reason.  What is excluded is the loop's own
				// bookkeeping: `(- i 1)` and `(+ i 1)` step a counter, and a
				// counter is not the work a loop does — counting it made a body
				// of pure bookkeeping look like a body worth compiling.
				r.accumulates++
			}
		}
	}
	r.scanValue(e, local, keep)
}

// callsOut reports whether an expression contains a call to something that is
// not this generator's to emit: a runtime procedure, as opposed to an operator
// or a recursive call.
//
// It is what separates `(+ acc (vector-ref v i))`, whose value comes from
// outside, from `(+ acc i)`, whose value the machine code computes itself.
func callsOut(e Value, known map[string]bool, self string) bool {
	switch x := e.(type) {
	case *Pair:
		if sym, ok := x.Car.(*Symbol); ok {
			if !pureOperator(sym.Name) && sym.Name != self && !known[sym.Name] && !isSyntax(sym.Name) {
				return true
			}
		}
		items, _ := ListToSlice(x)
		for _, a := range items {
			if callsOut(a, known, self) {
				return true
			}
		}
	case *Vector:
		for _, a := range x.Items {
			if callsOut(a, known, self) {
				return true
			}
		}
	}
	return false
}

// isCounting reports whether an operation is a loop counter being stepped: one
// that combines a single carried value with a constant.
//
// The distinction being drawn is between stepping and computing.  `(- i 1)` and
// `(+ i 1)` move a counter; `(+ acc i)` combines two things the loop carries, and
// `(* acc acc)` does something with one.  Only the first is bookkeeping, and a
// body made only of it has nothing for machine code to do.
func isCounting(op string, p *Pair) bool {
	args, _ := ListToSlice(p.Cdr)
	if len(args) != 2 {
		return false
	}
	// A counter step is a name and a *literal*, in either order: `(- i 1)` and
	// `(+ 1 i)` both move a counter.  The literal has to be a number, which is
	// what distinguishes it from `(+ n (string-length "hello"))` — that combines
	// the parameter with a call, and a call is work.
	_, firstSym := args[0].(*Symbol)
	_, secondSym := args[1].(*Symbol)
	if firstSym == secondSym {
		return false
	}
	other := args[0]
	if firstSym {
		other = args[1]
	}
	switch other.(type) {
	case *Integer, *Float, *Rational:
		return true
	}
	return false
}

// scanValue is the walk itself, with keep threaded to the sub-expressions that
// inherit it.
func (r *pureReport) scanValue(e Value, local map[string]bool, keep bool) {
	switch x := e.(type) {
	case *Pair:
		r.scanCombination(x, local, keep)
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
	case *Integer, *Float, *Rational, *String, Boolean, *Char, Empty:
		// A literal: it computes nothing.  Boolean is a value type, not a
		// pointer, so it is listed without a star — with one it matches nothing
		// and every `#f` in a body looks like something unreadable.
	default:
		r.stop("the body contains %s, which is not a literal or a call", TypeName(e))
	}
}

// scanCombination handles a call form.
func (r *pureReport) scanCombination(x *Pair, local map[string]bool, keep bool) {
	head, ok := x.Car.(*Symbol)
	if !ok {
		// The operator is an expression rather than a name: `((make 1) 2)`, or
		// `(f x)` where f is a parameter holding a procedure.  Both the operator
		// and the arguments are evaluated and the callee is applied as a value,
		// which is emitted — see emitComputedCall — so this is a call to scan
		// rather than a form to refuse.
		//
		// Refusing it was the difference between `closures.scm` compiling its
		// loop and not: the loop body was `((make i) 1)`, the only thing the
		// scanner objected to was the operator not being a name, and the whole
		// procedure was left to the interpreter while the `make` it called *was*
		// compiled.  The result was slower than interpreting both, because every
		// iteration then crossed into machine code to build a closure and back
		// out through the boundary to call it.
		r.scanKeep(x.Car, local, true)
		if !r.ok {
			return
		}
		args, _ := ListToSlice(x.Cdr)
		for _, a := range args {
			r.scanKeep(a, local, true)
			if !r.ok {
				return
			}
		}
		// The callee is not known here, so the answer comes from outside like any
		// other runtime call.
		r.runtimeCalls = append(r.runtimeCalls, "a computed procedure")
		r.runtimeCost++
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
		// The test decides control flow and nothing else; the arms inherit
		// whether the whole `if` is kept.
		r.scanValue(args[0], local, false)
		for _, a := range args[1:] {
			r.scanKeep(a, local, keep)
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
		// Only the last part is the value of the `begin`.
		for _, a := range args[:len(args)-1] {
			r.scanValue(a, local, false)
		}
		r.scanKeep(args[len(args)-1], local, keep)
	case "quote":
		if len(args) != 1 {
			r.stop("quote takes one part")
		}
	case "and", "or":
		for _, a := range args {
			r.scanKeep(a, local, keep)
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
		// A do loop whose shape is a recognised walk is not refused: it is
		// emitted as that walk, so there is nothing in it the scan has to judge.
		// Any other form is refused here, where the reason can say so, rather
		// than left to the emitter to trip over.
		if isSyntax(head.Name) {
			// A do loop that is a recognised walk is accepted as a whole.  What
			// is emitted for it is the walk, not the do's own parts, so there is
			// nothing here for the scan to walk into — and walking into them
			// would fail, because the variable list and the step expressions are
			// not the list of literals and calls this scan reads.
			if head.Name == "do" {
				if loopName, vars, dbody, ok := doAsLoop(x); ok {
					if _, _, _, isWalk := recogniseAnyWalk(loopName, vars, dbody); isWalk {
						return
					}
				}
			}
			// A `set!` is scanned rather than refused: its value expression is
			// kept, and the assignment itself is emitted — as a rebinding for a
			// local and as a call into the runtime for a global.  See emitSet.
			// Nothing is accumulated by it, so it does not make a body worth
			// compiling on its own; it stops making one *uncompilable*, which is
			// the difference between a body that assigns in passing and a body
			// the compiler will not touch.
			if head.Name == "set!" {
				if len(args) == 2 {
					r.scanKeep(args[1], local, false)
				}
				return
			}
			// A `lambda` is emitted as a closure, and its body is scanned with
			// its own parameters added to the locals — so a variable it closes
			// over is captured and a variable it binds itself is not.  The body
			// is scanned rather than skipped because a lambda whose body this
			// cannot emit has to be refused here, where the reason can name it,
			// rather than by the emitter halfway through generating the outer
			// procedure.
			if head.Name == "lambda" {
				r.scanLambda(args, local)
				return
			}
			r.stop("%s is a form, not a call this can compile", head.Name)
			return
		}
		// An argument's value is used by the call, whatever becomes of the
		// call's own result — so it is kept, unless the call is a test.
		argKeep := !(head.Name == "=" || head.Name == "<" || head.Name == ">" ||
			head.Name == "<=" || head.Name == ">=")
		for _, a := range args {
			r.scanKeep(a, local, argKeep)
			if !r.ok {
				return
			}
		}
		if pureOperator(head.Name) {
			r.nativeOps += len(args)
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
		//
		// Counted by its operands and its result, because a crossing is what it
		// costs: two boxed arguments and a boxed answer is three times the work
		// of the call the interpreter would have made with everything already
		// unboxed.
		r.runtimeCalls = append(r.runtimeCalls, head.Name)
		r.runtimeCost += len(args) + 1
	}
}

// scanLet handles let and let*, which introduce bindings a body may use.
//
// A **named** let is the same syntax with the loop's name before the bindings:
//
//	(let loop ((i n) (acc 0)) (if (= i 0) acc (loop (- i 1) (+ acc i))))
//
// and it read as malformed here for as long as this function has existed,
// because `args[0]` is then the name rather than the binding list.  The name is
// bound to a procedure the body may call, so it joins the locals and the set of
// known procedures — which is what makes `(let loop ...)` inside an expression
// compile rather than being refused with a message about bindings.
func (r *pureReport) scanLet(args []Value, outer map[string]bool, sequential bool) {
	if len(args) < 1 {
		r.stop("a let with no bindings")
		return
	}
	// A named let: the name comes first and the bindings after it.
	named := ""
	if sym, ok := args[0].(*Symbol); ok {
		named = sym.Name
		args = args[1:]
		if len(args) < 1 {
			r.stop("a named let with no bindings")
			return
		}
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
	if named != "" {
		local[named] = true
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

// scanLambda scans a lambda body with its own parameters in scope.
//
// The parameters are what makes the difference between a capture and a local: a
// name the body binds itself is not free, and one it does not is read from the
// enclosing procedure.  Both are accepted, and a body that assigns a captured
// variable is not — an SSA parameter cannot be assigned through, so the capture
// would have to be boxed, and that is a different representation rather than a
// flag on this one.  Refusing it here says so.
func (r *pureReport) scanLambda(args []Value, outer map[string]bool) {
	if len(args) < 2 {
		return // malformed; the emitter reports it
	}
	formals, ok := ListToSlice(args[0])
	if !ok {
		r.stop("a lambda with a dotted parameter list")
		return
	}
	local := map[string]bool{}
	for k, v := range outer {
		local[k] = v
	}
	for _, f := range formals {
		s, ok := f.(*Symbol)
		if !ok {
			r.stop("a lambda parameter that is not a name")
			return
		}
		local[s.Name] = true
	}
	// The body is scanned for what it can emit, but nothing it does makes the
	// enclosing body worth compiling: a procedure that only makes a closure has
	// no arithmetic to put in machine code, and counting the lambda's body
	// toward the enclosing one would compile a body on the strength of work that
	// happens somewhere else.  So the counters are saved and restored.
	savedOps, savedAcc, savedFrom, savedCost := r.nativeOps, r.accumulates, r.fromCalls, r.runtimeCost
	savedCalls := append([]string(nil), r.runtimeCalls...)
	for _, b := range args[1:] {
		r.scan(b, local)
		if !r.ok {
			return
		}
	}
	r.nativeOps, r.accumulates, r.fromCalls, r.runtimeCost = savedOps, savedAcc, savedFrom, savedCost
	r.runtimeCalls = savedCalls
}
