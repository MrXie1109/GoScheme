// SPDX-License-Identifier: MIT

package scheme

import (
	"os"
	"runtime"
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
		m.Libraries[name] = lib
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
	lib, ok := m.Libraries[name]
	if !ok {
		return nil, NewError("import: unknown library", spec)
	}
	out := make(map[*Symbol]Value, len(lib.Exports))
	for s, v := range lib.Exports {
		out[Intern(s.Name)] = v
	}
	return out, nil
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
