// SPDX-License-Identifier: MIT

package scheme

import (
	"strings"
	"testing"
)

// Every construct a pure body can be written with must either compile or be
// refused with a reason that says which kind of refusal it is.
//
// The compiler has two gates, and this test exists because they are easy to
// confuse.  The **syntax** gate asks whether the generator has a rule for a
// form; a `cond` failing it was a defect, because `cond` means an `if` chain
// and nothing about it is hard to emit.  The **cost** gate asks whether
// emitting is worth it; a body whose only work is one crossing into the runtime
// fails it, and that refusal is correct because such a body measured slower
// compiled than interpreted.
//
// A construct therefore has three acceptable outcomes and no others:
//
//	compiled                     the generator has a rule and the body is worth it
//	refused for cost             the body has nothing to gain from machine code
//	refused as having effects    the body does something only the interpreter can
//
// What is not acceptable is a refusal with no reason, or a pure computation
// refused because the generator lacks a rule for a *derived* form.  This test
// walks a list of the constructs and checks each one lands in one of the three.

// pureConstructs are bodies that compute a value from their parameters and
// nothing else.  Each must compile, or be refused for a reason that is about
// cost rather than about a missing rule.
//
// The body is written so that there is arithmetic of its own to do, so that the
// cost gate has something to weigh: a construct tested in a body whose only
// work is the construct would be refused for cost and would prove nothing about
// whether the generator can emit the construct.
var pureConstructs = []struct{ name, body string }{
	{"if", `(if (< n 0) (+ n 1) (- n 1))`},
	{"if with no alternative", `(if (< n 0) (+ n 1))`},
	{"begin", `(begin (+ n 1) (* n 2))`},
	{"let", `(let ((x (+ n 1))) (* x 2))`},
	{"let*", `(let* ((x (+ n 1)) (y (* x 2))) (+ x y))`},
	{"named let", `(let loop ((i n) (acc 0)) (if (= i 0) acc (loop (- i 1) (+ acc i))))`},
	{"and", `(and (< n 10) (> n -10) (* n 2))`},
	{"or", `(or (< n 0) (* n 2))`},
	{"when", `(when (< n 10) (* n 2))`},
	{"unless", `(unless (< n 0) (* n 2))`},
	{"cond with else", `(cond ((< n 0) (+ n 100)) (else (* n 2)))`},
	{"cond without else", `(cond ((< n 0) (+ n 100)))`},
	{"cond multiple expressions", `(cond ((< n 0) (+ n 1) (* n 2)) (else (- n 1)))`},
	{"cond arrow", `(cond ((assv n '((1 . 2))) => cdr) (else (* n 2)))`},
	{"cond single test", `(cond ((memv n '(1 2 3))) (else (* n 2)))`},
	{"case", `(case n ((1 2) (* n 10)) (else (+ n 1)))`},
	{"case with else only", `(case n (else (* n 2)))`},
	{"quote", `(car '((1 2) 3))`},
	{"quoted string", `(+ n (string-length "hello"))`},
	{"quoted list of symbols", `(+ n (length '(a b c)))`},
	{"quoted character", `(+ n (char->integer #\A))`},
	{"do", `(do ((i 0 (+ i 1)) (acc 0 (+ acc i))) ((= i n) acc))`},
	{"a call to another compiled procedure does not stop it", `(+ n n)`},
}

// effectfulConstructs are bodies that do something the interpreter runs.  They
// may be compiled — a crossing into the runtime is how — or refused, but a
// refusal has to say so rather than being silent.
var effectfulConstructs = []struct{ name, body string }{
	{"display", `(begin (display n) (+ n 1))`},
	{"vector-set!", `(begin (vector-set! v 0 n) (+ n 1))`},
	{"set! of a global", `(begin (set! g n) (+ n 1))`},
	{"call/cc", `(call/cc (lambda (k) (+ n 1)))`},
}

func TestEveryPureConstructCompilesOrSaysWhyNot(t *testing.T) {
	for _, tc := range pureConstructs {
		t.Run(tc.name, func(t *testing.T) {
			src := `(define (probe n) ` + tc.body + `)`
			p, err := CompileToIR(src, "test")
			if err != nil {
				t.Fatalf("compiling %s: %v", tc.body, err)
			}
			if p.Native > 0 {
				return
			}
			// Not compiled: the refusal has to be there, and it has to be about
			// the cost of crossing rather than about a form the generator does
			// not understand.  "is a form, not a call this can compile" means a
			// derived form reached the emitter unexpanded, which is the defect
			// this test is for.
			if len(p.Refused) == 0 {
				t.Fatalf("%s neither compiled nor said why", tc.body)
			}
			reason := strings.Join(p.Refused, " ")
			if strings.Contains(reason, "is a form, not a call this can compile") {
				t.Errorf("the generator has no rule for %s, which is derived syntax: %s", tc.body, reason)
			}
		})
	}
}

func TestEveryEffectfulConstructSaysWhyWhenRefused(t *testing.T) {
	for _, tc := range effectfulConstructs {
		t.Run(tc.name, func(t *testing.T) {
			src := `(define (probe n) ` + tc.body + `)`
			p, err := CompileToIR(src, "test")
			if err != nil {
				t.Fatalf("compiling %s: %v", tc.body, err)
			}
			if p.Native == 0 && len(p.Refused) == 0 {
				t.Fatalf("%s neither compiled nor said why", tc.body)
			}
		})
	}
}

// The derived forms are rewritten before anything else sees the body, so the
// scanner and the emitter never meet a `cond`.  If that ever stops being true —
// someone adds a rule to the emitter without adding the expansion — the
// coverage test above would still pass for a `cond` written at the top of a
// body while failing for one nested inside a `let`.  These two check the
// nesting, because the traversal is what makes the expansion general.
func TestDerivedSyntaxIsExpandedAtEveryDepth(t *testing.T) {
	for _, src := range []string{
		`(define (probe n) (let ((x n)) (+ n (cond ((< x 0) (+ x 1)) (else (* x 2))))))`,
		`(define (probe n) (if (< n 0) (+ n (cond ((= n -1) 1) (else 2))) (+ n (case n ((3) 3) (else 4)))))`,
		`(define (probe n) (+ n (cond ((< n 0) (when (< n -5) 1)) (else 2))))`,
	} {
		p, err := CompileToIR(src, "test")
		if err != nil {
			t.Fatalf("compiling %q: %v", src, err)
		}
		if p.Native == 0 {
			t.Errorf("nested derived syntax stopped the body compiling: %v", p.Refused)
		}
	}
}

// A quoted `cond` is data and must not be rewritten into an `if`.
//
// The check is on the emitted module rather than on whether the body compiled:
// whether a body with one `length` call in it is worth compiling is the cost
// gate's business, and the expansion is what this test is about.  So the datum
// is put where it will certainly be emitted — as a top-level call's argument —
// and the module is read back.
func TestQuotedDerivedSyntaxIsLeftAlone(t *testing.T) {
	prog, err := CompileToIR(`(define (probe n) (+ n 1))
(display (length '(cond (a b))))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	// The datum is in the module as the text the reader will read back.  If the
	// expansion had reached into the quote it would appear as `(if a b)`.
	if !strings.Contains(prog.IR, "cond") {
		t.Error("the quoted cond is not in the module at all")
	}
	if strings.Contains(prog.IR, `c"(if a b)"`) || strings.Contains(prog.IR, `c" (if a b)"`) {
		t.Error("the quoted cond was rewritten into an if")
	}
}
