// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

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

// application compiles a call.  When the operator is a name the interpreter
// itself binds to one of the arithmetic primitives — and the call has operands
// to fold — the instruction for that operation is emitted instead of the call,
// which is most of the inner loops of a numerical program.  Everything else
// takes the general path: evaluate the operator, evaluate the operands, call.
func (c *comp) application(x *Pair, tail bool) {
	if op, args, ok := c.arithmeticOp(x); ok {
		// The operands, and nothing else: an arithmetic instruction has no
		// operator on the stack to call, which is the point of it.  They are
		// compiled here rather than by the general path below because the order
		// on the stack is what the instruction reads.
		for _, a := range args {
			c.expr(a, false)
		}
		count := int32(len(args))
		if tail {
			// The instruction leaves its result on the stack, so a call in
			// tail position still returns it.
			c.emit(op, count, 0)
			c.emit(opReturn, 0, 0)
			return
		}
		c.emit(op, count, 0)
		return
	}
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

// arithmeticOp recognises a call to a name that is still bound to the
// interpreter's arithmetic primitive, and returns the instruction to emit and
// the operand count.
//
// It is deliberately narrow.  The name must be unmarked and unshadowed, the
// binding must be the primitive itself — a program that has done
// (define (+ a b) ...) has put something else there, and gets the general call
// — and the arity must be one the primitive accepts, so that a wrong number of
// arguments is reported by the primitive and not by an instruction that
// assumed it was right.
func (c *comp) arithmeticOp(x *Pair) (opcode, []Value, bool) {
	sym, ok := x.Car.(*Symbol)
	if !ok || sym.IsMarked() {
		return 0, nil, false
	}
	if _, _, _, mac, local := c.lookupName(sym); local && mac == nil {
		return 0, nil, false // a local binding shadows the builtin
	}
	op, isArith := arithmeticNames[sym.Name]
	if !isArith {
		return 0, nil, false
	}
	// The binding in the global environment has to be the primitive this
	// instruction stands for.
	if v, bound := c.globals.Lookup(sym); !bound || v != Value(arithmeticPrimitives[op]) {
		return 0, nil, false
	}
	args := []Value{}
	for rest := x.Cdr; ; {
		p, ok := rest.(*Pair)
		if !ok {
			if _, isNil := rest.(Empty); !isNil {
				return 0, nil, false // improper: let the general path report it
			}
			break
		}
		args = append(args, p.Car)
		rest = p.Cdr
	}
	p := arithmeticPrimitives[op]
	if len(args) < p.MinArgs || (p.MaxArgs >= 0 && len(args) > p.MaxArgs) {
		return 0, nil, false // the primitive should report this, not us
	}
	return op, args, true
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
