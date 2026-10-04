// SPDX-License-Identifier: MIT

package scheme

// The comparison instructions.
//
// A call to < costs a type assertion on the operator, an arity check and an
// indirect call through the primitive's function pointer — once per comparison,
// which in a loop is once per iteration.  The compiler emits an instruction
// instead wherever it can see that the operator is still the interpreter's own
// binding (see comp.arithmeticOp).  A program that does (define (< a b) ...)
// rebinds the name; the compiler notices that the binding is no longer the
// builtin and emits the general call, as before.
//
// Only the comparisons are instructions.  The arithmetic operations were tried
// as instructions too and measured *slower* — 22% on a loop of additions —
// because + - * are variadic primitives whose body is already a tight loop, so
// an instruction that tests two operands and then falls back to the same
// primitive costs more than the call it saves.  Comparisons are the other way
// around (9% faster): they are chained, so the call and the arity check are the
// expensive part, and the common case is two fixnums.
//
// Nothing here reimplements the numeric tower.  The fast path covers small
// exact integers with a result in the same range; everything else — flonums,
// bignums, rationals, complex, or a chain with a non-number in it — goes to the
// primitive itself, so a value behaves the same either way.

// smallIntKind says what the fast path produced.
type smallIntKind uint8

const (
	smallIntNo   smallIntKind = iota // not applicable: use the primitive
	smallIntBool                     // the result is a boolean
)

// smallIntOp answers a chained comparison over small exact integers.  It
// reports 1 or 0 and smallIntBool when every operand was a small integer, and
// smallIntNo otherwise — which is the caller's signal to fall back to the
// primitive, including when an operand is a flonum and the answer would be a
// different one for a mixed comparison.
//
// The two-operand case is written out in the instruction loop rather than
// called from here, because the loop is far too large for the compiler to
// inline into; this function is what handles the longer chains.
func smallIntOp(op opcode, vals []Value) (int64, smallIntKind) {
	if len(vals) == 0 {
		return 0, smallIntNo // (=) and (<) with no arguments: the primitive's
	}
	prev, ok := vals[0].(*Integer)
	if !ok || !prev.small {
		return 0, smallIntNo
	}
	for _, v := range vals[1:] {
		cur, ok := v.(*Integer)
		if !ok || !cur.small {
			return 0, smallIntNo
		}
		less, equal := prev.i < cur.i, prev.i == cur.i
		ok = true
		switch op {
		case opNumLt:
			ok = less
		case opNumLe:
			ok = less || equal
		case opNumGt:
			ok = !less && !equal
		case opNumGe:
			ok = !less
		case opNumEq:
			ok = equal
		}
		if !ok {
			return 0, smallIntBool
		}
		prev = cur
	}
	return 1, smallIntBool
}

// The comparison operators an instruction stands for, and the primitive each
// one is.  The compiler looks a name up and compares what it finds with the
// primitive registered here: a program that has rebound < has something else
// there, and gets the general call it asked for.  Names and primitives are
// collected in one place so that the two cannot drift apart.
var arithmeticOps = []struct {
	op   opcode
	name string
}{
	{opNumLt, "<"}, {opNumLe, "<="}, {opNumGt, ">"}, {opNumGe, ">="},
	// = is used for eqv? as well, but as an instruction it is the numeric one.
	{opNumEq, "="},
}

// arithmeticPrimitives maps each comparison instruction to the primitive that
// implements it in full, which is what the instruction calls when its operands
// are not all small integers.  It is filled once the builtins exist.
var arithmeticPrimitives = map[opcode]*Primitive{}

// arithmeticNames maps an operator name to the instruction that stands for it,
// for the compiler.  A name that is not here is not one of these instructions.
var arithmeticNames = map[string]opcode{}

// installArithmeticInstructions records the builtin each instruction stands for.
// It runs once the builtins are registered, so that the compiler can tell "this
// is still the interpreter's <" from "the program has rebound <".
func installArithmeticInstructions(m *Machine) {
	for _, a := range arithmeticOps {
		if v, ok := m.Builtin.Lookup(Intern(a.name)); ok {
			if p, ok := v.(*Primitive); ok && p.Sync {
				arithmeticPrimitives[a.op] = p
				arithmeticNames[a.name] = a.op
			}
		}
	}
}
