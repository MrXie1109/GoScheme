#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Running other programs -- the (goscheme process) library.
;;;
;;; Run with:  goscheme examples/processes.scm
;;;
;;; (system command) runs one command line through the shell, and
;;; (system* program arg ...) executes the program directly.  Both return the
;;; exit status the way a shell reports it, and both let the child inherit this
;;; process's standard streams.  These examples assume a POSIX shell.

(import (scheme base) (scheme write) (goscheme process))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;; A child writes to the same stdout as we do, so anything we have buffered must
;; be flushed first: otherwise the child's line appears before its own label.
;; The status is printed after the child's output, to keep the two apart.
(define (show-run label thunk)
  (display label)
  (flush-output-port (current-output-port))
  (let ((status (thunk)))
    (display "  [exit status ")
    (write status)
    (display "]")
    (newline)))

;;; --------------------------------------------------------------- exit status
(show "a command line through the shell:" (system "exit 0") (system "exit 3"))
(show "programs run directly:" (system* "true") (system* "false"))
(show "with arguments:" (system* "sh" "-c" "exit 7"))
;; A process killed by a signal is reported the way a shell reports it:
;; 128 + the signal number, so SIGTERM (15) becomes 143.
(show "killed by SIGTERM:" (system* "sh" "-c" "kill -TERM $$"))

;;; -------------------------------------------------------- the shell matters
;; system takes one string, so the shell parses it and pipelines and
;; redirections work.  system* takes the program and its arguments separately
;; and does not involve a shell at all.
(show-run "system can use a pipeline: " (lambda () (system "echo one two three | wc -w")))
(show "system* would look for a program with that whole name:"
      (guard (e ((file-error? e) 'file-error)) (system* "echo one two three")))

;;; ------------------------------------------------------------ child output
;; The child writes to the same stdout, so its output arrives in order.
(show-run "the child's own output: " (lambda () (system "echo this line comes from the shell")))

;; To read a child's output instead of showing it, send it to a file.
(define tmp "goscheme-process-example.txt")
(system (string-append "printf 'captured\\n' > " tmp))
(show "read back from the file:" (call-with-input-file tmp read-line))
(delete-file tmp)
(show "and cleaned up:" (file-exists? tmp))

;;; ----------------------------------------------------------- a failed start
;; A program that cannot be started at all is a file error, not an exit status.
(show "starting a missing program:"
      (guard (e ((file-error? e) 'file-error)) (system* "/nonexistent-program-xyz")))

;;; ------------------------------------------------------------ a whole line
;; Since system returns the shell's status, shell logic is visible here.
(show-run "true && false || echo recovered: "
          (lambda () (system "true && false || echo recovered")))

(newline)
(display "processes: end of tour") (newline)
