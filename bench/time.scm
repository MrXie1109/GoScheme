;;; SPDX-License-Identifier: MIT
;;;
;;; time-it — run a command N times and print the fastest wall-clock time.
;;;
;;;     goscheme bench/time.scm N command [args...]
;;;
;;; A shell cannot do this job: /usr/bin/time has a resolution of 10 ms, and
;;; most of the C benchmarks here finish in less than one, so the shell reports
;;; 0.00 and the arithmetic that follows has nothing to work with.  GoScheme's
;;; (scheme time) gives a nanosecond clock, and process-spawn reports when the
;;; child has exited, which together are enough to time a subprocess properly.
(import (scheme base)
        (scheme write)
        (scheme time)
        (scheme process-context))

(define (usage)
  (display "usage: goscheme bench/time.scm RUNS COMMAND [ARG...]")
  (newline)
  (exit 2))

;; Run the command once and return the seconds it took.  Its output goes to
;; this program's standard output unless the caller redirected it, so the
;; benchmark's own report is thrown away by the caller.
;; The command line, quoted for /bin/sh, with its output discarded: the
;; benchmark's own report would otherwise be printed once per run.  A subprocess
;; started by system* inherits this process's output, so the redirect has to be
;; part of the line, which means going through the shell.
(define (shell-quote s)
  (string-append "'" (list->string
                      (map (lambda (c) (if (char=? c #\') #\' c))
                           (string->list s))) "'"))

(define (silenced command args)
  (let loop ((parts (cons command args)) (out ""))
    (if (null? parts)
        (string-append out " >/dev/null 2>&1")
        (loop (cdr parts)
              (string-append out " " (shell-quote (car parts)))))))

(define (once command args)
  (let ((t0 (current-jiffy)))
    (system (silenced command args))
    (/ (- (current-jiffy) t0) (jiffies-per-second))))

(define (best runs command args)
  (let loop ((i 0) (best #f))
    (if (= i runs)
        best
        (let ((t (once command args)))
          (loop (+ i 1) (if (or (not best) (< t best)) t best))))))

(define (main argv)
  (if (< (length argv) 2)
      (usage)
      (let* ((runs (string->number (car argv)))
             (command (cadr argv))
             (args (cddr argv))
             (t (best runs command args)))
        ;; Seconds with enough digits to see a microsecond program.
        (display (exact->inexact t))
        (newline)
        (exit 0))))

(main (cdr (command-line)))
