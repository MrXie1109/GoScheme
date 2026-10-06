// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// ---------------------------------------------------------------------------
// Special forms
// ---------------------------------------------------------------------------

// special compiles one syntactic keyword.  It reports whether it handled it;
// when it returns false the enclosing body is interpreted instead.
func (c *comp) special(name string, x *Pair, tail bool) bool {
	args := mustSlice(x.Cdr)
	switch name {
	case "quote":
		if len(args) != 1 {
			c.fail("quote: expected one datum")
			return true
		}
		c.emit(opConst, c.konst(args[0]), 0)
		return true

	case "match":
		return c.matchForm(args, tail)

	case "define-syntax":
		if len(args) != 2 {
			c.fail("define-syntax: expected (define-syntax keyword transformer)")
			return true
		}
		name, ok := args[0].(*Symbol)
		if !ok {
			c.fail("define-syntax: keyword is not an identifier")
			return true
		}
		mac, err := makeSyntaxRules(name.Name, args[1], c.useEnv())
		if err != nil {
			c.fail("%v", err)
			return true
		}
		// A body pre-binds the names of its definitions, this one included; a
		// syntactic definition replaces that binding, as it does in an
		// environment.
		c.dropLocal(name)
		c.defineMacro(name, mac)
		// The interpreter also defines it in its environment, which a compiled
		// frame does not have; every use in this body was expanded here, so
		// the form is worth the unspecified value and nothing else.
		c.emit(opConst, c.konst(UnspecifiedValue), 0)
		return true

	case "let-syntax", "letrec-syntax":
		if len(args) < 1 {
			c.fail("%s: malformed", name)
			return true
		}
		bindings, ok := ListToSlice(args[0])
		if !ok {
			c.fail("%s: malformed bindings", name)
			return true
		}
		// A scope of syntax bindings, and the body compiled inside it.
		saved := c.block
		block := &cblock{parent: saved, frame: saved.frame}
		c.block = block
		for _, b := range bindings {
			p, ok := b.(*Pair)
			if !ok {
				c.fail("%s: malformed binding", name)
				return true
			}
			kw, ok := p.Car.(*Symbol)
			if !ok {
				c.fail("%s: keyword is not an identifier", name)
				return true
			}
			items, _ := ListToSlice(p.Cdr)
			if len(items) != 1 {
				c.fail("%s: malformed binding", name)
				return true
			}
			mac, err := makeSyntaxRules(kw.Name, items[0], c.useEnv())
			if err != nil {
				c.fail("%v", err)
				return true
			}
			c.defineMacro(kw, mac)
		}
		c.bodyWithLocals(args[1:], tail, 0)
		c.block = saved
		return true

	case "cond-expand":
		chosen, ok := condExpandBody(c.m, args)
		if !ok {
			c.fail("cond-expand: malformed clause")
			return true
		}
		c.body(chosen, tail)
		return true

	case "select":
		return c.selectForm(args, tail)

	case "define-values":
		if len(args) != 2 {
			c.fail("define-values: expected (define-values formals expr)")
			return true
		}
		names, err := flatFormals(args[0])
		if err != nil {
			c.fail("define-values: %v", err)
			return true
		}
		producer := c.bodyWithFormals(Empty{}, []Value{args[1]}, "define-values", func(sub *comp) {
			sub.body([]Value{args[1]}, true)
		})
		if producer == nil {
			return true
		}
		c.bindValuesFrom(names, args[0], producer, tail)
		return true

	case "define-record-type":
		return c.defineRecordTypeForm(args, tail)

	case "let-values", "let*-values":
		// Both are call-with-values built at compile time.  The expansion is
		// the interpreter's own (letValuesForm), so the two paths cannot
		// disagree about what a producer may see.
		expanded, err := letValuesForm(name, args)
		if err != nil {
			c.fail("%v", err)
			return true
		}
		c.expr(expanded, tail)
		return true

	case "go":
		if len(args) == 0 {
			c.fail("go: expected a body")
			return true
		}
		// The thread's body is a thunk, and it should be compiled: it is the
		// one piece of a concurrent program that runs on another machine.
		thunk := c.bodyWithFormals(Empty{}, args, "go", func(sub *comp) {
			sub.body(args, true)
		})
		if thunk == nil {
			return true
		}
		c.emit(opConst, c.konst(goHelper), 0)
		c.emit(opClosure, c.konst(thunk), 0)
		if tail {
			c.emit(opTailCall, 1, 0)
		} else {
			c.emit(opCall, 1, 0)
		}
		return true

	case "assert":
		if len(args) != 1 {
			c.fail("assert: expected one expression")
			return true
		}
		c.expr(args[0], false)
		ok := c.emit(opJumpTrueKeep, 0, 0)
		c.emit(opPop, 0, 0)
		c.emit(opConst, c.konst(assertFailed), 0)
		c.emit(opConst, c.konst(args[0]), 0)
		c.emit(opCall, 1, 0)
		// The raise never returns, but every path through the code needs one
		// value, and this is the one the false path would have left.
		c.emit(opConst, c.konst(UnspecifiedValue), 0)
		c.patch(ok, c.here())
		return true

	case "delay", "delay-force":
		if len(args) != 1 {
			c.fail("%s: expected one expression", name)
			return true
		}
		// The promise is a thunk, and the thunk should be compiled: leaving it
		// to the interpreter would make every force() of a compiled delay walk
		// the tree.
		thunk := c.bodyWithFormals(Empty{}, args, name, func(sub *comp) {
			sub.body(args, true)
		})
		if thunk == nil {
			return true
		}
		c.emit(opConst, c.konst(promiseHelper), 0)
		c.emit(opClosure, c.konst(thunk), 0)
		c.emit(opConst, c.konst(BooleanOf(name == "delay-force")), 0)
		if tail {
			c.emit(opTailCall, 2, 0)
		} else {
			c.emit(opCall, 2, 0)
		}
		return true

	case "case-lambda":
		// Every clause is a lambda of its own; the helper collects them into
		// one procedure, which is what the interpreter's evalCaseLambda builds.
		if len(args) == 0 {
			c.fail("case-lambda: no clauses")
			return true
		}
		var codes []*Code
		for _, cl := range args {
			p, ok := cl.(*Pair)
			if !ok {
				c.fail("case-lambda: bad clause")
				return true
			}
			body, _ := ListToSlice(p.Cdr)
			code := c.lambda(p.Car, body, "")
			if code == nil {
				return true
			}
			codes = append(codes, code)
		}
		c.emit(opConst, c.konst(caseLambdaHelper), 0)
		for _, code := range codes {
			c.emit(opClosure, c.konst(code), 0)
		}
		if tail {
			c.emit(opTailCall, int32(len(codes)), 0)
		} else {
			c.emit(opCall, int32(len(codes)), 0)
		}
		return true

	case "parameterize":
		expanded, err := parameterizeExpansion(args)
		if err != nil {
			c.fail("%v", err)
			return true
		}
		c.expr(expanded, tail)
		return true

	case "do":
		// A do loop is an expansion in the interpreter too, into a letrec
		// whose body is guarded — the guard is what gives (continue) its
		// meaning — and every form in that expansion compiles.  The only thing
		// that has to differ is how (continue) is recognised: the interpreter
		// has the token in hand, a compiled loop asks a helper that knows it.
		expanded, err := doExpansion(args, func(cond *Symbol) Value {
			return List(doContinue, cond)
		})
		if err != nil {
			c.fail("%v", err)
			return true
		}
		c.expr(expanded, tail)
		return true

	case "guard":
		return c.guardForm(args, tail)

	case "quasiquote":
		// `x is not a special form to compile: it is syntax sugar, and the
		// expander the interpreter uses turns it into cons/append/list->vector
		// calls that compile like anything else.  Doing it here rather than at
		// run time is the point of a compiler.
		if len(args) != 1 {
			c.fail("quasiquote: expected one template")
			return true
		}
		c.expr(EvalQuasiquote(args[0]), tail)
		return true

	case "if":
		if len(args) < 2 || len(args) > 3 {
			c.fail("if: expected two or three forms")
			return true
		}
		c.expr(args[0], false)
		branch := c.emit(opJumpFalse, 0, 0)
		c.expr(args[1], tail)
		if len(args) == 3 {
			skip := c.emit(opJump, 0, 0)
			c.patch(branch, c.here())
			c.expr(args[2], tail)
			c.patch(skip, c.here())
		} else {
			c.patch(branch, c.here())
			c.emit(opConst, c.konst(UnspecifiedValue), 0)
		}
		return true

	case "begin":
		c.body(args, tail)
		return true

	case "lambda":
		if len(args) < 1 {
			c.fail("lambda: missing formals")
			return true
		}
		code := c.lambda(args[0], args[1:], "")
		if c.err == nil {
			c.emit(opClosure, c.konst(code), 0)
		}
		return true

	case "set!":
		if len(args) != 2 {
			c.fail("set!: expected (set! variable expression)")
			return true
		}
		sym, ok := args[0].(*Symbol)
		if !ok {
			c.fail("set!: target is not an identifier")
			return true
		}
		c.expr(args[1], false)
		if depth, slot, frame, local := c.lookup(sym); local {
			if frame.boxed[slot] {
				c.emit(opSetCell, int32(depth), int32(slot))
			} else {
				// A variable that is assigned is boxed by the analysis in
				// lambda and in the body pre-scan, so this only happens for a
				// binding the analysis did not see.
				c.fail("set!: %s is not boxed", sym.Name)
			}
		} else {
			c.emit(opSetGlobal, c.konst(sym), 0)
			return true
		}
		// set! is worth an unspecified value, like the interpreter's.
		c.emit(opConst, c.konst(UnspecifiedValue), 0)
		return true

	case "define":
		c.define(args, tail)
		return true

	case "and":
		var skip []int
		for i, a := range args {
			if i == len(args)-1 {
				c.expr(a, tail)
			} else {
				c.expr(a, false)
				skip = append(skip, c.emit(opJumpFalseKeep, 0, 0))
				c.emit(opPop, 0, 0)
			}
		}
		if len(args) == 0 {
			c.emit(opConst, c.konst(True), 0)
		}
		for _, at := range skip {
			c.patch(at, c.here())
		}
		return true

	case "or":
		var done []int
		for i, a := range args {
			if i == len(args)-1 {
				c.expr(a, tail)
			} else {
				c.expr(a, false)
				skip := c.emit(opJumpTrueKeep, 0, 0)
				c.emit(opPop, 0, 0)
				done = append(done, skip)
			}
		}
		if len(args) == 0 {
			c.emit(opConst, c.konst(False), 0)
		}
		for _, at := range done {
			c.patch(at, c.here())
		}
		return true

	case "when":
		if len(args) < 1 {
			c.fail("when: missing test")
			return true
		}
		// Both paths are worth one value: the body's when the test holds, and
		// the unspecified value the interpreter returns when it does not.
		c.expr(args[0], false)
		skip := c.emit(opJumpFalse, 0, 0)
		c.body(args[1:], tail)
		done := c.emit(opJump, 0, 0)
		c.patch(skip, c.here())
		c.emit(opConst, c.konst(UnspecifiedValue), 0)
		c.patch(done, c.here())
		return true

	case "unless":
		if len(args) < 1 {
			c.fail("unless: missing test")
			return true
		}
		c.expr(args[0], false)
		skip := c.emit(opJumpTrue, 0, 0)
		c.body(args[1:], tail)
		done := c.emit(opJump, 0, 0)
		c.patch(skip, c.here())
		c.emit(opConst, c.konst(UnspecifiedValue), 0)
		c.patch(done, c.here())
		return true

	case "let":
		c.letForm(args, tail)
		return true

	case "let*":
		c.letStar(args, tail)
		return true

	case "letrec":
		c.letrec(args, tail, false)
		return true

	case "letrec*":
		c.letrec(args, tail, true)
		return true

	case "cond":
		c.condForm(args, tail)
		return true

	case "case":
		c.caseForm(args, tail)
		return true
	}
	return false
}

// define compiles a definition, at the top level or inside a body.
func (c *comp) define(args []Value, tail bool) {
	if len(args) < 1 {
		c.fail("define: missing name")
		return
	}
	target := args[0]
	var name *Symbol
	var rhs Value
	if sym, ok := target.(*Symbol); ok {
		if len(args) != 2 {
			c.fail("define: expected one expression")
			return
		}
		name = sym
		rhs = args[1]
	} else {
		// (define (name . formals) body ...), and nothing more exotic: a
		// curried definition is left to the interpreter.
		p, ok := target.(*Pair)
		if !ok {
			c.fail("define: bad procedure name")
			return
		}
		sym, ok := p.Car.(*Symbol)
		if !ok {
			c.fail("define: bad procedure name")
			return
		}
		name = sym
		lambda := Cons(Intern("lambda"), Cons(p.Cdr, listFromSlice(args[1:])))
		rhs = lambda
	}
	c.expr(rhs, false)
	if depth, slot, frame, local := c.lookup(name); local {
		if frame.boxed[slot] {
			c.emit(opSetCell, int32(depth), int32(slot))
		} else {
			c.emit(opSetLocal, int32(depth), int32(slot))
		}
		// Every form is worth one value, so that a body can pop the ones it
		// does not want.
		c.emit(opConst, c.konst(UnspecifiedValue), 0)
	} else {
		c.emit(opDefineGlobal, c.konst(name), 0)
	}
}

// letForm compiles let, including the named form.
func (c *comp) letForm(args []Value, tail bool) {
	if len(args) < 1 {
		c.fail("let: missing bindings")
		return
	}
	if name, ok := args[0].(*Symbol); ok {
		// (let loop ((v init) ...) body ...) is a letrec whose variable is a
		// lambda, applied to the initial values.
		if len(args) < 2 {
			c.fail("let: missing bindings")
			return
		}
		bindings, ok := ListToSlice(args[1])
		if !ok {
			c.fail("let: malformed bindings")
			return
		}
		var params, inits []Value
		var paramSyms []*Symbol
		for _, b := range bindings {
			p, ok := b.(*Pair)
			if !ok {
				c.fail("let: malformed binding")
				return
			}
			items, _ := ListToSlice(p)
			if len(items) < 1 {
				c.fail("let: malformed binding")
				return
			}
			if sym, ok := items[0].(*Symbol); ok {
				paramSyms = append(paramSyms, sym)
			}
			params = append(params, items[0])
			if len(items) > 1 {
				inits = append(inits, items[1])
			} else {
				inits = append(inits, UnspecifiedValue)
			}
		}
		if !c.checkDuplicates("let", paramSyms) {
			return
		}
		// The loop variable is a letrec binding: allocate it, make it visible
		// to the lambda (which recurses through it), then fill it in.
		savedBlock, savedEnv := c.pushScope(c.frame)
		slot, boxed := c.declare(name, true)
		c.block.names = append(c.block.names, name)
		c.block.slots = append(c.block.slots, slot)
		c.env.Define(name, Unassigned)
		code := c.lambda(listFromSlice(params), args[2:], name.Name)
		if c.err != nil {
			return
		}
		c.emit(opClosure, c.konst(code), 0)
		c.storeLocal(slot, boxed)
		// The procedure goes on the stack before its arguments, the way an
		// application is laid out.
		c.loadLocal(slot, boxed, true)
		for _, init := range inits {
			c.expr(init, false)
		}
		if tail {
			c.emit(opTailCall, int32(len(inits)), 0)
		} else {
			c.emit(opCall, int32(len(inits)), 0)
		}
		c.popScope(savedBlock, savedEnv)
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		c.fail("let: malformed bindings")
		return
	}
	// A scope of its own, though the variables are slots of this body's frame:
	// the block and the shadow environment are what make the scope a scope.
	savedBlock, savedEnv := c.pushScope(c.frame)
	var slots []int
	var boxed []bool
	var syms []*Symbol
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			c.fail("let: malformed binding")
			return
		}
		items, _ := ListToSlice(p)
		if len(items) < 1 {
			c.fail("let: malformed binding")
			return
		}
		sym, ok := items[0].(*Symbol)
		if !ok {
			c.fail("let: binding name is not an identifier")
			return
		}
		if !c.checkDuplicates("let", append(syms, sym)) {
			return
		}
		syms = append(syms, sym)
		init := Value(UnspecifiedValue)
		if len(items) > 1 {
			init = items[1]
		}
		c.expr(init, false)
		slot, bx := c.reserve(sym)
		c.storeLocal(slot, bx)
		slots = append(slots, slot)
		boxed = append(boxed, bx)
	}
	for i, b := range bindings {
		p, _ := b.(*Pair)
		items, _ := ListToSlice(p)
		sym := items[0].(*Symbol)
		c.block.names = append(c.block.names, sym)
		c.block.slots = append(c.block.slots, slots[i])
		c.frame.boxed[slots[i]] = boxed[i]
		c.env.Define(sym, Unassigned)
	}
	c.bodyWithLocals(args[1:], tail, 0)
	c.popScope(savedBlock, savedEnv)
}

// letStar compiles let*, whose bindings are visible to the ones after them.
func (c *comp) letStar(args []Value, tail bool) {
	if len(args) < 1 {
		c.fail("let*: missing bindings")
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		c.fail("let*: malformed bindings")
		return
	}
	savedBlock, savedEnv := c.pushScope(c.frame)
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			c.fail("let*: malformed binding")
			return
		}
		items, _ := ListToSlice(p)
		if len(items) < 1 {
			c.fail("let*: malformed binding")
			return
		}
		sym, ok := items[0].(*Symbol)
		if !ok {
			c.fail("let*: binding name is not an identifier")
			return
		}
		init := Value(UnspecifiedValue)
		if len(items) > 1 {
			init = items[1]
		}
		c.expr(init, false)
		slot, bx := c.reserve(sym)
		c.storeLocal(slot, bx)
		c.block.names = append(c.block.names, sym)
		c.block.slots = append(c.block.slots, slot)
		c.env.Define(sym, Unassigned)
	}
	c.bodyWithLocals(args[1:], tail, 0)
	c.popScope(savedBlock, savedEnv)
}

// letrec compiles letrec and letrec*, which differ in one thing: letrec
// evaluates *every* initializer before assigning any of them, so an
// initializer that reads a sibling sees it unassigned, while letrec* assigns
// each one as it goes, so the ones after it see it.  Both allocate every
// variable first, and reading one before its initializer has run is an error,
// which the Checked flag makes the VM report.
func (c *comp) letrec(args []Value, tail bool, sequential bool) {
	if len(args) < 1 {
		c.fail("letrec: missing bindings")
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		c.fail("letrec: malformed bindings")
		return
	}
	savedBlock, savedEnv := c.pushScope(c.frame)
	var slots []int
	var boxed []bool
	var syms []*Symbol
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			c.fail("letrec: malformed binding")
			return
		}
		items, _ := ListToSlice(p)
		if len(items) < 1 {
			c.fail("letrec: malformed binding")
			return
		}
		sym, ok := items[0].(*Symbol)
		if !ok {
			c.fail("letrec: binding name is not an identifier")
			return
		}
		if !c.checkDuplicates("letrec", append(syms, sym)) {
			return
		}
		syms = append(syms, sym)
		slot, bx := c.reserve(sym)
		c.frame.checked[slot] = true
		if bx {
			c.emit(opConst, c.konst(Unassigned), 0)
			c.emit(opNewCell, 0, int32(slot))
		} else {
			c.emit(opConst, c.konst(Unassigned), 0)
			c.emit(opSetLocal, 0, int32(slot))
		}
		c.block.names = append(c.block.names, sym)
		c.block.slots = append(c.block.slots, slot)
		c.env.Define(sym, Unassigned)
		slots = append(slots, slot)
		boxed = append(boxed, bx)
	}
	if sequential {
		for i, b := range bindings {
			items, _ := ListToSlice(b.(*Pair))
			if len(items) < 2 {
				c.emit(opConst, c.konst(UnspecifiedValue), 0)
			} else {
				c.expr(items[1], false)
			}
			c.storeLocal(slots[i], boxed[i])
		}
	} else {
		// Every initializer first, then the assignments: the values are on the
		// operand stack, one per variable, and are stored from the top down.
		// Nothing is assigned until all of them have been evaluated, so an
		// initializer that reads a sibling reads it unassigned.
		for _, b := range bindings {
			items, _ := ListToSlice(b.(*Pair))
			if len(items) < 2 {
				c.emit(opConst, c.konst(UnspecifiedValue), 0)
			} else {
				c.expr(items[1], false)
			}
		}
		for i := len(bindings) - 1; i >= 0; i-- {
			c.storeLocal(slots[i], boxed[i])
		}
	}
	c.bodyWithLocals(args[1:], tail, 0)
	c.popScope(savedBlock, savedEnv)
}

// condForm compiles cond, including the => clauses.  Every clause ends by
// jumping over the fallthrough value, so that the value of a matched clause and
// the unspecified value of no match are never both left behind.
func (c *comp) condForm(clauses []Value, tail bool) {
	var ends []int
	for i, cl := range clauses {
		p, ok := cl.(*Pair)
		if !ok {
			c.fail("cond: malformed clause")
			return
		}
		items, _ := ListToSlice(p)
		if len(items) == 0 {
			c.fail("cond: malformed clause")
			return
		}
		isElse := false
		if s, ok := items[0].(*Symbol); ok && s.Name == "else" && c.auxSyntax(s) {
			if i != len(clauses)-1 {
				c.fail("cond: else is not the last clause")
				return
			}
			isElse = true
		}
		next := -1
		if !isElse {
			// The test value is kept: a => clause needs it, a clause with a
			// body discards it explicitly, and a clause with only a test is
			// worth it.
			c.expr(items[0], false)
			next = c.emit(opJumpFalseKeep, 0, 0)
		}
		switch {
		case isElse:
			// No test was evaluated, so there is nothing to discard.
			c.body(items[1:], tail)
		case len(items) >= 2 && c.isArrow(items[1]):
			if len(items) != 3 {
				c.fail("cond: malformed => clause")
				return
			}
			slot := c.tempSlot()
			c.emit(opSetLocal, 0, int32(slot))
			c.expr(items[2], false)
			c.loadLocal(slot, false, false)
			if tail {
				c.emit(opTailCall, 1, 0)
			} else {
				c.emit(opCall, 1, 0)
			}
		case len(items) >= 2:
			c.emit(opPop, 0, 0)
			c.body(items[1:], tail)
		default:
			// A clause with only a test is worth its value.
		}
		ends = append(ends, c.emit(opJump, 0, 0))
		if next >= 0 {
			// The miss path still holds this clause's test value, which the
			// keeping jump left behind: it has to go before the next clause
			// runs, or the clauses pile up on the stack.
			c.patch(next, c.here())
			c.emit(opPop, 0, 0)
		}
	}
	// Nothing matched: the report leaves the value unspecified.
	c.emit(opConst, c.konst(UnspecifiedValue), 0)
	for _, at := range ends {
		c.patch(at, c.here())
	}
}

// auxSyntax reports whether the symbol is the auxiliary keyword it looks like.
// A lexical binding shadows it: (let ((=> #f)) (cond (#t => 'ok))) is a clause
// with two expressions, not an arrow clause, and the interpreter asks its
// environment the same question.  A compiled body keeps its lexicals in slots,
// so the block has to be asked as well as the globals.
func (c *comp) auxSyntax(s *Symbol) bool {
	if _, _, _, ok := c.lookup(s); ok {
		return false
	}
	return isAuxSyntax(s, c.globals)
}

// isArrow reports whether v is the auxiliary keyword => in scope.
func (c *comp) isArrow(v Value) bool {
	s, ok := v.(*Symbol)
	return ok && s.Name == "=>" && c.auxSyntax(s)
}

// guardClauses compiles the clauses of a guard.  They are cond clauses with one
// difference: when none of them matches, the condition is raised again.  The
// condition variable is the parameter of the body these are compiled in, and
// the re-raise goes to the guard's own primitive rather than to whatever
// `raise` is bound to, which a program is free to rebind — the interpreter
// makes the same choice.
func (c *comp) guardClauses(varSym *Symbol, clauses []Value, tail bool) {
	var ends []int
	for i, cl := range clauses {
		p, ok := cl.(*Pair)
		if !ok {
			c.fail("guard: malformed clause")
			return
		}
		items, _ := ListToSlice(p)
		if len(items) == 0 {
			c.fail("guard: malformed clause")
			return
		}
		isElse := false
		if s, ok := items[0].(*Symbol); ok && s.Name == "else" && c.auxSyntax(s) {
			if i != len(clauses)-1 {
				c.fail("guard: else is not the last clause")
				return
			}
			isElse = true
		}
		next := -1
		if !isElse {
			c.expr(items[0], false)
			next = c.emit(opJumpFalseKeep, 0, 0)
		}
		switch {
		case isElse:
			c.body(items[1:], tail)
		case len(items) >= 2 && c.isArrow(items[1]):
			if len(items) != 3 {
				c.fail("guard: malformed => clause")
				return
			}
			slot := c.tempSlot()
			c.emit(opSetLocal, 0, int32(slot))
			c.expr(items[2], false)
			c.loadLocal(slot, false, false)
			if tail {
				c.emit(opTailCall, 1, 0)
			} else {
				c.emit(opCall, 1, 0)
			}
		case len(items) >= 2:
			c.emit(opPop, 0, 0)
			c.body(items[1:], tail)
		default:
			// A clause with only a test is worth its value.
		}
		ends = append(ends, c.emit(opJump, 0, 0))
		if next >= 0 {
			c.patch(next, c.here())
			c.emit(opPop, 0, 0)
		}
	}
	// Nothing matched: raise the condition again.  This never returns, so the
	// clauses that did match jump straight past it.
	c.emit(opConst, c.konst(guardReRaise), 0)
	c.expr(varSym, false)
	if tail {
		c.emit(opTailCall, 1, 0)
	} else {
		c.emit(opCall, 1, 0)
	}
	c.emit(opConst, c.konst(UnspecifiedValue), 0)
	for _, at := range ends {
		c.patch(at, c.here())
	}
}

// caseForm compiles case, which compares the key with eqv?, as the
// interpreter does.  A clause matches when the key is eqv? to *any* of its
// datums, so the datum tests jump into the body on success and one jump at the
// end of the tests goes on to the next clause.
func (c *comp) caseForm(args []Value, tail bool) {
	if len(args) < 1 {
		c.fail("case: missing key")
		return
	}
	c.expr(args[0], false)
	key := c.tempSlot()
	c.caseKey = key
	c.emit(opSetLocal, 0, int32(key))
	var ends []int
	clauses := args[1:]
	for i, cl := range clauses {
		p, ok := cl.(*Pair)
		if !ok {
			c.fail("case: malformed clause")
			return
		}
		items, _ := ListToSlice(p)
		if len(items) == 0 {
			c.fail("case: malformed clause")
			return
		}
		last := i == len(clauses)-1
		isElse := false
		if s, ok := items[0].(*Symbol); ok && s.Name == "else" && c.auxSyntax(s) {
			if !last {
				c.fail("case: else is not the last clause")
				return
			}
			isElse = true
		}
		var matched []int
		miss := -1
		if !isElse {
			data, ok := ListToSlice(items[0])
			if !ok {
				c.fail("case: malformed datum list")
				return
			}
			for _, d := range data {
				c.loadLocal(key, false, false)
				c.emit(opConst, c.konst(d), 0)
				c.emit(opEqv, 0, 0)
				matched = append(matched, c.emit(opJumpTrue, 0, 0))
			}
			// Nothing matched: on to the next clause.
			miss = c.emit(opJump, 0, 0)
			for _, at := range matched {
				c.patch(at, c.here())
			}
		}
		c.clauseBody(items, tail, isElse)
		// Every clause leaves its value behind and then skips the fallthrough
		// value below, so that a matched clause and the no-match path each
		// leave exactly one.
		ends = append(ends, c.emit(opJump, 0, 0))
		if miss >= 0 {
			c.patch(miss, c.here())
		}
		_ = last
	}
	// No datum matched and there was no else: the report leaves the value
	// unspecified, and one value keeps the stack balanced.
	c.emit(opConst, c.konst(UnspecifiedValue), 0)
	for _, at := range ends {
		c.patch(at, c.here())
	}
}

// clauseBody compiles the body of a case clause, which is either a sequence of
// expressions or a => recipient.  An else clause's body starts at items[1] like
// any other, but it can never be a => recipient: the else is not a test.
func (c *comp) clauseBody(items []Value, tail bool, isElse bool) {
	if len(items) >= 2 {
		// (else => proc) is a case clause like any other: the interpreter's
		// evalCaseBody hands `=>` to the else clause too, and the R7RS test
		// suite uses it.  cond is the one that does not: its else clause is a
		// sequence of expressions.
		if s, ok := items[1].(*Symbol); ok && s.Name == "=>" && c.auxSyntax(s) {
			if len(items) != 3 {
				c.fail("case: malformed => clause")
				return
			}
			c.expr(items[2], false)
			key := c.caseKey
			c.loadLocal(key, false, false)
			if tail {
				c.emit(opTailCall, 1, 0)
			} else {
				c.emit(opCall, 1, 0)
			}
			return
		}
		c.body(items[1:], tail)
		return
	}
	c.emit(opConst, c.konst(UnspecifiedValue), 0)
}
