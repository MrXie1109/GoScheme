// Command goscheme is a Scheme (R7RS) interpreter.
//
// Usage:
//
//	goscheme [options] [file] [argument ...]
//
// Options:
//
//	-e expr    evaluate expr (repeatable)
//	-i         enter the REPL after loading the file
//	-q         do not print the banner in interactive mode
//	-v         print the version
//
// With no file and no -e the interpreter starts an interactive REPL.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"goscheme/internal/scheme"
)

const version = "GoScheme 1.0 (R7RS)"

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]
	var files []string
	var exprs []string
	interactive := false
	quiet := false
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		switch a {
		case "-e", "--eval":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "goscheme: -e requires an argument")
				return 2
			}
			i++
			exprs = append(exprs, args[i])
		case "-i", "--interactive":
			interactive = true
		case "-q", "--quiet":
			quiet = true
		case "-v", "--version":
			fmt.Println(version)
			return 0
		case "-h", "--help":
			usage()
			return 0
		default:
			fmt.Fprintf(os.Stderr, "goscheme: unknown option %s\n", a)
			usage()
			return 2
		}
	}
	rest := args[i:]
	if len(rest) > 0 {
		files = append(files, rest[0])
		rest = rest[1:]
	}

	m := scheme.NewMachine()
	m.Args = append([]string{"goscheme"}, append(append([]string{}, files...), rest...)...)

	for _, e := range exprs {
		if code := evalString(m, e, "(command line)"); code != 0 {
			return code
		}
	}
	for _, f := range files {
		if code := loadFile(m, f); code != 0 {
			return code
		}
	}
	if interactive || (len(files) == 0 && len(exprs) == 0) {
		repl(m, quiet)
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: goscheme [-i] [-q] [-e expr] [file] [args...]")
}

func evalString(m *scheme.Machine, src, name string) int {
	r := scheme.NewStringReader(src)
	r.Source = name
	forms, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
		return 1
	}
	for _, f := range forms {
		if _, err := m.Run(f, m.Global); err != nil {
			return reportError(err)
		}
	}
	return 0
}

func loadFile(m *scheme.Machine, path string) int {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
		return 1
	}
	r := scheme.NewStringReader(string(data))
	r.Source = abs
	forms, err := r.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme: %v\n", err)
		return 1
	}
	m.AddLoadPath(filepath.Dir(abs))
	for _, f := range forms {
		if _, err := m.Run(f, m.Global); err != nil {
			return reportError(err)
		}
	}
	return 0
}

func reportError(err error) int {
	if ee, ok := err.(*scheme.ExitError); ok {
		return ee.Code
	}
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	return 1
}

func repl(m *scheme.Machine, quiet bool) {
	if !quiet {
		fmt.Printf("%s\n", version)
		fmt.Println("Type (exit) or press Ctrl-D to leave.")
	}
	in := bufio.NewReader(os.Stdin)
	prompt := func() { fmt.Print("> ") }
	prompt()
	var buf strings.Builder
	for {
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			fmt.Println()
			return
		}
		buf.WriteString(line)
		src := buf.String()
		r := scheme.NewStringReader(src)
		r.Source = "<stdin>"
		forms, rerr := r.ReadAll()
		if rerr != nil {
			if rerr == io.EOF || isIncomplete(rerr) {
				prompt()
				continue
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", rerr)
			buf.Reset()
			prompt()
			continue
		}
		buf.Reset()
		for _, f := range forms {
			v, err := m.Run(f, m.Global)
			if err != nil {
				if _, ok := err.(*scheme.ExitError); ok {
					return
				}
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				continue
			}
			if _, un := v.(scheme.Unspecified); !un {
				fmt.Println(scheme.WriteToString(v))
			}
		}
		prompt()
	}
}

func isIncomplete(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "unterminated") ||
		strings.Contains(msg, "end of input") ||
		strings.Contains(msg, "end of file")
}
