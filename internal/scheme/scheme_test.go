// SPDX-License-Identifier: MIT

package scheme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runFile loads and evaluates a Scheme source file.
func runFile(m *Machine, path string) error {
	forms, err := ReadFileForms(m, path, false)
	if err != nil {
		return err
	}
	m.AddLoadPath(filepath.Dir(path))
	_, err = m.RunForms(forms, m.Global)
	return err
}

// runSuite runs a Scheme test file with the (chibi test) shim preloaded and
// returns the captured output together with the exit status.
func runSuite(t *testing.T, file string) (string, int, error) {
	t.Helper()
	out := NewOutputStringPort()
	m := NewMachine()
	m.CurOut = out
	m.OutParam.values[0] = out

	shim := filepath.Join("..", "..", "test", "scheme", "chibi", "test.scm")
	if err := runFile(m, shim); err != nil {
		t.Fatalf("loading test shim: %v", err)
	}
	err := runFile(m, file)
	code := 0
	if err != nil {
		ee, ok := err.(*ExitError)
		if !ok {
			return out.OutputString(), 1, err
		}
		code = ee.Code
	}
	return out.OutputString(), code, nil
}

func TestR7RSReferenceSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the reference suite in short mode")
	}
	path := filepath.Join("..", "..", "test", "scheme", "r7rs-tests.scm")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("reference suite not available: %v", err)
	}
	out, code, err := runSuite(t, path)
	if err != nil {
		t.Fatalf("suite aborted: %v\n%s", err, out)
	}
	if code != 0 {
		t.Fatalf("reference suite reported failures:\n%s", out)
	}
	if !strings.Contains(out, "0 failed") {
		t.Fatalf("unexpected summary:\n%s", out)
	}
	t.Log(strings.TrimSpace(out))
}

// extensionSuites are the suites for the extensions this interpreter adds to
// R7RS.  Each is run as its own subtest, so a failure names the suite.
var extensionSuites = []string{
	"goscheme-tests.scm",
	"goscheme-concurrency-tests.scm",
	"goscheme-network-tests.scm",
	"goscheme-process-tests.scm",
	"goscheme-data-tests.scm",
	"goscheme-match-tests.scm",
}

func TestExtensionSuites(t *testing.T) {
	for _, name := range extensionSuites {
		name := name
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "test", "scheme", name)
			if _, err := os.Stat(path); err != nil {
				t.Skipf("suite not available: %v", err)
			}
			out, code, err := runSuite(t, path)
			if err != nil {
				t.Fatalf("suite aborted: %v\n%s", err, out)
			}
			if code != 0 {
				t.Fatalf("suite reported failures:\n%s", out)
			}
			t.Log(strings.TrimSpace(out))
		})
	}
}

func TestReader(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"(1 2 3)", "(1 2 3)"},
		{"(1 . 2)", "(1 . 2)"},
		{"'a", "(quote a)"},
		{"'(1 . (2))", "(quote (1 2))"},
		{"#(1 2)", "#(1 2)"},
		{"#u8(1 2)", "#u8(1 2)"},
		{"#t", "#t"},
		{"#true", "#t"},
		{"#\\a", "#\\a"},
		{"#\\space", "#\\space"},
		{"\"a\\nb\"", "\"a\\nb\""},
		{"1/2", "1/2"},
		{"#e1.5", "3/2"},
		{"#x10", "16"},
		{"1e3", "1000.0"},
		{"+inf.0", "+inf.0"},
		{"...", "..."},
		{"-.5", "-0.5"},
		{"|a b|", "|a b|"},
		{"(a . #;b c)", "(a . c)"},
		{"#;(1 2) 3", "3"},
		{"#!/usr/bin/env goscheme\n(+ 1 2)", "(+ 1 2)"},
	}
	for _, c := range cases {
		r := NewStringReader(c.src)
		v, err := r.Read()
		if err != nil {
			t.Errorf("read %q: %v", c.src, err)
			continue
		}
		if got := WriteToString(v); got != c.want {
			t.Errorf("read %q = %s, want %s", c.src, got, c.want)
		}
	}
}

func TestNumbers(t *testing.T) {
	m := NewMachine()
	cases := []struct{ expr, want string }{
		{"(+ 1 2)", "3"},
		{"(+ 1/2 1/2)", "1"},
		{"(* 1000000000000 1000000000000)", "1000000000000000000000000"},
		{"(/ 1 3)", "1/3"},
		{"(expt 2 100)", "1267650600228229401496703205376"},
		{"(exact->inexact 1/2)", "0.5"},
		{"(number->string 255 16)", "\"ff\""},
		{"(sqrt 16)", "4"},
		{"(sqrt -4)", "0+2i"},
		{"(round 5/2)", "2"},
		{"(round 7/2)", "4"},
		{"(floor -1/2)", "-1"},
		{"(truncate -1/2)", "0"},
		{"(rationalize (exact .3) 1/10)", "1/3"},
		{"(max 1 2.0)", "2.0"},
		{"(expt 2.0 3)", "8.0"},
		{"(exact 2.5)", "5/2"},
		{"(magnitude 3+4i)", "5"},
	}
	for _, c := range cases {
		v, err := m.EvalString(c.expr)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got := WriteToString(v); got != c.want {
			t.Errorf("%s = %s, want %s", c.expr, got, c.want)
		}
	}
}

func TestTailCallDoesNotGrowStack(t *testing.T) {
	m := NewMachine()
	v, err := m.EvalString(`
		(let loop ((i 0))
		  (if (= i 2000000) i (loop (+ i 1))))`)
	if err != nil {
		t.Fatal(err)
	}
	if WriteToString(v) != "2000000" {
		t.Fatalf("got %s", WriteToString(v))
	}
}

func TestDeepNonTailRecursion(t *testing.T) {
	m := NewMachine()
	v, err := m.EvalString(`
		(define (sum n) (if (= n 0) 0 (+ n (sum (- n 1)))))
		(sum 10000)`)
	if err != nil {
		t.Fatal(err)
	}
	if WriteToString(v) != "50005000" {
		t.Fatalf("got %s", WriteToString(v))
	}
}

func TestUncaughtError(t *testing.T) {
	m := NewMachine()
	_, err := m.EvalString(`(car 5)`)
	if err == nil {
		t.Fatal("expected an error")
	}
}

// The examples double as documentation, so they have to keep running.  A
// failure here means examples/ has rotted, not that an assertion regressed.
func TestExamples(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.scm"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join("..", "..", "examples", "libraries", "main.scm"))
	if len(files) < 2 {
		t.Fatalf("no examples found: %v", files)
	}
	for _, file := range files {
		file := file
		name := filepath.Base(file) + "-" + filepath.Base(filepath.Dir(file))
		t.Run(name, func(t *testing.T) {
			out := NewOutputStringPort()
			m := NewMachine()
			m.CurOut = out
			m.OutParam.values[0] = out
			// (command-line) as the command line interpreter would set it, so
			// an example that reports its own arguments still works.
			m.Args = []string{file}
			if err := runFile(m, file); err != nil {
				t.Fatalf("example failed: %v\n%s", err, out.OutputString())
			}
			if strings.TrimSpace(out.OutputString()) == "" {
				t.Fatal("example printed nothing")
			}
		})
	}
}

// Path handling has to follow the host's separators.  A hand-rolled scan for
// '/' broke library and include loading on Windows only, where the cache and
// the library path are built with backslashes, so this test is worth having on
// that platform (it is skipped elsewhere because the expectation would differ).
func TestPathHelpersFollowTheHost(t *testing.T) {
	joined := filepath.Join("a", "b", "c.scm")
	if got, want := dirOf(joined), filepath.Join("a", "b"); got != want {
		t.Errorf("dirOf(%q) = %q, want %q", joined, got, want)
	}

	// An include written relative to a library must be found next to it, which
	// is the case the Windows suite caught.
	dir := t.TempDir()
	sub := filepath.Join(dir, "lib")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(sub, "body.scm")
	if err := os.WriteFile(inner, []byte("(define from-body 7)"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewMachine()
	m.AddLoadPath(sub)
	if got := m.resolvePath("body.scm"); got != inner {
		t.Errorf("resolvePath found %q, want %q", got, inner)
	}
	if got := m.resolvePath(filepath.Join("nope", "x.scm")); got != filepath.Join("nope", "x.scm") {
		t.Errorf("resolvePath on a missing file = %q", got)
	}
}
