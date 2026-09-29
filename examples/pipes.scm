#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Pipelines built from processes -- the (goscheme process) library.
;;;
;;; Run with:  goscheme examples/pipes.scm
;;;
;;; (system command) lets the shell splice a pipeline together from one
;;; string.  When the stages should be chosen by the program instead,
;;; open-input-process and open-output-process connect them directly: one
;;; child's standard output is a Scheme input port, another child's standard
;;; input is a Scheme output port, and an ordinary loop moves the data.  No
;;; shell is started in either direction.

(import (scheme base) (scheme write) (goscheme process))

(define (displayln . args)
  (for-each display args)
  (newline))

;;; ------------------------------------------------------- one child's output
;; open-input-process runs its program directly and gives back an input port
;; on the child's standard output, so read-line works on it with no new
;; vocabulary.  The child is reaped when the port is closed, and only then
;; does process-status have an answer.
(define hello (open-input-process "echo" "hello from echo"))
(displayln "the child says: " (read-line hello))
(displayln "before closing, its status is " (process-status hello))
(close-port hello)
(displayln "after closing, its status is " (process-status hello))

;;; -------------------------------------------------------- a two-stage pipe
;; This is the shape `echo hello pipeline | tr a-z A-Z` has in a shell,
;; written out stage by stage:
;;   * source reads what `echo hello pipeline` writes to its standard output;
;;   * sink writes into the standard input of `tr a-z A-Z`, whose own standard
;;     output is inherited from this program, so the upper-cased line appears
;;     on our standard output.
;; The copy loop is the pipe.  Closing sink closes tr's standard input, which
;; is how tr sees end of file and finishes; its status is then available.
(define source (open-input-process "echo" "hello pipeline"))
(define sink (open-output-process "tr" "a-z" "A-Z"))

(let copy ((lines 0))
  (let ((line (read-line source)))
    (if (eof-object? line)
        (begin
          (close-output-port sink)
          (close-port source)
          (displayln "copied " lines " line(s) through the pipeline"))
        (begin
          (write-string line sink)
          (newline sink)
          (copy (+ lines 1))))))

(displayln "pipeline statuses: " (process-status source) " and " (process-status sink))

;;; ---------------------------------------------------------- through a shell
;; For comparison, the same pipeline through the shell is a single call.  The
;; child writes to our standard output, so anything buffered must be flushed
;; first to keep the output in order.
(display "through the shell: ")
(flush-output-port (current-output-port))
(system "echo hello shell | tr a-z A-Z")

;;; ------------------------------------------------------------ the difference
;; The shell form is shorter, but only the port form can hand the data back to
;; Scheme in the middle: read a line, transform it, and write it on.
(displayln "the port form keeps the middle in Scheme")

(newline)
(display "pipes: end of tour") (newline)
