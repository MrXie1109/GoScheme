// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"strings"
)

// Packing source.
//
// A packed program is its source with the comments and the layout taken out:
// the same program, written once by a person and stored as a reader would read
// it back.  It is what `goscheme pack` puts inside an executable, and it exists
// because the alternative — a compiled file format — was a second thing to
// keep, version and explain for a saving that is invisible (compiling a script
// takes milliseconds).
//
// The transformation is a read/write round trip rather than a text edit, and
// that is the whole point: a program that removes "everything from a semicolon
// to the end of the line" removes the semicolons inside string literals too,
// and one that collapses whitespace collapses the newline inside a string as
// well.  Reading the program into data and writing the data back cannot make
// either mistake, because the reader is the one that decides what a comment is.
//
// What it costs is the layout: indentation, the choice between 'x and (quote
// x), and any comment meant for a person.  What it keeps is the program.

// PackSource returns source with its comments and layout removed.
//
// The result reads back as the same sequence of data: PackSource followed by
// ReadAll gives what ReadAll gave for the input.  A source that cannot be read
// is an error, and the caller decides what to do — `goscheme pack` keeps the
// original text in that case, because a script that does not read is a script
// whose problem should be reported when it runs, not when it is packed.
func PackSource(source, name string) ([]byte, error) {
	r := NewStringReader(source)
	if name != "" {
		r.Source = name
	}
	forms, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.Grow(len(source) / 2)
	for _, f := range forms {
		b.WriteString(WriteToString(f))
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// UnpackCheck reports whether packed source reads back as the same data as the
// original, which is what makes packing safe to do.  It is used by the tests
// and by `goscheme pack --check`.
func UnpackCheck(original, packed, name string) error {
	orig := NewStringReader(original)
	if name != "" {
		orig.Source = name
	}
	a, err := orig.ReadAll()
	if err != nil {
		return fmt.Errorf("the original does not read: %w", err)
	}
	pack := NewStringReader(string(packed))
	if name != "" {
		pack.Source = name
	}
	b, err := pack.ReadAll()
	if err != nil {
		return fmt.Errorf("the packed source does not read: %w", err)
	}
	if len(a) != len(b) {
		return fmt.Errorf("the original has %d forms and the packed source has %d", len(a), len(b))
	}
	for i := range a {
		if !Equal(a[i], b[i]) {
			return fmt.Errorf("form %d differs:\n  original: %s\n  packed:   %s",
				i+1, WriteToString(a[i]), WriteToString(b[i]))
		}
	}
	return nil
}
