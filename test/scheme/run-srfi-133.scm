;;; SPDX-License-Identifier: MIT
;;; Runs the SRFI-133 vector library suite.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/srfi-133-tests.scm")
