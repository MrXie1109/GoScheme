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

// RuntimeKind is how a compiled program is linked against the runtime.
type RuntimeKind int

const (
	// RuntimeShared links against a shared library, and the program carries
	// only its own machine code.  This is the default: a compiled program is a
	// few kilobytes instead of eight megabytes, and several programs on one
	// machine share one copy of the interpreter.  The library has to be
	// findable at run time, which RuntimeSharedLibraryDir reports.
	RuntimeShared RuntimeKind = iota
	// RuntimeStatic links the runtime into the program, which then needs
	// nothing but itself.  It is what `-static` asks for: eight megabytes per
	// program, and no library to ship beside it.
	RuntimeStatic
)

// RuntimeLibraryPath returns the path of the runtime library a compiled program
// links against, building it if it is not there yet, for the given kind.
//
// The library is this module's `re` package built by the Go tool:
// `-buildmode=c-shared` for the shared form and `-buildmode=c-archive` for the
// static one.  Both are built on demand and cached, because each takes seconds
// and several megabytes, and rebuilding per compilation would be wasteful.
//
// The cache is keyed to the build arguments, so changing them produces a
// different directory rather than reusing a library built the old way.  See
// runtimeCacheDir.
func RuntimeLibraryPath(kind RuntimeKind) (string, error) {
	dir, err := runtimeCacheDir(kind)
	if err != nil {
		return "", err
	}
	lib := filepath.Join(dir, runtimeLibName(kind))
	if _, err := os.Stat(lib); err == nil {
		return lib, nil
	}
	if err := buildRuntimeLibrary(dir, kind); err != nil {
		return "", err
	}
	return lib, nil
}

// runtimeLibName is the library's file name on this platform.
//
// Windows names a shared library .dll rather than .so, and there is no
// import-library story here, so the static archive is what a Windows build
// gets; see runtimeArchiveBuildArgs.
func runtimeLibName(kind RuntimeKind) string {
	if kind == RuntimeStatic {
		return "libgoscheme.a"
	}
	switch runtime.GOOS {
	case "darwin":
		return "libgoscheme.dylib"
	case "windows":
		return "libgoscheme.dll"
	default:
		return "libgoscheme.so"
	}
}

// RuntimeSharedLibraryDir is the directory the shared runtime library lives in,
// which a linked program has to be able to find at run time.
//
// It is exported because a program linked against the shared runtime needs it:
// either on LD_LIBRARY_PATH, or compiled into the program as an rpath, or
// copied next to the program.  `goscheme compile` sets an rpath pointing here,
// and this is how a caller learns where that is.
func RuntimeSharedLibraryDir() (string, error) { return runtimeCacheDir(RuntimeShared) }

// runtimeCacheDir is where the runtime archive is kept: under the user's cache
// runtimeCacheDir is the directory a built runtime library is cached in:
// under the user's cache directory, so that two checkouts do not fight over
// one file and a read-only installation still works.
//
// The name carries a stamp for how the library is built, because the cache
// previously had no key at all: it was one fixed path, and the comment called it
// "keyed to the interpreter's own build" while nothing was checking.  That is
// the kind of claim that is worse than none, because it is what stops the next
// person looking — an archive built before a flag was added stayed, and a
// program compiled afterwards was silently linked against the older runtime.
//
// runtimeCacheDir is the directory a built runtime library is cached in: under
// the user's cache directory, so that two checkouts do not fight over one file
// and a read-only installation still works.
//
// The name carries a stamp for how the library is built and what it is built
// from; see runtimeCacheStamp.
func runtimeCacheDir(kind RuntimeKind) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "goscheme", "runtime-"+runtimeCacheStamp(kind))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// The stamp covers two things: how the library is built, and **what it is built
// from**.  The flag half was there first and the source half was missing, which
// is the same class of mistake one level down — the cache was honest about the
// command and silent about the code.  A change to the runtime environment
// therefore kept the library built before it, and a program compiled afterwards
// was linked against the older interpreter.
//
// That is not hypothetical: it happened while adding a `set!` that assigns
// global variables, and it looked like the fix had not worked. The write was
// going to the right place and the *library doing the writing* was the one from
// before the change.
//
// The fingerprint is over the sources the runtime is built from — the `re`
// package and the interpreter package it imports.  Reading them costs a few
// milliseconds once per compile and is what makes the cache correct rather than
// merely convenient.
func runtimeCacheStamp(kind RuntimeKind) string {
	args := strings.Join(runtimeBuildArgs(kind), "\x00")
	sum := sha256.Sum256([]byte(args + "\x00" + runtimeSourceFingerprint()))
	return hex.EncodeToString(sum[:6])
}

// runtimeSourceFingerprint hashes the runtime's own source files.
//
// It walks the two packages rather than the whole module, because those are what
// the library is built from: a change to `cmd/goscheme` does not alter the
// runtime and should not force a rebuild of it.
//
// An unreadable directory or file contributes its name and nothing else, so a
// fingerprint that cannot be computed is still stable within one run rather than
// changing between two calls.  Getting it wrong the other way — a fingerprint
// that varies — would rebuild the library on every compile, which is slow but
// correct; the failure to avoid is a fingerprint that never varies.
func runtimeSourceFingerprint() string {
	h := sha256.New()
	for _, dir := range runtimeSourceDirs() {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			fmt.Fprintf(h, "%s\x00", path)
			if b, err := os.ReadFile(path); err == nil {
				h.Write(b)
			}
			return nil
		})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// runtimeSourceDirs are the directories the runtime library is compiled from:
// the C ABI package, and the interpreter it links.
func runtimeSourceDirs() []string {
	pkgDir, err := runtimePackageDir()
	if err != nil {
		return nil
	}
	// pkgDir is the `re` package at the module root; internal/scheme is its
	// sibling under internal/.
	root := filepath.Dir(pkgDir)
	return []string{pkgDir, filepath.Join(root, "internal", "scheme"), filepath.Join(root, "internal", "re")}
}

// runtimeBuildArgs is the command a runtime library is built with, in one place
// so that the stamp cannot describe flags the build does not use.
//
// It is a variable so that a test can change it and see the cache key follow,
// which is the property the stamp exists for.
//
// The two kinds differ only in build mode, and both are stripped: -s -w removes
// the symbol table and the DWARF, which nobody debugs a compiled Scheme program
// through.  Keeping them cost 10 MB of a 27 MB archive before the link threw
// them away, and a shared library is loaded rather than linked, so there they
// would be paid for on every load.
var runtimeBuildArgs = func(kind RuntimeKind) []string {
	mode := "-buildmode=c-shared"
	if kind == RuntimeStatic {
		mode = "-buildmode=c-archive"
	}
	return []string{"build", mode, "-ldflags=-s -w"}
}

// buildRuntimeLibrary compiles this package as a C library of the given kind in
// dir, a shared object or a static archive.
//
// Both are built by the Go tool from the `re` package, which is the runtime
// environment: a main package whose exported functions are the C ABI a compiled
// program calls.  It is a separate package from this one because both build
// modes require a main package, and because the boundary between "the runtime a
// compiled program links" and "the interpreter this process runs" is worth
// having a name.
func buildRuntimeLibrary(dir string, kind RuntimeKind) error {
	pkgDir, err := runtimePackageDir()
	if err != nil {
		return err
	}
	lib := filepath.Join(dir, runtimeLibName(kind))
	cmd := exec.Command("go", append(runtimeBuildArgs(kind), "-o", lib, ".")...)
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

// RuntimeLinkFlags is what the runtime library needs beside it: the system
// libraries the Go runtime uses, which differ per platform, and for the shared
// kind the search path and an rpath.
//
// The rpath is what makes a dynamically linked program runnable straight after
// it is built.  Without it the loader would have to be told where the library
// is every time, through LD_LIBRARY_PATH (or DYLD_LIBRARY_PATH), and a program
// that works only under an environment variable is not a program anyone can
// hand to someone else.
func RuntimeLinkFlags(kind RuntimeKind, libDir string) []string {
	var flags []string
	switch runtime.GOOS {
	case "darwin":
		flags = []string{"-lpthread", "-framework", "CoreFoundation", "-framework", "Security"}
	case "windows":
		return []string{"-lws2_32", "-lntdll", "-luserenv"}
	default:
		flags = []string{"-lpthread", "-lm"}
	}
	if kind == RuntimeShared && libDir != "" {
		flags = append(flags, "-L"+libDir)
		// -Wl,-rpath passes the path to the linker rather than to cc, which is
		// the only way to spell it that both GNU ld and the macOS linker take.
		flags = append(flags,
			"-Wl,-rpath,"+libDir,
			// Link against the library by name so the loader records
			// "libgoscheme.so" rather than the absolute path of the cached
			// file, which would break as soon as the cache directory moved.
			"-lgoscheme")
	}
	return flags
}
