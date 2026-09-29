;;; SPDX-License-Identifier: MIT
;;; A library whose body is included from another file.
(define-library (lib use-include)
  (export twice)
  (import (scheme base))
  (include "body.scm"))
