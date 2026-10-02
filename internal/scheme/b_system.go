// SPDX-License-Identifier: MIT

package scheme

import (
	"os"
	"strings"
	"time"
)

// ExitError is raised by exit / emergency-exit; the command line driver
// turns it into the process exit status.
type ExitError struct {
	Code      int
	Emergency bool
}

func (e *ExitError) Error() string { return "exit" }

// runWinds runs the outstanding dynamic-wind after thunks, innermost first.
func runWinds(m *Machine) {
	for i := len(m.winds) - 1; i >= 0; i-- {
		after := m.winds[i].after
		if after == nil {
			continue
		}
		child := m.Child()
		if _, err := child.RunApply(after, nil, child.Global); err != nil {
			// Nowhere to report it: the program is leaving.
			continue
		}
	}
	m.winds = m.winds[:0]
}

func installSystem(m *Machine) {
	// ------------------------------------------------------------- files
	m.def("call-with-input-file", 2, 2, func(m *Machine, a []Value) {
		name := wantString("call-with-input-file", a[0]).Value()
		proc := wantProcedure("call-with-input-file", a[1])
		p, err := openFilePort(m, name, true, false)
		if err != nil {
			m.RaiseError(err)
			return
		}
		m.ApplyWith(proc, []Value{p}, func(m *Machine, v Value) {
			_ = p.Close()
			m.Return(v)
		})
	}, libBase, libFile)
	m.def("call-with-output-file", 2, 2, func(m *Machine, a []Value) {
		name := wantString("call-with-output-file", a[0]).Value()
		proc := wantProcedure("call-with-output-file", a[1])
		p, err := openFilePort(m, name, false, false)
		if err != nil {
			m.RaiseError(err)
			return
		}
		m.ApplyWith(proc, []Value{p}, func(m *Machine, v Value) {
			_ = p.Flush()
			_ = p.Close()
			m.Return(v)
		})
	}, libBase, libFile)
	m.defSimple("open-input-file", 1, 1, func(a []Value) (Value, error) {
		name := wantString("open-input-file", a[0]).Value()
		return openFilePortRaw(name, true, false)
	}, libBase, libFile)
	m.defSimple("open-output-file", 1, 1, func(a []Value) (Value, error) {
		name := wantString("open-output-file", a[0]).Value()
		return openFilePortRaw(name, false, false)
	}, libBase, libFile)
	m.defSimple("open-binary-input-file", 1, 1, func(a []Value) (Value, error) {
		name := wantString("open-binary-input-file", a[0]).Value()
		return openFilePortRaw(name, true, true)
	}, libBase, libFile)
	m.defSimple("open-binary-output-file", 1, 1, func(a []Value) (Value, error) {
		name := wantString("open-binary-output-file", a[0]).Value()
		return openFilePortRaw(name, false, true)
	}, libBase, libFile)
	m.def("with-input-from-file", 2, 2, func(m *Machine, a []Value) {
		name := wantString("with-input-from-file", a[0]).Value()
		thunk := wantProcedure("with-input-from-file", a[1])
		p, err := openFilePort(m, name, true, false)
		if err != nil {
			m.RaiseError(err)
			return
		}
		before := &Primitive{Name: "install-input-port", Fn: func(mm *Machine, _ []Value) {
			mm.InParam.push(p)
			mm.Return(UnspecifiedValue)
		}}
		after := &Primitive{Name: "restore-input-port", Fn: func(mm *Machine, _ []Value) {
			mm.InParam.pop()
			_ = p.Close()
			mm.Return(UnspecifiedValue)
		}}
		m.stack = append(m.stack, &fDynamicWindPush{before: before, after: after, thunk: thunk})
		m.apply(before, nil)
	}, libBase, libFile)
	m.def("with-output-to-file", 2, 2, func(m *Machine, a []Value) {
		name := wantString("with-output-to-file", a[0]).Value()
		thunk := wantProcedure("with-output-to-file", a[1])
		p, err := openFilePort(m, name, false, false)
		if err != nil {
			m.RaiseError(err)
			return
		}
		before := &Primitive{Name: "install-output-port", Fn: func(mm *Machine, _ []Value) {
			mm.OutParam.push(p)
			mm.Return(UnspecifiedValue)
		}}
		after := &Primitive{Name: "restore-output-port", Fn: func(mm *Machine, _ []Value) {
			mm.OutParam.pop()
			_ = p.Flush()
			_ = p.Close()
			mm.Return(UnspecifiedValue)
		}}
		m.stack = append(m.stack, &fDynamicWindPush{before: before, after: after, thunk: thunk})
		m.apply(before, nil)
	}, libBase, libFile)
	m.defSimple("delete-file", 1, 1, func(a []Value) (Value, error) {
		name := wantString("delete-file", a[0]).Value()
		if err := os.Remove(name); err != nil {
			return nil, NewFileError(err.Error(), a[0])
		}
		return UnspecifiedValue, nil
	}, libBase, libFile)
	m.defSimple("file-exists?", 1, 1, func(a []Value) (Value, error) {
		name := wantString("file-exists?", a[0]).Value()
		_, err := os.Stat(name)
		return BooleanOf(err == nil), nil
	}, libBase, libFile)

	// --------------------------------------------------- process context
	m.defSimple("command-line", 0, 0, func(a []Value) (Value, error) {
		items := make([]Value, len(m.Args))
		for i, s := range m.Args {
			items[i] = NewString(s)
		}
		return List(items...), nil
	}, libBase, libProcessContext)
	m.defSimple("get-environment-variable", 1, 1, func(a []Value) (Value, error) {
		name := wantString("get-environment-variable", a[0]).Value()
		v, ok := os.LookupEnv(name)
		if !ok {
			return False, nil
		}
		return NewString(v), nil
	}, libBase, libProcessContext)
	m.defSimple("get-environment-variables", 0, 0, func(a []Value) (Value, error) {
		var items []Value
		for _, kv := range os.Environ() {
			i := strings.IndexByte(kv, '=')
			if i < 0 {
				continue
			}
			items = append(items, Cons(NewString(kv[:i]), NewString(kv[i+1:])))
		}
		return List(items...), nil
	}, libBase, libProcessContext)
	exitFn := func(emergency bool) func(*Machine, []Value) {
		return func(m *Machine, a []Value) {
			code := 0
			if len(a) > 0 {
				switch v := a[0].(type) {
				case Boolean:
					if !bool(v) {
						code = 1
					}
				case *Integer:
					c, _ := v.Int64()
					code = int(c)
				}
			}
			// R7RS: exit runs the outstanding dynamic-wind after thunks on the
			// way out.  Unwinding by an error did run them; leaving by exit
			// silently skipped them.  emergency-exit does not, by definition.
			if !emergency {
				runWinds(m)
			}
			panic(&ExitError{Code: code, Emergency: emergency})
		}
	}
	m.def("exit", 0, 1, exitFn(false), libBase, libProcessContext)
	m.def("emergency-exit", 0, 1, exitFn(true), libBase, libProcessContext)

	// ------------------------------------------------------------- time
	m.defSimple("current-second", 0, 0, func(a []Value) (Value, error) {
		return Float(float64(time.Now().UnixNano())/1e9 + 2208988800.0), nil
	}, libBase, libTime)
	m.defSimple("current-jiffy", 0, 0, func(a []Value) (Value, error) {
		return Int(time.Now().UnixNano()), nil
	}, libBase, libTime)
	m.defSimple("jiffies-per-second", 0, 0, func(a []Value) (Value, error) {
		return Int(1000000000), nil
	}, libBase, libTime)

	// ------------------------------------------------------------- eval
	m.def("eval", 2, 2, func(m *Machine, a []Value) {
		env, ok := a[1].(*Env)
		if !ok {
			m.Raise(errf("eval", "expected an environment but got %s", WriteToString(a[1])))
			return
		}
		m.Eval(a[0], env)
	}, libBase, libEval, libR5RS)
	m.def("environment", 0, -1, func(m *Machine, a []Value) {
		env := NewEnvNamed(nil, "environment")
		for _, spec := range a {
			bindings, err := m.ResolveImportSet(spec)
			if err != nil {
				m.RaiseError(err)
				return
			}
			for name, v := range bindings {
				env.Define(name, v)
			}
		}
		m.Return(env)
	}, libBase, libEval)
	m.defSimple("interaction-environment", 0, 0, func(a []Value) (Value, error) {
		return m.Global, nil
	}, libBase, libEval, libRepl, libR5RS)
	m.defSimple("scheme-report-environment", 1, 1, func(a []Value) (Value, error) {
		return m.Global, nil
	}, libR5RS)
	m.defSimple("null-environment", 1, 1, func(a []Value) (Value, error) {
		return NewEnvNamed(nil, "null"), nil
	}, libR5RS)
	m.def("load", 1, 2, func(m *Machine, a []Value) {
		name := wantString("load", a[0]).Value()
		env := m.Global
		if len(a) == 2 {
			e, ok := a[1].(*Env)
			if !ok {
				m.Raise(errf("load", "expected an environment"))
				return
			}
			env = e
		}
		path := m.resolvePath(name)
		forms, err := ReadFileForms(m, path, false)
		if err != nil {
			m.RaiseError(err)
			return
		}
		m.AddLoadPath(dirOf(path))
		m.stack = append(m.stack, &fGeneric{fn: func(mm *Machine, v Value) {
			mm.PopLoadPath()
			mm.Return(v)
		}})
		// Compiled where possible, like a script run from the command line:
		// load is how a program reads another file, and interpreting it made
		// every loaded file the tree-walker's business.
		if err := m.startForms(forms, env); err != nil {
			m.RaiseError(err)
		}
	}, libBase, libLoad)
	m.defSimple("environment-variables", 0, 0, func(a []Value) (Value, error) {
		var items []Value
		for _, kv := range os.Environ() {
			i := strings.IndexByte(kv, '=')
			if i < 0 {
				continue
			}
			items = append(items, Cons(NewString(kv[:i]), NewString(kv[i+1:])))
		}
		return List(items...), nil
	}, libBase)
}

func openFilePort(m *Machine, name string, input, binary bool) (*Port, error) {
	return openFilePortRaw(m.resolvePath(name), input, binary)
}

func openFilePortRaw(name string, input, binary bool) (*Port, error) {
	if input {
		f, err := os.Open(name)
		if err != nil {
			return nil, NewFileError(err.Error(), NewString(name))
		}
		p := NewPortFromFile(name, f, true, !binary)
		p.Binary = binary
		return p, nil
	}
	f, err := os.Create(name)
	if err != nil {
		return nil, NewFileError(err.Error(), NewString(name))
	}
	p := NewPortFromFile(name, f, false, !binary)
	p.Binary = binary
	return p, nil
}

// installR5RS adds the R5RS-only aliases.
func installR5RS(m *Machine) {
	for _, name := range []string{"force", "promise?", "make-promise", "delay", "delay-force",
		"call-with-current-continuation", "call-with-values", "dynamic-wind", "values"} {
		if _, ok := m.Builtin.Lookup(Intern(name)); ok {
			m.addExport(libR5RS, name)
		}
	}
}
