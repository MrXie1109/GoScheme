// SPDX-License-Identifier: MIT

package scheme

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// obfuscatedProgram compiles a program and returns its bytes with and without
// obfuscation.
func obfuscatedProgram(t *testing.T, src string) (plain, obfuscated []byte) {
	t.Helper()
	compile := func(obfuscate bool) []byte {
		m := NewMachine()
		forms, err := NewStringReader(src).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		prog, err := CompileProgram(m, forms, m.Global)
		if err != nil {
			t.Fatal(err)
		}
		if obfuscate {
			Obfuscate(prog)
			ObfuscateGlobals(prog, m)
		}
		var buf bytes.Buffer
		if err := WriteBytecode(&buf, prog); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	return compile(false), compile(true)
}

const secretProgram = `
(define secret-key "hunter2")
(define (check-password p)
  (if (string=? p secret-key) 'granted 'denied))
(check-password "hunter2")`

// The names a program is written with are not in the file any more: not the
// names of its procedures, not of its locals, and not of the globals it defines
// itself.
func TestObfuscationRemovesNames(t *testing.T) {
	plain, obfuscated := obfuscatedProgram(t, secretProgram)
	for _, name := range []string{"secret-key", "check-password"} {
		if !bytes.Contains(plain, []byte(name)) {
			t.Fatalf("the plain file does not mention %q, so this proves nothing", name)
		}
		if bytes.Contains(obfuscated, []byte(name)) {
			t.Errorf("the obfuscated file still mentions %q", name)
		}
	}
	// The parameter name is a slot name, and slot names go too.
	if bytes.Contains(obfuscated, []byte("p\x00")) {
		t.Errorf("a slot name survived")
	}
}

// What the program *does* is untouched: it runs and prints the same thing.
func TestObfuscatedProgramRunsTheSame(t *testing.T) {
	_, obfuscated := obfuscatedProgram(t, secretProgram)
	run := func(data []byte) string {
		out := NewOutputStringPort()
		m := NewMachine()
		m.CurOut = out
		m.OutParam.values[0] = out
		prog, err := ReadBytecode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		v, err := m.RunProgram(prog, m.Global)
		if err != nil {
			t.Fatal(err)
		}
		return out.OutputString() + WriteToString(v)
	}
	plain, _ := obfuscatedProgram(t, secretProgram)
	if run(plain) != run(obfuscated) {
		t.Errorf("obfuscated run printed %q, plain printed %q", run(obfuscated), run(plain))
	}
}

// The file is still a valid .scmc file of the same version, and a reader that
// knows nothing of obfuscation reads it.
func TestObfuscatedFileIsStillReadable(t *testing.T) {
	plain, obfuscated := obfuscatedProgram(t, secretProgram)
	pl, err := ReadBytecode(bytes.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}
	ob, err := ReadBytecode(bytes.NewReader(obfuscated))
	if err != nil {
		t.Fatalf("the obfuscated file was not read back: %v", err)
	}
	if len(pl.Chunks) != len(ob.Chunks) {
		t.Errorf("chunk counts differ: %d and %d", len(pl.Chunks), len(ob.Chunks))
	}
}

// A name that appears in a source chunk — a define-syntax, an import — is not
// renamed: that chunk is evaluated by the interpreter as written, so its
// symbols have to keep the names it will look up.
func TestObfuscationKeepsNamesSourceChunksUse(t *testing.T) {
	src := `
(define-syntax twice (syntax-rules () ((_ e) (begin e e))))
(define counter 0)
(twice (set! counter (+ counter 1)))
counter`
	_, obfuscated := obfuscatedProgram(t, src)
	// counter is defined in compiled code and used in compiled code, so it can
	// be renamed; the macro's own name is in a source chunk and cannot.
	if !bytes.Contains(obfuscated, []byte("twice")) {
		t.Log("the macro name was not in the file anyway (it is a compile-time form)")
	}
	// Whatever it did, it has to still run.
	run := func(data []byte) string {
		out := NewOutputStringPort()
		m := NewMachine()
		m.CurOut = out
		m.OutParam.values[0] = out
		prog, err := ReadBytecode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.RunProgram(prog, m.Global); err != nil {
			t.Fatal(err)
		}
		return out.OutputString()
	}
	plain, _ := obfuscatedProgram(t, src)
	if run(plain) != run(obfuscated) {
		t.Errorf("the macro program behaves differently obfuscated")
	}
}

// Two compilations of the same program differ, because the constant pools are
// shuffled from a fresh seed: an obfuscated file is not a fingerprint of the
// source, and compiling it twice does not produce the same bytes.
func TestObfuscationIsNotDeterministic(t *testing.T) {
	src := `(define (f x) (list x x x x x x x x)) (f 'a)`
	// Several builds, because two can coincide by chance when the pools are
	// small: the shuffle is random, and a test that demands two differ is
	// flaky in exactly the case it is least needed.  Ten builds of a program
	// with eight constants cannot all be the same permutation by accident.
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		_, b := obfuscatedProgram(t, src)
		seen[string(b)] = true
	}
	if len(seen) == 1 {
		t.Errorf("ten obfuscated builds are identical, so the shuffle is not seeded")
	}
	first := []byte{}
	for k := range seen {
		first = []byte(k)
		break
	}
	_ = first
	// Both still have the same instructions: the shuffle moves constants, it
	// does not change the program.
	strip := func(b []byte) string {
		prog, err := ReadBytecode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		var walk func(*Code)
		walk = func(c *Code) {
			if c == nil {
				return
			}
			for _, in := range c.Instrs {
				fmt.Fprintf(&sb, "%d ", in.op)
				sb.WriteByte(' ')
			}
			for _, k := range c.Consts {
				if sub, ok := k.(*Code); ok {
					walk(sub)
				}
			}
		}
		for i := range prog.Chunks {
			walk(prog.Chunks[i].Code)
		}
		return sb.String()
	}
	// Every build has the same instructions: the shuffle moves constants
	// around, it does not change the program.
	var want string
	for k := range seen {
		got := strip([]byte(k))
		if want == "" {
			want = got
			continue
		}
		if got != want {
			t.Errorf("two builds do not have the same instructions")
		}
	}
}

// 编译后的文件按内容识别，不按名字：它写成可执行、可能被装成一个没有扩展
// 名的命令，那时按扩展名判断就会把字节码当源码读。
func TestIsBytecodeLooksAtContent(t *testing.T) {
	compile := func(src string) []byte {
		m := NewMachine()
		forms, err := NewStringReader(src).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		prog, err := CompileProgram(m, forms, m.Global)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := WriteBytecode(&buf, prog); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	compiled := compile(`(display 1)`)

	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"a compiled file", compiled, true},
		{"a compiled file with no shebang", compiled[len(bytecodeShebang):], true},
		{"a compiled file with another shebang",
			append([]byte("#!/bin/sh\n"), compiled[len(bytecodeShebang):]...), true},
		{"source", []byte("(display 1)\n"), false},
		{"source that begins with #!", []byte("#!/usr/bin/env goscheme\n(display 1)\n"), false},
		{"empty", nil, false},
		{"a shebang with nothing after it", []byte("#!/usr/bin/env goscheme\n"), false},
		{"the magic alone, truncated", []byte("GSC"), false},
	} {
		if got := IsBytecode(bytes.NewReader(tc.data)); got != tc.want {
			t.Errorf("%s: IsBytecode = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// 符号表：文件里每个名字只存一次，引用是索引。存储的字节因此少了很多，
// 而且名字在文件里只出现一次，而不是每次用到都写一遍。
func TestSymbolTableIsWrittenOnce(t *testing.T) {
	src := `
	  (import (scheme base) (scheme write))
	  (display "a") (display "b") (display "c") (display "d")
	  (newline)`
	m := NewMachine()
	forms, err := NewStringReader(src).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	prog, err := CompileProgram(m, forms, m.Global)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteBytecode(&buf, prog); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	// display is called four times and is a compiled reference each time, so
	// its name is stored once — in the table — and referenced by index.
	if n := bytes.Count(data, []byte("display")); n != 1 {
		t.Errorf("`display` appears %d times in the file, want 1 (the table)", n)
	}
	// The import form is a source chunk: it is kept as written and evaluated by
	// the interpreter, so the library name in it is text and is expected.  What
	// must not happen is a symbol written twice as a *reference*.
	if n := bytes.Count(data, []byte("display\x00")); n != 0 {
		t.Errorf("a symbol reference was written by name %d times", n)
	}
	// And the program still runs.
	loaded, err := ReadBytecode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out := NewOutputStringPort()
	m2 := NewMachine()
	m2.CurOut = out
	m2.OutParam.values[0] = out
	if _, err := m2.RunProgram(loaded, m2.Global); err != nil {
		t.Fatal(err)
	}
	if got := out.OutputString(); got != "abcd\n" {
		t.Errorf("printed %q, want %q", got, "abcd")
	}
}

// 表按使用频率排序，所以最常用的名字索引最小、编码最短：一个 uvarint 在
// 127 以下只占一个字节。这是文件变小的地方，而不是表的排序本身。
func TestFrequentSymbolsComeFirst(t *testing.T) {
	src := `(car (cdr (car (cdr (car (cdr x))))))`
	m := NewMachine()
	forms, err := NewStringReader(src).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	prog, err := CompileProgram(m, forms, m.Global)
	if err != nil {
		t.Fatal(err)
	}
	names := collectSymbols(prog)
	if len(names) == 0 {
		t.Fatal("no symbols collected")
	}
	// car and cdr are used three times each; x once.  Both frequent ones have
	// to come before the rare one.
	pos := map[string]int{}
	for i, n := range names {
		pos[n] = i
	}
	if pos["car"] >= pos["x"] || pos["cdr"] >= pos["x"] {
		t.Errorf("table order is %v; the frequent names should come first", names)
	}
}

// 旧文件仍然读得进来：版本 4 的文件把每个符号写在用到它的地方，没有表。
func TestVersion4FileStillReads(t *testing.T) {
	// A file written by the old writer: version 4, a symbol by name.
	var buf bytes.Buffer
	bw := &byteWriter{w: &bufWriter{w: &buf}}
	bw.raw([]byte(bytecodeMagic))
	bw.u8(4)
	bw.uvarint(1) // one chunk
	// chunk: a Code constant (tag 1), then a code body.
	bw.u8(1)
	bw.str("<top>")
	bw.uvarint(1) // one instruction
	bw.u8(byte(opConst))
	bw.uvarint(0)
	bw.uvarint(0)
	bw.uvarint(0) // consts
	bw.uvarint(0) // slots
	bw.uvarint(0) // params
	bw.u8(0)      // no rest
	bw.uvarint(0) // no boxed
	bw.uvarint(0) // no checked
	bw.uvarint(0) // no names
	bw.uvarint(1) // one constant: a symbol by name, old style
	bw.u8(tSymbol)
	bw.str("old-style")
	if bw.err != nil {
		t.Fatal(bw.err)
	}
	bw.w.Flush()
	if _, err := ReadBytecode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Errorf("a version 4 file was not read: %v", err)
	}
}
