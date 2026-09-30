#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; JSON, both directions -- (goscheme json).
;;;
;;; Run with:  goscheme examples/json.scm
;;;
;;; What the mapping is: objects become equal? hash tables with string keys,
;;; arrays become vectors, and JSON null is the symbol null.  Numbers keep
;;; their exactness, so an integer with more digits than a machine word
;;; survives the round trip.

(import (scheme base) (scheme write) (goscheme json) (goscheme hash-table))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

(define document
  "{\"name\":\"goscheme\",\"version\":2,\"r7rs\":true,\"tags\":[\"scheme\",\"go\"],\
\"nothing\":null,\"big\":123456789012345678901234567890}")

(define parsed (json-parse document))

(show "it parses to a hash table:" (hash-table? parsed) (hash-table-size parsed))
(show "a string member:" (hash-table-ref parsed "name"))
(show "a boolean member:" (hash-table-ref parsed "r7rs"))
(show "an array member:" (hash-table-ref parsed "tags"))
(show "the first tag:" (vector-ref (hash-table-ref parsed "tags") 0))
(show "null is the symbol null:" (hash-table-ref parsed "nothing"))
(show "and a big integer stays exact:"
      (hash-table-ref parsed "big")
      (exact? (hash-table-ref parsed "big"))
      (string-length (number->string (hash-table-ref parsed "big"))))

;; Writing it back gives JSON again, and reading that gives the same value.
(define written (json-write parsed))
(show "written back:" written)

;; Reading it again gives the same members.  Note that equal? compares hash
;; tables by identity, which R7RS allows, so the comparison is made on what is
;; inside them rather than on the tables themselves.
(define reparsed (json-parse written))
(show "the round trip keeps the members:"
      (equal? (hash-table-ref reparsed "tags") (hash-table-ref parsed "tags"))
      (equal? (hash-table-ref reparsed "big") (hash-table-ref parsed "big"))
      (equal? (hash-table-ref reparsed "nothing") (hash-table-ref parsed "nothing")))

;;; ------------------------------------------------------------ building one
(define table (make-equal-hashtable))
(hash-table-set! table "answer" 42)
(hash-table-set! table "list" (vector 1 2 3))
(hash-table-set! table "maybe" 'null)
(show "built by hand:" (json-write table))

;;; ----------------------------------------------------------------- errors
(show "bad JSON is an ordinary condition:"
      (guard (e (#t 'bad-json)) (json-parse "{nope")))

(newline)
(display "json: end of tour")
(newline)
