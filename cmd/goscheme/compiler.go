// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// runCompile implements "goscheme compile script.scm [-o out.scmc]": the script
// is read, compiled to bytecode, and written out.  The compiled file runs
// without reading source again — see "goscheme file.scmc".
func runCompile(args []string) int {
	var scriptPath, out string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-o", "--output":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "goscheme compile: -o requires an argument")
				return 2
			}
			i++
			out = args[i]
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
		fmt.Fprintln(os.Stderr, "usage: goscheme compile <script> [-o <output.scmc>]")
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
	compiled, total := prog.Compiled()
	fmt.Printf("%s: %d of %d top-level forms compiled to bytecode\n", out, compiled, total)
	return 0
}
