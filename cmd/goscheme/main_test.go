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
