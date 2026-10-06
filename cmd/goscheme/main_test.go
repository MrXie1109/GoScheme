// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MrXie1109/GoScheme/internal/re"
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
	m.SetStandardOutput(re.NewPortFromFile("stdout", &out, false, true))
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

// incompleteForm reports whether text is an unfinished expression, which is
// what an editor in the REPL is given.
func incompleteForm(text string) bool {
	_, err := readForms(text)
	return re.IsIncomplete(err)
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
	// Each row is drawn behind its own prompt, so the text appears as
	// ">>> (+ 1" followed by "... 2 3)" rather than as one unbroken string.
	// Both rows have to be there: the pasted text was once kept in the buffer
	// and never drawn at all.
	got := out.String()
	if !strings.Contains(got, primaryPrompt+"(+ 1") {
		t.Errorf("the first pasted row was not drawn: %q", got)
	}
	if !strings.Contains(got, continuationPrompt+"2 3)") {
		t.Errorf("the second pasted row was not drawn: %q", got)
	}
	if !strings.Contains(got, "\x1b[J") {
		t.Errorf("the redraw did not erase what it replaced: %q", got)
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
	m.SetStandardOutput(re.NewPortFromFile("stdout", tracker, false, true))
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
	m.SetStandardOutput(re.NewPortFromFile("stdout", tracker, false, true))
	ed := newLineEditor(strings.NewReader("\x1b[200~(+ 1 2)\n(* 3 4)\n\x1b[201~\r\x04"), tracker)
	ed.continues = incompleteForm
	replEdited(m, ed, tracker, &errOut, nil)

	got := out.String()
	// The pasted block is one editing buffer holding two rows, so the rows are
	// drawn behind the primary prompt and then the continuation prompt — the
	// second row *is* a continuation row as far as the editor is concerned,
	// whichever form happens to sit on it.  What matters is that both rows
	// reach the terminal, in order, with nothing wedged between them and
	// nothing overwritten.
	if !strings.Contains(got, primaryPrompt+"(+ 1 2)") {
		t.Errorf("the first pasted row was not drawn:\n%s", got)
	}
	if !strings.Contains(got, continuationPrompt+"(* 3 4)") {
		t.Errorf("the second pasted row was not drawn:\n%s", got)
	}
	if strings.Contains(got, "(* 3 4)"+primaryPrompt) {
		t.Errorf("the rows were drawn out of order:\n%s", got)
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
	m.SetStandardOutput(re.NewPortFromFile("stdout", tracker, false, true))
	ed := newLineEditor(strings.NewReader("\x1b[200~(+ 1\r2 3)\x1b[201~\r\x04"), tracker)
	ed.continues = incompleteForm
	replEdited(m, ed, tracker, &errOut, nil)

	got := out.String()
	if strings.Contains(got, "2 3)(+ 1") {
		t.Errorf("the pasted lines overwrote each other:\n%q", got)
	}
	// The first row is drawn behind the primary prompt and the continuation
	// behind its own, which is what the two lines look like on screen.
	if !strings.Contains(got, primaryPrompt+"(+ 1") || !strings.Contains(got, continuationPrompt+"2 3)") {
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
	m.SetStandardOutput(re.NewPortFromFile("stdout", tracker, false, true))

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
	if want := len(fake) + int(bundleHead) + len(script) + len("prog.scm") + len(bundleMagicPacked) + 8; len(data) != want {
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
		"bad magic":       append(append([]byte{}, data[:len(data)-len(bundleMagicPacked)-8]...), []byte("XXXXXXXXX")...),
		"absurd length":   nil, // built below
		"length mismatch": nil,
	}
	absurd := append([]byte{}, data...)
	binary.BigEndian.PutUint64(absurd[len(absurd)-8:], uint64(1)<<40)
	cases["absurd length"] = absurd
	mismatch := append([]byte{}, data...)
	binary.BigEndian.PutUint64(mismatch[len(mismatch)-8-len(bundleMagicPacked):], uint64(len(data))) // payloadLen way off
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

// A packed program carries its source with the comments and layout removed,
// and running it must not need the source file — so the script is deleted
// before the payload runs.
func TestPackPayloadIsPackedSource(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "prog.scm")
	src := `;;; a comment that should not survive
(define (square x) (* x x))          ; nor this one
(define (sum-to n)
  (let loop ((i 0) (acc 0))
    (if (= i n) acc (loop (+ i 1) (+ acc (square i))))))
#| a block comment
   over two lines |#
(display (list (sum-to 5) (do ((i 0 (+ i 1)) (acc '() (cons i acc))) ((= i 3) acc))))
(newline)`
	if err := os.WriteFile(scriptPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, kind, note := payloadFor(scriptPath, []byte(src))
	if kind != kindSource {
		t.Fatalf("kind = %d (%s), want source", kind, note)
	}
	// The comments are gone and the program is not.
	for _, gone := range []string{"a comment that should not survive", "nor this one", "a block comment"} {
		if strings.Contains(string(payload), gone) {
			t.Errorf("the packed payload still contains %q", gone)
		}
	}
	for _, kept := range []string{"define", "square", "sum-to", "display"} {
		if !strings.Contains(string(payload), kept) {
			t.Errorf("the packed payload lost %q: %s", kept, payload)
		}
	}
	// And it is the same program: packing checks the round trip itself.
	if err := scheme.UnpackCheck(src, string(payload), "prog.scm"); err != nil {
		t.Errorf("the packed source does not read as the original: %v", err)
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

	// The source is gone: whatever runs now comes from the payload.
	if err := os.Remove(scriptPath); err != nil {
		t.Fatal(err)
	}
	got := runPayloadOnStringPort(t, info)
	if !strings.Contains(got, "(30 (2 1 0))") {
		t.Errorf("the packed program printed %q", got)
	}
}

// A script that does not read cannot be packed, so the bundle carries it as it
// was written: a script whose problem is a syntax error should report that when
// it runs, not fail the packing.
func TestPackKeepsUnreadableSource(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "prog.scm")
	src := "(display 1\n" // an unclosed paren
	if err := os.WriteFile(scriptPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, kind, note := payloadFor(scriptPath, []byte(src))
	if kind != kindSource {
		t.Fatalf("kind = %d, want source", kind)
	}
	if string(payload) != src {
		t.Errorf("payload = %q, want the script as written", payload)
	}
	if !strings.Contains(note, "packing failed") {
		t.Errorf("note = %q, want it to say packing failed", note)
	}
}

// runPayloadOnStringPort runs a bundle's payload with its output captured.
func runPayloadOnStringPort(t *testing.T, info *bundleInfo) string {
	t.Helper()
	m := scheme.NewMachine()
	out := re.NewOutputStringPort()
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

	// Both rows are drawn, each behind its own prompt, and the line is erased
	// before it is rewritten so that a shorter line cannot leave the old one
	// behind.
	if !strings.Contains(got, "> (+ 1\n"+continuationPrompt+" 2)") {
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
	// width of that row's prompt plus the width of "bc".  Every row has a
	// prompt in front of it, so the column is never just the text — but it is
	// that row's prompt, which is why the two rows do not come out the same.
	got := out.String()
	if !strings.Contains(got, fmt.Sprintf("\x1b[%dC", displayWidth([]rune(continuationPrompt))+2)) {
		t.Errorf("the cursor column on the second row is wrong: %q", got)
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
		out := re.NewOutputStringPort()
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
	m.SetStandardOutput(re.NewPortFromFile("stdout", tracker, false, true))
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

// A half-written expression keeps being edited on the next line, and a
// backspace at the start of that line climbs back over the break — so a
// multi-line form can be corrected instead of only abandoned.
func TestMultiLineEditing(t *testing.T) {
	var out bytes.Buffer
	e := newLineEditor(strings.NewReader("(list 1 2\r\x7f\x7f\x7f99)\r"), newLineTracker(&out))
	e.continues = incompleteForm
	line, err := e.ReadLine(primaryPrompt)
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	// (list 1 2, then Enter opens a continuation; three backspaces delete the
	// space and the "2" and the break itself, and 99) finishes the form.
	if line != "(list 199)" {
		t.Errorf("line = %q, want %q", line, "(list 199)")
	}
	got := out.String()
	// The first row keeps the prompt it was typed at, whatever the editor is
	// drawing later.
	if !strings.Contains(got, primaryPrompt+"(list 1 2") {
		t.Errorf("the first row lost its prompt: %q", got)
	}
	if !strings.Contains(got, continuationPrompt) {
		t.Errorf("the continuation row was not drawn behind its own prompt: %q", got)
	}
}

// A bracket opened on one row and closed on another is matched, because the
// whole expression is coloured at once.  Colouring row by row made the closing
// bracket look like an error.
func TestBracketsMatchAcrossRows(t *testing.T) {
	text := []rune("(list (car '(1 2))\n      3)")
	spans := highlight(text, len(text), classOf(nil, nil))
	var red, marked []string
	for _, sp := range spans {
		switch sp.style {
		case styleUnmatched:
			red = append(red, sp.text)
		case styleParen, styleMatch:
			marked = append(marked, sp.text)
		}
	}
	if len(red) != 0 {
		t.Errorf("a matched bracket was drawn as an error: %v", red)
	}
	if len(marked) != 2 {
		t.Errorf("marked %d brackets, want the pair: %v", len(marked), marked)
	}

	// And the rows of styled text still rebuild the expression exactly.
	var sb strings.Builder
	for _, sp := range spans {
		sb.WriteString(sp.text)
	}
	if sb.String() != string(text) {
		t.Errorf("the spans rebuilt %q", sb.String())
	}
}

// captureUsage returns what usage() prints.
func captureUsage(t *testing.T) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	usage()
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	return string(out)
}

// 每个子命令的选项都要出现在 usage 的**用法行**里，不只是出现在下面的说明
// 里：用法行是"怎么调用"的答案，而手工维护的文本最容易漏掉后加的选项。
func TestUsageLinesMentionEveryOption(t *testing.T) {
	got := captureUsage(t)
	// 用法行是 "usage:" 那一行和两个子命令行；说明段以 "The " 开头，所以
	// 认子命令行时要求它紧接着是 "<script>"，那正是用法行的写法。
	lines := []string{}
	for _, l := range strings.Split(got, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "usage: goscheme") ||
			strings.HasPrefix(t, "goscheme pack <script>") ||
			strings.HasPrefix(t, "goscheme compile <script>") {
			lines = append(lines, t)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("usage 的用法行有 %d 行，期望 3 行：\n%s", len(lines), got)
	}
	// 每个选项必须出现在它所属的那一行里。
	for _, tc := range []struct{ line, opt string }{
		{lines[0], "-e"}, {lines[0], "-interp"}, {lines[0], "-i"}, {lines[0], "-q"},
		{lines[1], "-o"}, {lines[1], "-i"}, {lines[1], "-static"},
		{lines[2], "-o"}, {lines[2], "--emit-llvm"}, {lines[2], "-O"},
	} {
		if !strings.Contains(tc.line, tc.opt) {
			t.Errorf("用法行 %q 没提到 %s", tc.line, tc.opt)
		}
	}
	// 下面要有对应的说明，而且不能还留着已经删掉的东西。
	for _, opt := range []string{"--emit-llvm", "-static", "-interp", "-e EXPR"} {
		if !strings.Contains(got, opt) {
			t.Errorf("usage 没有解释 %s", opt)
		}
	}
	for _, gone := range []string{"-obfuscate", ".scmc", "goscheme build"} {
		if strings.Contains(got, gone) {
			t.Errorf("usage 仍然提到已经删掉的 %s：\n%s", gone, got)
		}
	}
}

// goscheme compile 自己的 usage 也要提到它的选项。
// goscheme compile turns a script into a native program through LLVM, and the
// usage has to say so — including the option that stops before the tools run,
// which is the one a person asks for when they want to read the generated IR.
func TestCompileUsageMentionsItsOptions(t *testing.T) {
	got := captureStderr(t, func() { runCompile(nil) })
	for _, want := range []string{"--emit-llvm", "-o", "-O", "LLVM"} {
		if !strings.Contains(got, want) {
			t.Errorf("goscheme compile 的 usage 没提到 %s：\n%s", want, got)
		}
	}
	// The old format is gone, and the usage must not still offer it.
	for _, gone := range []string{"-obfuscate", ".scmc"} {
		if strings.Contains(got, gone) {
			t.Errorf("goscheme compile 的 usage 仍然提到 %s：\n%s", gone, got)
		}
	}
}

// captureStderr runs f and returns what it wrote to stderr.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	f()
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	return string(out)
}

// Ctrl+Right and Ctrl+Left move by a word.  The sequences are ESC [ 1 ; 5 C
// and ESC [ 1 ; 5 D; before this the modifier was ignored and they moved by one
// character, which is what a user does not expect from a modified arrow.
func TestCtrlArrowMovesByWord(t *testing.T) {
	// The sequences are decoded through the same path a terminal's bytes take,
	// so this is a test of classifyCSI as well as of the movement.
	for _, tc := range []struct {
		keys []byte
		want int // the cursor position after the keys
		line string
	}{
		// From the start of `(foo bar)`: one press lands after foo, another
		// after bar, another at the end.
		{[]byte("\x1b[1;5C"), 4, "(foo bar)"},
		{[]byte("\x1b[1;5C\x1b[1;5C"), 8, "(foo bar)"},
		{[]byte("\x1b[1;5C\x1b[1;5C\x1b[1;5C"), 9, "(foo bar)"},
		// And back from the end.  The editor starts with the cursor at 0, so
		// these begin by putting it at the end — Ctrl-E in the keys below is
		// not part of what is being tested, the position is.
		{[]byte("\x05\x1b[1;5D"), 5, "(foo bar)"},
		{[]byte("\x05\x1b[1;5D\x1b[1;5D"), 1, "(foo bar)"},
		{[]byte("\x05\x1b[1;5D\x1b[1;5D\x1b[1;5D"), 0, "(foo bar)"},
		// A plain arrow still moves by one character.
		{[]byte("\x1b[C"), 1, "(foo bar)"},
		{[]byte("\x1b[C\x1b[C"), 2, "(foo bar)"},
		// Ctrl+Home and Ctrl+End keep their ordinary meaning — the ends of the
		// line — rather than being read as word movement, so neither moves by
		// a word.
		{[]byte("\x1b[1;5F"), 9, "(foo bar)"},
		{[]byte("\x05\x1b[1;5H"), 0, "(foo bar)"},
		// Alt is not Ctrl: it must not be read as a word movement.
		{[]byte("\x1b[1;3C"), 1, "(foo bar)"},
	} {
		var out bytes.Buffer
		e := newLineEditor(bytes.NewReader(tc.keys), newLineTracker(&out))
		e.line = []rune(tc.line)
		for {
			k, err := e.readKey()
			if err != nil {
				break
			}
			switch k.kind {
			case keyWordRight:
				e.pos = wordEnd(e.line, e.pos)
			case keyWordLeft:
				e.pos = wordStart(e.line, e.pos)
			case keyRight:
				if e.pos < len(e.line) {
					e.pos++
				}
			case keyLeft:
				if e.pos > 0 {
					e.pos--
				}
			case keyCtrlE, keyEnd:
				e.pos = len(e.line)
			case keyHome:
				e.pos = 0
			default:
				t.Fatalf("%q gave an unexpected key %v", tc.keys, k.kind)
			}
		}
		if e.pos != tc.want {
			t.Errorf("%q from %q left the cursor at %d, want %d", tc.keys, tc.line, e.pos, tc.want)
		}
	}
}

// The word boundaries themselves, including the edges.
func TestWordBoundaries(t *testing.T) {
	// "(foo-bar baz!)": 0 is (, 1..7 is foo-bar, 8 is the space, 9..12 is
	// baz!, 13 is ), and 14 is the end of the line.  The values below are what
	// the rule gives — skip what is not a word, then the word — not what the
	// positions look like they should be.
	line := []rune("(foo-bar baz!)")
	for _, tc := range []struct{ from, right, left int }{
		{0, 8, 0},   // on the ( : right skips it and foo-bar; left is the start
		{1, 8, 0},   // on the f: left walks back over the ( , which is not a word
		{2, 8, 1},   // inside foo-bar: right to its end, left to its start
		{7, 8, 1},   // on its last letter
		{8, 13, 1},  // on the space: right past baz!, left to the start of foo-bar
		{9, 13, 1},  // on the b of baz!: left to foo-bar, over the space
		{12, 13, 9}, // on the ! at the end of baz!
		{13, 14, 9}, // on the ): right past it, left to baz!
		{14, 14, 9}, // at the end of the line
	} {
		if got := wordEnd(line, tc.from); got != tc.right {
			t.Errorf("wordEnd(%q, %d) = %d, want %d", string(line), tc.from, got, tc.right)
		}
		if got := wordStart(line, tc.from); got != tc.left {
			t.Errorf("wordStart(%q, %d) = %d, want %d", string(line), tc.from, got, tc.left)
		}
	}
	// An empty line, and a line of separators, must not move past the ends.
	if got := wordEnd(nil, 0); got != 0 {
		t.Errorf("wordEnd on an empty line = %d", got)
	}
	if got := wordStart([]rune("   "), 3); got != 0 {
		t.Errorf("wordStart on separators = %d, want 0", got)
	}
}

// Submitting a multi-line expression must not leave a blank line between it and
// its value.
//
// redrawSettled draws the expression again with no cursor highlight, and it
// then moved the cursor *down* by the number of newlines in the expression —
// on the reasoning that writing the rows had left the cursor above the last
// one.  It had not: writing a row ends with the cursor on it, so the extra
// moves went past the end, one blank line per newline in the form.  A
// single-line expression has no newline, moved nothing, and looked right, which
// is why only multi-line input showed it.
func TestSubmittingMultiLineLeavesNoBlankLine(t *testing.T) {
	var out bytes.Buffer
	e := newLineEditor(strings.NewReader("(list\r 1\r 2)\r\x04"), newLineTracker(&out))
	e.highlight = func(line []rune, pos int) []span {
		return highlight(line, pos, classOf(nil, nil))
	}
	e.colour = true
	e.continues = incompleteForm
	line, err := e.ReadLine(primaryPrompt)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadLine: %v", err)
	}
	if line != "(list\n 1\n 2)" {
		t.Fatalf("line = %q", line)
	}
	// The submitted output must end with the cursor at the end of the *last*
	// row of the expression, not on a row below it: the caller prints the value
	// on the next line, and a move past the end shows up as a blank line.
	got := out.String()
	tail := got[strings.LastIndex(got, "\x1b[J"):]
	if strings.Contains(tail, "\x1b[1B") || strings.Contains(tail, "\x1b[2B") {
		t.Errorf("the settled redraw moved the cursor down past the last row: %q", tail)
	}
	// It ends by putting the cursor in the column after the last row's prompt
	// and text: the last row is "...  2)", four columns of prompt and three of
	// text, so the cursor belongs in column seven.  What follows it in the
	// output is the newline the caller adds, which starts the value's line.
	if !strings.Contains(tail, "\x1b[7C") {
		t.Errorf("the cursor was not put after the last row's text: %q", tail)
	}
	if !strings.HasSuffix(tail, "\x1b[7C\n") {
		t.Errorf("the submitted line did not end at the last row: %q", tail)
	}
}

// A wide character occupies two columns, and a terminal moves two cells for it.
// Counting runes instead put the cursor one cell short for every wide character
// before it, so the text and the cursor drifted apart as the line was typed.
func TestWideCharactersMoveTheCursorTwoColumns(t *testing.T) {
	for _, tc := range []struct {
		keys string
		want int
		why  string
	}{
		{"中文", 8, "two wide characters after a four-column prompt"},
		{"中文abc", 11, "wide characters then narrow ones"},
		{"é", 5, "a narrow non-ASCII character takes one column"},
		{"", 4, "backspace over a wide character removes both columns"},
		{"中", 6, "Ctrl-A then Ctrl-E returns to the end"},
	} {
		var out bytes.Buffer
		e := newLineEditor(strings.NewReader(tc.keys), newLineTracker(&out))
		for {
			k, err := e.readKey()
			if err != nil {
				break
			}
			switch k.kind {
			case keyRune:
				e.line = append(e.line, k.r)
				e.pos = len(e.line)
			case keyBackspace:
				if e.pos > 0 {
					e.line = append(e.line[:e.pos-1], e.line[e.pos:]...)
					e.pos--
				}
			case keyCtrlA:
				e.pos = 0
			case keyCtrlE:
				e.pos = len(e.line)
			default:
				t.Fatalf("%q gave an unexpected key", tc.keys)
			}
		}
		// The column the editor would place the cursor in, which is what the
		// terminal is told to move to.
		_, col := rowAndColumn(e.line, e.pos)
		got := col + displayWidth([]rune(primaryPrompt))
		if got != tc.want {
			t.Errorf("%q (%s): cursor column %d, want %d", tc.keys, tc.why, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// compile: the native path, end to end
// ---------------------------------------------------------------------------

// TestCompileProducesARunningProgram checks the whole pipeline: source to LLVM
// IR, through opt and llc, linked against the runtime archive, and run.
//
// It is an end-to-end test because the failure it exists to catch is one that
// no amount of inspecting the IR can find.  A module can be well-formed, verify
// cleanly, contain a correct native body for every procedure — and still be
// dead code, because nothing in the program ever calls it.  That is exactly what
// happened, and the only way to see it is to run the result and watch what it
// does.
func TestCompileProducesARunningProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("compile shells out to opt, llc and cc")
	}
	requireToolchain(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "prog.scm")
	// Every one of these is computed by native code: the arithmetic, the
	// recursion, and the call from one compiled procedure to another.
	program := `(define (add a b) (+ a b))
(define (square x) (* x x))
(define (sumsq a b) (+ (square a) (square b)))
(define (fact n) (if (= n 0) 1 (* n (fact (- n 1)))))
(display (add 20 22))
(newline)
(display (sumsq 3 4))
(newline)
(display (fact 10))
(newline)`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if code := compileToNative(src, bin, "2", false, false, false, false); code != 0 {
		t.Fatalf("compiling failed with code %d", code)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("no program was produced: %v", err)
	}
	got := runNative(t, bin)
	want := "42\n25\n3628800\n"
	if got != want {
		t.Errorf("the compiled program printed %q, want %q", got, want)
	}
}

// TestCompiledProgramAgreesWithTheInterpreter checks that compiling a program
// does not change what it computes.
//
// The compiler's whole contract is that this is true; a native body is an
// optimization and never a second answer.  The cases here are the ones where an
// optimization is most likely to become one: arithmetic that leaves the range a
// machine word can hold, a comparison of two such values, and a value that
// passes through several compiled procedures.
func TestCompiledProgramAgreesWithTheInterpreter(t *testing.T) {
	if testing.Short() {
		t.Skip("compile shells out to opt, llc and cc")
	}
	requireToolchain(t)

	// Each case is (source, what the interpreter prints).  The expected output
	// is written out rather than computed here so that a test failure says
	// which of the two changed.
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "arithmetic inside a machine word",
			src: `(define (add a b) (+ a b))
(display (add 20 22))`,
			want: "42",
		},
		{
			name: "a sum that leaves a machine word",
			src: `(define (add a b) (+ a b))
(display (add 9223372036854775807 1))`,
			want: "9223372036854775808",
		},
		{
			name: "a product that leaves a machine word",
			src: `(define (square x) (* x x))
(display (square 4000000000))`,
			want: "16000000000000000000",
		},
		{
			name: "a value far outside a machine word",
			src: `(define (square x) (* x x))
(define (fourth x) (square (square x)))
(display (fourth 100000000000))`,
			want: "100000000000000000000000000000000000000000000",
		},
		{
			name: "a large value in a later computation",
			src: `(define (square x) (* x x))
(define (f) (+ (square 100000000000) 1))
(display (f))`,
			want: "10000000000000000000001",
		},
		{
			name: "comparing values past a machine word",
			src: `(define (square x) (* x x))
(define (lt a b) (< a b))
(display (lt (square 4000000000) (square 4000000001)))`,
			want: "#t",
		},
		{
			name: "a bignum compared as an argument",
			src: `(define (positive?2 x) (> x 0))
(display (positive?2 (* 4000000000 4000000000)))`,
			want: "#t",
		},
		// An `if` with no alternative has an unspecified value, and the compiled
		// code used to produce the fixnum 0 there.  These are the cases that
		// caught it: the value of such an `if` was printed through a procedure
		// the compiler emits, and it printed `0` where the interpreter printed
		// `#!unspecified`.
		//
		// Each body has arithmetic of its own so that it is actually compiled.
		// `(define (f n) (if (< n 0) 1))` on its own has nothing for machine code
		// to do and the cost rule declines it, which is how the bug hid: that
		// program ran in the interpreter and looked right.
		{
			name: "the value of an if with no alternative",
			src: `(define (f n) (if (> (* n n) 100) (* n 2)))
(display (list (f 5) (f 20)))`,
			want: "(#!unspecified 40)",
		},
		{
			name: "an if with no alternative inside a recursion",
			src: `(define (f n acc) (if (< n 0) (* acc 2) (f (- n 1) (+ acc n))))
(display (f 3 0))`,
			want: "12",
		},
		// A `cond` whose clauses all fail is unspecified too, and rewriting
		// `cond` into `if` chains put this through the same path.
		{
			name: "a cond whose clauses all fail",
			src: `(define (f n) (cond ((> (* n n) 100) (* n 2))))
(display (list (f 5) (f 20)))`,
			want: "(#!unspecified 40)",
		},
		// An assignment has to be visible where the value is read afterwards,
		// and the compiled form of that is not the obvious one: a local is
		// rebound as an SSA value, while a global goes through the runtime,
		// which owns the global environment.  The second case is the one that
		// caught a real bug — the write went to a different environment frame
		// than the read, so a compiled program answered 0 where the interpreter
		// answered 99.
		{
			name: "set! of a global is seen by the next read",
			src: `(define g 0)
(define (f n) (if (= n 0) g (begin (set! g 99) (f 0))))
(display (list (f 1) g))`,
			want: "(99 99)",
		},
		{
			name: "set! of a global inside a loop",
			src: `(define g 0)
(define (bump n acc) (if (= n 0) acc (begin (set! g (+ g 1)) (bump (- n 1) (+ acc g)))))
(display (list (bump 4 0) g))`,
			want: "(10 4)",
		},
		{
			name: "set! of a local",
			src: `(define (f n) (let ((x n)) (set! x (+ x 1)) (* x 2)))
(display (f 5))`,
			want: "12",
		},
		// A closure is a value the compiled code makes and the runtime holds, and
		// the two halves have to agree about what it is: the capture has to be
		// read from the right place, the arity has to match, and the procedure
		// has to be applyable by the interpreter as well — `map` and `fold`
		// receive compiled closures and call them without knowing it.
		{
			name: "a closure captures and is called",
			src: `(define (make-adder n) (lambda (x) (+ x n)))
(display (list ((make-adder 5) 10) ((make-adder 100) 1)))`,
			want: "(15 101)",
		},
		{
			name: "a compiled closure reaches the interpreter",
			src: `(define (make-adder n) (lambda (x) (+ x n)))
(display (map (make-adder 10) (list 1 2 3)))`,
			want: "(11 12 13)",
		},
		{
			name: "a closure is a procedure to procedure?",
			src: `(define (make-adder n) (lambda (x) (+ x n)))
(display (procedure? (make-adder 1)))`,
			want: "#t",
		},
		{
			name: "a closure made in a loop captures each iteration",
			src: `(define (f n acc)
  (if (= n 0) acc (f (- n 1) (cons (lambda (x) (+ x n)) acc))))
(display (map (lambda (g) (g 100)) (f 3 '())))`,
			want: "(101 102 103)",
		},
		{
			name: "a closure passed to another compiled procedure",
			src: `(define (twice f v) (f (f v)))
(define (make-adder n) (lambda (x) (+ x n)))
(display (twice (make-adder 3) 10))`,
			want: "16",
		},
		// A capture is a variable, and a closure that counts is the reason
		// closures exist.  The compiled version used to return 1 on every call:
		// the capture arrived as a parameter, so the assignment changed that
		// call's copy and the closure's own storage was never written.  Nothing
		// reported it, because the body compiled.
		{
			name: "a closure assigns what it captured",
			src: `(define (make-counter)
  (let ((n 0)) (lambda () (set! n (+ n 1)) n)))
(define c (make-counter))
(c) (c)
(display (c))`,
			want: "3",
		},
		{
			name: "each closure has its own captured storage",
			src: `(define (make-counter)
  (let ((n 0)) (lambda () (set! n (+ n 1)) n)))
(define a (make-counter))
(define b (make-counter))
(a) (a)
(display (list (a) (b) (a)))`,
			want: "(3 1 4)",
		},
		// A `letrec*` whose initialisers refer only to earlier names is a `let*`
		// and compiles; one that refers forward must not, because a `let*` binds
		// each name in its own frame and the later one does not exist yet.  The
		// second case is the one that crashed when the rewrite was too eager.
		{
			name: "a letrec* referring only backwards",
			src: `(define (f n) (letrec* ((a 1) (b (+ a n))) (* b 2)))
(display (f 5))`,
			want: "12",
		},
		// A `guard` compiles its body as a thunk and lets the interpreter run the
		// handler, so both paths have to agree: the one that returns and the one
		// that raises.  The first case is the one that was wrong when the thunk
		// was not called at all — its captured variables came back unbound.
		{
			name: "a guard whose body returns",
			src: `(define (f n) (guard (e (#t (+ n 99))) (+ (* n n) 1)))
(display (f 5))`,
			want: "26",
		},
		{
			name: "a guard that catches",
			src:  `(display (guard (e (#t 'caught)) (raise 'oops)))`,
			want: "caught",
		},
		{
			name: "a guard with a variable in its body",
			src: `(define (f n) (guard (e (#t (* n 100))) (+ (* n n) n)))
(display (list (f 5) (guard (e (#t (f 3))) (raise 'x))))`,
			want: "(30 12)",
		},
		// A promise is the interpreter's object but its body is not: `delay` is
		// emitted as a compiled thunk, so forcing one runs machine code.  The
		// memoisation is the part that says a promise is a promise.
		{
			name: "a delay is forced once",
			src: `(define count 0)
(define p (delay (begin (set! count (+ count 1)) (* 6 7))))
(display (list (force p) (force p) count))`,
			want: "(42 42 1)",
		},
		{
			name: "a delay-force chain",
			src: `(define (loop n) (if (= n 0) 42 (delay-force (loop (- n 1)))))
(display (force (loop 5)))`,
			want: "42",
		},
		// The unspecified value must not be confusable with the values a program
		// can legitimately produce, which is the whole reason it has a tag of its
		// own rather than being the fixnum 0 or the empty list.
		{
			name: "unspecified is not the fixnum zero",
			src: `(define (f n) (if (> (* n n) 100) (* n 2)))
(display (eqv? (f 5) 0))`,
			want: "#f",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.scm")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			// What the interpreter says, which is the answer to agree with.
			if got := runScriptFile(t, src); got != tc.want {
				t.Fatalf("the interpreter printed %q, want %q", got, tc.want)
			}
			bin := filepath.Join(dir, "prog")
			if code := compileToNative(src, bin, "2", false, false, false, false); code != 0 {
				t.Fatalf("compiling failed with code %d", code)
			}
			if got := runNative(t, bin); got != tc.want {
				t.Errorf("the compiled program printed %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCompileEmitLLVMStopsBeforeTheToolchain checks that --emit-llvm writes the
// IR and runs nothing else.
//
// It is the option a person uses to see what the compiler did, so it has to work
// on a machine where opt, llc and cc are absent — which is what makes it useful
// for reporting a code-generation problem.
func TestCompileEmitLLVMStopsBeforeTheToolchain(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.scm")
	if err := os.WriteFile(src, []byte("(define (add a b) (+ a b))\n(display (add 1 2))"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog.ll")
	if code := compileToNative(src, out, "2", true, false, false, false); code != 0 {
		t.Fatalf("--emit-llvm failed with code %d", code)
	}
	ir, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("no IR was written: %v", err)
	}
	text := string(ir)
	// The native body, the registration that makes it reachable, and the
	// fallback the runtime provides.
	for _, want := range []string{
		"%gs.val = type { i64, i64 }",
		"define %gs.val @gs_lam_add(",
		"@gs_register",
		"@gs_arith",
		"@llvm.sadd.with.overflow.i64",
		"call i64 @gs_eval_source(",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the IR does not mention %q:\n%s", want, text)
		}
	}
}

// requireToolchain skips a test when the LLVM tools are not installed.
//
// The native path is a feature of the program rather than a dependency of the
// interpreter, so a checkout without a toolchain is still a working checkout.
func requireToolchain(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"opt", "llc", "cc"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed, so the native path cannot run", tool)
		}
	}
}

// runNative runs a compiled program and returns its standard output.
func runNative(t *testing.T, bin string) string {
	t.Helper()
	cmd := exec.Command(bin)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("running %s: %v\nstderr: %s", bin, err, errBuf.String())
	}
	return out.String()
}

// TestBundleFromTheCompiledFormatIsReported checks that a bundle written by a
// version that had a compiled file format is refused with an explanation.
//
// The compiled format is gone, so those bytes cannot be read — but they are
// still inside a file that looks like a bundle, and falling through to "treat it
// as source" would report a syntax error somewhere in the middle of binary data.
// The user would have no way to tell that the real problem is the version.
func TestBundleFromTheCompiledFormatIsReported(t *testing.T) {
	dir := t.TempDir()
	interp, err := os.Executable()
	if err != nil {
		t.Skip("cannot find a binary to append to")
	}
	out := filepath.Join(dir, "old.bundle")
	if err := writeBundle(interp, out, "prog.scm", kindRetired, []byte("not really code")); err != nil {
		t.Fatal(err)
	}
	_, err = readBundle(out)
	if err == nil {
		t.Fatal("a bundle from the compiled format was accepted")
	}
	// The message has to name the version, because "pack it again" is the only
	// thing the user can do about it.
	if !strings.Contains(err.Error(), "older version") {
		t.Errorf("the error does not explain itself: %v", err)
	}
}

// TestNoSourceNamesTheOldSubcommand checks that nothing in the program still
// tells the user about `goscheme build`.
//
// The subcommand is `pack` now, and a message that names the old one is worse
// than a message with no name at all: the user types what it says and gets
// "unknown command".  The case that made this worth a test is a warning only
// macOS prints, which no test on any other platform would ever have run.
func TestNoSourceNamesTheOldSubcommand(t *testing.T) {
	// The usage test covers the usage text; this covers the messages a program
	// prints while it runs.
	for _, src := range []string{"main.go", "bundle.go", "static.go",
		"resign_darwin.go", "resign_other.go", "compiler.go"} {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("reading %s: %v", src, err)
		}
		if strings.Contains(string(data), "goscheme build") {
			t.Errorf("%s still names `goscheme build`, which is `pack` now", src)
		}
	}
}

// TestCompiledTailCallsDoNotGrowTheStack checks the language guarantee that a
// loop written as tail recursion runs in constant stack.
//
// R7RS requires proper tail calls, and both other engines provide them, so a
// compiled program that segfaults on a loop the interpreter runs is not a
// performance problem — it is a program that does not work.  That was the case
// until the generated code started emitting `musttail`: about 87000 iterations
// was the ceiling, and the failure was a crash with no message.
//
// The count is deliberately far past that ceiling, and the value it computes is
// checked because a stack that does not grow is only half of it.
func TestCompiledTailCallsDoNotGrowTheStack(t *testing.T) {
	if testing.Short() {
		t.Skip("compile shells out to opt, llc and cc")
	}
	requireToolchain(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "tail.scm")
	// 2,000,000 iterations: n(n+1)/2, which is past a machine word's worth of
	// iterations by two orders of magnitude and still an exact integer here.
	program := `(define (loop i acc) (if (= i 0) acc (loop (- i 1) (+ acc i))))
(display (loop 2000000 0))
(newline)`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "2000001000000\n"
	if got := runScriptFile(t, src); got != want {
		t.Fatalf("the interpreter printed %q, want %q", got, want)
	}
	bin := filepath.Join(dir, "tail")
	if code := compileToNative(src, bin, "2", false, false, false, false); code != 0 {
		t.Fatalf("compiling failed with code %d", code)
	}
	if got := runNative(t, bin); got != want {
		t.Errorf("the compiled program printed %q, want %q", got, want)
	}
}

// TestCompiledTailCallKeepsEveryArgumentEager checks that a tail call still
// evaluates every argument before jumping.
//
// A tail call reuses the frame, and doing that too eagerly is how an argument
// that is itself a call gets skipped: `(square (square x))` emitted the *inner*
// call as the tail call, returned from there, and never ran the outer one — so
// `(fourth 100000000000)` printed 10^22 where 10^44 was right.  The module was
// well-formed and the program ran, which is what made it worth a test.
func TestCompiledTailCallKeepsEveryArgumentEager(t *testing.T) {
	if testing.Short() {
		t.Skip("compile shells out to opt, llc and cc")
	}
	requireToolchain(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "nest.scm")
	program := `(define (square x) (* x x))
(define (fourth x) (square (square x)))
(display (fourth 100000000000))
(newline)`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "100000000000000000000000000000000000000000000\n"
	bin := filepath.Join(dir, "nest")
	if code := compileToNative(src, bin, "2", false, false, false, false); code != 0 {
		t.Fatalf("compiling failed with code %d", code)
	}
	if got := runNative(t, bin); got != want {
		t.Errorf("the compiled program printed %q, want %q", got, want)
	}
}

// TestCompiledListWalkAgreesWithTheInterpreter checks the recognised list walk.
//
// A loop like `(define (sum-list lst acc) (if (null? lst) acc (sum-list (cdr lst)
// (+ acc (car lst)))))` compiled naively crosses into the runtime four times per
// element — null?, car, cdr and + — and each crossing costs more than the
// element's work, so it ran slower than the interpreter.  It is now recognised
// and emitted as a single call that walks the list where the data already is.
//
// The cases below are the ones where a recognition mistake would be silent: the
// accumulator starting at something other than zero, elements that are not
// fixnums, and a list that is empty.  A walk that computed the wrong thing would
// still print a number.
func TestCompiledListWalkAgreesWithTheInterpreter(t *testing.T) {
	if testing.Short() {
		t.Skip("compile shells out to opt, llc and cc")
	}
	requireToolchain(t)

	for _, tc := range []struct {
		name        string
		src         string
		want        string
		interpreter string
	}{
		{
			name: "sum of a built list",
			src: `(define (build n acc) (if (= n 0) acc (build (- n 1) (cons n acc))))
(define (sum-list lst acc) (if (null? lst) acc (sum-list (cdr lst) (+ acc (car lst)))))
(display (sum-list (build 100 '()) 0))`,
			want: "5050",
		},
		{
			// The accumulator is not a fixnum: the walk must add through the
			// runtime's own arithmetic, not as machine integers.
			name: "sum starting from a bignum",
			src: `(define (sum-list lst acc) (if (null? lst) acc (sum-list (cdr lst) (+ acc (car lst)))))
(display (sum-list (list 1 2 3) 100000000000000000000))`,
			want: "100000000000000000006",
		},
		{
			// An element that does not fit a machine word.
			name: "sum of bignum elements",
			src: `(define (sum-list lst acc) (if (null? lst) acc (sum-list (cdr lst) (+ acc (car lst)))))
(display (sum-list (list 100000000000000000000 1) 0))`,
			want: "100000000000000000001",
		},
		{
			name: "empty list returns the accumulator",
			src: `(define (sum-list lst acc) (if (null? lst) acc (sum-list (cdr lst) (+ acc (car lst)))))
(display (sum-list '() 42))`,
			want: "42",
		},
		{
			name: "counting",
			src: `(define (count lst n) (if (null? lst) n (count (cdr lst) (+ 1 n))))
(display (count (list 'a 'b 'c) 0))`,
			want: "3",
		},
		{
			name: "collecting reverses",
			src: `(define (rev lst acc) (if (null? lst) acc (rev (cdr lst) (cons (car lst) acc))))
(display (rev (list 1 2 3) '()))`,
			want: "(3 2 1)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.scm")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := runScriptFile(t, src); got != tc.want {
				t.Fatalf("the interpreter printed %q, want %q", got, tc.want)
			}
			bin := filepath.Join(dir, "prog")
			if code := compileToNative(src, bin, "2", false, false, false, false); code != 0 {
				t.Fatalf("compiling failed with code %d", code)
			}
			if got := runNative(t, bin); got != tc.want {
				t.Errorf("the compiled program printed %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCompiledTopLevelCallRunsOnce checks that a top-level call to a compiled
// procedure is emitted as that call and does not *also* run interpreted.
//
// The first version added the native call and kept the interpreter's evaluation
// of the same form, on the reasoning that evaluation is what makes a form's
// effect happen.  That ran the loop twice, and it showed up in the benchmark as
// `globals` dropping from 0.99× to 0.60× — a pessimization, which is worse than
// no optimization because it is invisible in the program's output.
//
// The check here is on the count: the form must appear in the module once.
func TestCompiledTopLevelCallRunsOnce(t *testing.T) {
	src := `(define (loop i acc) (if (= i 0) acc (loop (- i 1) (+ acc i))))
(loop 100 0)`
	p, err := scheme.CompileToIR(src, "test")
	if err != nil {
		t.Fatal(err)
	}
	// The definition is evaluated by the interpreter because that is what binds
	// the name; the call is not evaluated at all.
	if got := strings.Count(p.IR, "@gs_eval_source("); got != 2 { // declare + one call
		t.Errorf("evaluated %d forms through the interpreter, want 1 (the definition):\n%s", got-1, p.IR)
	}
	if !strings.Contains(p.IR, "call %gs.val @gs_lam_loop(") {
		t.Errorf("the top-level call was not emitted natively:\n%s", p.IR)
	}
}

// TestCompiledTopLevelCallKeepsLaterForms checks that replacing one form's
// evaluation does not skip the forms after it.
//
// A top-level call is replaced only when it is a call to a compiled procedure
// with literal arguments; every other form still goes to the interpreter, and a
// form after the replaced one must still run.
func TestCompiledTopLevelCallKeepsLaterForms(t *testing.T) {
	if testing.Short() {
		t.Skip("compile shells out to opt, llc and cc")
	}
	requireToolchain(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "prog.scm")
	program := `(define (loop i acc) (if (= i 0) acc (loop (- i 1) (+ acc i))))
(define before 1)
(loop 100 0)
(define after 2)
(display (list before after))
(newline)`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "(1 2)\n"
	if got := runScriptFile(t, src); got != want {
		t.Fatalf("the interpreter printed %q, want %q", got, want)
	}
	bin := filepath.Join(dir, "prog")
	if code := compileToNative(src, bin, "2", false, false, false, false); code != 0 {
		t.Fatalf("compiling failed with code %d", code)
	}
	if got := runNative(t, bin); got != want {
		t.Errorf("the compiled program printed %q, want %q", got, want)
	}
}

// The REPL evaluates with the tree-walker and everything it does must be
// interruptible, which together are the two halves of one decision: a REPL
// evaluates one form with no idea what comes next, and a person who has just
// typed an accidental infinite loop needs to be able to stop it.
//
// Both are easy to lose.  Routing the REPL through the bytecode compiler would
// be faster and would break the first; wiring Ctrl-C to cancel the *line* rather
// than the evaluation would look like it works and break the second.  This
// checks the second, which is the one a test can check: the loop below never
// returns on its own, so if the machine were not watching the cancel channel the
// test would hang until the timeout rather than pass.
func TestTheREPLCanInterruptALoopThatNeverEnds(t *testing.T) {
	m := scheme.NewMachine()
	cancel := make(chan struct{})
	m.SetCancel(cancel)
	forms, err := re.NewStringReader(`(let loop () (loop))`).ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := m.Run(forms[0], m.Global)
		done <- err
	}()
	// Long enough that the loop is certainly running, short enough that the
	// test is not slow: the machine checks the channel on every step, so the
	// margin is enormous either way.
	time.Sleep(100 * time.Millisecond)
	close(cancel)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an endless loop returned normally")
		}
		if !errors.Is(err, scheme.ErrInterrupted) {
			t.Fatalf("interrupted with %v, want ErrInterrupted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled within 100ms and still running after 5s: the loop is not interruptible")
	}
}

// TestCompileLinksTheRuntimeBothWays checks the two link modes end to end, and
// the sizes are the reason it exists.
//
// The default links the runtime as a shared library, so a program is kilobytes
// and needs libgoscheme on the machine.  `-static` links it in, so a program is
// megabytes and needs nothing.  Both have to work, and the difference has to be
// real: a `-static` that quietly produced a shared program, or a default that
// quietly produced a static one, would look fine in every other test and cost
// either a ten-megabyte binary per program or a program that does not start on
// a machine without the library.
func TestCompileLinksTheRuntimeBothWays(t *testing.T) {
	if testing.Short() {
		t.Skip("compile shells out to opt, llc and cc")
	}
	requireToolchain(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "prog.scm")
	if err := os.WriteFile(src, []byte("(display (+ 20 22))\n(newline)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "42\n"

	shared := filepath.Join(dir, "shared")
	if code := compileToNative(src, shared, "2", false, false, false, false); code != 0 {
		t.Fatalf("the default (shared) link failed with code %d", code)
	}
	if got := runNative(t, shared); got != want {
		t.Errorf("the shared program printed %q, want %q", got, want)
	}

	static := filepath.Join(dir, "static")
	if code := compileToNative(src, static, "2", false, false, false, true); code != 0 {
		t.Fatalf("the static link failed with code %d", code)
	}
	if got := runNative(t, static); got != want {
		t.Errorf("the static program printed %q, want %q", got, want)
	}

	sharedInfo, err := os.Stat(shared)
	if err != nil {
		t.Fatal(err)
	}
	staticInfo, err := os.Stat(static)
	if err != nil {
		t.Fatal(err)
	}
	// A wide margin rather than a byte count, because the exact size depends on
	// the platform's linker and the machine code the compiler emitted.  What is
	// being checked is that one carries the runtime and the other does not.
	if sharedInfo.Size() >= staticInfo.Size() {
		t.Errorf("the shared program (%d bytes) is not smaller than the static one (%d bytes)",
			sharedInfo.Size(), staticInfo.Size())
	}
	if sharedInfo.Size() > 1<<20 {
		t.Errorf("the shared program is %d bytes, so it is carrying the runtime anyway",
			sharedInfo.Size())
	}
}

// The link-mode flag is -static, spelled the way cc spells it, and not
// --static.  It is a small thing that drifts: both spellings work if both are
// accepted, and then the usage line documents one while the parser quietly
// takes two.  This pins the one spelling, and that the other is refused rather
// than ignored — an option that is silently dropped is worse than one that is
// rejected, because the program still builds and is eight megabytes bigger.
func TestTheStaticFlagIsSpelledTheTraditionalWay(t *testing.T) {
	if code := runCompile([]string{"-static", "prog.scm"}); code == 2 {
		t.Error("-static was rejected")
	}
	// runCompile reports a bad option with exit code 2 and prints usage.  The
	// script does not exist, so a *recognised* flag gets past the parser and
	// fails later with a different code; that difference is what is checked.
	if code := runCompile([]string{"--static", "prog.scm"}); code != 2 {
		t.Errorf("--static was accepted (exit %d); the flag is -static", code)
	}
}

// A `letrec*` whose initialisers refer forward must not be rewritten into a
// `let*`, and this is the test that says so with a reason.
//
// The rewrite is tempting and was written: a reference inside a `lambda` body is
// deferred, so `(letrec* ((a (lambda () (b))) (b ...)) ...)` looks like a `let*`.
// It is not.  A lambda captures its environment when it is *created*, which is
// before the later binding exists, so the compiled program reported "b:
// undefined" where the interpreter found b defined — and in the shape without the
// lambda it produced a wrong number and then panicked inside the arithmetic.
//
// So the property is that the rewrite does not happen: the body must be left to
// the interpreter, which shares one environment frame and can bind later.  The
// test is on the refusal rather than on the output because the program is one
// R7RS calls an error to run at all.
func TestALetrecStarWithAForwardReferenceIsNotAReverseLet(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"a forward reference inside a lambda",
			`(define (f n) (letrec* ((a (lambda () (b))) (b (lambda () (+ n 1)))) (+ (* n n) (a))))`},
		{"a forward reference in an initialiser",
			`(define (g n) (letrec* ((x (+ y 1)) (y 2)) (* x n)))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := scheme.CompileToIR(tc.src, "test")
			if err != nil {
				t.Fatal(err)
			}
			if p.Native != 0 {
				t.Fatal("a letrec* that refers forward was rewritten into a let*, which is not what it means")
			}
		})
	}
	// The other half: one that refers only backwards *is* a let*, and compiles.
	p, err := scheme.CompileToIR(`(define (f n) (letrec* ((a 1) (b (+ a n))) (* b 2)))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native == 0 {
		t.Errorf("a letrec* that refers only backwards was not compiled: %v", p.Refused)
	}
}
