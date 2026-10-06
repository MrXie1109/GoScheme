// SPDX-License-Identifier: MIT

// Package goscheme embeds the interpreter in a Go program: evaluate Scheme,
// hand it Go functions to call, and read the results back.
//
//	i := goscheme.New()
//	i.Define("double", 1, 1, func(args []goscheme.Value) (goscheme.Value, error) {
//		n, ok := args[0].Int()
//		if !ok {
//			return goscheme.Value{}, fmt.Errorf("double: expected an integer")
//		}
//		return goscheme.Int(n * 2), nil
//	})
//	v, err := i.Eval("(double 21)")
//	fmt.Println(v) // 42
//
// A host program usually wants one of three things: to run a script the user
// wrote, to expose its own functions to that script, or to keep a little
// Scheme as a configuration language.  Interp covers all three, and Call lets
// Go drive a Scheme procedure that was passed to it.
package goscheme

import (
	"io"

	"github.com/MrXie1109/GoScheme/internal/scheme"
)

// Value is a Scheme value seen from Go.  It is a small view rather than the
// interpreter's own value type, so that programs embedding GoScheme do not
// depend on the interpreter's internals.
type Value struct{ v scheme.Value }

// Func is a Go function offered to Scheme.  Returning an error raises an
// ordinary Scheme condition, which the Scheme side may catch with guard.
type Func func(args []Value) (Value, error)

// Interp is an interpreter instance.  Each one has its own top level
// environment, so two of them cannot see each other's definitions.
type Interp struct {
	m *scheme.Machine
}

// New builds an interpreter with the full standard library and every extension
// this build has.
func New() *Interp {
	return &Interp{m: scheme.NewMachine()}
}

// Eval reads and evaluates every form in src and returns the value of the last.
func (i *Interp) Eval(src string) (Value, error) {
	v, err := i.m.EvalString(src)
	if err != nil {
		return Value{}, err
	}
	return Value{v}, nil
}

// EvalFile loads a Scheme source file and evaluates it.
//
// It runs the forms the way the command line does, which means compiling each
// one where the compiler can and interpreting the rest.  Calling RunForms
// instead would skip the compiler and evaluate the whole file as one form,
// which is both slower and a different extent for a continuation — so this is
// the compiled entry point on purpose, not by accident.
func (i *Interp) EvalFile(path string) error {
	forms, err := scheme.ReadFileForms(i.m, path, false)
	if err != nil {
		return err
	}
	i.m.AddLoadPath(dirOf(path))
	_, err = i.m.RunFormsCompiled(forms, i.m.Global)
	return err
}

// Define binds name to a Go function.  maxArgs may be -1 for no limit.
func (i *Interp) Define(name string, minArgs, maxArgs int, fn Func) {
	i.m.DefineGoFunc(name, minArgs, maxArgs, func(args []scheme.Value) (scheme.Value, error) {
		converted := make([]Value, len(args))
		for k, a := range args {
			converted[k] = Value{a}
		}
		result, err := fn(converted)
		if err != nil {
			return nil, err
		}
		return result.v, nil
	})
}

// Lookup reports the value bound to a name at the top level.
func (i *Interp) Lookup(name string) (Value, bool) {
	v, ok := i.m.LookupGlobal(name)
	if !ok {
		return Value{}, false
	}
	return Value{v}, true
}

// Call applies a Scheme procedure to arguments, from Go.  This is what makes a
// callback work: a Go function can be handed a Scheme procedure and call it.
func (i *Interp) Call(proc Value, args ...Value) (Value, error) {
	internal := make([]scheme.Value, len(args))
	for k, a := range args {
		internal[k] = a.v
	}
	child := i.m.Child()
	v, err := child.RunApply(proc.v, internal, child.Global)
	if err != nil {
		return Value{}, err
	}
	return Value{v}, nil
}

// SetOutput sends the interpreter's standard output to w, which is how a host
// program captures what a script displays.
func (i *Interp) SetOutput(w io.Writer) {
	i.m.SetStandardOutput(scheme.NewPortFromFile("stdout", w, false, true))
}

// SetArgs sets what (command-line) reports, without the program name.
func (i *Interp) SetArgs(args []string) {
	i.m.Args = append([]string(nil), args...)
}

// ------------------------------------------------------------------- values

// Int builds an exact integer.
func Int(n int64) Value { return Value{scheme.Int(n)} }

// Float builds an inexact number.
func Float(f float64) Value { return Value{scheme.Float(f)} }

// Str builds a string.
func Str(s string) Value { return Value{scheme.NewString(s)} }

// Bool builds a boolean.
func Bool(b bool) Value { return Value{scheme.BooleanOf(b)} }

// Nil is the empty list.
func Nil() Value { return Value{scheme.Nil} }

// List builds a proper list.
func List(items ...Value) Value {
	internal := make([]scheme.Value, len(items))
	for k, it := range items {
		internal[k] = it.v
	}
	return Value{scheme.List(internal...)}
}

// String renders the value the way the interpreter's `write` would.
func (v Value) String() string { return scheme.WriteToString(v.v) }

// IsNil reports whether the value is the empty list.
func (v Value) IsNil() bool {
	_, ok := v.v.(scheme.Empty)
	return ok
}

// IsFalse reports whether the value is #f, the only false value.
func (v Value) IsFalse() bool { return scheme.IsFalse(v.v) }

// Bool reads a boolean.
func (v Value) Bool() (bool, bool) {
	b, ok := v.v.(scheme.Boolean)
	return bool(b), ok
}

// Int reads an exact integer that fits in an int64.
func (v Value) Int() (int64, bool) {
	n, ok := v.v.(*scheme.Integer)
	if !ok {
		return 0, false
	}
	return n.Int64()
}

// Float reads a number as a float64, accepting exact integers as well.
func (v Value) Float() (float64, bool) {
	switch x := v.v.(type) {
	case scheme.Float:
		return float64(x), true
	case *scheme.Integer:
		if n, ok := x.Int64(); ok {
			return float64(n), true
		}
	}
	return 0, false
}

// Str reads a string.
func (v Value) Str() (string, bool) {
	s, ok := v.v.(*scheme.String)
	if !ok {
		return "", false
	}
	return s.Value(), true
}

// Slice reads a proper list as a slice.  An improper list or a non-list reports
// false, which keeps the caller from having to know about pairs.
func (v Value) Slice() ([]Value, bool) {
	items, ok := scheme.ListToSlice(v.v)
	if !ok {
		return nil, false
	}
	out := make([]Value, len(items))
	for k, it := range items {
		out[k] = Value{it}
	}
	return out, true
}

// IsProcedure reports whether the value can be applied, which is what a host
// function checks before using Call.
func (v Value) IsProcedure() bool {
	switch v.v.(type) {
	case *scheme.Primitive, *scheme.Closure, *scheme.Continuation, *scheme.Parameter:
		return true
	}
	return false
}

// Value returns the interpreter's own value.  It is here for code that already
// depends on the interpreter package, and for tests; embedding programs should
// not need it.
func (v Value) Value() scheme.Value { return v.v }

// From wraps an interpreter value.
func From(v scheme.Value) Value { return Value{v} }
