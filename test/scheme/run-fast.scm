;;; SPDX-License-Identifier: MIT
;;; Runs the GoScheme performance library suite.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/goscheme-fast-tests.scm")
