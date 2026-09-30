// SPDX-License-Identifier: MIT

package scheme

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Library is an R7RS library.
type Library struct {
	Name    string
	Env     *Env
	Exports map[*Symbol]Value
}

// SyntaxKeyword is the value bound to a syntactic keyword by a library
// export.  Importing a syntax keyword makes it available under its own name;
// the evaluator dispatches on the keyword name.
type SyntaxKeyword struct {
	Name string
}

// addExport records that lib exports name.  Libraries are materialised by
// finishLibraries once every builtin has been registered.
func (m *Machine) addExport(lib, name string) {
	// The export table is shared by every interpreter thread, and a library
	// loaded from two threads at once used to append to it without a lock.
	m.libMu.Lock()
	defer m.libMu.Unlock()
	if m.libExports == nil {
		m.libExports = map[string][]string{}
	}
	m.libExports[lib] = append(m.libExports[lib], name)
}

// finishLibraries builds the Library objects for the builtin libraries.
func (m *Machine) finishLibraries() {
	for name, names := range m.libExports {
		lib := &Library{Name: name, Env: m.Builtin, Exports: map[*Symbol]Value{}}
		for _, n := range names {
			sym := Intern(n)
			if v, ok := m.Builtin.Lookup(sym); ok {
				lib.Exports[sym] = v
			}
		}
		m.registerLibrary(name, lib)
	}
}

// ResolveImportSet resolves an import set to a map of bindings.
func (m *Machine) ResolveImportSet(spec Value) (map[*Symbol]Value, error) {
	return m.resolveImportSet(spec)
}

func (m *Machine) resolveImportSet(spec Value) (map[*Symbol]Value, error) {
	if p, ok := spec.(*Pair); ok {
		if h, ok := p.Car.(*Symbol); ok {
			args := mustSlice(p.Cdr)
			switch h.Name {
			case "only":
				if len(args) < 1 {
					return nil, NewError("import: malformed only", spec)
				}
				base, err := m.resolveImportSet(args[0])
				if err != nil {
					return nil, err
				}
				out := map[*Symbol]Value{}
				for _, a := range args[1:] {
					s, ok := a.(*Symbol)
					if !ok {
						return nil, NewError("import: only expects identifiers", a)
					}
					v, ok := base[s]
					if !ok {
						return nil, NewError("import: identifier is not exported by the library", s)
					}
					out[s] = v
				}
				return out, nil
			case "except":
				if len(args) < 1 {
					return nil, NewError("import: malformed except", spec)
				}
				base, err := m.resolveImportSet(args[0])
				if err != nil {
					return nil, err
				}
				for _, a := range args[1:] {
					s, ok := a.(*Symbol)
					if !ok {
						return nil, NewError("import: except expects identifiers", a)
					}
					delete(base, s)
				}
				return base, nil
			case "prefix":
				if len(args) != 2 {
					return nil, NewError("import: malformed prefix", spec)
				}
				base, err := m.resolveImportSet(args[0])
				if err != nil {
					return nil, err
				}
				pfx, ok := args[1].(*Symbol)
				if !ok {
					return nil, NewError("import: prefix expects an identifier", args[1])
				}
				out := map[*Symbol]Value{}
				for s, v := range base {
					out[Intern(pfx.Name+s.Name)] = v
				}
				return out, nil
			case "rename":
				if len(args) < 1 {
					return nil, NewError("import: malformed rename", spec)
				}
				base, err := m.resolveImportSet(args[0])
				if err != nil {
					return nil, err
				}
				for _, a := range args[1:] {
					pair, ok := a.(*Pair)
					if !ok {
						return nil, NewError("import: rename expects (old new) pairs", a)
					}
					oldS, ok1 := pair.Car.(*Symbol)
					newS, ok2 := cadr(pair).(*Symbol)
					if !ok1 || !ok2 {
						return nil, NewError("import: malformed rename pair", a)
					}
					v, ok := base[oldS]
					if !ok {
						return nil, NewError("import: identifier is not exported by the library", oldS)
					}
					delete(base, oldS)
					base[newS] = v
				}
				return base, nil
			}
		}
	}
	name := LibraryNameString(spec)
	lib, ok := m.lookupLibrary(name)
	if !ok {
		// Not registered yet: look for it on the library search path, which is
		// how a program imports a library that lives in its own files.
		if err := m.loadLibrary(name, spec); err != nil {
			return nil, err
		}
		lib, ok = m.lookupLibrary(name)
	}
	if !ok {
		return nil, NewError("import: unknown library", spec)
	}
	out := make(map[*Symbol]Value, len(lib.Exports))
	for s, v := range lib.Exports {
		out[Intern(s.Name)] = v
	}
	return out, nil
}

// loadLibrary finds the definition of a library on the search path, evaluates
// the file, and leaves the library registered.  A library that is not found is
// not an error here: the caller reports the unknown library, which is the
// better message.
func (m *Machine) loadLibrary(name string, spec Value) error {
	path, found := m.findLibraryFile(spec)
	if !found {
		return nil
	}
	// The lock is held only for the bookkeeping: evaluating the library body
	// below may import another library in the same thread, which must not
	// deadlock on it.
	m.libMu.Lock()
	if m.libLoading == nil {
		m.libLoading = map[string]bool{}
	}
	if m.libLoading[name] {
		m.libMu.Unlock()
		return NewError("import: circular dependency between libraries", spec)
	}
	m.libLoading[name] = true
	m.libMu.Unlock()
	defer func() {
		m.libMu.Lock()
		delete(m.libLoading, name)
		m.libMu.Unlock()
	}()

	forms, err := ReadFileForms(m, path, false)
	if err != nil {
		return err
	}
	// A library is evaluated on its own interpreter thread: it has its own
	// continuation stack, so loading it cannot disturb the evaluation that
	// asked for the import.  Libraries, the global environment and the load
	// path are shared, and the file's directory is added so that include and
	// nested imports resolve relative to it.
	sub := m.Child()
	sub.AddLoadPath(dirOf(path))
	if _, err := sub.RunForms(forms, sub.Global); err != nil {
		return err
	}
	if _, ok := m.lookupLibrary(name); !ok {
		return NewFileError("library file does not define "+name, NewString(path))
	}
	return nil
}

// findLibraryFile maps a library name to a file: (a b c) is looked for as
// a/b/c.sld, a/b/c.scm or a/b/c.sls in each directory of the search path.
func (m *Machine) findLibraryFile(spec Value) (string, bool) {
	parts, ok := ListToSlice(spec)
	if !ok || len(parts) == 0 {
		return "", false
	}
	segs := make([]string, 0, len(parts))
	for _, p := range parts {
		switch x := p.(type) {
		case *Symbol:
			segs = append(segs, x.Name)
		case *Integer:
			segs = append(segs, x.String())
		case *String:
			segs = append(segs, x.Value())
		default:
			return "", false
		}
	}
	rel := filepath.Join(segs...)
	for _, dir := range m.librarySearchPath() {
		for _, ext := range []string{".sld", ".scm", ".sls", ".ss"} {
			p := filepath.Join(dir, rel+ext)
			if fileExists(p) {
				return p, true
			}
		}
	}
	return "", false
}

// librarySearchPath is where libraries are looked for: the directories of the
// load path, innermost first, then GOSCHEME_LIBRARY_PATH, then the working
// directory.
func (m *Machine) librarySearchPath() []string {
	var dirs []string
	for i := len(m.LoadPath) - 1; i >= 0; i-- {
		dirs = append(dirs, m.LoadPath[i])
	}
	if env := os.Getenv("GOSCHEME_LIBRARY_PATH"); env != "" {
		for _, d := range strings.Split(env, string(os.PathListSeparator)) {
			if d != "" {
				dirs = append(dirs, d)
			}
		}
	}
	return append(dirs, ".")
}

// libraryAvailable reports whether a library is registered or can be found on
// the search path; cond-expand's (library ...) requirement uses it.
func (m *Machine) libraryAvailable(spec Value) bool {
	if _, ok := m.lookupLibrary(LibraryNameString(spec)); ok {
		return true
	}
	_, found := m.findLibraryFile(spec)
	return found
}

// ReadFileForms reads every datum in a file.
func ReadFileForms(m *Machine, path string, fold bool) ([]Value, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, NewFileError("cannot read file: "+err.Error(), NewString(path))
	}
	r := NewStringReader(string(data))
	r.Source = path
	r.FoldCase = fold
	forms, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	return forms, nil
}

func platformFeaturesImpl() []string {
	var feats []string
	switch runtime.GOOS {
	case "linux":
		feats = append(feats, "linux", "posix", "unix")
	case "darwin":
		feats = append(feats, "darwin", "macos", "posix", "unix", "bsd")
	case "windows":
		feats = append(feats, "windows", "win32")
	case "freebsd", "openbsd", "netbsd":
		feats = append(feats, runtime.GOOS, "posix", "unix", "bsd")
	default:
		feats = append(feats, runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		feats = append(feats, "x86-64", "x86_64", "little-endian")
	case "arm64":
		feats = append(feats, "aarch64", "arm64", "little-endian")
	case "386":
		feats = append(feats, "i386", "x86", "little-endian")
	case "arm":
		feats = append(feats, "arm", "little-endian")
	default:
		feats = append(feats, runtime.GOARCH)
	}
	return feats
}

// FeatureList returns the feature identifiers as a Scheme list.
func (m *Machine) FeatureList() Value {
	var items []Value
	for _, f := range m.Features() {
		items = append(items, Intern(f))
	}
	return List(items...)
}
