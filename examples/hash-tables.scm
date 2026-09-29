#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Hash tables -- the (goscheme hash-table) library.
;;;
;;; Run with:  goscheme examples/hash-tables.scm
;;;
;;; The names follow SRFI 125.  Every table is a Go map underneath, so the order
;;; of keys, values and alists is unspecified; this example only prints results
;;; that do not depend on it.

(import (scheme base) (scheme write) (goscheme hash-table))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;;; ------------------------------------------------- how keys are compared
;; There are three constructors, differing only in the comparison used for
;; keys, plus make-hash-table, which is the equal? one.  An optional size hint
;; is accepted; the underlying Go map grows on its own.

(define by-equal (make-equal-hashtable))  ; equal?  -- structural
(define by-eq (make-eq-hashtable))        ; eq?     -- identity
(define by-eqv (make-eqv-hashtable))      ; eqv?    -- numbers, characters

(hash-table-set! by-equal '(1 2) 'structural)
(show "equal? table finds an equal but freshly built key:"
      (hash-table-ref/default by-equal (list 1 2) 'missing))

(hash-table-set! by-eq '(1 2) 'identity)
(show "eq? table does not:"
      (hash-table-ref/default by-eq (list 1 2) 'missing))
(show "unless it is the very same object:"
      (let ((k (list 1 2)))
        (hash-table-set! by-eq k 'found)
        (hash-table-ref/default by-eq k 'missing)))

;;; ------------------------------------------------------------- accessors
(define plain (make-hash-table))
(hash-table-set! plain 'a 1)
(hash-table-set! plain 'b 2)

(show "ref on a present key:" (hash-table-ref plain 'a))
;; The third argument of ref is a *thunk* to call when the key is missing
;; (SRFI 125), which is why it is not simply a default value.
(show "ref with a failure thunk:" (hash-table-ref plain 'zzz (lambda () 'computed)))
(show "ref/default takes a value instead:"
      (hash-table-ref/default plain 'zzz 'fallback))

;; Without a default, a missing key is an error rather than #f, so it can be
;; caught like any other condition.
(show "ref on a missing key raises:"
      (guard (e (#t (error-object-message e))) (hash-table-ref plain 'zzz)))

(show "exists?:" (hash-table-exists? plain 'a) (hash-table-exists? plain 'zzz))
(show "contains? is an alias:" (hash-table-contains? plain 'a))
(show "size and count are aliases:" (hash-table-size plain) (hash-table-count plain))
(show "the table itself:" plain)
(show "hash-table?:" (hash-table? plain) (hash-table? 'not-a-table))

;;; --------------------------------------------------------------- updating
(hash-table-update! plain 'a (lambda (n) (+ n 10)))      ; a := 1 + 10
(hash-table-update! plain 'c (lambda (n) (+ n 10)) 0)    ; c := 0 + 10
(hash-table-delete! plain 'b)
(show "after update! and delete!:" (hash-table-ref plain 'a) (hash-table-ref plain 'c)
      (hash-table-exists? plain 'b))

;;; ------------------------------------------------------- walking and bulk
;; walk visits every pair; the order is unspecified, so we accumulate.
(define total 0)
(hash-table-walk plain (lambda (key value) (set! total (+ total value))))
(show "sum of all values:" total)

(show "keys and values have the table's size:"
      (length (hash-table-keys plain)) (length (hash-table-values plain)))
(show "->alist has it too:" (length (hash-table->alist plain)))

;; A copy is independent of its original.
(define copy (hash-table-copy plain))
(hash-table-set! copy 'd 99)
(show "copy grew, original did not:" (hash-table-size copy) (hash-table-size plain))

(hash-table-clear! copy)
(show "after clear!:" (hash-table-size copy) (hash-table-exists? copy 'a))

;;; --------------------------------------------------------- building from a list
(define from-list (alist->hash-table '((one . 1) (two . 2) (three . 3))))
(show "alist->hash-table:" (hash-table-size from-list) (hash-table-ref from-list 'two))

;;; ------------------------------------------------------------------- hashing
;; hash returns an exact non-negative integer; equal objects hash alike, and an
;; optional bound restricts the result to [0, bound).
(show "equal keys hash alike:"
      (= (hash '(1 2)) (hash (list 1 2))))
(show "it is an exact, non-negative integer:"
      (exact? (hash "x")) (>= (hash "x") 0))
(show "a bound restricts it:"
      (let ((h (hash "anything" 16))) (and (<= 0 h) (< h 16))))

;;; --------------------------------------------------------------------- done
(newline)
(display "hash tables: end of tour") (newline)
