;;; SPDX-License-Identifier: MIT
;;; Runs the GoScheme pattern matching suite.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/goscheme-match-tests.scm")
