// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// runCompile implements "goscheme compile script.scm [-o out] [--emit-llvm]":
// the script is compiled to a native program through LLVM.
//
// The pipeline is: read the script, generate LLVM IR for it, run the IR through
// `opt` to optimise it, run that through `llc` to get an object file, and link
// the object against the GoScheme runtime library.  The result is a program that
// needs nothing but itself.
//
//	goscheme compile prog.scm                 # a.out
//	goscheme compile prog.scm -o prog         # named
//	goscheme compile prog.scm --emit-llvm     # prog.ll on stdout, nothing built
//	goscheme compile prog.scm -O0             # no optimisation
//
// The LLVM tools are found on PATH, and their absence is reported rather than
// worked around: a compiler that cannot find its back end should say so.
func runCompile(args []string) int {
	var scriptPath, out string
	emitLLVM := false
	optLevel := "2"
	keepTemps := false

	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-o", "--output":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "goscheme compile: -o requires an argument")
				return 2
			}
			i++
			out = args[i]
		case "--emit-llvm", "-S":
			// Write the LLVM IR and stop: no opt, no llc, no link.  It is what
			// a person who wants to read the generated code asks for, and what
			// the tests use to check the generator without a toolchain.
			emitLLVM = true
		case "--keep-temps":
			keepTemps = true
		case "-O0", "-O1", "-O2", "-O3":
			optLevel = a[2:]
		case "-h", "--help":
			compileUsage(os.Stdout)
			return 0
		default:
			if len(a) > 1 && a[0] == '-' {
				fmt.Fprintf(os.Stderr, "goscheme compile: unknown option %s\n", a)
				compileUsage(os.Stderr)
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
		compileUsage(os.Stderr)
		return 2
	}
	return compileToNative(scriptPath, out, optLevel, emitLLVM, keepTemps)
}

func compileUsage(w *os.File) {
	fmt.Fprintln(w, "usage: goscheme compile <script> [-o <output>] [--emit-llvm] [-O0..-O3]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Compiles <script> to a native executable through LLVM: the script is")
	fmt.Fprintln(w, "translated to LLVM IR, optimised with opt, assembled with llc, and")
	fmt.Fprintln(w, "linked against the GoScheme runtime.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  -o, --output FILE  where to write the executable (default: a.out)")
	fmt.Fprintln(w, "  --emit-llvm, -S    write the LLVM IR instead of building anything")
	fmt.Fprintln(w, "  -O0..-O3           optimisation level passed to opt (default: -O2)")
	fmt.Fprintln(w, "  --keep-temps       keep the intermediate .ll and .o files")
}

// compileToNative runs the LLVM pipeline over a script.
func compileToNative(scriptPath, out, optLevel string, emitLLVM, keepTemps bool) int {
	abs, err := filepath.Abs(scriptPath)
	if err != nil {
		abs = scriptPath
	}
	source, err := os.ReadFile(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}

	// The reader runs first, so that a script with a syntax error is reported
	// as one without invoking a single LLVM tool.
	prog, err := scheme.CompileToIR(string(source), abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}

	if emitLLVM {
		if out == "" {
			_, err := os.Stdout.WriteString(prog.IR)
			if err != nil {
				fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
				return 1
			}
			return 0
		}
		if err := os.WriteFile(out, []byte(prog.IR), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
			return 1
		}
		return 0
	}

	if out == "" {
		out = "a.out"
		if runtime.GOOS == "windows" {
			out = "a.exe"
		}
	}
	if err := buildNative(prog, abs, out, optLevel, keepTemps); err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}
	return 0
}

// buildNative takes generated IR the rest of the way: opt, llc, and the link
// against the runtime library.
func buildNative(prog *scheme.IRProgram, scriptPath, out, optLevel string, keepTemps bool) error {
	dir, err := os.MkdirTemp("", "goscheme-compile-")
	if err != nil {
		return err
	}
	if !keepTemps {
		defer os.RemoveAll(dir)
	}

	llPath := filepath.Join(dir, "prog.ll")
	if err := os.WriteFile(llPath, []byte(prog.IR), 0o644); err != nil {
		return err
	}

	// opt: the IR in, optimised IR out.  -O<n> may be given as a level rather
	// than as the passes, which is what a user expects from a -O flag.
	optPath := filepath.Join(dir, "prog.opt.ll")
	if err := runTool("opt", "-O"+optLevel, "-S", llPath, "-o", optPath); err != nil {
		return err
	}

	objPath := filepath.Join(dir, "prog.o")
	if err := runTool("llc", "-filetype=obj", optPath, "-o", objPath); err != nil {
		return err
	}

	// The link: the object, then the runtime library, then whatever the
	// platform needs beside it.
	lib, err := scheme.RuntimeLibraryPath()
	if err != nil {
		return err
	}
	linkArgs := append([]string{objPath, lib, "-o", out}, scheme.RuntimeLinkFlags()...)
	if err := runTool(scheme.RuntimeLinker(), linkArgs...); err != nil {
		return err
	}
	return os.Chmod(out, 0o755)
}

// runTool runs one of the toolchain programs, reporting what it said when it
// fails.  A missing tool is a plain error rather than a panic: the toolchain is
// not part of the interpreter and its absence is a normal thing to hit.
func runTool(name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s not found on PATH; the LLVM toolchain is needed to compile (apt install llvm)", name)
	}
	cmd := exec.Command(path, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s: %s", name, msg)
	}
	return nil
}
