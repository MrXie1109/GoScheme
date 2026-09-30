// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// docPages maps each extension library to the reference page that has to
// mention every name it exports.  Adding a library without adding a page, or
// adding an export without documenting it, fails the tests below.
var docPages = map[string]string{
	"(goscheme channel)":    "channel.md",
	"(goscheme fast)":       "fast.md",
	"(goscheme ffi)":        "ffi.md",
	"(goscheme fs)":         "fs.md",
	"(goscheme hash-table)": "hash-table.md",
	"(goscheme http)":       "http.md",
	"(goscheme json)":       "json.md",
	"(goscheme match)":      "match.md",
	"(goscheme process)":    "process.md",
	"(goscheme regexp)":     "regexp.md",
	"(goscheme socket)":     "socket.md",
	"(goscheme sync)":       "sync.md",
	"(goscheme time)":       "time.md",
}

// TestExtensionDocsCoverEveryExport is the guard against documentation drift:
// every name an extension library exports has to appear in its page as a
// backticked identifier.
func TestExtensionDocsCoverEveryExport(t *testing.T) {
	m := NewMachine()
	// (goscheme ffi) only exists in cgo builds, so a library that this build
	// does not register cannot be enumerated here.
	registered := map[string]bool{}
	for _, lib := range m.LibraryNames() {
		registered[lib] = true
	}
	for lib, page := range docPages {
		if !registered[lib] {
			continue
		}
		exports := m.libExports[lib]
		if len(exports) == 0 {
			t.Errorf("%s: the library exports nothing, so the mapping is stale", lib)
			continue
		}
		data, err := os.ReadFile(filepath.Join("..", "..", "docs", "extensions", page))
		if err != nil {
			t.Errorf("%s: %v", lib, err)
			continue
		}
		text := string(data)
		for _, name := range exports {
			if !strings.Contains(text, "`"+name+"`") {
				t.Errorf("%s: %s is exported but missing from docs/extensions/%s", lib, name, page)
			}
		}
	}
}

// TestEveryExtensionLibraryHasADocPage makes sure a new (goscheme ...) library
// cannot be added without a page in docs/extensions.
func TestEveryExtensionLibraryHasADocPage(t *testing.T) {
	m := NewMachine()
	for _, lib := range m.LibraryNames() {
		if !strings.HasPrefix(lib, "(goscheme") {
			continue
		}
		if _, ok := docPages[lib]; !ok {
			t.Errorf("%s has no page in docs/extensions", lib)
		}
	}
}

// TestDumpLibraryExports is a maintenance helper rather than a test: it prints
// every extension library with the arity of each export, which is where the
// tables in docs/extensions come from.  Run it with
//
//	go test ./internal/scheme -run TestDumpLibraryExports -v
func TestDumpLibraryExports(t *testing.T) {
	m := NewMachine()
	names := m.LibraryNames()
	sort.Strings(names)
	for _, lib := range names {
		if !strings.HasPrefix(lib, "(goscheme") {
			continue
		}
		sorted := append([]string(nil), m.libExports[lib]...)
		sort.Strings(sorted)
		fmt.Printf("### %s (%d)\n", lib, len(sorted))
		for _, n := range sorted {
			v, _ := m.Builtin.Lookup(Intern(n))
			if p, ok := v.(*Primitive); ok {
				fmt.Printf("%s %d %d\n", n, p.MinArgs, p.MaxArgs)
			} else {
				fmt.Printf("%s special\n", n)
			}
		}
	}
}
