;;; SPDX-License-Identifier: MIT
;;; Runs the SRFI-1 list library suite.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/srfi-1-tests.scm")
