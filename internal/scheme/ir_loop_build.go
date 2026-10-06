// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// ---------------------------------------------------------------------------
// Building a list from a list
// ---------------------------------------------------------------------------

// buildWalk is a loop that copies a list, or maps it, or appends to it:
//
//	(define (copy a)   (if (null? a) '()      (cons (car a) (copy (cdr a)))))
//	(define (append a b) (if (null? a) b      (cons (car a) (append (cdr a) b))))
//
// The recursive call is *not* in tail position — the cons happens after it
// returns — so this is a different shape from the folding walks, and it needs
// its own runtime entry point rather than a fold.  It is worth having because
// append, copy-list and a map over a builtin are all written this way, and a
// program that appends in a loop is otherwise crossing the boundary twice per
// element.
type buildWalk struct {
	name string
	list *Symbol
	// tail is what the loop returns when the list runs out: `'()` for a copy,
	// the second parameter for an append.
	tail Value
	// elem is the head of each consed pair — `(car LIST)` — or a builtin applied
	// to it, which is what makes a map.
	elem Value
	// wantElement says the element is the head itself rather than the result of
	// a call.
	wantElement bool
	// mapPred is the builtin applied to the element, or predNone.
	mapPred predKind
	// op is how the element and the recursive result are combined: `cons` for a
	// list, `+` or `*` for a sum or a product.
	//
	//	(cons (car l) (f (cdr l)))     a list
	//	(+ (car l) (f (cdr l)))        a sum
	//	(* (car l) (f (cdr l)))        a product
	//
	// All three are the same shape — a non-tail recursion that combines the
	// element with what the rest of the list produced — and they differ only in
	// the operator, which is why one recogniser covers them and the runtime is
	// told which.
	op string
	// filter says the list keeps the elements the test accepts rather than
	// mapping the test over them:
	//
	//	(filter even? lst)  vs  (map even? lst)
	//
	// Both cons onto a recursive call and both are written with a builtin, so
	// they are the same shape up to one nesting — and they mean different
	// things, which is why the recogniser distinguishes them rather than
	// guessing.
	filter bool
}

// recogniseBuild reports whether a procedure builds a list by walking another.
//
// The accepted body is exactly
//
//	(if (null? LIST) TAIL (cons ELEM (NAME (cdr LIST) ...)))
//
// where TAIL is a literal or another parameter and ELEM is `(car LIST)` or a
// builtin applied to it.  A map whose function is written by the programmer is
// refused, because applying it would mean calling back into Scheme per element.
func recogniseBuild(name string, formals []*Symbol, body []Value) (buildWalk, bool) {
	var none buildWalk
	if len(body) != 1 || len(formals) < 1 {
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
	if !isNullOf(parts[0], formals[0]) {
		return none, false
	}
	listSym := formals[0]
	// The tail has to be a literal or one of the other parameters, so that it is
	// a value the caller already has rather than something the loop computes.
	tail := parts[1]
	if !isLiteral(tail) && !isQuoted(tail) && !hasParam(formals, asSymbol(tail)) {
		return none, false
	}
	// A filter nests one more if around the cons:
	//
	//	(if (PRED (car LIST)) (cons (car LIST) (NAME (cdr LIST))) (NAME (cdr LIST)))
	if f, ok := recogniseFilterTail(parts[2], name, listSym); ok {
		f.tail = tail
		return f, true
	}
	// (OP SOMETHING (NAME (cdr LIST) ...)) for one of the accepted operators.
	combine, ok := parts[2].(*Pair)
	if !ok {
		return none, false
	}
	combineHead, ok := combine.Car.(*Symbol)
	if !ok {
		return none, false
	}
	switch combineHead.Name {
	case "cons", "+", "*":
	default:
		return none, false
	}
	cargs, _ := ListToSlice(combine.Cdr)
	if len(cargs) != 2 {
		return none, false
	}
	// The recursive call is the second operand for `cons` and either for the
	// arithmetic: `(+ (car l) (f (cdr l)))` and `(+ (f (cdr l)) (car l))` are the
	// same sum, and both are written.
	elem, call := cargs[0], cargs[1]
	if _, isCall := call.(*Pair); !isCall || !isRecurOnCdr(call, name, listSym) {
		elem, call = cargs[1], cargs[0]
	}
	if !isRecurOnCdr(call, name, listSym) {
		return none, false
	}
	// The element: the head of the list, or a builtin applied to it.
	w := buildWalk{name: name, list: listSym, tail: tail, op: combineHead.Name}
	if isCarOf(elem, listSym) {
		w.wantElement = true
		return w, true
	}
	elemForm, ok := elem.(*Pair)
	if !ok {
		return none, false
	}
	elemHead, ok := elemForm.Car.(*Symbol)
	if !ok {
		return none, false
	}
	pred := predName(elemHead.Name)
	if pred == predNone {
		return none, false
	}
	eargs, _ := ListToSlice(elemForm.Cdr)
	if len(eargs) != 1 || !isCarOf(eargs[0], listSym) {
		return none, false
	}
	w.mapPred = pred
	return w, true
}

// recogniseFilterTail matches the body of a filter, given the list and the name
// the recursive call goes by.
//
//	(if (PRED (car LIST))
//	    (cons (car LIST) (NAME (cdr LIST)))
//	    (NAME (cdr LIST)))
//
// The two branches have to recurse on the same thing and agree about which
// element is kept; a version that consed an element the test rejected would be a
// different function and is not recognised.
func recogniseFilterTail(v Value, name string, listSym *Symbol) (buildWalk, bool) {
	var none buildWalk
	inner, ok := v.(*Pair)
	if !ok || !isForm(inner, "if") {
		return none, false
	}
	parts, _ := ListToSlice(inner.Cdr)
	if len(parts) != 3 {
		return none, false
	}
	predForm, ok := parts[0].(*Pair)
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
	// The accepted branch conses the head and recurses.
	keep, ok := parts[1].(*Pair)
	if !ok || !isForm(keep, "cons") {
		return none, false
	}
	kargs, _ := ListToSlice(keep.Cdr)
	if len(kargs) != 2 || !isCarOf(kargs[0], listSym) || !isRecurOnCdr(kargs[1], name, listSym) {
		return none, false
	}
	// The rejected branch recurses and keeps nothing.
	if !isRecurOnCdr(parts[2], name, listSym) {
		return none, false
	}
	return buildWalk{name: name, list: listSym, mapPred: pred, filter: true}, true
}

// isRecurOnCdr reports whether a value is `(NAME (cdr LIST) ...)`.
func isRecurOnCdr(v Value, name string, listSym *Symbol) bool {
	p, ok := v.(*Pair)
	if !ok {
		return false
	}
	head, ok := p.Car.(*Symbol)
	if !ok || head.Name != name {
		return false
	}
	args, _ := ListToSlice(p.Cdr)
	return len(args) >= 1 && isCdrOf(args[0], listSym)
}

// asSymbol returns a value as a symbol, or nil.
func asSymbol(v Value) *Symbol {
	s, _ := v.(*Symbol)
	return s
}

// RunBuild copies a list, optionally mapping a builtin over it, and puts TAIL at
// the end.
//
// The walk is not tail recursive in Scheme, so the runtime builds the result in
// one pass rather than recursing — which is the whole gain, because the Scheme
// version's stack frames are what a compiled copy should not have to pay for.
func RunBuild(pred int, list, tail Value, mode int) Value {
	// The elements are collected first and combined afterwards, which is what
	// makes a non-tail recursion cheap: the Scheme version holds a frame open
	// until the end of the list and then combines on the way back, and the
	// combining is the same either way round for the operators accepted here —
	// `cons` builds the same list, and `+` and `*` are associative and
	// commutative over the numbers a program is likely to use.
	//
	// A product over a list containing a zero comes out the same; a product
	// containing a non-number raises the same error, from NumMul rather than
	// from the Scheme `*` — the same condition either way.
	var out []Value
	cur := list
	for {
		p, ok := cur.(*Pair)
		if !ok {
			break
		}
		switch mode {
		case buildMap:
			// `(map even? lst)` is a list of booleans: the mapped value is what
			// the predicate returns, not the element it accepted.
			out = append(out, BooleanOf(predKind(pred).holds(p.Car)))
		case buildFilter:
			// `(filter even? lst)` keeps the elements, not the answers.
			if predKind(pred).holds(p.Car) {
				out = append(out, p.Car)
			}
		default:
			// A copy, a sum and a product all take the element as it is; what
			// differs is what is done with the collected elements afterwards.
			out = append(out, p.Car)
		}
		cur = p.Cdr
	}
	switch mode {
	case buildCopy, buildMap, buildFilter:
		return appendList(out, tail)
	case buildSum:
		acc := tail
		for _, v := range out {
			acc = NumAdd(acc, v)
		}
		return acc
	case buildProduct:
		acc := tail
		for _, v := range out {
			acc = NumMul(acc, v)
		}
		return acc
	}
	return appendList(out, tail)
}

// The three modes of a build walk, as the generated code passes them.
const (
	buildCopy    = 0
	buildMap     = 1
	buildFilter  = 2
	buildSum     = 3
	buildProduct = 4
)
