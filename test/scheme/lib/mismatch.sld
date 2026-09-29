;;; SPDX-License-Identifier: MIT
;;; A file whose library is not the one an import asks for.
(define-library (lib something-else)
  (export whatever)
  (import (scheme base))
  (begin (define whatever 1)))
