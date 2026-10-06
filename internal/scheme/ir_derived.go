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
		case "letrec", "letrec*":
			if rewritten, ok := expandLetrecToNamedLet(p); ok {
				return expandedChildren(rewritten)
			}
			// Not a loop, so it stays a binding form.  `letrec*` is the one the
			// emitter can do directly: its initialisers run in order and each
			// sees the ones before it, which is exactly `let*`.  The difference
			// is that a `letrec*` name is bound *before* its initialiser runs, so
			// a closure written in an initialiser may refer to a later name — and
			// a closure that refers to a *later* name cannot be a `let*`, because
			// there the name is not bound yet.
			//
			// So this is offered only when no initialiser refers forward, and
			// expandLetrecToLet checks that.  A plain `letrec` evaluates its
			// initialisers in an unspecified order and none of them may refer to
			// another's value at all, so the same reading is sound for it.
			if rewritten, ok := expandLetrecToLet(p); ok {
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

// expandLetrecToNamedLet rewrites a `letrec` that binds exactly one procedure
// into a named `let`, which is the same thing written the way the loop
// recognisers read it.
//
// R7RS defines a named `let` *as* a `letrec`:
//
//	(let loop ((i 0)) BODY)  =  ((letrec ((loop (lambda (i) BODY))) loop) 0)
//
// so going the other way is not a trick, it is reading the definition
// backwards.  It is worth doing because every loop recogniser in ir_loop*.go
// understands a named `let` and none of them understands a `letrec` — that is
// what makes the loop forms worth recognising at all, since a loop is what a
// named `let` usually is.
//
// The shape accepted is narrow on purpose, and each part of it is required:
//
//		(letrec ((NAME (lambda (FORMALS) BODY ...))) CALL ...)
//
//	  - exactly one binding, because a named let binds one name;
//	  - that binding is a `lambda`, because that is what the name is bound to in
//	    the definition above.  A `letrec` binding a number is a different form
//	    and is left alone;
//	  - the body is a call to NAME, which is what makes it a loop.  A `letrec`
//	    whose body calls something else is left alone as well — rewriting it
//	    would produce a named let with no recursive call, which is legal but is
//	    not what this is for, and leaving it costs nothing because the scan
//	    understands `letrec` bindings well enough to refuse it with a reason.
//
// Recursion is not required to be *direct* for the rewrite to be correct, only
// for it to help: a body that does not call NAME becomes a named let that the
// recognisers decline, and it compiles or not exactly as it would have.
func expandLetrecToNamedLet(form *Pair) (Value, bool) {
	items, _ := ListToSlice(form.Cdr)
	if len(items) < 2 {
		return nil, false
	}
	binds, ok := ListToSlice(items[0])
	if !ok || len(binds) != 1 {
		return nil, false
	}
	b, ok := binds[0].(*Pair)
	if !ok {
		return nil, false
	}
	parts, _ := ListToSlice(b)
	if len(parts) != 2 {
		return nil, false
	}
	name, ok := parts[0].(*Symbol)
	if !ok {
		return nil, false
	}
	lam, ok := parts[1].(*Pair)
	if !ok || !isForm(lam, "lambda") {
		return nil, false
	}
	lamParts, _ := ListToSlice(lam.Cdr)
	if len(lamParts) < 1 {
		return nil, false
	}
	// The body must call the name, or this is not a loop and the rewrite would
	// only rename a form the recognisers were going to decline anyway.
	if !callsName(items[1:], name.Name) {
		return nil, false
	}
	// (let NAME ((v init) ...) BODY ...) — the lambda's formals become the
	// named let's variables with no initial values, and the call the letrec's
	// body performs supplies them.  That is the whole of the difference: a
	// named let's initialisers are part of the source, and here they are the
	// arguments of the call that would have followed.
	//
	// A lambda with a rest parameter or with formals that are not all symbols
	// is left alone: the recognisers do not read those shapes either.
	formals, ok := ListToSlice(lamParts[0])
	if !ok {
		return nil, false
	}
	vars := make([]Value, 0, len(formals))
	for _, f := range formals {
		s, ok := f.(*Symbol)
		if !ok {
			return nil, false
		}
		vars = append(vars, List(s))
	}
	// The call that opened the loop: `(NAME ARG ...)`, whose arguments are the
	// initial values in the named let's spelling.
	callForm, ok := items[1].(*Pair)
	if !ok {
		return nil, false
	}
	callArgs, _ := ListToSlice(callForm.Cdr)
	if len(callArgs) != len(vars) {
		return nil, false
	}
	inits := make([]Value, len(vars))
	for i := range vars {
		v, ok := vars[i].(*Pair)
		if !ok {
			return nil, false
		}
		inits[i] = List(v.Car, callArgs[i])
	}
	namedLet := make([]Value, 0, len(items))
	namedLet = append(namedLet, name, listFromSlice(inits))
	namedLet = append(namedLet, lamParts[1:]...)
	return Cons(Intern("let"), listFromSlice(namedLet)), true
}

// callsName reports whether any of the forms calls a procedure by this name.
//
// It looks through the structure rather than at the head only, because the call
// that opens a loop is often inside an `if` — that is what a loop test is.
func callsName(forms []Value, name string) bool {
	for _, f := range forms {
		if callsNameIn(f, name) {
			return true
		}
	}
	return false
}

func callsNameIn(v Value, name string) bool {
	switch x := v.(type) {
	case *Pair:
		if s, ok := x.Car.(*Symbol); ok && s.Name == name {
			return true
		}
		if isForm(x, "quote") {
			return false
		}
		items, _ := ListToSlice(x)
		for _, a := range items {
			if callsNameIn(a, name) {
				return true
			}
		}
	case *Vector:
		for _, a := range x.Items {
			if callsNameIn(a, name) {
				return true
			}
		}
	}
	return false
}

// expandLetrecToLet rewrites a `letrec` or `letrec*` whose initialisers never
// refer forward into the `let*` it is equivalent to.
//
//	(letrec* ((a 1) (b (+ a n))) BODY ...)  =>  (let* ((a 1) (b (+ a n))) BODY ...)
//
// The check is the whole point: an initialiser that mentions a name bound later
// is the one case where the two differ, because `let*` would not have bound it
// yet.  Refusing those is not a gap — a `letrec` that depends on its own
// unspecified order is a program whose meaning the report declines to define, and
// leaving it to the interpreter is the honest answer.
//
// Forward reference is checked textually over the whole form, which is
// conservative in the right direction: a name that *is* bound later but appears
// only inside a nested lambda's body would in fact be fine, and this declines it
// anyway.  Declining costs a slower correct answer; accepting wrongly would cost
// a wrong one.
func expandLetrecToLet(form *Pair) (Value, bool) {
	items, _ := ListToSlice(form.Cdr)
	if len(items) < 1 {
		return nil, false
	}
	binds, ok := ListToSlice(items[0])
	if !ok {
		return nil, false
	}
	// Names in order, so that "later" is well defined.
	names := make([]string, 0, len(binds))
	initExprs := make([]Value, 0, len(binds))
	for _, b := range binds {
		bp, ok := b.(*Pair)
		if !ok {
			return nil, false
		}
		parts, _ := ListToSlice(bp)
		if len(parts) != 2 {
			return nil, false
		}
		s, ok := parts[0].(*Symbol)
		if !ok {
			return nil, false
		}
		names = append(names, s.Name)
		initExprs = append(initExprs, parts[1])
	}
	for i, init := range initExprs {
		later := map[string]bool{}
		for _, n := range names[i+1:] {
			later[n] = true
		}
		if len(later) == 0 {
			continue
		}
		if mentionsAny(init, later) {
			return nil, false
		}
	}
	return Cons(Intern("let*"), listFromSlice(items)), true
}

// mentionsAny reports whether an expression names any of these identifiers.
//
// A quoted datum is data and is skipped, and so is a nested `lambda`'s parameter
// list — a binding of the same name shadows it, so the occurrence is not a
// reference to the outer one.
func mentionsAny(v Value, names map[string]bool) bool {
	switch x := v.(type) {
	case *Symbol:
		return names[x.Name]
	case *Pair:
		if isForm(x, "quote") {
			return false
		}
		args, _ := ListToSlice(x.Cdr)
		if s, ok := x.Car.(*Symbol); ok && s.Name == "lambda" && len(args) >= 1 {
			formals, _ := ListToSlice(args[0])
			shadowed := map[string]bool{}
			for _, f := range formals {
				if fs, ok := f.(*Symbol); ok {
					shadowed[fs.Name] = true
				}
			}
			rest := map[string]bool{}
			for n := range names {
				if !shadowed[n] {
					rest[n] = true
				}
			}
			for _, b := range args[1:] {
				if mentionsAny(b, rest) {
					return true
				}
			}
			return false
		}
		items, _ := ListToSlice(x)
		for _, a := range items {
			if mentionsAny(a, names) {
				return true
			}
		}
	case *Vector:
		for _, a := range x.Items {
			if mentionsAny(a, names) {
				return true
			}
		}
	}
	return false
}

// A forward reference **anywhere** in an initialiser stops the rewrite, including
// one inside a lambda — and that is not over-caution, it is a bug that was
// written and caught here.
//
// The tempting argument is that a reference inside a lambda body is deferred: the
// body runs after every initialiser has finished, so `(letrec* ((a (lambda () (b)))
// (b ...)) ...)` looks like a `let*`.  It is not, and the compiled program said
// so: the lambda *captures its environment when it is created*, which is before
// `b` is bound, so calling it found `b` undefined where the interpreter found it
// defined.  Deferring the read does not defer the capture.
//
// The interpreter can do this because it shares one environment frame that the
// later binding is added to.  `let*` gives each binding its own frame, which is
// what makes it fast and what makes the rewrite wrong for this shape.
//
// So the check is textual over the whole initialiser and pointedly includes
// lambda bodies.  It declines some programs that would have worked; declining
// costs a slower correct answer, and accepting wrongly cost a crash.
// mentionsAnyNow reports whether an expression names any of these identifiers
// *before* the binding form has finished.
//
// A reference inside a `lambda` body is deferred: the body runs when the closure
// is called, which is after every initialiser has run, so a `letrec*` that
// mentions a later name only from inside a lambda is still a `let*`.  That is not
// a technicality — it is the common case, and the R7RS test suite's `means` is
// exactly it:
//
//	(letrec* ((mean (lambda (f g) (f (/ (sum g ton) n))))   ; sum and n come later
//	          (sum  (lambda (g ton) ...))
//	          (n    (sum (lambda (x) 1) ton)))
//	  ...)
//
// `mean` names `sum` and `n`, both bound after it, and the program is correct
// because neither name is *read* until `mean` is called.  A reference that is
// evaluated as the initialiser runs — `(n (sum ...))` above, which calls `sum` —
// is not deferred and is why `n` must come last.
//
// So a lambda is looked through rather than into: its parameters shadow, and its
// body does not count as a use here.  The nested lambda is still rewritten by
// expandDerived, so nothing inside it escapes the traversal.
func mentionsAnyNow(v Value, names map[string]bool) bool {
	switch x := v.(type) {
	case *Symbol:
		return names[x.Name]
	case *Pair:
		if isForm(x, "quote") {
			return false
		}
		if s, ok := x.Car.(*Symbol); ok && s.Name == "lambda" {
			// Deferred: the body runs after the binding form has finished.
			return false
		}
		items, _ := ListToSlice(x)
		for _, a := range items {
			if mentionsAnyNow(a, names) {
				return true
			}
		}
	case *Vector:
		for _, a := range x.Items {
			if mentionsAnyNow(a, names) {
				return true
			}
		}
	}
	return false
}
