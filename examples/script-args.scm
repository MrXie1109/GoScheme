#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; What a script sees: (command-line), assertions, #!unspecified, reader forms.
;;;
;;; Run with:  goscheme examples/script-args.scm alpha beta
;;;
;;; The shebang line above is skipped by the reader, so after `chmod +x` and
;;; with goscheme on PATH the same file also runs as ./script-args.scm alpha beta

(import (scheme base) (scheme write))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;;; ------------------------------------------------------------ command-line
;; The first element is the script, and the rest are the arguments that were
;; given to it.  The interpreter's own name is deliberately *not* in the list,
;; so a script and an executable built from it with `goscheme build` report the
;; same shape, and (cdr (command-line)) is always just the user's arguments.
(show "command line:" (command-line))
(show "the script itself:" (car (command-line)))
(show "the user's arguments:" (cdr (command-line)))

;;; --------------------------------------------------------------- assertions
;; (assert expr) raises an ordinary condition when expr is false, which is more
;; useful in a script than a warning: it can be caught, or left to stop the run.
(assert (= 2 (+ 1 1)))
(show "a passing assert is silent")

(show "a failing assert carries the expression:"
      (guard (e (#t (list (error-object-message e) (error-object-irritants e))))
        (assert (= 2 (+ 1 2)))))

;;; ------------------------------------------------------------- #!unspecified
;; The extensions return #!unspecified where there is no useful result, and the
;; reader turns it into a value you can compare against and print.
(show "#!unspecified is a value:" #!unspecified)
(show "and it is recognisable:" (eq? #!unspecified (if #f #f)))

;;; ----------------------------------------------------------------- features
;; (features) reports what this particular build can do.  Note that `ffi` only
;; appears in a build made with cgo; see docs/ffi-design.md.
(show "features:" (features))

;;; --------------------------------------------------------- reader comforts
;; Numbers may use the alternative exponent markers s, f, d and l, and the usual
;; radix prefixes; #! at the start of a line is skipped so a shebang can lead.
(show "exponent markers:" (list 1s3 1f3 1d3 1l3))
(show "radix prefixes:" (list #x1f #b1011 #o17))
(show "#e makes an exact number from a decimal:" #e1.5)

(newline)
(display "script arguments: end of tour") (newline)
