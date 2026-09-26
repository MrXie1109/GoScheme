package scheme

import (
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
)

// RuneScanner is the minimal input abstraction used by the reader; *Port
// implements it for textual ports.
type RuneScanner interface {
	ReadRune() (rune, error)
	// UnreadRune pushes ch back onto the input; it must accept an arbitrary
	// number of characters so that lookahead of any depth is possible.
	UnreadRune(ch rune) error
}

// stringScanner adapts a Go string to RuneScanner.
type stringScanner struct {
	runes []rune
	pos   int
}

func newStringScanner(s string) *stringScanner {
	return &stringScanner{runes: []rune(s)}
}

func (s *stringScanner) ReadRune() (rune, error) {
	if s.pos >= len(s.runes) {
		return 0, io.EOF
	}
	r := s.runes[s.pos]
	s.pos++
	return r, nil
}

func (s *stringScanner) UnreadRune(ch rune) error {
	if s.pos == 0 {
		return fmt.Errorf("unread at start of input")
	}
	s.pos--
	return nil
}

// Reader turns characters into Scheme datums.
type Reader struct {
	src      RuneScanner
	FoldCase bool
	labels   map[int]Value
	// Source is used in error messages.
	Source string
	Line   int
}

// NewReader builds a reader over an arbitrary rune scanner.
func NewReader(src RuneScanner) *Reader {
	return &Reader{src: src, labels: map[int]Value{}, Line: 1}
}

// NewStringReader builds a reader over a string.
func NewStringReader(s string) *Reader {
	return NewReader(newStringScanner(s))
}

func (r *Reader) readRune() (rune, bool) {
	ch, err := r.src.ReadRune()
	if err != nil {
		return 0, false
	}
	if ch == '\n' {
		r.Line++
	}
	return ch, true
}

func (r *Reader) unget(ch rune) {
	if ch == 0 {
		return
	}
	if err := r.src.UnreadRune(ch); err != nil {
		return
	}
	if ch == '\n' {
		r.Line--
	}
}

func (r *Reader) peekRune() (rune, bool) {
	ch, ok := r.readRune()
	if !ok {
		return 0, false
	}
	r.unget(ch)
	return ch, true
}

func (r *Reader) errf(format string, args ...interface{}) error {
	msg := fmt.Sprintf(format, args...)
	if r.Source != "" {
		msg = fmt.Sprintf("%s:%d: %s", r.Source, r.Line, msg)
	}
	return NewReadError(msg)
}

// ReadAll reads every datum until end of input.
func (r *Reader) ReadAll() ([]Value, error) {
	var out []Value
	for {
		v, err := r.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, v)
	}
}

// Read reads one datum.  It returns io.EOF at end of input.
func (r *Reader) Read() (Value, error) {
	r.labels = map[int]Value{}
	return r.readDatum()
}

func (r *Reader) skipAtmosphere() error {
	for {
		ch, ok := r.readRune()
		if !ok {
			return io.EOF
		}
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' || ch == '\v':
			continue
		case ch == ';':
			for {
				c, ok := r.readRune()
				if !ok {
					return io.EOF
				}
				if c == '\n' {
					break
				}
			}
		case ch == '#':
			next, ok := r.peekRune()
			if !ok {
				r.unget(ch)
				return nil
			}
			switch next {
			case '|':
				r.readRune()
				if err := r.skipBlockComment(); err != nil {
					return err
				}
				continue
			case ';':
				r.readRune()
				if _, err := r.readDatum(); err != nil {
					return err
				}
				continue
			case '!':
				r.readRune()
				word, err := r.readToken()
				if err != nil {
					return err
				}
				switch word {
				case "fold-case":
					r.FoldCase = true
				case "no-fold-case":
					r.FoldCase = false
				default:
					return r.errf("unknown directive #!%s", word)
				}
				continue
			default:
				r.unget(ch)
				return nil
			}
		default:
			r.unget(ch)
			return nil
		}
	}
}

func (r *Reader) skipBlockComment() error {
	depth := 1
	for depth > 0 {
		ch, ok := r.readRune()
		if !ok {
			return r.errf("unterminated block comment")
		}
		if ch == '#' {
			if n, ok := r.peekRune(); ok && n == '|' {
				r.readRune()
				depth++
			}
		} else if ch == '|' {
			if n, ok := r.peekRune(); ok && n == '#' {
				r.readRune()
				depth--
			}
		}
	}
	return nil
}

// readDatum reads one datum, assuming atmosphere has not yet been skipped.
func (r *Reader) readDatum() (Value, error) {
	if err := r.skipAtmosphere(); err != nil {
		return nil, err
	}
	ch, ok := r.readRune()
	if !ok {
		return nil, io.EOF
	}
	switch ch {
	case '(':
		return r.readList(')')
	case '[':
		return r.readList(']')
	case ')', ']':
		return nil, r.errf("unexpected '%c'", ch)
	case '\'':
		d, err := r.readDatum()
		if err != nil {
			return nil, err
		}
		return List(Intern("quote"), d), nil
	case '`':
		d, err := r.readDatum()
		if err != nil {
			return nil, err
		}
		return List(Intern("quasiquote"), d), nil
	case ',':
		if n, ok := r.peekRune(); ok && n == '@' {
			r.readRune()
			d, err := r.readDatum()
			if err != nil {
				return nil, err
			}
			return List(Intern("unquote-splicing"), d), nil
		}
		d, err := r.readDatum()
		if err != nil {
			return nil, err
		}
		return List(Intern("unquote"), d), nil
	case '"':
		return r.readString()
	case '#':
		return r.readHash()
	case '|':
		return r.readBarSymbol()
	case '.':
		// A lone dot is only valid inside a list; a dot followed by a digit
		// or by a delimiter that is not a delimiter starts a number/symbol.
		n, ok := r.peekRune()
		if !ok || isDelimiter(n) {
			return nil, r.errf("unexpected '.'")
		}
		r.unget(ch)
		return r.readAtom()
	default:
		r.unget(ch)
		return r.readAtom()
	}
}

func (r *Reader) readList(close rune) (Value, error) {
	var head, tail *Pair
	// A leading dot is invalid; handled by readDatum returning an error.
	for {
		if err := r.skipAtmosphere(); err != nil {
			return nil, r.errf("unterminated list")
		}
		ch, ok := r.readRune()
		if !ok {
			return nil, r.errf("unterminated list")
		}
		if ch == close || (close == ')' && ch == ']') || (close == ']' && ch == ')') {
			if head == nil {
				return Nil, nil
			}
			return head, nil
		}
		if ch == '.' {
			n, ok := r.peekRune()
			if !ok || !isDelimiter(n) {
				// The dot begins an atom such as `.5` or `...`; it is an
				// ordinary element, not a dotted tail.
				r.unget(ch)
				v, err := r.readDatum()
				if err != nil {
					return nil, err
				}
				cell := &Pair{Car: v, Cdr: Nil}
				if head == nil {
					head = cell
				} else {
					tail.Cdr = cell
				}
				tail = cell
				continue
			}
			// A lone dot introduces the tail of an improper list.
			if head == nil {
				return nil, r.errf("bad dotted list")
			}
			t, err := r.readDatum()
			if err != nil {
				return nil, err
			}
			tail.Cdr = t
			if err := r.skipAtmosphere(); err != nil {
				return nil, r.errf("unterminated list")
			}
			c, ok := r.readRune()
			if !ok || (c != close && !(close == ')' && c == ']') && !(close == ']' && c == ')')) {
				return nil, r.errf("expected '%c' after dotted tail", close)
			}
			return head, nil
		}
		r.unget(ch)
		v, err := r.readDatum()
		if err != nil {
			return nil, err
		}
		cell := &Pair{Car: v, Cdr: Nil}
		if head == nil {
			head = cell
		} else {
			tail.Cdr = cell
		}
		tail = cell
	}
}

func (r *Reader) readString() (Value, error) {
	var sb []rune
	for {
		ch, ok := r.readRune()
		if !ok {
			return nil, r.errf("unterminated string")
		}
		if ch == '"' {
			return NewStringFromRunes(sb), nil
		}
		if ch != '\\' {
			sb = append(sb, ch)
			continue
		}
		e, ok := r.readRune()
		if !ok {
			return nil, r.errf("unterminated string")
		}
		switch e {
		case 'a':
			sb = append(sb, 7)
		case 'b':
			sb = append(sb, 8)
		case 't':
			sb = append(sb, '\t')
		case 'n':
			sb = append(sb, '\n')
		case 'r':
			sb = append(sb, '\r')
		case 'f':
			sb = append(sb, '\f')
		case 'v':
			sb = append(sb, '\v')
		case '0':
			sb = append(sb, 0)
		case '"':
			sb = append(sb, '"')
		case '\\':
			sb = append(sb, '\\')
		case '|':
			sb = append(sb, '|')
		case 'x', 'X':
			var hex []rune
			for {
				c, ok := r.readRune()
				if !ok {
					return nil, r.errf("bad \\x escape")
				}
				if c == ';' {
					break
				}
				hex = append(hex, c)
			}
			n, err := parseHex(string(hex))
			if err != nil {
				return nil, r.errf("bad \\x escape: %v", err)
			}
			sb = append(sb, rune(n))
		case '\n', ' ', '\t', '\r':
			// Line continuation: backslash, intraline whitespace, newline,
			// intraline whitespace -> nothing.
			if e != '\n' {
				for {
					c, ok := r.readRune()
					if !ok {
						return nil, r.errf("unterminated string")
					}
					if c == '\n' {
						break
					}
					if c != ' ' && c != '\t' && c != '\r' {
						return nil, r.errf("bad escape in string")
					}
				}
			}
			for {
				c, ok := r.peekRune()
				if !ok || (c != ' ' && c != '\t') {
					break
				}
				r.readRune()
			}
		default:
			return nil, r.errf("unknown escape \\%c", e)
		}
	}
}

func parseHex(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 16); !ok {
		return 0, fmt.Errorf("invalid hex %q", s)
	}
	if !n.IsInt64() {
		return 0, fmt.Errorf("hex escape too large")
	}
	return n.Int64(), nil
}

func (r *Reader) readHash() (Value, error) {
	ch, ok := r.readRune()
	if !ok {
		return nil, r.errf("unexpected end of input after #")
	}
	switch ch {
	case 't':
		if err := r.expectDelimiterOrWord("rue"); err != nil {
			return nil, err
		}
		return True, nil
	case 'f':
		if err := r.expectDelimiterOrWord("alse"); err != nil {
			return nil, err
		}
		return False, nil
	case 'T':
		if err := r.expectDelimiterOrWord("rue"); err != nil {
			return nil, err
		}
		return True, nil
	case 'F':
		if err := r.expectDelimiterOrWord("alse"); err != nil {
			return nil, err
		}
		return False, nil
	case '\\':
		return r.readChar()
	case '(':
		lst, err := r.readList(')')
		if err != nil {
			return nil, err
		}
		items, _ := ListToSlice(lst)
		return NewVectorFrom(items), nil
	case 'u', 'U':
		// #u8(
		word := []rune{ch}
		for {
			c, ok := r.readRune()
			if !ok {
				return nil, r.errf("unexpected end of input")
			}
			if c == '(' {
				break
			}
			word = append(word, c)
			if len(word) > 3 {
				return nil, r.errf("unknown # syntax")
			}
		}
		if strings.ToLower(string(word)) != "u8" {
			return nil, r.errf("unknown # syntax #%s(", string(word))
		}
		lst, err := r.readList(')')
		if err != nil {
			return nil, err
		}
		items, _ := ListToSlice(lst)
		bv := NewBytevector(len(items))
		for i, it := range items {
			n, ok := it.(*Integer)
			if !ok {
				return nil, r.errf("bytevector element is not an exact integer")
			}
			v, ok := n.Int64()
			if !ok || v < 0 || v > 255 {
				return nil, r.errf("bytevector element out of range")
			}
			bv.Bytes[i] = byte(v)
		}
		return bv, nil
	case '!':
		word, err := r.readToken()
		if err != nil {
			return nil, err
		}
		switch word {
		case "eof":
			return EOFObject, nil
		case "default", "fold-case":
			r.FoldCase = true
			return r.readDatum()
		case "no-fold-case":
			r.FoldCase = false
			return r.readDatum()
		case "unspecified":
			return UnspecifiedValue, nil
		}
		return nil, r.errf("unknown directive #!%s", word)
	default:
		if ch >= '0' && ch <= '9' {
			// datum label
			var digits []rune
			digits = append(digits, ch)
			for {
				c, ok := r.peekRune()
				if !ok || c < '0' || c > '9' {
					break
				}
				r.readRune()
				digits = append(digits, c)
			}
			var label int
			fmt.Sscanf(string(digits), "%d", &label)
			c, ok := r.readRune()
			if !ok {
				return nil, r.errf("unexpected end of input in datum label")
			}
			switch c {
			case '=':
				// Build a placeholder that can be patched to support cycles.
				ph := &Pair{}
				r.labels[label] = ph
				v, err := r.readDatum()
				if err != nil {
					return nil, err
				}
				if p, ok := v.(*Pair); ok {
					*ph = *p
					return ph, nil
				}
				if _, isVec := v.(*Vector); isVec {
					ph.Car = v
					ph.Cdr = Nil
					r.labels[label] = v
					return v, nil
				}
				r.labels[label] = v
				return v, nil
			case '#':
				v, ok := r.labels[label]
				if !ok {
					return nil, r.errf("undefined datum label #%d#", label)
				}
				return v, nil
			default:
				return nil, r.errf("bad datum label syntax")
			}
		}
		// Not a # syntax we handle specially: it may be a numeric prefix
		// such as #x1f, #e1e10 or #b101.
		rest, err := r.readToken()
		if err != nil {
			return nil, err
		}
		tok := "#" + string(ch) + rest
		if n, ok := ParseNumber(tok, 10); ok {
			return n, nil
		}
		return nil, r.errf("unknown # syntax %s", tok)
	}
}

// expectDelimiterOrWord handles the #t / #true / #f / #false family: the
// suffix is optional but must be complete if present, and must be followed by
// a delimiter.
func (r *Reader) expectDelimiterOrWord(rest string) error {
	consumed := 0
	for _, want := range rest {
		c, ok := r.peekRune()
		if !ok || unicode.ToLower(c) != want {
			break
		}
		r.readRune()
		consumed++
	}
	if consumed != 0 && consumed != len(rest) {
		return r.errf("bad # syntax")
	}
	if c, ok := r.peekRune(); ok && !isDelimiter(c) {
		return r.errf("bad # syntax")
	}
	return nil
}

func (r *Reader) readChar() (Value, error) {
	ch, ok := r.readRune()
	if !ok {
		return nil, r.errf("unexpected end of input after #\\")
	}
	// Read the whole token.
	var sb []rune
	sb = append(sb, ch)
	for {
		c, ok := r.peekRune()
		if !ok || isDelimiter(c) {
			break
		}
		r.readRune()
		sb = append(sb, c)
	}
	name := string(sb)
	if len(sb) == 1 {
		return Char(ch), nil
	}
	lower := strings.ToLower(name)
	switch lower {
	case "alarm":
		return Char(7), nil
	case "backspace":
		return Char(8), nil
	case "tab":
		return Char('\t'), nil
	case "newline", "linefeed", "nel":
		return Char('\n'), nil
	case "vtab":
		return Char('\v'), nil
	case "page":
		return Char('\f'), nil
	case "return":
		return Char('\r'), nil
	case "esc", "escape":
		return Char(27), nil
	case "space":
		return Char(' '), nil
	case "delete", "rubout", "del":
		return Char(127), nil
	case "null", "nul":
		return Char(0), nil
	}
	if (sb[0] == 'x' || sb[0] == 'X') && len(sb) > 1 {
		n, err := parseHex(string(sb[1:]))
		if err != nil {
			return nil, r.errf("bad character literal #\\%s", name)
		}
		return Char(rune(n)), nil
	}
	if len(sb) == 1 {
		return Char(ch), nil
	}
	return nil, r.errf("unknown character name #\\%s", name)
}

func (r *Reader) readBarSymbol() (Value, error) {
	var sb []rune
	for {
		ch, ok := r.readRune()
		if !ok {
			return nil, r.errf("unterminated |symbol|")
		}
		if ch == '|' {
			return Intern(string(sb)), nil
		}
		if ch == '\\' {
			e, ok := r.readRune()
			if !ok {
				return nil, r.errf("unterminated |symbol|")
			}
			switch e {
			case 'a':
				sb = append(sb, 7)
			case 'b':
				sb = append(sb, 8)
			case 't':
				sb = append(sb, '\t')
			case 'n':
				sb = append(sb, '\n')
			case 'r':
				sb = append(sb, '\r')
			case '|':
				sb = append(sb, '|')
			case '\\':
				sb = append(sb, '\\')
			case 'x', 'X':
				var hex []rune
				for {
					c, ok := r.readRune()
					if !ok || c == ';' {
						break
					}
					hex = append(hex, c)
				}
				n, err := parseHex(string(hex))
				if err != nil {
					return nil, r.errf("bad \\x escape in symbol")
				}
				sb = append(sb, rune(n))
			default:
				sb = append(sb, e)
			}
			continue
		}
		sb = append(sb, ch)
	}
}

func (r *Reader) readToken() (string, error) {
	var sb []rune
	for {
		c, ok := r.peekRune()
		if !ok || isDelimiter(c) {
			break
		}
		r.readRune()
		sb = append(sb, c)
	}
	return string(sb), nil
}

// readAtom reads a number or an identifier.
func (r *Reader) readAtom() (Value, error) {
	ch, ok := r.peekRune()
	if !ok {
		return nil, io.EOF
	}
	if ch == '|' {
		r.readRune()
		return r.readBarSymbol()
	}
	tok, err := r.readToken()
	if err != nil {
		return nil, err
	}
	if tok == "" {
		c, _ := r.readRune()
		return nil, r.errf("unexpected character %q", string(c))
	}
	if n, ok := ParseNumber(tok, 10); ok {
		return n, nil
	}
	if r.FoldCase {
		tok = strings.ToLower(tok)
	}
	return Intern(tok), nil
}

func isDelimiter(ch rune) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '\f', '\v', '(', ')', '[', ']', '"', ';', '\'', '`', ',':
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Number syntax
// ---------------------------------------------------------------------------

// ParseNumber parses a Scheme numeric literal.  radix is the default radix
// used when the literal carries no radix prefix.
func ParseNumber(s string, radix int) (Value, bool) {
	if s == "" {
		return nil, false
	}
	// A lone + or - is an identifier, not a number.
	if s == "+" || s == "-" || s == "..." {
		return nil, false
	}
	exact := byte(0) // 0 unknown, 'e' exact, 'i' inexact
	i := 0
	for i+1 < len(s) && s[i] == '#' {
		switch s[i+1] {
		case 'b', 'B':
			radix = 2
		case 'o', 'O':
			radix = 8
		case 'd', 'D':
			radix = 10
		case 'x', 'X':
			radix = 16
		case 'e', 'E':
			exact = 'e'
		case 'i', 'I':
			exact = 'i'
		default:
			return nil, false
		}
		i += 2
	}
	body := s[i:]
	if body == "" {
		return nil, false
	}
	// Polar notation a@b
	if idx := strings.IndexByte(body, '@'); idx >= 0 {
		re, ok1 := parseReal(body[:idx], radix, exact)
		im, ok2 := parseReal(body[idx+1:], radix, exact)
		if !ok1 || !ok2 {
			return nil, false
		}
		if exact == 'e' {
			re, im = Exact(re), Exact(im)
		}
		mag := asFloat(re)
		ang := asFloat(im)
		v := NormalizeComplex(Float(mag*math.Cos(ang)), Float(mag*math.Sin(ang)))
		if exact == 'e' {
			v = Exact(v)
		}
		return v, true
	}
	// Complex with trailing i
	if strings.HasSuffix(body, "i") || strings.HasSuffix(body, "I") {
		inner := body[:len(body)-1]
		split := -1
		for j := 1; j < len(inner); j++ {
			c := inner[j]
			if (c == '+' || c == '-') && !isExponentMarker(inner[j-1]) {
				split = j
			}
		}
		// A bare "i" is an identifier; the imaginary part must be introduced
		// by a sign, either as in "1+2i" or as in "+2i".
		signed := len(inner) > 0 && (inner[0] == '+' || inner[0] == '-')
		if signed || split > 0 {
			var im Value
			imStr := inner
			if split > 0 {
				imStr = inner[split:]
			}
			switch imStr {
			case "+", "":
				im = Int(1)
			case "-":
				im = Int(-1)
			default:
				v, ok := parseReal(imStr, radix, exact)
				if !ok {
					return nil, false
				}
				im = v
			}
			if exact == 'e' && IsInexact(im) {
				im = Exact(im)
			}
			var re Value = Int(0)
			if split > 0 {
				v, ok := parseReal(inner[:split], radix, exact)
				if !ok {
					return nil, false
				}
				re = v
			} else if exact == 'e' {
				re = Int(0)
			}
			return NormalizeComplex(re, im), true
		}
		return nil, false
	}
	// Plain real
	v, ok := parseReal(body, radix, exact)
	if !ok {
		return nil, false
	}
	return v, true
}

func parseReal(s string, radix int, exact byte) (Value, bool) {
	if s == "" {
		return nil, false
	}
	switch strings.ToLower(s) {
	case "+inf.0":
		if radix != 10 {
			return nil, false
		}
		return Float(math.Inf(1)), true
	case "-inf.0":
		if radix != 10 {
			return nil, false
		}
		return Float(math.Inf(-1)), true
	case "+nan.0", "-nan.0":
		if radix != 10 {
			return nil, false
		}
		return Float(math.NaN()), true
	}
	sign := 1
	body := s
	if body[0] == '+' {
		body = body[1:]
	} else if body[0] == '-' {
		sign = -1
		body = body[1:]
	}
	if body == "" {
		return nil, false
	}
	if idx := strings.IndexByte(body, '/'); idx >= 0 {
		numStr, denStr := body[:idx], body[idx+1:]
		num, ok1 := parseUInteger(numStr, radix)
		den, ok2 := parseUInteger(denStr, radix)
		if !ok1 || !ok2 {
			return nil, false
		}
		if den.Sign() == 0 {
			return nil, false
		}
		if sign < 0 {
			num.Neg(num)
		}
		r := new(big.Rat).SetFrac(num, den)
		if exact == 'i' {
			f, _ := r.Float64()
			return Float(f), true
		}
		return normRat(r), true
	}
	// Decimal?
	if radix == 10 && strings.ContainsAny(body, ".eEsSfFdDlL") {
		f, err := parseDecimal(body)
		if err != nil {
			return nil, false
		}
		if sign < 0 {
			f = -f
		}
		if exact == 'e' {
			return Exact(Float(f)), true
		}
		return Float(f), true
	}
	n, ok := parseUInteger(body, radix)
	if !ok {
		return nil, false
	}
	if sign < 0 {
		n.Neg(n)
	}
	if exact == 'i' {
		f, _ := new(big.Float).SetInt(n).Float64()
		return Float(f), true
	}
	return BigInt(n), true
}

func isExponentMarker(c byte) bool {
	switch c {
	case 'e', 'E', 's', 'S', 'f', 'F', 'd', 'D', 'l', 'L':
		return true
	}
	return false
}

func parseUInteger(s string, radix int) (*big.Int, bool) {
	if s == "" {
		return nil, false
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, radix); !ok {
		return nil, false
	}
	return n, true
}

// parseDecimal parses a decimal literal without sign: digits with optional
// '.', '.', and an exponent marker.
func parseDecimal(s string) (float64, error) {
	// Validate the shape ourselves so that things like "1.2.3" or "1e" are
	// rejected rather than silently accepted by strconv.
	mant := s
	exp := ""
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 'e', 'E', 's', 'S', 'f', 'F', 'd', 'D', 'l', 'L':
			if i > 0 {
				mant, exp = s[:i], s[i+1:]
			}
		}
		if exp != "" {
			break
		}
	}
	digits := 0
	dots := 0
	for _, c := range mant {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			dots++
			if dots > 1 {
				return 0, fmt.Errorf("bad decimal")
			}
		default:
			return 0, fmt.Errorf("bad decimal")
		}
	}
	if digits == 0 || mant == "." {
		return 0, fmt.Errorf("bad decimal")
	}
	if exp != "" {
		i := 0
		if exp[0] == '+' || exp[0] == '-' {
			i++
		}
		if i >= len(exp) {
			return 0, fmt.Errorf("bad exponent")
		}
		for ; i < len(exp); i++ {
			if exp[i] < '0' || exp[i] > '9' {
				return 0, fmt.Errorf("bad exponent")
			}
		}
	}
	// Go only understands `e` as the exponent marker; normalise the others.
	norm := mant
	if exp != "" {
		norm = mant + "e" + exp
	}
	f, err := strconv.ParseFloat(norm, 64)
	if err != nil {
		return 0, err
	}
	return f, nil
}
