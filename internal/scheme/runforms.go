// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// Running a sequence of top-level forms.
//
// This is what a file, `-e`, the REPL and `load` all go through: each form is
// compiled where the compiler understands it and evaluated where it does not,
// and the ones that teach the compiler something — an import, a macro
// definition — are evaluated on the spot, because the forms after them cannot be
// compiled until they have been.
//
// There is no compiled file any more: this runs the forms it is handed, in
// memory.  The distinction it draws between compiled and interpreted code is
// still the one that matters, and `-interp` still turns the whole thing off.

// teachingForm reports whether a top-level form changes what the compiler
// knows, and so has to be run while compiling: an import, a macro definition,
// an include — or a macro call that expands into one of those.  The R7RS
// suite's
//
//	(define-syntax be-like-begin1
//	  (syntax-rules () ((_ name) (define-syntax name ...))))
//	(be-like-begin1 sequence1)
//
// is the second kind: the forms after it can only be compiled once sequence1
// is a macro, and compiling them first turned (sequence1 0 1 2 3) into a call
// to a binding that turned out to be syntax.
func teachingForm(form Value, m *Machine, env *Env) bool {
	if syntacticTeachingForm(form) {
		return true
	}
	if p, ok := form.(*Pair); ok {
		if s, ok := p.Car.(*Symbol); ok && s.Name == "cond-expand" {
			return condExpandIsTeaching(m, form)
		}
	}
	p, ok := form.(*Pair)
	if !ok {
		return false
	}
	sym, ok := p.Car.(*Symbol)
	if !ok {
		return false
	}
	v, ok := env.Lookup(sym)
	if !ok {
		return false
	}
	mac, ok := v.(*Macro)
	if !ok {
		return false
	}
	expanded, err := mac.Expand(form, env)
	if err != nil {
		return false // the compiler will report it when it compiles the form
	}
	return syntacticTeachingForm(expanded)
}

// condExpandIsTeaching runs the same choice cond-expand would, to see whether
// what it chose teaches the compiler something: an import or a define-syntax
// inside a cond-expand has to be run while compiling like any other.
func condExpandIsTeaching(m *Machine, form Value) bool {
	p, ok := form.(*Pair)
	if !ok {
		return false
	}
	forms, ok := condExpandBody(m, mustSlice(p.Cdr))
	if !ok {
		return false
	}
	for _, f := range forms {
		if teachingForm(f, m, m.Global) {
			return true
		}
	}
	return false
}

// syntacticTeachingForm is the check that needs no expansion.
func syntacticTeachingForm(form Value) bool {
	p, ok := form.(*Pair)
	if !ok {
		return false
	}
	s, ok := p.Car.(*Symbol)
	if !ok {
		return false
	}
	switch s.Name {
	case "import", "define-syntax", "include", "include-ci", "define-library":
		return true
	case "begin":
		// A top-level begin may hold definitions of macros, which have to be
		// run for the rest to compile.
		items, _ := ListToSlice(p.Cdr)
		for _, it := range items {
			if syntacticTeachingForm(it) {
				return true
			}
		}
	}
	return false
}

// RunFormsCompiled runs a sequence of top-level forms, compiling each where it
// can.  It is what runs a file: a form that the compiler does not understand is
// interpreted, and the file as a whole is neither all one nor all the other.
//
// A form that teaches the compiler something is evaluated as it is met, since
// the rest of the file cannot be compiled until it has been.  That is why this
// cannot simply compile everything first and run it afterwards.
func (m *Machine) RunFormsCompiled(forms []Value, env *Env) (Value, error) {
	if m.Interpret || compileDisabled {
		return m.RunForms(forms, env)
	}
	return m.guardedRun(func() (Value, error) {
		baseStack, baseWinds, baseHands := len(m.stack), len(m.winds), len(m.hands)
		if err := m.startForms(forms, env); err != nil {
			return nil, err
		}
		return m.runLoop(baseStack, baseWinds, baseHands)
	})
}

// startForms begins the forms of a file in the *current* evaluation.  The
// machine's own loop drives them, so this is what a primitive that loads a file
// uses: `load` cannot call RunFormsCompiled, because that would start a second
// evaluation inside the one that called it.
func (m *Machine) startForms(forms []Value, env *Env) error {
	if m.Interpret || compileDisabled {
		m.EvalSeq(forms, env)
		return nil
	}
	if len(forms) == 0 {
		m.Return(UnspecifiedValue)
		return nil
	}
	form := forms[0]
	// A form that teaches the compiler something is interpreted, because its
	// point is its effect on the environment — the macro it defines, the
	// library it loads — and a compiled define-syntax expands its uses at
	// compile time and defines nothing.
	//
	// The rest of the file waits in a frame, so that this form and the ones
	// after it share one evaluation.  That is what makes a continuation
	// captured at the top level span the rest of the file, and what lets a
	// `load` inside a form come back to the form after it: running each form
	// in an evaluation of its own looks equivalent and is not — the value of a
	// form is lost, and `(letrec ((f (lambda () 7))) (f))` came back
	// unspecified because the tail call's result had nowhere to return to.
	if len(forms) > 1 {
		m.stack = append(m.stack, &fForms{forms: forms[1:], env: env})
	}
	if teachingForm(form, m, env) {
		_, err := m.runInterpreted(form, env)
		return err
	}
	if code, err := m.compile(form, env); err == nil && code != nil {
		m.runCompiledTop(code, env)
		return nil
	}
	// The compiler declined this form; the tree-walker takes it, in the same
	// evaluation.
	m.Eval(form, env)
	return nil
}

// fForms runs the remaining top-level forms of a file after the one being
// evaluated returns.  It is what keeps a file in one extent: the forms are run
// one after another inside a single evaluation, so a continuation captured in
// one of them spans the rest, and a `load` from inside one of them continues
// where it left off rather than starting an evaluation of its own.
type fForms struct {
	forms []Value
	env   *Env
}

func (f *fForms) resume(m *Machine, _ Value) { m.startForms(f.forms, f.env) }
