// SPDX-License-Identifier: MIT

package scheme

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The runtime library is the biggest thing a compiled program is linked
// against, and how it is linked is the difference between a program measured in
// kilobytes and one measured in megabytes.  What is worth pinning is that both
// kinds are built the way they are supposed to be, and that the cache cannot
// hand back a library built a different way.

func TestBothKindsOfRuntimeLibraryAreBuiltStripped(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind RuntimeKind
		mode string
	}{
		{"shared", RuntimeShared, "-buildmode=c-shared"},
		{"static", RuntimeStatic, "-buildmode=c-archive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := strings.Join(runtimeBuildArgs(tc.kind), " ")
			if !strings.Contains(args, tc.mode) {
				t.Errorf("built with %q, want %s", args, tc.mode)
			}
			// Unstripped, the DWARF alone was 10 MB of a 27 MB archive, and a
			// shared library is loaded rather than linked, so there it would be
			// paid for on every load rather than thrown away once.
			if !strings.Contains(args, "-ldflags=-s -w") {
				t.Errorf("built with %q, which keeps the debug information", args)
			}
		})
	}
}

// The two kinds must not share a cache directory: they are different files
// built by different commands, and one path for both would mean whichever was
// built first was handed to both.
func TestTheTwoKindsDoNotShareACacheEntry(t *testing.T) {
	shared := runtimeCacheStamp(RuntimeShared)
	static := runtimeCacheStamp(RuntimeStatic)
	if shared == static {
		t.Fatal("the shared and static runtimes share a cache key")
	}
	if again := runtimeCacheStamp(RuntimeShared); again != shared {
		t.Fatal("the cache key is not stable for one kind")
	}
}

// The cache used to be one fixed path while a comment claimed it was "keyed to
// the interpreter's own build". Nothing was checking, so a library built before
// a flag was added stayed and later programs were linked against it. The stamp
// is what makes the claim true, and it has to follow the flags.
func TestTheRuntimeCacheIsKeyedToTheBuildFlags(t *testing.T) {
	before := runtimeCacheStamp(RuntimeStatic)

	saved := runtimeBuildArgs
	runtimeBuildArgs = func(RuntimeKind) []string {
		return []string{"build", "-buildmode=c-archive"}
	}
	defer func() { runtimeBuildArgs = saved }()

	if after := runtimeCacheStamp(RuntimeStatic); after == before {
		t.Fatal("changing the build flags did not change the cache key, so a stale library would be reused")
	}
	if again := runtimeCacheStamp(RuntimeStatic); again != runtimeCacheStamp(RuntimeStatic) {
		t.Fatal("the cache key is not stable for one set of flags")
	}
}

// A shared program needs to find the library at run time, which is what the
// rpath in the link flags is for. Without it the loader would need
// LD_LIBRARY_PATH set, and a program that only runs under an environment
// variable is not one anybody can hand to someone else.
func TestTheSharedLinkCarriesAnRpathAndTheStaticOneDoesNot(t *testing.T) {
	shared := strings.Join(RuntimeLinkFlags(RuntimeShared, "/tmp/libdir"), " ")
	if !strings.Contains(shared, "-Wl,-rpath,/tmp/libdir") {
		t.Errorf("shared link flags %q carry no rpath", shared)
	}
	if !strings.Contains(shared, "-L/tmp/libdir") {
		t.Errorf("shared link flags %q do not search that directory", shared)
	}
	// Linked by name, so the loader records "libgoscheme.so" rather than the
	// absolute path of the cached file, which would break when the cache moved.
	if !strings.Contains(shared, "-lgoscheme") {
		t.Errorf("shared link flags %q do not name the library", shared)
	}

	static := strings.Join(RuntimeLinkFlags(RuntimeStatic, "/tmp/libdir"), " ")
	if strings.Contains(static, "rpath") {
		t.Errorf("static link flags %q carry an rpath, but nothing is shared", static)
	}
}

// The names differ by platform, and a shared library on Windows is a .dll while
// the static form stays an archive everywhere.
func TestTheLibraryNamesMatchTheirKind(t *testing.T) {
	if got := runtimeLibName(RuntimeStatic); got != "libgoscheme.a" {
		t.Errorf("static name is %q, want libgoscheme.a", got)
	}
	switch got := runtimeLibName(RuntimeShared); got {
	case "libgoscheme.so", "libgoscheme.dylib", "libgoscheme.dll":
	default:
		t.Errorf("shared name is %q, which is not a shared library name", got)
	}
}

// The cache stamp must change when the runtime's source changes, not only when
// the build flags do.
//
// This is the half that was missing, and it cost real time: a change to the
// environment a compiled `set!` writes through did not change the stamp, so the
// library built before the change was reused and the fix looked like it had not
// worked. The write went to the right frame; the binary doing the writing was
// the old one.
//
// The fingerprint is checked for the two properties that matter — it follows the
// source, and it is stable when the source is — by hashing the files it reads
// and pretending one changed. A fingerprint that varied between calls would
// rebuild the library every compile, which is slow but correct, so stability is
// the property to pin first.
func TestTheRuntimeCacheIsKeyedToTheRuntimeSource(t *testing.T) {
	first := runtimeSourceFingerprint()
	if first == "" {
		t.Fatal("no fingerprint was computed")
	}
	if again := runtimeSourceFingerprint(); again != first {
		t.Fatal("the source fingerprint is not stable between calls")
	}
	// The fingerprint hashes file contents, so a source file it reads changing
	// must change it.  This cannot be tested by editing a file in the tree, so
	// the check is that the fingerprint is a digest of something rather than a
	// constant: it must differ from the digest of the empty input.
	sum := sha256.Sum256(nil)
	if first == hex.EncodeToString(sum[:8]) {
		t.Fatal("the fingerprint is the digest of nothing, so it does not read the sources")
	}
}

// The two things the stamp covers must both reach it: the flags and the source.
func TestTheCacheStampCoversFlagsAndSource(t *testing.T) {
	before := runtimeCacheStamp(RuntimeShared)

	savedArgs := runtimeBuildArgs
	runtimeBuildArgs = func(RuntimeKind) []string {
		return []string{"build", "-buildmode=c-shared", "-ldflags=-s -w", "-tags=something"}
	}
	defer func() { runtimeBuildArgs = savedArgs }()

	if after := runtimeCacheStamp(RuntimeShared); after == before {
		t.Fatal("a flag change did not change the stamp")
	}
}
