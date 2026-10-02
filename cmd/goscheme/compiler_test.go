// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	w.Close()
	os.Stdout = saved
	return <-done
}

// runScriptFile runs a file the way the command line does and returns what the
// program printed.
func runScriptFile(t *testing.T, path string) string {
	t.Helper()
	m := scheme.NewMachine()
	out := scheme.NewOutputStringPort()
	m.SetStandardOutput(out)
	if code := loadFile(m, path); code != 0 {
		t.Fatalf("running %s failed with code %d", path, code)
	}
	return out.OutputString()
}

// TestCompileAndRunBytecode is the end-to-end promise of the bytecode work: a
// script is compiled by "goscheme compile", the .scmc file it writes runs
// without being parsed, and it does what the script did.
func TestCompileAndRunBytecode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.scm")
	program := `(import (scheme base) (scheme write))
(define (fact n) (if (= n 0) 1 (* n (fact (- n 1)))))
(display (fact 10)) (newline)
(define (counter) (let ((n 0)) (lambda () (set! n (+ n 1)) n)))
(define c (counter))
(c)
(display (c)) (newline)
(display (let loop ((i 0) (acc '())) (if (= i 3) (reverse acc) (loop (+ i 1) (cons i acc)))))
(newline)
`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog.scmc")
	report := captureStdout(t, func() {
		if code := runCompile([]string{src, "-o", out}); code != 0 {
			t.Errorf("compile returned %d", code)
		}
	})
	if report == "" {
		t.Errorf("compile printed no report")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("no bytecode file: %v", err)
	}
	fromSource := runScriptFile(t, src)
	fromBytecode := runScriptFile(t, out)
	if fromSource == "" {
		t.Fatalf("the script printed nothing")
	}
	if fromSource != fromBytecode {
		t.Errorf("bytecode and source differ:\n source: %q\nbytecode: %q", fromSource, fromBytecode)
	}
}

// TestBytecodeFileIsNotSource checks that the two kinds of file take two
// different paths: bytecode with a mangled header is refused rather than read
// as Scheme, and a source file is not accepted as bytecode.
func TestBytecodeFileIsNotSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p.scm")
	if err := os.WriteFile(src, []byte("(display 7)(newline)"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "p.scmc")
	captureStdout(t, func() {
		if code := runCompile([]string{src, "-o", out}); code != 0 {
			t.Fatalf("compile returned %d", code)
		}
	})
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4 || string(data[:4]) != "GSCM" {
		t.Fatalf("the file does not start with the bytecode magic")
	}
	if got := runScriptFile(t, out); got != "7\n" {
		t.Errorf("running the bytecode printed %q", got)
	}
	// A source file is not bytecode.
	source, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheme.ReadBytecode(bytes.NewReader(source)); err == nil {
		t.Errorf("a source file was accepted as bytecode")
	}
	// Neither is a file whose magic has been mangled: the loader refuses it
	// instead of falling back to reading it as Scheme.
	mangled := filepath.Join(dir, "bad.scmc")
	bad := append([]byte("XSCM"), data[4:]...)
	if err := os.WriteFile(mangled, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	m := scheme.NewMachine()
	if code := loadFile(m, mangled); code == 0 {
		t.Errorf("a bytecode file with a bad header was accepted")
	}
}
