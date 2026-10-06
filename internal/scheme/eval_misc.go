// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// Lazy evaluation
// ---------------------------------------------------------------------------

func makeThunk(body []Value, env *Env) *Closure {
	return &Closure{Clauses: []ClosureClause{{Body: body, BodyNames: scanBodyNames(body)}}, Env: env}
}

// installDelayThunk registers the primitive a compiled `delay` builds its
// promise through.
//
// A compiled `delay` is a thunk — the body is machine code and compiles like any
// other — but a promise is the interpreter's object, and forcing one walks a
// chain of promises and runs Scheme code.  So the compiled form calls here with
// the thunk it built, and what comes back is an ordinary Scheme promise that
// `force` handles without knowing a compiler was involved.
func installDelayThunk(m *Machine) {
	m.def("make-promise-from-thunk", 1, 2, func(m *Machine, a []Value) {
		thunk := wantProcedure("make-promise-from-thunk", a[0])
		force := len(a) == 2 && IsTrue(a[1])
		m.Return(&Promise{Thunk: thunk, IsDelayForce: force})
	})
}

func evalDelay(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) != 1 {
		m.Raise(NewError("delay: expected one expression"))
		return
	}
	m.Return(&Promise{Thunk: makeThunk(args, env)})
}

func evalDelayForce(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) != 1 {
		m.Raise(NewError("delay-force: expected one expression"))
		return
	}
	m.Return(&Promise{Thunk: makeThunk(args, env), IsDelayForce: true})
}

// ---------------------------------------------------------------------------
// parameterize
// ---------------------------------------------------------------------------

func evalParameterize(m *Machine, form Value, env *Env) {
	expanded, err := parameterizeExpansion(formArgs(form))
	if err != nil {
		m.Raise(err)
		return
	}
	m.Eval(expanded, env)
}

// parameterizeExpansion builds the form parameterize means: the parameter and
// the value are evaluated once, the old value is read once, and a dynamic-wind
// sets the new value for the body and puts the old one back afterwards — as it
// was, without running it through the converter a second time, which would
// compound a non-idempotent one.  The compiler shares this, because every form
// in it compiles.
func parameterizeExpansion(args []Value) (Value, error) {
	if len(args) < 1 {
		return nil, NewError("parameterize: missing bindings")
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		return nil, NewError("parameterize: malformed bindings", args[0])
	}
	body := args[1:]
	// (let ((p pe) (v ve) ...) (let ((old (p))) (dynamic-wind (lambda () (p v) ...) (lambda () body) (lambda () (p old) ...))))
	var pSyms, vSyms []*Symbol
	var outerBindings []Value
	var inits []Value
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			return nil, NewError("parameterize: malformed binding", b)
		}
		ps := FreshSymbol("param")
		vs := FreshSymbol("val")
		pSyms = append(pSyms, ps)
		vSyms = append(vSyms, vs)
		outerBindings = append(outerBindings, List(ps, p.Car))
		outerBindings = append(outerBindings, List(vs, Cadr(p)))
		inits = append(inits, ps)
		inits = append(inits, vs)
	}
	_ = inits
	// old values
	var oldSyms []*Symbol
	var oldBindings []Value
	for _, ps := range pSyms {
		os := FreshSymbol("old")
		oldSyms = append(oldSyms, os)
		oldBindings = append(oldBindings, List(os, List(ps)))
	}
	var setNew, setOld []Value
	for i := range pSyms {
		setNew = append(setNew, List(pSyms[i], vSyms[i]))
		// Restoring puts the old value back as it was; running it through the
		// converter again would compound a non-idempotent one and corrupt the
		// outer binding.
		setOld = append(setOld, List(Intern("%parameter-set-raw!"), pSyms[i], oldSyms[i]))
	}
	beforeLam := Cons(Intern("lambda"), Cons(Nil, listFromSlice(setNew)))
	thunkLam := Cons(Intern("lambda"), Cons(Nil, listFromSlice(body)))
	afterLam := Cons(Intern("lambda"), Cons(Nil, listFromSlice(setOld)))
	dw := List(Intern("dynamic-wind"), beforeLam, thunkLam, afterLam)
	inner := List(Intern("let"), listFromSlice(oldBindings), dw)
	return List(Intern("let"), listFromSlice(outerBindings), inner), nil
}

// ---------------------------------------------------------------------------
// guard
// ---------------------------------------------------------------------------

func evalGuard(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) < 1 {
		m.Raise(NewError("guard: missing clauses", form))
		return
	}
	spec, ok := args[0].(*Pair)
	if !ok {
		m.Raise(NewError("guard: malformed clause list", args[0]))
		return
	}
	varSym, ok := spec.Car.(*Symbol)
	if !ok {
		m.Raise(NewError("guard: condition variable is not an identifier", spec.Car))
		return
	}
	clauses := mustSlice(spec.Cdr)
	body := args[1:]

	m.framesCopied = true
	guardStack := append([]frame(nil), m.stack...)
	guardWinds := append([]*windFrame(nil), m.winds...)
	guardHands := append([]*handlerFrame(nil), m.hands...)

	handler := &Primitive{Name: "guard", MinArgs: 1, MaxArgs: 1, Fn: func(m *Machine, hargs []Value) {
		cond := hargs[0]
		// Escaping from the guard's body must run the dynamic-wind after
		// thunks of every wind frame that is being left.
		target := m.captureContinuationFrom(guardStack, guardWinds, guardHands)
		m.transferToWith(target, func(m *Machine) {
			clauseEnv := NewEnv(env)
			clauseEnv.Define(varSym, cond)
			m.evalGuardClauses(clauses, clauseEnv, cond)
		})
	}}
	m.hands = append(m.hands, &handlerFrame{proc: handler})
	savedLen := len(m.hands)
	m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, v Value) {
		if len(m.hands) >= savedLen {
			m.hands = m.hands[:savedLen-1]
		}
		m.Return(v)
	}})
	m.EvalSeq(body, env)
}

func (m *Machine) evalGuardClauses(clauses []Value, env *Env, cond Value) {
	if len(clauses) == 0 {
		m.Raise(cond)
		return
	}
	cl := clauses[0]
	p, ok := cl.(*Pair)
	if !ok {
		m.Raise(NewError("guard: bad clause", cl))
		return
	}
	if s, ok := p.Car.(*Symbol); ok && s.Name == "else" && isAuxSyntax(s, env) {
		m.EvalSeq(mustSlice(p.Cdr), env)
		return
	}
	m.EvalWith(p.Car, env, func(m *Machine, v Value) {
		if IsTrue(v) {
			body := mustSlice(p.Cdr)
			if len(body) == 0 {
				m.Return(v)
				return
			}
			if s, ok := body[0].(*Symbol); ok && s.Name == "=>" && isAuxSyntax(s, env) {
				m.EvalWith(body[1], env, func(m *Machine, proc Value) {
					m.apply(proc, []Value{v})
				})
				return
			}
			m.EvalSeq(body, env)
			return
		}
		m.evalGuardClauses(clauses[1:], env, cond)
	})
}

// ---------------------------------------------------------------------------
// Records
// ---------------------------------------------------------------------------

func evalDefineRecordType(m *Machine, form Value, env *Env) {
	names, values, err := recordType(formArgs(form))
	if err != nil {
		m.RaiseError(err)
		return
	}
	for i, name := range names {
		env.Define(name, values[i])
	}
	m.Return(UnspecifiedValue)
}

// recordType builds what a define-record-type defines: the identifiers it binds
// and the values they take, in the same order — the type, the constructor, the
// predicate, then each field's accessor and modifier.  It defines nothing, so
// that the interpreter can define them in the environment it is evaluating in
// and a compiled body can store them in the slots it reserved, from one
// implementation of what the form means.
func recordType(args []Value) ([]*Symbol, []Value, error) {
	if len(args) < 3 {
		return nil, nil, NewError("define-record-type: malformed")
	}
	typeName, ok := args[0].(*Symbol)
	if !ok {
		return nil, nil, NewError("define-record-type: type name is not an identifier", args[0])
	}
	ctorSpec, ok := args[1].(*Pair)
	if !ok {
		return nil, nil, NewError("define-record-type: malformed constructor spec", args[1])
	}
	ctorName, ok := ctorSpec.Car.(*Symbol)
	if !ok {
		return nil, nil, NewError("define-record-type: constructor name is not an identifier", ctorSpec.Car)
	}
	ctorFields := mustSlice(ctorSpec.Cdr)
	for _, f := range ctorFields {
		if _, ok := f.(*Symbol); !ok {
			return nil, nil, NewError("define-record-type: constructor field is not an identifier", f)
		}
	}
	predName, ok := args[2].(*Symbol)
	if !ok {
		return nil, nil, NewError("define-record-type: predicate name is not an identifier", args[2])
	}
	rt := &RecordType{Name: typeName.Name}
	fieldIndex := map[string]int{}
	names := []*Symbol{typeName, ctorName, predName}
	var fields []Value
	for _, spec := range args[3:] {
		p, ok := spec.(*Pair)
		if !ok {
			return nil, nil, NewError("define-record-type: malformed field spec", spec)
		}
		fname, ok := p.Car.(*Symbol)
		if !ok {
			return nil, nil, NewError("define-record-type: field name is not an identifier", p.Car)
		}
		items := mustSlice(p.Cdr)
		if len(items) < 1 {
			return nil, nil, NewError("define-record-type: missing accessor", spec)
		}
		accName, ok := items[0].(*Symbol)
		if !ok {
			return nil, nil, NewError("define-record-type: accessor is not an identifier", items[0])
		}
		mutable := false
		var modName *Symbol
		if len(items) > 1 {
			modName, ok = items[1].(*Symbol)
			if !ok {
				return nil, nil, NewError("define-record-type: modifier is not an identifier", items[1])
			}
			mutable = true
		}
		fieldIndex[fname.Name] = len(rt.Fields)
		rt.Fields = append(rt.Fields, fname)
		rt.Mutable = append(rt.Mutable, mutable)
		idx := len(rt.Fields) - 1
		fields = append(fields, nil) // the type and procedures are built below
		names = append(names, accName)
		accessor := &Primitive{Name: accName.Name, MinArgs: 1, MaxArgs: 1,
			Fn: func(m *Machine, a []Value) {
				r, ok := a[0].(*Record)
				if !ok || r.Type != rt {
					m.Raise(WrongTypeName("record of type "+rt.Name, a[0], accName.Name))
					return
				}
				m.Return(r.Fields[idx])
			}}
		fields[len(fields)-1] = accessor
		if modName != nil {
			names = append(names, modName)
			fields = append(fields, &Primitive{Name: modName.Name, MinArgs: 2, MaxArgs: 2,
				Fn: func(m *Machine, a []Value) {
					r, ok := a[0].(*Record)
					if !ok || r.Type != rt {
						m.Raise(WrongTypeName("record of type "+rt.Name, a[0], modName.Name))
						return
					}
					r.Fields[idx] = a[1]
					m.Return(UnspecifiedValue)
				}})
		}
	}
	for _, f := range ctorFields {
		if _, ok := fieldIndex[f.(*Symbol).Name]; !ok {
			return nil, nil, NewError("define-record-type: constructor field is not a record field", f)
		}
	}
	ctorIdx := make([]int, len(ctorFields))
	for i, f := range ctorFields {
		ctorIdx[i] = fieldIndex[f.(*Symbol).Name]
	}
	values := []Value{
		&RecordTypeDescriptor{Type: rt},
		&Primitive{Name: ctorName.Name, MinArgs: len(ctorFields), MaxArgs: len(ctorFields),
			Fn: func(m *Machine, a []Value) {
				r := &Record{Type: rt, Fields: make([]Value, len(rt.Fields))}
				for i := range r.Fields {
					r.Fields[i] = UnspecifiedValue
				}
				for i, idx := range ctorIdx {
					r.Fields[idx] = a[i]
				}
				m.Return(r)
			}},
		&Primitive{Name: predName.Name, MinArgs: 1, MaxArgs: 1,
			Fn: func(m *Machine, a []Value) {
				r, ok := a[0].(*Record)
				m.Return(BooleanOf(ok && r.Type == rt))
			}},
	}
	values = append(values, fields...)
	return names, values, nil
}

// ---------------------------------------------------------------------------
// Macros
// ---------------------------------------------------------------------------

func evalDefineSyntax(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) != 2 {
		m.Raise(NewError("define-syntax: expected (define-syntax keyword transformer)", form))
		return
	}
	name, ok := args[0].(*Symbol)
	if !ok {
		m.Raise(NewError("define-syntax: keyword is not an identifier", args[0]))
		return
	}
	tf, ok := args[1].(*Pair)
	if !ok {
		m.Raise(NewError("define-syntax: unsupported transformer", args[1]))
		return
	}
	kw, _ := tf.Car.(*Symbol)
	if kw == nil || kw.Name != "syntax-rules" {
		m.Raise(NewError("define-syntax: only syntax-rules transformers are supported", args[1]))
		return
	}
	mac, err := ParseSyntaxRules(name.Name, tf, env)
	if err != nil {
		m.RaiseError(err)
		return
	}
	env.Define(name, mac)
	m.Return(UnspecifiedValue)
}

func evalLetSyntax(m *Machine, form Value, env *Env) { evalLetSyntaxCommon(m, form, env, false) }
func evalLetrecSyntax(m *Machine, form Value, env *Env) {
	evalLetSyntaxCommon(m, form, env, true)
}

func evalLetSyntaxCommon(m *Machine, form Value, env *Env, rec bool) {
	args := formArgs(form)
	if len(args) < 1 {
		m.Raise(NewError("let-syntax: malformed", form))
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		m.Raise(NewError("let-syntax: malformed bindings", args[0]))
		return
	}
	newEnv := NewEnv(env)
	defEnv := env
	if rec {
		defEnv = newEnv
	}
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			m.Raise(NewError("let-syntax: malformed binding", b))
			return
		}
		name, ok := p.Car.(*Symbol)
		if !ok {
			m.Raise(NewError("let-syntax: keyword is not an identifier", p.Car))
			return
		}
		tf, ok := Cadr(p).(*Pair)
		if !ok {
			m.Raise(NewError("let-syntax: unsupported transformer", Cadr(p)))
			return
		}
		kw, _ := tf.Car.(*Symbol)
		if kw == nil || kw.Name != "syntax-rules" {
			m.Raise(NewError("let-syntax: only syntax-rules transformers are supported", tf))
			return
		}
		mac, err := ParseSyntaxRules(name.Name, tf, defEnv)
		if err != nil {
			m.RaiseError(err)
			return
		}
		newEnv.Define(name, mac)
	}
	m.EvalSeq(args[1:], newEnv)
}

// ---------------------------------------------------------------------------
// include / cond-expand
// ---------------------------------------------------------------------------

func evalInclude(m *Machine, form Value, env *Env)   { evalIncludeCommon(m, form, env, false) }
func evalIncludeCI(m *Machine, form Value, env *Env) { evalIncludeCommon(m, form, env, true) }

func evalIncludeCommon(m *Machine, form Value, env *Env, fold bool) {
	args := formArgs(form)
	var forms []Value
	var dir string
	for _, a := range args {
		s, ok := a.(*String)
		if !ok {
			m.Raise(NewError("include: file name must be a string", a))
			return
		}
		path := m.resolvePath(s.Value())
		dir = dirOf(path)
		fs, err := ReadFileForms(m, path, fold)
		if err != nil {
			m.RaiseError(err)
			return
		}
		forms = append(forms, fs...)
	}
	if dir != "" {
		m.AddLoadPath(dir)
		m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, v Value) {
			m.PopLoadPath()
			m.Return(v)
		}})
	}
	m.EvalSeq(forms, env)
}

// dirOf is the directory a file lives in.  It goes through filepath because
// the separators are the host's: a hand-rolled scan for '/' silently returned
// "." on Windows, which made include and library loading look in the current
// directory instead of next to the file, and only running the suite there
// showed it.
func dirOf(p string) string {
	return filepath.Dir(p)
}

func evalAssert(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) != 1 {
		m.Raise(NewError("assert: expected one expression"))
		return
	}
	m.EvalWith(args[0], env, func(m *Machine, v Value) {
		if IsFalse(v) {
			m.Raise(NewError("assertion failed", args[0]))
			return
		}
		m.Return(v)
	})
}

// ---------------------------------------------------------------------------
// cond-expand
// ---------------------------------------------------------------------------

func evalCondExpand(m *Machine, form Value, env *Env) {
	clauses := formArgs(form)
	for _, cl := range clauses {
		p, ok := cl.(*Pair)
		if !ok {
			continue
		}
		if s, ok := p.Car.(*Symbol); ok && s.Name == "else" {
			m.EvalSeq(mustSlice(p.Cdr), env)
			return
		}
		if FeatureMatch(m, p.Car) {
			m.EvalSeq(mustSlice(p.Cdr), env)
			return
		}
	}
	m.Return(UnspecifiedValue)
}

// FeatureMatch evaluates a cond-expand feature requirement.
func FeatureMatch(m *Machine, req Value) bool {
	switch r := req.(type) {
	case *Symbol:
		if r.Name == "else" {
			return true
		}
		for _, f := range m.Features() {
			if f == r.Name {
				return true
			}
		}
		return false
	case *Pair:
		head, _ := r.Car.(*Symbol)
		if head == nil {
			return false
		}
		items := mustSlice(r.Cdr)
		switch head.Name {
		case "and":
			for _, it := range items {
				if !FeatureMatch(m, it) {
					return false
				}
			}
			return true
		case "or":
			for _, it := range items {
				if FeatureMatch(m, it) {
					return true
				}
			}
			return false
		case "not":
			if len(items) != 1 {
				return false
			}
			return !FeatureMatch(m, items[0])
		case "library":
			if len(items) != 1 {
				return false
			}
			return m.libraryAvailable(items[0])
		}
	}
	return false
}

// Features returns the list of features supported by this implementation.
func (m *Machine) Features() []string {
	feats := []string{
		"r7rs", "exact-closed", "exact-complex", "ratios", "ieee-float",
		"full-unicode", "goscheme",
	}
	if ffiAvailable {
		feats = append(feats, "ffi")
	}
	feats = append(feats, platformFeatures()...)
	return feats
}
