// SPDX-License-Identifier: MIT

package scheme

// The bytecode compiler.
//
// It compiles one body — a lambda body or a top-level form — into a Code for
// the VM in vm.go.  Anything it does not handle makes it give up on that
// *body*, and the body then runs in the tree-walker instead.  That is what
// keeps the two execution paths in agreement: the compiler never has to
// imitate the interpreter for a form, it declines it, and the interpreter runs
// the original source.
//
// Macros are expanded at compile time with the same expander the interpreter
// uses, so a macro use compiles to whatever it expands to.  A macro that
// expands to something uncompilable, or a form that is not in the table below,
// makes the enclosing body interpreted.

import "fmt"

// cframe is one activation frame: what the VM will allocate for it.
type cframe struct {
	parent  *cframe
	names   []*Symbol // per slot, for "used before initialization"
	boxed   []bool
	checked []bool
}

func (f *cframe) slot(sym *Symbol, boxed, checked bool) int {
	f.names = append(f.names, sym)
	f.boxed = append(f.boxed, boxed)
	f.checked = append(f.checked, checked)
	return len(f.names) - 1
}

func (f *cframe) grow(sym *Symbol, boxed, checked bool, slot int) {
	for len(f.names) <= slot {
		f.names = append(f.names, nil)
		f.boxed = append(f.boxed, false)
		f.checked = append(f.checked, false)
	}
	f.names[slot] = sym
	f.boxed[slot] = boxed
	f.checked[slot] = checked
}

func (f *cframe) code() *Code {
	return &Code{
		NSlots:  len(f.names),
		Boxed:   append([]bool(nil), f.boxed...),
		Checked: append([]bool(nil), f.checked...),
		Names:   append([]*Symbol(nil), f.names...),
	}
}

// cblock is one lexical block: the names it made visible and the frame slots
// they live in.  A let extends the block list without a new frame; a lambda
// starts a new frame.
type cblock struct {
	parent *cblock
	frame  *cframe
	names  []*Symbol
	slots  []int
}

// lookup finds sym and reports the lexical depth from the frame being
// compiled, which is what the VM's instructions take.
func (c *comp) lookup(sym *Symbol) (depth, slot int, frame *cframe, ok bool) {
	for b := c.block; b != nil; b = b.parent {
		for i := len(b.names) - 1; i >= 0; i-- {
			if b.names[i] == sym {
				return c.frameDepth(b.frame), b.slots[i], b.frame, true
			}
		}
	}
	return 0, 0, nil, false
}

// frameDepth counts the activations between the frame being compiled and f.
func (c *comp) frameDepth(f *cframe) int {
	n := 0
	for cur := c.frame; cur != nil && cur != f; cur = cur.parent {
		n++
	}
	return n
}

// comp is the state of one compilation.
type comp struct {
	m       *Machine
	globals *Env
	frame   *cframe
	block   *cblock
	code    *Code
	err     error
	// assigned is the set of variables the body being compiled assigns with
	// set!; exactly those bindings are boxed.
	assigned map[*Symbol]bool
	// caseKey is the slot holding the key of the case clause being compiled.
	caseKey int
}

func newComp(m *Machine, globals *Env) *comp {
	frame := &cframe{}
	return &comp{m: m, globals: globals, frame: frame, code: &Code{}}
}

func (c *comp) fail(format string, args ...interface{}) {
	if c.err == nil {
		c.err = fmt.Errorf(format, args...)
	}
}

func (c *comp) emit(op opcode, arg1, arg2 int32) int {
	at := len(c.code.Instrs)
	c.code.Instrs = append(c.code.Instrs, instr{op: op, arg1: arg1, arg2: arg2})
	return at
}

func (c *comp) here() int32 { return int32(len(c.code.Instrs)) }

func (c *comp) patch(at int, target int32) { c.code.Instrs[at].arg1 = target }

func (c *comp) konst(v Value) int32 {
	c.code.Consts = append(c.code.Consts, v)
	return int32(len(c.code.Consts) - 1)
}

// tempSlot allocates a slot that holds an intermediate value, such as the key
// of a case form.
func (c *comp) tempSlot() int {
	return c.frame.slot(nil, false, false)
}

// ---------------------------------------------------------------------------
// Compiling an expression
// ---------------------------------------------------------------------------

// expr compiles e.  tail says whether the value of e is returned directly by
// the enclosing activation, which is what makes a call a tail call.
func (c *comp) expr(e Value, tail bool) {
	if c.err != nil {
		return
	}
	switch x := e.(type) {
	case *Pair:
		c.combination(x, tail)
	case *Symbol:
		c.symbolRef(x)
	case Empty:
		c.fail("the empty list is not an expression")
	default:
		c.emit(opConst, c.konst(e), 0)
	}
}

func (c *comp) symbolRef(sym *Symbol) {
	if depth, slot, frame, ok := c.lookup(sym); ok {
		switch {
		case frame.checked[slot] && frame.boxed[slot]:
			c.emit(opLocalCellCheck, int32(depth), int32(slot))
		case frame.checked[slot]:
			c.emit(opLocalCheck, int32(depth), int32(slot))
		case frame.boxed[slot]:
			c.emit(opLocalCell, int32(depth), int32(slot))
		default:
			c.emit(opLocal, int32(depth), int32(slot))
		}
		return
	}
	c.emit(opGlobal, c.konst(sym), 0)
}

// combination compiles a pair: a special form, a macro use, or an application.
// The lookup order mirrors evalStep exactly: a lexical binding shadows syntax,
// a bound macro is expanded, a bound keyword is the special form it names, and
// an *unbound* name that happens to be a special form is still that form —
// which is how the embedded libraries can use define-syntax while the builtins
// are still being installed.
func (c *comp) combination(x *Pair, tail bool) {
	sym, isSym := x.Car.(*Symbol)
	if !isSym {
		c.application(x, tail)
		return
	}
	if _, _, _, local := c.lookup(sym); local {
		c.application(x, tail)
		return
	}
	v, bound := c.globals.Lookup(sym)
	if !bound {
		if _, isSpecial := specialForms[sym.Name]; isSpecial {
			if c.special(sym.Name, x, tail) {
				return
			}
			c.fail("%s is not compiled", sym.Name)
			return
		}
		c.application(x, tail)
		return
	}
	switch b := v.(type) {
	case *Macro:
		expanded, err := b.Expand(x, c.globals)
		if err != nil {
			c.fail("macro %s: %v", sym.Name, err)
			return
		}
		c.expr(expanded, tail)
	case *SyntaxKeyword:
		if c.special(b.Name, x, tail) {
			return
		}
		c.fail("%s is not compiled", b.Name)
	default:
		c.application(x, tail)
	}
}

// application compiles the general case: evaluate the operator and the
// operands, then call.
func (c *comp) application(x *Pair, tail bool) {
	c.expr(x.Car, false)
	n := int32(0)
	for rest := x.Cdr; ; {
		p, ok := rest.(*Pair)
		if !ok {
			if _, isNil := rest.(Empty); !isNil {
				c.fail("improper argument list")
			}
			break
		}
		c.expr(p.Car, false)
		n++
		rest = p.Cdr
	}
	if tail {
		c.emit(opTailCall, n, 0)
	} else {
		c.emit(opCall, n, 0)
	}
}

// body compiles a sequence of expressions, the last one in the position the
// caller asked for.
func (c *comp) body(forms []Value, tail bool) {
	if len(forms) == 0 {
		c.emit(opConst, c.konst(UnspecifiedValue), 0)
		return
	}
	for _, f := range forms[:len(forms)-1] {
		c.expr(f, false)
		c.emit(opPop, 0, 0)
	}
	c.expr(forms[len(forms)-1], tail)
}

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

	case "letrec", "letrec*":
		c.letrec(args, tail)
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
			params = append(params, items[0])
			if len(items) > 1 {
				inits = append(inits, items[1])
			} else {
				inits = append(inits, UnspecifiedValue)
			}
		}
		// The loop variable is a letrec binding: allocate it, make it visible
		// to the lambda (which recurses through it), then fill it in.
		saved := len(c.block.names)
		slot, boxed := c.declare(name, true)
		c.block.names = append(c.block.names, name)
		c.block.slots = append(c.block.slots, slot)
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
		c.block.names = c.block.names[:saved]
		c.block.slots = c.block.slots[:saved]
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		c.fail("let: malformed bindings")
		return
	}
	saved := len(c.block.names)
	var slots []int
	var boxed []bool
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
	}
	c.body(args[1:], tail)
	c.block.names = c.block.names[:saved]
	c.block.slots = c.block.slots[:saved]
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
	saved := len(c.block.names)
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
	}
	c.body(args[1:], tail)
	c.block.names = c.block.names[:saved]
	c.block.slots = c.block.slots[:saved]
}

// letrec compiles letrec and letrec*, which differ only in the order the
// initializers see each other; both allocate every variable first, and reading
// one before its initializer has run is an error, which the Checked flag
// makes the VM report.
func (c *comp) letrec(args []Value, tail bool) {
	if len(args) < 1 {
		c.fail("letrec: missing bindings")
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		c.fail("letrec: malformed bindings")
		return
	}
	saved := len(c.block.names)
	var slots []int
	var boxed []bool
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
		slots = append(slots, slot)
		boxed = append(boxed, bx)
	}
	for i, b := range bindings {
		items, _ := ListToSlice(b.(*Pair))
		if len(items) < 2 {
			c.emit(opConst, c.konst(UnspecifiedValue), 0)
		} else {
			c.expr(items[1], false)
		}
		c.storeLocal(slots[i], boxed[i])
	}
	c.body(args[1:], tail)
	c.block.names = c.block.names[:saved]
	c.block.slots = c.block.slots[:saved]
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

// ---------------------------------------------------------------------------
// Frames, blocks and lambdas
// ---------------------------------------------------------------------------

// declare reserves a slot for a fresh binding of sym, boxing it when the
// enclosing body assigns it.
func (c *comp) declare(sym *Symbol, checked bool) (int, bool) {
	boxed := c.assigned[sym]
	slot := c.frame.slot(sym, boxed, checked)
	return slot, boxed
}

// reserve allocates a slot without making it visible yet.
func (c *comp) reserve(sym *Symbol) (int, bool) {
	return c.declare(sym, false)
}

func (c *comp) storeLocal(slot int, boxed bool) {
	if boxed {
		c.emit(opNewCell, 0, int32(slot))
	} else {
		c.emit(opSetLocal, 0, int32(slot))
	}
}

func (c *comp) loadLocal(slot int, boxed, checked bool) {
	switch {
	case checked && boxed:
		c.emit(opLocalCellCheck, 0, int32(slot))
	case checked:
		c.emit(opLocalCheck, 0, int32(slot))
	case boxed:
		c.emit(opLocalCell, 0, int32(slot))
	default:
		c.emit(opLocal, 0, int32(slot))
	}
}

// lambda compiles a lambda body into a Code of its own.
func (c *comp) lambda(formals Value, body []Value, name string) *Code {
	saved := c.frame
	savedBlock := c.block
	savedAssigned := c.assigned
	frame := &cframe{parent: saved}
	sub := &comp{
		m:        c.m,
		globals:  c.globals,
		frame:    frame,
		block:    &cblock{parent: savedBlock, frame: frame},
		code:     &Code{Name: name},
		assigned: assignedNames(body),
	}
	// Parameters first, in order.
	switch f := formals.(type) {
	case *Symbol:
		sub.code.HasRest = true
		slot := sub.frame.slot(f, sub.assigned[f], false)
		sub.code.RestSlot = slot
		// The name has to be visible to the body as a local.  Without this
		// the body's reference to it is compiled as a global one and the
		// procedure fails with "unbound variable" the first time it is
		// called — (lambda args args) is the whole of the bug's surface.
		sub.block.names = append(sub.block.names, f)
		sub.block.slots = append(sub.block.slots, slot)
	case Empty:
		// no parameters
	case *Pair:
		cur := Value(f)
		for {
			p, ok := cur.(*Pair)
			if !ok {
				break
			}
			sym, ok := p.Car.(*Symbol)
			if !ok {
				c.fail("lambda: parameter is not an identifier")
				return nil
			}
			slot := sub.frame.slot(sym, sub.assigned[sym], false)
			sub.block.names = append(sub.block.names, sym)
			sub.block.slots = append(sub.block.slots, slot)
			sub.code.Params = append(sub.code.Params, sym)
			sub.code.NParams++
			cur = p.Cdr
		}
		if sym, ok := cur.(*Symbol); ok {
			sub.code.HasRest = true
			slot := sub.frame.slot(sym, sub.assigned[sym], false)
			sub.code.RestSlot = slot
			sub.block.names = append(sub.block.names, sym)
			sub.block.slots = append(sub.block.slots, slot)
		}
	default:
		c.fail("lambda: malformed formals")
		return nil
	}
	// Internal definitions are bound before the body runs, which is what gives
	// it letrec* semantics; the interpreter does the same with the same scan.
	for _, s := range scanBodyNames(body) {
		slot, boxed := sub.reserve(s)
		sub.frame.checked[slot] = true
		if boxed {
			sub.emit(opConst, sub.konst(Unassigned), 0)
			sub.emit(opNewCell, 0, int32(slot))
		} else {
			sub.emit(opConst, sub.konst(Unassigned), 0)
			sub.emit(opSetLocal, 0, int32(slot))
		}
		sub.block.names = append(sub.block.names, s)
		sub.block.slots = append(sub.block.slots, slot)
	}
	sub.body(body, true)
	sub.emit(opReturn, 0, 0)
	if sub.err != nil {
		c.fail("lambda: %v", sub.err)
		return nil
	}
	code := sub.frame.code()
	code.Instrs = sub.code.Instrs
	code.Consts = sub.code.Consts
	code.Name = sub.code.Name
	code.NParams = sub.code.NParams
	code.Params = sub.code.Params
	code.HasRest = sub.code.HasRest
	code.RestSlot = sub.code.RestSlot
	code.Checked = append([]bool(nil), sub.frame.checked...)
	c.frame = saved
	c.block = savedBlock
	c.assigned = savedAssigned
	return code
}

// assignedNames collects the identifiers a body assigns with set!, descending
// into nested lambdas because they share the binding.
func assignedNames(body []Value) map[*Symbol]bool {
	out := map[*Symbol]bool{}
	var walk func(v Value, quoted bool)
	walk = func(v Value, quoted bool) {
		switch x := v.(type) {
		case *Pair:
			if s, ok := x.Car.(*Symbol); ok && !quoted {
				switch s.Name {
				case "quote", "quasiquote":
					return
				case "set!":
					if t, ok := car(cdr(x)).(*Symbol); ok {
						out[t] = true
					}
				}
			}
			walk(x.Car, quoted)
			walk(x.Cdr, quoted)
		case *Vector:
			for _, e := range x.Items {
				walk(e, quoted)
			}
		}
	}
	for _, f := range body {
		walk(f, false)
	}
	return out
}

// compileTop compiles one top-level form.  It returns an error when the form
// (or a body inside it) uses something the compiler does not handle, and the
// caller then evaluates the original form in the tree-walker.
func compileTop(m *Machine, form Value, env *Env) (*Code, error) {
	c := newComp(m, env)
	c.assigned = assignedNames([]Value{form})
	c.block = &cblock{frame: c.frame}
	c.expr(form, true)
	c.emit(opReturn, 0, 0)
	if c.err != nil {
		return nil, c.err
	}
	code := c.frame.code()
	code.Instrs = c.code.Instrs
	code.Consts = c.code.Consts
	code.Name = "<top level>"
	code.NParams = 0
	return code, nil
}
