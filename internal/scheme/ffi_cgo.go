//go:build cgo

package scheme

/*
#cgo linux LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

static void *gs_dlopen(const char *name) {
	return dlopen(name, RTLD_NOW | RTLD_GLOBAL);
}
static void *gs_dlsym(void *handle, const char *name) { return dlsym(handle, name); }
static const char *gs_dlerror(void) { return dlerror(); }

// One helper per arity for the two calling conventions this FFI supports:
// every argument integral (passed as long, which also covers pointers and
// strings) or every argument a double.  Calling through a cast function
// pointer is not strictly portable C, but it is exactly what dlopen-based
// foreign function interfaces have always done on these ABIs.
static long gs_call_0_l(void *f) { return ((long (*)(void))f)(); }
static long gs_call_1_l(void *f, long a) { return ((long (*)(long))f)(a); }
static long gs_call_2_l(void *f, long a, long b) { return ((long (*)(long, long))f)(a, b); }
static long gs_call_3_l(void *f, long a, long b, long c) { return ((long (*)(long, long, long))f)(a, b, c); }
static long gs_call_4_l(void *f, long a, long b, long c, long d) {
	return ((long (*)(long, long, long, long))f)(a, b, c, d);
}

static double gs_call_0_d(void *f) { return ((double (*)(void))f)(); }
static double gs_call_1_d(void *f, double a) { return ((double (*)(double))f)(a); }
static double gs_call_2_d(void *f, double a, double b) { return ((double (*)(double, double))f)(a, b); }
static double gs_call_3_d(void *f, double a, double b, double c) {
	return ((double (*)(double, double, double))f)(a, b, c);
}

// The other two combinations: integral arguments with a double result (strtod)
// and double arguments with an integral result (lround).
static double gs_call_1_ld(void *f, long a) { return ((double (*)(long))f)(a); }
static double gs_call_2_ld(void *f, long a, long b) { return ((double (*)(long, long))f)(a, b); }
static double gs_call_3_ld(void *f, long a, long b, long c) {
	return ((double (*)(long, long, long))f)(a, b, c);
}
static double gs_call_4_ld(void *f, long a, long b, long c, long d) {
	return ((double (*)(long, long, long, long))f)(a, b, c, d);
}
static long gs_call_1_dl(void *f, double a) { return ((long (*)(double))f)(a); }
static long gs_call_2_dl(void *f, double a, double b) { return ((long (*)(double, double))f)(a, b); }
static long gs_call_3_dl(void *f, double a, double b, double c) {
	return ((long (*)(double, double, double))f)(a, b, c);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// HasFFI reports whether this build can load shared libraries.
const HasFFI = true

func init() { ffiAvailable = true }

// ForeignLibrary is a shared library opened with load-shared-library.
type ForeignLibrary struct {
	Name   string
	handle unsafe.Pointer
}

// ffiKind is the type of one foreign argument or result.
type ffiKind int

const (
	ffiVoid ffiKind = iota
	ffiLong
	ffiDouble
	ffiString
	ffiPointer
)

func parseFFIType(name string, isArg bool) (ffiKind, error) {
	switch name {
	case "void":
		if isArg {
			return 0, fmt.Errorf("void is not a valid argument type")
		}
		return ffiVoid, nil
	case "int", "long", "size_t", "ssize_t":
		return ffiLong, nil
	case "double":
		return ffiDouble, nil
	case "string", "char*":
		return ffiString, nil
	case "pointer", "void*":
		return ffiPointer, nil
	}
	return 0, fmt.Errorf("unknown foreign type %s", name)
}

// installFFI provides load-shared-library and foreign-function.
func installFFI(m *Machine) {
	const lib = "(goscheme ffi)"

	m.defSimple("load-shared-library", 1, 1, func(a []Value) (Value, error) {
		name := ""
		switch x := a[0].(type) {
		case *String:
			name = x.Value()
		case Boolean:
			if bool(x) {
				return nil, errf("load-shared-library", "expected a library name or #f")
			}
		default:
			return nil, errf("load-shared-library", "expected a library name or #f")
		}
		var handle unsafe.Pointer
		if name == "" {
			// No name: the symbols already loaded with the process, which is
			// where libc lives.
			handle = C.gs_dlopen(nil)
		} else {
			cname := C.CString(name)
			defer C.free(unsafe.Pointer(cname))
			handle = C.gs_dlopen(cname)
		}
		if handle == nil {
			msg := "cannot load " + name
			if name == "" {
				msg = "cannot open the running program's symbols"
			}
			if e := C.gs_dlerror(); e != nil {
				msg += ": " + C.GoString(e)
			}
			return nil, NewFileError(msg, a[0])
		}
		if name == "" {
			name = "(self)"
		}
		return &ForeignLibrary{Name: name, handle: handle}, nil
	}, lib)

	m.defSimple("foreign-library?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*ForeignLibrary)
		return BooleanOf(ok), nil
	}, lib)

	m.def("foreign-function", 3, -1, func(m *Machine, a []Value) {
		lib, ok := a[0].(*ForeignLibrary)
		if !ok {
			m.Raise(errf("foreign-function", "expected a foreign library but got %s", WriteToString(a[0])))
			return
		}
		sym, ok := a[1].(*Symbol)
		if !ok {
			m.Raise(errf("foreign-function", "expected a symbol as the function name"))
			return
		}
		retName, ok := a[2].(*Symbol)
		if !ok {
			m.Raise(errf("foreign-function", "expected a symbol as the return type"))
			return
		}
		ret, err := parseFFIType(retName.Name, false)
		if err != nil {
			m.RaiseError(err)
			return
		}
		args := make([]ffiKind, 0, len(a)-3)
		doubles, integrals := 0, 0
		for _, t := range a[3:] {
			s, ok := t.(*Symbol)
			if !ok {
				m.Raise(errf("foreign-function", "expected a symbol as an argument type"))
				return
			}
			k, err := parseFFIType(s.Name, true)
			if err != nil {
				m.RaiseError(err)
				return
			}
			if k == ffiDouble {
				doubles++
			} else {
				integrals++
			}
			args = append(args, k)
		}
		if doubles > 0 && integrals > 0 {
			m.Raise(NewError("foreign-function: arguments must be either all " +
				"integral (int, long, string, pointer) or all double"))
			return
		}
		if doubles > 3 || integrals > 4 {
			m.Raise(NewError("foreign-function: at most 4 integral or 3 double arguments"))
			return
		}

		csym := C.CString(sym.Name)
		fn := C.gs_dlsym(lib.handle, csym)
		C.free(unsafe.Pointer(csym))
		if fn == nil {
			msg := "cannot find " + sym.Name + " in " + lib.Name
			if e := C.gs_dlerror(); e != nil {
				msg += ": " + C.GoString(e)
			}
			m.Raise(NewError(msg))
			return
		}
		m.Return(makeForeignFunction(sym.Name, fn, ret, args))
	}, lib)
}

// makeForeignFunction wraps a C function pointer as a Scheme procedure.
func makeForeignFunction(name string, fn unsafe.Pointer, ret ffiKind, args []ffiKind) *Primitive {
	n := len(args)
	allLong := true
	for _, k := range args {
		if k == ffiDouble {
			allLong = false
		}
	}
	return &Primitive{
		Name:    name,
		MinArgs: n,
		MaxArgs: n,
		Fn: func(m *Machine, a []Value) {
			// Convert the arguments, keeping any C strings alive until the
			// call has returned.
			var longs []C.long
			var doubles []C.double
			var allocated []unsafe.Pointer
			for i, kind := range args {
				switch kind {
				case ffiDouble:
					f, err := ffiNumber(name, a[i])
					if err != nil {
						m.RaiseError(err)
						return
					}
					doubles = append(doubles, C.double(f))
				case ffiString:
					s, ok := a[i].(*String)
					if !ok {
						m.Raise(errf(name, "argument %d should be a string but is %s", i+1, WriteToString(a[i])))
						return
					}
					cs := C.CString(s.Value())
					allocated = append(allocated, unsafe.Pointer(cs))
					longs = append(longs, C.long(uintptr(unsafe.Pointer(cs))))
				default:
					v, err := ffiInteger(name, a[i])
					if err != nil {
						m.RaiseError(err)
						return
					}
					longs = append(longs, C.long(v))
				}
			}
			defer func() {
				for _, p := range allocated {
					C.free(p)
				}
			}()

			if allLong {
				if ret == ffiDouble {
					m.Return(Float(float64(callLongToDouble(fn, longs))))
					return
				}
				m.Return(ffiResult(ret, callLong(fn, longs), 0))
				return
			}
			if ret == ffiDouble {
				m.Return(Float(float64(callDouble(fn, doubles))))
				return
			}
			m.Return(ffiResult(ret, callDoubleToLong(fn, doubles), 0))
		},
	}
}

func callLong(fn unsafe.Pointer, a []C.long) C.long {
	switch len(a) {
	case 0:
		return C.gs_call_0_l(fn)
	case 1:
		return C.gs_call_1_l(fn, a[0])
	case 2:
		return C.gs_call_2_l(fn, a[0], a[1])
	case 3:
		return C.gs_call_3_l(fn, a[0], a[1], a[2])
	default:
		return C.gs_call_4_l(fn, a[0], a[1], a[2], a[3])
	}
}

func callDouble(fn unsafe.Pointer, a []C.double) C.double {
	switch len(a) {
	case 0:
		return C.gs_call_0_d(fn)
	case 1:
		return C.gs_call_1_d(fn, a[0])
	case 2:
		return C.gs_call_2_d(fn, a[0], a[1])
	default:
		return C.gs_call_3_d(fn, a[0], a[1], a[2])
	}
}

// callLongToDouble calls a function that takes integral arguments and returns
// a double, such as strtod.
func callLongToDouble(fn unsafe.Pointer, a []C.long) C.double {
	switch len(a) {
	case 0:
		return C.gs_call_0_d(fn)
	case 1:
		return C.gs_call_1_ld(fn, a[0])
	case 2:
		return C.gs_call_2_ld(fn, a[0], a[1])
	case 3:
		return C.gs_call_3_ld(fn, a[0], a[1], a[2])
	default:
		return C.gs_call_4_ld(fn, a[0], a[1], a[2], a[3])
	}
}

// callDoubleToLong calls a function that takes double arguments and returns an
// integral result, such as lround.
func callDoubleToLong(fn unsafe.Pointer, a []C.double) C.long {
	switch len(a) {
	case 0:
		return C.gs_call_0_l(fn)
	case 1:
		return C.gs_call_1_dl(fn, a[0])
	case 2:
		return C.gs_call_2_dl(fn, a[0], a[1])
	default:
		return C.gs_call_3_dl(fn, a[0], a[1], a[2])
	}
}

// ffiResult converts what the C function returned into a Scheme value.
func ffiResult(ret ffiKind, l C.long, d C.double) Value {
	switch ret {
	case ffiVoid:
		return UnspecifiedValue
	case ffiDouble:
		return Float(float64(d))
	case ffiString:
		if l == 0 {
			return False
		}
		return NewString(C.GoString((*C.char)(unsafe.Pointer(uintptr(l)))))
	default:
		return Int(int64(l))
	}
}

func ffiNumber(name string, v Value) (float64, error) {
	if !IsReal(v) {
		return 0, errf(name, "expected a real number but got %s", WriteToString(v))
	}
	return asFloat(v), nil
}

func ffiInteger(name string, v Value) (int64, error) {
	i, ok := v.(*Integer)
	if !ok {
		return 0, errf(name, "expected an exact integer but got %s", WriteToString(v))
	}
	n, ok := i.Int64()
	if !ok {
		return 0, errf(name, "integer out of range: %s", WriteToString(v))
	}
	return n, nil
}
