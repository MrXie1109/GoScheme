// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"strings"
)

// Compiling a pure function to native code.
//
// A procedure is **pure** for this purpose when its body computes from its
// parameters and constants alone: no side effects, no global reads, no
// captured variables, no calls except to procedures that are themselves pure.
// `(define (add a b) (+ a b))` and `(define (fact n) (if (= n 0) 1 (* n (fact
// (- n 1)))))` are pure; `(define (f) (display 1))` is not, and neither is a
// procedure that closes over anything.
//
// A pure procedure is compiled to a real LLVM function — machine integers in
// registers, `br` for `if`, `call` for the recursive call — and it never
// touches the runtime.  That is the half of the hybrid that makes compiled
// programs fast, and the other half is everything this file refuses: anything
// it cannot prove is left for the runtime, which is the interpreter.
//
// The proof is conservative on purpose.  A procedure it accepts must be one
// whose meaning is exactly what the native code computes, so the checks below
// are of the form "this is the only thing the body does" rather than "this
// looks safe": an unboxed machine integer is not a Scheme number unless nothing
// in the body can make it something else, and `(* n (fact (- n 1)))` can
// overflow int64 where Scheme would have promoted to a bignum.
//
// Which is why the arithmetic is checked: a pure procedure is compiled natively
// **with an overflow test**, and takes the runtime path when the test fires.
// The fast path is the machine integer and the slow path is the interpreter,
// which is the arrangement the whole design rests on.

// pureReport says what a scan of a body found.
type pureReport struct {
	// ok is true when the whole body is a pure computation.
	ok bool
	// calls are the procedures the body calls by name, which have to be pure
	// themselves before this one can be.
	calls []string
	// why is what stopped the scan, for a message when a user asks why a
	// procedure was not compiled natively.
	why string
	// self is the name being defined, so that a recursive call is allowed.
	self string
}

// pureBody scans a procedure body and reports whether it can be compiled to
// native code, and what it calls.
//
// self is the name the procedure is being defined under, which is what makes a
// recursive procedure pure: a call to itself is a call to a pure procedure by
// construction, and `(fact (- n 1))` inside `fact` is the case that matters —
// the recursive shape is most of what a numeric procedure does.
func pureBody(formals []*Symbol, body []Value) pureReport {
	return pureBodyNamed("", formals, body)
}

// pureBodyNamed is pureBody with the procedure's own name, so that a recursive
// call is recognised.
func pureBodyNamed(self string, formals []*Symbol, body []Value) pureReport {
	r := &pureReport{ok: true, self: self}
	local := map[string]bool{}
	for _, s := range formals {
		local[s.Name] = true
	}
	for _, f := range body {
		r.scan(f, local)
		if !r.ok {
			return *r
		}
	}
	return *r
}

// scan walks one expression.
func (r *pureReport) scan(e Value, local map[string]bool) {
	switch x := e.(type) {
	case *Pair:
		r.scanCombination(x, local)
	case *Symbol:
		if !local[x.Name] {
			r.stop("%s is not a parameter", x.Name)
		}
	case *Integer, *Float, *Rational, *String, *Boolean, *Char, Empty:
		// A literal: it computes nothing.
	default:
		r.stop("the body contains %s, which is not a literal or a call", typeName(e))
	}
}

// scanCombination handles a call form.
func (r *pureReport) scanCombination(x *Pair, local map[string]bool) {
	head, ok := x.Car.(*Symbol)
	if !ok {
		r.stop("the operator is not a name")
		return
	}
	args, ok := ListToSlice(x.Cdr)
	if !ok {
		r.stop("the argument list is improper")
		return
	}
	switch head.Name {
	case "if":
		if len(args) < 2 || len(args) > 3 {
			r.stop("if takes two or three parts")
			return
		}
		for _, a := range args {
			r.scan(a, local)
		}
	case "let":
		r.scanLet(args, local, false)
	case "let*":
		r.scanLet(args, local, true)
	case "begin":
		if len(args) == 0 {
			r.stop("an empty begin is not a value")
			return
		}
		for _, a := range args {
			r.scan(a, local)
		}
	case "quote":
		if len(args) != 1 {
			r.stop("quote takes one part")
		}
	case "and", "or":
		for _, a := range args {
			r.scan(a, local)
		}
	default:
		// A call: an operation this generator can emit, or the procedure
		// calling itself, which is pure by construction.
		isSelf := r.self != "" && head.Name == r.self
		if !pureOperator(head.Name) && !isSelf {
			r.stop("%s is not an operation this can compile", head.Name)
			return
		}
		for _, a := range args {
			r.scan(a, local)
		}
		if !isSelf {
			r.calls = append(r.calls, head.Name)
		}
	}
}

// scanLet handles let and let*, which introduce bindings a body may use.
func (r *pureReport) scanLet(args []Value, outer map[string]bool, sequential bool) {
	if len(args) < 1 {
		r.stop("a let with no bindings")
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		r.stop("the bindings are malformed")
		return
	}
	local := map[string]bool{}
	for k := range outer {
		local[k] = true
	}
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			r.stop("a binding is malformed")
			return
		}
		items, _ := ListToSlice(p)
		if len(items) != 2 {
			r.stop("a binding needs a name and a value")
			return
		}
		name, ok := items[0].(*Symbol)
		if !ok {
			r.stop("a binding name is not an identifier")
			return
		}
		// The initialiser sees the outer scope for let, and the bindings so far
		// for let*.
		if sequential {
			r.scan(items[1], local)
		} else {
			r.scan(items[1], outer)
		}
		if !r.ok {
			return
		}
		local[name.Name] = true
	}
	for _, b := range args[1:] {
		r.scan(b, local)
	}
}

func (r *pureReport) stop(why string, args ...interface{}) {
	if r.ok {
		r.ok = false
		r.why = fmt.Sprintf(why, args...)
	}
}

// pureOperator reports whether a name is one this generator can emit as a
// machine operation.  Everything else — display, cons, car, a user procedure —
// is the runtime's, which is what keeps the accepted set small enough to be
// sure of.
func pureOperator(name string) bool {
	switch name {
	case "+", "-", "*", "=", "<", ">", "<=", ">=",
		"quotient", "remainder", "modulo", "abs", "min", "max",
		"zero?", "positive?", "negative?", "even?", "odd?", "not":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Emitting native code for a pure procedure
// ---------------------------------------------------------------------------

// irFunc is one native function being generated.
type irFunc struct {
	// name is the symbol the function is emitted under, which the runtime is
	// also told about so that a call through a variable finds the native code.
	name string
	// params are the formals, in order, as SSA values.
	params []string
	// locals maps a name to the SSA value or slot holding it.  A parameter is a
	// value; a let binding is a slot, because it is assigned once and read
	// many times.
	locals map[string]string
	// entry collects the allocas, which LLVM wants in the entry block, and
	// body the rest of the instructions.
	entry strings.Builder
	body  strings.Builder
	// slots are the alloca'd locals, and nextSlot counts them.
	slots    []string
	nextSlot int
	// label counts the basic blocks within this function, and nextReg the
	// SSA values.
	label   int
	nextReg int
	// currentBlock is the basic block the emitter is writing into, which a phi
	// node needs to name its predecessor.
	currentBlock string
	// calls are the natively-compiled procedures this one may call, and self is
	// its own name, which a recursive call names.
	calls []string
	self  string
}

// emitPureFunction writes a native LLVM function for a pure procedure.
//
// Every value in it is an i64: a Scheme fixnum.  That is the whole point of
// accepting only a pure body — a machine integer is a Scheme exact integer
// exactly as long as nothing can turn it into something else, and the only
// things the accepted body can do are arithmetic on parameters, constants, and
// calls to procedures that are pure in the same way.  Overflow is the one way
// out, and the generated code checks for it and takes the runtime path (see
// emitCheckedArith), because a Scheme integer that leaves the fixnum range
// becomes a bignum rather than wrapping.
func (g *irGen) emitPureFunction(name string, formals []*Symbol, body []Value, calls []string) error {
	f := &irFunc{
		name:   name,
		self:   name,
		locals: map[string]string{},
		calls:  calls,
	}
	// The parameters arrive as i64 and stay in SSA values: a parameter is never
	// assigned in Scheme without set!, which a pure body cannot contain.
	var sig strings.Builder
	fmt.Fprintf(&sig, "define i64 @%s(", mangle(name))
	for i, p := range formals {
		if i > 0 {
			sig.WriteString(", ")
		}
		reg := fmt.Sprintf("%%p_%s", p.Name)
		fmt.Fprintf(&sig, "i64 %s", reg)
		f.params = append(f.params, reg)
		f.locals[p.Name] = reg
	}
	sig.WriteString(") {\n")
	sig.WriteString("entry:\n")
	// The runtime entry points a pure body may need: the overflow fallback, and
	// the three intrinsics it uses to detect the overflow.  A pure function
	// normally touches neither, and the declarations are emitted anyway because
	// they are what makes the calls in the body type-check — LLVM reports the
	// *call* as the error when a declaration is missing, which is a misleading
	// place for a missing declaration to show up.
	m := g.module
	m.declare("i64 @gs_arith(i32, i64, i64)")
	m.declare("{ i64, i1 } @llvm.sadd.with.overflow.i64(i64, i64)")
	m.declare("{ i64, i1 } @llvm.ssub.with.overflow.i64(i64, i64)")
	m.declare("{ i64, i1 } @llvm.smul.with.overflow.i64(i64, i64)")

	// A recursive call names this function and needs no declaration of it: a
	// define is visible to its own body, and adding a declare as well is a
	// redefinition error — which `opt -passes=verify` said the first time this
	// was tried, and is the reason the check is part of the build below.

	val, err := f.emitExpr(Cons(Intern("begin"), listFromSlice(body)))
	if err != nil {
		return err
	}
	var out strings.Builder
	out.WriteString(sig.String())
	out.WriteString(f.entry.String())
	out.WriteString(f.body.String())
	fmt.Fprintf(&out, "  ret i64 %s\n}\n\n", val)
	g.module.body.WriteString(out.String())
	g.native++
	return nil
}

// emitExpr generates code for one expression and returns the SSA value it
// computes.
func (f *irFunc) emitExpr(e Value) (string, error) {
	switch x := e.(type) {
	case *Integer:
		if !x.small {
			return "", fmt.Errorf("ir: %s is too large to be a machine integer", x.String())
		}
		return fmt.Sprintf("%d", x.i), nil
	case *Boolean:
		if *x {
			return "1", nil
		}
		return "0", nil
	case *Symbol:
		v, ok := f.locals[x.Name]
		if !ok {
			return "", fmt.Errorf("ir: %s is not bound", x.Name)
		}
		return v, nil
	case Empty:
		return "0", nil
	case *Pair:
		return f.emitForm(x)
	default:
		return "", fmt.Errorf("ir: cannot emit %s", typeName(e))
	}
}

// emitForm handles a call form.
func (f *irFunc) emitForm(x *Pair) (string, error) {
	head, _ := x.Car.(*Symbol)
	if head == nil {
		return "", fmt.Errorf("ir: the operator is not a name")
	}
	args, _ := ListToSlice(x.Cdr)
	switch head.Name {
	case "begin":
		var last string
		var err error
		for _, a := range args {
			last, err = f.emitExpr(a)
			if err != nil {
				return "", err
			}
		}
		return last, nil
	case "if":
		return f.emitIf(args)
	case "let", "let*":
		return f.emitLet(args, head.Name == "let*")
	case "and", "or":
		return f.emitAndOr(args, head.Name == "and")
	case "quote":
		if len(args) != 1 {
			return "", fmt.Errorf("ir: quote takes one part")
		}
		return f.emitQuoted(args[0])
	}
	return f.emitCall(head.Name, args)
}

// mangle turns a Scheme name into an LLVM symbol.  A Scheme name may hold
// characters an LLVM identifier cannot, and two names must not collide.
func mangle(name string) string {
	var b strings.Builder
	b.WriteString("gs_lam_")
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "_%02x", c)
		}
	}
	return b.String()
}

// emitIf emits a conditional.  Both arms produce an i64 and the result is
// selected by the branch, which is what a phi node is for.
func (f *irFunc) emitIf(args []Value) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("ir: if takes two or three parts")
	}
	test, err := f.emitExpr(args[0])
	if err != nil {
		return "", err
	}
	thenLabel := f.freshLabel("then")
	elseLabel := f.freshLabel("else")
	endLabel := f.freshLabel("endif")
	// A Scheme value is true unless it is #f, which is 0 here; every value the
	// accepted body can produce is a number, and a number is true.
	cond := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", cond, test)
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", cond, thenLabel, elseLabel)

	fmt.Fprintf(&f.body, "%s:\n", thenLabel)
	thenVal, err := f.emitExpr(args[1])
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)

	fmt.Fprintf(&f.body, "%s:\n", elseLabel)
	elseVal := "0"
	if len(args) > 2 {
		elseVal, err = f.emitExpr(args[2])
		if err != nil {
			return "", err
		}
	}
	fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)

	fmt.Fprintf(&f.body, "%s:\n", endLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i64 [ %s, %%%s ], [ %s, %%%s ]\n",
		out, thenVal, thenLabel, elseVal, elseLabel)
	return out, nil
}

// emitAndOr emits and/or, which return the value that decided them rather than
// a boolean.
func (f *irFunc) emitAndOr(args []Value, isAnd bool) (string, error) {
	if len(args) == 0 {
		if isAnd {
			return "1", nil
		}
		return "0", nil
	}
	if len(args) == 1 {
		return f.emitExpr(args[0])
	}
	// The chain is short-circuiting, so each step is a branch that either
	// continues or yields this value.
	var slots []string
	endLabel := f.freshLabel("end")
	for i, a := range args {
		v, err := f.emitExpr(a)
		if err != nil {
			return "", err
		}
		if i == len(args)-1 {
			slots = append(slots, v)
			break
		}
		next := f.freshLabel("next")
		cond := f.reg()
		if isAnd {
			fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", cond, v)
		} else {
			fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, 0\n", cond, v)
		}
		keep := f.freshLabel("keep")
		fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", cond, next, keep)
		fmt.Fprintf(&f.body, "%s:\n", keep)
		slots = append(slots, v)
		fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)
		fmt.Fprintf(&f.body, "%s:\n", next)
	}
	last := slots[len(slots)-1]
	fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)
	fmt.Fprintf(&f.body, "%s:\n", endLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i64 [ %s, %%%s ]\n", out, last, endLabel)
	return out, nil
}

// emitLet emits a let: each binding is computed and kept in a slot, because a
// body may read it more than once.
func (f *irFunc) emitLet(args []Value, sequential bool) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("ir: a let with no bindings")
	}
	bindings, _ := ListToSlice(args[0])
	saved := map[string]string{}
	for k, v := range f.locals {
		saved[k] = v
	}
	for _, b := range bindings {
		p := b.(*Pair)
		items, _ := ListToSlice(p)
		name := items[0].(*Symbol)
		val, err := f.emitExpr(items[1])
		if err != nil {
			return "", err
		}
		if sequential {
			// Each binding is visible to the next, so assign as we go.
			f.locals[name.Name] = val
			continue
		}
		// A parallel let: the initialisers all see the outer scope, so the
		// values are held aside and assigned together.
		f.locals[name.Name] = val
	}
	var last string
	var err error
	for _, b := range args[1:] {
		last, err = f.emitExpr(b)
		if err != nil {
			return "", err
		}
	}
	f.locals = saved
	if last == "" {
		last = "0"
	}
	return last, nil
}

// emitQuoted emits a quoted literal the accepted body can hold: a number.
func (f *irFunc) emitQuoted(v Value) (string, error) {
	switch x := v.(type) {
	case *Integer:
		if !x.small {
			return "", fmt.Errorf("ir: a quoted integer too large for a machine word")
		}
		return fmt.Sprintf("%d", x.i), nil
	case *Boolean:
		if *x {
			return "1", nil
		}
		return "0", nil
	case Empty:
		return "0", nil
	}
	return "", fmt.Errorf("ir: a quoted value that is not a number")
}

// freshLabel returns a basic-block label unique within this function.
func (f *irFunc) freshLabel(prefix string) string {
	f.label++
	return fmt.Sprintf("%s_%d", prefix, f.label)
}

func (f *irFunc) reg() string {
	f.nextReg++
	return fmt.Sprintf("%%v%d", f.nextReg)
}

// emitCall emits a call: an arithmetic operation, a comparison, or a call to a
// procedure that was itself compiled natively.
//
// The arithmetic is **checked**.  A Scheme exact integer is unbounded, so
// `(* n (fact (- n 1)))` on a machine word can overflow where Scheme would have
// promoted to a bignum; the generated code tests for it and, when it happens,
// calls the runtime, which is the interpreter and knows about bignums.  That is
// what makes it safe to compile a procedure natively without proving anything
// about the size of its values.
func (f *irFunc) emitCall(op string, args []Value) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("ir: %s takes at least one argument", op)
	}
	vals := make([]string, 0, len(args))
	for _, a := range args {
		v, err := f.emitExpr(a)
		if err != nil {
			return "", err
		}
		vals = append(vals, v)
	}
	switch op {
	case "+", "-", "*":
		acc := vals[0]
		if len(vals) == 1 {
			if op == "-" {
				return f.emitCheckedNeg(acc)
			}
			return acc, nil // (+ x) is x, (* x) is x
		}
		var err error
		for _, v := range vals[1:] {
			acc, err = f.emitCheckedArith(op, acc, v)
			if err != nil {
				return "", err
			}
		}
		return acc, nil
	case "=", "<", ">", "<=", ">=":
		// Chained comparison, as Scheme has it: (< 1 2 3) is (< 1 2) and (< 2 3).
		if len(vals) < 2 {
			return "1", nil
		}
		acc := "1"
		for i := 0; i+1 < len(vals); i++ {
			cmp := f.emitCompare(op, vals[i], vals[i+1])
			both := f.reg()
			fmt.Fprintf(&f.body, "  %s = and i64 %s, %s\n", both, acc, cmp)
			acc = both
		}
		return acc, nil
	case "zero?":
		c := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, 0\n", c, vals[0])
		return f.zext(c), nil
	case "positive?":
		c := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp sgt i64 %s, 0\n", c, vals[0])
		return f.zext(c), nil
	case "negative?":
		c := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp slt i64 %s, 0\n", c, vals[0])
		return f.zext(c), nil
	case "not":
		c := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, 0\n", c, vals[0])
		return f.zext(c), nil
	case "abs":
		c := f.reg()
		n := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp slt i64 %s, 0\n", c, vals[0])
		fmt.Fprintf(&f.body, "  %s = sub i64 0, %s\n", n, vals[0])
		out := f.reg()
		fmt.Fprintf(&f.body, "  %s = select i1 %s, i64 %s, i64 %s\n", out, c, n, vals[0])
		return out, nil
	case "min", "max":
		acc := vals[0]
		for _, v := range vals[1:] {
			c := f.reg()
			if op == "min" {
				fmt.Fprintf(&f.body, "  %s = icmp slt i64 %s, %s\n", c, v, acc)
			} else {
				fmt.Fprintf(&f.body, "  %s = icmp sgt i64 %s, %s\n", c, v, acc)
			}
			out := f.reg()
			fmt.Fprintf(&f.body, "  %s = select i1 %s, i64 %s, i64 %s\n", out, c, v, acc)
			acc = out
		}
		return acc, nil
	}
	// A call to another procedure.  It is native only if that procedure was
	// compiled; otherwise the whole body would not have been accepted, since
	// the scan only allows operators it knows.
	if !f.known(op) && op != f.self {
		return "", fmt.Errorf("ir: %s is not a native procedure", op)
	}
	out := f.reg()
	argList := strings.Join(vals, ", ")
	fmt.Fprintf(&f.body, "  %s = call i64 @%s(%s)\n", out, mangle(op), argList)
	return out, nil
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

// emitCompare emits an ordering comparison, which has to agree with Scheme:
// integers are exact and unbounded here, so a comparison of two machine words
// is the same comparison.
func (f *irFunc) emitCompare(op, a, b string) string {
	pred := map[string]string{
		"=": "eq", "<": "slt", ">": "sgt", "<=": "sle", ">=": "sge",
	}[op]
	c := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp %s i64 %s, %s\n", c, pred, a, b)
	return f.zext(c)
}

// emitCheckedArith emits a +, - or * that falls back to the runtime when the
// machine-word result would not be the Scheme result.
//
// The check is LLVM's own overflow intrinsic, which sets a flag rather than
// trapping: `llvm.sadd.with.overflow.i64` returns the sum and a boolean, and
// the generated code tests the boolean.  Without it, `(* 1000000000000
// 1000000000000)` would produce a wrapped negative number where Scheme produces
// 10^24, and a compiled program would disagree with the interpreter — which is
// the one thing a compiler must never do.
//
// The result is written to a slot rather than selected by a phi node.  Both
// branches store to the same alloca and the join loads it, which needs no
// bookkeeping of which block branched from where — the alternative is keeping
// track of the current block for every phi, and the register allocator folds
// the alloca away again on the fast path.
func (f *irFunc) emitCheckedArith(op, a, b string) (string, error) {
	intr := map[string]string{
		"+": "llvm.sadd.with.overflow.i64",
		"-": "llvm.ssub.with.overflow.i64",
		"*": "llvm.smul.with.overflow.i64",
	}[op]
	if intr == "" {
		return "", fmt.Errorf("ir: %s is not a checked operation", op)
	}
	slot := f.alloca()
	// The intrinsic returns { i64, i1 }; take it apart with extractvalue.
	pair := f.reg()
	fmt.Fprintf(&f.body, "  %s = call { i64, i1 } @%s(i64 %s, i64 %s)\n", pair, intr, a, b)
	val := f.reg()
	over := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue { i64, i1 } %s, 0\n", val, pair)
	fmt.Fprintf(&f.body, "  %s = extractvalue { i64, i1 } %s, 1\n", over, pair)

	// The fast path stores its value and jumps to the join; the slow path does
	// the same with what the runtime returned.  Both store to the same slot, so
	// the join needs no phi — only one of the two runs, and the load sees which.
	fastLabel := f.freshLabel("fast")
	slowLabel := f.freshLabel("slow")
	doneLabel := f.freshLabel("done")
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", over, slowLabel, fastLabel)

	fmt.Fprintf(&f.body, "%s:\n", fastLabel)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", val, slot)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	fmt.Fprintf(&f.body, "%s:\n", slowLabel)
	slow := f.reg()
	fmt.Fprintf(&f.body, "  %s = call i64 @gs_arith(i32 %d, i64 %s, i64 %s)\n",
		slow, arithCode(op), a, b)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", slow, slot)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	fmt.Fprintf(&f.body, "%s:\n", doneLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", out, slot)
	return out, nil
}

// alloca makes a slot.  LLVM requires the alloca instruction to be in the entry
// block, so the declaration is collected here and written out when the function
// is assembled — the body is generated before the entry block is final.
func (f *irFunc) alloca() string {
	f.nextSlot++
	name := fmt.Sprintf("%%slot%d", f.nextSlot)
	fmt.Fprintf(&f.entry, "  %s = alloca i64\n", name)
	f.slots = append(f.slots, name)
	return name
}

// emitCheckedNeg emits a negation that falls back to the runtime on overflow,
// which is the one case int64 cannot represent: -(-2^63).
func (f *irFunc) emitCheckedNeg(a string) (string, error) {
	return f.emitCheckedArith("-", "0", a)
}

// arithCode is the small integer the runtime uses to know which operation the
// fallback is for.
func arithCode(op string) int {
	switch op {
	case "+":
		return 0
	case "-":
		return 1
	case "*":
		return 2
	}
	return -1
}

// topLevelProcedure recognises (define (name args...) body...), the shape a
// procedure definition has when it can be compiled natively.
//
// Only that shape: (define name (lambda ...)) is the same thing written
// differently and is not recognised, because the scan would have to look
// through the lambda and the emitted symbol would have to match what the
// runtime bound — which it can, but keeping to one shape is one fewer thing to
// get wrong in a compiler whose contract is that it never changes what a
// program means.
func topLevelProcedure(form Value) (name string, formals []*Symbol, body []Value, ok bool) {
	p, isPair := form.(*Pair)
	if !isPair {
		return "", nil, nil, false
	}
	head, isSym := p.Car.(*Symbol)
	if !isSym || head.Name != "define" {
		return "", nil, nil, false
	}
	items, _ := ListToSlice(p.Cdr)
	if len(items) < 2 {
		return "", nil, nil, false
	}
	sig, isPair := items[0].(*Pair)
	if !isPair {
		return "", nil, nil, false
	}
	headSym, isSym := sig.Car.(*Symbol)
	if !isSym {
		return "", nil, nil, false
	}
	params, isList := ListToSlice(sig.Cdr)
	if !isList {
		return "", nil, nil, false // a rest parameter: not this shape
	}
	for _, prm := range params {
		s, isSym := prm.(*Symbol)
		if !isSym {
			return "", nil, nil, false
		}
		formals = append(formals, s)
	}
	return headSym.Name, formals, items[1:], true
}

// paramTypes is the LLVM parameter list for n i64 parameters.
func paramTypes(n int) string {
	if n == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("i64, ", n), ", ")
}
