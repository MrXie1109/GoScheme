// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

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
