// SPDX-License-Identifier: MIT

package scheme

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
)

// PortError reports an I/O failure.
type PortError struct{ Msg string }

func (e *PortError) Error() string { return e.Msg }

func portErrf(format string, args ...interface{}) *PortError {
	return &PortError{Msg: fmt.Sprintf(format, args...)}
}

// Port is a Scheme port.  A port may be textual or binary, and input or
// output.  String and bytevector ports are backed by an in-memory buffer,
// file ports by the corresponding OS file.
type Port struct {
	// mu serialises access so that several interpreter threads can share a
	// port without corrupting its buffer.
	mu      sync.Mutex
	Name    string
	IsInput bool
	IsOut   bool
	Binary  bool
	closed  bool

	rd     *bufio.Reader
	wr     io.Writer
	buf    *bytes.Buffer // backing store for string / bytevector ports
	outBuf *bytes.Buffer // output string / bytevector ports
	closer io.Closer

	pushedR []rune
	pushedB []byte

	Line int
}

// NewPortFromFile wraps an io.Reader / io.Writer as a port.
func NewPortFromFile(name string, f interface{}, input bool, textual bool) *Port {
	p := &Port{Name: name, Line: 1}
	if input {
		p.IsInput = true
		if r, ok := f.(io.Reader); ok {
			p.rd = bufio.NewReaderSize(r, 8192)
		}
	} else {
		p.IsOut = true
		if w, ok := f.(io.Writer); ok {
			p.wr = w
		}
	}
	if !textual {
		p.Binary = true
	}
	if c, ok := f.(io.Closer); ok {
		p.closer = c
	}
	return p
}

// NewPortFromStream wraps a bidirectional stream — a socket, a pipe, a child
// process — as a port that may be both read and written.  Whatever directions
// the stream supports are the directions the port has, and closing the port
// closes the stream, which is what makes a TCP connection usable with the
// ordinary Scheme input and output procedures.
func NewPortFromStream(name string, stream interface{}, textual bool) *Port {
	p := &Port{Name: name, Line: 1}
	if r, ok := stream.(io.Reader); ok {
		p.IsInput = true
		p.rd = bufio.NewReaderSize(r, 8192)
	}
	if w, ok := stream.(io.Writer); ok {
		p.IsOut = true
		p.wr = w
	}
	if !textual {
		p.Binary = true
	}
	if c, ok := stream.(io.Closer); ok {
		p.closer = c
	}
	return p
}

// NewInputStringPort builds a textual input port over a string.
func NewInputStringPort(s string) *Port {
	b := bytes.NewBufferString(s)
	return &Port{Name: "string", IsInput: true, rd: bufio.NewReader(b), buf: b, Line: 1}
}

// NewOutputStringPort builds a textual output port collecting into a buffer.
func NewOutputStringPort() *Port {
	b := &bytes.Buffer{}
	return &Port{Name: "string", IsOut: true, outBuf: b, wr: b}
}

// NewInputBytevectorPort builds a binary input port over a byte slice.
func NewInputBytevectorPort(data []byte) *Port {
	b := bytes.NewBuffer(append([]byte(nil), data...))
	return &Port{Name: "bytevector", IsInput: true, Binary: true, rd: bufio.NewReader(b), buf: b, Line: 1}
}

// NewOutputBytevectorPort builds a binary output port collecting into a buffer.
func NewOutputBytevectorPort() *Port {
	b := &bytes.Buffer{}
	return &Port{Name: "bytevector", IsOut: true, Binary: true, outBuf: b, wr: b}
}

func (p *Port) kindName() string {
	switch {
	case p.Binary && p.IsInput:
		return "binary-input"
	case p.Binary:
		return "binary-output"
	case p.IsInput:
		return "input"
	default:
		return "output"
	}
}

// Close closes the port.
func (p *Port) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if p.closer != nil {
		return p.closer.Close()
	}
	return nil
}

// Flush flushes buffered output.
func (p *Port) Flush() error {
	if f, ok := p.wr.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	if s, ok := p.wr.(*bufio.Writer); ok {
		return s.Flush()
	}
	return nil
}

func (p *Port) checkOpen() error {
	if p.closed {
		return portErrf("port %s is closed", p.Name)
	}
	return nil
}

// ReadChar reads one character from a textual input port.
func (p *Port) ReadChar() (rune, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readChar()
}

func (p *Port) readChar() (rune, error) {
	if n := len(p.pushedR); n > 0 {
		ch := p.pushedR[n-1]
		p.pushedR = p.pushedR[:n-1]
		return ch, nil
	}
	if err := p.checkOpen(); err != nil {
		return 0, err
	}
	if p.rd == nil {
		return 0, io.EOF
	}
	r, _, err := p.rd.ReadRune()
	if err != nil {
		return 0, err
	}
	if r == '\n' {
		p.Line++
	}
	return r, nil
}

// UnreadChar pushes ch back onto the port.
func (p *Port) UnreadChar(ch rune) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.unreadChar(ch)
}

func (p *Port) unreadChar(ch rune) error {
	p.pushedR = append(p.pushedR, ch)
	if ch == '\n' && p.Line > 1 {
		p.Line--
	}
	return nil
}

// PeekChar returns the next character without consuming it.
func (p *Port) PeekChar() (rune, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, err := p.readChar()
	if err != nil {
		return 0, err
	}
	_ = p.unreadChar(r)
	return r, nil
}

// Ready reports whether a character is available without blocking.
func (p *Port) Ready() bool {
	if len(p.pushedR) > 0 {
		return true
	}
	if p.rd == nil {
		return true
	}
	if p.rd.Buffered() > 0 || p.closer == nil {
		return true
	}
	// A regular file never blocks: either a byte is there or the file has
	// ended, and R7RS says char-ready? is true at end of file.  A terminal, a
	// socket or a pipe can block, so nothing buffered means not ready.
	if f, ok := p.closer.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// IsClosed reports whether the port has been closed.
func (p *Port) IsClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// ReadByte reads one byte from a binary input port.
func (p *Port) ReadByte() (byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readByte()
}

func (p *Port) readByte() (byte, error) {
	if n := len(p.pushedB); n > 0 {
		b := p.pushedB[n-1]
		p.pushedB = p.pushedB[:n-1]
		return b, nil
	}
	if err := p.checkOpen(); err != nil {
		return 0, err
	}
	if p.rd == nil {
		return 0, io.EOF
	}
	b, err := p.rd.ReadByte()
	if err != nil {
		return 0, err
	}
	return b, nil
}

// PeekByte returns the next byte without consuming it.
func (p *Port) PeekByte() (byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, err := p.readByte()
	if err != nil {
		return 0, err
	}
	p.pushedB = append(p.pushedB, b)
	return b, nil
}

// PushByte pushes b back onto the port.
func (p *Port) PushByte(b byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pushedB = append(p.pushedB, b)
	return nil
}

// ReadChars reads up to n characters.
func (p *Port) ReadChars(n int) ([]rune, error) {
	var out []rune
	for i := 0; i < n; i++ {
		r, err := p.ReadChar()
		if err != nil {
			break
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		if _, err := p.PeekChar(); err != nil {
			return nil, io.EOF
		}
	}
	return out, nil
}

// LineRead reads one line (without the terminator).  It returns io.EOF when
// no characters are available at all.
func (p *Port) LineRead() ([]rune, error) {
	var out []rune
	for {
		r, err := p.ReadChar()
		if err != nil {
			if len(out) == 0 {
				return nil, io.EOF
			}
			return out, nil
		}
		if r == '\n' {
			return out, nil
		}
		if r == '\r' {
			if nx, err := p.PeekChar(); err == nil && nx == '\n' {
				p.ReadChar()
			}
			return out, nil
		}
		out = append(out, r)
	}
}

// ReadBytes reads up to n bytes.
func (p *Port) ReadBytes(n int) ([]byte, error) {
	var out []byte
	for i := 0; i < n; i++ {
		b, err := p.ReadByte()
		if err != nil {
			break
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, io.EOF
	}
	return out, nil
}

func (p *Port) WriteRune(r rune) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkOpen(); err != nil {
		return err
	}
	if p.wr == nil {
		return portErrf("not an output port")
	}
	if p.outBuf != nil {
		p.outBuf.WriteRune(r)
		return nil
	}
	if p.closer == nil {
		_, err := p.wr.Write([]byte(string(r)))
		return err
	}
	_, err := p.wr.Write([]byte(string(r)))
	return err
}

func (p *Port) WriteStr(s string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkOpen(); err != nil {
		return err
	}
	if p.wr == nil {
		return portErrf("not an output port")
	}
	_, err := io.WriteString(p.wr, s)
	return err
}

func (p *Port) WriteBytes(b []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkOpen(); err != nil {
		return err
	}
	if p.wr == nil {
		return portErrf("not an output port")
	}
	_, err := p.wr.Write(b)
	return err
}

// OutputString returns the accumulated characters of an output string port.
func (p *Port) OutputString() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.outBuf == nil {
		return ""
	}
	return p.outBuf.String()
}

// OutputBytes returns the accumulated bytes of an output bytevector port.
func (p *Port) OutputBytes() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.outBuf == nil {
		return nil
	}
	return append([]byte(nil), p.outBuf.Bytes()...)
}

// ensure *Port satisfies RuneScanner.
var _ RuneScanner = (*Port)(nil)
