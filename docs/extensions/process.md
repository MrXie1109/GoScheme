# (goscheme process)

The (goscheme process) library starts child processes, which R7RS-small has no
way to express: it can run a command to completion and return its exit status,
or connect a child's standard input or output to an ordinary Scheme port. It is
built directly on Go's os/exec, and under the interactive command-line driver a
child is handed the terminal in cooked mode, so editors, pagers and shells
started this way behave normally. Import it alongside (scheme base):

```scheme
(import (scheme base) (goscheme process))
```

## Procedures

### Running a command to completion

| Procedure | Arguments | Description |
|---|---|---|
| `system` | `(system command)` | Runs the single string command through the operating system's command processor — /bin/sh -c on Unix, cmd /c on Windows — so shell syntax, quoting, redirection and pipelines all work. Returns the child's exit status as an exact integer: 0 on success, the exit code otherwise, or 128+signal when the child was killed by a signal (Unix). The shell itself starts successfully, so a command the shell cannot find yields 127 rather than an error. The child inherits the interpreter's standard input, output and error. |
| `system*` | `(system* program arg ...)` | Runs program directly with each remaining string as one argument, without a shell, so no quoting, globbing or variable expansion is performed; program is looked up in PATH when it contains no separator. Returns the exit status as an exact integer using the same convention as system (0, the exit code, or 128+signal). A program that cannot be started — not found, not executable — raises a file error. The child inherits the interpreter's standard streams. |

### Process ports

| Procedure | Arguments | Description |
|---|---|---|
| `open-input-process` | `(open-input-process program arg ...)` | Starts program directly (no shell) with the remaining strings as arguments and returns a textual input port carrying the child's standard output. The child's standard input is the null device, so it sees end of file immediately, and its standard error is inherited. Starting returns as soon as the process is launched; the constructor does not wait. Read the port with the ordinary input procedures, and close it to close the pipe and reap the child. |
| `open-output-process` | `(open-output-process program arg ...)` | Starts program directly (no shell) with the remaining strings as arguments and returns a textual output port connected to the child's standard input; the child's standard output and standard error are inherited. Write with the ordinary output procedures; closing the port closes the child's input so the child sees end of file and can finish, and closing also reaps the child. Starting does not wait. |
| `process-status` | `(process-status port)` | Returns the exit status of the child behind a port made by open-input-process or open-output-process as an exact integer (0, the exit code, or 128+signal), or #f while the child has not been reaped. The port must already have been closed — process-status does not close it, and reading to end of file on an input port does not reap the child either — so call close-port first. Once reaped, repeated calls return the same status. A port that did not come from one of the process constructors raises an error. |

## Notes

* Arguments are strings: system takes exactly one (the whole command line),
  while system* and the two port constructors take the program followed by one
  string per argument, so an argument containing spaces stays a single argument
  and needs no quoting.
* Exit statuses follow the shell convention: 0 for success, the child's exit
  code, 128+signal when a Unix child was killed by a signal, and 1 when the wait
  fails for an unknown reason. Windows has no signals, so there the status is
  simply the exit code and the 128+signal case does not arise.
* A program that cannot be started is reported differently by the two
  families: system* and the port constructors raise a file error in that case,
  while system cannot fail that way because it always launches the shell, so a
  missing command is the shell's own 127.
* The command line given to system is interpreted by /bin/sh on Unix and by
  cmd /c on Windows, so shell quoting rules differ between platforms; use
  system* when the shell must be bypassed.
* Child streams: system and system* give the child the interpreter's own
  stdin/stdout/stderr. open-input-process gives the child the null device as
  stdin, inherits stderr, and pipes stdout to the port; open-output-process
  inherits stdout and stderr and pipes the port to the child's stdin. Output
  written to stdout by an open-output-process child therefore appears in the
  interpreter's own output, not in the port.
* The port constructors do not wait when they start the child, which is what
  lets a reader consume the output before the child is reaped. The child is
  reaped by close-port, which also waits; process-status only reads the
  remembered result. Because close-port waits, it blocks until the child
  actually exits, so a child that keeps running (for example one that ignores
  its closed input) also blocks the close.
* Under the interactive command-line driver, children started by system and
  system* get the terminal back in cooked mode through the driver's terminal
  hook, so an editor, pager or shell reads normally; a script run
  non-interactively is unaffected.
* The ports are textual, so read-line, read-char, read-string, write-string and
  write-char work on them directly. There is no procedure to wait for a process
  port without closing it.

## Example

```scheme
(import (scheme base) (scheme write) (goscheme process))

;; system takes one command line and runs it through the shell.
(display "system: ")
(display (system "echo hello from the shell"))
(newline)                                   ; => hello from the shell, then 0

;; system* takes the program and each argument separately, with no shell.
(display "system*: ")
(display (system* "/bin/echo" "hello" "from" "system*"))
(newline)                                   ; => hello from system*, then 0

;; A child's exit status is an exact integer.
(display "exit status: ")
(write (system* "/bin/sh" "-c" "exit 3"))
(newline)                                   ; => 3

;; An input process port is an ordinary textual input port.
(define in (open-input-process "/bin/echo" "one" "two"))
(display "read: ")
(write (read-line in))
(newline)                                   ; => "one two"
(display "status before close: ")
(write (process-status in))                 ; not reaped yet
(newline)                                   ; => #f
(close-port in)                             ; closing reaps the child
(display "status after close: ")
(write (process-status in))
(newline)                                   ; => 0

;; An output process port carries the child's standard input.
(define out (open-output-process "/bin/cat"))
(write-string "round trip\n" out)
(close-port out)
(display "cat status: ")
(write (process-status out))
(newline)                                   ; => 0
```
