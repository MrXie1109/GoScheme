package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A bundled executable is an interpreter binary with a script appended to it:
//
//	[ interpreter ][ script ][ name ][ magic ][ trailer length ]
//
// Appending data to an ELF, PE or Mach-O image is harmless — the loader reads
// the headers it knows and ignores the tail — so the interpreter keeps working
// normally, and at startup it checks its own tail for the magic.  That makes a
// script into a single self-contained executable without needing a compiler,
// or anything else, on the machine that runs it.
const (
	bundleMagic = "GOSCHEME1" // 9 bytes
	// bundleTail is the fixed part of the trailer: the magic and the length.
	bundleTail = int64(len(bundleMagic) + 8)
	// bundleHeader is the fixed part in front of the script and its name.
	bundleHeader = int64(16)
)

var errNotBundled = errors.New("not a bundled executable")

// bundleInfo is the script carried by a bundled executable.
type bundleInfo struct {
	Script []byte
	Name   string
}

// readBundle reads the bundle trailer of the executable at path.
func readBundle(path string) (*bundleInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if size < bundleTail+bundleHeader {
		return nil, errNotBundled
	}

	// The last bytes are the magic followed by the trailer length.
	tail := make([]byte, bundleTail)
	if _, err := f.ReadAt(tail, size-bundleTail); err != nil {
		return nil, err
	}
	if string(tail[:len(bundleMagic)]) != bundleMagic {
		return nil, errNotBundled
	}
	trailerLen := int64(binary.BigEndian.Uint64(tail[len(bundleMagic):]))
	if trailerLen < bundleTail+bundleHeader || trailerLen > size {
		return nil, errNotBundled
	}

	trailer := make([]byte, trailerLen)
	if _, err := f.ReadAt(trailer, size-trailerLen); err != nil {
		return nil, err
	}
	scriptLen := int64(binary.BigEndian.Uint64(trailer[0:8]))
	nameLen := int64(binary.BigEndian.Uint64(trailer[8:16]))
	if scriptLen < 0 || nameLen < 0 || bundleHeader+scriptLen+nameLen+bundleTail != trailerLen {
		return nil, errNotBundled
	}
	info := &bundleInfo{
		Script: trailer[16 : 16+scriptLen],
		Name:   string(trailer[16+scriptLen : 16+scriptLen+nameLen]),
	}
	if info.Name == "" {
		info.Name = "script"
	}
	return info, nil
}

// writeBundle writes a copy of interpreter to out with script appended to it.
func writeBundle(interpreter, out, name string, script []byte) error {
	if _, err := readBundle(interpreter); err == nil {
		return fmt.Errorf("%s is already a bundle", interpreter)
	}
	in, err := os.Open(interpreter)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}

	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm()|0o111)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, in); err != nil {
		return err
	}
	// Trailer: [scriptLen][nameLen][script][name][magic][trailerLen]
	var head [16]byte
	binary.BigEndian.PutUint64(head[0:8], uint64(len(script)))
	binary.BigEndian.PutUint64(head[8:16], uint64(len(name)))
	if _, err := f.Write(head[:]); err != nil {
		return err
	}
	if _, err := f.Write(script); err != nil {
		return err
	}
	if _, err := f.WriteString(name); err != nil {
		return err
	}
	trailerLen := bundleHeader + int64(len(script)+len(name)) + bundleTail
	var num [8]byte
	if _, err := io.WriteString(f, bundleMagic); err != nil {
		return err
	}
	binary.BigEndian.PutUint64(num[:], uint64(trailerLen))
	if _, err := f.Write(num[:]); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(out, st.Mode().Perm()|0o111)
}

// runBuild implements "goscheme build script.scm [-o output] [-i interpreter]".
func runBuild(args []string) int {
	var scriptPath, out, interpreter string
	for i := 0; i < len(args); i++ {
		a := args[i]
		need := func(what string) (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "goscheme build: %s requires an argument\n", what)
				return "", false
			}
			i++
			return args[i], true
		}
		switch a {
		case "-o", "--output":
			v, ok := need("--output")
			if !ok {
				return 2
			}
			out = v
		case "-i", "--interpreter":
			v, ok := need("--interpreter")
			if !ok {
				return 2
			}
			interpreter = v
		case "-h", "--help":
			buildUsage(os.Stdout)
			return 0
		default:
			if strings.HasPrefix(a, "-") && a != "-" {
				fmt.Fprintf(os.Stderr, "goscheme build: unknown option %s\n", a)
				buildUsage(os.Stderr)
				return 2
			}
			if scriptPath != "" {
				fmt.Fprintf(os.Stderr, "goscheme build: only one script may be given\n")
				return 2
			}
			scriptPath = a
		}
	}
	if scriptPath == "" {
		buildUsage(os.Stderr)
		return 2
	}
	if interpreter == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "goscheme build: cannot find the interpreter: %v\n", err)
			return 1
		}
		interpreter = exe
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme build: %v\n", err)
		return 1
	}
	if out == "" {
		out = defaultOutput(scriptPath, interpreter)
	}
	if same, err := sameFile(scriptPath, out); err == nil && same {
		fmt.Fprintf(os.Stderr, "goscheme build: refusing to overwrite the script %s\n", scriptPath)
		return 1
	}
	if err := writeBundle(interpreter, out, filepath.Base(scriptPath), script); err != nil {
		fmt.Fprintf(os.Stderr, "goscheme build: %v\n", err)
		return 1
	}
	// A Mach-O binary carries a code signature that appending to it
	// invalidates, so re-sign it ad hoc when we can.
	resignIfNeeded(out)
	return 0
}

// defaultOutput derives the executable name from the script and the
// interpreter, next to the script: dir/prog.scm -> dir/prog (or dir/prog.exe
// when bundling for Windows).
func defaultOutput(scriptPath, interpreter string) string {
	out := scriptPath
	for _, ext := range []string{".scm", ".ss", ".sls", ".sld"} {
		if strings.HasSuffix(strings.ToLower(out), ext) {
			out = out[:len(out)-len(ext)]
			break
		}
	}
	if strings.HasSuffix(strings.ToLower(interpreter), ".exe") &&
		!strings.HasSuffix(strings.ToLower(out), ".exe") {
		out += ".exe"
	}
	return out
}

func sameFile(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(sa, sb), nil
}

func buildUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: goscheme build <script> [-o <output>] [-i <interpreter>]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Writes a standalone executable that runs <script>: a copy of the")
	fmt.Fprintln(w, "interpreter with the script bound to it.  The result needs nothing")
	fmt.Fprintln(w, "else on the machine that runs it.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  -o, --output FILE       name of the executable (default: the script")
	fmt.Fprintln(w, "                          name without its .scm extension)")
	fmt.Fprintln(w, "  -i, --interpreter FILE  interpreter to bind (default: this program;")
	fmt.Fprintln(w, "                          use e.g. dist/goscheme-windows-amd64.exe to")
	fmt.Fprintln(w, "                          produce an executable for another platform)")
}
