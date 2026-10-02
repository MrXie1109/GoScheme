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
	// macros are the syntax bindings this scope introduces — a let-syntax, a
	// letrec-syntax, or a define-syntax in a body.  They live beside the
	// variables because that is how the interpreter's environments hold them:
	// one namespace, and the innermost binding of either kind wins.
	macros []*Symbol
	syn    []*Macro
	// senv is the shadow environment of this scope: a real Env with the same
	// names, kept only so that a macro defined in this scope has somewhere for
	// its marks to point.  A compiled body's variables are slots, so this is
	// how the compiler remembers where a template's identifiers came from.
	senv *Env
}

// pushScope starts a scope of its own: a block whose names are its own, and a
// shadow environment for the macro expander.
func (c *comp) pushScope(frame *cframe) (*cblock, *Env) {
	savedBlock, savedEnv := c.block, c.env
	c.block = &cblock{parent: savedBlock, frame: frame, senv: c.shadowEnv()}
	c.env = c.block.senv
	return savedBlock, savedEnv
}

// shadowEnv makes the shadow environment of a new scope.  Its parent is the
// global one when there is nothing else, because a template's identifiers that
// are not lexical have to resolve to the globals they name — the mark machinery
// follows the chain the macro was defined in.
func (c *comp) shadowEnv() *Env {
	if c.env == nil {
		return NewEnv(c.globals)
	}
	return NewEnv(c.env)
}

func (c *comp) popScope(block *cblock, env *Env) {
	c.block, c.env = block, env
}

// lookupMarked resolves an identifier the macro expander marked.  The mark
// names the environment the template was written in; when that scope is still
// an ancestor of the one being compiled — which it is whenever a macro is used
// inside the scope that defined it — the binding it stands for has a slot here,
// and hygiene comes out right without an environment at run time.
func (c *comp) lookupMarked(sym *Symbol) (*Macro, int, int, *cframe, bool) {
	def := markEnvOf(sym.Mark)
	if def == nil {
		return nil, 0, 0, nil, false
	}
	name := sym.orig
	if name == nil {
		name = sym.Base()
	}
	for b := c.block; b != nil; b = b.parent {
		if b.senv != def {
			continue
		}
		for i := len(b.names) - 1; i >= 0; i-- {
			if b.names[i] == name {
				return nil, c.frameDepth(b.frame), b.slots[i], b.frame, true
			}
		}
		for i := len(b.macros) - 1; i >= 0; i-- {
			if b.macros[i] == name {
				return b.syn[i], 0, 0, nil, true
			}
		}
		return nil, 0, 0, nil, false
	}
	return nil, 0, 0, nil, false
}

// lookupMacro finds a lexical syntax binding of sym.
func (c *comp) lookupMacro(sym *Symbol) (*Macro, bool) {
	for b := c.block; b != nil; b = b.parent {
		for i := len(b.macros) - 1; i >= 0; i-- {
			if b.macros[i] == sym {
				return b.syn[i], true
			}
		}
	}
	return nil, false
}

// lookupName finds the innermost binding of sym in the lexical scopes, and says
// which kind it is: a variable or a syntax binding.  They share one namespace,
// as they do in the interpreter's environments, so the innermost binding of
// either kind is the one that counts.
func (c *comp) lookupName(sym *Symbol) (depth, slot int, frame *cframe, mac *Macro, ok bool) {
	for b := c.block; b != nil; b = b.parent {
		for i := len(b.names) - 1; i >= 0; i-- {
			if b.names[i] == sym {
				return c.frameDepth(b.frame), b.slots[i], b.frame, nil, true
			}
		}
		for i := len(b.macros) - 1; i >= 0; i-- {
			if b.macros[i] == sym {
				return 0, 0, nil, b.syn[i], true
			}
		}
	}
	return 0, 0, nil, nil, false
}

// dropLocal removes a variable binding from the scope being compiled, for a
// name that a definition in the same scope turns into something else: the
// interpreter's Define replaces the binding it finds, and a define-syntax of a
// name the body had pre-bound as a variable is the case that matters.
func (c *comp) dropLocal(sym *Symbol) {
	for i := len(c.block.names) - 1; i >= 0; i-- {
		if c.block.names[i] == sym {
			c.block.names = append(c.block.names[:i], c.block.names[i+1:]...)
			c.block.slots = append(c.block.slots[:i], c.block.slots[i+1:]...)
			return
		}
	}
}

// defineMacro records a syntax binding in the scope being compiled.  It is how
// a compiled body knows a macro the interpreter would have put in its
// environment; nothing is emitted, because every use of it is expanded here.
func (c *comp) defineMacro(name *Symbol, mac *Macro) {
	c.block.macros = append(c.block.macros, name)
	c.block.syn = append(c.block.syn, mac)
	if c.env != nil {
		c.env.Define(name, mac)
	}
}

// useEnv is the environment a macro is expanded in: the shadow one, so that a
// template's identifiers resolve the way they would in the interpreter.
func (c *comp) useEnv() *Env {
	if c.env != nil {
		return c.env
	}
	return c.globals
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
	// env is the shadow environment of the scope being compiled (see cblock).
	env *Env
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
	if mac, depth, slot, frame, ok := c.lookupMarked(sym); ok {
		if mac != nil {
			c.fail("a macro name is not an expression: %s", sym.Name)
			return
		}
		c.emitLocalRef(depth, slot, frame)
		return
	}
	if depth, slot, frame, ok := c.lookup(sym); ok {
		c.emitLocalRef(depth, slot, frame)
		return
	}
	c.emit(opGlobal, c.konst(sym), 0)
}

// emitLocalRef reads a local, with the check and the box a variable may need.
func (c *comp) emitLocalRef(depth, slot int, frame *cframe) {
	{
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
	}
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
	if sym.IsMarked() {
		if mac, _, _, _, ok := c.lookupMarked(sym); ok && mac != nil {
			expanded, err := mac.Expand(x, c.useEnv())
			if err != nil {
				c.fail("macro %s: %v", sym.Name, err)
				return
			}
			c.expr(expanded, tail)
			return
		}
	}
	if _, _, _, mac, local := c.lookupName(sym); local {
		if mac == nil {
			c.application(x, tail)
			return
		}
		expanded, err := mac.Expand(x, c.globals)
		if err != nil {
			c.fail("macro %s: %v", sym.Name, err)
			return
		}
		c.expr(expanded, tail)
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

	case "match":
		if len(args) < 2 {
			c.fail("match: expected an expression and at least one clause")
			return true
		}
		// The subject, then four values per clause: the pattern, the variables
		// its body takes, the guard (or #f) and the body.  The matching stays
		// in matchPattern; what compiles is the guard and the body, which is
		// where the work in a match is.
		type matchClause struct {
			pattern Value
			vars    []*Symbol
			guard   Value
			body    []Value
		}
		var parsed []matchClause
		for _, cl := range args[1:] {
			pattern, guard, body, err := parseMatchClause(cl)
			if err != nil {
				c.fail("%v", err)
				return true
			}
			parsed = append(parsed, matchClause{
				pattern: pattern, vars: matchPatternVars(pattern),
				guard: guard, body: body,
			})
		}
		// The operator goes on the stack first, then the subject, then the
		// clauses — the layout a call expects.
		c.emit(opConst, c.konst(matchHelper), 0)
		c.expr(args[0], false)
		argc := int32(1) // the subject; the helper is the operator
		for _, cl := range parsed {
			formals := make([]Value, len(cl.vars))
			for i, v := range cl.vars {
				formals[i] = v
			}
			formalList := listFromSlice(formals)
			c.emit(opConst, c.konst(cl.pattern), 0)
			c.emit(opConst, c.konst(formalList), 0)
			if cl.guard != nil {
				thunk := c.boundThunk(formalList, []Value{cl.guard}, "match guard")
				if thunk == nil {
					return true
				}
				c.emit(opClosure, c.konst(thunk), 0)
			} else {
				c.emit(opConst, c.konst(False), 0)
			}
			thunk := c.boundThunk(formalList, cl.body, "match")
			if thunk == nil {
				return true
			}
			c.emit(opClosure, c.konst(thunk), 0)
			argc += 4
		}
		if tail {
			c.emit(opTailCall, argc, 0)
		} else {
			c.emit(opCall, argc, 0)
		}
		return true

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
		specs, err := selectSpecs(args)
		if err != nil {
			c.fail("%v", err)
			return true
		}
		c.emit(opConst, c.konst(selectHelper), 0)
		argc := int32(0)
		for _, sp := range specs {
			c.emit(opConst, c.konst(Int(int64(sp.kind))), 0)
			argc++
			for _, e := range sp.exprs {
				thunk := c.bodyWithFormals(Empty{}, []Value{e}, "select", func(sub *comp) {
					sub.body([]Value{e}, true)
				})
				if thunk == nil {
					return true
				}
				c.emit(opClosure, c.konst(thunk), 0)
				argc++
			}
		}
		if tail {
			c.emit(opTailCall, argc, 0)
		} else {
			c.emit(opCall, argc, 0)
		}
		return true

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
		names, _, err := recordType(args)
		if err != nil {
			c.fail("%v", err)
			return true
		}
		// The helper builds the type and its procedures; the compiler knows
		// their names, so it can store each where a binding of that name
		// lives — a slot in this body, or a global at the top level.
		call := List(recordTypeHelper, List(Intern("quote"), listFromSlice(args)))
		producer := c.bodyWithFormals(Empty{}, []Value{call}, "define-record-type", func(sub *comp) {
			sub.body([]Value{call}, true)
		})
		if producer == nil {
			return true
		}
		c.bindValuesFrom(names, Empty{}, producer, false)
		return true

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
		if len(args) < 1 {
			c.fail("guard: missing clause list")
			return true
		}
		spec, ok := args[0].(*Pair)
		if !ok {
			c.fail("guard: malformed clause list")
			return true
		}
		varSym, ok := spec.Car.(*Symbol)
		if !ok {
			c.fail("guard: condition variable is not an identifier")
			return true
		}
		clauses := mustSlice(spec.Cdr)
		// (guard-helper <clauses> <body>): the clauses are a procedure of the
		// condition and the body a thunk, both compiled, and the helper
		// installs the handler around the call the same way the interpreter
		// installs it around its own evaluation.
		clauseCode := c.bodyWithFormals(List(varSym), clauses, "guard", func(sub *comp) {
			sub.guardClauses(varSym, clauses, true)
		})
		bodyCode := c.bodyWithFormals(Empty{}, args[1:], "guard body", func(sub *comp) {
			sub.body(args[1:], true)
		})
		if clauseCode == nil || bodyCode == nil {
			return true
		}
		c.emit(opConst, c.konst(guardHelper), 0)
		c.emit(opClosure, c.konst(clauseCode), 0)
		c.emit(opClosure, c.konst(bodyCode), 0)
		if tail {
			c.emit(opTailCall, 2, 0)
		} else {
			c.emit(opCall, 2, 0)
		}
		return true

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
	return c.bodyWithFormals(formals, body, name, func(sub *comp) {
		sub.body(body, true)
	})
}

// bodyWithFormals compiles a body of its own with the given formals: the
// parameters are bound, the internal definitions are reserved, and emit
// compiles the body itself.  A lambda's body is a sequence of expressions and
// a guard's clause handler is a list of clauses, so the two share all of this
// and differ only in what they emit.
func (c *comp) bodyWithFormals(formals Value, body []Value, name string, emit func(*comp)) *Code {
	sub := c.beginBody(formals, body, name)
	if sub == nil {
		return nil
	}
	emit(sub)
	return c.finishBody(sub)
}

// beginBody starts a body of its own, with its formals bound and its internal
// definitions reserved, and returns the sub-compiler that emits it.  It
// reports a malformed formals list itself and returns nil.
func (c *comp) beginBody(formals Value, body []Value, name string) *comp {
	saved := c.frame
	savedBlock := c.block
	frame := &cframe{parent: saved}
	senv := c.shadowEnv()
	sub := &comp{
		m:        c.m,
		globals:  c.globals,
		env:      senv,
		frame:    frame,
		block:    &cblock{parent: savedBlock, frame: frame, senv: senv},
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
		sub.env.Define(f, Unassigned)
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
			sub.env.Define(sym, Unassigned)
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
			sub.env.Define(sym, Unassigned)
		}
	default:
		c.fail("lambda: malformed formals")
		return nil
	}
	sub.reserveBodyNames(body, 0)
	return sub
}

// reserveBodyNames gives the names an internal definition introduces a slot in
// this frame before the body is compiled, which is what makes them visible to
// the whole body and to each other with letrec* semantics, and marks them
// checked so that reading one before its definition has run is an error.  The
// interpreter does the same in prepBody, including the "not if the frame
// already has it" rule: a parameter or a let binding of the same name is
// reused rather than shadowed.
func (c *comp) reserveBodyNames(body []Value, from int) {
	for _, s := range scanBodyNames(body) {
		if c.inScope(s, from) {
			continue
		}
		slot, boxed := c.reserve(s)
		c.frame.checked[slot] = true
		if boxed {
			c.emit(opConst, c.konst(Unassigned), 0)
			c.emit(opNewCell, 0, int32(slot))
		} else {
			c.emit(opConst, c.konst(Unassigned), 0)
			c.emit(opSetLocal, 0, int32(slot))
		}
		c.block.names = append(c.block.names, s)
		c.block.slots = append(c.block.slots, slot)
		if c.env != nil {
			c.env.Define(s, Unassigned)
		}
	}
}

// inScope reports whether the scope that starts at index from already binds
// sym.  It is deliberately not a lookup: a binding in an enclosing scope is a
// different variable, and an internal definition of the same name is a new one
// that shadows it — which is the question the interpreter's prepBody asks its
// own frame, and only its own frame.
//
// The index matters because a compiled body keeps several scopes' names in one
// block: a let's bindings are slots of the body's frame, so from is where that
// let's own bindings begin, not where the block does.
func (c *comp) inScope(sym *Symbol, from int) bool {
	for i := len(c.block.names) - 1; i >= from; i-- {
		if c.block.names[i] == sym {
			return true
		}
	}
	return false
}

// boundThunk compiles a body whose parameters are a pattern's variables.  Its
// parameters are checked, so a clause whose pattern bound none of them — the
// other branch of an `or` — reports reading one instead of handing back the
// unassigned marker.
func (c *comp) boundThunk(formals Value, body []Value, name string) *Code {
	sub := c.beginBody(formals, body, name)
	if sub == nil {
		return nil
	}
	for _, param := range sub.code.Params {
		if _, slot, _, ok := sub.lookup(param); ok {
			sub.frame.checked[slot] = true
		}
	}
	sub.body(body, true)
	return c.finishBody(sub)
}

// makeSyntaxRules builds the macro a define-syntax or let-syntax binding
// stands for, so that a compiled body can expand it where the interpreter
// would have put it in an environment.
func makeSyntaxRules(name string, transformer Value, env *Env) (*Macro, error) {
	tf, ok := transformer.(*Pair)
	if !ok {
		return nil, NewError("unsupported transformer", transformer)
	}
	kw, _ := tf.Car.(*Symbol)
	if kw == nil || kw.Name != "syntax-rules" {
		return nil, NewError("only syntax-rules transformers are supported", transformer)
	}
	return ParseSyntaxRules(name, tf, env)
}

// condExpandBody picks the clause of a cond-expand whose requirement holds, in
// the same order the interpreter tries them, and returns its forms.
func condExpandBody(m *Machine, args []Value) ([]Value, bool) {
	for _, cl := range args {
		p, ok := cl.(*Pair)
		if !ok {
			return nil, false
		}
		if s, ok := p.Car.(*Symbol); ok && s.Name == "else" {
			return mustSlice(p.Cdr), true
		}
		if FeatureMatch(m, p.Car) {
			return mustSlice(p.Cdr), true
		}
	}
	return nil, true
}

// flatFormals lists the variables of a formals list in order, whether it is a
// proper list, a dotted one or a single rest name.
func flatFormals(formals Value) ([]*Symbol, error) {
	var out []*Symbol
	for f := formals; ; {
		switch v := f.(type) {
		case *Symbol:
			return append(out, v), nil
		case Empty:
			return out, nil
		case *Pair:
			sym, ok := v.Car.(*Symbol)
			if !ok {
				return nil, NewError("binding name is not an identifier", v.Car)
			}
			out = append(out, sym)
			f = v.Cdr
		default:
			return nil, NewError("malformed formals", formals)
		}
	}
}

// bindValuesFrom compiles the shape define-values and define-record-type share:
// a producer of values, and a consumer that stores each value in the binding it
// belongs to.  The names are the compiler's, so a binding this body declared
// has a slot already and a top-level one becomes a global — which is what the
// interpreter does when it defines them in the environment it is evaluating in.
func (c *comp) bindValuesFrom(names []*Symbol, formals Value, producer *Code, tail bool) {
	if formals == nil || formals == Value(Empty{}) {
		// define-record-type has no formals of its own: the consumer takes one
		// parameter per name.
		parts := make([]Value, len(names))
		for i, n := range names {
			parts[i] = n
		}
		formals = listFromSlice(parts)
	}
	fresh, err := freshFormals(formals)
	if err != nil {
		c.fail("%v", err)
		return
	}
	temps, err := flatFormals(fresh)
	if err != nil {
		c.fail("%v", err)
		return
	}
	if len(temps) != len(names) {
		c.fail("the values and the names do not line up")
		return
	}
	sub := c.beginBody(fresh, nil, "define")
	if sub == nil {
		return
	}
	for i, name := range names {
		depth, slot, frame, local := c.lookup(name)
		sub.loadLocal(slotOf(sub, temps[i]), false, false)
		if local {
			if frame.boxed[slot] {
				sub.emit(opSetCell, int32(depth+1), int32(slot))
			} else {
				sub.emit(opSetLocal, int32(depth+1), int32(slot))
			}
		} else {
			sub.emit(opDefineGlobal, sub.konst(name), 0)
		}
	}
	// The form's own value is the unspecified one, like a define.
	sub.emit(opConst, sub.konst(UnspecifiedValue), 0)
	consumer := c.finishBody(sub)
	if consumer == nil {
		return
	}
	c.emit(opConst, c.konst(bindValues), 0)
	c.emit(opClosure, c.konst(producer), 0)
	c.emit(opClosure, c.konst(consumer), 0)
	if tail {
		c.emit(opTailCall, 2, 0)
	} else {
		c.emit(opCall, 2, 0)
	}
}

// slotOf is the slot a name has in the sub-compiler's own frame.
func slotOf(sub *comp, sym *Symbol) int {
	_, slot, _, ok := sub.lookup(sym)
	if !ok {
		sub.fail("internal: no slot for the bound name")
	}
	return slot
}

// bodyWithLocals compiles the body of a binding form, whose internal
// definitions are local to it rather than global.  from is the index in the
// block where this scope's own bindings start.
func (c *comp) bodyWithLocals(body []Value, tail bool, from int) {
	c.reserveBodyNames(body, from)
	c.body(body, tail)
}

// finishBody emits the return of a body and turns the sub-compiler's state
// into the Code the machine runs.
func (c *comp) finishBody(sub *comp) *Code {
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
	return code
}

// checkDuplicates reports a binding form that names the same variable twice,
// which R7RS makes an error rather than a silent last-one-wins.  The
// interpreter checks the same forms (let and letrec, not let*), with the same
// message, and a compiled body has to agree about what is an error.
func (c *comp) checkDuplicates(what string, syms []*Symbol) bool {
	seen := map[*Symbol]bool{}
	for _, s := range syms {
		if seen[s] {
			c.fail("%s: duplicate variable in the same binding form", what)
			return false
		}
		seen[s] = true
	}
	return true
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
