package scheme

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
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
