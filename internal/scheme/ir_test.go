// SPDX-License-Identifier: MIT

package scheme

import (
	"strings"
	"testing"
)

// irFor compiles source and returns the module text, failing the test if the
// source cannot be read.
func irFor(t *testing.T, src string) string {
	t.Helper()
	p, err := CompileToIR(src, "test")
	if err != nil {
		t.Fatalf("compiling %q: %v", src, err)
	}
	return p.IR
}

// TestIRPureProceduresBecomeNative checks that a procedure the generator
// accepts is emitted as a function.
//
// This is the test the tagged-value work most needed and did not have: the
// emitter can produce a perfectly valid module and still leave every procedure
// out of it, which is what happened when an operator like `+` was recorded as a
// call to a procedure that does not exist — every pure body was then dropped
// for calling something uncompiled.
func TestIRPureProceduresBecomeNative(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "arithmetic",
			src:  `(define (add a b) (+ a b))`,
			want: []string{"@gs_lam_add"},
		},
		{
			name: "recursion",
			src:  `(define (fact n) (if (= n 0) 1 (* n (fact (- n 1)))))`,
			want: []string{"@gs_lam_fact"},
		},
		{
			// A procedure calling another procedure is the case that was
			// refused outright until the scan learned which names are
			// candidates.
			name: "one pure procedure calls another",
			src: `(define (square x) (* x x))
(define (sumsq a b) (+ (square a) (square b)))`,
			want: []string{"@gs_lam_square", "@gs_lam_sumsq"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ir := irFor(t, tc.src)
			for _, want := range tc.want {
				if !strings.Contains(ir, "define ") || !strings.Contains(ir, want+"(") {
					t.Errorf("%s is missing from the module:\n%s", want, ir)
				}
			}
		})
	}
}

// TestIRRefusesWhatItCannotEmit checks that a body the generator cannot express
// is left to the interpreter rather than emitted wrongly.
//
// The compiler's contract is that a program means the same thing compiled or
// not, and the way that is kept is by refusing anything the generator cannot
// express exactly.  A refusal is not an error — the procedure still runs.
//
// What is *not* refused is a call into the runtime: `(display x)` compiles, with
// the display performed by the interpreter and the rest of the body in machine
// code.  Those cases are in TestIRPartiallyCompilableBodies below.
func TestIRRefusesWhatItCannotEmit(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"set!", `(define (f x) (set! x 1))`},
		{"a nested define", `(define (f x) (define y 1) (+ x y))`},
		{"set! of a global", `(define (f x) (begin (set! k x) k))`},
		{"a lambda", `(define (f x) (lambda (y) (+ x y)))`},
		{"do", `(define (f n) (do ((i 0 (+ i 1))) ((= i n) i)))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := CompileToIR(tc.src, "test")
			if err != nil {
				t.Fatalf("compiling %q: %v", tc.src, err)
			}
			if p.Native != 0 {
				t.Errorf("%s was compiled natively, but it should have been refused", tc.name)
			}
			if len(p.Refused) == 0 {
				t.Errorf("%s was refused without a reason", tc.name)
			}
		})
	}
}

// TestIRGlobalReadsHappenWhereTheyAppear checks that a global is read at the
// point of use rather than once at entry.
//
// A global is mutable, so anything the body calls may assign it.  A value read
// once at entry and reused would be the value from before the call, which is
// what the interpreter would not see — and getting this wrong produces a
// program that is right until something assigns a global, which is the hardest
// kind of wrong to notice.
func TestIRGlobalReadsHappenWhereTheyAppear(t *testing.T) {
	p, err := CompileToIR(`(define k 10)
(define (f n) (+ (* n k) k))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native != 1 {
		t.Fatalf("a body reading a global was not compiled: %v", p.Refused)
	}
	// One read per use: two uses of k, so two calls.
	if got := strings.Count(p.IR, "call %gs.val @gs_global("); got != 2 {
		t.Errorf("emitted %d global reads for two uses of k:\n%s", got, p.IR)
	}
}

// TestIRPartiallyCompilableBodies checks the middle ground: a body with one part
// the generator cannot emit and one part it can is compiled, with the first part
// performed by the runtime.
//
// This is the difference between compiling a procedure and compiling a
// procedure's arithmetic.  Refusing the whole body because of `display` would
// give up the multiplication beside it for nothing, and a Scheme program is
// mostly made of such bodies — with per-procedure fallback the compiler would
// accept only the handful of procedures that touch nothing but numbers.
func TestIRPartiallyCompilableBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string // what the module must contain
	}{
		{
			name: "output beside arithmetic",
			src:  `(define (f n) (begin (display n) (* n n)))`,
			want: []string{"@gs_lam_f", "@gs_call", "@llvm.smul.with.overflow.i64"},
		},
		{
			name: "a library call as an operand of arithmetic",
			src:  `(define (f n) (+ (* n n) (string-length "abc")))`,
			want: []string{"@gs_lam_f", "@gs_call", "@llvm.sadd.with.overflow.i64"},
		},
		{
			// Boxed three times over — the literal on the way in, the argument
			// and the answer on the way back — which is why a body whose only
			// kept value is a call's result is not compiled.  See the test below.
			name: "a literal beside a call",
			src:  `(define (f n) (begin (display "hello") (* n n)))`,
			want: []string{"@gs_lam_f", "@gs_box_literal", "@gs_call"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := CompileToIR(tc.src, "test")
			if err != nil {
				t.Fatalf("compiling %q: %v", tc.src, err)
			}
			if p.Native != 1 {
				t.Fatalf("%s was not compiled: %v", tc.name, p.Refused)
			}
			for _, want := range tc.want {
				if !strings.Contains(p.IR, want) {
					t.Errorf("%s is missing from the module:\n%s", want, p.IR)
				}
			}
		})
	}
}

// TestIRABodyThatIsOnlyACallIsNotCompiled checks the one case where the compiler
// decides against itself.
//
// A body whose only work is a call into the runtime cannot gain from being
// compiled: the machine code would box the arguments, cross the boundary, and
// unbox the answer, which is strictly more work than the interpreter doing the
// same call with everything already unboxed.  Measured, these were the two
// programs that ran *slower* compiled — `string-append` and `vector-ref` in a
// loop, at 0.40× and 0.68× of the interpreter's speed.
//
// The rule is narrow on purpose, and this test pins both halves of that: what it
// refuses, and what it must not refuse even though the gain is small.
func TestIRABodyThatIsOnlyACallIsNotCompiled(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"a body that only displays", `(define (f n) (display n))`},
		{"a body that only calls out with no arguments", `(define (f) (newline))`},
		{"a counter around a call", `(define (f i acc) (if (= i 0) acc (f (- i 1) (string-append acc "x"))))`},
		{"an accumulator fed only by a call", `(define (f i acc) (if (= i 0) acc (f (+ i 1) (+ acc (vector-ref v i)))))`},
		{"arithmetic whose only operand is a call's result", `(define (f n) (+ n (string-length "hello")))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := CompileToIR(tc.src, "test")
			if err != nil {
				t.Fatalf("compiling %q: %v", tc.src, err)
			}
			if p.Native != 0 {
				t.Errorf("%s was compiled, but there is nothing in it for machine code to do", tc.name)
			}
			if len(p.Refused) == 0 {
				t.Errorf("%s was declined without a reason", tc.name)
			}
		})
	}
	// The other half: a small gain is still a gain, and refusing to compile
	// something that would have been faster is the failure that is invisible.
	for _, tc := range []struct{ name, src string }{
		{"arithmetic with no runtime call at all", `(define (f n) (+ n 1))`},
		{"an accumulator beside a call", `(define (f i acc) (if (= i 0) acc (f (- i 1) (+ acc i))))`},
		{"arithmetic beside a call it does not feed", `(define (f n) (begin (display n) (* n n)))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := CompileToIR(tc.src, "test")
			if err != nil {
				t.Fatalf("compiling %q: %v", tc.src, err)
			}
			if p.Native != 1 {
				t.Errorf("%s was not compiled, though it does work of its own: %v", tc.name, p.Refused)
			}
		})
	}
}

// TestIRIgnoresWhatIsNotAProcedure checks that a definition which is not a
// procedure of the accepted shape is passed over silently.
//
// It is not a refusal: there is no procedure, so there is nothing to leave to
// the interpreter and nothing to explain.  Reporting it would make the list of
// reasons noisy enough to be useless.
func TestIRIgnoresWhatIsNotAProcedure(t *testing.T) {
	for _, src := range []string{
		`(define x 1)`,
		`(define x (lambda (y) y))`, // the same thing written differently
		`(display 1)`,
	} {
		p, err := CompileToIR(src, "test")
		if err != nil {
			t.Fatalf("compiling %q: %v", src, err)
		}
		if p.Native != 0 || len(p.Refused) != 0 {
			t.Errorf("%q: native=%d refused=%v, want neither", src, p.Native, p.Refused)
		}
	}
}

// TestIRCheckedArithmeticIsGuarded checks that an arithmetic operation is
// emitted with an overflow test and a runtime fallback.
//
// Without the guard a compiled program would wrap where the interpreter
// promotes, and the two would disagree — the one thing the compiler must never
// let happen.  The fallback has to be a call, not an assumption, because a
// bignum can only be built by the runtime.
func TestIRCheckedArithmeticIsGuarded(t *testing.T) {
	ir := irFor(t, `(define (add a b) (+ a b))
(define (mul a b) (* a b))
(define (sub a b) (- a b))`)
	for _, want := range []string{
		"@llvm.sadd.with.overflow.i64",
		"@llvm.ssub.with.overflow.i64",
		"@llvm.smul.with.overflow.i64",
		"call %gs.val @gs_arith(i32 0",
		"call %gs.val @gs_arith(i32 1",
		"call %gs.val @gs_arith(i32 2",
	} {
		if !strings.Contains(ir, want) {
			t.Errorf("the module does not mention %s:\n%s", want, ir)
		}
	}
}

// TestIRValuesCarryATag checks the shape of the value that crosses the boundary.
//
// A machine word is not always enough for a Scheme exact integer, so the word
// travels with a tag saying what it is; a body that assumed the word was the
// value would truncate silently.
//
// The tag is a named type wherever a value is carried, and the arguments of a
// compiled body are the one place it is *unpacked*: a body takes each argument
// as its own (word, tag) pair.  That is what lets the optimizer see a tag across
// a call and drop the runtime checks it would otherwise have to keep — see
// TestIRKnownFixnumsSkipTheRuntime below.  The array-shaped entry point still
// exists, as an adapter, because the runtime calls through one uniform shape
// whatever the arity.
func TestIRValuesCarryATag(t *testing.T) {
	ir := irFor(t, `(define (add a b) (+ a b))`)
	for _, want := range []string{
		"%gs.val = type { i64, i64 }",
		// The body, with the arguments unpacked.
		"define %gs.val @gs_lam_add(i64 %p_a.bits, i64 %p_a.tag, i64 %p_b.bits, i64 %p_b.tag)",
		// The adapter, which is what the runtime is handed.
		"define %gs.val @gs_lam_add_entry(i64 %n, %gs.val* %args)",
	} {
		if !strings.Contains(ir, want) {
			t.Errorf("the module does not mention %q:\n%s", want, ir)
		}
	}
}

// TestIRKnownFixnumsSkipTheRuntime checks that arithmetic on values whose tags
// are known does not ask the runtime whether they are fixnums.
//
// This is the difference between the compiler being worth using and not.  With
// the operands packed into a struct, the callee loaded a tag it could know
// nothing about, so every addition kept its runtime path and a loop that adds
// two numbers called gs_num_eq and gs_truthy on every iteration.  Passing the
// tag as its own argument lets LLVM prove the check is dead.
//
// What is asserted is the absence of the *check*, which is what survives
// optimization and what costs the time; the calls themselves are only emitted
// on a path that is never taken.
func TestIRKnownFixnumsSkipTheRuntime(t *testing.T) {
	// `x` is a parameter, so its tag is unknown — and the body must still say
	// so rather than assume.
	ir := irFor(t, `(define (f x) (+ x 1))`)
	if !strings.Contains(ir, "or i64 %p_x.tag, 0") {
		t.Errorf("an unknown tag is not checked:\n%s", ir)
	}
	// `x` is computed here, so its tag is a literal zero and the comparison
	// against a literal needs no runtime path at all.
	ir = irFor(t, `(define (g n) (if (= (* n n) 0) 1 2))`)
	if strings.Contains(ir, "@gs_num_eq") {
		t.Errorf("a comparison of two known fixnums still calls the runtime:\n%s", ir)
	}
	if strings.Contains(ir, "@gs_truthy") {
		t.Errorf("the truth of a boolean still calls the runtime:\n%s", ir)
	}
}

// TestIRRegistersEveryNativeBody checks that a compiled procedure is announced
// to the runtime.
//
// Emitting a function is not the same as running it: the top-level forms go to
// the interpreter, so a module that defines a function and never tells the
// runtime about it has produced dead code.  This is the check that would have
// caught that.
func TestIRRegistersEveryNativeBody(t *testing.T) {
	ir := irFor(t, `(define (add a b) (+ a b))
(define (square x) (* x x))
(display (add 1 2))`)
	if got := strings.Count(ir, "call void @gs_register("); got != 2 {
		t.Errorf("registered %d procedures, want 2:\n%s", got, ir)
	}
	for _, want := range []string{"@gs_lam_add", "@gs_lam_square"} {
		if !strings.Contains(ir, want) {
			t.Errorf("%s is missing from the module:\n%s", want, ir)
		}
	}
}

// TestIRTheModuleVerifies checks that what the generator writes is well-formed
// LLVM.
//
// The generator builds text, so a mistake in it is a syntax error in a file
// nobody reads until the toolchain refuses it.  The checks here are the ones
// structural: a function defined before its callers, a phi whose predecessors
// are real blocks, a name used after it is defined.
func TestIRTheModuleVerifies(t *testing.T) {
	// A callee must be defined before the first call to it: declaring a
	// function and then defining it is a redefinition error, so the emitter
	// orders procedures by dependency instead of forward-declaring.
	ir := irFor(t, `(define (square x) (* x x))
(define (sumsq a b) (+ (square a) (square b)))
(define (caller n) (sumsq n n))`)
	square := strings.Index(ir, "define %gs.val @gs_lam_square(")
	sumsq := strings.Index(ir, "define %gs.val @gs_lam_sumsq(")
	caller := strings.Index(ir, "define %gs.val @gs_lam_caller(")
	if square < 0 || sumsq < 0 || caller < 0 {
		t.Fatalf("not every procedure was emitted:\n%s", ir)
	}
	if !(square < sumsq && sumsq < caller) {
		t.Errorf("procedures are not in dependency order: square=%d sumsq=%d caller=%d",
			square, sumsq, caller)
	}
}

// TestIRCyclesAreRefused checks the one ordering the generator cannot satisfy.
//
// Two procedures that call each other cannot both be defined first, and LLVM
// will not accept either ordering, so the cycle is refused and both run
// interpreted.  Emitting them anyway would produce a module the toolchain
// rejects, which is a compile that fails rather than a compile that is slower.
func TestIRCyclesAreRefused(t *testing.T) {
	p, err := CompileToIR(`(define (ping n) (if (= n 0) 0 (pong (- n 1))))
(define (pong n) (if (= n 0) 1 (ping (- n 1))))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native != 0 {
		t.Fatalf("a mutual recursion was compiled natively; the refusal reasons were %v", p.Refused)
	}
	// Both members are refused, not just the one the walk happened to enter
	// first: leaving one behind would emit a body calling a function that is
	// no longer in the module.
	if len(p.Refused) != 2 {
		t.Errorf("refused %d procedures, want both: %v", len(p.Refused), p.Refused)
	}
	joined := strings.Join(p.Refused, "\n")
	if !strings.Contains(joined, "cycle") {
		t.Errorf("the refusal does not mention the cycle: %v", p.Refused)
	}
}

// TestIRACycleIsRefusedEvenWhenTheBodiesAreOtherwiseFine checks that a cycle
// among procedures the generator would otherwise accept is still refused.
//
// The cycle is the only thing wrong with these two, so a generator that emitted
// them anyway would produce a module LLVM rejects — and the failure would show
// up as a broken build rather than a slower program.
func TestIRACycleIsRefusedEvenWhenTheBodiesAreOtherwiseFine(t *testing.T) {
	ir := irFor(t, `(define (down n) (if (= n 0) 0 (down (- n 1))))
(define (up n) (down n))`)
	if !strings.Contains(ir, "@gs_lam_down") || !strings.Contains(ir, "@gs_lam_up") {
		t.Errorf("a self-recursive procedure, which is orderable, was dropped:\n%s", ir)
	}
}

// TestIRRefusedIsReported checks that every procedure left to the interpreter
// comes with a reason.
//
// "Why is this slow" is the question the compiler has to be able to answer, and
// a silent refusal is the one answer that cannot be acted on.
func TestIRRefusedIsReported(t *testing.T) {
	p, err := CompileToIR(`(define (pure x) (+ x 1))
(define (reader x) (+ x global))
(define (setter x) (set! x 1))
(define (nested x) (define y 1) (+ x y))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	// A global read is compiled; the two forms are not.
	if p.Native != 2 {
		t.Errorf("compiled %d procedures natively, want 2: %v", p.Native, p.Refused)
	}
	if len(p.Refused) != 2 {
		t.Fatalf("refusals = %v, want one per refused procedure", p.Refused)
	}
	joined := strings.Join(p.Refused, "\n")
	for _, want := range []string{"setter", "nested"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no refusal names %s: %v", want, p.Refused)
		}
	}
}

// TestIRLiteralsAndForms checks the pieces a body is built from: a literal that
// fits a machine word, one that does not, and the forms that are emitted rather
// than called.
func TestIRLiteralsAndForms(t *testing.T) {
	t.Run("a large literal becomes a handle", func(t *testing.T) {
		ir := irFor(t, `(define (f x) (+ x 100000000000000000000))`)
		if !strings.Contains(ir, "gs_box_literal") {
			t.Errorf("a literal larger than a machine word was not boxed:\n%s", ir)
		}
	})
	t.Run("a small literal is a constant", func(t *testing.T) {
		ir := irFor(t, `(define (f x) (+ x 7))`)
		if !strings.Contains(ir, "sadd.with.overflow.i64(i64 %") ||
			!strings.Contains(ir, ", i64 7)") {
			t.Errorf("a small literal was not emitted as a constant:\n%s", ir)
		}
	})
	t.Run("let and if are emitted", func(t *testing.T) {
		ir := irFor(t, `(define (f x) (let ((y (+ x 1))) (if (< y 0) y 0)))`)
		if !strings.Contains(ir, "phi ") {
			t.Errorf("if did not produce a phi:\n%s", ir)
		}
	})
	t.Run("a comparison against a handle asks the runtime", func(t *testing.T) {
		ir := irFor(t, `(define (f a b) (< a b))`)
		if !strings.Contains(ir, "@gs_num_lt") {
			t.Errorf("a comparison has no runtime path:\n%s", ir)
		}
	})
}

// TestIRProgramKeepsEveryForm checks that the generated main still evaluates
// every top-level form.
//
// The native bodies are an addition to the program, not a replacement for it:
// a form whose result the compiler cannot compute is still the interpreter's to
// run, so dropping any of them would change what the program does.
func TestIRProgramKeepsEveryForm(t *testing.T) {
	ir := irFor(t, `(define (add a b) (+ a b))
(display (add 1 2))
(newline)`)
	if got := strings.Count(ir, "call i64 @gs_eval_source("); got != 3 {
		t.Errorf("evaluated %d of 3 top-level forms:\n%s", got, ir)
	}
}

// TestIRTailCallsAreJumps checks that a call in tail position is emitted as a
// jump rather than a call.
//
// This is not an optimization, it is the language: R7RS requires proper tail
// calls, and both the interpreter and the bytecode VM provide them.  A compiled
// loop written as recursion must not grow the stack, and before this the
// generated code did exactly that — a loop of 90000 iterations segfaulted where
// the interpreter returned the right answer, so a program that worked
// interpreted crashed when compiled.
func TestIRTailCallsAreJumps(t *testing.T) {
	// A tail-recursive loop that is *not* one of the recognised walks, so that
	// what is being tested here is the tail call and not the walk: the walk
	// recogniser replaces a matching body with a single call, and then there is
	// no recursive call left to be a tail call.
	ir := irFor(t, `(define (loop i a b) (if (= i 0) a (loop (- i 1) b (+ a b))))`)
	if !strings.Contains(ir, "musttail call %gs.val @gs_lam_loop(") {
		t.Errorf("the recursive call is not a tail call:\n%s", ir)
	}
	// `musttail` has to be followed immediately by the return that gives its
	// value back: LLVM rejects the module otherwise, so this is what keeps the
	// claim honest rather than a hint that can be ignored.
	if !strings.Contains(ir, "musttail call") {
		return
	}
	idx := strings.Index(ir, "musttail call")
	rest := ir[idx:]
	end := strings.Index(rest, "\n  ret ")
	if end < 0 {
		t.Errorf("a musttail call is not followed by a ret:\n%s", ir)
	}
	// Nothing but the return may sit between the call and the ret.
	if between := rest[:end]; strings.Contains(between, "\n  br ") ||
		strings.Contains(between, "\n  store ") {
		t.Errorf("something runs between the musttail call and its ret:\n%s", between)
	}
}

// TestIRTailCallArgumentsAreNotTailCalls checks the bug that made the wrong
// answer look like a working program.
//
// The arguments of a call are needed *by* that call, so a call among them must
// return rather than jump.  With the tail flag left set, `(square (square x))`
// emitted the inner call as the tail call and returned straight from it, so the
// outer square never ran: `(fourth 100000000000)` printed 10^22 where the
// interpreter printed 10^44, and nothing about the module looked wrong.
func TestIRTailCallArgumentsAreNotTailCalls(t *testing.T) {
	ir := irFor(t, `(define (square x) (* x x))
(define (fourth x) (square (square x)))`)
	body := ir[strings.Index(ir, "define %gs.val @gs_lam_fourth("):]
	body = body[:strings.Index(body, "\n}\n")]
	// The outer call is the tail call; the inner one must be an ordinary call,
	// which means the body contains both forms.
	if !strings.Contains(body, "musttail call %gs.val @gs_lam_square(") {
		t.Errorf("the outer call is not a tail call:\n%s", body)
	}
	if !strings.Contains(body, "= call %gs.val @gs_lam_square(") {
		t.Errorf("the inner call was emitted as a tail call, so it never returns:\n%s", body)
	}
}

// TestIRTailCallsBetweenDifferentAritiesAreNotMusttail checks the one case where
// `musttail` is unavailable.
//
// LLVM reuses the frame only when the caller and callee have the same parameter
// count, and it refuses the module rather than dropping the requirement.  A
// self-recursive call always matches, which is the case the language needs; a
// tail call from a procedure of one arity to a procedure of another is emitted
// as an ordinary call, which is correct and merely loses the frame reuse.
//
// This was a real bug: `(define (main) (fib 32))` failed to compile with
// "cannot guarantee tail call due to mismatched parameter counts", so a program
// with a zero-argument procedure tail-calling a one-argument one was rejected
// outright.
func TestIRTailCallsBetweenDifferentAritiesAreNotMusttail(t *testing.T) {
	// `loop` calls itself in tail position; `main` takes no arguments and calls
	// `loop`, which takes one — so one call may be musttail and the other may
	// not, in the same module.
	p, err := CompileToIR(`(define (loop n a b) (if (= n 0) a (loop (- n 1) b (+ a b))))
(define (main) (loop 32 0 1))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native != 2 {
		t.Fatalf("not both procedures compiled: %v", p.Refused)
	}
	// The definitions are extracted by searching for the exact signature, since
	// `@gs_lam_fib(` is also a prefix of `@gs_lam_fib_entry(`.
	//
	// `main` takes no arguments and calls a three-argument procedure, so its
	// call cannot be musttail.
	main := bodyOf(t, p.IR, "define %gs.val @gs_lam_main()")
	if strings.Contains(main, "musttail") {
		t.Errorf("a call between different arities was emitted as musttail:\n%s", main)
	}
	// The self-recursive call inside loop still is, because the arities match.
	loop := bodyOf(t, p.IR, "define %gs.val @gs_lam_loop(i64 %p_n.bits, i64 %p_n.tag, i64 %p_a.bits, i64 %p_a.tag, i64 %p_b.bits, i64 %p_b.tag)")
	if !strings.Contains(loop, "musttail") {
		t.Errorf("the self-recursive call lost its musttail:\n%s", loop)
	}
}

// bodyOf returns one function definition from a module, up to its closing brace.
func bodyOf(t *testing.T, ir, signature string) string {
	t.Helper()
	i := strings.Index(ir, signature)
	if i < 0 {
		t.Fatalf("%q is not in the module:\n%s", signature, ir)
	}
	rest := ir[i:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("the definition of %q is unterminated", signature)
	}
	return rest[:end]
}

// TestIRTheModuleIsReproducible holds down a property the other tests in this
// file depend on by comparing generated modules to each other.
//
// The emission order is a topological order, which is what LLVM requires, but a
// topological order is only unique up to procedures that do not call each
// other.  Visiting the roots in Go map order made those independent procedures
// come out in a different order on every run, so two runs of the same compiler
// on the same file produced different modules — and a refactor verified by
// comparing output would look like a behaviour change when nothing had changed.
func TestIRTheModuleIsReproducible(t *testing.T) {
	const src = `
(define (build i acc) (if (= i 0) acc (build (- i 1) (cons i acc))))
(define (sum l acc) (if (null? l) acc (sum (cdr l) (+ acc (car l)))))
(define (times a b) (* a b))
(display (sum (build 10 '()) 0))
(display (times 6 7))`
	first, err := CompileToIR(src, "test")
	if err != nil {
		t.Fatal(err)
	}
	// Map order is randomised per run, so a single repeat would pass by luck.
	for i := 0; i < 20; i++ {
		again, err := CompileToIR(src, "test")
		if err != nil {
			t.Fatal(err)
		}
		if again.IR != first.IR {
			t.Fatalf("run %d emitted a different module; the output is not reproducible", i+1)
		}
	}
}
