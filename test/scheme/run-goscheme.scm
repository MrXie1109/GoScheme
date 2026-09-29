;;; SPDX-License-Identifier: MIT
;;; Runs the GoScheme-specific regression suite.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/goscheme-tests.scm")
