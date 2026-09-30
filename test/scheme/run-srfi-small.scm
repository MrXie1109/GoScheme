;;; SPDX-License-Identifier: MIT
;;; Runs the small SRFI suites: and-let*, receive, cut/cute and boxes.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/srfi-small-tests.scm")
