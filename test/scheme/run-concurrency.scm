;;; Runs the GoScheme concurrency extension suite.
(import (scheme base) (scheme load) (scheme write))
(load "test/scheme/chibi/test.scm")
(load "test/scheme/goscheme-concurrency-tests.scm")
