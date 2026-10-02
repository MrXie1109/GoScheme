// SPDX-License-Identifier: MIT

package scheme

// Some extensions are easier to express in Scheme than in Go: a macro such as
// with-mutex, or the whole of (goscheme match).  The helpers here load such
// source from a string that is compiled into the binary, so a program that
// imports it works everywhere, including an executable produced by
// `goscheme build` on a machine that has none of the repository's files.

// runEmbedded evaluates self-contained source in the builtin frame and returns
// that frame, where the definitions it made now live.
func (m *Machine) runEmbedded(name, source string) *Env {
	r := NewStringReader(source)
	r.Source = name
	forms, err := r.ReadAll()
	if err != nil {
		// A mistake here is a bug in this binary, not in the user's program,
		// so it is loud and immediate rather than a Scheme condition.
		panic("goscheme: bad embedded source in " + name + ": " + err.Error())
	}
	// Compiled like any other source: these are procedure definitions as well
	// as macros, and an interpreted library body is a slow library.
	if _, err := m.RunFormsCompiled(forms, m.Builtin); err != nil {
		panic("goscheme: " + name + " failed to load: " + err.Error())
	}
	return m.Builtin
}

// installEmbeddedSource evaluates Scheme source and exports the names it
// defines from a builtin library.  It is how a library that is mostly Go gains
// a macro or two without touching the evaluator.
func (m *Machine) installEmbeddedSource(lib, source string, names ...string) {
	env := m.runEmbedded(lib, source)
	for _, n := range names {
		v, ok := env.Lookup(Intern(n))
		if !ok {
			panic("goscheme: " + lib + " does not define " + n)
		}
		m.defValue(n, v, lib)
	}
}

// installEmbeddedLibrary evaluates a complete (define-library ...) form, which
// registers the library and its exports by itself.  The source may hold several
// libraries.
func (m *Machine) installEmbeddedLibrary(source string) {
	m.runEmbedded("embedded library", source)
}
