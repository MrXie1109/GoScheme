// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// ---------------------------------------------------------------------------
// Quoting
// ---------------------------------------------------------------------------

// SyntaxNames lists the syntactic keywords the interpreter recognises, for a
// caller that wants to show them differently from procedures — the REPL colours
// them.  It is a function rather than the map itself so that nothing outside
// can add to or remove from the table the evaluator dispatches on.
func SyntaxNames() []string {
	names := make([]string, 0, len(specialForms))
	for name := range specialForms {
		names = append(names, name)
	}
	return names
}

func evalQuote(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) != 1 {
		m.Raise(NewError("quote: expected one datum"))
		return
	}
	m.Return(args[0])
}

// EvalQuasiquote expands `form` into calls to cons/append/list->vector.  The
// constructors are inserted as procedure objects, so the expansion cannot be
// captured by user bindings.
func EvalQuasiquote(form Value) Value {
	return expandQQ(form, 1)
}

func qqList(elems ...Value) Value { return List(elems...) }

func expandQQ(tmpl Value, depth int) Value {
	switch t := tmpl.(type) {
	case *Pair:
		if s, ok := t.Car.(*Symbol); ok && !s.IsMarked() {
			switch s.Name {
			case "unquote":
				if depth == 1 {
					return Cadr(t)
				}
				return qqList(Intern("cons"), qqQuote(Intern("unquote")),
					qqList(Intern("cons"), expandQQ(Cadr(t), depth-1), qqQuote(Nil)))
			case "quasiquote":
				return qqList(Intern("cons"), qqQuote(Intern("quasiquote")),
					qqList(Intern("cons"), expandQQ(Cadr(t), depth+1), qqQuote(Nil)))
			case "unquote-splicing":
				if depth == 1 {
					return qqList(Intern("append"), Cadr(t), qqQuote(Nil))
				}
				return qqList(Intern("cons"), qqQuote(Intern("unquote-splicing")),
					qqList(Intern("cons"), expandQQ(Cadr(t), depth-1), qqQuote(Nil)))
			}
		}
		// Check for splicing in the car.
		if inner, ok := t.Car.(*Pair); ok {
			if s, ok := inner.Car.(*Symbol); ok && s.Name == "unquote-splicing" && !s.IsMarked() {
				if depth == 1 {
					return qqList(Intern("append"), Cadr(inner), expandQQ(t.Cdr, depth))
				}
			}
		}
		return qqList(Intern("cons"), expandQQ(t.Car, depth), expandQQ(t.Cdr, depth))
	case *Vector:
		return qqList(Intern("list->vector"), expandQQ(listFromSlice(t.Items), depth))
	default:
		return qqQuote(tmpl)
	}
}

func qqQuote(v Value) Value { return List(Intern("quote"), v) }

func evalQuasiquote(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) != 1 {
		m.Raise(NewError("quasiquote: expected one template"))
		return
	}
	m.Eval(EvalQuasiquote(args[0]), env)
}

// ---------------------------------------------------------------------------
// Conditionals and sequencing
// ---------------------------------------------------------------------------

func evalIf(m *Machine, form Value, env *Env) {
	// if is the most frequently evaluated special form, so its three parts are
	// taken straight out of the form: building an argument slice for it was
	// one of the largest remaining allocations.
	p, ok := form.(*Pair)
	if !ok {
		m.Raise(NewError("if: malformed", form))
		return
	}
	test, ok := p.Cdr.(*Pair)
	if !ok {
		m.Raise(NewError("if: expected 2 or 3 subforms", form))
		return
	}
	conseq, ok := test.Cdr.(*Pair)
	if !ok {
		m.Raise(NewError("if: expected 2 or 3 subforms", form))
		return
	}
	alt := Value(UnspecifiedValue)
	switch rest := conseq.Cdr.(type) {
	case Empty:
	case *Pair:
		if _, extra := rest.Cdr.(Empty); !extra {
			m.Raise(NewError("if: expected 2 or 3 subforms", form))
			return
		}
		alt = rest.Car
	default:
		m.Raise(NewError("if: malformed", form))
		return
	}
	m.EvalWith(test.Car, env, func(m *Machine, v Value) {
		if IsTrue(v) {
			m.Eval(conseq.Car, env)
		} else {
			m.Eval(alt, env)
		}
	})
}

func evalBegin(m *Machine, form Value, env *Env) {
	m.EvalSeq(formArgs(form), env)
}

// ---------------------------------------------------------------------------
// Binding forms
// ---------------------------------------------------------------------------

// evalBindings evaluates a list of (variable init) pairs, then calls fn.
func evalBindings(m *Machine, bindings Value, env *Env, fn func(m *Machine, syms []*Symbol, vals []Value)) {
	items, ok := ListToSlice(bindings)
	if !ok {
		m.Raise(NewError("malformed binding list", bindings))
		return
	}
	var syms []*Symbol
	var inits []Value
	for _, b := range items {
		var name Value
		var init Value = UnspecifiedValue
		switch x := b.(type) {
		case *Symbol:
			name, init = x, UnspecifiedValue
		case *Pair:
			name = x.Car
			if _, isNil := x.Cdr.(Empty); !isNil {
				init = Cadr(x)
			}
		default:
			m.Raise(NewError("malformed binding", b))
			return
		}
		s, ok := name.(*Symbol)
		if !ok {
			m.Raise(NewError("binding name is not an identifier", name))
			return
		}
		syms = append(syms, s)
		inits = append(inits, init)
	}
	if err := checkDuplicateVars(syms); err != nil {
		m.RaiseError(err)
		return
	}
	m.EvalList(inits, env, func(m *Machine, vals []Value) {
		fn(m, syms, vals)
	})
}

// EvalList evaluates exprs left to right and calls fn with the values.
func (m *Machine) EvalList(exprs []Value, env *Env, fn func(*Machine, []Value)) {
	vals := make([]Value, 0, len(exprs))
	var step func(i int)
	step = func(i int) {
		if i >= len(exprs) {
			fn(m, vals)
			return
		}
		j := i
		m.EvalWith(exprs[j], env, func(m *Machine, v Value) {
			vals = append(vals, v)
			step(j + 1)
		})
	}
	step(0)
}

func evalLet(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) == 0 {
		m.Raise(NewError("let: missing bindings", form))
		return
	}
	// Named let.
	if name, ok := args[0].(*Symbol); ok {
		if len(args) < 2 {
			m.Raise(NewError("let: missing bindings", form))
			return
		}
		bindings, ok := ListToSlice(args[1])
		if !ok {
			m.Raise(NewError("let: malformed bindings", args[1]))
			return
		}
		var vars, inits []Value
		for _, b := range bindings {
			p, ok := b.(*Pair)
			if !ok {
				m.Raise(NewError("let: malformed binding", b))
				return
			}
			vars = append(vars, p.Car)
			if _, isNil := p.Cdr.(Empty); isNil {
				inits = append(inits, UnspecifiedValue)
			} else {
				inits = append(inits, Cadr(p))
			}
		}
		lam := Cons(Intern("lambda"), Cons(listFromSlice(vars), listFromSlice(args[2:])))
		binding := List(name, lam)
		body := Cons(Intern("letrec"), Cons(List(binding), List(name)))
		call := Cons(body, listFromSlice(inits))
		m.Eval(call, env)
		return
	}
	bindings := args[0]
	body := args[1:]
	if _, isNil := bindings.(Empty); isNil {
		newEnv := NewEnv(env)
		prepBody(newEnv, body)
		m.EvalSeq(body, newEnv)
		return
	}
	evalBindings(m, bindings, env, func(m *Machine, syms []*Symbol, vals []Value) {
		newEnv := NewEnv(env)
		for i, s := range syms {
			newEnv.Define(s, vals[i])
		}
		prepBody(newEnv, body)
		m.EvalSeq(body, newEnv)
	})
}

func evalLetStar(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) == 0 {
		m.Raise(NewError("let*: missing bindings", form))
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		m.Raise(NewError("let*: malformed bindings", args[0]))
		return
	}
	body := args[1:]
	if len(bindings) == 0 {
		newEnv := NewEnv(env)
		prepBody(newEnv, body)
		m.EvalSeq(body, newEnv)
		return
	}
	rest := appendToTail(listFromSlice(bindings[1:]), Nil)
	inner := Cons(Intern("let*"), Cons(rest, listFromSlice(body)))
	outer := List(Intern("let"), List(bindings[0]), inner)
	m.Eval(outer, env)
}

func evalLetrec(m *Machine, form Value, env *Env) { evalLetrecCommon(m, form, env, false) }
func evalLetrecStar(m *Machine, form Value, env *Env) {
	evalLetrecCommon(m, form, env, true)
}

func evalLetrecCommon(m *Machine, form Value, env *Env, sequential bool) {
	args := formArgs(form)
	if len(args) == 0 {
		m.Raise(NewError("letrec: missing bindings", form))
		return
	}
	items, ok := ListToSlice(args[0])
	if !ok {
		m.Raise(NewError("letrec: malformed bindings", args[0]))
		return
	}
	var syms []*Symbol
	var inits []Value
	for _, b := range items {
		p, ok := b.(*Pair)
		if !ok {
			m.Raise(NewError("letrec: malformed binding", b))
			return
		}
		s, ok := p.Car.(*Symbol)
		if !ok {
			m.Raise(NewError("letrec: binding name is not an identifier", p.Car))
			return
		}
		syms = append(syms, s)
		if _, isNil := p.Cdr.(Empty); isNil {
			inits = append(inits, UnspecifiedValue)
		} else {
			inits = append(inits, Cadr(p))
		}
	}
	if err := checkDuplicateVars(syms); err != nil {
		m.RaiseError(err)
		return
	}
	newEnv := NewEnv(env)
	for _, s := range syms {
		newEnv.Define(s, Unassigned)
	}
	body := args[1:]
	vals := make([]Value, len(inits))
	i := 0
	var step func()
	step = func() {
		if i >= len(inits) {
			if !sequential {
				for j, s := range syms {
					newEnv.Define(s, vals[j])
				}
			}
			prepBody(newEnv, body)
			m.EvalSeq(body, newEnv)
			return
		}
		j := i
		i++
		m.EvalWith(inits[j], newEnv, func(m *Machine, v Value) {
			if c, ok := v.(*Closure); ok && c.Name == "" {
				c.Name = syms[j].Name
			}
			if sequential {
				newEnv.Define(syms[j], v)
			} else {
				vals[j] = v
			}
			step()
		})
	}
	step()
}

func evalLetValues(m *Machine, form Value, env *Env) {
	evalLetValuesForm(m, form, env, "let-values")
}
func evalLetStarValues(m *Machine, form Value, env *Env) {
	evalLetValuesForm(m, form, env, "let*-values")
}

func evalLetValuesForm(m *Machine, form Value, env *Env, name string) {
	expanded, err := letValuesForm(name, formArgs(form))
	if err != nil {
		m.Raise(err)
		return
	}
	m.Eval(expanded, env)
}

// letValuesForm builds the call-with-values form let-values and let*-values
// mean — one definition, shared with the compiler, so the two execution paths
// cannot drift apart.
//
// The difference between the two is what a producer may see.  In let*-values
// each producer sees the bindings before it, so the calls nest: the consumer of
// one binding encloses the next producer.  In let-values no producer sees any
// binding, so the values are collected into fresh temporaries first and the
// names are bound at the end — the reference expansion, and the reason the
// temporaries exist at all.
func letValuesForm(name string, args []Value) (Value, error) {
	if len(args) == 0 {
		return nil, NewError(name + ": missing bindings")
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		return nil, NewError(name+": malformed bindings", args[0])
	}
	body := args[1:]
	if name == "let*-values" {
		// No bindings still means a body of its own: a definition in it must
		// not reach the enclosing scope, which is what the report's test for
		// this case checks.
		var expr Value = Cons(Intern("let"), Cons(Nil, listFromSlice(body)))
		for i := len(bindings) - 1; i >= 0; i-- {
			p, ok := bindings[i].(*Pair)
			if !ok {
				return nil, NewError("let*-values: malformed binding", bindings[i])
			}
			producer := Value(UnspecifiedValue)
			if _, isNil := p.Cdr.(Empty); !isNil {
				producer = Cadr(p)
			}
			consumer := List(Intern("lambda"), p.Car, expr)
			expr = List(bindValues,
				List(Intern("lambda"), Nil, producer), consumer)
		}
		return expr, nil
	}
	var pairs, temps, producers []Value
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			return nil, NewError("let-values: malformed binding", b)
		}
		producer := Value(UnspecifiedValue)
		if _, isNil := p.Cdr.(Empty); !isNil {
			producer = Cadr(p)
		}
		fresh, err := freshFormals(p.Car)
		if err != nil {
			return nil, err
		}
		bound, err := zipFormals(p.Car, fresh)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, bound...)
		temps = append(temps, fresh)
		producers = append(producers, producer)
	}
	var expr Value = Cons(Intern("let"), Cons(listFromSlice(pairs), listFromSlice(body)))
	for i := len(bindings) - 1; i >= 0; i-- {
		consumer := List(Intern("lambda"), temps[i], expr)
		expr = List(bindValues,
			List(Intern("lambda"), Nil, producers[i]), consumer)
	}
	return expr, nil
}

// freshFormals renames every variable of a formals list, keeping its shape, so
// that a producer cannot accidentally see a name the bindings introduce.
func freshFormals(formals Value) (Value, error) {
	switch f := formals.(type) {
	case *Symbol:
		return FreshSymbol(f.Name), nil
	case Empty:
		return Nil, nil
	case *Pair:
		s, ok := f.Car.(*Symbol)
		if !ok {
			return nil, NewError("binding name is not an identifier", f.Car)
		}
		rest, err := freshFormals(f.Cdr)
		if err != nil {
			return nil, err
		}
		return Cons(FreshSymbol(s.Name), rest), nil
	default:
		return nil, NewError("malformed formals", formals)
	}
}

// zipFormals pairs each variable of a formals list with its fresh counterpart:
// ((a t1) (b t2) ...) for the let that binds the real names at the end.
func zipFormals(orig, fresh Value) ([]Value, error) {
	switch o := orig.(type) {
	case *Symbol:
		return []Value{List(o, fresh)}, nil
	case Empty:
		return nil, nil
	case *Pair:
		s, ok := o.Car.(*Symbol)
		if !ok {
			return nil, NewError("binding name is not an identifier", o.Car)
		}
		fp, ok := fresh.(*Pair)
		if !ok {
			return nil, NewError("malformed formals", orig)
		}
		rest, err := zipFormals(o.Cdr, fp.Cdr)
		if err != nil {
			return nil, err
		}
		return append([]Value{List(s, fp.Car)}, rest...), nil
	default:
		return nil, NewError("malformed formals", orig)
	}
}

// bindFormals binds the values a producer returned to the formals of a
// define-values or let-values binding: a proper list, a dotted one, or a single
// name taking every value as a list.
func bindFormals(env *Env, formals Value, vals []Value) error {
	switch f := formals.(type) {
	case *Symbol:
		env.Define(f, List(vals...))
		return nil
	case Empty:
		return nil
	case *Pair:
		cur := Value(f)
		i := 0
		for {
			p, ok := cur.(*Pair)
			if !ok {
				break
			}
			s, ok := p.Car.(*Symbol)
			if !ok {
				return NewError("binding name is not an identifier", p.Car)
			}
			if i >= len(vals) {
				return NewError("too few values for the formals", formals)
			}
			env.Define(s, vals[i])
			i++
			cur = p.Cdr
		}
		if s, ok := cur.(*Symbol); ok {
			env.Define(s, List(vals[i:]...))
			return nil
		} else if _, isNil := cur.(Empty); !isNil {
			return NewError("malformed formals", formals)
		}
		if i != len(vals) {
			return NewError("too many values for the formals", formals)
		}
		return nil
	}
	return NewError("malformed formals", formals)
}

func evalDefineValues(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) != 2 {
		m.Raise(NewError("define-values: expected (define-values formals expr)", form))
		return
	}
	formals := args[0]
	m.EvalWithMulti(args[1], env, func(m *Machine, vs []Value) {
		if err := bindFormals(env, formals, vs); err != nil {
			m.RaiseError(err)
			return
		}
		m.Return(UnspecifiedValue)
	})
}

// valueList converts a possibly multiple value into a slice.
func valueList(v Value) []Value {
	if mv, ok := v.(*MultipleValues); ok {
		return mv.Values
	}
	return []Value{v}
}

// single unwraps multiple values where one value is expected.
func single(v Value) Value {
	if mv, ok := v.(*MultipleValues); ok {
		if len(mv.Values) == 0 {
			return UnspecifiedValue
		}
		return mv.Values[0]
	}
	return v
}

// ---------------------------------------------------------------------------
// cond / case / and / or / when / unless / do
// ---------------------------------------------------------------------------

func evalCond(m *Machine, form Value, env *Env) {
	m.evalCondClauses(formArgs(form), env)
}

func (m *Machine) evalCondClauses(clauses []Value, env *Env) {
	if len(clauses) == 0 {
		m.Return(UnspecifiedValue)
		return
	}
	cl := clauses[0]
	p, ok := cl.(*Pair)
	if !ok {
		m.Raise(NewError("cond: bad clause", cl))
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
				if len(body) != 2 {
					m.Raise(NewError("cond: bad => clause", cl))
					return
				}
				m.EvalWith(body[1], env, func(m *Machine, proc Value) {
					m.apply(proc, []Value{v})
				})
				return
			}
			m.EvalSeq(body, env)
			return
		}
		m.evalCondClauses(clauses[1:], env)
	})
}

func evalCase(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) < 1 {
		m.Raise(NewError("case: missing key", form))
		return
	}
	clauses := args[1:]
	m.EvalWith(args[0], env, func(m *Machine, key Value) {
		m.evalCaseClauses(clauses, key, env)
	})
}

func (m *Machine) evalCaseClauses(clauses []Value, key Value, env *Env) {
	if len(clauses) == 0 {
		m.Return(UnspecifiedValue)
		return
	}
	cl := clauses[0]
	p, ok := cl.(*Pair)
	if !ok {
		m.Raise(NewError("case: bad clause", cl))
		return
	}
	body := mustSlice(p.Cdr)
	if s, ok := p.Car.(*Symbol); ok && s.Name == "else" && isAuxSyntax(s, env) {
		m.evalCaseBody(body, key, env)
		return
	}
	datums, ok := ListToSlice(p.Car)
	if !ok {
		m.Raise(NewError("case: datum list is not a list", p.Car))
		return
	}
	for _, d := range datums {
		if Eqv(key, d) {
			m.evalCaseBody(body, key, env)
			return
		}
	}
	m.evalCaseClauses(clauses[1:], key, env)
}

// evalCaseBody evaluates the body of a case clause, handling the (=> proc)
// form which receives the key.
func (m *Machine) evalCaseBody(body []Value, key Value, env *Env) {
	if len(body) == 2 {
		if s, ok := body[0].(*Symbol); ok && s.Name == "=>" && isAuxSyntax(s, env) {
			m.EvalWith(body[1], env, func(m *Machine, proc Value) {
				m.apply(proc, []Value{key})
			})
			return
		}
	}
	m.EvalSeq(body, env)
}

func evalAnd(m *Machine, form Value, env *Env) {
	m.evalAnd(formArgs(form), env)
}

func (m *Machine) evalAnd(exprs []Value, env *Env) {
	switch len(exprs) {
	case 0:
		m.Return(True)
	case 1:
		m.Eval(exprs[0], env)
	default:
		m.EvalWith(exprs[0], env, func(m *Machine, v Value) {
			if IsFalse(v) {
				m.Return(False)
				return
			}
			m.evalAnd(exprs[1:], env)
		})
	}
}

func evalOr(m *Machine, form Value, env *Env) {
	m.evalOr(formArgs(form), env)
}

func (m *Machine) evalOr(exprs []Value, env *Env) {
	switch len(exprs) {
	case 0:
		m.Return(False)
	case 1:
		m.Eval(exprs[0], env)
	default:
		m.EvalWith(exprs[0], env, func(m *Machine, v Value) {
			if IsTrue(v) {
				m.Return(v)
				return
			}
			m.evalOr(exprs[1:], env)
		})
	}
}

func evalWhen(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) < 1 {
		m.Raise(NewError("when: missing test", form))
		return
	}
	body := args[1:]
	m.EvalWith(args[0], env, func(m *Machine, v Value) {
		if IsTrue(v) {
			m.EvalSeq(body, env)
			return
		}
		m.Return(UnspecifiedValue)
	})
}

func evalUnless(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) < 1 {
		m.Raise(NewError("unless: missing test", form))
		return
	}
	body := args[1:]
	m.EvalWith(args[0], env, func(m *Machine, v Value) {
		if IsFalse(v) {
			m.EvalSeq(body, env)
			return
		}
		m.Return(UnspecifiedValue)
	})
}

func evalDo(m *Machine, form Value, env *Env) {
	expanded, err := doExpansion(formArgs(form), func(cond *Symbol) Value {
		// The interpreter compares the condition with the token itself, using
		// the eq? primitive rather than the name, so that a body which rebinds
		// eq? cannot break the loop.
		return List(builtinProc(m, "eq?"), cond, continueToken)
	})
	if err != nil {
		m.Raise(err)
		return
	}
	m.Eval(expanded, env)
}

// doExpansion builds the form a do loop means:
//
//	(letrec ((loop (lambda (var ...)
//	                 (if test
//	                     (begin result ...)
//	                     (begin (guard (e (test-of e)) commands ...)
//	                            (loop step ...))))))
//	  (loop init ...))
//
// The guard is what gives (continue) its meaning: it recognises the private
// condition and falls through to the step expressions, and re-raises anything
// else.  testOf builds that recognition, because the two callers can say it
// differently: the interpreter has the token in hand, while a compiled loop
// needs a helper that knows it, since the token cannot travel in a constant
// pool.
func doExpansion(args []Value, testOf func(cond *Symbol) Value) (Value, error) {
	if len(args) < 2 {
		return nil, NewError("do: malformed")
	}
	specs, ok := ListToSlice(args[0])
	if !ok {
		return nil, NewError("do: malformed variable list", args[0])
	}
	testClause, ok := args[1].(*Pair)
	if !ok {
		return nil, NewError("do: malformed test clause", args[1])
	}
	commands := args[2:]
	loopName := FreshSymbol("do-loop")
	var vars, inits, steps []Value
	for _, s := range specs {
		p, ok := s.(*Pair)
		if !ok {
			return nil, NewError("do: malformed variable spec", s)
		}
		items, _ := ListToSlice(p)
		if len(items) < 1 {
			return nil, NewError("do: malformed variable spec", s)
		}
		vars = append(vars, items[0])
		if len(items) > 1 {
			inits = append(inits, items[1])
		} else {
			inits = append(inits, UnspecifiedValue)
		}
		if len(items) > 2 {
			steps = append(steps, items[2])
		} else {
			steps = append(steps, items[0])
		}
	}
	result := testClause.Cdr
	if _, isNil := result.(Empty); isNil {
		result = List(UnspecifiedValue)
	}
	// (if test (begin result...) (begin commands... (loop step...)))
	recur := Cons(loopName, listFromSlice(steps))
	var tail []Value
	if len(commands) > 0 {
		cond := FreshSymbol("e")
		clause := List(testOf(cond)) // ((<the recognition> e))
		spec := List(cond, clause)   // (e ((<the recognition> e)))
		tail = append(tail, Cons(Intern("guard"),
			Cons(spec, listFromSlice(commands))))
	}
	tail = append(tail, recur)
	ifExpr := List(Intern("if"), testClause.Car,
		Cons(Intern("begin"), result),
		Cons(Intern("begin"), listFromSlice(tail)))
	lam := Cons(Intern("lambda"), Cons(listFromSlice(vars), List(ifExpr)))
	binding := List(loopName, lam)
	call := Cons(loopName, listFromSlice(inits))
	return List(Intern("letrec"), List(binding), call), nil
}
