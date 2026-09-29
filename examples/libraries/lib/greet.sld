;;; SPDX-License-Identifier: MIT
;;; A library named (lib greet) lives in the file the name describes:
;;; examples/libraries/lib/greet.sld.  Nothing has to be registered anywhere --
;;; importing it finds the file.

(define-library (lib greet)
  (export greet greeting)
  (import (scheme base) (scheme write))

  (begin
    (define greeting "hello from a library")

    (define (greet who)
      (display greeting)
      (display ", ")
      (display who)
      (newline))))
