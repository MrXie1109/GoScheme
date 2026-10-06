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
	reportSplit(&buf, prog, false)
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
	reportSplit(&buf, prog, false)
	// A line on every successful build is a line a reader learns to skip, so
	// the good case stays quiet until --explain asks for it.
	if strings.Contains(buf.String(), "nothing was compiled") {
		t.Fatalf("a build that compiled warned that it did not: %q", buf.String())
	}
}

func TestExplainNamesWhatWasLeftBehind(t *testing.T) {
	// A nested `define` is one of the constructs the generator has no rule for
	// yet, so it is refused with a reason — which is what --explain is for.
	// (A `set!` was the example here until it started compiling, and then a
	// `lambda`; the test needs a refusal, and what it is about is the reporting
	// rather than which construct is refused.)
	prog, err := scheme.CompileToIR(`(define (f x) (define y 1) (+ x y))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Refused) == 0 {
		t.Fatal("a body holding a nested define should be refused, with a reason")
	}
	var buf bytes.Buffer
	reportSplit(&buf, prog, true)
	if !strings.Contains(buf.String(), "f:") {
		t.Fatalf("--explain did not name the procedure it left behind: %q", buf.String())
	}
}
