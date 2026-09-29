;;; SPDX-License-Identifier: MIT
;;; Half of a circular pair, with (lib cycle-a).
(define-library (lib cycle-b)
  (export b-value)
  (import (scheme base) (lib cycle-a))
  (begin (define b-value 2)))
