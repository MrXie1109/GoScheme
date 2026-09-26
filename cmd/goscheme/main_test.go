package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

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
	ed, out := editorFor("\x1b[200~(+ 1\n2 3)\x1b[201~")
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
	r := &lineFeeder{lines: []string{"\x1b[200~(+ 1 2", ")\x1b[20", "1~"}}
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
	ed := newLineEditor(strings.NewReader("\x1b[200~(+ 1 2)\n(* 3 4)\n\x1b[201~\x04"), tracker)
	replEdited(m, ed, tracker, &errOut)

	got := out.String()
	if strings.Contains(got, continuationPrompt) {
		t.Errorf("a completed paste produced a continuation prompt:\n%s", got)
	}
	// One prompt before the paste and one after it, while waiting for the
	// Ctrl-D that ends the session; nothing in between.
	if n := strings.Count(got, primaryPrompt); n != 2 {
		t.Errorf("got %d primary prompts, want 2:\n%s", n, got)
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
