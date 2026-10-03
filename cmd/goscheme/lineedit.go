// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// errInterrupted is returned by ReadLine when the user pressed Ctrl-C.  It is
// not a failure: it means "forget this line", which the REPL has to tell apart
// from an empty line so that an unfinished expression can be abandoned.
var errInterrupted = errors.New("interrupted")

// The escape sequences that bracket a paste when the terminal is asked for
// bracketed paste mode with ESC [ ? 2004 h.
const (
	bracketedPasteOn  = "\x1b[?2004h"
	bracketedPasteOff = "\x1b[?2004l"
	pasteStart        = "\x1b[200~"
	pasteEnd          = "\x1b[201~"
)

// lineTracker remembers whether the last byte written to the terminal was a
// newline.  The REPL uses it to start a fresh line before drawing a prompt
// without erasing output that did not end with one, and to avoid emitting a
// blank line after a paste that already ended with one.
type lineTracker struct {
	w           io.Writer
	atLineStart bool
}

func newLineTracker(w io.Writer) *lineTracker {
	return &lineTracker{w: w, atLineStart: true}
}

func (t *lineTracker) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		t.atLineStart = p[n-1] == '\n'
	}
	return n, err
}

// newLine starts a fresh line if the cursor is not already at the beginning of
// one.
func (t *lineTracker) newLine() {
	if !t.atLineStart {
		_, _ = io.WriteString(t.w, "\n")
		t.atLineStart = true
	}
}

// keyKind is one editing event.
type keyKind int

const (
	keyRune keyKind = iota
	keyEnter
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyUp
	keyDown
	keyCtrlA
	keyCtrlB
	keyCtrlC
	keyCtrlD
	keyCtrlE
	keyCtrlF
	keyCtrlK
	keyCtrlL
	keyCtrlU
	keyCtrlW
	keyTab
	keyPasteStart
	keyPasteEnd
	keyUnknown
)

type key struct {
	kind keyKind
	r    rune
}

// lineEditor reads lines from a terminal that is already in raw mode.  It
// provides the editing keys a user expects (cursor movement, history,
// backspace) and, most importantly, recognises bracketed paste: everything
// between ESC [ 200 ~ and ESC [ 201 ~ is returned as a single input block, so
// a pasted multi-line program is never mistaken for several lines of typing
// and never gets a prompt wedged into the middle of it.
type lineEditor struct {
	in  *bufio.Reader
	out *lineTracker

	history []string
	histPos int

	line []rune
	pos  int

	prompt     string
	promptCols int
	// pasting is true while a bracketed paste is being consumed; the pasted
	// text is echoed as it arrives instead of being redrawn.
	pasting bool
	// lastVPos is the row the cursor was left on by the last render, counted
	// from the prompt row, and lastRows is how many rows the line occupied when
	// it was drawn.  A redraw goes back up by lastVPos to reach the prompt row,
	// and lastRows is what tells it whether the screen is now showing more rows
	// than the line needs (a paste undone with backspace), which it has to
	// clear.
	lastVPos int
	lastRows int
	// pasteCR remembers a carriage return that ended a chunk, in case the
	// following chunk starts with a line feed.
	pasteCR bool
	// complete supplies Tab completion.  It is given the line and the cursor
	// and returns where the word being completed starts, and the candidates
	// that extend it.  Nil means Tab does nothing.
	complete func(line []rune, pos int) (int, []string)
}

func newLineEditor(in io.Reader, out *lineTracker) *lineEditor {
	return &lineEditor{in: bufio.NewReaderSize(in, 4096), out: out}
}

// ReadLine reads one line of input.  A bracketed paste is returned as one
// string that may contain newlines.
func (e *lineEditor) ReadLine(prompt string) (string, error) {
	e.line = e.line[:0]
	e.pos = 0
	e.lastVPos = 0
	e.prompt = prompt
	e.promptCols = displayWidth([]rune(prompt))
	e.histPos = len(e.history)
	e.out.newLine()
	e.render()

	for {
		k, err := e.readKey()
		if err != nil {
			// End of input: hand back whatever was typed so far.
			if len(e.line) > 0 {
				e.out.newLine()
				return string(e.line), nil
			}
			return "", err
		}

		switch k.kind {
		case keyEnter:
			e.out.newLine()
			line := string(e.line)
			e.remember(line)
			return line, nil

		case keyPasteStart:
			// A paste is inserted into the line being edited, exactly as it
			// would be in a shell: it is not submitted until Enter is
			// pressed, so a pasted program can be reviewed first.
			if err := e.consumePaste(); err != nil {
				return "", err
			}

		case keyRune:
			e.line = append(e.line, 0)
			copy(e.line[e.pos+1:], e.line[e.pos:])
			e.line[e.pos] = k.r
			e.pos++

		case keyBackspace:
			if e.pos > 0 {
				e.line = append(e.line[:e.pos-1], e.line[e.pos:]...)
				e.pos--
			}

		case keyDelete:
			if e.pos < len(e.line) {
				e.line = append(e.line[:e.pos], e.line[e.pos+1:]...)
			}

		case keyLeft, keyCtrlB:
			if e.pos > 0 {
				e.pos--
			}

		case keyRight, keyCtrlF:
			if e.pos < len(e.line) {
				e.pos++
			}

		case keyHome, keyCtrlA:
			e.pos = 0

		case keyEnd, keyCtrlE:
			e.pos = len(e.line)

		case keyCtrlK:
			e.line = e.line[:e.pos]

		case keyCtrlU:
			e.line = append(e.line[:0], e.line[e.pos:]...)
			e.pos = 0

		case keyTab:
			e.completeWord()

		case keyCtrlW:
			start := e.pos
			for start > 0 && e.line[start-1] == ' ' {
				start--
			}
			for start > 0 && e.line[start-1] != ' ' {
				start--
			}
			e.line = append(e.line[:start], e.line[e.pos:]...)
			e.pos = start

		case keyUp:
			if e.histPos > 0 {
				e.histPos--
				e.setLine(e.history[e.histPos])
			}

		case keyDown:
			if e.histPos < len(e.history)-1 {
				e.histPos++
				e.setLine(e.history[e.histPos])
			} else {
				e.histPos = len(e.history)
				e.setLine("")
			}

		case keyCtrlL:
			fmt.Fprint(e.out, "\x1b[H\x1b[2J")

		case keyCtrlC:
			// Abandon the line, like a shell does.  The caller is told, so
			// that a half-written expression can be abandoned too instead of
			// looking like an empty line of input.
			fmt.Fprint(e.out, "^C")
			e.out.atLineStart = false
			e.out.newLine()
			return "", errInterrupted

		case keyCtrlD:
			if len(e.line) == 0 {
				return "", io.EOF
			}
			if e.pos < len(e.line) {
				e.line = append(e.line[:e.pos], e.line[e.pos+1:]...)
			}
		}
		e.render()
	}
}

// completeWord asks the completer about the word before the cursor.  One
// candidate replaces the word; several insert what they have in common and, if
// that adds nothing, are listed above the redrawn line, as a shell does.
func (e *lineEditor) completeWord() {
	if e.complete == nil {
		return
	}
	start, candidates := e.complete(e.line, e.pos)
	if len(candidates) == 0 {
		return
	}
	word := string(e.line[start:e.pos])
	if len(candidates) == 1 {
		e.replaceWord(start, candidates[0])
		e.render()
		return
	}
	common := commonPrefix(candidates)
	if len(common) > len(word) {
		e.replaceWord(start, common)
		e.render()
		return
	}
	e.out.newLine()
	col := 0
	for _, c := range candidates {
		if col > 0 && col+len(c)+2 > 78 {
			fmt.Fprint(e.out, "\n")
			col = 0
		}
		fmt.Fprintf(e.out, "%s  ", c)
		col += len(c) + 2
	}
	fmt.Fprint(e.out, "\n")
	e.render()
}

// replaceWord replaces line[start:pos] with text and leaves the cursor after it.
func (e *lineEditor) replaceWord(start int, text string) {
	rs := []rune(text)
	rest := append([]rune(nil), e.line[e.pos:]...)
	e.line = append(e.line[:start], rs...)
	e.line = append(e.line, rest...)
	e.pos = start + len(rs)
}

// commonPrefix is the longest string every candidate starts with.
func commonPrefix(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	prefix := candidates[0]
	for _, c := range candidates[1:] {
		for !strings.HasPrefix(c, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}

// remember records a submitted line, skipping an immediate repeat.
func (e *lineEditor) remember(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	if n := len(e.history); n > 0 && e.history[n-1] == line {
		return
	}
	e.history = append(e.history, line)
}

func (e *lineEditor) setLine(s string) {
	e.line = append(e.line[:0], []rune(s)...)
	e.pos = len(e.line)
}

// render redraws the prompt and the line being edited, then puts the cursor
// back at the insertion point.  The line may contain newlines (a pasted
// block), in which case it occupies several rows and the cursor is moved
// accordingly.
// render draws the line and puts the cursor where e.pos is.
//
// It follows the model GNU readline uses (display.c: `_rl_last_v_pos`): the
// editor remembers the row the cursor was left on by the *previous* draw, and
// the first thing a redraw does is move back up to the prompt row from there.
// Deriving that row from e.pos instead — "the cursor is on the row e.pos is on"
// — is true only when the last draw put it there, and a paste breaks that: the
// text arrives without being drawn, so the cursor is still on the row the old
// (shorter) line ended on while e.pos is already several rows further down.
// The redraw then moves up by the wrong number of rows and rewrites over the
// prompt and the banner, which is exactly what a pasted multi-line expression
// did.
func (e *lineEditor) render() {
	var sb strings.Builder
	// Back to the prompt row: up by as many rows as the cursor is below it.
	// Start from the *top* of what the last draw put on the screen: the cursor
	// may be above or below where the line now ends, but the prompt row is
	// fixed, so going up by the number of rows the old line used is what
	// reaches it.  Using the cursor's row instead leaves the extra rows of a
	// longer previous line on screen — which is what a paste followed by
	// backspaces did.
	if up := e.lastVPos; up > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", up)
	}
	sb.WriteByte('\r')
	// Erase from the prompt row to the bottom of the screen, so that rows the
	// line used to occupy but no longer needs are cleared too.  Erasing only
	// from the cursor would leave anything below it.
	sb.WriteString("\x1b[J")
	sb.WriteString(e.prompt)
	sb.WriteString(string(e.line))

	// Forward from the end of the line, which is where writing it left the
	// cursor, to the row and column e.pos is at.
	up := countNewlines(e.line[e.pos:])
	if up > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", up)
	}
	sb.WriteByte('\r')
	// The column is counted from the start of the row the cursor ends up on.
	// On the prompt's own row that includes the prompt, because the prompt is
	// written before the line; on any later row the prompt is behind us — a row
	// above — and adding its width would push the cursor past the end of the
	// text by exactly four columns, which is how wide ">>> " is.  What decides
	// which row that is, is whether the cursor is on the first row of the line,
	// not whether it is at the end of it.
	col := displayWidth(afterLastNewline(e.line[:e.pos]))
	if countNewlines(e.line[:e.pos]) == 0 {
		col += e.promptCols
	}
	if col > 0 {
		fmt.Fprintf(&sb, "\x1b[%dC", col)
	}
	// Where that left the cursor, counted from the prompt row — which is how
	// far up the next redraw has to go to reach it.  That is the rows *before*
	// e.pos, not after it: readline calls this _rl_last_v_pos, and getting it
	// backwards is what made a backspace after a paste redraw on the row below
	// instead of the row above, stacking a copy of the prompt per keystroke.
	e.lastVPos = countNewlines(e.line[:e.pos])
	e.lastRows = countNewlines(e.line)
	io.WriteString(e.out, sb.String())
}

func countNewlines(rs []rune) int {
	n := 0
	for _, r := range rs {
		if r == '\n' {
			n++
		}
	}
	return n
}

// afterLastNewline returns the runes of rs that follow its last newline.
func afterLastNewline(rs []rune) []rune {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == '\n' {
			return rs[i+1:]
		}
	}
	return rs
}

// consumePaste reads everything up to the bracketed paste end marker, echoing
// it as it goes and appending it to the current line.  Newlines inside a paste
// are kept as part of the input instead of submitting the line.
func (e *lineEditor) consumePaste() error {
	e.pasting = true
	defer func() { e.pasting = false }()
	end := []byte(pasteEnd)
	for {
		n := e.in.Buffered()
		if n == 0 {
			if _, err := e.in.Peek(1); err != nil {
				return err
			}
			continue
		}
		chunk, err := e.in.Peek(n)
		if err != nil {
			return err
		}
		if i := bytes.Index(chunk, end); i >= 0 {
			e.appendPaste(chunk[:i])
			_, _ = e.in.Discard(i + len(end))
			e.flushPaste()
			return nil
		}
		// Hold back a suffix that may be the beginning of the end marker.
		keep := partialSuffixLen(chunk, pasteEnd)
		if keep == len(chunk) {
			// Everything buffered could be the start of the end marker, so
			// there is nothing to consume yet: wait for more input.
			if _, err := e.in.Peek(len(chunk) + 1); err != nil {
				return err
			}
			continue
		}
		e.appendPaste(chunk[:len(chunk)-keep])
		_, _ = e.in.Discard(len(chunk) - keep)
	}
}

// appendPaste adds pasted bytes to the line and echoes them.
//
// Terminals send a carriage return for a pasted newline, exactly as they do
// for the Enter key, and raw mode translates nothing: writing that CR back
// would return the cursor to column 0 without moving to the next line, so the
// pasted lines would overwrite each other.  Line endings are therefore
// normalised to LF, which the terminal driver turns back into CR+LF.
func (e *lineEditor) appendPaste(b []byte) {
	if e.pasteCR {
		// The held carriage return becomes a newline either way; when it
		// turned out to be the CR of a CRLF pair the LF is simply dropped.
		e.pasteCR = false
		if len(b) > 0 && b[0] == '\n' {
			b = b[1:]
		}
		e.emitPaste([]byte{'\n'})
	}
	if len(b) == 0 {
		return
	}
	norm := make([]byte, 0, len(b)+1)
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '\r':
			if i == len(b)-1 {
				// Might be the CR of a CRLF split across chunks.
				e.pasteCR = true
				continue
			}
			if b[i+1] == '\n' {
				i++
			}
			norm = append(norm, '\n')
		case '\n':
			norm = append(norm, '\n')
		default:
			norm = append(norm, b[i])
		}
	}
	e.emitPaste(norm)
}

// emitPaste inserts normalised bytes at the cursor.  A paste goes where the
// cursor is, as it does in a shell; this used to append it to the end of the
// line and leave the cursor there, whatever the cursor had been.  The line is
// redrawn by the key loop once the paste has been read.
func (e *lineEditor) emitPaste(b []byte) {
	if len(b) == 0 {
		return
	}
	rs := []rune(string(b))
	tail := len(e.line) - e.pos
	e.line = append(e.line, rs...)
	copy(e.line[e.pos+len(rs):], e.line[e.pos:e.pos+tail])
	copy(e.line[e.pos:], rs)
	e.pos += len(rs)
}

// flushPaste emits a carriage return that was held back at the very end of a
// paste.
func (e *lineEditor) flushPaste() {
	if e.pasteCR {
		e.pasteCR = false
		e.emitPaste([]byte{'\n'})
	}
}

// partialSuffixLen returns the length of the longest suffix of b that is a
// proper prefix of marker, i.e. how many trailing bytes must be held back
// until more input arrives.
func partialSuffixLen(b []byte, marker string) int {
	max := len(marker) - 1
	if len(b) < max {
		max = len(b)
	}
	for n := max; n > 0; n-- {
		if string(b[len(b)-n:]) == marker[:n] {
			return n
		}
	}
	return 0
}

// readKey decodes one editing event from the raw byte stream.
func (e *lineEditor) readKey() (key, error) {
	b, err := e.in.Peek(1)
	if err != nil {
		return key{}, err
	}
	switch c := b[0]; {
	case c == 0x1b:
		return e.readEscape()
	case c == '\r' || c == '\n':
		_, _ = e.in.ReadByte()
		return key{kind: keyEnter}, nil
	case c == 0x7f || c == 0x08:
		_, _ = e.in.ReadByte()
		return key{kind: keyBackspace}, nil
	case c == 0x01:
		_, _ = e.in.ReadByte()
		return key{kind: keyCtrlA}, nil
	case c == 0x02:
		_, _ = e.in.ReadByte()
		return key{kind: keyCtrlB}, nil
	case c == 0x03:
		_, _ = e.in.ReadByte()
		return key{kind: keyCtrlC}, nil
	case c == 0x04:
		_, _ = e.in.ReadByte()
		return key{kind: keyCtrlD}, nil
	case c == 0x05:
		_, _ = e.in.ReadByte()
		return key{kind: keyCtrlE}, nil
	case c == 0x06:
		_, _ = e.in.ReadByte()
		return key{kind: keyCtrlF}, nil
	case c == 0x0b:
		_, _ = e.in.ReadByte()
		return key{kind: keyCtrlK}, nil
	case c == 0x0c:
		_, _ = e.in.ReadByte()
		return key{kind: keyCtrlL}, nil
	case c == 0x15:
		_, _ = e.in.ReadByte()
		return key{kind: keyCtrlU}, nil
	case c == 0x17:
		_, _ = e.in.ReadByte()
		return key{kind: keyCtrlW}, nil
	case c == '\t':
		_, _ = e.in.ReadByte()
		return key{kind: keyTab}, nil
	case c < 0x20:
		_, _ = e.in.ReadByte()
		return key{kind: keyUnknown}, nil
	}
	r, _, err := e.in.ReadRune()
	if err != nil {
		return key{}, err
	}
	return key{kind: keyRune, r: r}, nil
}

// readEscape decodes an escape sequence.  Unknown sequences are swallowed.
func (e *lineEditor) readEscape() (key, error) {
	if _, err := e.in.ReadByte(); err != nil { // consume ESC
		return key{}, err
	}
	b, err := e.in.Peek(1)
	if err != nil {
		return key{}, err
	}
	if b[0] != '[' && b[0] != 'O' {
		// Alt+<key>: not an editing key, drop it.
		_, _ = e.in.ReadByte()
		return key{kind: keyUnknown}, nil
	}
	prefix, _ := e.in.ReadByte()
	var params []byte
	for {
		c, err := e.in.ReadByte()
		if err != nil {
			return key{}, err
		}
		if c >= 0x40 && c <= 0x7e {
			return classifyCSI(prefix, string(params), c), nil
		}
		if len(params) > 32 {
			return key{kind: keyUnknown}, nil
		}
		params = append(params, c)
	}
}

func classifyCSI(prefix byte, params string, final byte) key {
	switch final {
	case 'A':
		return key{kind: keyUp}
	case 'B':
		return key{kind: keyDown}
	case 'C':
		return key{kind: keyRight}
	case 'D':
		return key{kind: keyLeft}
	case 'H':
		return key{kind: keyHome}
	case 'F':
		return key{kind: keyEnd}
	case '~':
		switch params {
		case "1", "7":
			return key{kind: keyHome}
		case "4", "8":
			return key{kind: keyEnd}
		case "3":
			return key{kind: keyDelete}
		case "200":
			return key{kind: keyPasteStart}
		case "201":
			return key{kind: keyPasteEnd}
		}
	}
	return key{kind: keyUnknown}
}

// displayWidth approximates the number of terminal columns a slice of runes
// occupies, so that the cursor can be placed correctly after a redraw.
func displayWidth(rs []rune) int {
	w := 0
	for _, r := range rs {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32 || (r >= 0x7f && r < 0xa0):
		return 0
	case r >= 0x1100 && (r <= 0x115f || // Hangul Jamo
		r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) || // CJK ... Yi
		(r >= 0xac00 && r <= 0xd7a3) || // Hangul Syllables
		(r >= 0xf900 && r <= 0xfaff) || // CJK Compatibility Ideographs
		(r >= 0xfe30 && r <= 0xfe6f) || // CJK Compatibility Forms
		(r >= 0xff00 && r <= 0xff60) || // Fullwidth Forms
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x20000 && r <= 0x3fffd)):
		return 2
	}
	return 1
}

var _ = utf8.RuneLen
