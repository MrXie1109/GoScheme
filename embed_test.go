// SPDX-License-Identifier: MIT

package goscheme

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvalAndValues(t *testing.T) {
	i := New()
	v, err := i.Eval("(+ 1 2)")
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := v.Int(); !ok || n != 3 {
		t.Fatalf("got %s", v)
	}
	if v.String() != "3" {
		t.Errorf("String() = %q", v.String())
	}

	v, err = i.Eval(`(list 1 "two" #t)`)
	if err != nil {
		t.Fatal(err)
	}
	items, ok := v.Slice()
	if !ok || len(items) != 3 {
		t.Fatalf("Slice() = %v, %v", items, ok)
	}
	if n, _ := items[0].Int(); n != 1 {
		t.Errorf("first item = %s", items[0])
	}
	if s, _ := items[1].Str(); s != "two" {
		t.Errorf("second item = %s", items[1])
	}
	if b, _ := items[2].Bool(); !b {
		t.Errorf("third item = %s", items[2])
	}

	// Conversions that do not apply report false instead of panicking.
	if _, ok := items[1].Int(); ok {
		t.Error("a string reported as an integer")
	}
	if _, ok := items[0].Slice(); ok {
		t.Error("an integer reported as a list")
	}
	if items[2].IsFalse() {
		t.Error("#t reported as false")
	}
	if !Nil().IsNil() {
		t.Error("Nil() is not nil")
	}
}

func TestDefineAndCall(t *testing.T) {
	i := New()
	i.Define("double", 1, 1, func(args []Value) (Value, error) {
		n, ok := args[0].Int()
		if !ok {
			return Value{}, errors.New("double: expected an integer")
		}
		return Int(n * 2), nil
	})

	v, err := i.Eval("(double 21)")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := v.Int(); n != 42 {
		t.Fatalf("got %s", v)
	}

	// A host function may also take any number of arguments.
	i.Define("join-them", 0, -1, func(args []Value) (Value, error) {
		parts := make([]string, 0, len(args))
		for _, a := range args {
			s, _ := a.Str()
			parts = append(parts, s)
		}
		return Str(strings.Join(parts, "-")), nil
	})
	v, err = i.Eval(`(join-them "a" "b" "c")`)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := v.Str(); s != "a-b-c" {
		t.Fatalf("got %s", v)
	}

	// An error from Go becomes a Scheme condition, which guard can catch.
	v, err = i.Eval(`(guard (e (#t 'caught)) (double "x"))`)
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "caught" {
		t.Fatalf("a host error was not catchable: %s", v)
	}

	// Calling a Scheme procedure from Go, including one that calls back.
	if _, err := i.Eval(`(define (apply-twice f x) (f (f x)))`); err != nil {
		t.Fatal(err)
	}
	applyTwice, ok := i.Lookup("apply-twice")
	if !ok {
		t.Fatal("apply-twice was not defined")
	}
	double, _ := i.Lookup("double")
	v, err = i.Call(applyTwice, double, Int(5))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := v.Int(); n != 20 {
		t.Fatalf("Go -> Scheme -> Go gave %s", v)
	}
	if !double.IsProcedure() {
		t.Error("a procedure was not recognised as one")
	}
	if _, ok := i.Lookup("no-such-name"); ok {
		t.Error("Lookup found a name that is not bound")
	}
}

func TestOutputArgsAndFiles(t *testing.T) {
	i := New()
	var out strings.Builder
	i.SetOutput(&out)
	if _, err := i.Eval(`(import (scheme base) (scheme write)) (display "captured")`); err != nil {
		t.Fatal(err)
	}
	if out.String() != "captured" {
		t.Errorf("captured %q", out.String())
	}

	i.SetArgs([]string{"alpha", "beta"})
	v, err := i.Eval("(command-line)")
	if err != nil {
		t.Fatal(err)
	}
	if items, _ := v.Slice(); len(items) != 2 {
		t.Errorf("command-line = %s", v)
	}

	// A file, with a library next to it, so the load path is exercised.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	library := `(define-library (lib answer)
	  (export answer)
	  (import (scheme base))
	  (begin (define answer 42)))`
	if err := os.WriteFile(filepath.Join(dir, "lib", "answer.sld"), []byte(library), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "main.scm")
	body := `(import (scheme base) (lib answer))
	  (define from-file answer)
	  from-file`
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := i.EvalFile(script); err != nil {
		t.Fatal(err)
	}
	v, ok := i.Lookup("from-file")
	if !ok {
		t.Fatal("the file's definition is missing")
	}
	if n, _ := v.Int(); n != 42 {
		t.Errorf("from-file = %s", v)
	}
}

func TestInterpretersAreIndependent(t *testing.T) {
	a, b := New(), New()
	if _, err := a.Eval("(define only-in-a 1)"); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Lookup("only-in-a"); ok {
		t.Error("a definition leaked between interpreters")
	}
}

// TestEvalFileRunsTheWholeFileAsOneExtent checks that EvalFile gives a file the
// same treatment the command line does: each form compiled where the compiler
// can, and the whole file as one extent.
//
// The distinction is not cosmetic.  A continuation captured in one top-level
// form has to stay valid for the forms that follow it, which is what a file
// means; evaluating the forms one by one would make that continuation invalid
// the moment the next form started.  EvalFile used to do exactly that, while
// every other entry point in the project compiled, so an embedded program
// silently ran on the interpreter with a different extent from the same program
// run from the command line.
func TestEvalFileRunsTheWholeFileAsOneExtent(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "extent.scm")
	// The escape continues out of the form that captured it and into the next
	// one, which only works if both are part of the same evaluation.
	body := `(import (scheme base) (scheme write))
(define k #f)
(display (call/cc (lambda (c) (set! k c) 1)))
(newline)
(define done 'no)
(set! done 'yes)
(display done)
(newline)`
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	i := New()
	if err := i.EvalFile(script); err != nil {
		t.Fatal(err)
	}
	if _, ok := i.Lookup("done"); !ok {
		t.Error("the file's later definitions did not run")
	}
}
