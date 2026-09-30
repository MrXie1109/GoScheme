// SPDX-License-Identifier: MIT

package scheme

import "fmt"

// R7RS library names.
const (
	libBase           = "(scheme base)"
	libCaseLambda     = "(scheme case-lambda)"
	libChar           = "(scheme char)"
	libComplex        = "(scheme complex)"
	libCxr            = "(scheme cxr)"
	libEval           = "(scheme eval)"
	libFile           = "(scheme file)"
	libInexact        = "(scheme inexact)"
	libLazy           = "(scheme lazy)"
	libLoad           = "(scheme load)"
	libProcessContext = "(scheme process-context)"
	libRead           = "(scheme read)"
	libRepl           = "(scheme repl)"
	libTime           = "(scheme time)"
	libWrite          = "(scheme write)"
	libR5RS           = "(scheme r5rs)"
)

// def registers a machine-aware procedure.
func (m *Machine) def(name string, min, max int, fn func(*Machine, []Value), libs ...string) *Primitive {
	// Argument checks panic with *ErrorObject.  Every primitive is wrapped so
	// that a panic becomes a raised condition: without this, the 300-odd
	// primitives registered through def let the panic fly past the Scheme
	// handlers and terminate the program, while the ones registered through
	// defSimple were catchable — the same mistake behaving two ways.
	wrapped := func(m *Machine, args []Value) {
		defer func() {
			if r := recover(); r != nil {
				switch e := r.(type) {
				case *ErrorObject:
					m.RaiseError(e)
				case *SchemeError:
					m.RaiseError(e)
				case *PortError:
					m.RaiseError(NewFileError(e.Msg))
				default:
					panic(r)
				}
			}
		}()
		fn(m, args)
	}
	p := &Primitive{Name: name, MinArgs: min, MaxArgs: max, Fn: wrapped}
	m.Builtin.DefineName(name, p)
	for _, l := range libs {
		m.addExport(l, name)
	}
	return p
}

// defSimple registers a primitive implemented as a plain Go function.  The
// function may panic with *ErrorObject to signal a Scheme error; the wrapper
// turns that into a raised condition.
func (m *Machine) defSimple(name string, min, max int, fn func([]Value) (Value, error), libs ...string) *Primitive {
	return m.def(name, min, max, func(m *Machine, args []Value) {
		v, err := callSimple(name, fn, args)
		if err != nil {
			m.RaiseError(err)
			return
		}
		m.Return(v)
	}, libs...)
}

func callSimple(name string, fn func([]Value) (Value, error), args []Value) (v Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *ErrorObject:
				v, err = nil, e
			case *SchemeError:
				v, err = nil, e
			case *PortError:
				v, err = nil, NewFileError(e.Msg)
			default:
				panic(r)
			}
		}
	}()
	v, err = fn(args)
	if v == nil {
		v = UnspecifiedValue
	}
	return v, err
}

// installerHooks holds the installers that the extension files register in
// their init functions.  Registration keeps this file from being the single
// place every new library has to be threaded through.
var installerHooks []func(*Machine)

// registerInstaller adds fn to the list run when a machine is built.
func registerInstaller(fn func(*Machine)) {
	installerHooks = append(installerHooks, fn)
}

// defSyntax records a syntactic keyword as exported by a library.
func (m *Machine) defSyntax(lib string, names ...string) {
	for _, n := range names {
		m.Builtin.DefineName(n, &SyntaxKeyword{Name: n})
		m.addExport(lib, n)
	}
}

// ---------------------------------------------------------------------------
// Argument checking helpers
// ---------------------------------------------------------------------------

func errf(name, format string, args ...interface{}) *ErrorObject {
	return NewError(name + ": " + fmt.Sprintf(format, args...))
}

func wantNumber(name string, v Value) Value {
	if !IsNumber(v) {
		panic(errf(name, "expected a number but got %s", WriteToString(v)))
	}
	return v
}

func wantReal(name string, v Value) Value {
	if !IsReal(v) {
		panic(errf(name, "expected a real number but got %s", WriteToString(v)))
	}
	return v
}

func wantInteger(name string, v Value) *Integer {
	i, ok := v.(*Integer)
	if !ok {
		panic(errf(name, "expected an exact integer but got %s", WriteToString(v)))
	}
	return i
}

func wantIndex(name string, v Value) int {
	i, ok := v.(*Integer)
	if !ok {
		panic(errf(name, "expected an exact integer index but got %s", WriteToString(v)))
	}
	n, ok := i.Int64()
	if !ok || n < 0 || n > 1<<40 {
		panic(errf(name, "index out of range: %s", WriteToString(v)))
	}
	return int(n)
}

func wantString(name string, v Value) *String {
	s, ok := v.(*String)
	if !ok {
		panic(errf(name, "expected a string but got %s", WriteToString(v)))
	}
	return s
}

func wantSymbol(name string, v Value) *Symbol {
	s, ok := v.(*Symbol)
	if !ok {
		panic(errf(name, "expected a symbol but got %s", WriteToString(v)))
	}
	return s
}

func wantChar(name string, v Value) Char {
	c, ok := v.(Char)
	if !ok {
		panic(errf(name, "expected a character but got %s", WriteToString(v)))
	}
	return c
}

func wantPair(name string, v Value) *Pair {
	p, ok := v.(*Pair)
	if !ok {
		panic(errf(name, "expected a pair but got %s", WriteToString(v)))
	}
	return p
}

func wantVector(name string, v Value) *Vector {
	p, ok := v.(*Vector)
	if !ok {
		panic(errf(name, "expected a vector but got %s", WriteToString(v)))
	}
	return p
}

func wantBytevector(name string, v Value) *Bytevector {
	p, ok := v.(*Bytevector)
	if !ok {
		panic(errf(name, "expected a bytevector but got %s", WriteToString(v)))
	}
	return p
}

func wantList(name string, v Value) []Value {
	items, ok := ListToSlice(v)
	if !ok {
		panic(errf(name, "expected a proper list but got %s", WriteToString(v)))
	}
	return items
}

func wantProcedure(name string, v Value) Value {
	switch v.(type) {
	case *Closure, *Primitive, *Continuation, *Parameter:
		return v
	}
	panic(errf(name, "expected a procedure but got %s", WriteToString(v)))
}

func wantPort(name string, v Value) *Port {
	p, ok := v.(*Port)
	if !ok {
		panic(errf(name, "expected a port but got %s", WriteToString(v)))
	}
	return p
}

func wantInputPort(name string, v Value) *Port {
	p := wantPort(name, v)
	if !p.IsInput {
		panic(errf(name, "expected an input port"))
	}
	return p
}

func wantOutputPort(name string, v Value) *Port {
	p := wantPort(name, v)
	if !p.IsOut {
		panic(errf(name, "expected an output port"))
	}
	return p
}

func wantTextual(name string, v Value) *Port {
	p := wantPort(name, v)
	if p.Binary {
		panic(errf(name, "expected a textual port"))
	}
	return p
}

// ---------------------------------------------------------------------------
// installBuiltins
// ---------------------------------------------------------------------------

func installBuiltins(m *Machine) {
	installCore(m)
	installNumbers(m)
	installLists(m)
	installStrings(m)
	installChars(m)
	installVectors(m)
	installControl(m)
	installIO(m)
	installSystem(m)
	installHashtables(m)
	installConcurrency(m)
	installSync(m)
	installSockets(m)
	installProcess(m)
	installFFI(m)
	// Libraries that register themselves, so that adding one means adding a
	// file rather than editing this list.
	for _, install := range installerHooks {
		install(m)
	}
	installR5RS(m)
	installSyntaxExports(m)
}

func installSyntaxExports(m *Machine) {
	base := []string{
		"quote", "lambda", "if", "define", "set!", "begin", "cond", "case", "and",
		"or", "when", "unless", "let", "let*", "letrec", "letrec*", "let-values",
		"let*-values", "define-values", "do", "delay", "delay-force", "parameterize",
		"guard", "quasiquote", "unquote", "unquote-splicing", "define-record-type",
		"define-syntax", "let-syntax", "letrec-syntax", "syntax-rules", "else", "=>",
		"...", "_", "include", "include-ci", "cond-expand", "import", "define-library",
		"case-lambda",
	}
	m.defSyntax(libBase, base...)
	m.defSyntax(libCaseLambda, "case-lambda")
	m.defSyntax(libChannel, "go", "select")
	m.defSyntax(libLazy, "delay", "delay-force")
	m.defSyntax(libFile, "define-record-type")
	m.defSyntax(libR5RS, "quote", "lambda", "if", "define", "set!", "begin", "cond",
		"case", "and", "or", "let", "let*", "letrec", "do", "delay", "quasiquote",
		"unquote", "unquote-splicing", "define-syntax", "let-syntax", "letrec-syntax",
		"syntax-rules", "else", "=>")
}

// ---------------------------------------------------------------------------
// Core: equivalence, booleans, symbols, miscellaneous predicates
// ---------------------------------------------------------------------------

func installCore(m *Machine) {
	m.defSimple("eq?", 2, 2, func(a []Value) (Value, error) {
		return BooleanOf(Eq(a[0], a[1])), nil
	}, libBase, libR5RS)
	m.defSimple("eqv?", 2, 2, func(a []Value) (Value, error) {
		return BooleanOf(Eqv(a[0], a[1])), nil
	}, libBase, libR5RS)
	m.defSimple("equal?", 2, 2, func(a []Value) (Value, error) {
		return BooleanOf(Equal(a[0], a[1])), nil
	}, libBase, libR5RS)
	m.defSimple("not", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(IsFalse(a[0])), nil
	}, libBase, libR5RS)
	m.defSimple("boolean?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(Boolean)
		return BooleanOf(ok), nil
	}, libBase, libR5RS)
	m.defSimple("boolean=?", 2, -1, func(a []Value) (Value, error) {
		first, ok := a[0].(Boolean)
		if !ok {
			panic(errf("boolean=?", "expected a boolean"))
		}
		for _, v := range a[1:] {
			b, ok := v.(Boolean)
			if !ok {
				panic(errf("boolean=?", "expected a boolean"))
			}
			if b != first {
				return False, nil
			}
		}
		return True, nil
	}, libBase)

	m.defSimple("symbol?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Symbol)
		return BooleanOf(ok), nil
	}, libBase, libR5RS)
	m.defSimple("symbol=?", 2, -1, func(a []Value) (Value, error) {
		first := wantSymbol("symbol=?", a[0])
		for _, v := range a[1:] {
			if wantSymbol("symbol=?", v).Name != first.Name {
				return False, nil
			}
		}
		return True, nil
	}, libBase)
	m.defSimple("symbol->string", 1, 1, func(a []Value) (Value, error) {
		return NewString(wantSymbol("symbol->string", a[0]).Name), nil
	}, libBase, libR5RS)
	m.defSimple("string->symbol", 1, 1, func(a []Value) (Value, error) {
		return Intern(wantString("string->symbol", a[0]).Value()), nil
	}, libBase, libR5RS)

	m.defSimple("procedure?", 1, 1, func(a []Value) (Value, error) {
		switch a[0].(type) {
		case *Closure, *Primitive, *Continuation, *Parameter:
			return True, nil
		}
		return False, nil
	}, libBase, libR5RS)
	m.defSimple("error-object?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*ErrorObject)
		return BooleanOf(ok), nil
	}, libBase)
	m.defSimple("error-object-message", 1, 1, func(a []Value) (Value, error) {
		e, ok := a[0].(*ErrorObject)
		if !ok {
			panic(errf("error-object-message", "expected an error object"))
		}
		return NewString(e.Message), nil
	}, libBase)
	m.defSimple("error-object-irritants", 1, 1, func(a []Value) (Value, error) {
		e, ok := a[0].(*ErrorObject)
		if !ok {
			panic(errf("error-object-irritants", "expected an error object"))
		}
		return List(e.Irritants...), nil
	}, libBase)
	m.defSimple("read-error?", 1, 1, func(a []Value) (Value, error) {
		e, ok := a[0].(*ErrorObject)
		return BooleanOf(ok && e.Kind == errRead), nil
	}, libBase)
	m.defSimple("file-error?", 1, 1, func(a []Value) (Value, error) {
		e, ok := a[0].(*ErrorObject)
		return BooleanOf(ok && e.Kind == errFile), nil
	}, libBase)
	m.defSimple("error", 1, -1, func(a []Value) (Value, error) {
		msg := DisplayToString(a[0])
		if s, ok := a[0].(*String); ok {
			msg = s.Value()
		}
		return nil, NewError(msg, a[1:]...)
	}, libBase)

	m.defSimple("features", 0, 0, func(a []Value) (Value, error) {
		return m.FeatureList(), nil
	}, libBase)

}
