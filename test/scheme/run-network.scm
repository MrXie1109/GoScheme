;;; SPDX-License-Identifier: MIT
;;; Runs the GoScheme network extension suite.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/goscheme-network-tests.scm")
