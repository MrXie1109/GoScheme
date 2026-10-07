// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"strconv"
)

// Arithmetic and comparison in machine code.
//
// Every operation here is *checked*: a Scheme exact integer is unbounded, so an
// operation on a machine word can overflow where Scheme would have promoted to a
// bignum.  The generated code tests for it and, when it happens, calls the
// runtime, which is the interpreter and knows about bignums.  That is what makes
// it safe to compile a procedure natively without proving anything about the
// size of its values.
//
// Two things decide whether the fast path is taken: the operand tags, when they
// are known at compile time (a literal, or a value a previous operation
// produced), and a runtime test otherwise.  The tags being known is the common
// case inside a loop, which is why so much of this works at compile time.

func (f *irFunc) truthOf(v irVal) (string, error) {
	// A literal: the answer is known here.
	if v.isConstFixnum() {
		if v.bits == "0" {
			return "false", nil
		}
		return "true", nil
	}
	// A fixnum whose value is not a literal: only the word needs testing, since
	// a fixnum is never #f.
	if v.tag == tagFixnum {
		out := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", out, v.bits)
		return out, nil
	}
	// A boolean is #f exactly when its word is zero, so the test is the word —
	// no call needed.  Without this, `(if (= i 0) ...)` asked the runtime whether
	// the *result of a comparison* was true, once per iteration, which is the
	// most common shape in any Scheme program.
	if v.tag == tagBoolean {
		out := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", out, v.bits)
		return out, nil
	}
	f.want("i64 @gs_truthy(" + gsVal + ")")
	isFixnum := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", isFixnum, v.tag, tagFixnum)
	askLabel := f.freshLabel("truth.ask")
	okLabel := f.freshLabel("truth.ok")
	doneLabel := f.freshLabel("truth.done")
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", isFixnum, okLabel, askLabel)

	f.block(okLabel)
	okFrom := f.currentBlock
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(askLabel)
	asked := f.reg()
	fmt.Fprintf(&f.body, "  %s = call i64 @gs_truthy(%s %s)\n", asked, gsVal, v.bits0(f))
	askedBool := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", askedBool, asked)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i1 [ true, %%%s ], [ %s, %%%s ]\n",
		out, okFrom, askedBool, askLabel)
	return out, nil
}

// bits0 rebuilds a whole gs_val from an irVal, which a runtime call that takes
// one needs.
func (v irVal) bits0(f *irFunc) string {
	first := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s undef, i64 %s, 0\n", first, gsVal, v.bits)
	second := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s %s, i64 %s, 1\n", second, gsVal, first, v.tag)
	return second
}

// numericTest asks a question whose answer a fixnum can give directly, and
// hands it to the runtime when the value is a handle.
//
// The two answers are computed in separate blocks and joined, rather than
// asking the runtime and ignoring the result — a handle and a fixnum are both
// reachable, and a program that only ever uses small numbers must not pay for
// the other case.
func (f *irFunc) numericTest(v irVal, pred, other string) (irVal, error) {
	if v.isConstFixnum() && other == "0" {
		// A literal against zero is decided here rather than at run time.  The
		// answer depends on the literal's *value*, not on the fact that it is
		// one, and reading only the tag is what made `(zero? 5)` true.
		zero, err := strconv.ParseInt(v.bits, 10, 64)
		if err != nil {
			return irVal{}, fmt.Errorf("ir: %s is not a machine integer", v.bits)
		}
		yes := map[string]bool{
			"eq":  zero == 0,
			"sgt": zero > 0,
			"slt": zero < 0,
		}[pred]
		return boolVal(yes), nil
	}
	fast := f.reg()
	switch pred {
	case "eq":
		fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", fast, v.bits, other)
	case "sgt":
		fmt.Fprintf(&f.body, "  %s = icmp sgt i64 %s, %s\n", fast, v.bits, other)
	case "slt":
		fmt.Fprintf(&f.body, "  %s = icmp slt i64 %s, %s\n", fast, v.bits, other)
	}
	isFixnum := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", isFixnum, v.tag, tagFixnum)
	slowLabel := f.freshLabel("numtest.slow")
	doneLabel := f.freshLabel("numtest.done")
	// The block the fast answer comes from, which the phi has to name: the
	// branch is about to leave it.
	fastFrom := f.currentBlock
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", isFixnum, doneLabel, slowLabel)

	f.block(slowLabel)
	// A handle: the runtime compares it.  `zero?` and friends are the same
	// question as a comparison against zero.
	var asked string
	switch pred {
	case "eq":
		f.want("i64 @gs_num_eq(" + gsVal + ", " + gsVal + ")")
		asked = f.callNumCompare("gs_num_eq", v, fixnumVal(0))
	case "sgt":
		// x > 0 is 0 < x, and only `<` is an entry point.
		f.want("i64 @gs_num_lt(" + gsVal + ", " + gsVal + ")")
		asked = f.callNumCompare("gs_num_lt", fixnumVal(0), v)
	case "slt":
		f.want("i64 @gs_num_lt(" + gsVal + ", " + gsVal + ")")
		asked = f.callNumCompare("gs_num_lt", v, fixnumVal(0))
	}
	askedBool := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", askedBool, asked)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i1 [ %s, %%%s ], [ %s, %%%s ]\n",
		out, fast, fastFrom, askedBool, slowLabel)
	return f.truthVal(out), nil
}

// callNumCompare calls one of the runtime's comparisons on two tagged values.
func (f *irFunc) callNumCompare(fn string, a, b irVal) string {
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call i64 @%s(%s %s, %s %s)\n",
		out, fn, gsVal, a.bits0(f), gsVal, b.bits0(f))
	return out
}

// compareTagged is a comparison of two tagged values, as 0 or 1.
func (f *irFunc) compareTagged(op string, a, b irVal) (string, error) {
	return f.emitCompare(op, a, b)
}

// boxedLiteral is the value of a constant that does not fit a machine word.
//
// The literal is written out as Scheme text, which the runtime reads back — a
// bignum or a string is not something the generated code should know how to
// construct, and handing over the text keeps the representation on one side of
// the boundary.
//
// **It is built once, in `main`, and read from a global here.** A literal is a
// constant: the same `"hello"` is the same value everywhere in a program, and
// every later reference can load it rather than build it.  Building it at the
// point of use meant a loop crossed into the runtime, re-read the source text,
// parsed it and allocated an object on *every iteration*:
//
//	(define (loop i acc)
//	  (if (= i 0) acc (loop (- i 1) (+ acc (string-length "hello")))))
//
// boxed `"hello"` a million times to compute a number that never changed, and
// that was one of the four crossings the loop made per iteration.
//
// The load is of a `%gs.val` global, so it is two words read from static
// storage — no call, no boundary, and nothing for the collector to move, since
// what it holds is a handle rather than a pointer.
func (f *irFunc) boxedLiteral(text string) (irVal, error) {
	for _, e := range f.mod.boxed {
		if e.text == text {
			return f.loadGlobalVal(e.global), nil
		}
	}
	// First use: name a global for it, and let main fill it in.
	g := fmt.Sprintf("@.boxed%d", len(f.mod.boxed))
	f.mod.boxed = append(f.mod.boxed, boxedLiteralEntry{text: text, global: g})
	return f.loadGlobalVal(g), nil
}

// loadGlobalVal reads a tagged value from a global.
func (f *irFunc) loadGlobalVal(name string) irVal {
	bits := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* getelementptr (%s, %s* %s, i64 0, i32 0)\n",
		bits, gsVal, gsVal, name)
	tag := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* getelementptr (%s, %s* %s, i64 0, i32 1)\n",
		tag, gsVal, gsVal, name)
	return irVal{bits: bits, tag: tag}
}

// known reports whether a name is one the generated code can call directly.
func (f *irFunc) known(name string) bool {
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

// zext widens an i1 to the i64 a Scheme value is.
func (f *irFunc) zext(cmp string) string {
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = zext i1 %s to i64\n", out, cmp)
	return out
}

// emitCompare emits an ordering comparison as 0 or 1.
//
// Two fixnums are compared directly, which is the whole fast path.  Anything
// else goes to the runtime, because a handle's number may be larger than a
// machine word and the comparison has to be the exact one — and because the
// operands may not even be integers.
//
// `>` and `>=` are not runtime entry points: they are `<` and `<=` with the
// operands swapped, which is why the swap is written out here rather than left
// to each caller.
func (f *irFunc) emitCompare(op string, a, b irVal) (string, error) {
	pred := map[string]string{
		"=": "eq", "<": "slt", ">": "sgt", "<=": "sle", ">=": "sge",
	}[op]
	if pred == "" {
		return "", fmt.Errorf("ir: %s is not a comparison", op)
	}
	// Both operands are known to be machine words, so the machine comparison is
	// the whole answer and there is nothing to branch for.
	//
	// This matters more than it looks.  A tag that is a literal zero is the
	// common case — every value a compiled body computes locally is one — and
	// emitting the runtime path anyway put a call to gs_num_eq and a call to
	// gs_truthy inside the loop of `(define (loop i acc) (if (= i 0) ...))`.
	// The optimizer could not remove them, because the tag arrives through a phi
	// and it cannot prove which branch reaches the test.
	if a.tag == tagFixnum && b.tag == tagFixnum {
		fast := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp %s i64 %s, %s\n", fast, pred, a.bits, b.bits)
		return f.zext(fast), nil
	}
	// One operand is a literal fixnum and the other may be a handle.
	//
	// A handle is never equal to a fixnum: the runtime hands back a handle only
	// for a value that does not fit a machine word, so a value that *would*
	// compare equal to a small constant is always a fixnum.  For a comparison
	// whose answer is therefore decided by the tag — `=` against a constant —
	// the machine comparison of a handle would be meaningless and the answer is
	// simply false.
	//
	// This is the shape of every loop test in Scheme, `(= i 0)` above all, and
	// it is the difference between a loop that calls the runtime to ask and one
	// that does not.
	if op == "=" {
		// Whichever side is the literal, the other may be a handle.
		lit, other := irVal{}, irVal{}
		switch {
		case b.isConstFixnum() && a.tag != tagFixnum:
			lit, other = b, a
		case a.isConstFixnum() && b.tag != tagFixnum:
			lit, other = a, b
		}
		if lit.bits != "" {
			fast := f.reg()
			fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", fast, other.bits, lit.bits)
			isFixnum := f.reg()
			fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", isFixnum, other.tag, tagFixnum)
			out := f.reg()
			fmt.Fprintf(&f.body, "  %s = and i1 %s, %s\n", out, fast, isFixnum)
			return f.zext(out), nil
		}
	}
	fast := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp %s i64 %s, %s\n", fast, pred, a.bits, b.bits)

	// The fast answer only stands when both operands are machine words.
	bothFixnum := f.reg()
	tags := f.reg()
	fmt.Fprintf(&f.body, "  %s = or i64 %s, %s\n", tags, a.tag, b.tag)
	fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", bothFixnum, tags, tagFixnum)

	slowLabel := f.freshLabel("cmp.slow")
	doneLabel := f.freshLabel("cmp.done")
	// The block the fast answer comes from, which the phi has to name: the
	// branch is about to leave it.
	fastFrom := f.currentBlock
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", bothFixnum, doneLabel, slowLabel)

	f.block(slowLabel)
	f.want("i64 @gs_num_eq(" + gsVal + ", " + gsVal + ")")
	f.want("i64 @gs_num_lt(" + gsVal + ", " + gsVal + ")")
	f.want("i64 @gs_num_le(" + gsVal + ", " + gsVal + ")")
	// The table is indexed into a variable first, and deliberately so: written
	// as `fn, swap := map[...]{...}[op]` the two-result form of a map index
	// takes over, so `swap` would be the *presence* of the key — always true
	// here — rather than the field.  That silently reversed every `>` and `>=`.
	compare := map[string]struct {
		name string
		swap bool
	}{
		"=":  {"gs_num_eq", false},
		"<":  {"gs_num_lt", false},
		">":  {"gs_num_lt", true},
		"<=": {"gs_num_le", false},
		">=": {"gs_num_le", true},
	}[op]
	lhs, rhs := a, b
	if compare.swap {
		lhs, rhs = b, a
	}
	asked := f.callNumCompare(compare.name, lhs, rhs)
	askedBool := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", askedBool, asked)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i1 [ %s, %%%s ], [ %s, %%%s ]\n",
		out, fast, fastFrom, askedBool, slowLabel)
	// A plain 0 or 1: the caller chains comparisons with `and i64` and wraps the
	// finished chain in a boolean tag.
	return f.zext(out), nil
}

// emitCheckedArith emits a +, - or * that falls back to the runtime when the
// machine-word result would not be the Scheme result.
//
// The check is LLVM's own overflow intrinsic, which sets a flag rather than
// trapping: `llvm.sadd.with.overflow.i64` returns the sum and a boolean, and the
// generated code tests the boolean.  Without it, `(* 1000000000000
// 1000000000000)` would produce a wrapped negative number where Scheme produces
// 10^24, and a compiled program would disagree with the interpreter — which is
// the one thing a compiler must never do.
//
// The operands are not assumed to be machine words: if either carries a handle
// tag the runtime does the arithmetic, which is what makes an exact integer of
// any size reachable from compiled code.  The two results are two words each,
// so they are spilled to slots and the join loads them — a phi node over four
// values would have to name the right predecessor for each, and the alloca is
// folded away again on the fast path.
func (f *irFunc) emitCheckedArith(op string, a, b irVal) (irVal, error) {
	intr := map[string]string{
		"+": "llvm.sadd.with.overflow.i64",
		"-": "llvm.ssub.with.overflow.i64",
		"*": "llvm.smul.with.overflow.i64",
	}[op]
	if intr == "" {
		return irVal{}, fmt.Errorf("ir: %s is not a checked operation", op)
	}
	// Both operands must be machine words for the intrinsic to mean anything —
	// unless the tags say they already are, which they do whenever the operands
	// were computed locally.  Asking anyway is what put an unreachable runtime
	// check on every arithmetic operation in every loop, and the optimizer could
	// not remove it because a tag arriving through a phi is not something it can
	// prove.
	knownFixnums := a.tag == tagFixnum && b.tag == tagFixnum
	// The slow path is the runtime's arithmetic.  It is reached when an operand
	// may not be a fixnum, and -- always -- when the machine operation
	// overflows, because a fixnum that no longer fits a word is a bignum and
	// only the runtime knows how to make one.
	//
	// **Known fixnums get no fast *block*.**  Opening a label for one put
	// `arith.fast_1:` where the previous block had already ended, which LLVM
	// rejects with "expected instruction opcode" -- and because the module is
	// assembled as a whole, that one malformed function cost the program every
	// native body it had.  The arithmetic goes straight into the current block
	// instead, and only the overflow test needs a branch.
	slowLabel := f.freshLabel("arith.slow")
	doneLabel := f.freshLabel("arith.done")

	var bitsSlot, tagSlot string
	if !knownFixnums {
		bitsSlot = f.alloca()
		tagSlot = f.alloca()
		fastLabel := f.freshLabel("arith.fast")
		tags := f.reg()
		fmt.Fprintf(&f.body, "  %s = or i64 %s, %s\n", tags, a.tag, b.tag)
		bothFixnum := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", bothFixnum, tags, tagFixnum)
		fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", bothFixnum, fastLabel, slowLabel)
		// The slow block is entered from the branch above, so it has to be
		// emitted after it; the fast block follows and falls into the check.
		f.block(fastLabel)
	}

	// The intrinsic returns { i64, i1 }; take it apart with extractvalue.
	pair := f.reg()
	fmt.Fprintf(&f.body, "  %s = call { i64, i1 } @%s(i64 %s, i64 %s)\n",
		pair, intr, a.bits, b.bits)
	val := f.reg()
	over := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue { i64, i1 } %s, 0\n", val, pair)
	fmt.Fprintf(&f.body, "  %s = extractvalue { i64, i1 } %s, 1\n", over, pair)
	if knownFixnums {
		// Nothing to join, and nothing that could have been a handle: the
		// operands are words by construction, so an answer that did not
		// overflow is a word too.  On overflow the runtime takes over, and it
		// returns through the same slots the general path uses -- so they are
		// allocated here rather than above.
		bitsSlot = f.alloca()
		tagSlot = f.alloca()
	}
	okLabel := f.freshLabel("arith.ok")
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", over, slowLabel, okLabel)
	f.block(okLabel)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", val, bitsSlot)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", tagFixnum, tagSlot)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(slowLabel)
	f.want(gsVal + " @gs_arith(i32, " + gsVal + ", " + gsVal + ")")
	slow := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_arith(i32 %d, %s %s, %s %s)\n",
		slow, gsVal, arithCode(op), gsVal, a.bits0(f), gsVal, b.bits0(f))
	slowBits := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 0\n", slowBits, gsVal, slow)
	slowTag := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 1\n", slowTag, gsVal, slow)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", slowBits, bitsSlot)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", slowTag, tagSlot)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	outBits := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", outBits, bitsSlot)
	outTag := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", outTag, tagSlot)
	return irVal{bits: outBits, tag: outTag}, nil
}
