// SPDX-License-Identifier: MIT

package scheme

import (
	"errors"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// RunSubprocess, when set, runs fn with the terminal in its normal (cooked)
// mode.  The command line driver installs it so that interactive children —
// editors, pagers, a shell — behave, instead of inheriting the REPL's raw
// mode.  Programs that never touch the terminal are unaffected.
var RunSubprocess func(fn func())

// installProcess provides the subprocess procedures.  They are an extension:
// R7RS-small has no way to start a child process.
func installProcess(m *Machine) {
	const lib = "(goscheme process)"

	// (system command) runs command through the operating system's command
	// processor and returns its exit status.
	m.def("system", 1, 1, func(m *Machine, a []Value) {
		line := wantString("system", a[0]).Value()
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/c", line)
		} else {
			cmd = exec.Command("/bin/sh", "-c", line)
		}
		runSubprocess(m, "system", cmd)
	}, lib)

	// (system* program arg ...) runs program directly, without a shell.
	m.def("system*", 1, -1, func(m *Machine, a []Value) {
		program := wantString("system*", a[0]).Value()
		args := make([]string, 0, len(a)-1)
		for _, v := range a[1:] {
			args = append(args, wantString("system*", v).Value())
		}
		runSubprocess(m, "system*", exec.Command(program, args...))
	}, lib)

	// (open-input-process program arg ...) starts program directly, without a
	// shell, and returns an input port carrying its standard output.  The
	// child's standard input is empty and its standard error is inherited.
	// Starting is immediate, but waiting is not: the child is reaped when the
	// port is closed, and (process-status port) then reports its exit status.
	m.def("open-input-process", 1, -1, func(m *Machine, a []Value) {
		program, args := processCommand("open-input-process", a)
		cmd := exec.Command(program, args...)
		cmd.Stderr = os.Stderr
		// A nil Stdin gives the child the null device, so it reads end of
		// file at once; this is the same on every platform.
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			m.RaiseError(NewFileError("open-input-process: "+err.Error(), NewString(program)))
			return
		}
		if err := cmd.Start(); err != nil {
			_ = pipe.Close()
			m.RaiseError(NewFileError("open-input-process: "+err.Error(), NewString(program)))
			return
		}
		m.Return(NewPortFromStream(program, &processStream{reader: pipe, cmd: cmd}, true))
	}, lib)

	// (open-output-process program arg ...) starts program directly and
	// returns an output port connected to its standard input.  The child's
	// standard output goes to this interpreter's standard output.  Closing the
	// port closes the child's input, so it sees end of file and can finish.
	m.def("open-output-process", 1, -1, func(m *Machine, a []Value) {
		program, args := processCommand("open-output-process", a)
		cmd := exec.Command(program, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		pipe, err := cmd.StdinPipe()
		if err != nil {
			m.RaiseError(NewFileError("open-output-process: "+err.Error(), NewString(program)))
			return
		}
		if err := cmd.Start(); err != nil {
			_ = pipe.Close()
			m.RaiseError(NewFileError("open-output-process: "+err.Error(), NewString(program)))
			return
		}
		m.Return(NewPortFromStream(program, &processStream{writer: pipe, cmd: cmd}, true))
	}, lib)

	// (process-status port) reports the exit status of the child behind a port
	// made by open-input-process or open-output-process, or #f while that
	// child has not been reaped.  The port is closed first, which is when the
	// child is waited for; a child killed by a signal reports 128+signal, the
	// way a shell (and system*) reports it.  The port carries its
	// processStream as its underlying stream; since process-status lives in
	// the same package it reaches it straight through the port's closer.
	m.defSimple("process-status", 1, 1, func(a []Value) (Value, error) {
		p := wantPort("process-status", a[0])
		ps, ok := p.Closer().(*processStream)
		if !ok {
			return nil, errf("process-status", "expected a port from open-input-process or open-output-process but got %s", WriteToString(a[0]))
		}
		if status, done := ps.finished(); done {
			return Int(int64(status)), nil
		}
		return False, nil
	}, lib)
}

// processCommand reads the program and its arguments for the process
// procedures, which take the program and each argument as a separate string.
func processCommand(name string, a []Value) (string, []string) {
	program := wantString(name, a[0]).Value()
	args := make([]string, 0, len(a)-1)
	for _, v := range a[1:] {
		args = append(args, wantString(name, v).Value())
	}
	return program, args
}

// processStream couples a child process's pipe with the command that created
// it.  A port from open-input-process or open-output-process holds one of
// these as its underlying stream, so the port can be read or written with the
// ordinary I/O procedures, and closing the port closes the pipe, reaps the
// child with (*exec.Cmd).Wait and remembers the exit status for
// process-status.  Only one of reader and writer is set: the child's standard
// output for an input process, its standard input for an output process.
type processStream struct {
	mu     sync.Mutex
	reader io.ReadCloser
	writer io.WriteCloser
	cmd    *exec.Cmd
	done   bool
	status int
}

// Read reads from the child's standard output.
func (ps *processStream) Read(p []byte) (int, error) {
	if ps.reader == nil {
		return 0, io.EOF
	}
	return ps.reader.Read(p)
}

// Write writes to the child's standard input.
func (ps *processStream) Write(p []byte) (int, error) {
	if ps.writer == nil {
		return 0, errors.New("process port is not open for writing")
	}
	return ps.writer.Write(p)
}

// Close closes the pipe, waits for the child and records its exit status.  A
// non-zero status is not an error here: it is what process-status reports.
// Waiting is why open-input-process and open-output-process do not wait when
// they start the child — a reader may want to see the output first.
func (ps *processStream) Close() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.done {
		return nil
	}
	var err error
	if ps.writer != nil {
		err = ps.writer.Close()
		ps.writer = nil
	}
	if ps.reader != nil {
		if e := ps.reader.Close(); err == nil {
			err = e
		}
		ps.reader = nil
	}
	ps.status = processWaitStatus(ps.cmd.Wait())
	ps.done = true
	return err
}

// finished reports the remembered exit status and whether the child has been
// reaped.
func (ps *processStream) finished() (int, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.status, ps.done
}

// processWaitStatus turns the result of (*exec.Cmd).Wait into an exit status
// the way a shell reports it: 128+signal for a child killed by a signal, and 1
// when the wait failed for an unknown reason.  runSubprocess uses the same
// rule for system and system*, but reports a start failure too; here every
// failure is just a status.
func processWaitStatus(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
	}
	return 1
}

// runSubprocess starts cmd with the interpreter's own standard streams, waits
// for it, and returns its exit status.  A command that cannot be started at
// all raises a file error; a command killed by a signal reports 128+signal,
// the way a shell does.
func runSubprocess(m *Machine, name string, cmd *exec.Cmd) {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	run := func() (int, error) {
		err := cmd.Run()
		if err == nil {
			return 0, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if code := exitErr.ExitCode(); code >= 0 {
				return code, nil
			}
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				return 128 + int(ws.Signal()), nil
			}
			return 1, nil
		}
		return 0, NewFileError("cannot run "+name+": "+err.Error(), NewString(cmd.Path))
	}

	var code int
	var err error
	if RunSubprocess != nil {
		RunSubprocess(func() { code, err = run() })
	} else {
		code, err = run()
	}
	if err != nil {
		m.RaiseError(err)
		return
	}
	m.Return(Int(int64(code)))
}
