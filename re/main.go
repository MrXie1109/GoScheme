// SPDX-License-Identifier: MIT

// Command re is the GoScheme Runtime Environment: the interpreter built as a
// library that a compiled program links against.
//
// A program produced by `goscheme compile` is native code with a copy of this
// runtime inside it.  The generated code does the arithmetic it can do in
// machine words and calls in here for everything else — a call to a builtin, a
// macro, a continuation, a library — so the language stays whole while the hot
// path stays native.
//
// The boundary is a C ABI, which is what an LLVM-compiled program can call.  It
// is built with:
//
//	go build -buildmode=c-archive -o libgoscheme.a ./re
//
// and the result is a static library and a header, linked with the generated
// object file.
package main

import (
	"github.com/MrXie1109/GoScheme/internal/re"
)

func main() {
	// A main package with no main of its own: the c-archive build mode needs
	// one, and every entry point is exported to C below.  Nothing calls this.
	_ = re.UnspecifiedValue
}
