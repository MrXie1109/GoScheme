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
//	-v         print the version
//
// With no file and no -e the interpreter starts an interactive REPL.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"goscheme/internal/scheme"
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
	args := os.Args[1:]
	var files []string
	var exprs []string
	interactive := false
	quiet := false
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
	// R7RS 6.14: the first element of (command-line) is the name of the
	// command, i.e. whatever the operating system passed as argv[0]; the
	// second is the script (if any) and the remaining elements are its
	// arguments.
	prog := "goscheme"
	if len(os.Args) > 0 && os.Args[0] != "" {
		prog = os.Args[0]
	}
	m.Args = append([]string{prog}, append(append([]string{}, files...), rest...)...)

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
		repl(m, quiet)
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: goscheme [-i] [-q] [-e expr] [file] [args...]")
}

func evalString(m *scheme.Machine, src, name string) int {
	r := scheme.NewStringReader(src)
	r.Source = name
	forms, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
		return 1
	}
	if _, err := m.RunForms(forms, m.Global); err != nil {
		return reportError(err)
	}
	return 0
}

func loadFile(m *scheme.Machine, path string) int {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
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
	if _, err := m.RunForms(forms, m.Global); err != nil {
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

func repl(m *scheme.Machine, quiet bool) {
	if !isTerminal(os.Stdin) {
		// Not talking to a user: no banner, no prompts.
		replOn(m, os.Stdin, os.Stdout, os.Stderr, false, nil)
		return
	}
	// Everything written to the terminal goes through out, so the REPL knows
	// where the cursor is even when Scheme code used (display ...).
	out := newLineTracker(os.Stdout)
	m.SetStandardOutput(scheme.NewPortFromFile("stdout", out, false, true))
	if !quiet {
		fmt.Fprintln(out, versionString())
		fmt.Fprintln(out, "Type (exit) or press Ctrl-D to leave.")
	}
	restore, err := makeRaw(os.Stdin)
	if err != nil {
		// No raw mode available: fall back to the canonical reader, which
		// still avoids prompting while input is already queued.
		replOn(m, os.Stdin, os.Stdout, os.Stderr, false, func() bool {
			return inputPending(os.Stdin)
		})
		return
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

	// Ctrl-C aborts the evaluation in progress instead of killing the REPL.
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt)
	defer signal.Stop(sigint)

	replEdited(m, newLineEditor(os.Stdin, out), out, os.Stderr, sigint)
}

// replEdited is the line editing read-eval-print loop.  A bracketed paste is
// inserted into the line being edited and submitted only when Enter is pressed,
// so a pasted program is evaluated as a unit with no prompts in between.
func replEdited(m *scheme.Machine, ed *lineEditor, stdout, stderr io.Writer, sigint <-chan os.Signal) {
	var buf strings.Builder
	prompt := primaryPrompt
	for {
		line, err := ed.ReadLine(prompt)
		if err != nil {
			if buf.Len() > 0 {
				fmt.Fprintln(stderr, "Error: unexpected end of input")
			}
			fmt.Fprintln(stdout)
			return
		}
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
func replOn(m *scheme.Machine, stdin io.Reader, stdout, stderr io.Writer, banner bool, pending func() bool) {
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
				if !evalForm(m, f, stdout, stderr) {
					return
				}
			}
		case scheme.IsIncomplete(perr):
			// Wait for the rest of the datum.
			incomplete = true
		default:
			fmt.Fprintf(stderr, "Error: %v\n", perr)
			buf.Reset()
			incomplete = false
		}

		if rerr != nil {
			// End of input.
			if incomplete {
				fmt.Fprintln(stderr, "Error: unexpected end of input")
			}
			if pending != nil {
				fmt.Fprintln(stdout)
			}
			return
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
func evalForm(m *scheme.Machine, f scheme.Value, stdout, stderr io.Writer) bool {
	v, err := m.Run(f, m.Global)
	if err != nil {
		if _, ok := err.(*scheme.ExitError); ok {
			return false
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return true
	}
	if _, un := v.(scheme.Unspecified); !un {
		fmt.Fprintln(stdout, scheme.WriteToString(v))
	}
	return true
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
