// SPDX-License-Identifier: MIT

package re

import (
	"os/exec"
	"strings"
	"testing"
)

// TestREHasNoDependencyOnTheVM is the invariant the split rests on.  This
// package is the runtime environment: the value representation, the numeric
// tower, the reader and printer, the ports, and the standard procedures.  The
// machine that evaluates Scheme depends on it and not the other way round, and
// if that ever reverses the two are one package with an import statement in
// between.
//
// It is checked by asking the toolchain rather than by reading the imports,
// because a transitive dependency is what would actually do the damage and a
// transitive dependency is not visible in this package's own import block.
func TestREHasNoDependencyOnTheVM(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	if strings.Contains(string(out), "internal/scheme") {
		t.Fatal("internal/re depends on internal/scheme; the dependency must go one way")
	}
}
