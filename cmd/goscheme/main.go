// SPDX-License-Identifier: MIT

// Command goscheme is a Scheme (R7RS) interpreter.
//
// Usage:
//
//	goscheme [options] [file] [argument ...]
//
// Options:
//
//	-e expr    evaluate expr (repeatable)
//	-i         enter the REPL after loading the file
//	-q         do not print the banner in interactive mode
//	-interp    run in the tree-walker instead of the bytecode VM
//	-v         print the version
//
// With no file and no -e the interpreter starts an interactive REPL.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

func main() {
	os.Exit(runGuarded())
}

// runGuarded makes sure a Go panic never reaches the user as a stack dump.
func runGuarded() (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "goscheme: internal error: %v\n", r)
			code = 2
		}
	}()
	return run()
}

func run() int {
	// A standalone executable produced by "goscheme build" carries its script
	// in its own tail; if this binary has one, run it.
	if exe, err := os.Executable(); err == nil {
		if info, err := readBundle(exe); err == nil {
			return runBundled(exe, info)
		}
	}

	args := os.Args[1:]
	if len(args) > 0 && args[0] == "build" {
		return runBuild(args[1:])
	}
	if len(args) > 0 && args[0] == "compile" {
		return runCompile(args[1:])
	}
	var files []string
	var exprs []string
	interactive := false
	quiet := false
	interpret := false
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		switch a {
		case "-e", "--eval":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "goscheme: -e requires an argument")
				return 2
			}
			i++
			exprs = append(exprs, args[i])
		case "-i", "--interactive":
			interactive = true
		case "-q", "--quiet":
			quiet = true
		case "-interp", "--interpret":
			// Run everything in the tree-walker: the bytecode VM is the
			// default, and this is how the two are compared.
			interpret = true
		case "-v", "--version":
			fmt.Println(versionString())
			return 0
		case "-h", "--help":
			usage()
			return 0
		default:
			fmt.Fprintf(os.Stderr, "goscheme: unknown option %s\n", a)
			usage()
			return 2
		}
	}
	rest := args[i:]
	if len(rest) > 0 {
		files = append(files, rest[0])
		rest = rest[1:]
	}

	m := scheme.NewMachine()
	m.Interpret = interpret
	// (command-line) starts with the script and continues with the user's
	// arguments; the interpreter's own name is deliberately left out, so that
	// (cdr (command-line)) is the argument list whether the script is
	// interpreted or has been bound into an executable by "goscheme build".
	script := ""
	if len(files) > 0 {
		script = files[0]
	}
	m.Args = commandLine(script, rest)

	for _, e := range exprs {
		if code := evalString(m, e, "(command line)"); code != 0 {
			return code
		}
	}
	for _, f := range files {
		if code := loadFile(m, f); code != 0 {
			return code
		}
	}
	if interactive || (len(files) == 0 && len(exprs) == 0) {
		// A pipe or a file session that saw an error exits non-zero, the way
		// running a script does: otherwise `cat x.scm | goscheme` always looks
		// like a success to whatever called it.
		return repl(m, quiet)
	}
	return 0
}

// exitCodeFor turns the session's failures into an exit status.
func exitCodeFor(failed bool) int {
	if failed {
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: goscheme [-i] [-q] [-interp] [-e expr] [file] [args...]")
	fmt.Fprintln(os.Stderr, "       goscheme build <script> [-o <output>] [-i <interpreter>]")
	fmt.Fprintln(os.Stderr, "       goscheme compile <script> [-o <output.scmc>]")
}

// commandLine builds the (command-line) list: the script (or, for a bundled
// executable, the program as it was invoked) followed by the user's arguments.
func commandLine(script string, args []string) []string {
	out := make([]string, 0, len(args)+1)
	if script != "" {
		out = append(out, script)
	}
	return append(out, args...)
}

// runBundled runs the program bound into this executable.  There is no separate
// script name — the program *is* the script — so (command-line) reports the
// program as it was invoked, followed by the arguments, exactly as a compiled
// program would.
func runBundled(exe string, info *bundleInfo) int {
	m := scheme.NewMachine()
	prog := exe
	if len(os.Args) > 0 && os.Args[0] != "" {
		prog = os.Args[0]
	}
	m.Args = commandLine(prog, os.Args[1:])
	// Files shipped next to the executable are found by include and load.
	m.AddLoadPath(filepath.Dir(exe))

	return runPayload(m, info)
}

// runPayload runs what a bundle carries: a compiled program, or the source of
// a script the build machine could not compile.
func runPayload(m *scheme.Machine, info *bundleInfo) int {
	if info.Kind == kindBytecode {
		p, err := scheme.ReadBytecode(bytes.NewReader(info.Payload))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", info.Name, err)
			return 1
		}
		if _, err := m.RunProgram(p, m.Global); err != nil {
			return reportError(err)
		}
		return 0
	}
	r := scheme.NewStringReader(string(info.Payload))
	r.Source = info.Name
	forms, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", info.Name, err)
		return 1
	}
	if _, err := m.RunFormsCompiled(forms, m.Global); err != nil {
		return reportError(err)
	}
	return 0
}

func evalString(m *scheme.Machine, src, name string) int {
	r := scheme.NewStringReader(src)
	r.Source = name
	forms, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
		return 1
	}
	if _, err := m.RunFormsCompiled(forms, m.Global); err != nil {
		return reportError(err)
	}
	return 0
}

func loadFile(m *scheme.Machine, path string) int {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// A .scmc file is bytecode: it is loaded and run, never read as source.
	if strings.HasSuffix(path, ".scmc") {
		f, err := os.Open(abs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
			return 1
		}
		defer f.Close()
		prog, err := scheme.ReadBytecode(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
			return 1
		}
		m.AddLoadPath(filepath.Dir(abs))
		if _, err := m.RunProgram(prog, m.Global); err != nil {
			return reportError(err)
		}
		return 0
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
		return 1
	}
	r := scheme.NewStringReader(string(data))
	r.Source = abs
	forms, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
		return 1
	}
	m.AddLoadPath(filepath.Dir(abs))
	// Compiled where possible, exactly as a .scmc file holding the same
	// program would be: this is what "the VM is the default" has to mean for
	// the most ordinary thing anyone does, which is run a script.
	if _, err := m.RunFormsCompiled(forms, m.Global); err != nil {
		return reportError(err)
	}
	return 0
}

func reportError(err error) int {
	if ee, ok := err.(*scheme.ExitError); ok {
		return ee.Code
	}
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	return 1
}

// The two REPL prompts: one for a fresh expression, one while a form is still
// being read.
const (
	primaryPrompt      = ">>> "
	continuationPrompt = "... "
)

func repl(m *scheme.Machine, quiet bool) int {
	if !isTerminal(os.Stdin) {
		// Not talking to a user: no banner, no prompts.
		return replOn(m, os.Stdin, os.Stdout, os.Stderr, false, nil)
	}
	// Everything written to the terminal goes through out, so the REPL knows
	// where the cursor is even when Scheme code used (display ...).
	out := newLineTracker(os.Stdout)
	m.SetStandardOutput(scheme.NewPortFromFile("stdout", out, false, true))
	if !quiet {
		fmt.Fprintln(out, versionString())
		fmt.Fprintln(out, "Type (exit) or press Ctrl-D to leave.")
	}
	enterRaw, restore, err := makeRaw(os.Stdin)
	if err != nil {
		// No raw mode available: fall back to the canonical reader, which
		// still avoids prompting while input is already queued.
		return replOn(m, os.Stdin, os.Stdout, os.Stderr, false, func() bool {
			return inputPending(os.Stdin)
		})
	}
	fmt.Fprint(os.Stdout, bracketedPasteOn)
	defer func() {
		fmt.Fprint(os.Stdout, bracketedPasteOff)
		restore()
	}()

	// A pending timer keeps the Go runtime from reporting "all goroutines are
	// asleep - deadlock!" and killing the session: a form that blocks forever
	// then simply waits, and Ctrl-C abandons it.  Without this the runtime
	// tears the whole interpreter down and prints a Go stack dump.
	stopWatchdog := make(chan struct{})
	defer close(stopWatchdog)
	go func() {
		for {
			select {
			case <-stopWatchdog:
				return
			case <-time.After(time.Hour):
			}
		}
	}()

	// Hand the terminal back to a child process, and take it again afterwards.
	// An interactive child (a shell, an editor) needs the normal terminal, not
	// the REPL's raw mode, and interrupts must reach it.
	scheme.RunSubprocess = func(run func()) {
		restore()
		defer func() {
			enterRaw()
			_ = setInterrupts(os.Stdin, true)
		}()
		run()
	}
	defer func() { scheme.RunSubprocess = nil }()

	// Ctrl-C aborts the evaluation in progress instead of killing the REPL.
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt)
	defer signal.Stop(sigint)

	// An interactive session reports its errors on the terminal, and there is
	// usually a human deciding what to do about them, so its status is 0.
	replEdited(m, newLineEditor(os.Stdin, out), out, os.Stderr, sigint)
	return 0
}

// replEdited is the line editing read-eval-print loop.  A bracketed paste is
// inserted into the line being edited and submitted only when Enter is pressed,
// so a pasted program is evaluated as a unit with no prompts in between.
func replEdited(m *scheme.Machine, ed *lineEditor, stdout, stderr io.Writer, sigint <-chan os.Signal) {
	var buf strings.Builder
	prompt := primaryPrompt

	// The conveniences: Tab completion, and a history that outlives the
	// session.  Both are best effort — a REPL must still work when, say, the
	// home directory cannot be written.
	ed.complete = (&replCompleter{m: m}).complete
	histPath := historyPath()
	for _, entry := range loadHistory(histPath) {
		ed.history = append(ed.history, entry)
	}

	for {
		line, err := ed.ReadLine(prompt)
		if err != nil {
			if errors.Is(err, errInterrupted) {
				// Ctrl-C abandons what has been typed and goes back to the
				// primary prompt, as it does in a shell.  Treating it as an
				// empty line left a half-written expression in the buffer
				// that could only be got rid of by closing the input, which
				// then reported "unexpected end of input".
				buf.Reset()
				prompt = primaryPrompt
				continue
			}
			if buf.Len() > 0 {
				fmt.Fprintln(stderr, "Error: unexpected end of input")
			}
			fmt.Fprintln(stdout)
			return
		}
		// A comma command is handled here rather than by the reader, and only
		// when no continuation is pending.
		if buf.Len() == 0 {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, ",") {
				appendHistory(histPath, line)
				if runCommaCommand(m, trimmed, stdout, stderr, sigint) {
					return
				}
				continue
			}
		}
		appendHistory(histPath, line)

		buf.WriteString(line)
		buf.WriteString("\n")

		forms, perr := readForms(buf.String())
		switch {
		case perr == nil:
			// The pasted block may hold several forms; run them all before
			// asking for more input.
			buf.Reset()
			prompt = primaryPrompt
			if len(forms) == 0 {
				continue
			}
			for _, f := range forms {
				if !evalFormInteractive(m, f, stdout, stderr, sigint) {
					return
				}
			}
		case scheme.IsIncomplete(perr):
			prompt = continuationPrompt
		default:
			fmt.Fprintf(stderr, "Error: %v\n", perr)
			buf.Reset()
			prompt = primaryPrompt
		}
	}
}

// replOn drives the read-eval-print loop.  pending is non-nil for an
// interactive session and reports whether more input is already waiting.
//
// A prompt is written only when the interpreter genuinely has to wait for the
// user: neither its own buffer nor the terminal may hold further input.  A
// pasted multi-line form therefore shows the primary prompt once and the
// continuation prompt only when a human is really still typing, instead of a
// run of prompts wedged between the pasted lines.
func replOn(m *scheme.Machine, stdin io.Reader, stdout, stderr io.Writer, banner bool, pending func() bool) int {
	failed := false
	if banner {
		fmt.Fprintln(stdout, versionString())
		fmt.Fprintln(stdout, "Type (exit) or press Ctrl-D to leave.")
	}
	in := bufio.NewReaderSize(stdin, 1<<16)
	var buf strings.Builder
	incomplete := false
	for {
		moreQueued := in.Buffered() > 0
		if !moreQueued && pending != nil {
			moreQueued = pending()
		}
		if pending != nil && !moreQueued {
			if incomplete {
				fmt.Fprint(stdout, continuationPrompt)
			} else {
				fmt.Fprint(stdout, primaryPrompt)
			}
		}
		line, rerr := in.ReadString('\n')
		buf.WriteString(line)

		forms, perr := readForms(buf.String())
		switch {
		case perr == nil:
			buf.Reset()
			incomplete = false
			for _, f := range forms {
				keepGoing, bad := evalForm(m, f, stdout, stderr)
				if bad {
					failed = true
				}
				if !keepGoing {
					return exitCodeFor(failed)
				}
			}
		case scheme.IsIncomplete(perr):
			// Wait for the rest of the datum.
			incomplete = true
		default:
			fmt.Fprintf(stderr, "Error: %v\n", perr)
			buf.Reset()
			incomplete = false
			failed = true
		}

		if rerr != nil {
			// End of input.
			if incomplete {
				fmt.Fprintln(stderr, "Error: unexpected end of input")
				failed = true
			}
			if pending != nil {
				fmt.Fprintln(stdout)
			}
			return exitCodeFor(failed)
		}
	}
}

// evalFormInteractive evaluates one datum on a fresh interpreter thread.  Doing
// so means a form that never returns can be abandoned with Ctrl-C, and that a
// Go panic inside it is reported as one line instead of tearing down the
// session.  It returns false when the session should end.
func evalFormInteractive(m *scheme.Machine, form scheme.Value, stdout, stderr io.Writer, sigint <-chan os.Signal) bool {
	type outcome struct {
		value scheme.Value
		err   error
		panic interface{}
	}
	machine := m.Child()
	done := make(chan outcome, 1)

	// While an evaluation runs Ctrl-C must abort it rather than cancel a line.
	_ = setInterrupts(os.Stdin, true)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{panic: r}
			}
		}()
		v, err := machine.Run(form, machine.Global)
		done <- outcome{value: v, err: err}
	}()

	// The pending timer is what stops the Go runtime from declaring a
	// deadlock while the evaluation is blocked.
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	select {
	case r := <-done:
		_ = setInterrupts(os.Stdin, false)
		switch {
		case r.panic != nil:
			fmt.Fprintf(stderr, "internal error: %v\n", r.panic)
		case r.err != nil:
			if _, ok := r.err.(*scheme.ExitError); ok {
				return false
			}
			fmt.Fprintf(stderr, "Error: %v\n", r.err)
		default:
			if _, un := r.value.(scheme.Unspecified); !un {
				fmt.Fprintln(stdout, scheme.WriteToString(r.value))
			}
		}
		return true
	case <-sigint:
		_ = setInterrupts(os.Stdin, false)
		fmt.Fprint(stdout, "^C\n")
		return true
	case <-timer.C:
		return true
	}
}

// readForms parses every datum in src.
func readForms(src string) ([]scheme.Value, error) {
	r := scheme.NewStringReader(src)
	r.Source = "<stdin>"
	return r.ReadAll()
}

// evalForm evaluates one datum, reporting its value.  It returns false when
// the session should end.
func evalForm(m *scheme.Machine, f scheme.Value, stdout, stderr io.Writer) (keepGoing, failed bool) {
	v, err := m.Run(f, m.Global)
	if err != nil {
		if _, ok := err.(*scheme.ExitError); ok {
			return false, false
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return true, true
	}
	if _, un := v.(scheme.Unspecified); !un {
		fmt.Fprintln(stdout, scheme.WriteToString(v))
	}
	return true, false
}

// isTerminal reports whether f is a character device, i.e. whether the
// interpreter is talking to a user rather than to a pipe or a file.
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
