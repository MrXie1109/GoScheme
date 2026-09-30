# (goscheme http)

`(goscheme http)` is a small HTTP/1.1 client and server library built on Go's `net/http` package. The client side is one procedure per request method plus a general request procedure for the full response, and the server side is http-serve, which runs a Scheme handler procedure on its own interpreter thread for every request. Import it with:

```scheme
(import (scheme base) (goscheme http))
```

## Procedures

### Client requests

| Procedure | Arguments | Description |
|---|---|---|
| `http-get` | `(http-get url [headers])` | Sends a GET to url (a string such as "http://127.0.0.1:8080/index") and returns the response body as a string; the status code is discarded, so use http-request when it matters. headers is an optional alist of ("name" . "value") string pairs. A GET carries no body, so a third argument is an arity error rather than being ignored. Raises a file-error when the request cannot be made. |
| `http-post` | `(http-post url [body [headers]])` | Sends a POST carrying body (a string, default "") and returns the response body as a string. When body is non-empty the Content-Type defaults to "text/plain; charset=utf-8"; an entry in headers overrides it. |
| `http-put` | `(http-put url [body [headers]])` | The same as http-post with the PUT method: body (a string, default "") then the optional header alist, and the response body as a string. |
| `http-delete` | `(http-delete url [body [headers]])` | Sends a DELETE and returns the response body as a string. The second argument is the body when it is a string and the header alist when it is an alist, so `(http-delete url "payload")` sends a body and `(http-delete url '())` sends none; the third argument is the header alist and is only reachable together with a string body. |
| `http-head` | `(http-head url [headers])` | Sends a HEAD and returns the response body as a string, which a well-behaved server leaves empty; headers is the usual alist. Like GET, HEAD takes no body and so no third argument. |
| `http-request` | `(http-request method url [body [headers]])` | Sends an arbitrary method (the method string is upper-cased) with body (a string, default "") and returns an http-response object holding the status, body, content type and all headers. headers is the optional alist of ("name" . "value") string pairs. |

### Responses

| Procedure | Arguments | Description |
|---|---|---|
| `http-response` | `(http-response status body [content-type])` | Builds the value a handler returns: status is an exact integer, body a string, and content-type a string defaulting to "text/plain; charset=utf-8". |
| `http-response?` | `(http-response? obj)` | #t when obj is a response object, whether built by http-response or returned by http-request, otherwise #f. |
| `http-response-status` | `(http-response-status response)` | The HTTP status code as an exact integer, e.g. 200 or 404. |
| `http-response-body` | `(http-response-body response)` | The body as a string, "" when there was none. |
| `http-response-content-type` | `(http-response-content-type response)` | The Content-Type header as a string, or "" when the response did not carry one. |
| `http-response-header` | `(http-response-header response name)` | Case-insensitive lookup of one header by name (a string): the value as a string, or #f when the header is absent. A header that appeared more than once is joined with ", ". |

### Requests handed to a handler

| Procedure | Arguments | Description |
|---|---|---|
| `http-request?` | `(http-request? obj)` | #t when obj is the request object passed to a server handler, otherwise #f. |
| `http-request-method` | `(http-request-method request)` | The method as an upper-case string, e.g. "GET" or "POST". |
| `http-request-path` | `(http-request-path request)` | The path part of the URL as a string, e.g. "/greet", without the query string. |
| `http-request-query` | `(http-request-query request)` | The raw query string as a string, without the leading "?", or "" when the URL had no query. |
| `http-request-url` | `(http-request-url request)` | The request target as the server saw it: for a server request this is the path plus query, e.g. "/greet?x=1", not an absolute URL. |
| `http-request-header` | `(http-request-header request name)` | Case-insensitive lookup of one request header by name (a string): the value as a string, or #f when it is absent. A repeated header is joined with ", ". |
| `http-request-body` | `(http-request-body request)` | The request body as a string, empty for a GET. At most 8 MiB is read; anything beyond that is discarded rather than reported as an error. |

### Servers

| Procedure | Arguments | Description |
|---|---|---|
| `http-serve` | `(http-serve port handler [host])` | Starts a server and returns it. port is an exact non-negative integer; 0 asks the operating system for a free port (read it back with http-server-port) and anything above 65535 is an error. handler is a procedure of one request; host is a string defaulting to "127.0.0.1", so the server is loopback-only unless a wider host such as "0.0.0.0" is named. |
| `http-server?` | `(http-server? obj)` | #t when obj is a server returned by http-serve, otherwise #f. |
| `http-server-port` | `(http-server-port server)` | The bound port as an exact integer; this is how a server started on port 0 learns its own address. Returns #f only if the underlying address is not a TCP address. |
| `http-server-address` | `(http-server-address server)` | The bound address as a "host:port" string, e.g. "127.0.0.1:41707". |
| `http-server-requests` | `(http-server-requests server)` | The exact integer number of requests whose handler has finished, including requests answered with a 500. |
| `http-server-close` | `(http-server-close server)` | Closes the listener and the connections active at that moment, then returns unspecified. Idempotent, and it does not wait for handlers that are already running. |
| `http-server-wait` | `(http-server-wait server)` | Blocks until http-server-close has been called on the server, polling every 20 ms, and then returns unspecified; it is what keeps a script that only serves alive. |

## Notes

* A handler may return a string, which becomes a 200 response with a "text/plain; charset=utf-8" content type, or a response object from http-response, whose status, content type and headers are used as given. Any other return value becomes a 500 whose body says the value was neither a string nor a response.
* A handler that raises an error, or that panics, becomes a 500 response whose body starts with "handler failed: "; the server and the interpreter keep running.
* Every request runs on its own child interpreter thread, entered the way (go ...) enters one, so a handler may block on I/O, a channel or a mutex without stopping the server from accepting other requests. Handlers may use the concurrency and sync libraries.
* The client procedures give up after a 30-second timeout and raise a file-error (file-error? is #t) when the connection is refused, times out, or the URL is malformed. A malformed header alist raises an ordinary error before any request is sent.
* A header alist must be a proper list of ("name" . "value") pairs of strings; anything else raises an error naming the procedure that received it.
* Header lookup is case-insensitive on both sides, and a name that arrived more than once is flattened into one comma-joined string.
* Request bodies are truncated at 8 MiB without an error. The server closes its listener and active connections on http-server-close, but that cannot interrupt a handler already in flight, and http-server-wait only polls the closed flag.
* The server is driven by the interpreter that created it, so that interpreter (and its process) must stay alive for requests to be served.
* These procedures need real network access on the loopback interface. A server binds 127.0.0.1 by default, and binding a non-loopback host may require operating-system privileges or a firewall that permits it.

## Example

Starts a server on port 0, reads back the chosen port, serves a handler that echoes the request target, fetches it with http-get, inspects the full response with http-request, then closes the server.

```scheme
(import (scheme base) (scheme write) (goscheme http))

;; Serve on port 0 and learn the port the system chose.
(define server
  (http-serve 0
    (lambda (req)
      (if (equal? (http-request-path req) "/greet")
          (http-response 200
                         (string-append "hello from " (http-request-url req))
                         "text/plain; charset=utf-8")
          (http-response 404 "not found")))))
(define port (http-server-port server))
(display "serving on 127.0.0.1:") (display port) (newline)

;; http-get returns just the body as a string.
(define body (http-get (string-append "http://127.0.0.1:" (number->string port) "/greet")))
(display "body: ") (display body) (newline)

;; http-request returns the whole response.
(define resp (http-request "GET" (string-append "http://127.0.0.1:" (number->string port) "/greet")))
(display "status: ") (display (http-response-status resp)) (newline)
(display "content-type: ") (display (http-response-header resp "Content-Type")) (newline)
(display "requests served: ") (display (http-server-requests server)) (newline)

(http-server-close server)
```
