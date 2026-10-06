// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// Rewriting the derived syntax the emitter does not have a rule for.
//
// R7RS divides its syntax in two.  A *core* form has a meaning of its own, and
// the compiler has to know what it is: `if`, `lambda`, `let` and `quote` are
// core, and `ir_emit.go` emits each one directly.  A *derived* form is defined
// in the report as an equivalent core form, and correctly written code cannot
// tell the difference.  `cond`, `case`, `when` and `unless` are derived:
//
//	(when TEST BODY ...)   =  (if TEST (begin BODY ...))
//	(cond (T E) ...)       =  (if T E (cond ...))
//
// The interpreter evaluates both kinds directly — evalCond walks the clauses on
// the machine — so nothing was expanding these, and the compiler met a `cond`
// with no rule for it and refused the whole body.  A procedure written with
// `cond` therefore did not compile *at all*, while the same procedure written
// with nested `if` compiled completely, which is a difference the language does
// not make and the report does not allow.
//
// So the compiler rewrites them here, before it looks at the body.  This is
// deliberately not a general macro expander: it handles the four derived forms
// whose definitions are given in R7RS §4.2.1 and §4.2.2 and leaves everything
// else alone, because a macro needs the environment to expand and this runs
// without one.
//
// The rewrite is a source-to-source transformation on the datum, so everything
// downstream — the pure-body scan, the loop recognisers, the emitter — sees
// only core forms and needs no knowledge of `cond` at all.  That is what makes
// it one place instead of four: the merge recogniser already carried a private
// `condToIf` for exactly this reason, and this replaces it.

// expandBody rewrites the derived syntax in every form of a procedure body.
//
// It is called once per procedure, before the walk recognisers, the scan and
// the emitter see the body, so that all three agree on what the body is.  Doing
// it in any one of them would mean the others saw a `cond` and had to know what
// it meant.
func expandBody(body []Value) []Value {
	out := make([]Value, len(body))
	for i, f := range body {
		out[i] = expandDerived(f)
	}
	return out
}

// expandDerived rewrites the derived syntax in a body, recursively, leaving
// anything it does not know unchanged.
//
// The traversal is the point: a `cond` nested inside a `let` inside a `lambda`
// has to be found, and a rewriter that only looked at the top of the body would
// fix the example in the tests and nothing real.
func expandDerived(form Value) Value {
	p, ok := form.(*Pair)
	if !ok {
		return form
	}
	if s, ok := p.Car.(*Symbol); ok {
		switch s.Name {
		case "quote":
			// A quoted datum is data, not code: rewriting inside it would turn
			// the list `(cond (a b))` into an `if` in a program that only
			// wanted to talk about one.
			return form
		case "cond":
			if rewritten, ok := expandCond(p); ok {
				return expandedChildren(rewritten)
			}
			return form
		case "case":
			if rewritten, ok := expandCase(p); ok {
				return expandedChildren(rewritten)
			}
			return form
		case "when":
			if rewritten, ok := expandWhenUnless(p, false); ok {
				return expandedChildren(rewritten)
			}
			return form
		case "unless":
			if rewritten, ok := expandWhenUnless(p, true); ok {
				return expandedChildren(rewritten)
			}
			return form
		}
	}
	return expandedChildren(form)
}

// expandedChildren rewrites every element of a form, keeping the head.
func expandedChildren(form Value) Value {
	p, ok := form.(*Pair)
	if !ok {
		return form
	}
	items, _ := ListToSlice(p.Cdr)
	if len(items) == 0 {
		return form
	}
	out := make([]Value, len(items))
	for i, it := range items {
		out[i] = expandDerived(it)
	}
	return Cons(p.Car, listFromSlice(out))
}

// expandWhenUnless rewrites `when` and `unless` into the `if` they mean.
//
//	(when TEST BODY ...)    =>  (if TEST (begin BODY ...))
//	(unless TEST BODY ...)  =>  (if TEST #f (begin BODY ...))
//
// The failing branch of `when` is unspecified rather than #f, and `if` with no
// alternative is the core form for that: an `if` with two arms whose else is
// unspecified would be the same value but a second thing to get right, and the
// emitter already implements the one-armed `if`.  `unless` needs a real else
// branch, and the report says its value is unspecified when the test holds,
// which is what leaving the arms swapped with #f produces.
func expandWhenUnless(form *Pair, negate bool) (Value, bool) {
	args, _ := ListToSlice(form.Cdr)
	if len(args) < 1 {
		return nil, false // malformed: let the interpreter report it
	}
	test, body := args[0], args[1:]
	if len(body) == 0 {
		// `(when t)` and `(unless t)` have an empty body and are legal; the
		// body of the begin is empty, which the emitter reads as unspecified.
		body = nil
	}
	begin := Cons(Intern("begin"), listFromSlice(body))
	if negate {
		return List(Intern("if"), test, Boolean(false), begin), true
	}
	return List(Intern("if"), test, begin), true
}

// expandCond rewrites a `cond` into the `if` chain it means, or reports that
// this clause list is one it does not handle.
//
// The shapes are the ones R7RS §4.2.1 defines:
//
//	(cond (TEST EXPR ...) ...)      the first TEST that holds runs its EXPRs
//	(cond (TEST => PROC) ...)       and the value of TEST is passed to PROC
//	(cond (TEST) ...)               the value of TEST is the result
//	(cond (else EXPR ...))          the alternative, run when nothing held
//
// An empty clause list is unspecified, and a `cond` with no clause that can
// hold is unspecified too, which is the same thing.
//
// `=>` needs care rather than a simple rewrite: it evaluates TEST once and
// hands that value to PROC, so the expansion has to bind it.  Writing
// `(if TEST (PROC TEST) ...)` would evaluate TEST twice, which is wrong when it
// has an effect, and this file has no way to tell.  A `let` does the binding in
// core syntax the emitter already understands.
func expandCond(form *Pair) (Value, bool) {
	clauses, _ := ListToSlice(form.Cdr)
	return expandCondClauses(clauses, 0)
}

// condGenLimit bounds how many gensyms one `cond` may need, so that a malformed
// program cannot make expansion allocate without end.
const condGenLimit = 1 << 12

var condGenCounter int

// freshCondVar returns a name that no source program wrote, for binding the
// value a `=>` clause tests.
//
// A `gensym`-style name is needed rather than a fixed one because `cond` can
// nest: a fixed name would be captured by the enclosing clause and the outer
// test's value would be handed to the inner receiver.
func freshCondVar() *Symbol {
	condGenCounter++
	return Intern("%cond" + itoa(condGenCounter))
}

// itoa is a small integer formatter, kept local so that this file needs no
// formatting package for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func expandCondClauses(clauses []Value, depth int) (Value, bool) {
	if depth > condGenLimit {
		return nil, false
	}
	if len(clauses) == 0 {
		// No clause held: the value is unspecified, and an `if` with no
		// alternative is the core form that produces one.  `(if #f #f)` would
		// also be an `if` with a then-arm that is never taken, but its *result*
		// is whatever the else arm yields, which is the unspecified value only
		// because the emitter produces one there — saying `(if #f #f)` would
		// make this file depend on that in a way a reader could not see.
		return List(Intern("if"), Boolean(false)), true
	}
	cl, ok := clauses[0].(*Pair)
	if !ok {
		return nil, false
	}
	items, _ := ListToSlice(cl)
	if len(items) == 0 {
		return nil, false
	}
	rest, ok := expandCondClauses(clauses[1:], depth+1)
	if !ok {
		return nil, false
	}

	// (else EXPR ...) ends the chain.  A bare `else` with no body is malformed
	// and is left to the interpreter to report.
	if isElse(items[0]) {
		if len(items) < 2 {
			return nil, false
		}
		return seqAsValue(items[1:]), true
	}

	test := items[0]
	body := items[1:]
	switch {
	case len(body) == 0:
		// (TEST): the test's own value is the result, and `(if T T REST)` would
		// evaluate it twice.
		v := freshCondVar()
		bind := List(Intern("let"), List(List(v, test)), List(Intern("if"), v, v, rest))
		return bind, true
	case isArrow(body[0]) && len(body) == 2:
		v := freshCondVar()
		recv := body[1]
		return List(Intern("let"), List(List(v, test)),
			List(Intern("if"), v, List(recv, v), rest)), true
	case isArrow(body[0]):
		return nil, false // malformed => clause
	default:
		return List(Intern("if"), test, seqAsValue(body), rest), true
	}
}

// seqAsValue turns a clause body into one expression: the expression itself
// when there is one, or a `begin` when there are several.
func seqAsValue(body []Value) Value {
	if len(body) == 1 {
		return body[0]
	}
	return Cons(Intern("begin"), listFromSlice(body))
}

// isArrow reports whether a value is the `=>` auxiliary keyword.
func isArrow(v Value) bool {
	s, ok := v.(*Symbol)
	return ok && s.Name == "=>"
}

// expandCase rewrites a `case` into a `cond` that tests membership with `memv`,
// which is the definition R7RS §4.2.1 gives:
//
//	(case KEY ((D ...) E ...) ... (else E ...))
//	  =>  (let ((k KEY)) (cond ((memv k '(D ...)) E ...) ... (else E ...)))
//
// The key is bound once, which the report requires and which matters as soon as
// the key expression has an effect.  The `cond` this produces is then expanded
// by the caller, so there is one implementation of `cond` rather than two.
func expandCase(form *Pair) (Value, bool) {
	items, _ := ListToSlice(form.Cdr)
	if len(items) < 1 {
		return nil, false
	}
	key, clauses := items[0], items[1:]
	keyVar := freshCondVar()
	condClauses := make([]Value, 0, len(clauses))
	for _, c := range clauses {
		cl, ok := c.(*Pair)
		if !ok {
			return nil, false
		}
		parts, _ := ListToSlice(cl)
		if len(parts) == 0 {
			return nil, false
		}
		if isElse(parts[0]) {
			if len(parts) < 2 {
				return nil, false
			}
			condClauses = append(condClauses, Cons(Intern("else"), listFromSlice(parts[1:])))
			continue
		}
		if len(parts) < 2 {
			return nil, false
		}
		// `(memv k '(D ...))` — the datum list is quoted because that is what a
		// case clause holds.
		memv := List(Intern("memv"), keyVar, List(Intern("quote"), parts[0]))
		condClauses = append(condClauses,
			Cons(memv, listFromSlice(parts[1:])))
	}
	inner := Cons(Intern("cond"), listFromSlice(condClauses))
	return List(Intern("let"), List(List(keyVar, key)), inner), true
}
