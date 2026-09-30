;;; SPDX-License-Identifier: MIT
;;; Network extension: TCP sockets, and a concurrent server built from them.

(import (scheme base) (scheme write) (scheme char) (chibi test)
        (goscheme socket) (goscheme sync) (goscheme http))

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

;; ---------------------------------------------------------------------- http
(test-begin "HTTP")

;; One route per behaviour.  The handler is an ordinary procedure and runs on
;; its own interpreter thread, one per request, so raising is contained.
(define server
  (http-serve
   0
   (lambda (req)
     (cond
      ((string=? (http-request-path req) "/json")
       (http-response 200 "{\"ok\":true}" "application/json"))
      ((string=? (http-request-path req) "/echo")
       (http-request-body req))
      ((string=? (http-request-path req) "/boom")
       (raise 'handler-failed))
      (else
       (string-append "path: " (http-request-path req)))))))

(define base
  (string-append "http://127.0.0.1:" (number->string (http-server-port server))))

(test #t (http-server? server))
(test #f (http-server? 5))
(test #t (> (http-server-port server) 0))
(test #t (string? (http-server-address server)))

;; A handler that returns a string is a 200 with that body.
(test "path: /hello" (http-get (string-append base "/hello")))

;; The whole response is there when the status or a header matters.
(define json (http-request "GET" (string-append base "/json")))
(test #t (http-response? json))
(test 200 (http-response-status json))
(test "{\"ok\":true}" (http-response-body json))
(test "application/json" (http-response-header json "content-type"))
(test "application/json" (http-response-content-type json))
(test #f (http-response-header json "x-missing"))

;; A request body reaches the handler, and the method with it.
(test "ping" (http-post (string-append base "/echo") "ping"))
(define posted (http-request "POST" (string-append base "/echo") "pong"))
(test 200 (http-response-status posted))
(test "pong" (http-response-body posted))

;; DELETE takes (url [body [headers]]): a string in the second position is the
;; body, an alist is the headers.  Both used to be read from the same argument.
(test "drop-me" (http-delete (string-append base "/echo") "drop-me"))
(test 200 (http-response-status (http-request "DELETE" (string-append base "/echo"))))
(test #t (string? (http-delete (string-append base "/hello") '())))
(test 'arity (guard (e (#t 'arity))
               ;; GET and HEAD carry no body, so they take no third argument.
               (http-get (string-append base "/hello") '() '())))

;; A query string is kept apart from the path.
(define query (http-request "GET" (string-append base "/search?q=scheme")))
(test "path: /search" (http-response-body query))

;; A handler that raises becomes a 500 instead of a dead server.
(test 500 (http-response-status (http-request "GET" (string-append base "/boom"))))

;; ... and the server is still serving afterwards.
(test "path: /after" (http-get (string-append base "/after")))

;; The counter counts what has been served, so compare it against a snapshot
;; rather than a fixed number that every new request above would change.
(define served-before (http-server-requests server))
(http-get (string-append base "/counted"))
(test (+ served-before 1) (http-server-requests server))

;; A closed server refuses connections, which is a file error like any other.
(http-server-close server)
(test 'closed (guard (e ((file-error? e) 'closed)) (http-get (string-append base "/x"))))

(test-end)

(test-end)
