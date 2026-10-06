// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
	"math/big"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// sortRunes sorts in place, the way string-sort wants it.
func sortRunes(runes []rune) {
	sort.SliceStable(runes, func(i, j int) bool { return runes[i] < runes[j] })
}

// The text half of (goscheme fast): the string procedures a program ends up
// writing by hand, each one a single Go pass over the runes.

// wantCharOrString accepts the thing to search for: a character, or a string of
// any length.
func wantCharOrString(name string, v Value) string {
	switch x := v.(type) {
	case Char:
		return string(rune(x))
	case *String:
		return x.Value()
	}
	panic(errf(name, "expected a character or a string but got %s", WriteToString(v)))
}

// runeIndexFromByte turns a byte offset in s into a character index.
func runeIndexFromByte(s string, byteOffset int) int {
	return utf8.RuneCountInString(s[:byteOffset])
}

func installFastText(m *Machine, lib string) {
	m.defSimple("string-reverse", 1, 1, func(a []Value) (Value, error) {
		runes := []rune(wantString("string-reverse", a[0]).Value())
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return NewStringFromRunes(runes), nil
	}, lib)

	// (string-repeat string count) repeats the whole string.
	m.defSimple("string-repeat", 2, 2, func(a []Value) (Value, error) {
		s := wantString("string-repeat", a[0]).Value()
		n := wantIndex("string-repeat", a[1])
		return NewString(strings.Repeat(s, n)), nil
	}, lib)

	// (string-count string char-or-substring) counts non-overlapping
	// occurrences.
	m.defSimple("string-count", 2, 2, func(a []Value) (Value, error) {
		s := wantString("string-count", a[0]).Value()
		needle := wantCharOrString("string-count", a[1])
		return Int(int64(strings.Count(s, needle))), nil
	}, lib)

	// (string-last-index string char-or-substring) is the last index, or #f.
	m.defSimple("string-last-index", 2, 2, func(a []Value) (Value, error) {
		s := wantString("string-last-index", a[0]).Value()
		needle := wantCharOrString("string-last-index", a[1])
		at := strings.LastIndex(s, needle)
		if at < 0 {
			return False, nil
		}
		return Int(int64(runeIndexFromByte(s, at))), nil
	}, lib)

	// (string-index-from string char-or-substring start) searches from a
	// character index onwards.
	m.defSimple("string-index-from", 3, 3, func(a []Value) (Value, error) {
		s := wantString("string-index-from", a[0]).Value()
		needle := wantCharOrString("string-index-from", a[1])
		start := wantIndex("string-index-from", a[2])
		runes := []rune(s)
		if start > len(runes) {
			return False, nil
		}
		from := len(string(runes[:start]))
		at := strings.Index(s[from:], needle)
		if at < 0 {
			return False, nil
		}
		return Int(int64(start + runeIndexFromByte(s[from:from+at], 0))), nil
	}, lib)

	// (string-find-all string substring) is every non-overlapping index.
	m.defSimple("string-find-all", 2, 2, func(a []Value) (Value, error) {
		s := wantString("string-find-all", a[0]).Value()
		needle := wantCharOrString("string-find-all", a[1])
		var out []Value
		if needle == "" {
			return Nil, nil
		}
		from := 0
		for {
			at := strings.Index(s[from:], needle)
			if at < 0 {
				break
			}
			byteAt := from + at
			out = append(out, Int(int64(runeIndexFromByte(s, byteAt))))
			from = byteAt + len(needle)
			if from > len(s) {
				break
			}
		}
		return listOf(out), nil
	}, lib)

	// (string-fields string) splits on runs of whitespace, dropping the empty
	// pieces; (string-lines string) splits on newlines, tolerating CRLF.
	m.defSimple("string-fields", 1, 1, func(a []Value) (Value, error) {
		fields := strings.Fields(wantString("string-fields", a[0]).Value())
		out := make([]Value, len(fields))
		for i, f := range fields {
			out[i] = NewString(f)
		}
		return listOf(out), nil
	}, lib)

	m.defSimple("string-lines", 1, 1, func(a []Value) (Value, error) {
		s := wantString("string-lines", a[0]).Value()
		parts := strings.Split(s, "\n")
		if len(parts) > 1 && parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
		out := make([]Value, len(parts))
		for i, p := range parts {
			out[i] = NewString(strings.TrimSuffix(p, "\r"))
		}
		return listOf(out), nil
	}, lib)

	// (string-chunk string n) cuts it into pieces of n characters.
	m.defSimple("string-chunk", 2, 2, func(a []Value) (Value, error) {
		runes := []rune(wantString("string-chunk", a[0]).Value())
		n := wantIndex("string-chunk", a[1])
		if n < 1 {
			panic(errf("string-chunk", "the chunk size must be at least 1"))
		}
		var out []Value
		for i := 0; i < len(runes); i += n {
			j := i + n
			if j > len(runes) {
				j = len(runes)
			}
			out = append(out, NewStringFromRunes(runes[i:j]))
		}
		return listOf(out), nil
	}, lib)

	// (string-sort string) sorts the characters; the Go sort is stable, so
	// equal characters keep their relative order.
	m.defSimple("string-sort", 1, 1, func(a []Value) (Value, error) {
		runes := []rune(wantString("string-sort", a[0]).Value())
		sortRunes(runes)
		return NewStringFromRunes(runes), nil
	}, lib)

	// (string-take string n) and (string-drop string n) count characters, not
	// bytes.
	m.defSimple("string-take", 2, 2, func(a []Value) (Value, error) {
		runes := []rune(wantString("string-take", a[0]).Value())
		n := wantIndex("string-take", a[1])
		if n > len(runes) {
			panic(errf("string-take", "the string has only %d characters", len(runes)))
		}
		return NewStringFromRunes(runes[:n]), nil
	}, lib)

	m.defSimple("string-drop", 2, 2, func(a []Value) (Value, error) {
		runes := []rune(wantString("string-drop", a[0]).Value())
		n := wantIndex("string-drop", a[1])
		if n > len(runes) {
			panic(errf("string-drop", "the string has only %d characters", len(runes)))
		}
		return NewStringFromRunes(runes[n:]), nil
	}, lib)

	// (string-pad-center string width [char]) centres the text, putting the
	// extra character on the right.
	m.defSimple("string-pad-center", 2, 3, func(a []Value) (Value, error) {
		runes := []rune(wantString("string-pad-center", a[0]).Value())
		width := wantIndex("string-pad-center", a[1])
		pad := ' '
		if len(a) == 3 {
			pad = rune(wantChar("string-pad-center", a[2]))
		}
		if len(runes) >= width {
			return NewStringFromRunes(runes), nil
		}
		missing := width - len(runes)
		left := missing / 2
		return NewString(strings.Repeat(string(pad), left) + string(runes) +
			strings.Repeat(string(pad), missing-left)), nil
	}, lib)

	// (string-upper string) and (string-lower string) are Unicode-aware case
	// conversion.  R7RS puts string-upcase and string-downcase in
	// (scheme char); these are the same job without that import.
	m.defSimple("string-upper", 1, 1, func(a []Value) (Value, error) {
		return NewString(strings.ToUpper(wantString("string-upper", a[0]).Value())), nil
	}, lib)

	m.defSimple("string-lower", 1, 1, func(a []Value) (Value, error) {
		return NewString(strings.ToLower(wantString("string-lower", a[0]).Value())), nil
	}, lib)

	// (string-titlecase string) upper-cases the first letter of every word and
	// lower-cases the rest.
	m.defSimple("string-titlecase", 1, 1, func(a []Value) (Value, error) {
		runes := []rune(wantString("string-titlecase", a[0]).Value())
		out := make([]rune, len(runes))
		atWordStart := true
		for i, r := range runes {
			switch {
			case atWordStart && unicode.IsLetter(r):
				out[i] = unicode.ToUpper(r)
				atWordStart = false
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				out[i] = unicode.ToLower(r)
				atWordStart = false
			default:
				out[i] = r
				atWordStart = true
			}
		}
		return NewStringFromRunes(out), nil
	}, lib)

	// (string-blank? string) is true for the empty string and for one that is
	// all whitespace.
	m.defSimple("string-blank?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(strings.TrimSpace(wantString("string-blank?", a[0]).Value()) == ""), nil
	}, lib)

	m.defSimple("string-empty?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(wantString("string-empty?", a[0]).Value() == ""), nil
	}, lib)

	// (string-prefix-ci? string prefix) and (string-suffix-ci? string suffix)
	// ignore case.
	m.defSimple("string-prefix-ci?", 2, 2, func(a []Value) (Value, error) {
		s := strings.ToLower(wantString("string-prefix-ci?", a[0]).Value())
		p := strings.ToLower(wantString("string-prefix-ci?", a[1]).Value())
		return BooleanOf(strings.HasPrefix(s, p)), nil
	}, lib)

	m.defSimple("string-suffix-ci?", 2, 2, func(a []Value) (Value, error) {
		s := strings.ToLower(wantString("string-suffix-ci?", a[0]).Value())
		p := strings.ToLower(wantString("string-suffix-ci?", a[1]).Value())
		return BooleanOf(strings.HasSuffix(s, p)), nil
	}, lib)

	// (string-replace-first string from to) replaces one occurrence;
	// string-replace replaces them all.
	m.defSimple("string-replace-first", 3, 3, func(a []Value) (Value, error) {
		s := wantString("string-replace-first", a[0]).Value()
		from := wantString("string-replace-first", a[1]).Value()
		to := wantString("string-replace-first", a[2]).Value()
		if from == "" {
			panic(errf("string-replace-first", "the string to replace must not be empty"))
		}
		return NewString(strings.Replace(s, from, to, 1)), nil
	}, lib)

	// (string-chomp string) removes one trailing newline, CRLF included.
	m.defSimple("string-chomp", 1, 1, func(a []Value) (Value, error) {
		s := wantString("string-chomp", a[0]).Value()
		s = strings.TrimSuffix(s, "\n")
		s = strings.TrimSuffix(s, "\r")
		return NewString(s), nil
	}, lib)

	// (string-integer? string [radix]) asks whether the whole string is an
	// integer literal, which string->number answers with a value instead.
	m.defSimple("string-integer?", 1, 2, func(a []Value) (Value, error) {
		s := wantString("string-integer?", a[0]).Value()
		radix := 10
		if len(a) == 2 {
			radix = wantIndex("string-integer?", a[1])
			if radix < 2 || radix > 36 {
				panic(errf("string-integer?", "the radix must be between 2 and 36"))
			}
		}
		if s == "" {
			return False, nil
		}
		_, ok := new(big.Int).SetString(s, radix)
		return BooleanOf(ok), nil
	}, lib)

	// (string-byte-length string) is the UTF-8 length, where string-length
	// counts characters.
	m.defSimple("string-byte-length", 1, 1, func(a []Value) (Value, error) {
		return Int(int64(len(wantString("string-byte-length", a[0]).Value()))), nil
	}, lib)
}
