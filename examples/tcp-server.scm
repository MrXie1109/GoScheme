#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; A concurrent TCP server in Scheme.
;;;
;;; Run with:  goscheme examples/tcp-server.scm
;;;
;;; The shape is the one a Go programmer would write: one thread accepts
;;; connections, one thread per connection serves it, and the two sides are
;;; ordinary Scheme ports, so read-line and write-string are all the protocol
;;; needs.  The clients in this file talk to the server it starts, so running it
;;; shows both halves.

(import (scheme base) (scheme write)
        (goscheme socket)
        (goscheme sync))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;;; ------------------------------------------------------------------ server
;; Port 0 asks the operating system for a free port; the host defaults to the
;; loopback address, so this server is not reachable from the network.
(define listener (tcp-listen 0))
(define port (tcp-listener-port listener))
(define served (make-atomic))

(go
 (guard (e (#t 'listener-closed))
   (let accept-loop ()
     (let ((connection (tcp-accept listener)))
       ;; One thread per connection — the same shape as net/http, written out.
       (go
        (guard (e (#t 'client-gone))
          (atomic-add! served 1)
          (let echo ()
            (let ((line (read-line connection)))
              (if (eof-object? line)
                  #f
                  (begin
                    (write-string (string-append "echo: " line "\n") connection)
                    (flush-output-port connection)
                    (echo))))))
        (close-port connection))
       (accept-loop)))))

(show "listening on port" port)

;;; ------------------------------------------------------------------ clients
;; A client is just a port, so this reads like a file conversation.
(define (ask question)
  (let ((c (tcp-connect "127.0.0.1" port)))
    (write-string (string-append question "\n") c)
    (flush-output-port c)
    (let ((answer (read-line c)))
      (close-port c)
      answer)))

(for-each
 (lambda (question)
   (show question "->" (ask question)))
 '("hello" "is this Scheme?" "yes, over TCP"))

;;; ------------------------------------------------------- talk to itself, concurrently
;; Three clients at once, each in its own thread, collected by a wait group.
(define answers (make-atomic))
(define clients (make-waitgroup))
(for-each
 (lambda (n)
   (waitgroup-add! clients)
   (go
    (let ((answer (ask (string-append "concurrent " (number->string n)))))
      (if (and (string? answer) (> (string-length answer) 0))
          (atomic-add! answers 1)))
    (waitgroup-done! clients)))
 '(1 2 3))
(waitgroup-wait clients)
(show "concurrent clients that got an answer:" (atomic-ref answers))

;;; ------------------------------------------------------------- switch it off
;; Closing the listener makes accept raise, which ends the accept thread, so
;; go-wait can be used to reach a clean stop.
(tcp-close-listener listener)
(go-wait)
(show "connections served:" (atomic-ref served))

(newline)
(display "tcp server: end of tour") (newline)
