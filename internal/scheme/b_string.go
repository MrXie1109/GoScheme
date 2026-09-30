// SPDX-License-Identifier: MIT

package scheme

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Characters
// ---------------------------------------------------------------------------

func installChars(m *Machine) {
	m.defSimple("char?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(Char)
		return BooleanOf(ok), nil
	}, libBase, libR5RS)
	m.defSimple("char->integer", 1, 1, func(a []Value) (Value, error) {
		return Int(int64(wantChar("char->integer", a[0]))), nil
	}, libBase, libR5RS)
	m.defSimple("integer->char", 1, 1, func(a []Value) (Value, error) {
		i := wantInteger("integer->char", a[0])
		n, ok := i.Int64()
		if !ok || n < 0 || n > 0x10FFFF || (n >= 0xD800 && n <= 0xDFFF) {
			panic(errf("integer->char", "not a valid Unicode scalar value"))
		}
		return Char(rune(n)), nil
	}, libBase, libR5RS)

	charCmp := func(name string, fold bool, ok func(a, b rune) bool) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			runes := make([]rune, len(a))
			for i, v := range a {
				r := rune(wantChar(name, v))
				if fold {
					r = foldRune(r)
				}
				runes[i] = r
			}
			for i := 1; i < len(runes); i++ {
				if !ok(runes[i-1], runes[i]) {
					return False, nil
				}
			}
			return True, nil
		}
	}
	m.defSimple("char=?", 2, -1, charCmp("char=?", false, func(a, b rune) bool { return a == b }), libBase, libR5RS)
	m.defSimple("char<?", 2, -1, charCmp("char<?", false, func(a, b rune) bool { return a < b }), libBase, libR5RS)
	m.defSimple("char>?", 2, -1, charCmp("char>?", false, func(a, b rune) bool { return a > b }), libBase, libR5RS)
	m.defSimple("char<=?", 2, -1, charCmp("char<=?", false, func(a, b rune) bool { return a <= b }), libBase, libR5RS)
	m.defSimple("char>=?", 2, -1, charCmp("char>=?", false, func(a, b rune) bool { return a >= b }), libBase, libR5RS)
	m.defSimple("char-ci=?", 2, -1, charCmp("char-ci=?", true, func(a, b rune) bool { return a == b }), libChar)
	m.defSimple("char-ci<?", 2, -1, charCmp("char-ci<?", true, func(a, b rune) bool { return a < b }), libChar)
	m.defSimple("char-ci>?", 2, -1, charCmp("char-ci>?", true, func(a, b rune) bool { return a > b }), libChar)
	m.defSimple("char-ci<=?", 2, -1, charCmp("char-ci<=?", true, func(a, b rune) bool { return a <= b }), libChar)
	m.defSimple("char-ci>=?", 2, -1, charCmp("char-ci>=?", true, func(a, b rune) bool { return a >= b }), libChar)

	m.defSimple("char-alphabetic?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(unicode.IsLetter(rune(wantChar("char-alphabetic?", a[0])))), nil
	}, libBase, libChar)
	m.defSimple("char-numeric?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(unicode.IsDigit(rune(wantChar("char-numeric?", a[0])))), nil
	}, libBase, libChar)
	m.defSimple("char-whitespace?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(unicode.IsSpace(rune(wantChar("char-whitespace?", a[0])))), nil
	}, libBase, libChar)
	m.defSimple("char-upper-case?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(unicode.IsUpper(rune(wantChar("char-upper-case?", a[0])))), nil
	}, libBase, libChar)
	m.defSimple("char-lower-case?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(unicode.IsLower(rune(wantChar("char-lower-case?", a[0])))), nil
	}, libBase, libChar)
	m.defSimple("digit-value", 1, 1, func(a []Value) (Value, error) {
		r := rune(wantChar("digit-value", a[0]))
		if v, ok := decimalDigitValue(r); ok {
			return Int(int64(v)), nil
		}
		return False, nil
	}, libBase)
	m.defSimple("char-upcase", 1, 1, func(a []Value) (Value, error) {
		r := charCase(rune(wantChar("char-upcase", a[0])), caseUpper)
		return r, nil
	}, libBase, libChar)
	m.defSimple("char-downcase", 1, 1, func(a []Value) (Value, error) {
		return charCase(rune(wantChar("char-downcase", a[0])), caseLower), nil
	}, libBase, libChar)
	m.defSimple("char-foldcase", 1, 1, func(a []Value) (Value, error) {
		return charCase(rune(wantChar("char-foldcase", a[0])), caseFold), nil
	}, libBase, libChar)
}

// caseKind selects a case conversion.
type caseKind int

const (
	caseUpper caseKind = iota
	caseLower
	caseFold
)

// specialCase holds the Unicode full case mappings that differ from the
// simple one-to-one mappings (a subset of SpecialCasing.txt covering the
// mappings required by the R7RS full-unicode requirement).
var specialUpper = map[rune]string{
	0x00DF: "SS", 0x0149: "\u02BCN", 0x01F0: "J\u030C",
	0x0390: "\u0399\u0308\u0301", 0x03B0: "\u03A5\u0308\u0301",
	0x0587: "\u0535\u0552", 0x1E96: "H\u0331", 0x1E97: "T\u0308",
	0x1E98: "W\u030A", 0x1E99: "Y\u030A", 0x1E9A: "A\u02BE",
	0x1F50: "\u03A5\u0313", 0x1F52: "\u03A5\u0313\u0300",
	0x1F54: "\u03A5\u0313\u0301", 0x1F56: "\u03A5\u0313\u0342",
	0x1FB6: "\u0391\u0342", 0x1FC6: "\u0397\u0342", 0x1FD6: "\u0399\u0342",
	0x1FE6: "\u03A5\u0342", 0x1FF6: "\u03A9\u0342",
	0xFB00: "FF", 0xFB01: "FI", 0xFB02: "FL", 0xFB03: "FFI", 0xFB04: "FFL",
	0xFB05: "ST", 0xFB06: "ST",
	0xFB13: "\u0544\u0546", 0xFB14: "\u0544\u0535", 0xFB15: "\u0544\u053B",
	0xFB16: "\u054E\u0546", 0xFB17: "\u0544\u053D",
}

var specialLower = map[rune]string{
	0x0130: "i\u0307",
	0x1E9E: "\u00DF",
}

var specialFold = map[rune]string{
	0x00DF: "ss", 0x0130: "i\u0307", 0x0149: "\u02BCn", 0x017F: "s",
	0x01F0: "j\u030C", 0x0390: "\u03B9\u0308\u0301",
	0x03B0: "\u03C5\u0308\u0301", 0x0587: "\u0565\u0582",
	0x1E96: "h\u0331", 0x1E97: "t\u0308", 0x1E98: "w\u030A",
	0x1E99: "y\u030A", 0x1E9A: "a\u02BE", 0x1E9E: "ss",
	0x1F50: "\u03C5\u0313", 0x1F52: "\u03C5\u0313\u0300",
	0x1F54: "\u03C5\u0313\u0301", 0x1F56: "\u03C5\u0313\u0342",
	0x1FB6: "\u03B1\u0342", 0x1FC6: "\u03B7\u0342", 0x1FD6: "\u03B9\u0342",
	0x1FE6: "\u03C5\u0342", 0x1FF6: "\u03C9\u0342",
	0xFB00: "ff", 0xFB01: "fi", 0xFB02: "fl", 0xFB03: "ffi", 0xFB04: "ffl",
	0xFB05: "st", 0xFB06: "st",
	0xFB13: "\u0574\u0576", 0xFB14: "\u0574\u0565", 0xFB15: "\u0574\u056B",
	0xFB16: "\u057E\u0576", 0xFB17: "\u0574\u056D",
}

func foldRune(r rune) rune {
	if s, ok := specialFold[r]; ok {
		rs := []rune(s)
		if len(rs) == 1 {
			return rs[0]
		}
		return r
	}
	return unicode.ToLower(r)
}

func stringCase(s string, kind caseKind) string {
	var sb strings.Builder
	for _, r := range s {
		switch kind {
		case caseUpper:
			if repl, ok := specialUpper[r]; ok {
				sb.WriteString(repl)
				continue
			}
			sb.WriteRune(unicode.ToUpper(r))
		case caseLower:
			if repl, ok := specialLower[r]; ok {
				sb.WriteString(repl)
				continue
			}
			sb.WriteRune(unicode.ToLower(r))
		default:
			if repl, ok := specialFold[r]; ok {
				sb.WriteString(repl)
				continue
			}
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return sb.String()
}

// charCase performs a character level case conversion; multi-character
// expansions are truncated to their first character for char-upcase, which is
// what the report specifies for the character procedures.
func charCase(r rune, kind caseKind) Char {
	switch kind {
	case caseUpper:
		if repl, ok := specialUpper[r]; ok {
			rs := []rune(repl)
			if len(rs) > 0 {
				return Char(rs[0])
			}
		}
		return Char(unicode.ToUpper(r))
	case caseLower:
		if repl, ok := specialLower[r]; ok {
			rs := []rune(repl)
			if len(rs) > 0 {
				return Char(rs[0])
			}
		}
		return Char(unicode.ToLower(r))
	default:
		return Char(foldRune(r))
	}
}

// decimalDigitValue returns the numeric value of a Unicode decimal digit.
func decimalDigitValue(r rune) (int, bool) {
	if r >= '0' && r <= '9' {
		return int(r - '0'), true
	}
	for _, rt := range []*unicode.RangeTable{unicode.Nd} {
		for _, rr := range rt.R16 {
			if r >= rune(rr.Lo) && r <= rune(rr.Hi) && rr.Stride == 1 {
				return int((r - rune(rr.Lo)) % 10), true
			}
		}
		for _, rr := range rt.R32 {
			if r >= rune(rr.Lo) && r <= rune(rr.Hi) && rr.Stride == 1 {
				return int((r - rune(rr.Lo)) % 10), true
			}
		}
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Strings
// ---------------------------------------------------------------------------

func installStrings(m *Machine) {
	m.defSimple("string?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*String)
		return BooleanOf(ok), nil
	}, libBase, libR5RS)
	m.defSimple("make-string", 1, 2, func(a []Value) (Value, error) {
		n := wantIndex("make-string", a[0])
		fill := rune(0)
		if len(a) == 2 {
			fill = rune(wantChar("make-string", a[1]))
		}
		s := &String{Runes: make([]rune, n)}
		for i := range s.Runes {
			s.Runes[i] = fill
		}
		return s, nil
	}, libBase, libR5RS)
	m.defSimple("string", 0, -1, func(a []Value) (Value, error) {
		runes := make([]rune, len(a))
		for i, v := range a {
			runes[i] = rune(wantChar("string", v))
		}
		return NewStringFromRunes(runes), nil
	}, libBase, libR5RS)
	m.defSimple("string-length", 1, 1, func(a []Value) (Value, error) {
		return Int(int64(wantString("string-length", a[0]).Len())), nil
	}, libBase, libR5RS)
	m.defSimple("string-ref", 2, 2, func(a []Value) (Value, error) {
		s := wantString("string-ref", a[0])
		i := wantIndex("string-ref", a[1])
		if i >= s.Len() {
			panic(errf("string-ref", "index %d out of range for string of length %d", i, s.Len()))
		}
		return Char(s.Runes[i]), nil
	}, libBase, libR5RS)
	m.defSimple("string-set!", 3, 3, func(a []Value) (Value, error) {
		s := wantString("string-set!", a[0])
		i := wantIndex("string-set!", a[1])
		if i >= s.Len() {
			panic(errf("string-set!", "index out of range"))
		}
		s.Runes[i] = rune(wantChar("string-set!", a[2]))
		return UnspecifiedValue, nil
	}, libBase, libR5RS)

	strCmp := func(name string, fold bool, ok func(a, b string) bool) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			svals := make([]string, len(a))
			for i, v := range a {
				s := wantString(name, v).Value()
				if fold {
					s = stringCase(s, caseFold)
				}
				svals[i] = s
			}
			for i := 1; i < len(svals); i++ {
				if !ok(svals[i-1], svals[i]) {
					return False, nil
				}
			}
			return True, nil
		}
	}
	m.defSimple("string=?", 2, -1, strCmp("string=?", false, func(a, b string) bool { return a == b }), libBase, libR5RS)
	m.defSimple("string<?", 2, -1, strCmp("string<?", false, func(a, b string) bool { return a < b }), libBase, libR5RS)
	m.defSimple("string>?", 2, -1, strCmp("string>?", false, func(a, b string) bool { return a > b }), libBase, libR5RS)
	m.defSimple("string<=?", 2, -1, strCmp("string<=?", false, func(a, b string) bool { return a <= b }), libBase, libR5RS)
	m.defSimple("string>=?", 2, -1, strCmp("string>=?", false, func(a, b string) bool { return a >= b }), libBase, libR5RS)
	m.defSimple("string-ci=?", 2, -1, strCmp("string-ci=?", true, func(a, b string) bool { return a == b }), libChar)
	m.defSimple("string-ci<?", 2, -1, strCmp("string-ci<?", true, func(a, b string) bool { return a < b }), libChar)
	m.defSimple("string-ci>?", 2, -1, strCmp("string-ci>?", true, func(a, b string) bool { return a > b }), libChar)
	m.defSimple("string-ci<=?", 2, -1, strCmp("string-ci<=?", true, func(a, b string) bool { return a <= b }), libChar)
	m.defSimple("string-ci>=?", 2, -1, strCmp("string-ci>=?", true, func(a, b string) bool { return a >= b }), libChar)

	m.defSimple("substring", 2, 3, func(a []Value) (Value, error) {
		s := wantString("substring", a[0])
		start := wantIndex("substring", a[1])
		end := s.Len()
		if len(a) == 3 {
			end = wantIndex("substring", a[2])
		}
		if start > end || end > s.Len() {
			panic(errf("substring", "range [%d,%d) is invalid for a string of length %d", start, end, s.Len()))
		}
		return NewStringFromRunes(s.Runes[start:end]), nil
	}, libBase, libR5RS)
	m.defSimple("string-append", 0, -1, func(a []Value) (Value, error) {
		var sb []rune
		for _, v := range a {
			sb = append(sb, wantString("string-append", v).Runes...)
		}
		return NewStringFromRunes(sb), nil
	}, libBase, libR5RS)
	m.defSimple("string->list", 1, 3, func(a []Value) (Value, error) {
		s := wantString("string->list", a[0])
		start, end := 0, s.Len()
		if len(a) > 1 {
			start = wantIndex("string->list", a[1])
		}
		if len(a) > 2 {
			end = wantIndex("string->list", a[2])
		}
		if start > end || end > s.Len() {
			panic(errf("string->list", "invalid range"))
		}
		var items []Value
		for _, r := range s.Runes[start:end] {
			items = append(items, Char(r))
		}
		return List(items...), nil
	}, libBase, libR5RS)
	m.defSimple("list->string", 1, 1, func(a []Value) (Value, error) {
		items := wantList("list->string", a[0])
		runes := make([]rune, len(items))
		for i, v := range items {
			runes[i] = rune(wantChar("list->string", v))
		}
		return NewStringFromRunes(runes), nil
	}, libBase, libR5RS)
	m.defSimple("string-copy", 1, 3, func(a []Value) (Value, error) {
		s := wantString("string-copy", a[0])
		start, end := 0, s.Len()
		if len(a) > 1 {
			start = wantIndex("string-copy", a[1])
		}
		if len(a) > 2 {
			end = wantIndex("string-copy", a[2])
		}
		if start > end || end > s.Len() {
			panic(errf("string-copy", "invalid range"))
		}
		return NewStringFromRunes(s.Runes[start:end]), nil
	}, libBase)
	m.defSimple("string-copy!", 3, 5, func(a []Value) (Value, error) {
		to := wantString("string-copy!", a[0])
		at := wantIndex("string-copy!", a[1])
		from := wantString("string-copy!", a[2])
		start, end := 0, from.Len()
		if len(a) > 3 {
			start = wantIndex("string-copy!", a[3])
		}
		if len(a) > 4 {
			end = wantIndex("string-copy!", a[4])
		}
		if start > end || end > from.Len() || at+(end-start) > to.Len() {
			panic(errf("string-copy!", "invalid range"))
		}
		copy(to.Runes[at:], from.Runes[start:end])
		return UnspecifiedValue, nil
	}, libBase)
	m.defSimple("string-fill!", 2, 4, func(a []Value) (Value, error) {
		s := wantString("string-fill!", a[0])
		fill := rune(wantChar("string-fill!", a[1]))
		start, end := 0, s.Len()
		if len(a) > 2 {
			start = wantIndex("string-fill!", a[2])
		}
		if len(a) > 3 {
			end = wantIndex("string-fill!", a[3])
		}
		if start > end || end > s.Len() {
			panic(errf("string-fill!", "invalid range"))
		}
		for i := start; i < end; i++ {
			s.Runes[i] = fill
		}
		return UnspecifiedValue, nil
	}, libBase)
	m.defSimple("string-upcase", 1, 1, func(a []Value) (Value, error) {
		return NewString(stringCase(wantString("string-upcase", a[0]).Value(), caseUpper)), nil
	}, libBase, libChar)
	m.defSimple("string-downcase", 1, 1, func(a []Value) (Value, error) {
		return NewString(stringCase(wantString("string-downcase", a[0]).Value(), caseLower)), nil
	}, libBase, libChar)
	m.defSimple("string-foldcase", 1, 1, func(a []Value) (Value, error) {
		return NewString(stringCase(wantString("string-foldcase", a[0]).Value(), caseFold)), nil
	}, libBase, libChar)

	m.defSimple("string->vector", 1, 3, func(a []Value) (Value, error) {
		s := wantString("string->vector", a[0])
		start, end := 0, s.Len()
		if len(a) > 1 {
			start = wantIndex("string->vector", a[1])
		}
		if len(a) > 2 {
			end = wantIndex("string->vector", a[2])
		}
		if start > end || end > s.Len() {
			panic(errf("string->vector", "invalid range"))
		}
		items := make([]Value, 0, end-start)
		for _, r := range s.Runes[start:end] {
			items = append(items, Char(r))
		}
		return NewVectorFrom(items), nil
	}, libBase)
	m.defSimple("vector->string", 1, 3, func(a []Value) (Value, error) {
		v := wantVector("vector->string", a[0])
		start, end := 0, len(v.Items)
		if len(a) > 1 {
			start = wantIndex("vector->string", a[1])
		}
		if len(a) > 2 {
			end = wantIndex("vector->string", a[2])
		}
		if start > end || end > len(v.Items) {
			panic(errf("vector->string", "invalid range"))
		}
		runes := make([]rune, 0, end-start)
		for _, it := range v.Items[start:end] {
			runes = append(runes, rune(wantChar("vector->string", it)))
		}
		return NewStringFromRunes(runes), nil
	}, libBase)
	m.defSimple("string->utf8", 1, 3, func(a []Value) (Value, error) {
		s := wantString("string->utf8", a[0])
		start, end := 0, s.Len()
		if len(a) > 1 {
			start = wantIndex("string->utf8", a[1])
		}
		if len(a) > 2 {
			end = wantIndex("string->utf8", a[2])
		}
		if start > end || end > s.Len() {
			panic(errf("string->utf8", "invalid range"))
		}
		return NewBytevectorFrom([]byte(string(s.Runes[start:end]))), nil
	}, libBase)
	m.defSimple("utf8->string", 1, 3, func(a []Value) (Value, error) {
		b := wantBytevector("utf8->string", a[0])
		start, end := 0, len(b.Bytes)
		if len(a) > 1 {
			start = wantIndex("utf8->string", a[1])
		}
		if len(a) > 2 {
			end = wantIndex("utf8->string", a[2])
		}
		if start > end || end > len(b.Bytes) {
			panic(errf("utf8->string", "invalid range"))
		}
		// R7RS: it is an error for the bytes not to be valid UTF-8.  Silently
		// substituting U+FFFD, which is what a plain conversion does, hides a
		// corrupt input.
		if !utf8.Valid(b.Bytes[start:end]) {
			panic(errf("utf8->string", "bytevector is not valid UTF-8"))
		}
		return NewString(string(b.Bytes[start:end])), nil
	}, libBase)
}
