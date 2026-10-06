// SPDX-License-Identifier: MIT

package scheme

import (
	"bytes"
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
func runtimeCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "goscheme", "runtime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
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
	cmd := exec.Command("go", "build", "-buildmode=c-archive", "-o", lib, ".")
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
func runtimePackageDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot find the interpreter package directory")
	}
	// This file lives in internal/scheme; the runtime is ../../re.
	return filepath.Join(filepath.Dir(file), "..", "..", "re"), nil
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
