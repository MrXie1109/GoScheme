// SPDX-License-Identifier: MIT

package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"os"
	"sync"
	"unsafe"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// unsafeAdd moves a pointer by n elements, which is how the argv array is
// walked: cgo hands over **C.char and its elements are not addressable from Go.
func unsafeAdd(p **C.char, n uintptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(unsafe.Pointer(p)) + n*unsafe.Sizeof(uintptr(0)))
}

// The C ABI of the runtime.
//
// The surface is deliberately small.  A compiled program hands the runtime the
// **source of a form** and gets back a handle; everything a form can do is the
// interpreter's business.  Exposing pairs, closures and continuations across
// the boundary instead would mean the generated code implementing the language,
// which is the compiler this deliberately is not.
//
// Handles rather than pointers: a Scheme value is a Go interface value and
// cannot be a C pointer, so the runtime keeps a table and hands out an index.
// The table is append-only within a run, which is what makes a handle stable.

var (
	mu      sync.Mutex
	handles []scheme.Value
	mach    *scheme.Machine
	native  int
)

func store(v scheme.Value) int64 {
	mu.Lock()
	defer mu.Unlock()
	handles = append(handles, v)
	return int64(len(handles) - 1)
}

func load(h int64) scheme.Value {
	mu.Lock()
	defer mu.Unlock()
	if h < 0 || int(h) >= len(handles) {
		return scheme.UnspecifiedValue
	}
	return handles[h]
}

func goString(p *C.char, n int64) string {
	if p == nil || n <= 0 {
		return ""
	}
	return C.GoStringN(p, C.int(n))
}

//export gs_init
func gs_init(argc C.int, argv **C.char) {
	mach = scheme.NewMachine()
	args := make([]string, 0, int(argc))
	for i := 0; i < int(argc); i++ {
		args = append(args, C.GoString(*argv))
		argv = (**C.char)(unsafeAdd(argv, 1))
	}
	mach.Args = args
}

//export gs_finish
func gs_finish() {}

//export gs_eval_source
func gs_eval_source(src *C.char, n C.longlong, name *C.char) C.longlong {
	source := goString(src, int64(n))
	if mach == nil {
		gs_init(0, nil)
	}
	r := scheme.NewStringReader(source)
	if name != nil {
		r.Source = C.GoString(name)
	}
	forms, err := r.ReadAll()
	if err != nil {
		report(err)
		return -1
	}
	v, err := mach.RunFormsCompiled(forms, mach.Global)
	if err != nil {
		report(err)
		return -1
	}
	return C.longlong(store(v))
}

//export gs_note_native
func gs_note_native(label *C.char, n C.int) { native = int(n) }

func report(err error) {
	if err == nil {
		return
	}
	if ee, ok := err.(*scheme.ExitError); ok {
		os.Exit(ee.Code)
	}
	os.Stderr.WriteString("goscheme: " + err.Error() + "\n")
}
