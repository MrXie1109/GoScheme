;;; SPDX-License-Identifier: MIT
;;; Half of a circular pair, with (lib cycle-b).
(define-library (lib cycle-a)
  (export a-value)
  (import (scheme base) (lib cycle-b))
  (begin (define a-value 1)))
