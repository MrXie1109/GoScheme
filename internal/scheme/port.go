package scheme

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
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

// ReadRune reads one character from a textual input port.
func (p *Port) ReadRune() (rune, error) {
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

// UnreadRune pushes ch back onto the port.
func (p *Port) UnreadRune(ch rune) error {
	p.pushedR = append(p.pushedR, ch)
	if ch == '\n' && p.Line > 1 {
		p.Line--
	}
	return nil
}

// PeekRune returns the next character without consuming it.
func (p *Port) PeekRune() (rune, error) {
	r, err := p.ReadRune()
	if err != nil {
		return 0, err
	}
	_ = p.UnreadRune(r)
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
	return p.rd.Buffered() > 0 || p.closer == nil
}

// ReadByte reads one byte from a binary input port.
func (p *Port) ReadByte() (byte, error) {
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
	b, err := p.ReadByte()
	if err != nil {
		return 0, err
	}
	p.pushedB = append(p.pushedB, b)
	return b, nil
}

// UnreadByte pushes b back onto the port.
func (p *Port) UnreadByte(b byte) error {
	p.pushedB = append(p.pushedB, b)
	return nil
}

// ReadChars reads up to n characters.
func (p *Port) ReadChars(n int) ([]rune, error) {
	var out []rune
	for i := 0; i < n; i++ {
		r, err := p.ReadRune()
		if err != nil {
			break
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		if _, err := p.PeekRune(); err != nil {
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
		r, err := p.ReadRune()
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
			if nx, err := p.PeekRune(); err == nil && nx == '\n' {
				p.ReadRune()
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
	if p.outBuf == nil {
		return ""
	}
	return p.outBuf.String()
}

// OutputBytes returns the accumulated bytes of an output bytevector port.
func (p *Port) OutputBytes() []byte {
	if p.outBuf == nil {
		return nil
	}
	return append([]byte(nil), p.outBuf.Bytes()...)
}

// ensure *Port satisfies RuneScanner.
var _ RuneScanner = (*Port)(nil)

var _ = os.Stdin
