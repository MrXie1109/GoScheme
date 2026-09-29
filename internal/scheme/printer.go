// SPDX-License-Identifier: MIT

package scheme

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
	case *Macro:
		fmt.Fprintf(sb, "#<syntax %s>", x.Name)
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
	case *Env:
		sb.WriteString("#<environment>")
	case *Hashtable:
		sb.WriteString("#<hashtable>")
	case *Channel:
		state := "open"
		if x.isClosed() {
			state = "closed"
		}
		fmt.Fprintf(sb, "#<channel cap=%d %s>", x.capacity, state)
	case *ForeignLibrary:
		fmt.Fprintf(sb, "#<foreign-library %s>", x.Name)
	case *Mutex:
		sb.WriteString("#<mutex>")
	case *WaitGroup:
		fmt.Fprintf(sb, "#<waitgroup count=%d>", x.count)
	case *Once:
		state := "not run"
		if x.done {
			state = "done"
		}
		fmt.Fprintf(sb, "#<once %s>", state)
	case *Atomic:
		fmt.Fprintf(sb, "#<atomic %d>", x.n.Load())
	case *TcpListener:
		fmt.Fprintf(sb, "#<tcp-listener %s>", x.ln.Addr().String())
	case *MultipleValues:
		for i, mv := range x.Values {
			if i > 0 {
				sb.WriteString(" ")
			}
			p.print(sb, mv)
		}
	default:
		fmt.Fprintf(sb, "#<unknown %T>", v)
	}
}

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

func (p *printer) printChar(sb *strings.Builder, r rune) {
	if p.mode == modeDisplay {
		sb.WriteRune(r)
		return
	}
	switch r {
	case ' ':
		sb.WriteString("#\\space")
	case '\n':
		sb.WriteString("#\\newline")
	case '\t':
		sb.WriteString("#\\tab")
	case '\r':
		sb.WriteString("#\\return")
	case 0:
		sb.WriteString("#\\null")
	case 7:
		sb.WriteString("#\\alarm")
	case 8:
		sb.WriteString("#\\backspace")
	case 27:
		sb.WriteString("#\\escape")
	case 127:
		sb.WriteString("#\\delete")
	default:
		if r < 32 {
			fmt.Fprintf(sb, "#\\x%x", r)
		} else {
			sb.WriteString("#\\")
			sb.WriteRune(r)
		}
	}
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
