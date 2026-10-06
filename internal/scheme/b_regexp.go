// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"regexp"
)

// The (goscheme regexp) library puts Go's regexp package — RE2 — behind Scheme.
// RE2 trades backreferences and lookaround for linear-time matching, so a
// pattern from an untrusted source cannot make the interpreter hang; that is a
// good trade for a scripting language.
//
// Positions are exact integer byte offsets into the (UTF-8) string, and a
// group that did not participate in a match is reported as #f by
// regexp-match-positions, as in Racket.  In a replacement string, $1, $2, ...
// and ${name} refer to capture groups, using Go's expansion syntax.

// Regexp is a compiled regular expression.
type Regexp struct {
	re *regexp.Regexp
}

// SchemeDescribe prints the pattern; see re.Describer.
func (x *Regexp) SchemeDescribe(Printer) string {
	return fmt.Sprintf("#<regexp %s>", x.re.String())
}

func init() { registerInstaller(installRegexp) }

func installRegexp(m *Machine) {
	const lib = "(goscheme regexp)"

	// (regexp pattern) compiles pattern.  A pattern that RE2 rejects raises.
	m.defSimple("regexp", 1, 1, func(a []Value) (Value, error) {
		pat := wantString("regexp", a[0]).Value()
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, NewError("regexp: " + err.Error())
		}
		return &Regexp{re: re}, nil
	}, lib)

	m.defSimple("regexp?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Regexp)
		return BooleanOf(ok), nil
	}, lib)

	// (regexp-match pattern string) returns #f when there is no match, or a
	// list whose first element is the whole match and whose remaining elements
	// are the capture groups, as in Racket and Chicken.  A group that did not
	// participate is the empty string, matching Go's FindStringSubmatch.
	m.defSimple("regexp-match", 2, 2, func(a []Value) (Value, error) {
		re := wantRegexp("regexp-match", a[0])
		s := wantString("regexp-match", a[1]).Value()
		groups := re.re.FindStringSubmatch(s)
		if groups == nil {
			return False, nil
		}
		items := make([]Value, len(groups))
		for i, g := range groups {
			items[i] = NewString(g)
		}
		return List(items...), nil
	}, lib)

	m.defSimple("regexp-match?", 2, 2, func(a []Value) (Value, error) {
		re := wantRegexp("regexp-match?", a[0])
		s := wantString("regexp-match?", a[1]).Value()
		return BooleanOf(re.re.MatchString(s)), nil
	}, lib)

	// (regexp-match-positions pattern string) returns #f or a list of
	// (start . end) exact-integer pairs, one for the whole match and then one
	// per capture group.  A group that did not participate is #f.
	m.defSimple("regexp-match-positions", 2, 2, func(a []Value) (Value, error) {
		re := wantRegexp("regexp-match-positions", a[0])
		s := wantString("regexp-match-positions", a[1]).Value()
		ix := re.re.FindStringSubmatchIndex(s)
		if ix == nil {
			return False, nil
		}
		items := make([]Value, 0, len(ix)/2)
		for i := 0; i+1 < len(ix); i += 2 {
			if ix[i] < 0 {
				items = append(items, False)
				continue
			}
			items = append(items, Cons(Int(int64(ix[i])), Int(int64(ix[i+1]))))
		}
		return List(items...), nil
	}, lib)

	// (regexp-replace pattern string replacement) replaces the first match;
	// (regexp-replace-all ...) replaces every match.  In replacement, $1, $2,
	// ... and ${name} are the capture groups; a literal $ is written $$.
	m.defSimple("regexp-replace", 3, 3, func(a []Value) (Value, error) {
		re := wantRegexp("regexp-replace", a[0])
		s := wantString("regexp-replace", a[1]).Value()
		repl := wantString("regexp-replace", a[2]).Value()
		return NewString(regexpReplaceFirst(re.re, s, repl)), nil
	}, lib)

	m.defSimple("regexp-replace-all", 3, 3, func(a []Value) (Value, error) {
		re := wantRegexp("regexp-replace-all", a[0])
		s := wantString("regexp-replace-all", a[1]).Value()
		repl := wantString("regexp-replace-all", a[2]).Value()
		return NewString(re.re.ReplaceAllString(s, repl)), nil
	}, lib)

	// (regexp-split pattern string) returns the substrings between matches, as
	// Go's Regexp.Split does: a match at either end contributes an empty
	// string.
	m.defSimple("regexp-split", 2, 2, func(a []Value) (Value, error) {
		re := wantRegexp("regexp-split", a[0])
		s := wantString("regexp-split", a[1]).Value()
		parts := re.re.Split(s, -1)
		items := make([]Value, len(parts))
		for i, p := range parts {
			items[i] = NewString(p)
		}
		return List(items...), nil
	}, lib)
}

// wantRegexp accepts a compiled regexp or a pattern string, compiling the
// latter, so that every procedure can take "just a pattern".
func wantRegexp(name string, v Value) *Regexp {
	switch x := v.(type) {
	case *Regexp:
		return x
	case *String:
		re, err := regexp.Compile(x.Value())
		if err != nil {
			panic(NewError(name + ": " + err.Error()))
		}
		return &Regexp{re: re}
	}
	panic(errf(name, "expected a regexp or a pattern string but got %s", WriteToString(v)))
}

// regexpReplaceFirst expands repl for the first match in s and splices it in,
// leaving the text before and after the match alone.  Go's ReplaceAllString
// expands every match, so the first is done by hand with ExpandString.
func regexpReplaceFirst(re *regexp.Regexp, s, repl string) string {
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil {
		return s
	}
	var b []byte
	b = re.ExpandString(b, repl, s, loc)
	return s[:loc[0]] + string(b) + s[loc[1]:]
}
