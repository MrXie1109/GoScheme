//go:build !cgo

// SPDX-License-Identifier: MIT

package scheme

// HasFFI reports whether this build can load shared libraries.  The released
// binaries are built with CGO_ENABLED=0, which is what makes them static and
// cross-compilable, and a static Go binary cannot call into a shared library:
// doing so needs cgo (or a hand written assembly trampoline per ABI).
const HasFFI = false

func init() { ffiAvailable = false }

// ForeignLibrary is never produced by a build without cgo, but the type exists
// so that the rest of the interpreter compiles the same way.
type ForeignLibrary struct {
	Name string
}

// installFFI registers the names so that they are discoverable, and explains
// how to get a build that has them.
func installFFI(m *Machine) {
	const lib = "(goscheme ffi)"
	unavailable := func(name string) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			return nil, NewError(name + ": this interpreter was built without cgo, " +
				"so it cannot load shared libraries; rebuild with CGO_ENABLED=1")
		}
	}
	m.defSimple("load-shared-library", 1, 1, unavailable("load-shared-library"), lib)
	m.defSimple("foreign-library?", 1, 1, unavailable("foreign-library?"), lib)
	m.defSimple("foreign-function", 3, -1, unavailable("foreign-function"), lib)
}
