;;; SPDX-License-Identifier: MIT
;;; A library may import another library, and the search path is extended with
;;; each library's own directory as it is loaded, so (lib namer) finds its
;;; neighbour (lib greet) without being told where it is.

(define-library (lib namer)
  (export greet-all)
  (import (scheme base) (lib greet))

  (begin
    (define (greet-all first . rest)
      (greet first)
      (for-each greet rest))))
