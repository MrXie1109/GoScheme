// SPDX-License-Identifier: MIT

package scheme

import "fmt"

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

	kind, ok := recogniseCombine(callArgs[1], accSym, listSym)
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

// isSameSymbol reports whether a value is the given symbol.
func isSameSymbol(v Value, s *Symbol) bool {
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
	switch kind {
	case walkSum:
		return walkSumInto(list, acc)
	case walkCount:
		return walkCountInto(list, acc)
	case walkCollect:
		return walkCollectInto(list, acc)
	}
	return acc
}

// walkSumInto adds every element of list to acc.
//
// The accumulator is carried through NumAdd rather than added as a machine
// integer, so an element that is not a fixnum — a bignum, a rational, a float —
// is handled the way the interpreter would handle it.  The loop is fast because
// it does not cross a boundary per element, not because it assumes anything
// about the numbers.
func walkSumInto(list, acc Value) Value {
	cur := list
	for {
		p, ok := cur.(*Pair)
		if !ok {
			// () ends the walk; anything else is not a list, and the
			// interpreter's own car would report it.  Raising here would mean
			// the compiler had changed what the program does.
			return acc
		}
		acc = NumAdd(acc, p.Car)
		cur = p.Cdr
	}
}

// walkCountInto adds one for each element.
func walkCountInto(list, acc Value) Value {
	cur := list
	for {
		p, ok := cur.(*Pair)
		if !ok {
			return acc
		}
		acc = NumAdd(acc, Int(1))
		cur = p.Cdr
	}
}

// walkCollectInto conses each element onto acc, which reverses the list — the
// same thing the Scheme loop does.
func walkCollectInto(list, acc Value) Value {
	cur := list
	for {
		p, ok := cur.(*Pair)
		if !ok {
			return acc
		}
		acc = Cons(p.Car, acc)
		cur = p.Cdr
	}
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
	f.want(gsVal + " @gs_walk(i32, " + gsVal + ", " + gsVal + ")")
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
	fmt.Fprintf(&f.body, "  %s = call %s @gs_walk(i32 %d, %s %s, %s %s)\n",
		out, gsVal, int(w.kind), gsVal, listV2, gsVal, accV2)
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
	kind, ok := recogniseVecCombine(callArgs[3], accSym, vecSym, idxSym)
	if !ok {
		return none, false
	}
	for _, s := range []*Symbol{vecSym, idxSym, endSym, accSym} {
		if !hasParam(formals, s) {
			return none, false
		}
	}
	return vecWalk{name: name, vecParm: vecSym, idxParm: idxSym, endParm: endSym, accParm: accSym, kind: kind}, true
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
		switch kind {
		case walkSum:
			acc = NumAdd(acc, v.Items[i])
		case walkCount:
			acc = NumAdd(acc, Int(1))
		case walkCollect:
			acc = Cons(v.Items[i], acc)
		}
	}
	return acc
}

// emitVecWalk writes a recognised vector walk as one call into the runtime.
//
// The four arguments and the kind are handed over together; RunVecWalk does the
// counting, so nothing crosses per element.
func (f *irFunc) emitVecWalk(w vecWalk, formals []*Symbol) {
	f.want(gsVal + " @gs_vecwalk(i32, " + gsVal + "*)")
	slot := f.allocaArray(4)
	params := []*Symbol{w.vecParm, w.idxParm, w.endParm, w.accParm}
	for i, p := range params {
		f.storeArg(slot, i, irVal{
			bits: "%p_" + p.Name + ".bits",
			tag:  "%p_" + p.Name + ".tag",
		})
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_vecwalk(i32 %d, %s* %s)\n",
		out, gsVal, int(w.kind), gsVal, slot)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}
