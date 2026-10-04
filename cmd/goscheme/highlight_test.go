// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

func classOf(builtins, syntax []string) wordClass {
	b := map[string]bool{}
	for _, n := range builtins {
		b[n] = true
	}
	s := map[string]bool{}
	for _, n := range syntax {
		s[n] = true
	}
	return func(word string) style {
		switch {
		case s[word]:
			return styleSyntax
		case b[word]:
			return styleBuiltin
		default:
			return stylePlain
		}
	}
}

// The spans must concatenate to exactly the line: every column count in the
// editor comes from the plain text, so a character lost or doubled here would
// move the cursor.
func TestSpansRebuildTheLine(t *testing.T) {
	lines := []string{
		`(+ 1 2)`,
		`(define (fact n) (if (= n 0) 1 (* n (fact (- n 1)))))`,
		`(display "a string with (brackets) and \" an escape")`,
		`(list #\( #\) #\space #\newline)`,
		`; a comment with (unbalanced brackets`,
		`#| block ( comment |# (+ 1 2)`,
		"`(a ,b ,@c)",
		`(vector-ref v 0)`,
		``,
		`((((`,
		`))))`,
	}
	for _, line := range lines {
		for pos := 0; pos <= len([]rune(line)); pos++ {
			spans := highlight([]rune(line), pos, classOf([]string{"+"}, nil))
			var sb strings.Builder
			for _, s := range spans {
				sb.WriteString(s.text)
			}
			if sb.String() != line {
				t.Errorf("highlight(%q, %d) rebuilt %q", line, pos, sb.String())
			}
			if got, want := spansLength(spans), displayWidth([]rune(line)); got != want {
				t.Errorf("highlight(%q, %d) is %d columns, want %d", line, pos, got, want)
			}
		}
	}
}

// Colouring must not change the column count: the escapes are written when the
// spans are, and never counted.
func TestColourDoesNotChangeWidth(t *testing.T) {
	line := []rune(`(display (+ 1 2))`)
	spans := highlight(line, 3, classOf([]string{"display", "+"}, []string{"if"}))
	var plain, coloured strings.Builder
	renderSpans(&plain, spans, false)
	renderSpans(&coloured, spans, true)
	if plain.String() != string(line) {
		t.Errorf("plain render = %q, want the line", plain.String())
	}
	if !strings.Contains(coloured.String(), "\x1b[") {
		t.Errorf("coloured render has no escapes: %q", coloured.String())
	}
	if got, want := displayWidth([]rune(coloured.String())), displayWidth(line); got == want {
		t.Errorf("this test is not measuring anything: displayWidth of escapes is not 0")
	}
	if got, want := spansLength(spans), displayWidth(line); got != want {
		t.Errorf("spans are %d columns, want %d", got, want)
	}
}

// The bracket under the cursor and its partner are marked, and nothing else is.
func TestBracketMatching(t *testing.T) {
	line := []rune(`(a (b c) d)`)
	for _, tc := range []struct {
		pos     int
		bold    int // the bracket under the cursor
		partner int
	}{
		{0, 0, 10},
		{10, 10, 0},
		{3, 3, 7},
		{7, 7, 3},
	} {
		matches, _ := bracketPairs(line)
		if matches[tc.bold] != tc.partner {
			t.Errorf("cursor at %d: bracket %d matches %d, want %d",
				tc.pos, tc.bold, matches[tc.bold], tc.partner)
		}
	}
	// The styled spans must mark exactly those two.
	spans := highlight(line, 3, classOf(nil, nil))
	var marked []string
	for _, s := range spans {
		if s.style == styleParen || s.style == styleMatch {
			marked = append(marked, s.text)
		}
	}
	if len(marked) != 2 {
		t.Errorf("marked %d spans, want 2: %v", len(marked), marked)
	}
}

// The bracket the cursor is about is the one it sits on, or the one just to its
// left.  The second case is where the cursor is after typing a closing bracket
// or stepping over an opening one, and it is the position a person is in most
// of the time.
func TestBracketOnEitherSideOfTheCursor(t *testing.T) {
	line := []rune("(a (b c) d)")
	// The line is "(a (b c) d)": 0 and 3 and 10 are brackets, 7 is the inner
	// closing one, and 11 is the end of the line, just past the last bracket.
	for _, tc := range []struct {
		pos  int
		want int
	}{
		{0, 0},  // on the opening bracket
		{1, 0},  // just past it
		{3, 3},  // on the inner opening bracket
		{4, 3},  // just past it
		{7, 7},  // on the inner closing bracket
		{8, 7},  // just past it
		{2, -1}, // a space: nothing on either side
		{5, -1},
		{10, 10}, // the outer closing bracket
		{11, 10}, // the end of the line, just past it
	} {
		got, ok := bracketAtCursor(line, tc.pos)
		if tc.want < 0 {
			if ok {
				t.Errorf("pos %d: focused bracket %d, want none", tc.pos, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("pos %d: focused %d (ok=%v), want %d", tc.pos, got, ok, tc.want)
		}
		// And whatever it focused, that bracket and its partner are the two
		// that get drawn differently.
		spans := highlight(line, tc.pos, classOf(nil, nil))
		n := 0
		for _, s := range spans {
			if s.style == styleParen || s.style == styleMatch {
				n++
			}
		}
		if n != 2 {
			t.Errorf("pos %d: %d brackets marked, want 2", tc.pos, n)
		}
	}
	// A bracket that is not adjacent to the cursor is not marked: two positions
	// in from either side of the inner pair is a space and a letter.
	for _, pos := range []int{2, 5, 6, 9, 12} {
		spans := highlight(line, pos, classOf(nil, nil))
		for _, s := range spans {
			if s.style == styleParen || s.style == styleMatch {
				t.Errorf("pos %d marked %q, want nothing marked", pos, s.text)
			}
		}
	}
}

// Brackets inside strings, characters and comments are text, not structure.
func TestBracketsInsideLiteralsAreNotStructure(t *testing.T) {
	for _, tc := range []struct{ line, why string }{
		{`(display "(")`, "a bracket inside a string"},
		{`(list #\()`, "a bracket as a character literal"},
		{`(+ 1 2) ; )`, "a bracket inside a comment"},
		{`#| ( |# (+ 1 2)`, "a bracket inside a block comment"},
	} {
		matches, unmatched := bracketPairs([]rune(tc.line))
		for i, partner := range matches {
			if partner < 0 || partner >= len([]rune(tc.line)) {
				t.Errorf("%s: %q has a bad match at %d", tc.why, tc.line, i)
			}
		}
		// A closing bracket with no partner is marked; an opening one left open
		// is not, because a REPL line is often incomplete.
		line := []rune(tc.line)
		for i := range line {
			if unmatched[i] && (line[i] == '(' || line[i] == '[') {
				t.Errorf("%s: %q marked an opening bracket as unmatched", tc.why, tc.line)
			}
		}
	}
}

// An unclosed bracket is the ordinary state of a REPL line, and is not an
// error: it must not be coloured as one.  A closing bracket that closes nothing
// is a different matter — that is a mistake, and it is marked.
func TestIncompleteLineIsNotAnError(t *testing.T) {
	_, unmatched := bracketPairs([]rune(`(define (f x)`))
	if len(unmatched) != 0 {
		t.Errorf("an incomplete line was marked: %v", unmatched)
	}
	_, unmatched = bracketPairs([]rune(`(+ 1 2))`))
	if !unmatched[7] {
		t.Errorf("the extra closing bracket was not marked: %v", unmatched)
	}
}

// A bracket that closes nothing stays marked wherever the cursor is: the
// highlight for the bracket pair around the cursor must not repaint it, which
// it did when the two rules were tried in the other order.
func TestUnmatchedStaysMarkedAtEveryCursorPosition(t *testing.T) {
	line := []rune(`(+ 1 2))`)
	last := len(line) - 1
	for pos := 0; pos <= len(line); pos++ {
		spans := highlight(line, pos, classOf(nil, nil))
		// Find the last span and check the style attached to the final bracket.
		marked := false
		for _, sp := range spans {
			if strings.HasSuffix(sp.text, string(line[last])) && sp.style == styleUnmatched {
				marked = true
			}
		}
		if !marked {
			t.Errorf("pos %d: the extra closing bracket is not marked: %v", pos, spans)
		}
	}
	// And the bracket that does close something is never red.
	line = []rune(`(+ 1 2)`)
	for pos := 0; pos <= len(line); pos++ {
		for _, sp := range highlight(line, pos, classOf(nil, nil)) {
			if sp.style == styleUnmatched {
				t.Errorf("pos %d: %q is marked although it closes something", pos, sp.text)
			}
		}
	}
}
