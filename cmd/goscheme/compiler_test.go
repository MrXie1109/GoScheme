// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"os"
	"testing"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	w.Close()
	os.Stdout = saved
	return <-done
}

// runScriptFile runs a file the way the command line does and returns what the
// program printed.
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
