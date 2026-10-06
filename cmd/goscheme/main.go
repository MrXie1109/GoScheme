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
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/MrXie1109/GoScheme/internal/re"
	"github.com/MrXie1109/GoScheme/internal/scheme"
)

func main() {
	os.Exit(runGuarded())
}

// runGuarded makes sure a Go panic never reaches the user as a stack dump.
func runGuarded() (code int) {
	// A Go panic anywhere below here is a bug in the interpreter, not in the
	// program being run, and the program should be told so as a Scheme error
	// rather than shown a Go stack trace.  Every path goes through this — a
	// script, -e, the REPL — so it is the one place it has to
	// happen.
	defer func() {
		if r := recover(); r != nil {
			printError(os.Stderr, fmt.Errorf("internal error: %v", r))
			code = 2
		}
	}()
	// A goroutine that never finishes keeps the Go runtime from deciding that
	// *every* goroutine is asleep and calling it a deadlock.  That check is
	// what turns a program waiting on a channel into "fatal error: all
	// goroutines are asleep - deadlock!" and a stack dump about the
	// interpreter instead of a message about the program.  With one goroutine
	// parked for the life of the process, the runtime leaves the decision to
	// us: a program that blocks simply blocks, and Ctrl-C in the REPL abandons
	// it.  This is the whole of the deadlock handling — the runtime's check is
	// the detector, and refusing to trip it is the fix.
	go func() { select {} }()
	return run()
}

func run() int {
	// A standalone executable produced by "goscheme pack" carries its script
	// in its own tail; if this binary has one, run it.
	if exe, err := os.Executable(); err == nil {
		if info, err := readBundle(exe); err == nil {
			return runBundled(exe, info)
		}
	}

	args := os.Args[1:]
	if len(args) > 0 && args[0] == "pack" {
		return runPack(args[1:])
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
	// interpreted or has been bound into an executable by "goscheme pack".
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
	fmt.Fprintln(os.Stderr, "       goscheme pack <script> [-o <output>] [-i <interpreter>] [-static]")
	fmt.Fprintln(os.Stderr, "       goscheme compile <script> [-o <output>] [--emit-llvm] [-O0..-O3]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  -e EXPR        evaluate EXPR (may be repeated, evaluated in order)")
	fmt.Fprintln(os.Stderr, "  -i             enter the REPL after loading the file")
	fmt.Fprintln(os.Stderr, "  -q             do not print the REPL banner")
	fmt.Fprintln(os.Stderr, "  -interp        run in the tree-walker instead of the bytecode VM")
	fmt.Fprintln(os.Stderr, "  -v, --version  print the version and exit")
	fmt.Fprintln(os.Stderr, "  -h, --help     print this usage")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "The pack subcommand writes an executable with the source packed into it:")
	fmt.Fprintln(os.Stderr, "  -o FILE        where to write it (default: a.out)")
	fmt.Fprintln(os.Stderr, "  -i INTERPRETER the interpreter to copy (the running one by default)")
	fmt.Fprintln(os.Stderr, "  -static        bake in every library the script imports")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "The compile subcommand writes a native program, through LLVM:")
	fmt.Fprintln(os.Stderr, "  -o FILE        where to write it (default: a.out)")
	fmt.Fprintln(os.Stderr, "  --emit-llvm    write the LLVM IR instead of building anything")
	fmt.Fprintln(os.Stderr, "  -O0..-O3       optimisation level passed to opt (default: -O2)")
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

// runPayload runs what a bundle carries: the program's source, packed by
// `goscheme pack`.  There is one kind of payload now that there is no compiled
// file format — a packed program is its source, read and run the same way a
// script is.
func runPayload(m *scheme.Machine, info *bundleInfo) int {
	r := re.NewStringReader(string(info.Payload))
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
	r := re.NewStringReader(src)
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
	// A compiled file is loaded and run, never read as source.  The file is
	data, err := os.ReadFile(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
		return 1
	}
	r := re.NewStringReader(string(data))
	r.Source = abs
	forms, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
		return 1
	}
	m.AddLoadPath(filepath.Dir(abs))
	// Compiled where possible, form by form as the file is read: this is what
	// "the VM is the default" has to mean for the most ordinary thing anyone
	// does, which is run a script.  There is no intermediate file — a script is
	// read, compiled in memory and run.
	if _, err := m.RunFormsCompiled(forms, m.Global); err != nil {
		return reportError(err)
	}
	return 0
}

func reportError(err error) int {
	if ee, ok := err.(*scheme.ExitError); ok {
		return ee.Code
	}
	printError(os.Stderr, err)
	return 1
}

// printError writes a diagnostic.  The word "Error:" is red when the output is
// a terminal that can be expected to understand colour, so that a failure is
// visible in a wall of output; the message itself is left alone, because it is
// text a program may be reading.
func printError(w io.Writer, err error) {
	if colourWriter(w) {
		fmt.Fprintf(w, "\x1b[1;31mError:\x1b[0m %v\n", err)
		return
	}
	fmt.Fprintf(w, "Error: %v\n", err)
}

// colourWriter reports whether w is a terminal that wants colour: the same
// rules as the line editor's, so that a REPL that colours its input colours its
// errors too.
func colourWriter(w io.Writer) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if v := os.Getenv("TERM"); v == "" || v == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
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
	m.SetStandardOutput(re.NewPortFromFile("stdout", out, false, true))
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
	replEdited(m, newREPLLineEditor(m, out), out, os.Stderr, sigint)
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
				printError(stderr, errors.New("unexpected end of input"))
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
		case re.IsIncomplete(perr):
			prompt = continuationPrompt
		default:
			printError(stderr, perr)
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
		case re.IsIncomplete(perr):
			// Wait for the rest of the datum.
			incomplete = true
		default:
			printError(stderr, perr)
			buf.Reset()
			incomplete = false
			failed = true
		}

		if rerr != nil {
			// End of input.
			if incomplete {
				printError(stderr, errors.New("unexpected end of input"))
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
func evalFormInteractive(m *scheme.Machine, form re.Value, stdout, stderr io.Writer, sigint <-chan os.Signal) bool {
	type outcome struct {
		value re.Value
		err   error
		panic interface{}
	}
	machine := m.Child()
	done := make(chan outcome, 1)

	// Ctrl-C during a form abandons it.  The evaluation runs in another
	// goroutine, so printing "^C" is not enough: the machine is given a channel
	// to watch, and closing it stops the evaluation at its next step and wakes
	// it out of a blocked send or receive.  Without that the abandoned form
	// kept running — a (chan-send! ch v) that was "cancelled" stayed parked on
	// the channel and delivered v to whoever received next.
	cancel := make(chan struct{})
	machine.SetCancel(cancel)

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
			// An interrupted form is not a failure to report: the user asked
			// for it to stop.
			if errors.Is(r.err, scheme.ErrInterrupted) {
				fmt.Fprint(stdout, "^C\n")
				return true
			}
			printError(stderr, r.err)
		default:
			if _, un := r.value.(re.Unspecified); !un {
				fmt.Fprintln(stdout, re.WriteToString(r.value))
			}
		}
		return true
	case <-sigint:
		_ = setInterrupts(os.Stdin, false)
		close(cancel)
		// Wait for the evaluation to notice.  It is running in another
		// goroutine and may be part way through a step or blocked in a
		// primitive; both check the channel, so this returns quickly — and
		// waiting for it is what makes the next form start from a quiet
		// machine rather than alongside the one that was abandoned.
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			// It did not stop.  Say nothing about it here: the REPL must stay
			// usable, and a machine that ignores the channel is a bug to find,
			// not a reason to hang the prompt.
		}
		fmt.Fprint(stdout, "^C\n")
		return true
	}
}

// readForms parses every datum in src.
func readForms(src string) ([]re.Value, error) {
	r := re.NewStringReader(src)
	r.Source = "<stdin>"
	return r.ReadAll()
}

// evalForm evaluates one datum, reporting its value.  It returns false when
// the session should end.
func evalForm(m *scheme.Machine, f re.Value, stdout, stderr io.Writer) (keepGoing, failed bool) {
	v, err := m.Run(f, m.Global)
	if err != nil {
		if _, ok := err.(*scheme.ExitError); ok {
			return false, false
		}
		printError(stderr, err)
		return true, true
	}
	if _, un := v.(re.Unspecified); !un {
		fmt.Fprintln(stdout, re.WriteToString(v))
	}
	return true, false
}

// newREPLLineEditor builds the editor the interactive REPL uses, with the
// highlighting and completion a REPL wants.
func newREPLLineEditor(m *scheme.Machine, out *lineTracker) *lineEditor {
	e := newLineEditor(os.Stdin, out)
	h := &replHighlighter{m: m}
	e.highlight = h.highlight
	e.colour = colourEnabled(os.Stdout)
	// A half-written expression keeps being edited on the next line rather than
	// being submitted: the reader is asked whether it has a complete datum, and
	// says no while a form is still open.
	e.continues = func(text string) bool {
		_, err := readForms(text)
		return re.IsIncomplete(err)
	}
	return e
}

// colourEnabled reports whether to colour the line.  A terminal that says it is
// not one, or a user who has said no, gets plain text: escape sequences written
// into a pipe or a file are noise, and NO_COLOR is the convention for the rest.
func colourEnabled(f *os.File) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if v := os.Getenv("TERM"); v == "" || v == "dumb" {
		return false
	}
	return isTerminal(f)
}

// replHighlighter colours a line of Scheme: the names the interpreter provides,
// the syntax, and the bracket pair the cursor is on.
type replHighlighter struct {
	m *scheme.Machine
	// builtins and syntax are filled in the first time a line is drawn, because
	// the environment is still being built when the editor is created.
	builtins map[string]bool
	syntax   map[string]bool
}

func (h *replHighlighter) highlight(line []rune, pos int) []span {
	h.once()
	return highlight(line, pos, func(word string) style {
		switch {
		case h.syntax[word]:
			return styleSyntax
		case h.builtins[word]:
			return styleBuiltin
		default:
			return stylePlain
		}
	})
}

func (h *replHighlighter) once() {
	if h.builtins != nil {
		return
	}
	h.builtins = map[string]bool{}
	if h.m != nil {
		for sym := range h.m.Builtin.Snapshot() {
			h.builtins[sym.Name] = true
		}
	}
	h.syntax = map[string]bool{}
	for _, name := range scheme.SyntaxNames() {
		h.syntax[name] = true
	}
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
