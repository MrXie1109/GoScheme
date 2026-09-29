;;; SPDX-License-Identifier: MIT
;;; Runs the reference R7RS test suite with the bundled (chibi test) shim.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/r7rs-tests.scm")
