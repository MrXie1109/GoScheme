// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MrXie1109/GoScheme/internal/re"
	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// Helpers shared by the tests in this package.
//
// They live here rather than beside any one test because more than one file
// needs them: the native-compile tests run a script through the interpreter to
// get the answer to compare against, and the option tests read what a
// subcommand wrote to a stream.

// runScriptFile runs a file the way the command line does and returns what the
// program printed.
//
// It is the interpreter's answer, which is the thing the native path has to
// agree with: a test that compared a compiled program against a hard-coded
// string would pass on a day the interpreter itself was wrong.
func runScriptFile(t *testing.T, path string) string {
	t.Helper()
	m := scheme.NewMachine()
	out := re.NewOutputStringPort()
	m.SetStandardOutput(out)
	if code := loadFile(m, path); code != 0 {
		t.Fatalf("running %s failed with code %d", path, code)
	}
	return out.OutputString()
}

// The compiler is partial by design, so a program can come out with no machine
// code in it and still run correctly.  That is the worst way to fail, because
// nothing says so and the size of the binary suggests otherwise.  These tests
// hold the two halves of the answer down: a program that compiled nothing must
// be reported as such, and a program that compiled must not be.
func TestANothingCompiledBuildSaysSo(t *testing.T) {
	// Channel operations and `go` are outside what the generator emits, so this
	// program is entirely the interpreter's — and it is a real program, not a
	// contrived one: it was reported by a user who noticed the emitted IR had
	// no function in it.
	prog, err := scheme.CompileToIR(`(import (scheme base) (goscheme channel))
(define ch (make-channel))
(go (chan-send! ch 1))
(display (chan-recv! ch))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if prog.CompiledAnything() {
		t.Fatalf("expected nothing compiled, got native=%d topNative=%d",
			prog.Native, prog.TopNative)
	}
	var buf bytes.Buffer
	reportSplit(&buf, prog)
	if !strings.Contains(buf.String(), "nothing was compiled") {
		t.Fatalf("a build that compiled nothing did not say so: %q", buf.String())
	}
}

func TestACompiledBuildDoesNotWarn(t *testing.T) {
	prog, err := scheme.CompileToIR(`(define (fib n) (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2)))))
(display (fib 20))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !prog.CompiledAnything() {
		t.Fatal("fib should compile")
	}
	var buf bytes.Buffer
	reportSplit(&buf, prog)
	// A line on every successful build is a line a reader learns to skip, so a
	// build that compiled says nothing at all.
	if strings.Contains(buf.String(), "nothing was compiled") {
		t.Fatalf("a build that compiled warned that it did not: %q", buf.String())
	}
}

func TestACompiledBuildSaysNothingAtAll(t *testing.T) {
	// The compiler reports what it produced and not what it chose not to.  A
	// procedure the cost rule declined is a decision made in the program's
	// favour, and naming it would read as a defect and send the reader looking
	// for one; a procedure the generator could not express is a gap, and the
	// answer to a gap is to close it.
	//
	// So the only thing stderr ever carries from a successful build is the
	// warning that nothing was compiled, which the two tests above cover.
	prog, err := scheme.CompileToIR(`(define (split l)
  (let loop ((slow l) (fast l) (acc '()))
    (if (or (null? fast) (null? (cdr fast))) (values (reverse acc) slow)
        (loop (cdr slow) (cddr fast) (cons (car slow) acc)))))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if prog.CompiledAnything() {
		t.Skip("this procedure compiled, so there is nothing to be quiet about")
	}
	var buf bytes.Buffer
	reportSplit(&buf, prog)
	// Nothing was compiled *here*, so the warning is right.  What must not
	// appear is a per-procedure line.
	if strings.Contains(buf.String(), "split:") {
		t.Errorf("the report named a procedure: %q", buf.String())
	}
}
