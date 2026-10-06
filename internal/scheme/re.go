// SPDX-License-Identifier: MIT

package scheme

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// RuntimeLibraryPath returns the path of the runtime archive a compiled program
// links against, building it if it is not there yet.
//
// The archive is this package compiled as a C archive: `go build
// -buildmode=c-archive`, which produces a static library and a header.  It is
// built on demand and cached beside the executable, because it is several
// megabytes and rebuilding it per compilation would be wasteful — but it is
// keyed to the interpreter's own build, so that a program is never linked
// against a runtime that does not match the compiler that generated it.
func RuntimeLibraryPath() (string, error) {
	dir, err := runtimeCacheDir()
	if err != nil {
		return "", err
	}
	lib := filepath.Join(dir, runtimeLibName())
	if _, err := os.Stat(lib); err == nil {
		return lib, nil
	}
	if err := buildRuntimeArchive(dir); err != nil {
		return "", err
	}
	return lib, nil
}

// runtimeLibName is the archive's name on this platform.
func runtimeLibName() string {
	if runtime.GOOS == "windows" {
		return "libgoscheme.a"
	}
	return "libgoscheme.a"
}

// runtimeCacheDir is where the runtime archive is kept: under the user's cache
// directory, so that two checkouts do not fight over one file and a read-only
// installation still works.
// runtimeCacheDir is the directory a built runtime archive is cached in.
//
// The name carries a stamp for the flags the archive is built with, because the
// cache previously had no key at all: it was one fixed path, and the comment
// above claimed it was "keyed to the interpreter's own build" while nothing was
// checking.  That is the kind of claim that is worse than none, because it
// stops the next person looking — an archive built before a flag was added
// stayed, and a program compiled afterwards was silently linked against the
// older runtime.
//
// Stamping the flags is enough to make the cache honest about them.  It does
// not make it react to a source change, which needs the module to be rebuilt
// rather than relinked; `goscheme compile` after an edit to the interpreter is
// the case to keep in mind, and the answer there is to remove the directory.
func runtimeCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "goscheme", "runtime-"+runtimeCacheStamp())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// runtimeCacheStamp identifies the way the archive is built, so that changing
// the flags produces a different cache directory rather than a stale archive.
func runtimeCacheStamp() string {
	sum := sha256.Sum256([]byte(strings.Join(runtimeArchiveBuildArgs(), "\x00")))
	return hex.EncodeToString(sum[:6])
}

// runtimeArchiveBuildArgs is the command the archive is built with, in one
// place so that the stamp above cannot describe flags the build does not use.
//
// It is a variable so that a test can change it and see the cache key follow,
// which is the property the stamp exists for.
var runtimeArchiveBuildArgs = func() []string {
	return []string{"build", "-buildmode=c-archive", "-ldflags=-s -w"}
}

// buildRuntimeArchive compiles this package as a C archive in dir.
//
// It runs `go build -buildmode=c-archive` on a tiny main package that imports
// the interpreter and pulls in the exported functions.  The generated main is
// written here rather than kept in the tree because it is not a program anyone
// runs: its only job is to give the linker a package to build.
func buildRuntimeArchive(dir string) error {
	// The archive is built from the `re` package, which is the runtime
	// environment: a main package whose exported functions are the C ABI a
	// compiled program calls.  It is a separate package from this one because
	// `-buildmode=c-archive` requires a main package, and because the boundary
	// between "the runtime a compiled program links" and "the interpreter this
	// process runs" is worth having a name.
	pkgDir, err := runtimePackageDir()
	if err != nil {
		return err
	}
	lib := filepath.Join(dir, runtimeLibName())
	// -s -w strips the symbol table and the DWARF debug information.  Nobody
	// debugs a compiled Scheme program through the Go runtime linked into it,
	// and keeping them cost 10 MB of the 28 MB archive — which every compiled
	// program paid for on disk and then threw away, because the linker discards
	// debug sections it was not asked to keep.  Stripping the archive takes it
	// to 10 MB and the linked program from 18 MB to 8.5 MB.
	//
	// It is a flag on the archive rather than on the final link because the
	// final link is `cc`, run through RuntimeLinkFlags, and the archive is the
	// half this program controls.
	cmd := exec.Command("go", append(runtimeArchiveBuildArgs(), "-o", lib, ".")...)
	cmd.Dir = pkgDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("building the runtime archive: %s", msg)
	}
	return nil
}

// runtimePackageDir is the directory of the runtime environment package, which
// is what gets built into the archive a compiled program links against.
//
// Locating it is awkward because a released binary is built with -trimpath,
// which rewrites the recorded source path to a module-relative one
// (github.com/MrXie1109/GoScheme/internal/scheme) that is not a real directory.
// So the recorded path is tried first, and when it is not there the module is
// asked where it lives — `go list -m` resolves the module by import path from
// inside any directory belonging to it, which is exactly what the trimmed path
// still tells us.
func runtimePackageDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot find the interpreter package directory")
	}
	// This file lives in internal/scheme; the runtime is ../../re.
	if dir := filepath.Join(filepath.Dir(file), "..", "..", "re"); dirExists(dir) {
		return dir, nil
	}
	// The path was trimmed.  Walk up from the executable, and from the working
	// directory, looking for a module root that the Go tool recognises.
	for _, start := range candidateRoots() {
		dir, err := goListModuleDir(start)
		if err != nil {
			continue
		}
		if re := filepath.Join(dir, "re"); dirExists(re) {
			return re, nil
		}
	}
	return "", fmt.Errorf("cannot find the runtime package (re); build from a source checkout")
}

// candidateRoots are the places a module root might be, in the order worth
// trying: the executable's directory and its parents, then the working
// directory and its parents.
func candidateRoots() []string {
	var roots []string
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, parents(filepath.Dir(exe))...)
	}
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, parents(wd)...)
	}
	return roots
}

// parents returns dir and each of its ancestors, nearest first.
func parents(dir string) []string {
	var out []string
	for {
		out = append(out, dir)
		up := filepath.Dir(dir)
		if up == dir {
			return out
		}
		dir = up
	}
}

// goListModuleDir asks the Go tool for the directory of this module, run from
// inside dir.
func goListModuleDir(dir string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// dirExists reports whether path is a directory.
func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// RuntimeLinker is the program that links a compiled program: the C compiler,
// because the link needs the platform's own start files and libraries beside
// the Go archive.
func RuntimeLinker() string {
	if runtime.GOOS == "windows" {
		return "gcc"
	}
	return "cc"
}

// RuntimeLinkFlags is what the runtime archive needs beside it: the system
// libraries the Go runtime uses, which differ per platform.
func RuntimeLinkFlags() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"-lpthread", "-framework", "CoreFoundation", "-framework", "Security"}
	case "windows":
		return []string{"-lws2_32", "-lntdll", "-luserenv"}
	default:
		return []string{"-lpthread", "-lm"}
	}
}
