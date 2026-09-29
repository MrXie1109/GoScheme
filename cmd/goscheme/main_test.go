// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"goscheme/internal/scheme"
)

// lineFeeder hands out one line per Read call, which is how a terminal
// delivers input typed by a human: bufio's buffer is empty again after every
// line, so the REPL knows it really has to wait.
type lineFeeder struct {
	lines []string
	i     int
}

func (f *lineFeeder) Read(p []byte) (int, error) {
	if f.i >= len(f.lines) {
		return 0, io.EOF
	}
	n := copy(p, f.lines[f.i])
	f.i++
	return n, nil
}

// A terminal that reports queued input (a real paste) must not produce any
// prompt until the paste has been consumed.
func TestREPLPasteOnTerminalSuppressesPrompts(t *testing.T) {
	queued := 6
	pending := func() bool {
		if queued > 0 {
			queued--
			return true
		}
		return false
	}
	out, _ := runREPLWith(t, &lineFeeder{lines: strings.SplitAfter(multiLineForm, "\n")}, pending)

	if n := strings.Count(out, primaryPrompt) + strings.Count(out, continuationPrompt); n > 1 {
		t.Errorf("a queued paste produced %d prompts, output was:\n%s", n, out)
	}
	if !strings.Contains(out, "caught: #<error boom foo 123>") {
		t.Errorf("the form was not evaluated, output was:\n%s", out)
	}
}

const multiLineForm = "(guard (e\n" +
	"        (else\n" +
	"         (display \"caught: \")\n" +
	"         (display e)\n" +
	"         (newline)))\n" +
	"  (error \"boom\" 'foo 123))\n"

func runREPL(t *testing.T, src io.Reader, interactive bool) (string, string) {
	t.Helper()
	var pending func() bool
	if interactive {
		pending = func() bool { return false }
	}
	return runREPLWith(t, src, pending)
}

func runREPLWith(t *testing.T, src io.Reader, pending func() bool) (string, string) {
	t.Helper()
	m := scheme.NewMachine()
	var out, errOut bytes.Buffer
	m.SetStandardOutput(scheme.NewPortFromFile("stdout", &out, false, true))
	replOn(m, src, &out, &errOut, false, pending)
	return out.String(), errOut.String()
}

// Pasting a multi-line form must not wedge a prompt between every pasted line:
// while more input is already buffered the REPL stays quiet.
func TestREPLPasteDoesNotInterleavePrompts(t *testing.T) {
	out, errOut := runREPL(t, strings.NewReader(multiLineForm), true)

	if n := strings.Count(out, continuationPrompt); n != 0 {
		t.Errorf("pasted input produced %d continuation prompts, output was:\n%s", n, out)
	}
	if n := strings.Count(out, primaryPrompt); n > 2 {
		t.Errorf("pasted input produced %d prompts (expected at most 2), output was:\n%s", n, out)
	}
	if !strings.Contains(out, "caught: #<error boom foo 123>") {
		t.Errorf("the form was not evaluated, output was:\n%s", out)
	}
	if errOut != "" {
		t.Errorf("unexpected stderr output: %s", errOut)
	}
}

// When input arrives line by line the interpreter really is waiting, so it
// must show the continuation prompt.
func TestREPLTypedMultiLineUsesContinuationPrompt(t *testing.T) {
	out, _ := runREPL(t, &lineFeeder{lines: strings.SplitAfter(multiLineForm, "\n")}, true)

	const wantContinuations = 5 // six lines, the first one gets the primary prompt
	if n := strings.Count(out, continuationPrompt); n != wantContinuations {
		t.Errorf("typed input produced %d continuation prompts, want %d, output was:\n%s",
			n, wantContinuations, out)
	}
	if !strings.Contains(out, "caught: #<error boom foo 123>") {
		t.Errorf("the form was not evaluated, output was:\n%s", out)
	}
}

// A non-interactive session (a pipe) has no prompts and no banner at all.
func TestREPLNonInteractiveHasNoPrompts(t *testing.T) {
	out, _ := runREPL(t, strings.NewReader(multiLineForm+"(+ 1 2)\n"), false)
	if strings.Contains(out, primaryPrompt) || strings.Contains(out, continuationPrompt) {
		t.Errorf("prompts leaked into a non-interactive session:\n%s", out)
	}
	if !strings.Contains(out, "caught: #<error boom foo 123>") || !strings.Contains(out, "3\n") {
		t.Errorf("forms were not evaluated, output was:\n%s", out)
	}
}

// Errors must not desynchronise the buffer: the next form still works, and a
// half-finished form at end of input is reported.
func TestREPLRecoversFromErrors(t *testing.T) {
	out, errOut := runREPL(t, strings.NewReader("(car 5)\n(+ 1 2)\n"), false)
	if !strings.Contains(errOut, "expected a pair") {
		t.Errorf("the error was not reported: %q", errOut)
	}
	if !strings.Contains(out, "3\n") {
		t.Errorf("the REPL did not continue after the error:\n%s", out)
	}

	_, errOut = runREPL(t, strings.NewReader("(+ 1\n"), false)
	if !strings.Contains(errOut, "unexpected end of input") {
		t.Errorf("truncated input was not reported: %q", errOut)
	}
}

// ---------------------------------------------------------------- the editor

func editorFor(src string) (*lineEditor, *bytes.Buffer) {
	var out bytes.Buffer
	return newLineEditor(strings.NewReader(src), newLineTracker(&out)), &out
}

func TestLineEditorBracketedPaste(t *testing.T) {
	ed, out := editorFor("\x1b[200~(+ 1\n2 3)\x1b[201~\r")
	line, err := ed.ReadLine(primaryPrompt)
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "(+ 1\n2 3)" {
		t.Errorf("paste returned %q, want %q", line, "(+ 1\n2 3)")
	}
	if !strings.Contains(out.String(), "(+ 1\n2 3)") {
		t.Errorf("the pasted text was not echoed: %q", out.String())
	}
}

// The end marker may be split across reads; the editor must hold back the
// partial marker instead of treating it as pasted text.
func TestLineEditorPasteMarkerSplitAcrossReads(t *testing.T) {
	r := &lineFeeder{lines: []string{"\x1b[200~(+ 1 2", ")\x1b[20", "1~", "\r"}}
	var out bytes.Buffer
	ed := newLineEditor(r, newLineTracker(&out))
	line, err := ed.ReadLine(primaryPrompt)
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "(+ 1 2)" {
		t.Errorf("paste returned %q, want %q", line, "(+ 1 2)")
	}
}

func TestLineEditorKeys(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"insert and backspace", "abc\x7f\x7fde\r", "ade"},
		{"left arrow inserts", "ac\x1b[Db\r", "abc"},
		{"ctrl-a inserts at start", "bc\x01a\r", "abc"},
		{"ctrl-e appends", "ab\x01c\x05d\r", "cabd"},
		{"ctrl-u kills to start", "abc\x05\x15xy\r", "xy"},
		{"ctrl-k kills to end", "abc\x01\x0bxy\r", "xy"},
		{"home and end keys", "bc\x1b[Ha\x1b[Fd\r", "abcd"},
		{"delete key", "abc\x1b[D\x1b[3~\r", "ab"},
		{"utf-8 runes are one unit", "你好\x7f\r", "你"},
		{"ctrl-d on an empty line is eof", "\x04", ""},
	}
	for _, c := range cases {
		ed, _ := editorFor(c.in)
		line, err := ed.ReadLine(primaryPrompt)
		if c.name == "ctrl-d on an empty line is eof" {
			if err != io.EOF {
				t.Errorf("%s: err = %v, want io.EOF", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if line != c.want {
			t.Errorf("%s: got %q, want %q", c.name, line, c.want)
		}
	}
}

func TestLineEditorHistory(t *testing.T) {
	ed, _ := editorFor("(+ 1 2)\r(* 3 4)\r\x1b[A\r\x1b[A\x1b[A\r")
	for i, want := range []string{"(+ 1 2)", "(* 3 4)", "(* 3 4)", "(+ 1 2)"} {
		line, err := ed.ReadLine(primaryPrompt)
		if err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if line != want {
			t.Errorf("line %d = %q, want %q", i, line, want)
		}
	}
}

// A bracketed paste reaching the REPL is evaluated as one block: one prompt,
// every form run, no continuation prompt in between.
func TestREPLBracketedPasteBlock(t *testing.T) {
	m := scheme.NewMachine()
	var out, errOut bytes.Buffer
	tracker := newLineTracker(&out)
	m.SetStandardOutput(scheme.NewPortFromFile("stdout", tracker, false, true))
	ed := newLineEditor(strings.NewReader("\x1b[200~(+ 1 2)\n(* 3 4)\n\x1b[201~\r\x04"), tracker)
	replEdited(m, ed, tracker, &errOut, nil)

	got := out.String()
	if strings.Contains(got, continuationPrompt) {
		t.Errorf("a completed paste produced a continuation prompt:\n%s", got)
	}
	// The prompt also appears in redraws, so counting it is not meaningful;
	// what matters is that the pasted block reaches the terminal in one piece
	// with nothing wedged between its lines.
	if !strings.Contains(got, "(+ 1 2)\n(* 3 4)") {
		t.Errorf("the pasted block was broken up:\n%s", got)
	}
	if !strings.Contains(got, "3\n") || !strings.Contains(got, "12\n") {
		t.Errorf("not every form in the paste was evaluated:\n%s", got)
	}
	if errOut.Len() != 0 {
		t.Errorf("unexpected stderr: %s", errOut.String())
	}
}

// Output that does not end with a newline must not be erased by the next
// prompt.
func TestLineEditorKeepsUnterminatedOutput(t *testing.T) {
	var out bytes.Buffer
	tracker := newLineTracker(&out)
	io.WriteString(tracker, "你好")
	ed := newLineEditor(strings.NewReader("\x04"), tracker)
	if _, err := ed.ReadLine(primaryPrompt); err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if !strings.HasPrefix(out.String(), "你好\n") {
		t.Errorf("the prompt overwrote unterminated output: %q", out.String())
	}
}

// Terminals send CR for a pasted newline.  Echoing it verbatim in raw mode
// would return the cursor to column 0 without a line feed, so the pasted lines
// would overwrite each other; the editor normalises line endings instead.
func TestLineEditorPasteLineEndings(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"carriage return", "\x1b[200~(+ 1\r2 3)\x1b[201~\r", "(+ 1\n2 3)"},
		{"crlf", "\x1b[200~(+ 1\r\n2 3)\x1b[201~\r", "(+ 1\n2 3)"},
		{"line feed", "\x1b[200~(+ 1\n2 3)\x1b[201~\r", "(+ 1\n2 3)"},
		{"trailing carriage return", "\x1b[200~(+ 1 2)\r\x1b[201~\r", "(+ 1 2)\n"},
	}
	for _, c := range cases {
		ed, out := editorFor(c.input)
		line, err := ed.ReadLine(primaryPrompt)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if line != c.want {
			t.Errorf("%s: line = %q, want %q", c.name, line, c.want)
		}
		// The echo must use real newlines; a bare CR would make the pasted
		// lines overwrite each other.
		if !strings.Contains(out.String(), "\n") {
			t.Errorf("%s: the echo contains no newline: %q", c.name, out.String())
		}
		if strings.Contains(out.String(), "2 3)(+ 1") {
			t.Errorf("%s: the pasted lines overwrote each other: %q", c.name, out.String())
		}
	}
}

// A CRLF split across two reads must not produce two line feeds.
func TestLineEditorPasteCRLFSplitAcrossReads(t *testing.T) {
	r := &lineFeeder{lines: []string{"\x1b[200~(+ 1\r", "\n2 3)\x1b[201~", "\r"}}
	var out bytes.Buffer
	ed := newLineEditor(r, newLineTracker(&out))
	line, err := ed.ReadLine(primaryPrompt)
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "(+ 1\n2 3)" {
		t.Errorf("line = %q, want %q", line, "(+ 1\n2 3)")
	}
}

// The whole point: a pasted block is echoed on separate lines and evaluated as
// one unit.
func TestREPLPasteWithCarriageReturnsRendersLines(t *testing.T) {
	m := scheme.NewMachine()
	var out, errOut bytes.Buffer
	tracker := newLineTracker(&out)
	m.SetStandardOutput(scheme.NewPortFromFile("stdout", tracker, false, true))
	ed := newLineEditor(strings.NewReader("\x1b[200~(+ 1\r2 3)\x1b[201~\r\x04"), tracker)
	replEdited(m, ed, tracker, &errOut, nil)

	got := out.String()
	if strings.Contains(got, "2 3)(+ 1") {
		t.Errorf("the pasted lines overwrote each other:\n%q", got)
	}
	if !strings.Contains(got, "(+ 1\n2 3)") {
		t.Errorf("the pasted block was not echoed line by line:\n%s", got)
	}
	if !strings.Contains(got, "6\n") {
		t.Errorf("the pasted form was not evaluated:\n%s", got)
	}
}

// A checkout built with a plain `go build` must report the real version, not
// "dev": the VERSION file is embedded for exactly that reason.
func TestVersionIsEmbedded(t *testing.T) {
	if v := strings.TrimSpace(versionFile); v == "" {
		t.Fatal("the embedded VERSION file is empty")
	}
	re := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	if v := strings.TrimSpace(versionFile); !re.MatchString(v) {
		t.Errorf("VERSION = %q, want a x.y.z version", v)
	}
	// go test builds without our -ldflags, so this exercises the fallback.
	if got := versionString(); strings.Contains(got, "dev") || strings.Contains(got, "unknown") {
		t.Errorf("versionString() = %q; the embedded VERSION was not used", got)
	}
	if got := versionString(); !strings.Contains(got, strings.TrimSpace(versionFile)) {
		t.Errorf("versionString() = %q, want it to contain %q", got, strings.TrimSpace(versionFile))
	}
}

// A paste is *inserted*, not submitted: the interpreter waits for Enter, so a
// pasted program can be reviewed (and extended) before it runs.
func TestLineEditorPasteWaitsForEnter(t *testing.T) {
	ed, _ := editorFor("\x1b[200~(+ 1 2)\x1b[201~ 3\r")
	line, err := ed.ReadLine(primaryPrompt)
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "(+ 1 2) 3" {
		t.Errorf("line = %q, want %q: the paste should stay editable until Enter", line, "(+ 1 2) 3")
	}
}

// A form that never returns must not take the session down: Ctrl-C abandons it
// and the REPL carries on, with no Go stack dump anywhere.
func TestREPLEvaluationCanBeInterrupted(t *testing.T) {
	m := scheme.NewMachine()
	var out, errOut bytes.Buffer
	tracker := newLineTracker(&out)
	m.SetStandardOutput(scheme.NewPortFromFile("stdout", tracker, false, true))

	sigint := make(chan os.Signal, 1)
	src := "(chan-recv! (make-channel))\n(+ 1 2)\n"
	go func() {
		time.Sleep(100 * time.Millisecond)
		sigint <- os.Interrupt
	}()

	done := make(chan struct{})
	go func() {
		ed := newLineEditor(strings.NewReader(src), tracker)
		replEdited(m, ed, tracker, &errOut, sigint)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the REPL did not survive an evaluation that blocks forever")
	}

	got := out.String()
	if !strings.Contains(got, "^C") {
		t.Errorf("Ctrl-C did not report an interrupt:\n%s", got)
	}
	if !strings.Contains(got, "3\n") {
		t.Errorf("the REPL did not continue after the interrupt:\n%s", got)
	}
	for _, dump := range []string{"fatal error", "goroutine ", "deadlock"} {
		if strings.Contains(got, dump) || strings.Contains(errOut.String(), dump) {
			t.Errorf("a raw Go error leaked (%q):\nstdout: %s\nstderr: %s", dump, got, errOut.String())
		}
	}
}

// (command-line) must be (script-or-program arg ...) in both the interpreted
// and the bundled case, so that (cdr (command-line)) is always the arguments.
func TestCommandLineShape(t *testing.T) {
	cases := []struct {
		script string
		args   []string
		want   []string
	}{
		{"a.scm", []string{"1", "2", "3"}, []string{"a.scm", "1", "2", "3"}},
		{"a.scm", nil, []string{"a.scm"}},
		{"/tmp/dir/b.scm", []string{"x"}, []string{"/tmp/dir/b.scm", "x"}},
		{"", nil, nil}, // goscheme -e ... / REPL
		{"", []string{"ignored"}, []string{"ignored"}}, // no script, only args
		{"./a.out", []string{"1"}, []string{"./a.out", "1"}},
	}
	for _, c := range cases {
		got := commandLine(c.script, c.args)
		if len(got) != len(c.want) {
			t.Errorf("commandLine(%q, %v) = %v, want %v", c.script, c.args, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("commandLine(%q, %v) = %v, want %v", c.script, c.args, got, c.want)
				break
			}
		}
	}
}

// ------------------------------------------------------------------ bundles

func TestBundleTrailerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	interp := filepath.Join(dir, "interp")
	const fake = "FAKE-INTERPRETER-BYTES"
	if err := os.WriteFile(interp, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	script := []byte("(display (+ 1 2))\n")
	out := filepath.Join(dir, "prog")

	if err := writeBundle(interp, out, "prog.scm", script); err != nil {
		t.Fatalf("writeBundle: %v", err)
	}
	info, err := readBundle(out)
	if err != nil {
		t.Fatalf("readBundle: %v", err)
	}
	if string(info.Script) != string(script) {
		t.Errorf("script = %q, want %q", info.Script, script)
	}
	if info.Name != "prog.scm" {
		t.Errorf("name = %q, want %q", info.Name, "prog.scm")
	}

	// The interpreter is copied verbatim, so the bundle still runs.
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte(fake)) {
		t.Error("the interpreter was not copied verbatim")
	}
	if want := len(fake) + 16 + len(script) + len("prog.scm") + len(bundleMagic) + 8; len(data) != want {
		t.Errorf("bundle is %d bytes, want %d", len(data), want)
	}

	// A plain executable is not a bundle.
	if _, err := readBundle(interp); !errors.Is(err, errNotBundled) {
		t.Errorf("readBundle(interpreter) = %v, want errNotBundled", err)
	}
}

// A damaged trailer must be refused rather than trusted: the lengths in it
// drive how much is read.
func TestBundleRejectsCorruptTrailer(t *testing.T) {
	dir := t.TempDir()
	interp := filepath.Join(dir, "interp")
	if err := os.WriteFile(interp, []byte("INTERP"), 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good")
	if err := writeBundle(interp, good, "s.scm", []byte("(display 1)\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string][]byte{
		"truncated":       data[:len(data)-8],
		"bad magic":       append(append([]byte{}, data[:len(data)-len(bundleMagic)-8]...), []byte("XXXXXXXXX")...),
		"absurd length":   nil, // built below
		"length mismatch": nil,
	}
	absurd := append([]byte{}, data...)
	binary.BigEndian.PutUint64(absurd[len(absurd)-8:], uint64(1)<<40)
	cases["absurd length"] = absurd
	mismatch := append([]byte{}, data...)
	binary.BigEndian.PutUint64(mismatch[len(mismatch)-8-len(bundleMagic):], uint64(len(data))) // scriptLen way off
	cases["length mismatch"] = mismatch

	for name, b := range cases {
		path := filepath.Join(dir, "broken")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		if info, err := readBundle(path); err == nil {
			t.Errorf("%s: readBundle accepted a damaged trailer: %+v", name, info)
		}
	}
}

func TestBundleDefaultOutputName(t *testing.T) {
	// A C compiler writes a.out, or a.exe when targeting Windows.
	cases := []struct{ interp, want string }{
		{"/usr/bin/goscheme", "a.out"},
		{"/x/goscheme-linux-arm64", "a.out"},
		{"/x/goscheme-darwin-arm64", "a.out"},
		{"/x/goscheme-windows-amd64.exe", "a.exe"},
		{"/x/GOSCHEME.EXE", "a.exe"},
	}
	for _, c := range cases {
		if got := defaultOutput(c.interp); got != c.want {
			t.Errorf("defaultOutput(%q) = %q, want %q", c.interp, got, c.want)
		}
	}
}
