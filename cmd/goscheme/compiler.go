// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// runCompile implements "goscheme compile script.scm [-o out.scmc]": the script
// is read, compiled to bytecode, and written out.  The compiled file runs
// without reading source again — see "goscheme file.scmc".
func runCompile(args []string) int {
	var scriptPath, out string
	obfuscate := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-o", "--output":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "goscheme compile: -o requires an argument")
				return 2
			}
			i++
			out = args[i]
		case "-obfuscate", "--obfuscate":
			obfuscate = true
		default:
			if strings.HasPrefix(a, "-") && a != "-" {
				fmt.Fprintf(os.Stderr, "goscheme compile: unknown option %s\n", a)
				return 2
			}
			if scriptPath != "" {
				fmt.Fprintln(os.Stderr, "goscheme compile: one script at a time")
				return 2
			}
			scriptPath = a
		}
	}
	if scriptPath == "" {
		fmt.Fprintln(os.Stderr, "usage: goscheme compile <script> [-o <output.scmc>] [-obfuscate]")
		return 2
	}
	if out == "" {
		base := strings.TrimSuffix(scriptPath, filepath.Ext(scriptPath))
		out = base + ".scmc"
	}
	abs, err := filepath.Abs(scriptPath)
	if err != nil {
		abs = scriptPath
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}
	r := scheme.NewStringReader(string(data))
	r.Source = abs
	forms, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}
	m := scheme.NewMachine()
	m.Args = append([]string{scriptPath}, args...)
	m.AddLoadPath(filepath.Dir(abs))
	prog, err := scheme.CompileProgram(m, forms, m.Global)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}
	if obfuscate {
		scheme.Obfuscate(prog)
		scheme.ObfuscateGlobals(prog, m)
	}
	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}
	if err := scheme.WriteBytecode(f, prog); err != nil {
		f.Close()
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}
	// The file is written executable, because it starts with a shebang and can
	// be run as it stands:
	//
	//	goscheme compile prog.scm -o prog.scmc && ./prog.scmc
	//
	// The mode is set explicitly rather than left to the umask, and the file's
	// existing mode is kept when it has one, so that compiling over a file does
	// not quietly change permissions the user chose.
	if err := makeExecutable(out); err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}
	// Nothing is printed on success: no news is the good news, and the file
	// that was written is the evidence.  A form the compiler declined is not
	// news either — it is in the file as source and runs interpreted.
	return 0
}

// makeExecutable adds the execute bits that match the read bits, leaving the
// rest of the mode alone.  It does nothing on Windows, where the concept does
// not apply and the call would fail.
func makeExecutable(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := st.Mode().Perm()
	// Read implies execute, bit for bit: rw-r--r-- becomes rwxr-xr-x.
	exec := (mode & 0444) >> 2
	if mode&exec == exec {
		return nil
	}
	return os.Chmod(path, mode|exec)
}
