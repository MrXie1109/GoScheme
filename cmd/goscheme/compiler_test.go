// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// Helpers shared by the tests in this package.
//
// They live here rather than beside any one test because more than one file
// needs them: the native-compile tests run a script through the interpreter to
// get the answer to compare against, and the option tests read what a
// subcommand wrote to a stream.

// runScriptFile runs a file the way the command line does and returns what the
// program printed.
//
// It is the interpreter's answer, which is the thing the native path has to
// agree with: a test that compared a compiled program against a hard-coded
// string would pass on a day the interpreter itself was wrong.
func runScriptFile(t *testing.T, path string) string {
	t.Helper()
	m := scheme.NewMachine()
	out := scheme.NewOutputStringPort()
	m.SetStandardOutput(out)
	if code := loadFile(m, path); code != 0 {
		t.Fatalf("running %s failed with code %d", path, code)
	}
	return out.OutputString()
}
