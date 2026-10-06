// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// Eliminating calls that provably compute an argument they were given.
//
// The compiler emits a call into the runtime for every procedure it has no
// inline rule for, and each crossing costs between 50 and 100 nanoseconds —
// more than a hundred times what the called procedure usually does.  The
// interpreter has no such cost, so a body whose only work is one runtime call
// ran *slower* compiled than interpreted, and the cost rule in ir_pure.go
// refused such bodies rather than emitting them.
//
// That rule was treating a symptom.  Reading the IR is what shows the disease:
// `(car (list acc))` inside a loop emitted two crossings, two `alloca`s and
// four `store`s per iteration, for an expression that is *the identity*.  The
// argument `acc` comes out of `car` unchanged, so the whole thing should be
// `acc` and nothing at all should be emitted.  `opt -O2` cannot find this: the
// two calls are opaque external functions with memory side effects, so LLVM has
// no way to know one undoes the other.  Only the generator knows, because only
// the generator knows what these names mean.
//
// So the generator eliminates them.  What is recognised is a call whose result
// is *exactly one of its arguments*, structurally:
//
//	(car (list X))            =>  X        a list of one element is X
//	(cdr (cons A B))          =>  B        the tail of a cons is B
//	(car (cons A B))          =>  A
//	(vector-ref (vector X) 0) =>  X
//	(+ X 0)  (- X 0)  (* X 1) =>  X        and the mirror images
//
// **Evaluation is preserved, which is what keeps this honest.**  The arguments
// that remain are still evaluated, in the order Scheme evaluates arguments, and
// the ones that belong only to the eliminated constructor are still evaluated
// too: `(car (list (f) (g)))` is not a shape this accepts, but if it were, the
// rule is that dropping the constructor must not drop the work its arguments
// did.  So the substitution returns the *kept* argument and a list of
// expressions that must still be evaluated for their effect, in order.
//
// The result is that `(car (list acc))` becomes `acc` with nothing to evaluate
// first, while `(car (list (display "x") acc))` — which has a side effect in the
// dropped part — is not accepted at all, because the shape below requires the
// constructor to have exactly the arguments that make the identity hold.
//
// Nothing here is a general optimiser.  It is a list of identities whose two
// sides are equal by definition, checked structurally against the source
// expression, so that a program cannot tell whether it ran.

// identity returns the argument a call's result is equal to, when the call is
// one of the recognised identities, together with any expressions that still
// have to be evaluated before that argument's value is used.
//
// The second result is always empty for the shapes accepted here, and is
// returned anyway because it is the thing a reader would otherwise have to
// re-derive from the shape checks: an identity is only sound when the work in
// the elided half is empty.
func identity(op string, args []Value) (Value, []Value, bool) {
	switch op {
	case "car":
		if len(args) != 1 {
			return nil, nil, false
		}
		// (car (list X)) is X, provided the list has one element.
		if inner, ok := listOfOne(args[0], "list"); ok {
			return inner, nil, true
		}
		// (car (cons A B)) is A.  B is discarded by the car, so it is not
		// evaluated — which is what the interpreter does too, and is why this
		// one is allowed to drop an argument outright.
		if a, _, ok := consParts(args[0]); ok {
			return a, nil, true
		}
		return nil, nil, false
	case "cdr":
		if len(args) != 1 {
			return nil, nil, false
		}
		// (cdr (cons A B)) is B, and A was evaluated to build the cons, so it
		// stays as work to do first.
		if a, b, ok := consParts(args[0]); ok {
			return b, []Value{a}, true
		}
		return nil, nil, false
	case "vector-ref":
		if len(args) != 2 {
			return nil, nil, false
		}
		if !isZeroLiteral(args[1]) {
			return nil, nil, false
		}
		// (vector-ref (vector X) 0) is X.
		if inner, ok := listOfOne(args[0], "vector"); ok {
			return inner, nil, true
		}
		return nil, nil, false
	case "+", "-":
		if len(args) != 2 {
			return nil, nil, false
		}
		// (+ X 0) and (- X 0) are X.  A zero on the left is not the same fold
		// for `-`, which is not commutative.
		if isZeroLiteral(args[1]) {
			return args[0], nil, true
		}
		if op == "+" && isZeroLiteral(args[0]) {
			return args[1], nil, true
		}
		return nil, nil, false
	case "*":
		if len(args) != 2 {
			return nil, nil, false
		}
		// (* X 1) is X, and 1 is the identity for both sides.
		if isOneLiteral(args[1]) {
			return args[0], nil, true
		}
		if isOneLiteral(args[0]) {
			return args[1], nil, true
		}
		return nil, nil, false
	}
	return nil, nil, false
}

// listOfOne reports whether v is `(NAME X)` with exactly one argument, and
// returns X.
//
// One element exactly, because that is what makes the identity true: `(car
// (list a b))` is `a` but the `b` would have been evaluated and discarded, so
// accepting it would change the program's effects.
func listOfOne(v Value, name string) (Value, bool) {
	p, ok := v.(*Pair)
	if !ok || !isForm(p, name) {
		return nil, false
	}
	items, _ := ListToSlice(p.Cdr)
	if len(items) != 1 {
		return nil, false
	}
	return items[0], true
}

// consParts reports whether v is `(cons A B)` and returns both parts.
func consParts(v Value) (Value, Value, bool) {
	p, ok := v.(*Pair)
	if !ok || !isForm(p, "cons") {
		return nil, nil, false
	}
	items, _ := ListToSlice(p.Cdr)
	if len(items) != 2 {
		return nil, nil, false
	}
	return items[0], items[1], true
}

// isOneLiteral reports whether a value is the literal 1, read as an expression.
func isOneLiteral(v Value) bool {
	n, ok := v.(*Integer)
	return ok && isSmallEq(n, 1)
}

// exactIntegerResult lists the procedures whose result is *always* an exact
// integer, whatever they are given.
//
// This is not a guess about the implementation; it is a property of the
// procedure, and each entry is checkable against R7RS.  `string-length` returns
// an exact non-negative integer and no conforming implementation can return
// anything else, so a compiled body may rely on it the way it relies on
// `(+ 1 1)` being 2.
//
// It is worth writing down because of what it saves.  A call's result is a
// *handle* — the compiler cannot see inside it — and a handle operand sends
// every later arithmetic operation down the checked path:
//
//	(+ acc (string-length "hello"))
//
// emitted a tag test, a branch and a second crossing into the runtime for the
// addition, once per iteration.  Measured over a million iterations, that loop
// spent 873 nanoseconds a turn where a loop with no call spent 2.  With the
// result known to be an exact integer the addition is an overflow-checked add
// and no branch at all: the slow path could never be taken, so it is not
// emitted.
//
// **The list is short on purpose, and every absence is deliberate.**  `floor`,
// `round`, `truncate`, `abs`, `expt`, `sqrt`, `exact-integer-sqrt`, `gcd`, `lcm`
// and `string->number` all *can* return an exact integer and none of them always
// does — `(floor 1.5)` is inexact, `(expt 2 -1)` is a rational, `(sqrt -1)` is
// not a number at all.  Claiming a fixnum tag for one of those would be a wrong
// answer rather than a slower one, which is the trade this compiler never makes.
func exactIntegerResult(name string) bool {
	switch name {
	case "string-length",
		"vector-length",
		"bytevector-length":
		return true
	}
	return false
}
