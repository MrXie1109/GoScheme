// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
	"os"
	"path/filepath"
)

// The (goscheme fs) library adds the filesystem vocabulary a script usually
// wants and R7RS-small leaves out: globbing, walking a tree, making and
// removing directories, splitting paths, and reading a file's size.  It is
// path/filepath underneath, so the same program is at home on Unix and on
// Windows, and every failure is an ordinary Scheme condition rather than a Go
// panic.
func init() { registerInstaller(installFS) }

func installFS(m *Machine) {
	const lib = "(goscheme fs)"

	// (glob pattern) returns the paths matching a shell pattern, sorted.
	// A pattern that matches nothing is the empty list, not an error; a
	// malformed pattern is an error.
	m.defSimple("glob", 1, 1, func(a []Value) (Value, error) {
		pattern := wantString("glob", a[0]).Value()
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, newFSFileError("glob", err, pattern)
		}
		items := make([]Value, len(matches))
		for i, p := range matches {
			items[i] = NewString(p)
		}
		return List(items...), nil
	}, lib)

	// (directory-walk dir proc) calls (proc path) for dir itself, then for
	// every file and every subdirectory below it, in lexical order.  That is
	// exactly the order filepath.WalkDir visits them in.
	m.def("directory-walk", 2, 2, func(m *Machine, a []Value) {
		root := wantString("directory-walk", a[0]).Value()
		proc := wantProcedure("directory-walk", a[1])

		// Walk once to collect the paths, then call proc for each in turn.
		// Calling a Scheme procedure is a continuation, not a plain Go call,
		// so the callback cannot invoke it directly; collecting first also
		// keeps a proc that mutates the tree from confusing the traversal.
		var paths []string
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			m.RaiseError(newFSFileError("directory-walk", err, root))
			return
		}

		var step func(int)
		step = func(i int) {
			if i == len(paths) {
				m.Return(UnspecifiedValue)
				return
			}
			m.ApplyWith(proc, []Value{NewString(paths[i])}, func(mm *Machine, _ Value) {
				step(i + 1)
			})
		}
		step(0)
	}, lib)

	// (directory-list dir) returns the names — not the full paths — of the
	// entries directly inside dir, sorted.
	m.defSimple("directory-list", 1, 1, func(a []Value) (Value, error) {
		dir := wantString("directory-list", a[0]).Value()
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, newFSFileError("directory-list", err, dir)
		}
		items := make([]Value, len(entries))
		for i, e := range entries {
			items[i] = NewString(e.Name())
		}
		return List(items...), nil
	}, lib)

	// (create-directory path) makes one directory and is a no-op if the path
	// already exists.  create-directory-tree makes the parents as needed,
	// like mkdir -p.  A real failure is a file error.
	m.defSimple("create-directory", 1, 1, func(a []Value) (Value, error) {
		path := wantString("create-directory", a[0]).Value()
		if _, err := os.Stat(path); err == nil {
			return UnspecifiedValue, nil
		}
		if err := os.Mkdir(path, 0o777); err != nil {
			return nil, newFSFileError("create-directory", err, path)
		}
		return UnspecifiedValue, nil
	}, lib)

	m.defSimple("create-directory-tree", 1, 1, func(a []Value) (Value, error) {
		path := wantString("create-directory-tree", a[0]).Value()
		if err := os.MkdirAll(path, 0o777); err != nil {
			return nil, newFSFileError("create-directory-tree", err, path)
		}
		return UnspecifiedValue, nil
	}, lib)

	// (delete-directory path) removes an empty directory;
	// (delete-directory-tree path) removes it and everything below it.  A
	// failure — including a missing directory for the first form — is a file
	// error.  RemoveAll treats a missing path as already gone, which matches
	// the "clean up if it is there" use of the recursive form.
	m.defSimple("delete-directory", 1, 1, func(a []Value) (Value, error) {
		path := wantString("delete-directory", a[0]).Value()
		if err := os.Remove(path); err != nil {
			return nil, newFSFileError("delete-directory", err, path)
		}
		return UnspecifiedValue, nil
	}, lib)

	m.defSimple("delete-directory-tree", 1, 1, func(a []Value) (Value, error) {
		path := wantString("delete-directory-tree", a[0]).Value()
		if err := os.RemoveAll(path); err != nil {
			return nil, newFSFileError("delete-directory-tree", err, path)
		}
		return UnspecifiedValue, nil
	}, lib)

	// ------------------------------------------------------------ path pieces
	// These follow path/filepath, so a program that builds paths with
	// path-join and takes them apart with these works on any platform.
	m.defSimple("path-join", 1, -1, func(a []Value) (Value, error) {
		parts := make([]string, len(a))
		for i, v := range a {
			parts[i] = wantString("path-join", v).Value()
		}
		return NewString(filepath.Join(parts...)), nil
	}, lib)

	m.defSimple("path-directory", 1, 1, func(a []Value) (Value, error) {
		return NewString(filepath.Dir(wantString("path-directory", a[0]).Value())), nil
	}, lib)

	m.defSimple("path-base", 1, 1, func(a []Value) (Value, error) {
		return NewString(filepath.Base(wantString("path-base", a[0]).Value())), nil
	}, lib)

	// (path-extension path) includes the dot, or is the empty string when the
	// name has no extension.
	m.defSimple("path-extension", 1, 1, func(a []Value) (Value, error) {
		return NewString(filepath.Ext(wantString("path-extension", a[0]).Value())), nil
	}, lib)

	m.defSimple("path-absolute", 1, 1, func(a []Value) (Value, error) {
		path := wantString("path-absolute", a[0]).Value()
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, newFSFileError("path-absolute", err, path)
		}
		return NewString(abs), nil
	}, lib)

	// (file-size path) is the size in bytes, as an exact integer.
	m.defSimple("file-size", 1, 1, func(a []Value) (Value, error) {
		path := wantString("file-size", a[0]).Value()
		info, err := os.Stat(path)
		if err != nil {
			return nil, newFSFileError("file-size", err, path)
		}
		return Int(info.Size()), nil
	}, lib)
}

// newFSFileError wraps an operating system error as a Scheme file condition,
// naming the procedure that failed and the path it was given.
func newFSFileError(name string, err error, path string) *ErrorObject {
	return NewFileError(name+": "+err.Error(), NewString(path))
}
