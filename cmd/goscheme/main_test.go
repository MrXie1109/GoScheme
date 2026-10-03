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

	"github.com/MrXie1109/GoScheme/internal/scheme"
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

// Ctrl-C abandons a half-written expression instead of looking like an empty
// line: the continuation prompt went away and the next form still evaluates,
// where before the only way out of "..." was to close the input.
func TestREPLCtrlCAbandonsTheExpression(t *testing.T) {
	t.Setenv("GOSCHEME_HISTORY", "off")
	m := scheme.NewMachine()
	var out, errOut bytes.Buffer
	tracker := newLineTracker(&out)
	m.SetStandardOutput(scheme.NewPortFromFile("stdout", tracker, false, true))
	// "(do" then Enter leaves the reader waiting for more; Ctrl-C gives up on
	// it; then a complete form, then end of input.
	ed := newLineEditor(strings.NewReader("(do\n\x03(+ 1 2)\n\x04"), tracker)
	replEdited(m, ed, tracker, &errOut, nil)

	got := out.String()
	if !strings.Contains(got, "3") {
		t.Errorf("the form after Ctrl-C did not run:\n%s", got)
	}
	if strings.Contains(errOut.String(), "unexpected end of input") {
		t.Errorf("Ctrl-C left the unfinished expression behind: %q", errOut.String())
	}
	if n := strings.Count(got, primaryPrompt); n < 2 {
		t.Errorf("Ctrl-C did not return to the primary prompt (%d of them):\n%s", n, got)
	}
}

// ReadLine says which of the two things happened, rather than returning an
// empty line for both.
func TestReadLineReportsCtrlC(t *testing.T) {
	e := newLineEditor(strings.NewReader("abc\x03xy\r"), newLineTracker(io.Discard))
	if _, err := e.ReadLine("> "); !errors.Is(err, errInterrupted) {
		t.Errorf("first line: err = %v, want errInterrupted", err)
	}
	line, err := e.ReadLine("> ")
	if err != nil {
		t.Fatalf("second line: %v", err)
	}
	if line != "xy" {
		t.Errorf("second line = %q, want %q", line, "xy")
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

	if err := writeBundle(interp, out, "prog.scm", kindSource, script); err != nil {
		t.Fatalf("writeBundle: %v", err)
	}
	info, err := readBundle(out)
	if err != nil {
		t.Fatalf("readBundle: %v", err)
	}
	if string(info.Payload) != string(script) {
		t.Errorf("payload = %q, want %q", info.Payload, script)
	}
	if info.Kind != kindSource {
		t.Errorf("kind = %d, want source", info.Kind)
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
	if want := len(fake) + int(bundleHead) + len(script) + len("prog.scm") + len(bundleMagicCode) + 8; len(data) != want {
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
	if err := writeBundle(interp, good, "s.scm", kindSource, []byte("(display 1)\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string][]byte{
		"truncated":       data[:len(data)-8],
		"bad magic":       append(append([]byte{}, data[:len(data)-len(bundleMagicCode)-8]...), []byte("XXXXXXXXX")...),
		"absurd length":   nil, // built below
		"length mismatch": nil,
	}
	absurd := append([]byte{}, data...)
	binary.BigEndian.PutUint64(absurd[len(absurd)-8:], uint64(1)<<40)
	cases["absurd length"] = absurd
	mismatch := append([]byte{}, data...)
	binary.BigEndian.PutUint64(mismatch[len(mismatch)-8-len(bundleMagicCode):], uint64(len(data))) // payloadLen way off
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

// A bundle carries the compiled script when the build machine can compile it,
// and running it must not need the source — so the source file is deleted
// before the payload runs.  A form the compiler declines goes into the payload
// as source, which is how a mixed program still runs.
func TestBundleBytecodePayload(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "prog.scm")
	src := `(define (square x) (* x x))
(define (sum-to n)
  (let loop ((i 0) (acc 0))
    (if (= i n) acc (loop (+ i 1) (+ acc (square i))))))
(display (list (sum-to 5) (do ((i 0 (+ i 1)) (acc '() (cons i acc))) ((= i 3) acc))))
(newline)`
	if err := os.WriteFile(scriptPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, kind, note := payloadFor(scriptPath, []byte(src))
	if kind != kindBytecode {
		t.Fatalf("kind = %d (%s), want bytecode", kind, note)
	}

	interp := filepath.Join(dir, "interp")
	if err := os.WriteFile(interp, []byte("FAKE"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	if err := writeBundle(interp, out, "prog.scm", kind, payload); err != nil {
		t.Fatalf("writeBundle: %v", err)
	}
	info, err := readBundle(out)
	if err != nil {
		t.Fatalf("readBundle: %v", err)
	}
	if info.Kind != kindBytecode {
		t.Fatalf("the bundle carries kind %d, want bytecode", info.Kind)
	}

	// The source is gone: whatever runs now comes from the payload.
	if err := os.Remove(scriptPath); err != nil {
		t.Fatal(err)
	}
	got := runPayloadOnStringPort(t, info)

	want := runPayloadOnStringPort(t, &bundleInfo{Payload: []byte(src), Name: "prog.scm", Kind: kindSource})
	if got != want {
		t.Errorf("bytecode payload printed %q, source printed %q", got, want)
	}
	if !strings.Contains(want, "(30 (2 1 0))") {
		t.Fatalf("the program itself is wrong: %q", want)
	}
}

// A script that imports a library this machine cannot find cannot be compiled
// here, so the bundle carries the source instead of failing the build.
func TestBundlePayloadFallsBackToSource(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "prog.scm")
	src := "(import (no such lib))\n(display 1)\n"
	if err := os.WriteFile(scriptPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, kind, note := payloadFor(scriptPath, []byte(src))
	if kind != kindSource {
		t.Fatalf("kind = %d, want source", kind)
	}
	if string(payload) != src {
		t.Errorf("payload = %q, want the script", payload)
	}
	if !strings.Contains(note, "source") {
		t.Errorf("note = %q, want it to say the script was embedded", note)
	}
}

// runPayloadOnStringPort runs a bundle's payload with its output captured.
func runPayloadOnStringPort(t *testing.T, info *bundleInfo) string {
	t.Helper()
	m := scheme.NewMachine()
	out := scheme.NewOutputStringPort()
	m.SetStandardOutput(out)
	if code := runPayload(m, info); code != 0 {
		t.Fatalf("runPayload returned %d", code)
	}
	return out.OutputString()
}

// A paste is inserted at the cursor, not at the end of the line, and an
// escape sequence in a string literal is the character it names.
func TestPasteGoesToTheCursor(t *testing.T) {
	// Type "ac", put the cursor before "c", paste "b".
	input := "ac" + "\x1b[D" + pasteStart + "b" + pasteEnd + "\r"
	e := newLineEditor(strings.NewReader(input), newLineTracker(io.Discard))
	got, err := e.ReadLine("> ")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if got != "abc" {
		t.Errorf("pasted line is %q, want %q", got, "abc")
	}

	// A paste in the middle of a long line pushes the tail along rather than
	// replacing it, and a paste containing a newline keeps it.
	input = "xy" + pasteStart + "1\n2" + pasteEnd + "z" + "\r"
	e = newLineEditor(strings.NewReader(input), newLineTracker(io.Discard))
	got, err = e.ReadLine("> ")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if got != "xy1\n2z" {
		t.Errorf("pasted line is %q, want %q", got, "xy1\n2z")
	}
}

// A pasted multi-line expression has to be redrawn where it lands, and the
// redraw has to start from the row the cursor is really on.
//
// The bug this pins down: after pasting two rows, the cursor is on the second
// row but the editor thought it was on the first (it counted the newlines
// *after* e.pos, which is zero at the end of a line), so the redraw moved up
// nothing and rewrote the row it was on.  Every following keystroke stacked
// another copy of the prompt down the screen.
func TestPasteIsRedrawnWhereTheCursorIs(t *testing.T) {
	var out bytes.Buffer
	tracker := newLineTracker(&out)
	e := newLineEditor(strings.NewReader(
		pasteStart+"(+ 1\n 2)"+pasteEnd+"\r"), tracker)
	line, err := e.ReadLine("> ")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "(+ 1\n 2)" {
		t.Fatalf("line = %q", line)
	}
	got := out.String()

	// Both rows are drawn, and the line is erased before it is rewritten so
	// that a shorter line cannot leave the old one behind.
	if !strings.Contains(got, "> (+ 1\n 2)") {
		t.Errorf("the two-row line was not drawn as two rows: %q", got)
	}
	if !strings.Contains(got, "\x1b[J") {
		t.Errorf("the redraw did not erase what it was about to replace: %q", got)
	}
	// Having drawn it, the editor must know the cursor is on the second row, so
	// that the *next* redraw goes back up to the prompt row.  This is the value
	// the bug got wrong, and it is checked directly because the second redraw
	// is what showed it: with the cursor row wrong, every following keystroke
	// redrew on the row below and stacked a prompt.
	if e.lastVPos != 1 {
		t.Errorf("the editor thinks the cursor is on row %d after drawing a two-row line",
			e.lastVPos)
	}
}

// Deleting a row of a pasted line must not leave the row on the screen, and
// must not draw the line on the wrong row either.
func TestBackspaceAfterPasteStaysOnOneRow(t *testing.T) {
	var out bytes.Buffer
	tracker := newLineTracker(&out)
	e := newLineEditor(strings.NewReader(
		pasteStart+"(+ 1\n 2)"+pasteEnd+"\x7f\x7f\r"), tracker)
	line, err := e.ReadLine("> ")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "(+ 1\n " {
		t.Fatalf("line = %q, want %q", line, "(+ 1\n ")
	}
	// After the two backspaces the line is still two rows, so the redraws
	// between them must have gone up a row; the count of prompt writes is what
	// catches a redraw that lands on the wrong row and stacks a copy per
	// keystroke.
	if n := strings.Count(out.String(), "\x1b[J> "); n > 4 {
		t.Errorf("the line was redrawn on the wrong row (%d prompt writes): %q",
			n, out.String())
	}
}

// The cursor must land where the text ends, on whichever row that is.  The
// prompt is only in front of the *first* row of the line, so its width belongs
// in the column only when the cursor is on that row; adding it on later rows
// puts the cursor four columns past the end of the text, which is where the
// "off by four" report came from — ">>> " is four wide.
func TestCursorColumnOnEveryRow(t *testing.T) {
	// One row: the prompt counts.
	var out bytes.Buffer
	e := newLineEditor(strings.NewReader("(abc\r"), newLineTracker(&out))
	if _, err := e.ReadLine(">>> "); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if e.pos != 4 {
		t.Errorf("pos = %d, want 4", e.pos)
	}

	// Several rows: the prompt counts on the first row only.
	out.Reset()
	e = newLineEditor(strings.NewReader(
		pasteStart+"(a\nbc"+pasteEnd+"\r"), newLineTracker(&out))
	line, err := e.ReadLine(">>> ")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "(a\nbc" {
		t.Fatalf("line = %q", line)
	}
	// The last redraw writes the cursor's column; on the second row it is the
	// width of "bc", with no prompt in front of it.
	got := out.String()
	if !strings.Contains(got, "\x1b[2C") {
		t.Errorf("the cursor column on the second row is wrong: %q", got)
	}
	if strings.Contains(got, "\x1b[6C") {
		t.Errorf("the prompt width was added on a row that has no prompt: %q", got)
	}
}

func TestStringEscapes(t *testing.T) {
	cases := map[string]string{
		`"\033[31m"`: "\x1b[31m", // C-style octal
		`"\e[0m"`:    "\x1b[0m",  // ESC by name
		`"\x1b[1m"`:  "\x1b[1m",  // hex without the R7RS semicolon
		`"\x41;"`:    "A",        // and with it
		`"\0"`:       "\x00",     // still NUL on its own
		`"\101"`:     "A",        // three octal digits
		`"a\tb\n"`:   "a\tb\n",   // the escapes that were always there
	}
	for src, want := range cases {
		m := scheme.NewMachine()
		out := scheme.NewOutputStringPort()
		m.SetStandardOutput(out)
		if code := evalString(m, "(display "+src+")", "test"); code != 0 {
			t.Errorf("%s: evalString returned %d", src, code)
			continue
		}
		if out.OutputString() != want {
			t.Errorf("%s printed %q, want %q", src, out.OutputString(), want)
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

// ---------------------------------------------------------------- -static

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const staticGreet = "(define-library (lib greet) (export greet) (import (scheme base))\n" +
	"  (begin (define (greet who) (string-append \"hi \" who))))\n"

// -static must bake in every library the script imports, dependencies first,
// with includes inlined, so the executable needs nothing beside it.
func TestStaticResolve(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"lib/greet.sld": staticGreet,
		"lib/math.sld": "(define-library (lib math) (export square) (import (scheme base) (lib greet))\n" +
			"  (begin (define (square x) (* x x))))\n",
		"lib/body.scm": "(define (twice f x) (f (f x)))\n",
		"lib/inc.sld":  "(define-library (lib inc) (export twice) (import (scheme base)) (include \"body.scm\"))\n",
		"app.scm":      "(import (scheme base) (lib greet) (lib math) (lib inc))\n(display (greet \"x\"))\n",
	})
	script, err := os.ReadFile(filepath.Join(dir, "app.scm"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := resolveStatic(filepath.Join(dir, "app.scm"), script, []string{dir})
	if err != nil {
		t.Fatalf("resolveStatic: %v", err)
	}
	got := string(out)

	greet := strings.Index(got, "(define-library (lib greet)")
	math := strings.Index(got, "(define-library (lib math)")
	inc := strings.Index(got, "(define-library (lib inc)")
	if greet < 0 || math < 0 || inc < 0 {
		t.Fatalf("a library is missing from the prelude:\n%s", got)
	}
	if greet > math {
		t.Errorf("(lib greet) must come before the library that imports it:\n%s", got)
	}
	if strings.Contains(got, "include") {
		t.Errorf("include was not inlined:\n%s", got)
	}
	if !strings.Contains(got, "(define (twice f x) (f (f x)))") {
		t.Errorf("the included file's forms are missing:\n%s", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), `(display (greet "x"))`) {
		t.Errorf("the script must come last:\n%s", got)
	}
}

func TestStaticResolveSkipsBuiltins(t *testing.T) {
	dir := t.TempDir()
	src := "(import (scheme base) (scheme write))\n(display 1)\n"
	writeTree(t, dir, map[string]string{"app.scm": src})
	out, err := resolveStatic(filepath.Join(dir, "app.scm"), []byte(src), []string{dir})
	if err != nil {
		t.Fatalf("resolveStatic: %v", err)
	}
	if strings.Contains(string(out), "define-library") {
		t.Errorf("built-in libraries must not be baked in:\n%s", out)
	}
}

func TestStaticResolveErrors(t *testing.T) {
	dir := t.TempDir()
	// a missing library is a build error, not a runtime surprise
	writeTree(t, dir, map[string]string{"app.scm": "(import (no such lib))\n"})
	if _, err := resolveStatic(filepath.Join(dir, "app.scm"),
		[]byte("(import (no such lib))\n"), []string{dir}); err == nil {
		t.Error("a missing library was accepted")
	}

	// and so is a cycle
	dir2 := t.TempDir()
	writeTree(t, dir2, map[string]string{
		"lib/a.sld": "(define-library (lib a) (export a) (import (scheme base) (lib b)) (begin (define a 1)))\n",
		"lib/b.sld": "(define-library (lib b) (export b) (import (scheme base) (lib a)) (begin (define b 2)))\n",
		"app.scm":   "(import (lib a))\n",
	})
	if _, err := resolveStatic(filepath.Join(dir2, "app.scm"),
		[]byte("(import (lib a))\n"), []string{dir2}); err == nil {
		t.Error("a circular import was accepted")
	}
}

// ------------------------------------------------------------------ REPL extras

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Tab completes Scheme names, library names after (import, and the comma
// commands.
// Tab: complete a unique candidate silently, ring the bell when there is
// nothing to complete, and list several candidates only on the second Tab
// within a second.
func TestTabBehaviour(t *testing.T) {
	newEditor := func(input string) (*lineEditor, *bytes.Buffer) {
		var out bytes.Buffer
		e := newLineEditor(strings.NewReader(input), newLineTracker(&out))
		e.complete = func(line []rune, pos int) (int, []string) {
			switch string(line[:pos]) {
			case "(uniq":
				return 0, []string{"(unique-name"}
			case "(many", "(many-":
				// The second Tab asks again, with the word already extended to
				// what the candidates had in common.
				return 0, []string{"(many-a", "(many-b"}
			default:
				return 0, nil
			}
		}
		return e, &out
	}

	t.Run("one candidate completes", func(t *testing.T) {
		e, out := newEditor("(uniq\t\x03")
		if _, err := e.ReadLine("> "); err != nil && err != errInterrupted {
			t.Fatal(err)
		}
		if got := string(e.line); got != "(unique-name" {
			t.Errorf("line = %q, want %q", got, "(unique-name")
		}
		if strings.Contains(out.String(), "\a") {
			t.Errorf("a unique completion rang the bell: %q", out.String())
		}
	})

	t.Run("nothing to complete rings", func(t *testing.T) {
		e, out := newEditor("(none\t\x03")
		if _, err := e.ReadLine("> "); err != nil && err != errInterrupted {
			t.Fatal(err)
		}
		if n := strings.Count(out.String(), "\a"); n != 1 {
			t.Errorf("the bell rang %d times, want 1: %q", n, out.String())
		}
	})

	t.Run("several list only on the second tab", func(t *testing.T) {
		e, out := newEditor("(many\t\t\x03")
		if _, err := e.ReadLine("> "); err != nil && err != errInterrupted {
			t.Fatal(err)
		}
		got := out.String()
		// The first Tab found that every candidate begins "(many-", so it
		// extended the word instead of ringing: that is progress, and there is
		// nothing to complain about yet.
		if strings.Contains(got, "\a") {
			t.Errorf("the first Tab rang although it completed more of the word: %q", got)
		}
		if !strings.Contains(got, "(many-a") || !strings.Contains(got, "(many-b") {
			t.Errorf("the second Tab did not list the candidates: %q", got)
		}
		if !strings.Contains(got, "(many-") {
			t.Errorf("the word was not extended to the common prefix: %q", got)
		}
	})
}

func TestCompletionCandidates(t *testing.T) {
	m := scheme.NewMachine()
	if _, err := m.EvalString("(define my-thing 1)"); err != nil {
		t.Fatal(err)
	}
	c := &replCompleter{m: m}

	t.Run("scheme name", func(t *testing.T) {
		start, names := c.complete([]rune("hash-table-re"), len("hash-table-re"))
		if start != 0 {
			t.Errorf("start = %d, want 0", start)
		}
		if !contains(names, "hash-table-ref") || !contains(names, "hash-table-ref/default") {
			t.Errorf("candidates = %v", names)
		}
	})

	t.Run("name defined in the session", func(t *testing.T) {
		_, names := c.complete([]rune("my-"), len("my-"))
		if !contains(names, "my-thing") {
			t.Errorf("candidates = %v", names)
		}
	})

	t.Run("library name", func(t *testing.T) {
		line := []rune("(import (goscheme s")
		start, names := c.complete(line, len(line))
		if start != len("(import ") {
			t.Errorf("start = %d, want the library's opening parenthesis", start)
		}
		for _, want := range []string{"(goscheme socket)", "(goscheme sync)"} {
			if !contains(names, want) {
				t.Errorf("candidates = %v, want %s", names, want)
			}
		}
	})

	t.Run("comma command", func(t *testing.T) {
		_, names := c.complete([]rune(",li"), len(",li"))
		if !contains(names, ",libraries") {
			t.Errorf("candidates = %v", names)
		}
	})

	t.Run("nothing to offer", func(t *testing.T) {
		if _, names := c.complete([]rune("zzz-nothing-"), len("zzz-nothing-")); len(names) != 0 {
			t.Errorf("candidates = %v, want none", names)
		}
	})
}

// Tab in the line editor: one candidate is inserted, several insert what they
// share.
func TestTabCompletionInEditor(t *testing.T) {
	ed, _ := editorFor("he\t\r")
	ed.complete = func(line []rune, pos int) (int, []string) {
		return 0, []string{"hello"}
	}
	line, err := ed.ReadLine(primaryPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if line != "hello" {
		t.Errorf("got %q, want %q", line, "hello")
	}

	ed, _ = editorFor("he\t\r")
	ed.complete = func(line []rune, pos int) (int, []string) {
		return 0, []string{"hello", "help"}
	}
	line, err = ed.ReadLine(primaryPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if line != "hel" {
		t.Errorf("ambiguous completion gave %q, want the shared prefix %q", line, "hel")
	}

	// Without a completer, Tab is simply ignored.
	ed, _ = editorFor("he\t\r")
	line, err = ed.ReadLine(primaryPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if line != "he" {
		t.Errorf("got %q, want %q", line, "he")
	}
}

func TestHistoryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")

	defer func(old int) { historyLimit = old }(historyLimit)
	historyLimit = 3

	for _, line := range []string{"one", "two", "three", "four", "five"} {
		appendHistory(path, line)
	}
	// A pasted multi-line program is not a line, so it is not remembered.
	appendHistory(path, "(+ 1\n2)")

	got := loadHistory(path)
	want := []string{"three", "four", "five"}
	if len(got) != len(want) {
		t.Fatalf("loadHistory = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("loadHistory = %v, want %v", got, want)
		}
	}

	// Trimming leaves the most recent entries and nothing else.
	trimHistory(path)
	if got := loadHistory(path); len(got) != 3 || got[2] != "five" {
		t.Errorf("after trim: %v", got)
	}

	// A path of "" disables the file rather than failing.
	if got := loadHistory(""); got != nil {
		t.Errorf("loadHistory(\"\") = %v", got)
	}
	appendHistory("", "ignored")
}

func TestCommaCommands(t *testing.T) {
	t.Setenv("GOSCHEME_HISTORY", filepath.Join(t.TempDir(), "history"))

	m := scheme.NewMachine()
	var out, errOut bytes.Buffer
	tracker := newLineTracker(&out)
	m.SetStandardOutput(scheme.NewPortFromFile("stdout", tracker, false, true))
	ed := newLineEditor(strings.NewReader(
		",help\r,libraries\r,time (+ 1 2)\r,nope\r,quit\r(display 'after)\r"), tracker)

	replEdited(m, ed, tracker, &errOut, nil)

	got := out.String()
	for _, want := range []string{",bindings", "(goscheme match)", "ms\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
	// ,time evaluated the expression, so its value was printed.
	if !strings.Contains(got, "3") {
		t.Errorf(",time did not evaluate its expression:\n%s", got)
	}
	if !strings.Contains(errOut.String(), "unknown command ,nope") {
		t.Errorf("an unknown command was not reported: %q", errOut.String())
	}

	// ,quit ended the session, so the form after it was never read.  (The
	// command itself is echoed by the editor, which is why it is not asserted
	// on.)
	if strings.Contains(got, "after") {
		t.Errorf("input after ,quit was read:\n%s", got)
	}
}
