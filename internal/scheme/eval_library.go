// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
	"strings"
)

// ---------------------------------------------------------------------------
// Libraries
// ---------------------------------------------------------------------------

func evalImport(m *Machine, form Value, env *Env) {
	for _, spec := range formArgs(form) {
		bindings, err := m.ResolveImportSet(spec)
		if err != nil {
			m.RaiseError(err)
			return
		}
		for name, v := range bindings {
			env.Define(name, v)
		}
	}
	m.Return(UnspecifiedValue)
}

func evalDefineLibrary(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) < 1 {
		m.Raise(NewError("define-library: missing library name", form))
		return
	}
	name := LibraryNameString(args[0])
	if name == "" {
		m.Raise(NewError("define-library: malformed library name", args[0]))
		return
	}
	libEnv := NewEnvNamed(nil, name)
	lib := &Library{Name: name, Env: libEnv, Exports: map[*Symbol]Value{}}
	var exportSpecs []Value
	// First pass: imports.
	for _, decl := range args[1:] {
		p, ok := decl.(*Pair)
		if !ok {
			continue
		}
		h, _ := p.Car.(*Symbol)
		if h == nil {
			continue
		}
		switch h.Name {
		case "import":
			for _, spec := range mustSlice(p.Cdr) {
				bindings, err := m.ResolveImportSet(spec)
				if err != nil {
					m.RaiseError(err)
					return
				}
				for n, v := range bindings {
					libEnv.Define(n, v)
				}
			}
		case "export":
			exportSpecs = append(exportSpecs, mustSlice(p.Cdr)...)
		}
	}
	// Second pass: bodies.
	var bodyForms []Value
	var declFileDirs []string
	for _, decl := range args[1:] {
		p, ok := decl.(*Pair)
		if !ok {
			continue
		}
		h, _ := p.Car.(*Symbol)
		if h == nil {
			continue
		}
		switch h.Name {
		case "begin":
			bodyForms = append(bodyForms, mustSlice(p.Cdr)...)
		case "include":
			for _, a := range mustSlice(p.Cdr) {
				s, ok := a.(*String)
				if !ok {
					m.Raise(NewError("include: file name must be a string", a))
					return
				}
				path := m.resolvePath(s.Value())
				fs, err := ReadFileForms(m, path, false)
				if err != nil {
					m.RaiseError(err)
					return
				}
				declFileDirs = append(declFileDirs, dirOf(path))
				bodyForms = append(bodyForms, fs...)
			}
		case "include-library-declarations":
			for _, a := range mustSlice(p.Cdr) {
				s, ok := a.(*String)
				if !ok {
					continue
				}
				path := m.resolvePath(s.Value())
				fs, err := ReadFileForms(m, path, false)
				if err != nil {
					m.RaiseError(err)
					return
				}
				declFileDirs = append(declFileDirs, dirOf(path))
				for _, f := range fs {
					fp, ok := f.(*Pair)
					if !ok {
						continue
					}
					fh, _ := fp.Car.(*Symbol)
					if fh != nil && fh.Name == "export" {
						exportSpecs = append(exportSpecs, mustSlice(fp.Cdr)...)
					} else {
						bodyForms = append(bodyForms, f)
					}
				}
			}
		case "cond-expand":
			for _, cl := range mustSlice(p.Cdr) {
				cp, ok := cl.(*Pair)
				if !ok {
					continue
				}
				if s, ok := cp.Car.(*Symbol); ok && s.Name == "else" {
					bodyForms = append(bodyForms, mustSlice(cp.Cdr)...)
					break
				}
				if FeatureMatch(m, cp.Car) {
					bodyForms = append(bodyForms, mustSlice(cp.Cdr)...)
					break
				}
			}
		}
	}
	m.registerLibrary(name, lib)
	for _, d := range declFileDirs {
		m.AddLoadPath(d)
	}
	// The export resolution frame must be installed before the body runs:
	// evaluation is stack based, so a frame pushed afterwards would run
	// first.
	m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, v Value) {
		for range declFileDirs {
			m.PopLoadPath()
		}
		for _, spec := range exportSpecs {
			switch s := spec.(type) {
			case *Symbol:
				val, ok := libEnv.Lookup(Intern(s.Name))
				if !ok {
					m.Raise(NewError("define-library: exported identifier is not bound", s))
					return
				}
				lib.Exports[s] = val
			case *Pair:
				// (rename <internal> <external>)
				items := mustSlice(s)
				if len(items) != 3 {
					m.Raise(NewError("define-library: malformed rename export", spec))
					return
				}
				if kw, ok := items[0].(*Symbol); !ok || kw.Name != "rename" {
					m.Raise(NewError("define-library: malformed rename export", spec))
					return
				}
				internal, ok1 := items[1].(*Symbol)
				external, ok2 := items[2].(*Symbol)
				if !ok1 || !ok2 {
					m.Raise(NewError("define-library: malformed rename export", spec))
					return
				}
				val, ok := libEnv.Lookup(internal)
				if !ok {
					m.Raise(NewError("define-library: exported identifier is not bound", internal))
					return
				}
				lib.Exports[external] = val
			default:
				m.Raise(NewError("define-library: malformed export spec", spec))
				return
			}
		}
		m.Return(UnspecifiedValue)
	}})
	m.EvalSeq(bodyForms, libEnv)
}

// LibraryNameString renders a library name as a canonical string key.
func LibraryNameString(v Value) string {
	items, ok := ListToSlice(v)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		switch x := it.(type) {
		case *Symbol:
			parts = append(parts, x.Name)
		case *Integer:
			parts = append(parts, x.String())
		case *String:
			parts = append(parts, x.Value())
		default:
			return ""
		}
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func platformFeatures() []string {
	return platformFeaturesImpl()
}
