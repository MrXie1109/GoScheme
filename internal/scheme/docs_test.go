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

// srfiPages is the same mapping for the SRFI libraries, whose pages live in
// docs/srfi.
var srfiPages = map[string]string{
	"(srfi 1)":   "1.md",
	"(srfi 2)":   "2.md",
	"(srfi 8)":   "8.md",
	"(srfi 26)":  "26.md",
	"(srfi 111)": "111.md",
}

// docPage is where a builtin library's reference page lives.
func docPage(lib string) (string, bool) {
	if page, ok := docPages[lib]; ok {
		return filepath.Join("..", "..", "docs", "extensions", page), true
	}
	if page, ok := srfiPages[lib]; ok {
		return filepath.Join("..", "..", "docs", "srfi", page), true
	}
	return "", false
}

// TestExtensionDocsCoverEveryExport is the guard against documentation drift:
// every name a builtin library exports has to appear in its page as a
// backticked identifier.
func TestExtensionDocsCoverEveryExport(t *testing.T) {
	m := NewMachine()
	// (goscheme ffi) only exists in cgo builds, so a library that this build
	// does not register cannot be enumerated here.
	registered := map[string]bool{}
	for _, lib := range m.LibraryNames() {
		registered[lib] = true
	}
	for _, lib := range append(keysOf(docPages), keysOf(srfiPages)...) {
		if !registered[lib] {
			continue
		}
		page, _ := docPage(lib)
		exports := m.libExports[lib]
		if len(exports) == 0 {
			t.Errorf("%s: the library exports nothing, so the mapping is stale", lib)
			continue
		}
		data, err := os.ReadFile(page)
		if err != nil {
			t.Errorf("%s: %v", lib, err)
			continue
		}
		text := string(data)
		for _, name := range exports {
			if !strings.Contains(text, "`"+name+"`") {
				t.Errorf("%s: %s is exported but missing from %s", lib, name, page)
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
		if _, ok := docPage(lib); !ok {
			t.Errorf("%s has no reference page", lib)
		}
	}
}

// keysOf is the library names of a page mapping, for a deterministic order.
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
		// The R7RS libraries are the report's, not ours; everything else is a
		// page in docs/.
		if strings.HasPrefix(lib, "(scheme") {
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
