//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// resignIfNeeded re-signs a freshly written Mach-O ad hoc.  Appending the
// script invalidates the signature the Go linker produced, and on Apple
// silicon an unsigned or modified binary will not run.
func resignIfNeeded(path string) {
	if _, err := exec.LookPath("codesign"); err != nil {
		fmt.Fprintf(os.Stderr,
			"goscheme build: warning: %s may need re-signing; run: codesign --force --sign - %s\n",
			path, path)
		return
	}
	if out, err := exec.Command("codesign", "--force", "--sign", "-", path).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "goscheme build: warning: codesign failed: %v: %s\n", err, out)
	}
}
