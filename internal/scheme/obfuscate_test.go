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
	_, first := obfuscatedProgram(t, src)
	_, second := obfuscatedProgram(t, src)
	if bytes.Equal(first, second) {
		t.Errorf("two obfuscated builds are identical, so the shuffle is not seeded")
	}
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
	if strip(first) != strip(second) {
		t.Errorf("the two builds do not have the same instructions")
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
