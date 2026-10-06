// SPDX-License-Identifier: MIT

package scheme

import (
	"strings"
	"testing"
)

// The runtime archive is several megabytes and is built once per flag set, so
// two things about it are worth pinning: that it is built with the debug
// information stripped, and that changing how it is built produces a different
// cache directory rather than reusing the old archive.
//
// The size is the reason. Unstripped, the DWARF sections alone were 10 MB of a
// 28 MB archive, and every compiled program paid for them on disk before the
// linker threw them away: a `compiled` hello-world was 18 MB and is 8.5 MB now.

func TestTheRuntimeArchiveIsBuiltStripped(t *testing.T) {
	args := strings.Join(runtimeArchiveBuildArgs(), " ")
	if !strings.Contains(args, "-ldflags=-s -w") {
		t.Fatalf("the archive is built with %q, which keeps the debug information", args)
	}
	if !strings.Contains(args, "-buildmode=c-archive") {
		t.Fatalf("the archive is built with %q, which is not a C archive", args)
	}
}

// The cache used to be one fixed path while a comment claimed it was "keyed to
// the interpreter's own build". Nothing was checking, so an archive built before
// a flag was added stayed and later programs were linked against it. The stamp
// is what makes the claim true, and it has to follow the flags.
func TestTheRuntimeCacheIsKeyedToTheBuildFlags(t *testing.T) {
	before := runtimeCacheStamp()

	saved := runtimeArchiveBuildArgs
	runtimeArchiveBuildArgs = func() []string {
		return []string{"build", "-buildmode=c-archive"}
	}
	defer func() { runtimeArchiveBuildArgs = saved }()

	if after := runtimeCacheStamp(); after == before {
		t.Fatal("changing the build flags did not change the cache key, so a stale archive would be reused")
	}
	if again := runtimeCacheStamp(); again != runtimeCacheStamp() {
		t.Fatal("the cache key is not stable for one set of flags")
	}
}
