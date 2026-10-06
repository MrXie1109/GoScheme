// SPDX-License-Identifier: MIT

package re

import (
	"fmt"
	"strings"
)

type writeMode int

const (
	modeDisplay writeMode = iota
	modeWrite
	modeWriteShared
	modeWriteSimple
)

// WriteToString renders v as `write` would, to a Go string.
func WriteToString(v Value) string {
	var sb strings.Builder
	p := &printer{mode: modeWrite}
	p.prepare(v)
	p.print(&sb, v)
	return sb.String()
}

// DisplayToString renders v as `display` would, to a Go string.
func DisplayToString(v Value) string {
	var sb strings.Builder
	p := &printer{mode: modeDisplay}
	p.print(&sb, v)
	return sb.String()
}

// WriteSharedToString renders v with full sharing detection.
func WriteSharedToString(v Value) string {
	var sb strings.Builder
	p := &printer{mode: modeWriteShared}
	p.prepare(v)
	p.print(&sb, v)
	return sb.String()
}

// WriteSimpleToString renders v without datum labels.
func WriteSimpleToString(v Value) string {
	var sb strings.Builder
	p := &printer{mode: modeWriteSimple}
	p.print(&sb, v)
	return sb.String()
}

type printer struct {
	mode    writeMode
	labels  map[Value]int
	emitted map[Value]bool
}

// labelable reports whether v is an object the report requires datum labels
// for (pairs and vectors).
func labelable(v Value) bool {
	switch v.(type) {
	case *Pair, *Vector:
		return true
	}
	return false
}

func (p *printer) prepare(root Value) {
	if p.mode == modeWriteSimple || p.mode == modeDisplay {
		return
	}
	p.labels = map[Value]int{}
	p.emitted = map[Value]bool{}
	need := map[Value]bool{}
	if p.mode == modeWrite {
		// Only cycles require labels for `write`.
		open := map[Value]bool{}
		done := map[Value]bool{}
		var visit func(v Value)
		visit = func(v Value) {
			if !labelable(v) {
				return
			}
			if open[v] {
				need[v] = true
				return
			}
			if done[v] {
				return
			}
			open[v] = true
			switch x := v.(type) {
			case *Pair:
				visit(x.Car)
				visit(x.Cdr)
			case *Vector:
				for _, it := range x.Items {
					visit(it)
				}
			}
			delete(open, v)
			done[v] = true
		}
		visit(root)
	} else {
		counts := map[Value]int{}
		visited := map[Value]bool{}
		var count func(v Value)
		count = func(v Value) {
			if !labelable(v) {
				return
			}
			if visited[v] {
				counts[v]++
				return
			}
			visited[v] = true
			switch x := v.(type) {
			case *Pair:
				count(x.Car)
				count(x.Cdr)
			case *Vector:
				for _, it := range x.Items {
					count(it)
				}
			}
		}
		count(root)
		for v, n := range counts {
			if n > 0 {
				need[v] = true
			}
		}
	}
	// Number the labelled objects in a deterministic (left to right) order.
	var order func(v Value)
	walked := map[Value]bool{}
	order = func(v Value) {
		if !labelable(v) || walked[v] {
			return
		}
		walked[v] = true
		if need[v] {
			p.labels[v] = len(p.labels)
		}
		switch x := v.(type) {
		case *Pair:
			order(x.Car)
			order(x.Cdr)
		case *Vector:
			for _, it := range x.Items {
				order(it)
			}
		}
	}
	order(root)
}

func (p *printer) print(sb *strings.Builder, v Value) {
	if p.labels != nil {
		if n, ok := p.labels[v]; ok {
			if p.emitted[v] {
				fmt.Fprintf(sb, "#%d#", n)
				return
			}
			p.emitted[v] = true
			fmt.Fprintf(sb, "#%d=", n)
		}
	}
	p.printRaw(sb, v)
}

func (p *printer) printRaw(sb *strings.Builder, v Value) {
	switch x := v.(type) {
	case nil:
		sb.WriteString("#!unspecified")
	case Boolean:
		if x {
			sb.WriteString("#t")
		} else {
			sb.WriteString("#f")
		}
	case Empty:
		sb.WriteString("()")
	case EOF:
		sb.WriteString("#<eof>")
	case Unspecified:
		sb.WriteString("#!unspecified")
	case *Symbol:
		if p.mode == modeDisplay {
			sb.WriteString(x.Name)
		} else {
			sb.WriteString(printSymbol(x.Name))
		}
	case Char:
		p.printChar(sb, rune(x))
	case *String:
		if p.mode == modeDisplay {
			sb.WriteString(x.Value())
		} else {
			p.printString(sb, x)
		}
	case *Integer, *Rational, Float, *Complex:
		sb.WriteString(FormatNumber(v, 10))
	case *Pair:
		p.printPair(sb, x)
	case *Vector:
		p.printVector(sb, x)
	case *Bytevector:
		sb.WriteString("#u8(")
		for i, b := range x.Bytes {
			if i > 0 {
				sb.WriteString(" ")
			}
			fmt.Fprintf(sb, "%d", b)
		}
		sb.WriteString(")")
	case *Closure:
		if x.Name != "" {
			fmt.Fprintf(sb, "#<procedure %s>", x.Name)
		} else {
			sb.WriteString("#<procedure>")
		}
	case *Primitive:
		fmt.Fprintf(sb, "#<procedure %s>", x.Name)
	case *Continuation:
		sb.WriteString("#<continuation>")
	case *Parameter:
		fmt.Fprintf(sb, "#<parameter %s>", x.Name)
	case *Promise:
		sb.WriteString("#<promise>")
	case *Port:
		fmt.Fprintf(sb, "#<%s-port %s>", x.kindName(), x.Name)
	case *Record:
		fmt.Fprintf(sb, "#<%s", x.Type.Name)
		for i, f := range x.Fields {
			sb.WriteString(" ")
			if i < len(x.Type.Fields) {
				sb.WriteString(x.Type.Fields[i].Name)
				sb.WriteString(": ")
			}
			p.print(sb, f)
		}
		sb.WriteString(">")
	case *RecordTypeDescriptor:
		fmt.Fprintf(sb, "#<record-type %s>", x.Type.Name)
	case *ErrorObject:
		sb.WriteString("#<error ")
		sb.WriteString(x.Message)
		for _, ir := range x.Irritants {
			sb.WriteString(" ")
			sb.WriteString(WriteToString(ir))
		}
		sb.WriteString(">")
	case SchemeEnv:
		sb.WriteString("#<environment>")
	case *MultipleValues:
		for i, mv := range x.Values {
			if i > 0 {
				sb.WriteString(" ")
			}
			p.print(sb, mv)
		}
	default:
		// The interpreter's extension objects — a mutex, a socket, a regexp, a
		// macro — are declared with the procedures that use them, because
		// those procedures read their unexported fields and Go allows that only
		// in the declaring package.  This package cannot name their types, so
		// it asks: an object that knows how it prints says so, and one that
		// does not is reported by its Go type as before.
		if d, ok := v.(Describer); ok {
			sb.WriteString(d.SchemeDescribe(p))
			break
		}
		fmt.Fprintf(sb, "#<unknown %T>", v)
	}
}

// Describer is implemented by a runtime object whose printed form this package
// cannot know.  The interpreter declares those objects next to the procedures
// that work on them, and each of them knows the one thing the printer needs.
//
// The printer is passed rather than a mode flag because two of these objects
// print a contained value — a box prints what is inside it, and a
// multiple-values object prints each of its values — and a contained value has
// to be printed with the settings the outer call is using, so that display and
// write differ inside a box exactly as they do outside one.
type Describer interface {
	// SchemeDescribe returns the printed form of this object, using p for
	// anything it contains.
	SchemeDescribe(p Printer) string
}

// Printer is what a Describer is handed to print a contained value: this
// package's printer, as much of it as anything outside needs.  *printer
// implements it.
type Printer interface {
	// Print appends the printed form of v to sb in the current mode.
	Print(sb *strings.Builder, v Value)
}

// Print exposes the printer's one method to the interpreter, so that a
// Describer can print what it contains.
func (p *printer) Print(sb *strings.Builder, v Value) { p.print(sb, v) }

func (p *printer) printPair(sb *strings.Builder, pr *Pair) {
	sb.WriteString("(")
	p.print(sb, pr.Car)
	cur := pr.Cdr
	for {
		if _, isNil := cur.(Empty); isNil {
			break
		}
		cell, ok := cur.(*Pair)
		if !ok {
			sb.WriteString(" . ")
			p.print(sb, cur)
			break
		}
		if _, need := p.labels[cell]; need {
			sb.WriteString(" . ")
			p.print(sb, cell)
			break
		}
		sb.WriteString(" ")
		p.print(sb, cell.Car)
		cur = cell.Cdr
	}
	sb.WriteString(")")
}

func (p *printer) printVector(sb *strings.Builder, vec *Vector) {
	sb.WriteString("#(")
	for i, it := range vec.Items {
		if i > 0 {
			sb.WriteString(" ")
		}
		p.print(sb, it)
	}
	sb.WriteString(")")
}

func (p *printer) printString(sb *strings.Builder, s *String) {
	sb.WriteByte('"')
	for _, r := range s.Runes {
		switch r {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		case 7:
			sb.WriteString("\\a")
		case 8:
			sb.WriteString("\\b")
		default:
			if r < 32 || r == 127 {
				fmt.Fprintf(sb, "\\x%x;", r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
}

// namedChars is the rune-to-name table this printer writes, and the reader reads
// those names back through a switch of its own.
//
// The two are inverse in the direction that matters: every name here is one the
// reader accepts, so anything written can be read.  The reader accepts more than
// this — `#\vtab`, `#\nel`, `#\esc`, `#\rubout` and others — because a program
// may contain them, and those print as `#\x7` and the like, which reads back as
// the same character.
//
// The tables are kept apart on purpose: a reader has to accept every spelling
// the language allows, while a printer should choose one, and a single table
// cannot be both.  A test in printer_test.go holds them together by printing
// every entry and reading it back.
var namedChars = map[rune]string{
	' ':  "space",
	'\n': "newline",
	'\t': "tab",
	'\r': "return",
	0:    "null",
	7:    "alarm",
	8:    "backspace",
	27:   "escape",
	127:  "delete",
}

// NamedChars is the rune-to-name table for the interpreter's tests, which check
// that every name the printer writes is one the reader accepts.
func NamedChars() map[rune]string { return namedChars }

func (p *printer) printChar(sb *strings.Builder, r rune) {
	if p.mode == modeDisplay {
		sb.WriteRune(r)
		return
	}
	if name, ok := namedChars[r]; ok {
		sb.WriteString("#\\")
		sb.WriteString(name)
		return
	}
	// A character with no name: the hexadecimal escape is the one form that can
	// always be written and always read back.
	if r < 32 {
		fmt.Fprintf(sb, "#\\x%x", r)
		return
	}
	sb.WriteString("#\\")
	sb.WriteRune(r)
}

// isPeculiarIdentifier reports whether name is one of the R7RS "peculiar
// identifiers" that are unambiguously symbols.
func isPeculiarIdentifier(name string) bool {
	switch name {
	case "+", "-", "...":
		return true
	}
	return strings.HasPrefix(name, "->")
}

// printSymbol quotes symbols that would not read back as themselves.
func printSymbol(name string) string {
	if name == "" {
		return "||"
	}
	if name == "." {
		return "|.|"
	}
	if _, isNum := ParseNumber(name, 10); isNum {
		return "|" + name + "|"
	}
	// Symbols that merely *look* like numbers are also quoted so that the
	// output reads back as a symbol.  Peculiar identifiers (+, -, ..., ->foo)
	// are exempt because they can never be read as numbers.
	if !isPeculiarIdentifier(name) {
		switch name[0] {
		case '+', '-', '.', '@':
			return "|" + name + "|"
		}
		if name[0] >= '0' && name[0] <= '9' {
			return "|" + name + "|"
		}
	}
	needBar := false
	for i, r := range name {
		if r <= ' ' || r == 127 {
			needBar = true
			break
		}
		switch r {
		case '(', ')', '[', ']', '"', ';', '\'', '`', ',', '|', '\\', '#':
			needBar = true
		}
		if needBar {
			break
		}
		if i == 0 && (r >= '0' && r <= '9') {
			needBar = true
		}
		_ = i
	}
	if !needBar {
		return name
	}
	var sb strings.Builder
	sb.WriteByte('|')
	for _, r := range name {
		switch r {
		case '|':
			sb.WriteString("\\|")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\t':
			sb.WriteString("\\t")
		case '\r':
			sb.WriteString("\\r")
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('|')
	return sb.String()
}

// MultipleValues is the object produced by (values ...).
type MultipleValues struct {
	Values []Value
}
