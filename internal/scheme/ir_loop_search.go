// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

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

// isQuoted reports whether a value is a quoted literal — `'()` above all, which
// is what a list-building loop returns when it runs out.
//
// The empty list is the reason this exists: `'()` reads as `(quote ())`, a pair,
// so it is neither a literal nor a name, and a recogniser that only knew those
// two would refuse every `append` and `copy-list` ever written.
func isQuoted(v Value) bool {
	p, ok := v.(*Pair)
	if !ok || !isForm(p, "quote") {
		return false
	}
	args, _ := ListToSlice(p.Cdr)
	return len(args) == 1 && isLiteral(args[0])
}

// isLiteral reports whether a value is one the compiler can place in the module.
//
// Boolean is a value type rather than a pointer — `type Boolean bool` — so it is
// listed without a star.  Writing `*Boolean` instead compiles, matches nothing,
// and makes every `#f` look like something the recogniser cannot read.
func isLiteral(v Value) bool {
	switch v.(type) {
	case *Integer, Float, *Rational, *String, Char, Boolean, Empty:
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
