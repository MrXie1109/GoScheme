#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Regular expressions -- (goscheme regexp), which is Go's RE2.
;;;
;;; Run with:  goscheme examples/regexp.scm

(import (scheme base) (scheme write) (goscheme regexp))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

(define log-line "2026-09-30 09:15:22 ERROR could not open config.scm")

;; A match is the whole match followed by the capture groups, or #f.
(show "the fields of a log line:"
      (regexp-match "^(\\S+) (\\S+) (\\w+) (.*)$" log-line))
(show "no match gives #f:" (regexp-match "WARNING" log-line))
(show "and a predicate when only the answer matters:"
      (regexp-match? "ERROR" log-line)
      (regexp-match? "DEBUG" log-line))

;; Positions are half-open index pairs, so a program can slice the string.
(show "where ERROR sits:" (regexp-match-positions "ERROR" log-line))

;; Replacements understand $1, $2, ... for the groups.
(show "reordered with $2, $1:"
      (regexp-replace "^(\\S+) (\\S+)" log-line "$2 $1"))
(show "every vowel doubled:"
      (regexp-replace-all "[aeiou]" "scheme interpreter" "$0$0"))
(show "split on whitespace:"
      (regexp-split "\\s+" log-line))
(show "a compiled pattern can be reused:"
      (let ((number (regexp "[0-9]+")))
        (list (regexp? number)
              (regexp-match number "abc 123 def")
              (regexp-match? number "no digits here"))))
(show "a bad pattern is an ordinary condition:"
      (guard (e (#t 'bad-pattern)) (regexp "[unclosed")))

(newline)
(display "regexp: end of tour")
(newline)
