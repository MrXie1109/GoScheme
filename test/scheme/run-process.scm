;;; SPDX-License-Identifier: MIT
;;; Runs the GoScheme process and filesystem extension suite.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/goscheme-process-tests.scm")
