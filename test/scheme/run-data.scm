;;; SPDX-License-Identifier: MIT
;;; Runs the GoScheme data extension suite.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/goscheme-data-tests.scm")
