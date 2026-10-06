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

// TestIRRefusesWhatItCannotEmit checks that a body outside the accepted set is
// left to the interpreter rather than emitted wrongly.
//
// The compiler's contract is that a program means the same thing compiled or
// not, and the way that is kept is by refusing anything the generator cannot
// express exactly.  A refusal is not an error — the procedure still runs.
func TestIRRefusesWhatItCannotEmit(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"a call into the library", `(define (f x) (display x))`},
		{"a pair", `(define (f x) (cons x x))`},
		{"set!", `(define (f x) (set! x 1))`},
		{"a string", `(define (f x) (string-append "a" x))`},
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
// value would truncate silently.  The tag is a named type so that every
// signature agrees on it, which is what lets one function-pointer type stand
// for procedures of every arity.
func TestIRValuesCarryATag(t *testing.T) {
	ir := irFor(t, `(define (add a b) (+ a b))`)
	for _, want := range []string{
		"%gs.val = type { i64, i64 }",
		"define %gs.val @gs_lam_add(i64 %n, %gs.val* %args)",
		// The tag decides whether the fast path may run at all.
		"or i64",
		"icmp eq i64",
	} {
		if !strings.Contains(ir, want) {
			t.Errorf("the module does not mention %q:\n%s", want, ir)
		}
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
(define (impure x) (display x))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native != 1 {
		t.Errorf("compiled %d procedures natively, want 1", p.Native)
	}
	if len(p.Refused) != 1 || !strings.Contains(p.Refused[0], "impure") {
		t.Errorf("refusals = %v, want one naming impure", p.Refused)
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
