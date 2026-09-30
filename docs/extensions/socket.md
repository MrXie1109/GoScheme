# (goscheme socket)

`(goscheme socket)` is a small TCP client and server library built directly on Go's `net` package rather than on any Scheme networking layer. Its accept and connect procedures return an ordinary bidirectional R7RS port, so a connection needs no new protocol vocabulary — `read-line`, `write-string` and the rest of the I/O procedures just work — and a listener plus `(go ...)` is a complete server. Import it with:

```scheme
(import (scheme base) (goscheme socket))
```

## Procedures

### Listeners

| Procedure | Arguments | Description |
|---|---|---|
| `tcp-listen` | `(tcp-listen port [host])` | Binds a TCP listener and returns it. port is an exact non-negative integer; 0 asks the operating system for a free port (read it back with tcp-listener-port) and anything above 65535 is an error. host is a string defaulting to "127.0.0.1", so the listener is not exposed beyond the loopback interface unless a wider host such as "0.0.0.0" is named. |
| `tcp-listener?` | `(tcp-listener? obj)` | #t when obj is a listener returned by tcp-listen, otherwise #f. |
| `tcp-listener-port` | `(tcp-listener-port listener)` | The port actually bound, as an exact integer; this is how a listener opened on port 0 learns what to connect to. Returns #f only if the underlying address is not a TCP address, which cannot happen for a listener created here. |
| `tcp-listener-address` | `(tcp-listener-address listener)` | The bound address as a "host:port" string, e.g. "127.0.0.1:44483". |
| `tcp-close-listener` | `(tcp-close-listener listener)` | Closes the listener and returns unspecified. Idempotent, and it closes only the listener: connections already accepted stay open until they are closed themselves. |

### Connections

| Procedure | Arguments | Description |
|---|---|---|
| `tcp-accept` | `(tcp-accept listener [mode])` | Blocks until a client connects, then returns the connection as a port that is both an input and an output port. mode is the symbol 'textual (the default, characters) or 'binary (bytes); any other value is an error. Raises an error if the listener is already closed. |
| `tcp-connect` | `(tcp-connect host port [mode])` | Connects to host (a string) on port (an exact non-negative integer, at most 65535) and returns the connection as a bidirectional port. mode is 'textual (the default) or 'binary. Raises a file-error if the connection is refused, and gives up after a 10-second dial timeout. |
| `tcp-address` | `(tcp-address port)` | The name of a port as a string: for a connected socket this is the address recorded when it was created (the remote "host:port" for an accepted connection, the dialed "host:port" for a connected one). It accepts any port and simply returns that port's name. |

## Notes

* Port numbers must be exact non-negative integers; a negative value or a non-integer is an error, and a value above 65535 is rejected by tcp-listen and tcp-connect.
* The optional mode argument must be the symbol 'textual or 'binary. Textual mode is the default and supports read-line, read-char, write-string and the other character procedures; binary mode marks the port as binary so read-u8, read-bytevector, write-u8 and friends are used instead. The mode is chosen independently on each side.
* tcp-accept blocks the interpreter thread it runs on; the intended pattern is to accept inside `(go ...)` (from `(goscheme channel)`) so one thread can serve while others keep running.
* A connection is an ordinary port: it satisfies input-port? and output-port? at the same time, and reading returns the end-of-file object once the peer closes it. Its internal buffer is mutex-protected, so several interpreter threads may share one connection, but interleaved writes still need external coordination.
* Closing a listener does not close its accepted connections, and closing a connection does not close its listener. A blocked tcp-accept on a closed listener raises an error, so close listeners after the serving thread has finished.
* tcp-connect raises a file-error (file-error? is #t); the listener procedures raise ordinary errors. A refused connection fails immediately, while an unreachable host hits the 10-second timeout.
* These procedures need real network access on the loopback interface. They speak TCP only — there is no UDP, TLS or Unix-domain-socket support — and binding a non-loopback host may require operating-system privileges or a firewall that permits it.

## Example

Starts a listener on port 0, asks the interpreter for the chosen port, serves one connection from a background thread, connects a client, echoes one line, and closes both ends.

```scheme
(import (scheme base) (scheme write) (goscheme channel) (goscheme socket))

;; Listen on port 0: the operating system picks a free loopback port.
(define listener (tcp-listen 0))
(define port (tcp-listener-port listener))
(display "listening on 127.0.0.1:") (display port) (newline)

;; Serve exactly one connection on its own interpreter thread.
(go (let ((conn (tcp-accept listener)))
      (write-string (read-line conn) conn)
      (newline conn)
      (close-port conn)))

;; Connect a client and exchange one line.
(define client (tcp-connect "127.0.0.1" port))
(write-string "hello over tcp" client)
(newline client)
(display "client read: ") (display (read-line client)) (newline)
(close-port client)
(tcp-close-listener listener)
```
