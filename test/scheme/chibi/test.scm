;;; SPDX-License-Identifier: MIT
;;; (chibi test) compatible test library, implemented in portable R7RS.
;;;
;;; It provides the subset of the chibi/SRFI-64 API used by the reference
;;; R7RS test suite: test-begin, test-end, test, test-assert, test-values
;;; and test-error.  Failures are reported on the current error port and a
;;; summary is printed when the outermost group ends.

(define-library (chibi test)
  (export test-begin test-end test test-assert test-values test-error
          test-group test-passed test-failed test-failures
          test-approximate-equal?)
  (import (scheme base) (scheme write) (scheme process-context))
  (begin
    (define *passed* 0)
    (define *failed* 0)
    (define *failures* '())
    (define *groups* '())
    (define *verbose* #f)

    (define (group-name)
      (if (null? *groups*) "" (car *groups*)))

    (define (record-pass)
      (set! *passed* (+ *passed* 1)))

    (define (record-fail name expect actual)
      (set! *failed* (+ *failed* 1))
      (set! *failures* (cons (list (group-name) name expect actual) *failures*))
      (display "FAIL: ") (display (group-name))
      (display " / ") (write name)
      (display "\n  expected: ") (write expect)
      (display "\n  actual:   ") (write actual)
      (newline))

    (define (test-begin name . o)
      (set! *groups* (cons name *groups*))
      #f)

    (define (test-end . o)
      (if (pair? *groups*)
          (set! *groups* (cdr *groups*)))
      (if (null? *groups*)
          (begin
            (display "== ")
            (display *passed*) (display " passed, ")
            (display *failed*) (display " failed")
            (newline)
            (if (not (zero? *failed*))
                (exit 1)
                (exit 0))))
      #f)

    (define (approx-real? e a)
      (cond
        ((and (nan? e) (nan? a)) #t)
        ((or (infinite? e) (infinite? a)) (= e a))
        (else
         (<= (abs (- e a))
             (* 1e-5 (max 1.0 (abs e) (abs a)))))))

    (define (test-approximate-equal? expect actual)
      (cond
        ((and (number? expect) (number? actual)
              (or (inexact? expect) (inexact? actual)))
         (if (and (real? expect) (real? actual))
             (approx-real? (inexact expect) (inexact actual))
             (and (approx-real? (inexact (real-part expect)) (inexact (real-part actual)))
                  (approx-real? (inexact (imag-part expect)) (inexact (imag-part actual))))))
        ((and (number? expect) (number? actual)
              (or (inexact? expect) (inexact? actual))
              (real? expect) (real? actual))
         (let ((e (inexact expect)) (a (inexact actual)))
           (cond
             ((and (nan? e) (nan? a)) #t)
             ((or (infinite? e) (infinite? a)) (= e a))
             (else
              (<= (abs (- e a))
                  (* 1e-5 (max 1.0 (abs e) (abs a))))))))
        (else (equal? expect actual))))

    (define (run-test name expect thunk)
      (let ((actual (thunk)))
        (if (test-approximate-equal? expect actual)
            (record-pass)
            (record-fail name expect actual))))

    (define (run-test-values name expect-thunk thunk)
      (let ((e (call-with-values expect-thunk list))
            (a (call-with-values thunk list)))
        (if (equal? e a)
            (record-pass)
            (record-fail name e a))))

    (define (run-test-error name thunk)
      (guard (e (else (record-pass)))
        (thunk)
        (record-fail name 'error 'no-error-raised)))

    (define-syntax test
      (syntax-rules ()
        ((_ expect expr)
         (run-test 'expr expect (lambda () expr)))
        ((_ name expect expr)
         (run-test name expect (lambda () expr)))))

    (define-syntax test-assert
      (syntax-rules ()
        ((_ expr)
         (run-test 'expr #t (lambda () (if expr #t #f))))
        ((_ name expr)
         (run-test name #t (lambda () (if expr #t #f))))))

    (define-syntax test-values
      (syntax-rules ()
        ((_ expect expr)
         (run-test-values 'expr (lambda () expect) (lambda () expr)))))

    (define-syntax test-error
      (syntax-rules ()
        ((_ expr)
         (run-test-error 'expr (lambda () expr)))
        ((_ name expr)
         (run-test-error name (lambda () expr)))))

    (define-syntax test-group
      (syntax-rules ()
        ((_ name body ...)
         (begin
           (test-begin name)
           body ...
           (test-end name)))))

    (define (test-passed) *passed*)
    (define (test-failed) *failed*)
    (define (test-failures) (reverse *failures*))))
