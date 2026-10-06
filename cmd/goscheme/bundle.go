// SPDX-License-Identifier: MIT

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// A bundled executable is an interpreter binary with a program appended to it:
//
//	[ interpreter ][ payload ][ name ][ magic ][ trailer length ]
//
// Appending data to an ELF, PE or Mach-O image is harmless — the loader reads
// the headers it knows and ignores the tail — so the interpreter keeps working
// normally, and at startup it checks its own tail for the magic.  That makes a
// script into a single self-contained executable without needing a compiler,
// or anything else, on the machine that runs it.
//
// The payload is the script with its comments and layout removed — what
// `goscheme pack` produces — so the program does not read the original file, and
// the source it runs is exactly what it would have run anyway.  The magic says
// how to read it: a bundle written before there was packing to do carries the
// script as it was typed, and is still read.
const (
	// bundleMagicPacked marks a bundle whose payload is packed source.
	bundleMagicPacked = "GOSCHEME2" // 9 bytes
	// bundleMagicPlain marks a bundle whose payload is the script as typed.
	bundleMagicPlain = "GOSCHEME1" // 9 bytes
	// bundleTail is the fixed part of the trailer: the magic and the length.
	bundleTail = int64(len(bundleMagicPacked) + 8)
	// bundleHead is the fixed part in front of the payload and its name: the
	// payload length, the name length, and which kind of payload this is.
	bundleHead = int64(17)
	// bundleHeadV1 is the head of a script-only bundle, which has no kind.
	bundleHeadV1 = int64(16)
)

// Payload kinds.
//
// There is one kind now that the compiled file format is gone, and the field
// stays because it is part of the trailer's layout: a reader has to know how
// many bytes to expect, whether or not the distinction it records still exists.
const (
	kindSource = byte(0)
	// kindRetired was the compiled format, which no longer exists.  A bundle
	// carrying it is not readable any more, and is reported as such rather than
	// misread as source.
	kindRetired = byte(1)
)

var errNotBundled = errors.New("not a bundled executable")

// bundleInfo is the program carried by a bundled executable.
type bundleInfo struct {
	// Payload is the packed script text.
	Payload []byte
	Name    string
	Kind    byte
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
	if size < bundleTail+bundleHeadV1 {
		return nil, errNotBundled
	}

	// The last bytes are the magic followed by the trailer length.
	tail := make([]byte, bundleTail)
	if _, err := f.ReadAt(tail, size-bundleTail); err != nil {
		return nil, err
	}
	magic := string(tail[:len(bundleMagicPacked)])
	head := bundleHeadV1
	kind := kindSource
	hasKind := false
	switch magic {
	case bundleMagicPacked:
		head, hasKind = bundleHead, true // the kind byte is in the trailer
	case bundleMagicPlain:
	default:
		return nil, errNotBundled
	}
	trailerLen := int64(binary.BigEndian.Uint64(tail[len(bundleMagicPacked):]))
	if trailerLen < bundleTail+head || trailerLen > size {
		return nil, errNotBundled
	}

	trailer := make([]byte, trailerLen)
	if _, err := f.ReadAt(trailer, size-trailerLen); err != nil {
		return nil, err
	}
	payloadLen := int64(binary.BigEndian.Uint64(trailer[0:8]))
	nameLen := int64(binary.BigEndian.Uint64(trailer[8:16]))
	if hasKind {
		kind = trailer[16]
	}
	if payloadLen < 0 || nameLen < 0 || head+payloadLen+nameLen+bundleTail != trailerLen {
		return nil, errNotBundled
	}
	// A bundle built by a version that had a compiled format carries that
	// format, which this one cannot read.  Saying so is the whole job here:
	// treating the bytes as source would report a syntax error somewhere inside
	// a binary, which tells the user nothing about what actually happened.
	if kind == kindRetired {
		return nil, fmt.Errorf("%s: this file was packed by an older version, "+
			"whose compiled format is no longer supported; pack it again", path)
	}
	info := &bundleInfo{
		Payload: trailer[head : head+payloadLen],
		Name:    string(trailer[head+payloadLen : head+payloadLen+nameLen]),
		Kind:    kind,
	}
	if info.Name == "" {
		info.Name = "script"
	}
	return info, nil
}

// writeBundle writes a copy of interpreter to out with a program appended to
// it.  The payload is packed source, and kind says how to read it.
func writeBundle(interpreter, out, name string, kind byte, payload []byte) error {
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
	// Trailer: [payloadLen][nameLen][kind][payload][name][magic][trailerLen]
	var head [bundleHead]byte
	binary.BigEndian.PutUint64(head[0:8], uint64(len(payload)))
	binary.BigEndian.PutUint64(head[8:16], uint64(len(name)))
	head[16] = kind
	if _, err := f.Write(head[:]); err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		return err
	}
	if _, err := f.WriteString(name); err != nil {
		return err
	}
	trailerLen := bundleHead + int64(len(payload)+len(name)) + bundleTail
	var num [8]byte
	if _, err := io.WriteString(f, bundleMagicPacked); err != nil {
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

// runPack implements "goscheme pack script.scm [-o output] [-i interpreter]".
func runPack(args []string) int {
	var scriptPath, out, interpreter string
	static := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		need := func(what string) (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "goscheme pack: %s requires an argument\n", what)
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
		case "-static", "--static":
			static = true
		case "-h", "--help":
			packUsage(os.Stdout)
			return 0
		default:
			if strings.HasPrefix(a, "-") && a != "-" {
				fmt.Fprintf(os.Stderr, "goscheme pack: unknown option %s\n", a)
				packUsage(os.Stderr)
				return 2
			}
			if scriptPath != "" {
				fmt.Fprintf(os.Stderr, "goscheme pack: only one script may be given\n")
				return 2
			}
			scriptPath = a
		}
	}
	if scriptPath == "" {
		packUsage(os.Stderr)
		return 2
	}
	if interpreter == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "goscheme pack: cannot find the interpreter: %v\n", err)
			return 1
		}
		interpreter = exe
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goscheme pack: %v\n", err)
		return 1
	}
	if out == "" {
		out = defaultOutput(interpreter)
	}
	if same, err := sameFile(scriptPath, out); err == nil && same {
		fmt.Fprintf(os.Stderr, "goscheme pack: refusing to overwrite the script %s\n", scriptPath)
		return 1
	}
	if static {
		script, err = resolveStatic(scriptPath, script, staticSearchPath(scriptPath))
		if err != nil {
			fmt.Fprintf(os.Stderr, "goscheme pack: %v\n", err)
			return 1
		}
	}
	payload, kind, note := payloadFor(scriptPath, script)
	if err := writeBundle(interpreter, out, filepath.Base(scriptPath), kind, payload); err != nil {
		fmt.Fprintf(os.Stderr, "goscheme pack: %v\n", err)
		return 1
	}
	// Silent when the program compiled, because that is the expected case;
	// loud when it did not, because then the executable starts by reading
	// source and the user should know why.
	if note != "" {
		fmt.Fprintf(os.Stderr, "%s: %s\n", out, note)
	}
	// A Mach-O binary carries a code signature that appending to it
	// invalidates, so re-sign it ad hoc when we can.
	resignIfNeeded(out)
	return 0
}

// payloadFor turns a script into what a bundle carries: the script with its
// comments and layout removed.
//
// Packing is what a bundle does now that there is no compiled file format.  It
// is not an optimization — reading either form costs about the same — but it
// makes the executable carry the program in one canonical shape, and it drops
// the comments and blank lines a shipped program does not need.
//
// Packing can fail, on a script the reader cannot read far enough to rewrite,
// and then the original text is bound instead: a bundle that starts by reading
// source is slower to prepare and never wrong, and the note says which happened.
func payloadFor(scriptPath string, script []byte) (payload []byte, kind byte, note string) {
	abs, err := filepath.Abs(scriptPath)
	if err != nil {
		abs = scriptPath
	}
	packed, err := scheme.PackSource(string(script), abs)
	if err != nil {
		return script, kindSource,
			fmt.Sprintf("packing failed (%v), so the script is bound as it was written", err)
	}
	return packed, kindSource, ""
}

// staticSearchPath is where -static looks for libraries: the script's own
// directory, then GOSCHEME_LIBRARY_PATH, then the working directory — the same
// order the interpreter uses at run time.
func staticSearchPath(scriptPath string) []string {
	dirs := []string{dirOf(scriptPath)}
	if env := os.Getenv("GOSCHEME_LIBRARY_PATH"); env != "" {
		for _, d := range strings.Split(env, string(os.PathListSeparator)) {
			if d != "" {
				dirs = append(dirs, d)
			}
		}
	}
	return append(dirs, ".")
}

// defaultOutput is the name used when -o is omitted: a.out, or a.exe when
// binding a Windows interpreter, in the current directory — the same default a
// C compiler uses.  The name follows the interpreter being bound rather than the
// host, because the bundle is meant to run where that interpreter runs.
func defaultOutput(interpreter string) string {
	if strings.HasSuffix(strings.ToLower(interpreter), ".exe") {
		return "a.exe"
	}
	return "a.out"
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

func packUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: goscheme pack <script> [-o <output>] [-i <interpreter>] [-static]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Writes a standalone executable that runs <script>: a copy of the")
	fmt.Fprintln(w, "interpreter with the script bound to it, packed as source — comments")
	fmt.Fprintln(w, "and layout removed, the program kept.  The result needs nothing else on")
	fmt.Fprintln(w, "the machine that runs it.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  -o, --output FILE       name of the executable (default: a.out, or")
	fmt.Fprintln(w, "                          a.exe when binding a Windows interpreter)")
	fmt.Fprintln(w, "  -i, --interpreter FILE  interpreter to bind (default: this program;")
	fmt.Fprintln(w, "                          use e.g. dist/goscheme-windows-amd64.exe to")
	fmt.Fprintln(w, "                          produce an executable for another platform)")
	fmt.Fprintln(w, "  -static                 bake in every library the script imports, so")
	fmt.Fprintln(w, "                          the executable needs no library files beside")
	fmt.Fprintln(w, "                          it (they are resolved at build time)")
}
