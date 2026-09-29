#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Libraries loaded from files.
;;;
;;; Run it from anywhere -- the search starts at the importing file's own
;;; directory, so these all work:
;;;
;;;   goscheme examples/libraries/main.scm        (from the repository root)
;;;   cd examples/libraries && goscheme main.scm
;;;
;;; (lib greet) is read from lib/greet.sld next to this file, and (lib namer)
;;; imports it in turn.  See ../README.md for the search path rules, and for how
;;; `goscheme build -static` turns this into one file that needs no library at
;;; all.

(import (scheme base) (scheme write)
        (lib greet)
        (lib namer))

;; greet is a procedure from (lib greet), greeting a variable from it.
(greet "world")
(display greeting)
(newline)

;; greet-all comes from (lib namer), which imported (lib greet) itself.
(greet-all "Ada" "Grace" "Alan")
