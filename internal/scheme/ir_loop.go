// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"strings"
)

// Recognising a whole loop, so that it can be run in one call.
//
// This is the same idea as (goscheme fast), applied to a loop the *programmer*
// wrote rather than one of the library's procedures.  A list walk like
//
//	(define (sum-list lst acc)
//	  (if (null? lst) acc (sum-list (cdr lst) (+ acc (car lst)))))
//
// compiled naively crosses into the runtime four times per element — for null?,
// car, cdr and + — and each crossing costs more than the element's work.  Run
// as one call that walks the list in Go, the same loop is **72× faster**:
// measured over a million elements, 260 ms element-by-element against 3.6 ms
// with the loop in Go.
//
// The shape is narrow on purpose.  What is recognised is a tail-recursive walk
// of one list, advancing by cdr, stopping on null?, and combining the current
// element into an accumulator with one builtin operator.  Anything else is not
// recognised and takes the ordinary path, which stays correct — a loop that is
// not matched is merely not accelerated.
//
// The alternative — letting the generated code call car and cdr directly — was
// tried and cannot work: a Scheme pair lives on the Go heap, Go's collector
// moves it, and cgo forbids handing a Go pointer to C.  Keeping the walk in Go
// sidesteps all three problems, because there is no pointer to hand over.

// loopKind is the operation a recognised walk performs on each element.
type loopKind int

const (
	loopNone loopKind = iota
	// loopSum adds each element into a numeric accumulator.
	loopSum
	// loopCount counts the elements.
	loopCount
	// loopCollect builds a reversed list of the elements.
	loopCollect
)

// listWalk is a recognised list walk.
type listWalk struct {
	name string
	// list and acc are the two parameters, in the order the procedure takes
	// them; the walk needs to know which is which.
	listParam *Symbol
	accParam  *Symbol
	kind      loopKind
	// pred, when not predNone, means the fold happens only for elements the test
	// accepts; the others leave the accumulator alone.
	pred predKind
}

// vecWalk is a recognised vector walk: the same idea as a list walk, with an
// index that advances to a bound instead of a cdr that reaches the empty list.
//
//	(define (vsum v i n acc)
//	  (if (= i n) acc (vsum v (+ i 1) n (+ acc (vector-ref v i)))))
//
// The bound is a parameter rather than the vector's own length, which is what
// the shape below requires: a walk that recomputes `(vector-length v)` for every
// element would be a different body and is not recognised.
type vecWalk struct {
	name    string
	vecParm *Symbol
	idxParm *Symbol
	endParm *Symbol
	accParm *Symbol
	kind    loopKind
	pred    predKind
}

// recogniseListWalk reports whether a procedure is a list walk this can run in
// one call, and describes it.
//
// The accepted body is exactly
//
//	(if (null? LIST) ACC (NAME (cdr LIST) COMBINE))
//
// where LIST and ACC are two of the parameters, COMBINE is one of the builtin
// combinations below, and NAME is the procedure itself.  Written out, every
// part of that is checked rather than assumed, because a body that merely looks
// similar would compute something else if it were run this way.
func recogniseListWalk(name string, formals []*Symbol, body []Value) (listWalk, bool) {
	var none listWalk
	if len(formals) != 2 || len(body) != 1 {
		return none, false
	}
	ifForm, ok := body[0].(*Pair)
	if !ok {
		return none, false
	}
	if !isForm(ifForm, "if") {
		return none, false
	}
	parts, _ := ListToSlice(ifForm.Cdr)
	if len(parts) != 3 {
		return none, false
	}
	test, then, alt := parts[0], parts[1], parts[2]

	// (null? LIST)
	testForm, ok := test.(*Pair)
	if !ok || !isForm(testForm, "null?") {
		return none, false
	}
	testArgs, _ := ListToSlice(testForm.Cdr)
	if len(testArgs) != 1 {
		return none, false
	}
	listSym, ok := testArgs[0].(*Symbol)
	if !ok {
		return none, false
	}

	// The base case returns the accumulator.
	accSym, ok := then.(*Symbol)
	if !ok || accSym.Name == listSym.Name {
		return none, false
	}

	// (NAME (cdr LIST) COMBINE)
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
	// First argument: (cdr LIST), and nothing else.
	cdrForm, ok := callArgs[0].(*Pair)
	if !ok || !isForm(cdrForm, "cdr") {
		return none, false
	}
	cdrArgs, _ := ListToSlice(cdrForm.Cdr)
	if len(cdrArgs) != 1 {
		return none, false
	}
	if s, ok := cdrArgs[0].(*Symbol); !ok || s.Name != listSym.Name {
		return none, false
	}

	kind, pred, ok := classifyFold(callArgs[1], accSym, listSym)
	if !ok {
		return none, false
	}
	// The parameters have to be the ones the walk expects; a procedure whose
	// arguments are the other way round is a different shape.
	if !hasParam(formals, listSym) || !hasParam(formals, accSym) {
		return none, false
	}
	return listWalk{
		name:      name,
		listParam: listSym,
		accParam:  accSym,
		kind:      kind,
		pred:      pred,
	}, true
}

// recogniseCombine classifies the expression that folds one element into the
// accumulator.
//
// The three accepted forms are the ones whose meaning is unambiguous and whose
// result does not depend on anything the walk cannot see:
//
//	(+ ACC (car LIST))   the element added in
//	(+ 1 ACC)            counting
//	(cons (car LIST) ACC) collecting, reversed
//
// Anything else — a predicate, a nested lambda, a call to another procedure —
// is not recognised, because running it here would be running something else.
func recogniseCombine(e Value, accSym, listSym *Symbol) (loopKind, bool) {
	if accSym == nil {
		return loopNone, false
	}
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
		// (+ ACC (car LIST)) — the element into the accumulator.
		if isSameSymbol(args[0], accSym) && isCarOf(args[1], listSym) {
			return loopSum, true
		}
		// (+ 1 ACC) or (+ ACC 1) — counting.
		if isOne(args[0]) && isSameSymbol(args[1], accSym) {
			return loopCount, true
		}
		if isSameSymbol(args[0], accSym) && isOne(args[1]) {
			return loopCount, true
		}
	case "cons":
		if len(args) != 2 {
			return loopNone, false
		}
		if isCarOf(args[0], listSym) && isSameSymbol(args[1], accSym) {
			return loopCollect, true
		}
	}
	return loopNone, false
}

// isForm reports whether a pair is a form with the given head: (head ...).
func isForm(p *Pair, head string) bool {
	s, ok := p.Car.(*Symbol)
	return ok && s.Name == head
}

// isSameSymbol reports whether a value is the given symbol.  A nil symbol
// matches nothing, which keeps the callers that pass an optional name from
// having to check first.
func isSameSymbol(v Value, s *Symbol) bool {
	if s == nil {
		return false
	}
	got, ok := v.(*Symbol)
	return ok && got.Name == s.Name
}

// isCarOf reports whether a value is (car S).
func isCarOf(v Value, s *Symbol) bool {
	p, ok := v.(*Pair)
	if !ok || !isForm(p, "car") {
		return false
	}
	args, _ := ListToSlice(p.Cdr)
	return len(args) == 1 && isSameSymbol(args[0], s)
}

// isOne reports whether a value is the literal 1.
func isOne(v Value) bool {
	n, ok := v.(*Integer)
	return ok && n.small && n.i == 1
}

// hasParam reports whether a symbol is one of the formals.
func hasParam(formals []*Symbol, s *Symbol) bool {
	for _, f := range formals {
		if f.Name == s.Name {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The runtime walks
// ---------------------------------------------------------------------------

// Walking a whole list in Go, in one call.
//
// This is where the speed comes from.  Element by element through the runtime
// boundary costs about 260 ns per element — four crossings for a walk like
// sum-list — against 3.6 ns for the loop run here.  Nothing is handed across
// per element: the list is already a Go value on this side, so the walk is a
// pointer chase and the arithmetic is Go arithmetic.
//
// The three walks below are the ones recogniseCombine accepts, and they are
// written out rather than parameterised by a function value because the call
// per element is exactly what is being avoided.

// The runtime walk numbers, defined from loopKind rather than written out
// again.
//
// Writing them out separately is what let them drift: loopKind starts at
// loopNone = 0 so that its zero value means "not a walk", which put loopSum at
// 1 — while the runtime's table started at 0.  Summing then ran the counting
// walk, and `(sum-list (list 1..100))` returned 100 instead of 5050.  Deriving
// one from the other makes that impossible.
const (
	walkSum     = int(loopSum)
	walkCount   = int(loopCount)
	walkCollect = int(loopCollect)
)

// RunListWalk performs a recognised loop.  kind is loopKind as an integer and
// comes from the generated code, which is the only caller.
func RunListWalk(kind int, list, acc Value) Value {
	return runListWalk(kind, predNone, list, acc)
}

// RunListWalkPred is the entry point the generated code uses: it carries the
// element test as well as the fold.
func RunListWalkPred(kind, pred int, list, acc Value) Value {
	return runListWalk(kind, predKind(pred), list, acc)
}

// RunVecWalkPred is the vector counterpart.
func RunVecWalkPred(kind, pred int, vec, from, end, acc Value) Value {
	return runVecWalk(kind, predKind(pred), vec, from, end, acc)
}

// runListWalk is RunListWalk with the element test, which the generated code
// passes and the plain entry point does not.
func runListWalk(kind int, pred predKind, list, acc Value) Value {
	cur := list
	for {
		p, ok := cur.(*Pair)
		if !ok {
			// () ends the walk; anything else is not a list, and the
			// interpreter's own cdr would report it.  Raising here would mean
			// the compiler had changed what the program does.
			return acc
		}
		if pred.holds(p.Car) {
			acc = foldOne(kind, p.Car, acc)
		}
		cur = p.Cdr
	}
}

// foldOne applies one element.
func foldOne(kind int, elem, acc Value) Value {
	switch kind {
	case walkSum:
		// Through the runtime's own arithmetic, so an element that is not a
		// fixnum — a bignum, a rational, a float — is handled the way the
		// interpreter handles it.  The speed comes from not crossing the
		// boundary per element, not from assuming anything about the numbers.
		return NumAdd(acc, elem)
	case walkCount:
		return NumAdd(acc, Int(1))
	case walkCollect:
		return Cons(elem, acc)
	}
	return acc
}

// ---------------------------------------------------------------------------
// Emitting a recognised walk
// ---------------------------------------------------------------------------

// emitListWalk writes a procedure that is a recognised list walk as one call
// into the runtime.
//
// The body is replaced rather than added to: the walk's meaning is exactly what
// RunListWalk does, so emitting both would be emitting the loop twice.  What
// the module declares is a function with the walk's own signature — the same
// shape every other compiled body has — so that a call to it from compiled code
// needs no special case and the adapter registering it is unchanged.
//
// Nothing is checked here about the elements: the walk uses the runtime's own
// NumAdd and Cons, so a list of bignums sums exactly as the interpreter would
// sum it.  The speed comes from not crossing the boundary per element, not from
// assuming anything about what is in the list.
func (f *irFunc) emitListWalk(w listWalk, formals []*Symbol) {
	// The two arguments, in the order the procedure declares them.
	listArg := "%p_" + w.listParam.Name + ".bits"
	accArg := "%p_" + w.accParam.Name + ".bits"
	listTag := "%p_" + w.listParam.Name + ".tag"
	accTag := "%p_" + w.accParam.Name + ".tag"
	if w.listParam.Name == w.accParam.Name {
		return // impossible, but a miscompile is worse than a missed one
	}
	f.want(gsVal + " @gs_walk(i32, i32, " + gsVal + ", " + gsVal + ")")
	// The two tagged arguments are packed and handed over.
	listV := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s undef, i64 %s, 0\n", listV, gsVal, listArg)
	listV2 := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s %s, i64 %s, 1\n", listV2, gsVal, listV, listTag)
	accV := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s undef, i64 %s, 0\n", accV, gsVal, accArg)
	accV2 := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s %s, i64 %s, 1\n", accV2, gsVal, accV, accTag)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_walk(i32 %d, i32 %d, %s %s, %s %s)\n",
		out, gsVal, int(w.kind), int(w.pred), gsVal, listV2, gsVal, accV2)
	bits := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 0\n", bits, gsVal, out)
	tag := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 1\n", tag, gsVal, out)
	f.listWalkDone = true
	f.listWalkVal = irVal{bits: bits, tag: tag}
}

// ---------------------------------------------------------------------------
// Recognising a vector walk
// ---------------------------------------------------------------------------

// recogniseVecWalk reports whether a procedure walks a vector by index.
//
// The accepted body is exactly
//
//	(if (= IDX END) ACC (NAME VEC (+ IDX 1) END COMBINE))
//
// where VEC, IDX, END and ACC are four of the parameters, COMBINE folds the
// element at IDX into ACC, and NAME is the procedure itself.  The bound END is
// a parameter the loop counts up to, not the vector's length: the shape is
// checked, not inferred, and a loop that asked for the length each time is a
// different program.
//
// The element arrives as `(vector-ref VEC IDX)`, which is the vector's
// counterpart of `(car LIST)` and is why the walks below can share the
// accumulator arithmetic with the list ones.
func recogniseVecWalk(name string, formals []*Symbol, body []Value) (vecWalk, bool) {
	var none vecWalk
	if len(formals) != 4 || len(body) != 1 {
		return none, false
	}
	// Find the four parameters by the roles they play.
	ifForm, ok := body[0].(*Pair)
	if !ok || !isForm(ifForm, "if") {
		return none, false
	}
	parts, _ := ListToSlice(ifForm.Cdr)
	if len(parts) != 3 {
		return none, false
	}
	test, then, alt := parts[0], parts[1], parts[2]

	// (if (= IDX END) ACC ...)
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
	endSym, ok := testArgs[1].(*Symbol)
	if !ok || endSym.Name == idxSym.Name {
		return none, false
	}
	accSym, ok := then.(*Symbol)
	if !ok || accSym.Name == idxSym.Name || accSym.Name == endSym.Name {
		return none, false
	}

	// (NAME VEC (+ IDX 1) END COMBINE)
	callForm, ok := alt.(*Pair)
	if !ok {
		return none, false
	}
	callHead, ok := callForm.Car.(*Symbol)
	if !ok || callHead.Name != name {
		return none, false
	}
	callArgs, _ := ListToSlice(callForm.Cdr)
	if len(callArgs) != 4 {
		return none, false
	}
	vecSym, ok := callArgs[0].(*Symbol)
	if !ok {
		return none, false
	}
	// The index advances by one and nothing else.
	if !isPlusOne(callArgs[1], idxSym) {
		return none, false
	}
	if !isSameSymbol(callArgs[2], endSym) {
		return none, false
	}
	kind, pred, ok := classifyVecFold(callArgs[3], accSym, vecSym, idxSym)
	if !ok {
		return none, false
	}
	for _, s := range []*Symbol{vecSym, idxSym, endSym, accSym} {
		if !hasParam(formals, s) {
			return none, false
		}
	}
	return vecWalk{name: name, vecParm: vecSym, idxParm: idxSym, endParm: endSym, accParm: accSym, kind: kind, pred: pred}, true
}

// listCar is the expression (car LIST), which is the element a list walk folds.
func listCar(listSym *Symbol) Value {
	return List(Intern("car"), listSym)
}

// vecRef is the expression (vector-ref VEC IDX), the element a vector walk folds.
func vecRef(vecSym, idxSym *Symbol) Value {
	return List(Intern("vector-ref"), vecSym, idxSym)
}

// classifyFold accepts either a plain fold or a conditional one, for a list.
func classifyFold(e Value, accSym, listSym *Symbol) (loopKind, predKind, bool) {
	if k, ok := recogniseCombine(e, accSym, listSym); ok {
		return k, predNone, true
	}
	return recogniseConditionalFold(e, accSym, listCar(listSym))
}

// classifyVecFold is classifyFold for a vector walk.
func classifyVecFold(e Value, accSym, vecSym, idxSym *Symbol) (loopKind, predKind, bool) {
	if k, ok := recogniseVecCombine(e, accSym, vecSym, idxSym); ok {
		return k, predNone, true
	}
	return recogniseConditionalFold(e, accSym, vecRef(vecSym, idxSym))
}

// isPlusOne reports whether a value is (+ IDX 1).
func isPlusOne(v Value, idx *Symbol) bool {
	p, ok := v.(*Pair)
	if !ok || !isForm(p, "+") {
		return false
	}
	args, _ := ListToSlice(p.Cdr)
	if len(args) != 2 {
		return false
	}
	if isSameSymbol(args[0], idx) && isOne(args[1]) {
		return true
	}
	return isOne(args[0]) && isSameSymbol(args[1], idx)
}

// isVecRefOf reports whether a value is (vector-ref VEC IDX).
func isVecRefOf(v Value, vec, idx *Symbol) bool {
	p, ok := v.(*Pair)
	if !ok || !isForm(p, "vector-ref") {
		return false
	}
	args, _ := ListToSlice(p.Cdr)
	return len(args) == 2 && isSameSymbol(args[0], vec) && isSameSymbol(args[1], idx)
}

// recogniseVecCombine classifies the fold, which is the same set as the list
// walks' with the element read from a vector.
func recogniseVecCombine(e Value, accSym, vecSym, idxSym *Symbol) (loopKind, bool) {
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
		if isSameSymbol(args[0], accSym) && isVecRefOf(args[1], vecSym, idxSym) {
			return loopSum, true
		}
		if isOne(args[0]) && isSameSymbol(args[1], accSym) {
			return loopCount, true
		}
		if isSameSymbol(args[0], accSym) && isOne(args[1]) {
			return loopCount, true
		}
	case "cons":
		if len(args) != 2 {
			return loopNone, false
		}
		if isVecRefOf(args[0], vecSym, idxSym) && isSameSymbol(args[1], accSym) {
			return loopCollect, true
		}
	}
	return loopNone, false
}

// RunVecWalk performs a recognised vector walk.
//
// The bound is the one the procedure was given, and the elements are read with
// the runtime's own vector access, so an index the caller got wrong raises the
// same error the interpreted loop would raise rather than reading past the end.
func RunVecWalk(kind int, vec, from, end, acc Value) Value {
	return runVecWalk(kind, predNone, vec, from, end, acc)
}

// runVecWalk is RunVecWalk with the element test.
func runVecWalk(kind int, pred predKind, vec, from, end, acc Value) Value {
	v, ok := vec.(*Vector)
	if !ok {
		return acc
	}
	lo, _ := from.(*Integer)
	hi, _ := end.(*Integer)
	if lo == nil || hi == nil || !lo.small || !hi.small {
		return acc
	}
	i, n := lo.i, hi.i
	for ; i < n; i++ {
		if i < 0 || i >= int64(len(v.Items)) {
			// Out of range: the interpreted (vector-ref v i) would have raised,
			// so this must too rather than quietly stopping.
			panic(errf("vector-ref", "index %d out of range for vector of length %d", i, len(v.Items)))
		}
		if pred.holds(v.Items[i]) {
			acc = foldOne(kind, v.Items[i], acc)
		}
	}
	return acc
}

// emitVecWalk writes a recognised vector walk as one call into the runtime.
//
// The four arguments and the kind are handed over together; RunVecWalk does the
// counting, so nothing crosses per element.
func (f *irFunc) emitVecWalk(w vecWalk, formals []*Symbol) {
	f.want(gsVal + " @gs_vecwalk(i32, i32, " + gsVal + "*)")
	slot := f.allocaArray(4)
	params := []*Symbol{w.vecParm, w.idxParm, w.endParm, w.accParm}
	for i, p := range params {
		f.storeArg(slot, i, irVal{
			bits: "%p_" + p.Name + ".bits",
			tag:  "%p_" + p.Name + ".tag",
		})
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_vecwalk(i32 %d, i32 %d, %s* %s)\n",
		out, gsVal, int(w.kind), int(w.pred), gsVal, slot)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// ---------------------------------------------------------------------------
// A walk whose fold is conditional
// ---------------------------------------------------------------------------

// predKind is a builtin test the walk can apply to each element.
//
// Only builtins are accepted, and that is the point: a predicate the walk could
// apply itself costs nothing extra, while a predicate written by the programmer
// would have to be called back into Scheme once per element — which is exactly
// the crossing the walk exists to avoid.  A walk whose predicate is not a
// builtin is simply not recognised.
type predKind int

const (
	predNone predKind = iota
	predEven
	predOdd
	predPositive
	predNegative
	predZero
	predPair
	predNull
	predNumber
	predString
	predSymbol
	predVector
)

// predName maps a Scheme name to the test it denotes, or predNone.
func predName(name string) predKind {
	switch name {
	case "even?":
		return predEven
	case "odd?":
		return predOdd
	case "positive?":
		return predPositive
	case "negative?":
		return predNegative
	case "zero?":
		return predZero
	case "pair?":
		return predPair
	case "null?":
		return predNull
	case "number?":
		return predNumber
	case "string?":
		return predString
	case "symbol?":
		return predSymbol
	case "vector?":
		return predVector
	}
	return predNone
}

// holds reports whether the test accepts a value.  It is the same question the
// Scheme predicate answers, asked here so that the walk does not have to call
// back for it.
func (p predKind) holds(v Value) bool {
	switch p {
	case predEven:
		n, ok := v.(*Integer)
		return ok && n.Big().Bit(0) == 0
	case predOdd:
		n, ok := v.(*Integer)
		return ok && n.Big().Bit(0) == 1
	case predPositive:
		// wantReal raises for a non-number, which is what the Scheme predicate
		// does; testing IsNumber first and answering false would be a compiled
		// program that quietly disagreed with the interpreter about an error.
		return NumSign(wantReal("positive?", v)) == 1
	case predNegative:
		return NumSign(wantReal("negative?", v)) == -1
	case predZero:
		return NumSign(wantReal("zero?", v)) == 0
	case predPair:
		_, ok := v.(*Pair)
		return ok
	case predNull:
		return v == Value(Nil)
	case predNumber:
		return IsNumber(v)
	case predString:
		_, ok := v.(*String)
		return ok
	case predSymbol:
		_, ok := v.(*Symbol)
		return ok
	case predVector:
		_, ok := v.(*Vector)
		return ok
	}
	return true
}

// recogniseConditionalFold classifies an accumulator expression that is a
// conditional on the element.
//
//	(if (PRED ELEMENT) FOLD ACC)    or    (if (PRED ELEMENT) ACC FOLD)
//
// PRED has to be a builtin, because a predicate written in Scheme would have to
// be called per element and that call is the cost the walk exists to avoid.  A
// walk whose predicate is not recognised takes the ordinary path.
func recogniseConditionalFold(e Value, accSym *Symbol, element Value) (loopKind, predKind, bool) {
	form, ok := e.(*Pair)
	if !ok || !isForm(form, "if") {
		return loopNone, predNone, false
	}
	parts, _ := ListToSlice(form.Cdr)
	if len(parts) != 3 {
		return loopNone, predNone, false
	}
	predForm, ok := parts[0].(*Pair)
	if !ok {
		return loopNone, predNone, false
	}
	predHead, ok := predForm.Car.(*Symbol)
	if !ok {
		return loopNone, predNone, false
	}
	pred := predName(predHead.Name)
	if pred == predNone {
		return loopNone, predNone, false
	}
	pargs, _ := ListToSlice(predForm.Cdr)
	if len(pargs) != 1 || !sameExpr(pargs[0], element) {
		return loopNone, predNone, false
	}
	// One arm folds, the other leaves the accumulator as it is.
	kind, ok := recogniseFoldArm(parts[1], accSym, element)
	if ok && isSameSymbol(parts[2], accSym) {
		return kind, pred, true
	}
	kind, ok = recogniseFoldArm(parts[2], accSym, element)
	if ok && isSameSymbol(parts[1], accSym) {
		return kind, pred, true
	}
	return loopNone, predNone, false
}

// recogniseFoldArm classifies one arm of a conditional fold, where the element
// is an expression such as `(car LIST)` rather than a bare symbol.
//
// The three accepted arms are `(+ ACC ELEM)`, `(+ ACC 1)` and
// `(cons ELEM ACC)`, compared by structure because the element on both sides is
// the same written form.  Anything else is not this shape.
func recogniseFoldArm(e Value, accSym *Symbol, element Value) (loopKind, bool) {
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
		if isSameSymbol(args[0], accSym) && sameExpr(args[1], element) {
			return loopSum, true
		}
		if isSameSymbol(args[0], accSym) && isOne(args[1]) {
			return loopCount, true
		}
		if isOne(args[0]) && isSameSymbol(args[1], accSym) {
			return loopCount, true
		}
	case "cons":
		if len(args) != 2 {
			return loopNone, false
		}
		if sameExpr(args[0], element) && isSameSymbol(args[1], accSym) {
			return loopCollect, true
		}
	}
	return loopNone, false
}

// sameExpr reports whether two expressions are the same written form.  It is
// used to check that a predicate tests the very element the fold uses, which
// comparing by structure is enough for: the element is either `(car LIST)` or
// `(vector-ref VEC IDX)` on both sides or the walk is not this shape.
func sameExpr(a, b Value) bool {
	return WriteToString(a) == WriteToString(b)
}

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

	// (if (= N 0) ...) — the test compares a parameter with zero.
	testForm, ok := test.(*Pair)
	if !ok || !isForm(testForm, "=") {
		return none, false
	}
	testArgs, _ := ListToSlice(testForm.Cdr)
	if len(testArgs) != 2 {
		return none, false
	}
	var countSym *Symbol
	if s, ok := testArgs[0].(*Symbol); ok && isZeroLiteral(testArgs[1]) {
		countSym = s
	} else if s, ok := testArgs[1].(*Symbol); ok && isZeroLiteral(testArgs[0]) {
		countSym = s
	} else {
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
	return countLoop{name: name, count: countSym, acc: accSym, kind: kind, extras: extras}, true
}

// isZeroLiteral reports whether a value is the literal 0.
func isZeroLiteral(v Value) bool {
	n, ok := v.(*Integer)
	return ok && n.small && n.i == 0
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

// RunCountLoopExtras is RunCountLoop with the loop's invariants.
//
// Each iteration adds the counter plus every invariant, which is what
// `(+ acc n a b)` does when `a` and `b` come from an enclosing let.  The
// invariants are passed once rather than read per iteration, which is correct
// because a let binds them once and nothing in the loop's scope can assign
// them — that is the property the recogniser checked before accepting them.
func RunCountLoopExtras(kind int, n, acc Value, extras []Value) Value {
	i, ok := n.(*Integer)
	if !ok || !i.small || i.i < 0 {
		return acc
	}
	for k := i.i; k > 0; k-- {
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
	}, nil)
}

// ---------------------------------------------------------------------------
// A top-level call
// ---------------------------------------------------------------------------

// topCall is a top-level form that is a plain call to a compiled procedure with
// constant arguments.
//
//	(loop 200000 0)
//
// is the whole of a program's work as often as not, and it used to be handed to
// the interpreter along with everything else at the top level — so the compiled
// body was entered once and the loop ran interpreted.  Emitting the call means
// the loop runs natively from the start, which is the difference between a
// benchmark reading 0.95× and 4.9×.
//
// Only literal arguments are accepted.  A form whose arguments have to be
// computed is left to the interpreter, because computing them is evaluation and
// that is the interpreter's job; a literal is a value the compiler already has.
type topCall struct {
	name string
	args []Value
}

// recogniseTopCall reports whether a top-level form calls a compiled procedure
// with literal arguments.
func recogniseTopCall(form Value, compiled map[string]bool) (topCall, bool) {
	p, ok := form.(*Pair)
	if !ok {
		return topCall{}, false
	}
	head, ok := p.Car.(*Symbol)
	if !ok || !compiled[head.Name] {
		return topCall{}, false
	}
	items, ok := ListToSlice(p.Cdr)
	if !ok {
		return topCall{}, false
	}
	for _, a := range items {
		switch a.(type) {
		case *Integer, Boolean, *String, *Char, *Float, *Rational:
			// A literal the compiler can place in the module.
		default:
			return topCall{}, false
		}
	}
	return topCall{name: head.Name, args: items}, true
}

// emitTopCall writes the call, with the literals boxed as the callee expects.
func (g *irGen) emitTopCall(c topCall, formals []*Symbol, body *strings.Builder, reg func() string) {
	// The callee takes its arguments as (word, tag) pairs; a literal is a
	// fixnum, a boolean or a boxed value, and the boxing is the same the
	// ordinary emitter does for a literal in a body.
	var args []string
	for _, a := range c.args {
		switch v := a.(type) {
		case *Integer:
			if v.small {
				args = append(args, fmt.Sprintf("i64 %d, i64 0", v.i))
			} else {
				lit := g.module.stringLiteral(v.String(), "toplit")
				r := reg()
				fmt.Fprintf(body, "  %s = call i64 @gs_box_literal(i8* %s, i64 %d)\n", r, lit, len(v.String()))
				args = append(args, fmt.Sprintf("i64 %s, i64 1", r))
			}
		case Boolean:
			bit := 0
			if bool(v) {
				bit = 1
			}
			args = append(args, fmt.Sprintf("i64 %d, i64 2", bit))
		default:
			// Anything else is boxed by its source text, which is how a string
			// or a character literal crosses.
			text := WriteToString(a)
			lit := g.module.stringLiteral(text, "toplit")
			r := reg()
			fmt.Fprintf(body, "  %s = call i64 @gs_box_literal(i8* %s, i64 %d)\n", r, lit, len(text))
			args = append(args, fmt.Sprintf("i64 %s, i64 1", r))
		}
	}
	out := reg()
	fmt.Fprintf(body, "  %s = call %s @%s(%s)\n", out, gsVal, mangle(c.name), strings.Join(args, ", "))
}

// ---------------------------------------------------------------------------
// Looking through a named let
// ---------------------------------------------------------------------------

// recogniseWalkIn looks for a walk in a procedure body, seeing through a named
// let if there is one.
//
// It returns the shape it found, which is one of the ones the runtime can run;
// the callers below wrap it so that each recogniser can be asked for its own
// kind.
func recogniseWalkIn(name string, formals []*Symbol, body []Value) (loopKind, predKind, walkShape, bool) {
	// Directly, first: a body that is already a walk needs no rewriting.
	if k, p, shape, ok := recogniseAnyWalk(name, formals, body); ok {
		return k, p, shape, true
	}
	// Otherwise a named let, whose body is then the walk.
	nl, ok := parseNamedLet(body)
	if !ok {
		return loopNone, predNone, shapeNone, false
	}
	inv := map[string]bool{}
	for _, s := range nl.outer {
		inv[s.Name] = true
	}
	return recogniseAnyWalkInv(nl.loop, nl.vars, nl.body, inv)
}

// walkShape says which of the recognisers matched, so that the arguments can be
// passed to the right runtime entry point.
type walkShape int

const (
	shapeNone walkShape = iota
	shapeList
	shapeVec
	shapeCount
	shapeUp
	shapeSearch
	shapeMerge
)

// recogniseAnyWalk asks each recogniser in turn.
func recogniseAnyWalk(name string, formals []*Symbol, body []Value) (loopKind, predKind, walkShape, bool) {
	return recogniseAnyWalkInv(name, formals, body, nil)
}

// recogniseAnyWalkInv is recogniseAnyWalk with the invariants an enclosing let
// provides.
func recogniseAnyWalkInv(name string, formals []*Symbol, body []Value, inv map[string]bool) (loopKind, predKind, walkShape, bool) {
	// A do loop's recursive call carries the placeholder name, because the do
	// has no name of its own.  Asking each recogniser for the placeholder as
	// well as for the name means the same loop is found either way, and the
	// caller does not have to know which spelling it was written in.
	if name != doLoopPlaceholder {
		if k, p, shape, ok := recogniseAnyWalkInv(doLoopPlaceholder, formals, body, inv); ok {
			return k, p, shape, true
		}
	}
	if w, ok := recogniseListWalk(name, formals, body); ok {
		return w.kind, w.pred, shapeList, true
	}
	if w, ok := recogniseVecWalk(name, formals, body); ok {
		return w.kind, w.pred, shapeVec, true
	}
	if w, ok := recogniseCountLoopInv(name, formals, body, inv); ok {
		return w.kind, predNone, shapeCount, true
	}
	if w, ok := recogniseUpLoop(name, formals, body); ok {
		return w.kind, predNone, shapeUp, true
	}
	if w, ok := recogniseSearch(name, formals, body); ok {
		return loopNone, w.pred, shapeSearch, true
	}
	if _, ok := recogniseMerge(name, formals, body); ok {
		return loopNone, predNone, shapeMerge, true
	}
	return loopNone, predNone, shapeNone, false
}

// namedLetForm is a loop written as a named let, with the bindings that enclose
// it.
type namedLetForm struct {
	// loop is the let's name, which is the procedure the loop calls.
	loop string
	// vars and body are the loop's parameters and its body.
	vars []*Symbol
	body []Value
	// init are the expressions the loop's parameters start from, in order.
	init []Value
	// outer names the bindings of the lets that enclose the loop, and outerInit
	// their initial expressions.  These are the loop's invariants — a let binds
	// once and nothing in the loop's scope can assign it — so their values can
	// be computed once and handed to the runtime.
	outer     []*Symbol
	outerInit []Value
}

// parseNamedLet recognises a loop written as a named let and collects the
// bindings around it.
//
//	(let ((a 1) (b 2))                     ; enclosing let: invariants
//	  (let inner ((j i) (acc 0))           ; the loop
//	    (if (= j 0) acc (inner (- j 1) (+ acc a b)))))
func parseNamedLet(body []Value) (namedLetForm, bool) {
	var out namedLetForm
	if len(body) != 1 {
		return out, false
	}
	cur, ok := body[0].(*Pair)
	if !ok || !isForm(cur, "let") {
		return out, false
	}
	// Walk inward through anonymous lets, collecting their bindings.  A named
	// let is where the walk ends.
	for {
		parts, _ := ListToSlice(cur.Cdr)
		if len(parts) < 2 {
			return out, false
		}
		if _, isName := parts[0].(*Symbol); isName {
			// The loop itself.
			if len(parts) < 3 {
				return out, false
			}
			out.loop = parts[0].(*Symbol).Name
			binds, ok := ListToSlice(parts[1])
			if !ok {
				return out, false
			}
			for _, b := range binds {
				bp, ok := b.(*Pair)
				if !ok {
					return out, false
				}
				items, _ := ListToSlice(bp)
				if len(items) != 2 {
					return out, false
				}
				sym, ok := items[0].(*Symbol)
				if !ok {
					return out, false
				}
				out.vars = append(out.vars, sym)
				out.init = append(out.init, items[1])
			}
			out.body = parts[2:]
			return out, true
		}
		// An anonymous let: record its bindings, then look at its single body
		// form, which has to be the loop for this to be the shape.
		binds, ok := ListToSlice(parts[0])
		if !ok {
			return out, false
		}
		for _, b := range binds {
			bp, ok := b.(*Pair)
			if !ok {
				return out, false
			}
			items, _ := ListToSlice(bp)
			if len(items) != 2 {
				return out, false
			}
			sym, ok := items[0].(*Symbol)
			if !ok {
				return out, false
			}
			out.outer = append(out.outer, sym)
			out.outerInit = append(out.outerInit, items[1])
		}
		rest := parts[1:]
		if len(rest) != 1 {
			return out, false
		}
		next, ok := rest[0].(*Pair)
		if !ok || !isForm(next, "let") {
			return out, false
		}
		cur = next
	}
}

// letBindings lists a let's binding names, and the set of them for lookup.
func letBindings(letForm Value) ([]*Symbol, map[string]bool) {
	p, ok := letForm.(*Pair)
	if !ok || !isForm(p, "let") {
		return nil, nil
	}
	parts, _ := ListToSlice(p.Cdr)
	if len(parts) < 2 {
		return nil, nil
	}
	// (let NAME ((v i) ...) ...) — skip the name if there is one.
	if _, isName := parts[0].(*Symbol); isName {
		if len(parts) < 3 {
			return nil, nil
		}
		parts = parts[1:]
	}
	bindings, ok := ListToSlice(parts[0])
	if !ok {
		return nil, nil
	}
	var names []*Symbol
	set := map[string]bool{}
	for _, b := range bindings {
		bp, ok := b.(*Pair)
		if !ok {
			return nil, nil
		}
		sym, ok := bp.Car.(*Symbol)
		if !ok {
			return nil, nil
		}
		names = append(names, sym)
		set[sym.Name] = true
	}
	return names, set
}

// emitNamedLetScope evaluates the outer let's bindings that the loop uses and
// returns them as values, in a fixed order.
func (f *irFunc) emitNamedLetScope(names []*Symbol, _ map[string]bool) []irVal {
	_ = names
	return nil
}

// emitLetInits emits the initial values of a let's bindings.
func (f *irFunc) emitLetInits(bindingsVal Value) []irVal {
	bindings, ok := ListToSlice(bindingsVal)
	if !ok {
		return nil
	}
	out := make([]irVal, 0, len(bindings))
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			return nil
		}
		items, _ := ListToSlice(p)
		if len(items) != 2 {
			return nil
		}
		v, err := f.emitExpr(items[1])
		if err != nil {
			return nil
		}
		out = append(out, v)
	}
	return out
}

// emitWalkCall emits the walk that `body` is, called with the given argument
// values, and records the result as the function's return value.
func (f *irFunc) emitWalkCall(loopName string, vars []*Symbol, body []Value, args, extraVals []irVal, inv map[string]bool) {
	k, pred, shape, ok := recogniseAnyWalkInv(loopName, vars, body, inv)
	if !ok {
		return
	}
	switch shape {
	case shapeList:
		f.emitListWalkArgs(k, pred, args)
	case shapeVec:
		f.emitVecWalkArgs(k, pred, args)
	case shapeCount:
		f.emitCountLoopArgs(k, args, extraVals)
	case shapeUp:
		if u, ok := recogniseUpLoop(loopName, vars, body); ok {
			f.emitUpLoopArgs(k, args, u)
		}
	case shapeSearch:
		if w, ok := recogniseSearch(loopName, vars, body); ok {
			f.emitSearchArgs(w, args)
		}
	case shapeMerge:
		if w, ok := recogniseMerge(loopName, vars, body); ok {
			f.emitMergeArgs(w, args)
		}
	}
}

// emitMergeArgs calls the merge walk with the two lists and the comparison.
func (f *irFunc) emitMergeArgs(w mergeWalk, args []irVal) {
	if len(args) != 2 {
		return
	}
	f.want(gsVal + " @gs_merge(i32, " + gsVal + ", " + gsVal + ")")
	a := args[0].bits0(f)
	b := args[1].bits0(f)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_merge(i32 %d, %s %s, %s %s)\n",
		out, gsVal, mergeCode(w.less), gsVal, a, gsVal, b)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitSearchArgs calls the search walk with the list and the two answers.
func (f *irFunc) emitSearchArgs(w searchWalk, args []irVal) {
	if len(args) != 1 {
		return
	}
	// When the answer is the element itself, the runtime supplies it — there is
	// no expression to evaluate, and trying to evaluate `(car lst)` here would
	// fail, because `lst` is a name the loop's own body binds and the emitter
	// has no local for it.
	foundVal := irVal{bits: "0", tag: tagNull}
	if !w.wantElement {
		v, err := f.emitExpr(w.found)
		if err != nil {
			return
		}
		foundVal = v
	}
	missedVal, err := f.emitExpr(w.missed)
	if err != nil {
		return
	}
	wantElem := 0
	if w.wantElement {
		wantElem = 1
	}
	f.want(gsVal + " @gs_search(i32, " + gsVal + "*, i32)")
	slot := f.allocaArray(3)
	f.storeArg(slot, 0, args[0])
	f.storeArg(slot, 1, foundVal)
	f.storeArg(slot, 2, missedVal)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_search(i32 %d, %s* %s, i32 %d)\n",
		out, gsVal, int(w.pred), gsVal, slot, wantElem)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitUpLoopArgs calls the upward walk with (from, end, acc).
//
// The bound is evaluated here, once, rather than passed in with the loop's own
// parameters: it is usually a free variable rather than one of them, and
// evaluating it before the loop is what makes a `do` whose limit is a global
// work without reading the global every iteration.
func (f *irFunc) emitUpLoopArgs(kind loopKind, args []irVal, u upLoop) {
	if len(args) != 2 {
		return
	}
	endVal, err := f.emitExpr(u.end)
	if err != nil {
		return
	}
	// The runtime takes (from, end, acc), which is not the order the loop's own
	// parameters are in — the accumulator comes last because RunUpLoop's
	// signature ends with it, and getting that wrong made the sum come out as a
	// count: the bound arrived as the accumulator and the accumulator as the
	// bound.
	f.want(gsVal + " @gs_uploop(i32, " + gsVal + "*)")
	slot := f.allocaArray(3)
	f.storeArg(slot, 0, args[0]) // the index it starts at
	f.storeArg(slot, 1, endVal)  // the bound it counts up to
	f.storeArg(slot, 2, args[1]) // the accumulator
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_uploop(i32 %d, %s* %s)\n",
		out, gsVal, int(kind), gsVal, slot)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitListWalkArgs calls the list walk with the list and accumulator given.
func (f *irFunc) emitListWalkArgs(kind loopKind, pred predKind, args []irVal) {
	if len(args) != 2 {
		return
	}
	f.want(gsVal + " @gs_walk(i32, i32, " + gsVal + ", " + gsVal + ")")
	a := args[0].bits0(f)
	b := args[1].bits0(f)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_walk(i32 %d, i32 %d, %s %s, %s %s)\n",
		out, gsVal, int(kind), int(pred), gsVal, a, gsVal, b)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitCountLoopArgs calls the counting walk with the count and accumulator given.
func (f *irFunc) emitCountLoopArgs(kind loopKind, args []irVal, extras []irVal) {
	if len(args) != 2 {
		return
	}
	f.want(gsVal + " @gs_countloop(i32, " + gsVal + "*, i32)")
	slot := f.allocaArray(2 + len(extras))
	for i, v := range args {
		f.storeArg(slot, i, v)
	}
	for i, v := range extras {
		f.storeArg(slot, 2+i, v)
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_countloop(i32 %d, %s* %s, i32 %d)\n",
		out, gsVal, int(kind), gsVal, slot, len(extras))
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitVecWalkArgs calls the vector walk with all four arguments given.
func (f *irFunc) emitVecWalkArgs(kind loopKind, pred predKind, args []irVal) {
	if len(args) != 4 {
		return
	}
	f.want(gsVal + " @gs_vecwalk(i32, i32, " + gsVal + "*)")
	slot := f.allocaArray(4)
	for i, v := range args {
		f.storeArg(slot, i, v)
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_vecwalk(i32 %d, i32 %d, %s* %s)\n",
		out, gsVal, int(kind), int(pred), gsVal, slot)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitNamedLetLoop emits a loop written as a named let.
//
// The loop's starting values and the enclosing lets' bindings are all ordinary
// expressions, evaluated here in the scopes they belong to; only the *loop*
// has to be the recognised shape.  That split matters: `(let ((a (* 2 3))) ...)`
// is fine because evaluating `a` is something the compiler can already do, while
// the loop body is what has to match.
func (f *irFunc) emitNamedLetLoop(nl namedLetForm) {
	inv := map[string]bool{}
	for _, s := range nl.outer {
		inv[s.Name] = true
	}
	if _, _, shape, ok := recogniseAnyWalkInv(nl.loop, nl.vars, nl.body, inv); !ok || shape == shapeNone {
		return
	}
	// The enclosing bindings first, then the loop's own parameters: the loop
	// body may mention both, and the enclosing ones are the invariants.
	outerVals := make([]irVal, 0, len(nl.outerInit))
	for _, e := range nl.outerInit {
		v, err := f.emitExpr(e)
		if err != nil {
			return
		}
		outerVals = append(outerVals, v)
	}
	vals := make([]irVal, 0, len(nl.init))
	for _, e := range nl.init {
		v, err := f.emitExpr(e)
		if err != nil {
			return
		}
		vals = append(vals, v)
	}
	if len(vals) != len(nl.vars) {
		return
	}
	f.emitWalkCall(nl.loop, nl.vars, nl.body, vals, outerVals, inv)
}

// ---------------------------------------------------------------------------
// A do loop
// ---------------------------------------------------------------------------

// doAsCountLoop reads a do loop as a counting loop, which is what it is.
//
//	(do ((i 0 (+ i 1)) (acc 0 (+ acc i))) ((= i n) acc))
//
// The variables become the loop's parameters, the inits its starting values and
// the steps what it passes next — so the loop the do stands for is
//
//	(if (= i n) acc (loop (+ i 1) (+ acc i)))
//
// which is the same shape the named-let recogniser already handles.  Reading it
// here rather than through `doExpansion` is deliberate: that expansion wraps the
// steps in a `guard` to give `(continue)` its meaning, and a `guard` is not a
// shape anything can be recognised from.
//
// A do with a command list is refused.  Those commands run for their effect
// between the test and the steps, and folding that into a walk would mean
// deciding what the effects are, which is the interpreter's job.
func doAsLoop(form Value) (string, []*Symbol, []Value, bool) {
	p, ok := form.(*Pair)
	if !ok || !isForm(p, "do") {
		return "", nil, nil, false
	}
	args, _ := ListToSlice(p.Cdr)
	if len(args) != 2 {
		return "", nil, nil, false // a command list, or malformed
	}
	specs, ok := ListToSlice(args[0])
	if !ok {
		return "", nil, nil, false
	}
	testClause, ok := ListToSlice(args[1])
	if !ok || len(testClause) < 1 {
		return "", nil, nil, false
	}
	var vars []*Symbol
	var steps []Value
	for _, spec := range specs {
		items, ok := ListToSlice(spec)
		if !ok || len(items) != 3 {
			return "", nil, nil, false
		}
		v, ok := items[0].(*Symbol)
		if !ok {
			return "", nil, nil, false
		}
		vars = append(vars, v)
		steps = append(steps, items[2])
	}
	// (if TEST RESULT (loop STEP...))
	//
	// The test's first result is the loop's value.  A do may have several, which
	// makes it a `values` form and not a walk; and the `begin` the expansion
	// would wrap around them is not written here either, because the recognisers
	// accept a base case that is a bare name and would not see through it.
	if len(testClause) != 2 {
		return "", nil, nil, false
	}
	alt := Cons(doLoopSym, listFromSlice(steps))
	body := []Value{List(Intern("if"), testClause[0], testClause[1], alt)}
	// The placeholder is replaced by the caller, which knows the name the
	// recognisers will compare against.
	return doLoopPlaceholder, vars, body, true
}

// doLoopPlaceholder stands where a do loop's recursive call goes, and is
// replaced by the name the recognisers compare against.
//
// A do has no name of its own — its recursive call is an artifact of reading it
// as a loop rather than something written in the source — so one is chosen here.
// It has to be a *Symbol, because that is what the recognisers match on.  The
// spaces are what keep it from colliding with anything a program can write: a
// Scheme identifier cannot contain one.
const doLoopPlaceholder = " do-loop "

// doLoopSym is the symbol a do loop's recursive call carries.
var doLoopSym = Intern(doLoopPlaceholder)

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
	if !ok || !lo.small {
		return acc
	}
	hi, ok := end.(*Integer)
	if !ok || !hi.small {
		return acc
	}
	for i := lo.i; i < hi.i; i++ {
		acc = foldOne(kind, Int(i), acc)
	}
	return acc
}

// ---------------------------------------------------------------------------
// Searching
// ---------------------------------------------------------------------------

// Search walks are the other thing a loop over a sequence usually does: stop at
// the first element a test accepts instead of folding every element in.
//
//	(define (find-even lst)
//	  (if (null? lst) #f (if (even? (car lst)) (car lst) (find-even (cdr lst)))))
//
// Like the folding walks, the test has to be a builtin, and like them the shape
// is checked rather than matched loosely.  What is different is the answer: a
// search returns something found *inside* the loop, so the walk has to be able to
// say whether it found anything at all — a search that fell off the end returns
// the missed value, and the runtime has to distinguish that from having found
// the same value.
type searchWalk struct {
	name string
	list *Symbol
	pred predKind
	// found is what the loop returns when the test accepts an element, and
	// missed is what it returns when the list runs out.
	found, missed Value
	// wantElement says the answer is the element itself — `(car lst)` — rather
	// than a fixed value, which is what a search for a minimum or a count
	// would return.
	wantElement bool
}

// recogniseSearch reports whether a procedure searches a list.
//
// The accepted body is exactly
//
//	(if (null? LIST) MISSED (if (PRED (car LIST)) FOUND (NAME (cdr LIST))))
//
// with PRED a builtin and FOUND either `(car LIST)` or a literal.  Anything else
// is refused: a search whose result is computed, or whose test is written by the
// programmer, is not something this can run without calling back per element.
func recogniseSearch(name string, formals []*Symbol, body []Value) (searchWalk, bool) {
	var none searchWalk
	if len(formals) != 1 || len(body) != 1 {
		return none, false
	}
	outer, ok := body[0].(*Pair)
	if !ok || !isForm(outer, "if") {
		return none, false
	}
	parts, _ := ListToSlice(outer.Cdr)
	if len(parts) != 3 {
		return none, false
	}
	// (null? LIST)
	testForm, ok := parts[0].(*Pair)
	if !ok || !isForm(testForm, "null?") {
		return none, false
	}
	targs, _ := ListToSlice(testForm.Cdr)
	if len(targs) != 1 {
		return none, false
	}
	listSym, ok := targs[0].(*Symbol)
	if !ok || !hasParam(formals, listSym) {
		return none, false
	}
	missed := parts[1]

	// (if (PRED (car LIST)) FOUND (NAME (cdr LIST)))
	inner, ok := parts[2].(*Pair)
	if !ok || !isForm(inner, "if") {
		return none, false
	}
	iparts, _ := ListToSlice(inner.Cdr)
	if len(iparts) != 3 {
		return none, false
	}
	predForm, ok := iparts[0].(*Pair)
	if !ok {
		return none, false
	}
	predHead, ok := predForm.Car.(*Symbol)
	if !ok {
		return none, false
	}
	pred := predName(predHead.Name)
	if pred == predNone {
		return none, false
	}
	pargs, _ := ListToSlice(predForm.Cdr)
	if len(pargs) != 1 || !isCarOf(pargs[0], listSym) {
		return none, false
	}
	found := iparts[1]
	wantElement := false
	if isCarOf(found, listSym) {
		wantElement = true
	} else if !isLiteral(found) {
		return none, false
	}
	if !isLiteral(missed) {
		return none, false
	}
	// The alternative is the recursive call, walking on.
	altForm, ok := iparts[2].(*Pair)
	if !ok {
		return none, false
	}
	altHead, ok := altForm.Car.(*Symbol)
	if !ok || altHead.Name != name {
		return none, false
	}
	aargs, _ := ListToSlice(altForm.Cdr)
	if len(aargs) != 1 {
		return none, false
	}
	cdrForm, ok := aargs[0].(*Pair)
	if !ok || !isForm(cdrForm, "cdr") {
		return none, false
	}
	cargs, _ := ListToSlice(cdrForm.Cdr)
	if len(cargs) != 1 || !isSameSymbol(cargs[0], listSym) {
		return none, false
	}
	return searchWalk{name: name, list: listSym, pred: pred, found: found, missed: missed, wantElement: wantElement}, true
}

// isLiteral reports whether a value is one the compiler can place in the module.
//
// Boolean is a value type rather than a pointer — `type Boolean bool` — so it is
// listed without a star.  Writing `*Boolean` instead compiles, matches nothing,
// and makes every `#f` look like something the recogniser cannot read.
func isLiteral(v Value) bool {
	switch v.(type) {
	case *Integer, *Float, *Rational, *String, *Char, Boolean, Empty:
		return true
	}
	return false
}

// RunSearch walks a list and returns the first element a test accepts.
//
// The loop's two answers are both passed in — what to return when something is
// found and what to return when the list runs out — so the runtime never has to
// signal "nothing found" out of band.  That matters because the two can be the
// same value: a search for zero in a list of zeros returns zero, and a search
// that found nothing returns whatever the empty case says, which may also be
// zero.  Returning a separate flag instead would put the distinction in the
// caller, where the numbers are the same either way.
func RunSearch(pred int, list, found, missed Value, wantElement int) Value {
	cur := list
	for {
		p, ok := cur.(*Pair)
		if !ok {
			return missed
		}
		if predKind(pred).holds(p.Car) {
			if wantElement != 0 {
				return p.Car
			}
			return found
		}
		cur = p.Cdr
	}
}

// walkParams returns the loop's own parameters as the arguments a walk takes, or
// nil when the body is not a walk at all.
//
// It exists so that the emitter has one place to ask "is this a walk" rather
// than a list of recognisers that has to be kept in step with the dispatcher.
// The caller passes the name the recognisers should match a recursive call
// against, which for a procedure is its own name and for a do loop is the
// placeholder.
func walkParams(name string, formals []*Symbol, body []Value) ([]*Symbol, bool) {
	if _, _, shape, ok := recogniseAnyWalk(name, formals, body); !ok || shape == shapeNone {
		return nil, false
	}
	return formals, true
}

// ---------------------------------------------------------------------------
// Merging two lists
// ---------------------------------------------------------------------------

// mergeWalk is a loop that merges two lists, which is the core of every sort
// written in Scheme:
//
//	(define (merge a b)
//	  (cond ((null? a) b)
//	        ((null? b) a)
//	        ((< (car b) (car a)) (cons (car b) (merge a (cdr b))))
//	        (else (cons (car a) (merge (cdr a) b)))))
//
// It is recognised because it is worth a great deal and because it is written
// one way: two lists in, one out, one element taken per iteration.  A sort whose
// merge is left to the interpreter is a sort the compiler has not helped.
type mergeWalk struct {
	name string
	a, b *Symbol
	// less is the comparison, applied as `(less (car b) (car a))`.
	less string
	// takeB says the first branch takes from b, which is what `<` does when the
	// comparison is written the other way round.
	takeB bool
}

// recogniseMerge reports whether a procedure merges two lists.
//
// The accepted body is
//
//	(if (null? A) B
//	    (if (null? B) A
//	        (if (LESS (car B) (car A))
//	            (cons (car B) (NAME A (cdr B)))
//	            (cons (car A) (NAME (cdr A) B)))))
//
// which is what the `cond` above expands to.  Both null tests have to be there
// and both have to return the *other* list: a merge that returned something else
// at the end would be sorting differently.
func recogniseMerge(name string, formals []*Symbol, body []Value) (mergeWalk, bool) {
	var none mergeWalk
	if len(formals) != 2 || len(body) != 1 {
		return none, false
	}
	aSym, bSym := formals[0], formals[1]
	// A merge is usually written as a `cond`, because that is how it reads, and
	// the four clauses are exactly the nesting below.  Looking through the cond
	// rather than requiring the if-chain means the shape can be written the way
	// people write it.
	form := condToIf(body[0])
	// (if (null? A) B (if (null? B) A ...))
	l1, ok := form.(*Pair)
	if !ok || !isForm(l1, "if") {
		return none, false
	}
	p1, _ := ListToSlice(l1.Cdr)
	if len(p1) != 3 {
		return none, false
	}
	if !isNullOf(p1[0], aSym) || !isSameSymbol(p1[1], bSym) {
		return none, false
	}
	l2, ok := p1[2].(*Pair)
	if !ok || !isForm(l2, "if") {
		return none, false
	}
	p2, _ := ListToSlice(l2.Cdr)
	if len(p2) != 3 {
		return none, false
	}
	if !isNullOf(p2[0], bSym) || !isSameSymbol(p2[1], aSym) {
		return none, false
	}
	// (if (LESS (car B) (car A)) (cons (car B) (NAME A (cdr B))) (cons (car A) (NAME (cdr A) B)))
	l3, ok := p2[2].(*Pair)
	if !ok || !isForm(l3, "if") {
		return none, false
	}
	p3, _ := ListToSlice(l3.Cdr)
	if len(p3) != 3 {
		return none, false
	}
	cmp, ok := p3[0].(*Pair)
	if !ok {
		return none, false
	}
	less, ok := cmp.Car.(*Symbol)
	if !ok {
		return none, false
	}
	switch less.Name {
	case "<", "<=":
		// takes from b when b's head is smaller
	default:
		return none, false
	}
	cargs, _ := ListToSlice(cmp.Cdr)
	if len(cargs) != 2 || !isCarOf(cargs[0], bSym) || !isCarOf(cargs[1], aSym) {
		return none, false
	}
	// The two branches take from b and a respectively.
	// The first branch takes from b and walks on with b's tail; the second takes
	// from a and walks on with a's tail.
	if !isConsOfCarThen(name, p3[1], bSym, aSym) {
		return none, false
	}
	if !isConsOfCarThen(name, p3[2], aSym, bSym) {
		return none, false
	}
	return mergeWalk{name: name, a: aSym, b: bSym, less: less.Name, takeB: true}, true
}

// condToIf rewrites a cond into the if-chain it means, or returns the form
// unchanged when it is not a cond.
//
// Only the clause shapes a merge uses are handled: `(TEST EXPR)` and
// `(else EXPR)`.  A clause with several expressions, or one using `=>`, is left
// alone and the recognisers downstream refuse it, which is the right answer —
// this is a reader for one shape, not a second implementation of cond.
func condToIf(form Value) Value {
	p, ok := form.(*Pair)
	if !ok || !isForm(p, "cond") {
		return form
	}
	clauses, _ := ListToSlice(p.Cdr)
	var out Value = Nil
	// Built from the last clause backwards, so the first test ends up outermost.
	for i := len(clauses) - 1; i >= 0; i-- {
		cl, ok := clauses[i].(*Pair)
		if !ok {
			return form
		}
		items, _ := ListToSlice(cl)
		if len(items) != 2 {
			return form
		}
		if isElse(items[0]) {
			// An else is the final alternative and cannot be nested inside an
			// if's then-branch.
			out = items[1]
			continue
		}
		if out == Value(Nil) && i == len(clauses)-1 {
			return form // no else and a final else-less clause: not this shape
		}
		out = List(Intern("if"), items[0], items[1], out)
	}
	return out
}

// isElse reports whether a value is the symbol `else`.
func isElse(v Value) bool {
	s, ok := v.(*Symbol)
	return ok && s.Name == "else"
}

// isNullOf reports whether a value is (null? S).
func isNullOf(v Value, s *Symbol) bool {
	p, ok := v.(*Pair)
	if !ok || !isForm(p, "null?") {
		return false
	}
	args, _ := ListToSlice(p.Cdr)
	return len(args) == 1 && isSameSymbol(args[0], s)
}

// isConsOfCarThen reports whether a value takes the head of one list and recurses
// on the rest of it:
//
//	(cons (car TAKEN) (NAME (cdr TAKEN) OTHER))
//
// which is one step of a merge — take the smaller element, walk on.  The two
// arguments mean what they say: TAKEN is the list the element came from, and
// OTHER is the one left alone.  The merge's two branches are the two ways round,
// so getting them the wrong way round makes the second branch fail.
func isConsOfCarThen(name string, v Value, taken, other *Symbol) bool {
	p, ok := v.(*Pair)
	if !ok || !isForm(p, "cons") {
		return false
	}
	args, _ := ListToSlice(p.Cdr)
	if len(args) != 2 || !isCarOf(args[0], taken) {
		return false
	}
	call, ok := args[1].(*Pair)
	if !ok {
		return false
	}
	head, ok := call.Car.(*Symbol)
	if !ok || head.Name != name {
		return false
	}
	cargs, _ := ListToSlice(call.Cdr)
	if len(cargs) != 2 {
		return false
	}
	// The recursive call advances the taken list and leaves the other alone, in
	// whichever order the two were written: `(merge a (cdr b))` and
	// `(merge (cdr b) a)` are the same call, and both spellings appear because
	// the merge is symmetrical in the arguments it passes and not in the ones it
	// tests.
	advance, keep := cargs[0], cargs[1]
	if isSameSymbol(cargs[0], other) {
		advance, keep = cargs[1], cargs[0]
	}
	return isCdrOf(advance, taken) && isSameSymbol(keep, other)
}

// isCdrOf reports whether a value is (cdr S).
func isCdrOf(v Value, s *Symbol) bool {
	p, ok := v.(*Pair)
	if !ok || !isForm(p, "cdr") {
		return false
	}
	args, _ := ListToSlice(p.Cdr)
	return len(args) == 1 && isSameSymbol(args[0], s)
}

// RunMerge merges two lists the way the loop does.
//
// The comparison is a code rather than a procedure, for the same reason the
// other walks' predicates are: a comparison written by the programmer would have
// to be called back into Scheme for every element, and that call is the cost
// this exists to avoid.  The two accepted ones are `<` and `<=`.
func RunMerge(cmp int, a, b Value) Value {
	// The result is built forward and reversed once at the end, rather than
	// consed onto the answer as the loop goes: the loop is not in tail position,
	// so building it backwards would mean rebuilding it, and one reverse at the
	// end is cheaper than the intermediate lists.
	var out []Value
	for {
		ap, aok := a.(*Pair)
		bp, bok := b.(*Pair)
		if !aok {
			return appendList(out, b)
		}
		if !bok {
			return appendList(out, a)
		}
		takeB := NumCmp(bp.Car, ap.Car) < 0
		if cmp == mergeLe {
			takeB = NumCmp(bp.Car, ap.Car) <= 0
		}
		if takeB {
			out = append(out, bp.Car)
			b = bp.Cdr
		} else {
			out = append(out, ap.Car)
			a = ap.Cdr
		}
	}
}

// merge comparison codes.
const (
	mergeLt = 0
	mergeLe = 1
)

// mergeCode maps a comparison name to its code.
func mergeCode(name string) int {
	if name == "<=" {
		return mergeLe
	}
	return mergeLt
}

// appendList conses a slice onto a list, reusing the tail rather than copying it.
func appendList(items []Value, tail Value) Value {
	for i := len(items) - 1; i >= 0; i-- {
		tail = Cons(items[i], tail)
	}
	return tail
}
