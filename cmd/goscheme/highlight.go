// SPDX-License-Identifier: MIT

package main

import (
	"strings"
)

// Colouring the line as it is typed.
//
// The editor draws the line itself rather than echoing keystrokes, so it can
// draw it in pieces.  This file decides what colour each piece should be; the
// editor writes the escape sequences and, crucially, keeps counting columns
// from the *plain* text, because an escape sequence occupies no columns and
// counting it would put the cursor in the wrong place.
//
// Three things are coloured:
//
//   - the names of things the interpreter provides, so that a name is visibly a
//     name and a typo is visibly not one;
//   - string and character literals, so that text inside them is not mistaken
//     for code;
//   - a pair of parentheses, when the cursor is on one of them, so that the
//     matching bracket is found by looking rather than by counting.
//
// Colour is only used when the terminal can be expected to understand it: see
// colourEnabled in lineedit.go.

// style is how a run of the line is drawn.
type style uint8

const (
	stylePlain style = iota
	styleBuiltin
	styleSyntax
	styleString
	styleComment
	styleParen
	styleMatch // the bracket matching the one the cursor is on
	styleUnmatched
)

// palette maps a style to its escape sequences.  It is a variable so that tests
// can replace it with something readable, and so that a caller could choose a
// theme.
var palette = map[style][2]string{
	stylePlain:     {"", ""},
	styleBuiltin:   {"\x1b[36m", "\x1b[0m"},   // cyan: a procedure the interpreter has
	styleSyntax:    {"\x1b[35m", "\x1b[0m"},   // magenta: syntax
	styleString:    {"\x1b[32m", "\x1b[0m"},   // green: text
	styleComment:   {"\x1b[90m", "\x1b[0m"},   // grey: commentary
	styleParen:     {"\x1b[1m", "\x1b[0m"},    // bold: the bracket under the cursor
	styleMatch:     {"\x1b[1;32m", "\x1b[0m"}, // bold green: the one it matches
	styleUnmatched: {"\x1b[1;31m", "\x1b[0m"}, // bold red: a bracket with no partner
}

// span is a run of the line drawn in one style.
type span struct {
	text  string
	style style
}

// wordClass says how a bare word should be drawn.  It is what the editor is
// given so that this file does not need a machine.
type wordClass func(word string) style

// highlight splits a line into styled spans.  pos is where the cursor is: the
// bracket under it, and the bracket matching that one, are drawn differently
// from everything else, which is the whole point of the exercise.
//
// The result always concatenates to exactly the line, so the caller can count
// columns from it without knowing anything about colour.
func highlight(line []rune, pos int, class wordClass) []span {
	var spans []span
	matches, unmatched := bracketPairs(line)

	at := func(i int) style {
		if s, ok := matches[i]; ok {
			_ = s
			return styleMatch
		}
		if unmatched[i] {
			return styleUnmatched
		}
		return stylePlain
	}
	_ = at

	flush := func(text string, st style) {
		if text == "" {
			return
		}
		if n := len(spans); n > 0 && spans[n-1].style == st {
			spans[n-1].text += text
			return
		}
		spans = append(spans, span{text: text, style: st})
	}

	i := 0
	for i < len(line) {
		// The bracket the cursor is on, and its partner, are decided first:
		// they are what the eye is looking for, and they override whatever the
		// character would otherwise be coloured as.
		if st, ok := bracketStyle(line, pos, matches, unmatched, i); ok {
			flush(string(line[i]), st)
			i++
			continue
		}
		switch c := line[i]; {
		case c == ';':
			// A comment runs to the end of the line, and swallows any brackets
			// in it: they are text, not structure.
			flush(string(line[i:]), styleComment)
			i = len(line)
		case c == '"':
			end := endOfString(line, i)
			flush(string(line[i:end]), styleString)
			i = end
		case c == '#' && i+1 < len(line) && line[i+1] == '\\':
			end := endOfCharacter(line, i)
			flush(string(line[i:end]), styleString)
			i = end
		case c == '#' && i+1 < len(line) && line[i+1] == '|':
			end := endOfBlockComment(line, i)
			flush(string(line[i:end]), styleComment)
			i = end
		case isNameChar(c) && !isDigits(line[i:]):
			end := i
			for end < len(line) && isNameChar(line[end]) {
				end++
			}
			word := string(line[i:end])
			flush(word, class(word))
			i = end
		default:
			flush(string(c), stylePlain)
			i++
		}
	}
	return spans
}

// bracketStyle reports the style for line[i] when it is a bracket the cursor
// has singled out.
func bracketStyle(line []rune, pos int, matches map[int]int, unmatched map[int]bool, i int) (style, bool) {
	if !isBracket(line[i]) {
		return stylePlain, false
	}
	// A bracket with no partner is the one thing that is always worth saying
	// something about, so it is decided first: a closing bracket that closes
	// nothing stays red however the cursor is placed, rather than being
	// repainted by the highlight below.
	if unmatched[i] {
		return styleUnmatched, true
	}
	// Otherwise the bracket the cursor is about: the one it sits on, or — when
	// it sits just past one, which is where it is after typing a closing
	// bracket or stepping over an opening one — the one immediately to its
	// left.  Showing the pair in both positions is what makes the bracket under
	// the eye the one that gets matched.
	if focus, ok := bracketAtCursor(line, pos); ok {
		if i == focus {
			return styleParen, true
		}
		if partner, ok := matches[focus]; ok && i == partner {
			return styleMatch, true
		}
	}
	return stylePlain, false
}

// bracketAtCursor returns the index of the bracket the cursor is about: the one
// at pos, or the one just before it.  There is at most one, because two
// brackets cannot both be adjacent to the same cursor position.
func bracketAtCursor(line []rune, pos int) (int, bool) {
	if pos < len(line) && isBracket(line[pos]) {
		return pos, true
	}
	if pos > 0 && pos-1 < len(line) && isBracket(line[pos-1]) {
		return pos - 1, true
	}
	return 0, false
}

// bracketPairs finds, for every bracket that has a partner, the index of that
// partner, and marks the ones that have none.
//
// Matching is done the way a reader does it: by nesting, ignoring brackets
// inside strings, character literals and comments.  A closing bracket with
// nothing open, or an opening one left open at the end of the line, has no
// partner — the second case is ordinary in a REPL, where a line may be the
// first of several, so it is not an error and is drawn quietly rather than in
// red.
func bracketPairs(line []rune) (map[int]int, map[int]bool) {
	matches := map[int]int{}
	unmatched := map[int]bool{}
	var stack []int
	i := 0
	for i < len(line) {
		switch c := line[i]; {
		case c == ';':
			i = len(line)
		case c == '"':
			i = endOfString(line, i)
		case c == '#' && i+1 < len(line) && line[i+1] == '\\':
			i = endOfCharacter(line, i)
		case c == '#' && i+1 < len(line) && line[i+1] == '|':
			i = endOfBlockComment(line, i)
		case c == '(' || c == '[':
			stack = append(stack, i)
			i++
		case c == ')' || c == ']':
			if len(stack) == 0 {
				unmatched[i] = true
				i++
				continue
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			matches[open] = i
			matches[i] = open
			i++
		default:
			i++
		}
	}
	// Brackets still open at the end get no partner from this line.  A REPL
	// line is often incomplete, so this is the normal case and stays unmarked.
	return matches, unmatched
}

func isBracket(r rune) bool { return r == '(' || r == ')' || r == '[' || r == ']' }

// endOfString returns the index just past the string literal that starts at i.
// A backslash escapes the next character, including a quote.
func endOfString(line []rune, i int) int {
	i++ // the opening quote
	for i < len(line) {
		switch line[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		default:
			i++
		}
	}
	return len(line) // unterminated: the rest of the line is the string
}

// endOfCharacter returns the index just past the character literal at i, which
// is #\x, #\newline, #\( and so on.  The tricky one is #\(: it is a character,
// not a bracket.
func endOfCharacter(line []rune, i int) int {
	i += 2 // #\
	if i >= len(line) {
		return len(line)
	}
	// A named character is a run of name characters; a single character is one
	// rune, and a name character that is also a bracket (like #\() ends there.
	if isNameChar(line[i]) {
		for i < len(line) && isNameChar(line[i]) {
			i++
		}
		return i
	}
	return i + 1
}

// endOfBlockComment returns the index just past the #| ... |# that starts at i.
// Block comments nest, as the reader's do.
func endOfBlockComment(line []rune, i int) int {
	depth := 0
	for i < len(line) {
		if i+1 < len(line) && line[i] == '#' && line[i+1] == '|' {
			depth++
			i += 2
			continue
		}
		if i+1 < len(line) && line[i] == '|' && line[i+1] == '#' {
			depth--
			i += 2
			if depth == 0 {
				return i
			}
			continue
		}
		i++
	}
	return len(line)
}

// isDigits reports whether the runes start with a digit or a sign followed by
// one, so that 1+ and -2 are not taken for names.
func isDigits(rs []rune) bool {
	if len(rs) == 0 {
		return false
	}
	if rs[0] == '-' || rs[0] == '+' || rs[0] == '.' {
		rs = rs[1:]
	}
	return len(rs) > 0 && rs[0] >= '0' && rs[0] <= '9'
}

// spansLength is the number of columns the spans occupy: what the cursor
// position is computed from.  Escape sequences are not part of it.
func spansLength(spans []span) int {
	n := 0
	for _, s := range spans {
		n += displayWidth([]rune(s.text))
	}
	return n
}

// renderSpans writes the spans with their colours.  A style with no escape
// sequences — or colour turned off — is written plainly.
func renderSpans(sb *strings.Builder, spans []span, colour bool) {
	for _, s := range spans {
		seq := palette[s.style]
		on, off := seq[0], seq[1]
		if !colour || on == "" {
			sb.WriteString(s.text)
			continue
		}
		sb.WriteString(on)
		sb.WriteString(s.text)
		sb.WriteString(off)
	}
}
