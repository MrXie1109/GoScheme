// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// ---------------------------------------------------------------------------
// A counting loop
// ---------------------------------------------------------------------------

// countLoop is a loop that counts a parameter down to zero instead of walking a
// sequence.  It is the shape `build`, `iota` and `make-list` are written in:
//
//	(define (build n acc)
//	  (if (= n 0) acc (build (- n 1) (cons n acc))))
//
// There is no list to walk and no vector to index, so the runtime needs only
// the counter and the accumulator.  The element folded in is the counter
// itself, which is why the fold expressions below mention it rather than an
// element expression.
type countLoop struct {
	name string
	// count is the parameter counted down; acc is folded as the count falls.
	count *Symbol
	acc   *Symbol
	kind  loopKind
	// inclusive says the loop folds the zero iteration too, which is what
	// `(< i 0)` or `(<= i 0)` does where `(= i 0)` does not:
	//
	//	(= i 0)  folds n-1 … 1, then stops at 0
	//	(< i 0)  folds n-1 … 0, then stops at -1
	//
	// The two differ by one element and both are ordinary ways to write a
	// count-down loop, so the recogniser records which and the runtime is told.
	inclusive bool
	// extras are the loop's invariants: values the fold adds that come from an
	// enclosing let and therefore cannot change while the loop runs.
	//
	// Only such bindings are accepted, and that is what makes passing them to
	// the runtime once safe.  A global is not accepted even though it would
	// usually be constant, because `set!` could change it between iterations
	// and the compiled loop would then use a stale value — a wrong answer with
	// nothing to show for it.
	extras []*Symbol
}

// recogniseCountLoop reports whether a procedure counts a parameter down to
// zero while folding.
//
// The accepted body is exactly
//
//	(if (= N 0) ACC (NAME (- N 1) FOLD))
//
// where FOLD is `(cons N ACC)`, `(+ ACC N)` or `(+ ACC 1)`.  The counter is the
// value folded, so `(- N 1)` in the recursive call has to be the same N the
// fold uses — a loop that folded a different number would be a different
// program and is not recognised.
func recogniseCountLoop(name string, formals []*Symbol, body []Value) (countLoop, bool) {
	return recogniseCountLoopInv(name, formals, body, nil)
}

// recogniseCountLoopInv is recogniseCountLoop with the loop's invariants: the
// names an enclosing let bound, which the fold may add in.
func recogniseCountLoopInv(name string, formals []*Symbol, body []Value, inv map[string]bool) (countLoop, bool) {
	var none countLoop
	if len(formals) != 2 || len(body) != 1 {
		return none, false
	}
	ifForm, ok := body[0].(*Pair)
	if !ok || !isForm(ifForm, "if") {
		return none, false
	}
	parts, _ := ListToSlice(ifForm.Cdr)
	if len(parts) != 3 {
		return none, false
	}
	test, then, alt := parts[0], parts[1], parts[2]

	// The test compares a parameter with zero, either with `=` or with the
	// ordering a count-down loop is often written with:
	//
	//	(if (= n 0) acc (f (- n 1) ...))
	//	(if (< n 1) acc (f (- n 1) ...))
	//	(if (<= n 0) acc (f (- n 1) ...))
	//
	// All three stop at the same place when the counter only ever decreases by
	// one from a whole number, which is what the body below is checked for.  A
	// `>` or `>=` would stop somewhere else and is not accepted.
	testForm, ok := test.(*Pair)
	if !ok {
		return none, false
	}
	testHead, ok := testForm.Car.(*Symbol)
	if !ok {
		return none, false
	}
	testArgs, _ := ListToSlice(testForm.Cdr)
	if len(testArgs) != 2 {
		return none, false
	}
	var countSym *Symbol
	// Whether the zero iteration is folded, which the test decides.
	inclusive := false
	// Which side the counter is on, and what it is compared against.
	var bound Value
	if sym, ok := testArgs[0].(*Symbol); ok {
		countSym, bound = sym, testArgs[1]
	} else if sym, ok := testArgs[1].(*Symbol); ok {
		countSym, bound = sym, testArgs[0]
	} else {
		return none, false
	}
	switch testHead.Name {
	case "=":
		if !isZeroLiteral(bound) {
			return none, false
		}
	case "<", "<=":
		// The ordering test a count-down loop is written with.  `(< i 0)` and
		// `(<= i 0)` both stop later than `(= i 0)` — one iteration, or two —
		// but those extra iterations return the accumulator without folding
		// anything, because the counter has already gone past zero.  So the
		// answer is the same and the runtime can stop at zero.
		//
		// What is *not* the same is a bound on the other side: `(> i 0)` stops
		// before the zero iteration and would drop an element.
		if !isSmallZero(bound) {
			return none, false
		}
		inclusive = true
	default:
		return none, false
	}

	accSym, ok := then.(*Symbol)
	if !ok || accSym.Name == countSym.Name {
		return none, false
	}

	// (NAME (- N 1) FOLD)
	callForm, ok := alt.(*Pair)
	if !ok {
		return none, false
	}
	head, ok := callForm.Car.(*Symbol)
	if !ok || head.Name != name {
		return none, false
	}
	callArgs, _ := ListToSlice(callForm.Cdr)
	if len(callArgs) != 2 {
		return none, false
	}
	if !isMinusOne(callArgs[0], countSym) {
		return none, false
	}
	kind, extras, ok := recogniseCountFoldExtras(callArgs[1], accSym, countSym, inv)
	if !ok {
		return none, false
	}
	if !hasParam(formals, countSym) || !hasParam(formals, accSym) {
		return none, false
	}
	return countLoop{name: name, count: countSym, acc: accSym, kind: kind, extras: extras, inclusive: inclusive}, true
}

// isZeroLiteral reports whether a value is the literal 0.
func isZeroLiteral(v Value) bool {
	n, ok := v.(*Integer)
	return ok && isSmallEq(n, 0)
}

// isMinusOne reports whether a value is (- N 1).
func isMinusOne(v Value, n *Symbol) bool {
	p, ok := v.(*Pair)
	if !ok || !isForm(p, "-") {
		return false
	}
	args, _ := ListToSlice(p.Cdr)
	return len(args) == 2 && isSameSymbol(args[0], n) && isOne(args[1])
}

// recogniseCountFoldExtras is recogniseCountFold with the loop's invariants: the
// variables an enclosing let bound, which the fold may add in.
func recogniseCountFoldExtras(e Value, accSym, countSym *Symbol, inv map[string]bool) (loopKind, []*Symbol, bool) {
	form, ok := e.(*Pair)
	if !ok {
		return loopNone, nil, false
	}
	head, ok := form.Car.(*Symbol)
	if !ok {
		return loopNone, nil, false
	}
	args, _ := ListToSlice(form.Cdr)
	// (+ ACC x y ...) where every x is the counter or an invariant.
	if head.Name == "+" && len(args) >= 2 && isSameSymbol(args[0], accSym) {
		var extras []*Symbol
		sawCounter := false
		for _, a := range args[1:] {
			if isOne(a) {
				continue // counting: (+ acc 1)
			}
			if isSameSymbol(a, countSym) {
				sawCounter = true
				continue
			}
			sym, ok := a.(*Symbol)
			if !ok || !inv[sym.Name] {
				// Not something that provably cannot change during the loop.
				return loopNone, nil, false
			}
			extras = append(extras, sym)
		}
		// The counter does not have to appear in the fold.  `(+ acc a b)` adds
		// two invariants and never mentions the counter, and that is a perfectly
		// ordinary loop — requiring the counter made the recogniser reject the
		// shape it was written for.
		//
		// Adding only the literal one is counting, which is its own walk: it
		// adds one per iteration whatever the counter is, so the runtime does
		// not need the counter's value at all.
		if !sawCounter && len(extras) == 0 {
			return loopCount, nil, true
		}
		return loopSum, extras, true
	}
	if k, ok := recogniseCountFold(e, accSym, countSym); ok {
		return k, nil, true
	}
	return loopNone, nil, false
}

// recogniseCountFold classifies what a counting loop does with the counter.
func recogniseCountFold(e Value, accSym, countSym *Symbol) (loopKind, bool) {
	form, ok := e.(*Pair)
	if !ok {
		return loopNone, false
	}
	head, ok := form.Car.(*Symbol)
	if !ok {
		return loopNone, false
	}
	args, _ := ListToSlice(form.Cdr)
	switch head.Name {
	case "cons":
		if len(args) == 2 && isSameSymbol(args[0], countSym) && isSameSymbol(args[1], accSym) {
			return loopCollect, true
		}
	case "+":
		if len(args) != 2 {
			return loopNone, false
		}
		if isSameSymbol(args[0], accSym) && isSameSymbol(args[1], countSym) {
			return loopSum, true
		}
		if isSameSymbol(args[0], countSym) && isSameSymbol(args[1], accSym) {
			return loopSum, true
		}
		if isSameSymbol(args[0], accSym) && isOne(args[1]) {
			return loopCount, true
		}
		if isOne(args[0]) && isSameSymbol(args[1], accSym) {
			return loopCount, true
		}
	}
	return loopNone, false
}

// RunCountLoop performs a recognised counting loop.
//
// The count is an exact integer, and anything else is left to the interpreter:
// a loop whose counter is not a number is not the shape that was recognised, and
// guessing would be a compiled program computing something else.
func RunCountLoop(kind int, n, acc Value) Value {
	return RunCountLoopExtras(kind, n, acc, nil)
}

// RunCountLoopFull is the entry point the generated code uses: it carries the
// invariants and whether the zero iteration is folded.
func RunCountLoopFull(kind int, n, acc Value, extras []Value, inclusive bool) Value {
	return runCountLoop(kind, n, acc, extras, inclusive)
}

// RunCountLoopExtras is RunCountLoop with the loop's invariants.
//
// Each iteration adds the counter plus every invariant, which is what
// `(+ acc n a b)` does when `a` and `b` come from an enclosing let.  The
// invariants are passed once rather than read per iteration, which is correct
// because a let binds them once and nothing in the loop's scope can assign
// them — that is the property the recogniser checked before accepting them.
func RunCountLoopExtras(kind int, n, acc Value, extras []Value) Value {
	return runCountLoop(kind, n, acc, extras, false)
}

// runCountLoop is the walk itself, with the flag that says whether the zero
// iteration is folded.
func runCountLoop(kind int, n, acc Value, extras []Value, inclusive bool) Value {
	i, ok := n.(*Integer)
	iv, isSmall := smallInt(i)
	if !ok || !isSmall || iv < 0 {
		return acc
	}
	stop := int64(0)
	if inclusive {
		// One more iteration, the one that folds the zero.
		stop = -1
	}
	for k := iv; k > stop; k-- {
		switch kind {
		case walkSum:
			acc = NumAdd(acc, Int(k))
			for _, x := range extras {
				acc = NumAdd(acc, x)
			}
		case walkCount:
			acc = NumAdd(acc, Int(1))
		case walkCollect:
			acc = Cons(Int(k), acc)
		}
	}
	return acc
}

// emitCountLoop writes a recognised counting loop as one call into the runtime.
func (f *irFunc) emitCountLoop(w countLoop) {
	f.emitCountLoopArgs(w.kind, []irVal{
		{bits: "%p_" + w.count.Name + ".bits", tag: "%p_" + w.count.Name + ".tag"},
		{bits: "%p_" + w.acc.Name + ".bits", tag: "%p_" + w.acc.Name + ".tag"},
	}, nil, w.inclusive)
}

// ---------------------------------------------------------------------------
// Counting up
// ---------------------------------------------------------------------------

// upLoop is a loop that counts a parameter from a lower bound up to an upper
// one, which is the shape a `do` loop is usually written in:
//
//	(do ((i 0 (+ i 1)) (acc 0 (+ acc i))) ((= i n) acc))
//
// It is the mirror of the counting-down loop and needs its own recogniser,
// because the test is against a *bound* rather than against zero and the
// recursion passes `(+ i 1)` rather than `(- i 1)`.
type upLoop struct {
	name string
	idx  *Symbol
	acc  *Symbol
	kind loopKind
	// end is the bound the index counts up to.  It is an *expression* rather
	// than a name because a do loop's bound is usually a free variable — the
	// `N` in `(do ((i 0 (+ i 1)) (acc 0)) ((= i N) acc))` — and the expression is
	// what gets evaluated once, before the loop, to produce the limit.
	//
	// Evaluating it once is safe because a body that could assign it is not a
	// body this recognises: the walk has no set!, so nothing between the first
	// iteration and the last can change what the bound is.
	end Value
}

// recogniseUpLoop reports whether a procedure counts a parameter up to a bound.
//
// The accepted body is exactly
//
//	(if (= IDX END) ACC (NAME (+ IDX 1) FOLD))
//
// with FOLD one of `(+ acc IDX)`, `(+ acc 1)`, `(cons IDX acc)`.  The
// accumulator is the third parameter or the second, depending on how the loop
// was written, and both are accepted because `(do ((i 0 ...) (acc 0 ...)))` and
// `(let loop ((i 0) (acc 0)))` put them in the same order.
//
// What is *not* accepted is a loop whose bound is recomputed, or whose index is
// advanced by anything but one: those are different programs, and a recogniser
// that guessed would be compiling something else.
func recogniseUpLoop(name string, formals []*Symbol, body []Value) (upLoop, bool) {
	var none upLoop
	if len(formals) != 2 || len(body) != 1 {
		return none, false
	}
	ifForm, ok := body[0].(*Pair)
	if !ok || !isForm(ifForm, "if") {
		return none, false
	}
	parts, _ := ListToSlice(ifForm.Cdr)
	if len(parts) != 3 {
		return none, false
	}
	test, then, alt := parts[0], parts[1], parts[2]

	testForm, ok := test.(*Pair)
	if !ok || !isForm(testForm, "=") {
		return none, false
	}
	testArgs, _ := ListToSlice(testForm.Cdr)
	if len(testArgs) != 2 {
		return none, false
	}
	idxSym, ok := testArgs[0].(*Symbol)
	if !ok {
		return none, false
	}
	// The bound is whatever the index is compared with, as long as it is not the
	// index itself.
	endExpr := testArgs[1]
	if isSameSymbol(endExpr, idxSym) {
		return none, false
	}
	accSym, ok := then.(*Symbol)
	if !ok || accSym.Name == idxSym.Name {
		return none, false
	}

	callForm, ok := alt.(*Pair)
	if !ok {
		return none, false
	}
	callHead, ok := callForm.Car.(*Symbol)
	if !ok || callHead.Name != name {
		return none, false
	}
	callArgs, _ := ListToSlice(callForm.Cdr)
	if len(callArgs) != 2 {
		return none, false
	}
	if !isPlusOne(callArgs[0], idxSym) {
		return none, false
	}
	kind, ok := recogniseUpFold(callArgs[1], accSym, idxSym)
	if !ok {
		return none, false
	}
	if !hasParam(formals, idxSym) || !hasParam(formals, accSym) {
		return none, false
	}
	return upLoop{name: name, idx: idxSym, acc: accSym, kind: kind, end: endExpr}, true
}

// recogniseUpFold classifies what an upward loop does with the index.
func recogniseUpFold(e Value, accSym, idxSym *Symbol) (loopKind, bool) {
	form, ok := e.(*Pair)
	if !ok {
		return loopNone, false
	}
	head, ok := form.Car.(*Symbol)
	if !ok {
		return loopNone, false
	}
	args, _ := ListToSlice(form.Cdr)
	switch head.Name {
	case "+":
		if len(args) != 2 {
			return loopNone, false
		}
		if isSameSymbol(args[0], accSym) && isSameSymbol(args[1], idxSym) {
			return loopSum, true
		}
		if isSameSymbol(args[0], idxSym) && isSameSymbol(args[1], accSym) {
			return loopSum, true
		}
		if isSameSymbol(args[0], accSym) && isOne(args[1]) {
			return loopCount, true
		}
		if isOne(args[0]) && isSameSymbol(args[1], accSym) {
			return loopCount, true
		}
	case "cons":
		if len(args) == 2 && isSameSymbol(args[0], idxSym) && isSameSymbol(args[1], accSym) {
			return loopCollect, true
		}
	}
	return loopNone, false
}

// RunUpLoop performs a recognised upward loop.
//
// The bound and the starting index are both taken from the loop's arguments, so
// a `do` that starts at something other than zero is handled without a special
// case.
func RunUpLoop(kind int, from, end, acc Value) Value {
	lo, ok := from.(*Integer)
	loV, loSmall := smallInt(lo)
	if !ok || !loSmall {
		return acc
	}
	hi, ok := end.(*Integer)
	hiV, hiSmall := smallInt(hi)
	if !ok || !hiSmall {
		return acc
	}
	for i := loV; i < hiV; i++ {
		acc = foldOne(kind, Int(i), acc)
	}
	return acc
}
