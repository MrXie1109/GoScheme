// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

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
// which R7RS makes an error rather than a silent last-one-wins.
//
// The rule itself lives in duplicateVar, which the interpreter asks as well:
// a compiled body and an interpreted one have to agree about what is an error,
// and the way to make them agree is for there to be one answer rather than two
// that have to be kept in step.
func (c *comp) checkDuplicates(what string, syms []*Symbol) bool {
	if duplicateVar(syms) != nil {
		c.fail("%s: duplicate variable in the same binding form", what)
		return false
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
					if t, ok := Car(Cdr(x)).(*Symbol); ok {
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
