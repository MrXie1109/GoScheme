// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// staticBuilder resolves the libraries a script imports, transitively, so that
// "goscheme pack -static" can bake them into the executable.  The libraries
// are emitted in dependency order, in front of the script: a bundle then needs
// no library files beside it.
//
// Resolving happens at build time rather than at run time, which keeps the
// bundle format unchanged and turns a missing library into a build error
// instead of a surprise on the user's machine.
type staticBuilder struct {
	builtin  map[string]bool // libraries compiled into the interpreter
	emitted  map[string]bool // libraries already written to the prelude
	visiting map[string]bool // libraries being resolved, for cycles
	order    []scheme.Value  // the prelude, dependencies first
	search   []string
}

// resolveStatic returns the source to bind: the script's libraries followed by
// the script itself.
func resolveStatic(scriptPath string, script []byte, search []string) ([]byte, error) {
	m := scheme.NewMachine()
	b := &staticBuilder{
		builtin:  map[string]bool{},
		emitted:  map[string]bool{},
		visiting: map[string]bool{},
		search:   search,
	}
	for _, name := range m.LibraryNames() {
		b.builtin[name] = true
	}

	r := scheme.NewStringReader(string(script))
	r.Source = scriptPath
	forms, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	root := dirOf(scriptPath)
	if err := b.resolve(forms, root); err != nil {
		return nil, err
	}
	script, err = b.rewriteForms(forms, root)
	if err != nil {
		return nil, err
	}

	var out strings.Builder
	for _, lib := range b.order {
		out.WriteString(scheme.WriteToString(lib))
		out.WriteString("\n")
	}
	out.Write(script)
	return []byte(out.String()), nil
}

// resolve walks the forms for imports and pulls in each library's file,
// dependencies first.
func (b *staticBuilder) resolve(forms []scheme.Value, baseDir string) error {
	for _, spec := range importSpecs(forms) {
		name := scheme.LibraryNameString(spec)
		if name == "" || b.builtin[name] || b.emitted[name] {
			continue
		}
		if b.visiting[name] {
			return fmt.Errorf("circular import of library %s", name)
		}
		path, ok := b.findLibrary(spec, baseDir)
		if !ok {
			return fmt.Errorf("cannot find library %s (searched %s)",
				name, strings.Join(b.search, string(os.PathListSeparator)))
		}
		libForms, err := readFormsFrom(path)
		if err != nil {
			return err
		}
		b.visiting[name] = true
		if err := b.resolve(libForms, dirOf(path)); err != nil {
			return err
		}
		delete(b.visiting, name)

		rewritten, err := b.rewriteForms(libForms, dirOf(path))
		if err != nil {
			return err
		}
		parsed, err := parseForms(string(rewritten))
		if err != nil {
			return err
		}
		b.order = append(b.order, parsed...)
		b.emitted[name] = true
	}
	return nil
}

// findLibrary maps a library name to its file, the same way the interpreter
// does at run time: (lib greet) is lib/greet.sld, .scm, .sls or .ss.
func (b *staticBuilder) findLibrary(spec scheme.Value, baseDir string) (string, bool) {
	rel := libraryRelPath(spec)
	if rel == "" {
		return "", false
	}
	dirs := append([]string{baseDir}, b.search...)
	for _, dir := range dirs {
		for _, ext := range []string{".sld", ".scm", ".sls", ".ss"} {
			p := filepath.Join(dir, rel+ext)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, true
			}
		}
	}
	return "", false
}

// rewriteForms inlines every include in the forms, recursively.
func (b *staticBuilder) rewriteForms(forms []scheme.Value, baseDir string) ([]byte, error) {
	var out strings.Builder
	for _, f := range forms {
		rewritten, err := b.inlineIncludes(f, baseDir)
		if err != nil {
			return nil, err
		}
		out.WriteString(scheme.WriteToString(rewritten))
		out.WriteString("\n")
	}
	return []byte(out.String()), nil
}

// inlineIncludes replaces (include "file" ...) with a begin of the file's
// forms, so that a baked-in library needs nothing on disk.  quote and
// quasiquote are left alone: their contents are data.
func (b *staticBuilder) inlineIncludes(v scheme.Value, baseDir string) (scheme.Value, error) {
	pair, ok := v.(*scheme.Pair)
	if !ok {
		return v, nil
	}
	if s, ok := pair.Car.(*scheme.Symbol); ok {
		switch s.Name {
		case "quote", "quasiquote":
			return v, nil
		case "include", "include-ci", "include-library-declarations":
			fold := s.Name == "include-ci"
			var forms []scheme.Value
			for _, arg := range sliceOf(pair.Cdr) {
				name, ok := arg.(*scheme.String)
				if !ok {
					return nil, fmt.Errorf("%s expects file names", s.Name)
				}
				path := name.Value()
				if !filepath.IsAbs(path) {
					if cand := filepath.Join(baseDir, path); fileExists(cand) {
						path = cand
					}
				}
				fs, err := readFormsFromFold(path, fold)
				if err != nil {
					return nil, err
				}
				for _, f := range fs {
					inner, err := b.inlineIncludes(f, dirOf(path))
					if err != nil {
						return nil, err
					}
					forms = append(forms, inner)
				}
			}
			return scheme.Cons(scheme.Intern("begin"), scheme.List(forms...)), nil
		}
	}
	car, err := b.inlineIncludes(pair.Car, baseDir)
	if err != nil {
		return nil, err
	}
	cdr, err := b.inlineIncludes(pair.Cdr, baseDir)
	if err != nil {
		return nil, err
	}
	return scheme.Cons(car, cdr), nil
}

// importSpecs collects the import sets of every (import ...) form in the tree,
// with the modifiers (only, except, prefix, rename) stripped.
func importSpecs(forms []scheme.Value) []scheme.Value {
	var out []scheme.Value
	var walk func(v scheme.Value)
	walk = func(v scheme.Value) {
		pair, ok := v.(*scheme.Pair)
		if !ok {
			return
		}
		if s, ok := pair.Car.(*scheme.Symbol); ok {
			switch s.Name {
			case "quote", "quasiquote":
				return
			case "import":
				for _, spec := range sliceOf(pair.Cdr) {
					out = append(out, baseLibraryName(spec))
				}
				return
			}
		}
		walk(pair.Car)
		walk(pair.Cdr)
	}
	for _, f := range forms {
		walk(f)
	}
	return out
}

// baseLibraryName strips the import modifiers from an import set.
func baseLibraryName(spec scheme.Value) scheme.Value {
	pair, ok := spec.(*scheme.Pair)
	if !ok {
		return spec
	}
	s, ok := pair.Car.(*scheme.Symbol)
	if !ok {
		return spec
	}
	switch s.Name {
	case "only", "except", "prefix", "rename":
		items := sliceOf(pair.Cdr)
		if len(items) == 0 {
			return spec
		}
		return baseLibraryName(items[0])
	}
	return spec
}

// libraryRelPath turns a library name into a relative path: (lib greet) is
// lib/greet.
func libraryRelPath(spec scheme.Value) string {
	items, ok := scheme.ListToSlice(spec)
	if !ok || len(items) == 0 {
		return ""
	}
	segs := make([]string, 0, len(items))
	for _, it := range items {
		switch x := it.(type) {
		case *scheme.Symbol:
			segs = append(segs, x.Name)
		case *scheme.Integer:
			segs = append(segs, x.String())
		case *scheme.String:
			segs = append(segs, x.Value())
		default:
			return ""
		}
	}
	return filepath.Join(segs...)
}

// sliceOf returns the elements of a proper list; a non-list yields nothing.
func sliceOf(v scheme.Value) []scheme.Value {
	items, _ := scheme.ListToSlice(v)
	return items
}

func parseForms(src string) ([]scheme.Value, error) {
	r := scheme.NewStringReader(src)
	return r.ReadAll()
}

func readFormsFrom(path string) ([]scheme.Value, error) {
	return readFormsFromFold(path, false)
}

func readFormsFromFold(path string, fold bool) ([]scheme.Value, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r := scheme.NewStringReader(string(data))
	r.Source = path
	r.FoldCase = fold
	return r.ReadAll()
}

func dirOf(path string) string {
	d := filepath.Dir(path)
	if d == "" {
		return "."
	}
	return d
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
