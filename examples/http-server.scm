#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; A small web service, and the client that talks to it.
;;;
;;; Run with:  goscheme examples/http-server.scm
;;;
;;; (http-serve port handler) starts a server on its own goroutine and returns
;;; it; the handler is an ordinary Scheme procedure, and net/http gives it one
;;; interpreter thread per request, exactly as (go ...) would.  Port 0 asks the
;;; operating system for a free port, and the server reports the one it got.

(import (scheme base) (scheme write)
        (goscheme http))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;;; ------------------------------------------------------------------ handler
;; A handler receives a request and returns either a string — sent as a 200
;; text/plain — or a response built with http-response when the status, the
;; content type or a header matters.
(define (handler request)
  (let ((path (http-request-path request)))
    (cond
     ((string=? path "/") (string-append "hello from GoScheme, you asked for " path))
     ((string=? path "/ping") "pong")
     ((string=? path "/version") "GoScheme 2.0")
     ((string=? path "/json")
      (http-response 200 "{\"dialect\":\"goscheme\",\"r7rs\":true}" "application/json"))
     ((string=? path "/echo") (http-request-body request))
     ((string=? path "/secret")
      ;; A handler may look at the request headers.
      (if (string=? (or (http-request-header request "x-token") "") "letmein")
          "you are in"
          (http-response 403 "no token")))
     ((string=? path "/boom") (raise 'deliberate-failure))
     (else (http-response 404 (string-append "no route for " path))))))

(define server (http-serve 0 handler))
(define base (string-append "http://127.0.0.1:" (number->string (http-server-port server))))
(show "serving on" (http-server-address server))

;;; ------------------------------------------------------------ the client
;; http-get returns the body, which is what a script usually wants.
(show "GET /          ->" (http-get (string-append base "/")))
(show "GET /ping      ->" (http-get (string-append base "/ping")))

;; http-request returns the whole response, so a status or a header is visible.
(define json (http-request "GET" (string-append base "/json")))
(show "GET /json      ->" (http-response-status json) (http-response-body json))
(show "content type   ->" (http-response-header json "content-type"))

;; A request body reaches the handler, which echoes it back.
(show "POST /echo     ->" (http-post (string-append base "/echo") "the body arrives"))

;; Headers can be sent too, as an alist.
(show "GET /secret    ->"
      (http-response-status
       (http-request "GET" (string-append base "/secret")
                     #f '(("x-token" . "letmein")))))

;; A handler that raises becomes a 500; the server keeps serving.
(show "GET /boom      ->" (http-response-status (http-request "GET" (string-append base "/boom"))))
(show "GET /nope      ->" (http-response-status (http-request "GET" (string-append base "/nope"))))
(show "GET /ping      ->" (http-get (string-append base "/ping")))

;;; ------------------------------------------------------------- switch it off
(show "requests served:" (http-server-requests server))
(http-server-close server)

;; A closed server refuses connections, which is an ordinary file error.
(show "after close    ->" (guard (e ((file-error? e) 'refused)) (http-get base)))

(newline)
(display "http server: end of tour") (newline)
