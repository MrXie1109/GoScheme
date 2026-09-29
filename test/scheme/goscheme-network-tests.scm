;;; SPDX-License-Identifier: MIT
;;; Network extension: TCP sockets, and a concurrent server built from them.

(import (scheme base) (scheme write) (scheme char) (chibi test)
        (goscheme socket) (goscheme sync))

(test-begin "Network")

;; ------------------------------------------------------------------- sockets
(test-begin "TCP")

;; A listener on an ephemeral port.  The client connects first and accept is
;; called afterwards, so the test does not race: the connection is already
;; queued when accept runs.
(define listener (tcp-listen 0))
(test #t (tcp-listener? listener))
(test #f (tcp-listener? 5))
(define port (tcp-listener-port listener))
(test #t (and (exact? port) (> port 0)))
(test #t (string? (tcp-listener-address listener)))

(define client (tcp-connect "127.0.0.1" port))
(define server (tcp-accept listener))

;; A connection is a port, in both directions, so the ordinary I/O procedures
;; work on it with no new vocabulary.
(test #t (port? server))
(test #t (input-port? server))
(test #t (output-port? server))
(test #t (string? (tcp-address client)))

(write-string "hello\n" client)
(flush-output-port client)
(test "hello" (read-line server))

(write-string "world\n" server)
(flush-output-port server)
(test "world" (read-line client))

;; Closing one end shows up as end of file at the other.
(close-port client)
(test #t (eof-object? (read-line server)))
(close-port server)

;; A closed listener reports an error instead of blocking forever.
(tcp-close-listener listener)
(test 'closed (guard (e (#t 'closed)) (tcp-accept listener)))

;; A port nobody is listening on is a file error, not a hang.
(test 'refused (guard (e ((file-error? e) 'refused)) (tcp-connect "127.0.0.1" 1)))

;; 'binary asks for a binary port, so the byte procedures work.
(define bin-listener (tcp-listen 0))
(define bin-client (tcp-connect "127.0.0.1" (tcp-listener-port bin-listener) 'binary))
(define bin-server (tcp-accept bin-listener 'binary))
(test #t (binary-port? bin-server))
(test #f (binary-port? server))
(write-u8 65 bin-client)
(flush-output-port bin-client)
(test 65 (read-u8 bin-server))
(close-port bin-client)
(close-port bin-server)
(tcp-close-listener bin-listener)

(test-end)

;; --------------------------------------------------------- a concurrent server
(test-begin "Concurrent server")

;; The shape a Go programmer would recognise: one thread accepts, one thread
;; per connection greets the client and hangs up.  Closing the listener makes
;; accept raise, which ends the accept thread, so go-wait can be used to reach
;; a deterministic end.
(define listener (tcp-listen 0))
(define served (make-atomic))
(define handlers (make-waitgroup))

(go
 (guard (e (#t 'listener-closed))
   (let loop ()
     (let ((conn (tcp-accept listener)))
       (waitgroup-add! handlers)
       (go
        (guard (e (#t 'client-gone))
          (atomic-add! served 1)
          (write-string "hi\n" conn)
          (flush-output-port conn)
          (close-port conn))
        (waitgroup-done! handlers))
       (loop)))))

(define (ask)
  (let ((c (tcp-connect "127.0.0.1" (tcp-listener-port listener))))
    (let ((answer (read-line c)))
      (close-port c)
      answer)))

(test '("hi" "hi" "hi") (list (ask) (ask) (ask)))

(tcp-close-listener listener)
(go-wait)
(test 3 (atomic-ref served))
(test 0 (waitgroup-count handlers))

(test-end)

(test-end)
