// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"io"
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
	explain := false
	staticLink := false
	compileAll := false

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
		case "--explain":
			explain = true
		case "-compile-all":
			// Emit every procedure the generator understands, including the ones
			// the cost rule would leave to the interpreter.  It is a judgement
			// about speed and this is how a caller overrules it.
			compileAll = true
		case "-static":
			// Link the runtime into the program rather than against the shared
			// library. Eight megabytes instead of kilobytes, and nothing to
			// ship beside it: the default is shared, so this is the opt-in.
			staticLink = true
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
	return compileToNative(scriptPath, out, optLevel, emitLLVM, keepTemps, explain, staticLink, compileAll)
}

func compileUsage(w *os.File) {
	fmt.Fprintln(w, "usage: goscheme compile <script> [-o <output>] [--emit-llvm] [-O0..-O3] [-static]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Compiles <script> to a native executable through LLVM: the script is")
	fmt.Fprintln(w, "translated to LLVM IR, optimised with opt, assembled with llc, and")
	fmt.Fprintln(w, "linked against the GoScheme runtime.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  -o, --output FILE  where to write the executable (default: a.out)")
	fmt.Fprintln(w, "  --emit-llvm, -S    write the LLVM IR instead of building anything")
	fmt.Fprintln(w, "  -O0..-O3           optimisation level passed to opt (default: -O2)")
	fmt.Fprintln(w, "  -static            link the runtime into the program")
	fmt.Fprintln(w, "  -compile-all       emit every procedure, ignoring the cost rule")
	fmt.Fprintln(w, "  --keep-temps       keep the intermediate .ll and .o files")
	fmt.Fprintln(w, "  --explain          list every procedure left to the interpreter, and why")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "The runtime is linked as a shared library by default, so the program holds")
	fmt.Fprintln(w, "only its own machine code — a few kilobytes rather than the several")
	fmt.Fprintln(w, "megabytes the interpreter takes.  It needs that library at run time, and the")
	fmt.Fprintln(w, "program is linked with an rpath pointing at it, so it runs where it was")
	fmt.Fprintln(w, "built; to move it elsewhere, copy the library beside it or set")
	fmt.Fprintln(w, "LD_LIBRARY_PATH.  -static links the runtime in instead, for a program that")
	fmt.Fprintln(w, "has to run on a machine where the library is not installed.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "-compile-all overrules the cost rule, which otherwise leaves a procedure")
	fmt.Fprintln(w, "whose only work is a call into the runtime to the interpreter.  That rule is")
	fmt.Fprintln(w, "right — such a procedure measured 2 to 3 times slower compiled — but it is a")
	fmt.Fprintln(w, "judgement about speed, and a judgement should be visible and overridable")
	fmt.Fprintln(w, "rather than silent.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "The compiler takes the procedures whose bodies are pure computations and")
	fmt.Fprintln(w, "leaves the rest to the interpreter, so a program usually comes out part")
	fmt.Fprintln(w, "machine code and part interpreted.  What it took is reported on stderr;")
	fmt.Fprintln(w, "--explain names what it left behind and why.")
}

// reportSplit says what the compiler took and what it left to the interpreter.
//
// It is not decoration.  The compiler is partial by design, so a program can
// come out with no machine code in it at all, still run correctly, and run at
// exactly the speed it would have without compiling — which is the worst way to
// fail, because nothing says so and the binary's size suggests otherwise.  A
// build that compiled nothing therefore says so out loud.  Everything printed
// here was already computed for this purpose and was previously discarded.
func reportSplit(w io.Writer, prog *scheme.IRProgram, explain bool) {
	// Silence is right when the program compiled and the interpreter kept
	// nothing: that is the good case, and a line on every build would train the
	// reader to skip it.
	if !prog.CompiledAnything() {
		fmt.Fprintf(w, "goscheme compile: warning: nothing was compiled to native code\n")
		fmt.Fprintf(w, "  This program will run in the interpreter, at the speed it would have\n")
		fmt.Fprintf(w, "  had without compiling.  The binary is large because the runtime is\n")
		fmt.Fprintf(w, "  linked into it, not because any of the program is machine code.\n")
	} else {
		if prog.Runtime == 0 {
			// Everything compiled: no news, which is the good case, unless the
			// caller asked for the details.
			if !explain || len(prog.Refused) == 0 {
				return
			}
		} else {
			fmt.Fprintf(w, "goscheme compile: %d compiled to native code, %d left to the interpreter\n",
				prog.Native+prog.TopNative, prog.Runtime)
		}
	}
	switch {
	case explain && len(prog.Refused) > 0:
		fmt.Fprintf(w, "  left to the interpreter:\n")
		for _, line := range prog.Refused {
			fmt.Fprintf(w, "    %s\n", line)
		}
	case len(prog.Refused) > 0:
		fmt.Fprintf(w, "  run with --explain to list what was left behind, and why\n")
	}
}

// compileToNative runs the LLVM pipeline over a script.
func compileToNative(scriptPath, out, optLevel string, emitLLVM, keepTemps, explain, staticLink, compileAll bool) int {
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
	prog, err := scheme.CompileToIRWith(string(source), abs, scheme.Options{CompileEverything: compileAll})
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}

	// The report goes to stderr, so writing the IR to stdout stays pure IR and
	// `goscheme compile -S x.scm > x.ll` still produces a file LLVM accepts.
	// It is printed before the -S return below for the same reason a person
	// reading the IR benefits most from it: that is when the absence of any
	// gs_lam_ function is about to be noticed.
	reportSplit(os.Stderr, prog, explain)

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
	if err := buildNative(prog, abs, out, optLevel, keepTemps, staticLink); err != nil {
		fmt.Fprintf(os.Stderr, "goscheme compile: %v\n", err)
		return 1
	}
	return 0
}

// buildNative takes generated IR the rest of the way: opt, llc, and the link
// against the runtime library.
func buildNative(prog *scheme.IRProgram, scriptPath, out, optLevel string, keepTemps, staticLink bool) error {
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
	//
	// The runtime is shared by default, so the program carries only its own
	// machine code — kilobytes rather than the eight megabytes the interpreter
	// costs.  -static links the runtime in instead, for a program that has to
	// run on a machine where the library is not installed.
	kind := scheme.RuntimeShared
	if staticLink {
		kind = scheme.RuntimeStatic
	}
	lib, err := scheme.RuntimeLibraryPath(kind)
	if err != nil {
		return err
	}
	libDir := ""
	if kind == scheme.RuntimeShared {
		libDir = filepath.Dir(lib)
	}
	linkArgs := append([]string{objPath, lib, "-o", out},
		scheme.RuntimeLinkFlags(kind, libDir)...)
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
