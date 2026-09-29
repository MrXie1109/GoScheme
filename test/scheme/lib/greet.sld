;;; SPDX-License-Identifier: MIT
;;; A library loaded from a file, for the library tests.
(define-library (lib greet)
  (export greet greet-loud)
  (import (scheme base) (scheme char))
  (begin
    (define (greet who) (string-append "hello " who))
    (define (greet-loud who) (string-upcase (greet who)))))
