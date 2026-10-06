// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"net"
	"strconv"
	"sync"
	"time"
)

// The (goscheme socket) library is what makes the concurrency extension worth
// having: a connection is an ordinary port, so read-line, write-string and the
// rest of the R7RS I/O procedures work on a socket with no new vocabulary, and
// (go ...) turns a listener into a server.

// TcpListener is a listening TCP socket.
type TcpListener struct {
	mu     sync.Mutex
	ln     net.Listener
	closed bool
}

// SchemeDescribe prints the listener; see re.Describer.
func (t *TcpListener) SchemeDescribe(Printer) string {
	return fmt.Sprintf("#<tcp-listener %s>", t.ln.Addr().String())
}

// dialTimeout bounds (tcp-connect).  Without it a connection attempt to a host
// that is not answering would hang for the operating system's own timeout,
// which is measured in minutes.
const dialTimeout = 10 * time.Second

func installSockets(m *Machine) {
	const lib = "(goscheme socket)"

	// (tcp-listen port [host]) listens on port, which may be 0 to let the
	// operating system pick one; (tcp-listener-port) then reports it.  The host
	// defaults to the loopback address, so a server is not exposed to the
	// network unless it asks to be.
	m.def("tcp-listen", 1, 2, func(m *Machine, a []Value) {
		port := wantIndex("tcp-listen", a[0])
		if port > 65535 {
			m.Raise(NewError("tcp-listen: port out of range: " + WriteToString(a[0])))
			return
		}
		host := "127.0.0.1"
		if len(a) == 2 {
			host = wantString("tcp-listen", a[1]).Value()
		}
		address := net.JoinHostPort(host, strconv.Itoa(port))
		ln, err := net.Listen("tcp", address)
		if err != nil {
			m.Raise(NewError("tcp-listen: " + err.Error()))
			return
		}
		m.Return(&TcpListener{ln: ln})
	}, lib)

	m.defSimple("tcp-listener?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*TcpListener)
		return BooleanOf(ok), nil
	}, lib)

	// The port actually bound, which is how a caller of (tcp-listen 0) learns
	// what to connect to.
	m.defSimple("tcp-listener-port", 1, 1, func(a []Value) (Value, error) {
		l := wantTcpListener("tcp-listener-port", a[0])
		if addr, ok := l.ln.Addr().(*net.TCPAddr); ok {
			return Int(int64(addr.Port)), nil
		}
		return False, nil
	}, lib)

	m.defSimple("tcp-listener-address", 1, 1, func(a []Value) (Value, error) {
		l := wantTcpListener("tcp-listener-address", a[0])
		return NewString(l.ln.Addr().String()), nil
	}, lib)

	// (tcp-accept listener [mode]) waits for a client and returns the
	// connection as a port that is both an input and an output port.  A TCP
	// connection carries no idea of text or bytes — that is the reader's
	// decision — so the accepting side names the mode, as the connecting side
	// does.
	m.def("tcp-accept", 1, 2, func(m *Machine, a []Value) {
		l := wantTcpListener("tcp-accept", a[0])
		textual := true
		if len(a) == 2 {
			textual = wantTextualMode("tcp-accept", a[1])
		}
		conn, err := l.ln.Accept()
		if err != nil {
			m.Raise(NewError("tcp-accept: " + err.Error()))
			return
		}
		m.Return(NewPortFromStream(conn.RemoteAddr().String(), conn, textual))
	}, lib)

	// (tcp-connect host port [mode]) connects to a server and returns the
	// connection as a port.  mode is 'textual (the default) or 'binary.
	m.def("tcp-connect", 2, 3, func(m *Machine, a []Value) {
		host := wantString("tcp-connect", a[0]).Value()
		port := wantIndex("tcp-connect", a[1])
		if port > 65535 {
			m.Raise(NewError("tcp-connect: port out of range: " + WriteToString(a[1])))
			return
		}
		textual := true
		if len(a) == 3 {
			textual = wantTextualMode("tcp-connect", a[2])
		}
		address := net.JoinHostPort(host, strconv.Itoa(port))
		conn, err := net.DialTimeout("tcp", address, dialTimeout)
		if err != nil {
			m.Raise(NewFileError("tcp-connect: "+err.Error(), NewString(address)))
			return
		}
		m.Return(NewPortFromStream(address, conn, textual))
	}, lib)

	// The address of a connection, which is also its port name.
	m.defSimple("tcp-address", 1, 1, func(a []Value) (Value, error) {
		p := wantPort("tcp-address", a[0])
		return NewString(p.Name), nil
	}, lib)

	m.defSimple("tcp-close-listener", 1, 1, func(a []Value) (Value, error) {
		l := wantTcpListener("tcp-close-listener", a[0])
		l.mu.Lock()
		defer l.mu.Unlock()
		if !l.closed {
			l.closed = true
			l.ln.Close()
		}
		return UnspecifiedValue, nil
	}, lib)
}

// wantTcpListener checks for a listener.
func wantTcpListener(name string, v Value) *TcpListener {
	l, ok := v.(*TcpListener)
	if !ok {
		panic(errf(name, "expected a TCP listener but got %s", WriteToString(v)))
	}
	return l
}

// wantTextualMode reads the optional 'textual / 'binary argument the socket
// constructors take.
func wantTextualMode(name string, v Value) bool {
	s, ok := v.(*Symbol)
	if !ok {
		panic(errf(name, "expected 'textual or 'binary but got %s", WriteToString(v)))
	}
	switch s.Name {
	case "textual":
		return true
	case "binary":
		return false
	}
	panic(errf(name, "expected 'textual or 'binary but got %s", WriteToString(v)))
}
