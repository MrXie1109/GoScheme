// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// This file holds the parts of the REPL that are conveniences rather than
// language: Tab completion, a history file that survives the session, and the
// comma commands.  They live here so that main.go stays about the interpreter.

// ------------------------------------------------------------------- history

// historyLimit is how many entries the history file keeps.  It is a variable so
// that a test can lower it instead of writing a thousand lines.
var historyLimit = 1000

// historyPath is where the REPL remembers what was typed.  GOSCHEME_HISTORY
// overrides it, and an empty result disables the file entirely.
func historyPath() string {
	if p := os.Getenv("GOSCHEME_HISTORY"); p != "" {
		if p == "off" || p == "-" {
			return ""
		}
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".goscheme_history")
}

// loadHistory reads the most recent entries, oldest first.
func loadHistory(path string) []string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > historyLimit {
		lines = lines[len(lines)-historyLimit:]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// appendHistory adds one entry.  A multi-line entry is a pasted program rather
// than something that was typed, so it is not remembered; the file is a list of
// lines and would otherwise lose its shape.
func appendHistory(path, line string) {
	if path == "" || strings.TrimSpace(line) == "" || strings.Contains(line, "\n") {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 64*1024 {
		trimHistory(path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// trimHistory rewrites the file with only the most recent entries.
func trimHistory(path string) {
	entries := loadHistory(path)
	if len(entries) == 0 {
		return
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	for _, e := range entries {
		fmt.Fprintln(f, e)
	}
	f.Close()
	os.Rename(tmp, path)
}

// ---------------------------------------------------------------- completion

// replCommands are the comma commands, and also what Tab completes at the start
// of a line.
var replCommands = []string{",help", ",bindings", ",libraries", ",time", ",quit"}

// replCompleter supplies Tab completion: Scheme names, library names after
// (import, and the comma commands.
type replCompleter struct {
	m *scheme.Machine
}

func (c *replCompleter) complete(line []rune, pos int) (int, []string) {
	start := pos
	for start > 0 && isNameChar(line[start-1]) {
		start--
	}
	word := string(line[start:pos])

	// A comma command is one word, so the comma is part of it.
	if pos > 0 && line[0] == ',' {
		start, word = 0, string(line[:pos])
	}
	if strings.HasPrefix(word, ",") {
		if out := withPrefix(replCommands, word); len(out) > 0 {
			return start, out
		}
	}
	// Inside (import ...) the unit being typed is the whole library name, from
	// its opening parenthesis, so that is what gets replaced.
	if open, ok := importSpecStart(line[:pos]); ok {
		return open, withPrefix(c.libraryNames(), string(line[open:pos]))
	}
	return start, withPrefix(c.schemeNames(), word)
}

// schemeNames is every name the interpreter knows: what has been defined, plus
// everything built in.
func (c *replCompleter) schemeNames() []string {
	seen := map[string]bool{}
	add := func(env *scheme.Env) {
		for sym := range env.Snapshot() {
			seen[sym.Name] = true
		}
	}
	add(c.m.Global)
	add(c.m.Builtin)
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// libraryNames is every library that can be imported: the ones built in, and
// the ones already loaded from files.
func (c *replCompleter) libraryNames() []string {
	return c.m.LibraryNames()
}

// withPrefix keeps the candidates that extend prefix, and drops an exact match
// so that Tab on a complete name lists the alternatives instead of doing
// nothing.
func withPrefix(candidates []string, prefix string) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if strings.HasPrefix(c, prefix) && c != prefix {
			out = append(out, c)
		}
	}
	return out
}

// isNameChar reports whether r may appear in a Scheme name, which is what the
// cursor steps back over to find the word to complete.
func isNameChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("!$%&*+-/:<=>?@^_~.", r)
}

// importSpecStart reports whether the cursor is inside an (import ...) form and,
// if so, where the library name being typed begins: the parenthesis that opens
// it, which may be the one that starts the import form itself.
func importSpecStart(prefix []rune) (int, bool) {
	s := string(prefix)
	imp := strings.LastIndex(s, "(import")
	if imp < 0 {
		return 0, false
	}
	// Still inside the form?  Count from the import keyword onwards.
	depth := 0
	for _, r := range s[imp:] {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		}
	}
	if depth <= 0 {
		return 0, false
	}
	if open := strings.LastIndex(s, "("); open > imp {
		return open, true
	}
	return imp, true
}

// -------------------------------------------------------------- comma commands

// runCommaCommand handles a line that begins with a comma.  It reports whether
// the session should end.
func runCommaCommand(m *scheme.Machine, line string, stdout, stderr io.Writer, sigint <-chan os.Signal) bool {
	fields := strings.Fields(line)
	cmd := fields[0]
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), cmd))

	switch cmd {
	case ",help", ",h", ",?":
		fmt.Fprint(stdout, `,help             this list
,bindings [prefix] names the interpreter knows
,libraries        libraries that can be imported
,time EXPR        evaluate EXPR and report how long it took
,quit             leave the session
`)
	case ",quit", ",q", ",exit":
		return true

	case ",libraries":
		c := &replCompleter{m: m}
		for _, name := range c.libraryNames() {
			fmt.Fprintln(stdout, name)
		}

	case ",bindings":
		prefix := ""
		if len(fields) > 1 {
			prefix = fields[1]
		}
		c := &replCompleter{m: m}
		shown := 0
		for _, name := range c.schemeNames() {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			fmt.Fprintln(stdout, name)
			shown++
			if shown == 200 {
				fmt.Fprintln(stdout, "... (and more)")
				break
			}
		}
		if shown == 0 {
			fmt.Fprintf(stdout, "nothing matches %q\n", prefix)
		}

	case ",time":
		if rest == "" {
			fmt.Fprintln(stderr, ",time needs an expression")
			return false
		}
		forms, err := readForms(rest)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return false
		}
		start := time.Now()
		for _, f := range forms {
			if !evalFormInteractive(m, f, stdout, stderr, sigint) {
				return true
			}
		}
		fmt.Fprintf(stdout, "%.3f ms\n", float64(time.Since(start).Microseconds())/1000)

	default:
		fmt.Fprintf(stderr, "unknown command %s; try ,help\n", cmd)
	}
	return false
}
