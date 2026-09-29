// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"strings"
)

// specialForms maps the core syntactic keywords to their evaluators.
var specialForms map[string]func(m *Machine, form Value, env *Env)

func init() {
	specialForms = map[string]func(m *Machine, form Value, env *Env){
		"quote":              evalQuote,
		"quasiquote":         evalQuasiquote,
		"unquote":            evalBadAux,
		"unquote-splicing":   evalBadAux,
		"if":                 evalIf,
		"define":             evalDefine,
		"set!":               evalSet,
		"lambda":             evalLambda,
		"case-lambda":        evalCaseLambda,
		"begin":              evalBegin,
		"let":                evalLet,
		"let*":               evalLetStar,
		"letrec":             evalLetrec,
		"letrec*":            evalLetrecStar,
		"let-values":         evalLetValues,
		"let*-values":        evalLetStarValues,
		"define-values":      evalDefineValues,
		"cond":               evalCond,
		"case":               evalCase,
		"and":                evalAnd,
		"or":                 evalOr,
		"when":               evalWhen,
		"unless":             evalUnless,
		"do":                 evalDo,
		"delay":              evalDelay,
		"delay-force":        evalDelayForce,
		"parameterize":       evalParameterize,
		"guard":              evalGuard,
		"define-record-type": evalDefineRecordType,
		"define-syntax":      evalDefineSyntax,
		"let-syntax":         evalLetSyntax,
		"letrec-syntax":      evalLetrecSyntax,
		"syntax-rules":       evalBadAux,
		"include":            evalInclude,
		"include-ci":         evalIncludeCI,
		"cond-expand":        evalCondExpand,
		"import":             evalImport,
		"define-library":     evalDefineLibrary,
		"else":               evalBadAux,
		"=>":                 evalBadAux,
		"...":                evalBadAux,
		"_":                  evalBadAux,
		"assert":             evalAssert,
		"go":                 evalGo,
		"select":             evalSelect,
	}
}

// evalStep performs one step of the abstract machine.
func (m *Machine) evalStep() {
	expr := m.evalExpr
	env := m.env
	m.evalExpr = nil
	switch x := expr.(type) {
	case *Symbol:
		v, ok := env.Lookup(x)
		if !ok {
			m.Raise(NewError("unbound variable", x))
			return
		}
		if _, un := v.(unassigned); un {
			m.Raise(NewError("variable used before initialization", x))
			return
		}
		m.Return(v)
	case *Pair:
		if s, ok := x.Car.(*Symbol); ok {
			if v, bound := env.Lookup(s); bound {
				if mac, isMac := v.(*Macro); isMac {
					expanded, err := mac.Expand(expr)
					if err != nil {
						m.RaiseError(err)
						return
					}
					m.Eval(expanded, env)
					return
				}
				if kw, isKW := v.(*SyntaxKeyword); isKW {
					if fn, isSpecial := specialForms[kw.Name]; isSpecial {
						fn(m, expr, env)
						return
					}
				}
				// A variable binding shadows any syntactic keyword.
			} else if fn, isSpecial := specialForms[s.Name]; isSpecial {
				fn(m, expr, env)
				return
			}
		}
		args, _ := ListToSlice(x.Cdr)
		m.stack = append(m.stack, &fAppOp{args: args, env: env})
		m.Eval(x.Car, env)
	default:
		m.Return(expr)
	}
}

// isAuxSyntax reports whether the auxiliary keyword s (else, =>) is in scope
// as syntax rather than shadowed by a variable binding.
func isAuxSyntax(s *Symbol, env *Env) bool {
	if s.IsMarked() {
		return false
	}
	v, ok := env.Lookup(s)
	if !ok {
		return true
	}
	_, isKW := v.(*SyntaxKeyword)
	return isKW
}

func formArgs(form Value) []Value {
	items, _ := ListToSlice(cdr(form))
	return items
}

func evalBadAux(m *Machine, form Value, env *Env) {
	m.Raise(NewError("invalid use of auxiliary syntax", car(form)))
}

// ---------------------------------------------------------------------------
// Quoting
// ---------------------------------------------------------------------------

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
					return cadr(t)
				}
				return qqList(Intern("cons"), qqQuote(Intern("unquote")),
					qqList(Intern("cons"), expandQQ(cadr(t), depth-1), qqQuote(Nil)))
			case "quasiquote":
				return qqList(Intern("cons"), qqQuote(Intern("quasiquote")),
					qqList(Intern("cons"), expandQQ(cadr(t), depth+1), qqQuote(Nil)))
			case "unquote-splicing":
				if depth == 1 {
					return qqList(Intern("append"), cadr(t), qqQuote(Nil))
				}
				return qqList(Intern("cons"), qqQuote(Intern("unquote-splicing")),
					qqList(Intern("cons"), expandQQ(cadr(t), depth-1), qqQuote(Nil)))
			}
		}
		// Check for splicing in the car.
		if inner, ok := t.Car.(*Pair); ok {
			if s, ok := inner.Car.(*Symbol); ok && s.Name == "unquote-splicing" && !s.IsMarked() {
				if depth == 1 {
					return qqList(Intern("append"), cadr(inner), expandQQ(t.Cdr, depth))
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
	args := formArgs(form)
	if len(args) < 2 || len(args) > 3 {
		m.Raise(NewError("if: expected 2 or 3 subforms", form))
		return
	}
	var alt Value = UnspecifiedValue
	if len(args) == 3 {
		alt = args[2]
	}
	conseq := args[1]
	m.EvalWith(args[0], env, func(m *Machine, v Value) {
		if IsTrue(v) {
			m.Eval(conseq, env)
		} else {
			m.Eval(alt, env)
		}
	})
}

func evalBegin(m *Machine, form Value, env *Env) {
	m.EvalSeq(formArgs(form), env)
}

// ---------------------------------------------------------------------------
// Procedures
// ---------------------------------------------------------------------------

// parseFormals splits a lambda parameter list.
func parseFormals(formals Value) (params []*Symbol, rest *Symbol, hasRest bool, err error) {
	switch f := formals.(type) {
	case Empty:
		return nil, nil, false, nil
	case *Symbol:
		return nil, f, true, nil
	case *Pair:
		cur := Value(f)
		for {
			p, ok := cur.(*Pair)
			if !ok {
				break
			}
			s, ok := p.Car.(*Symbol)
			if !ok {
				return nil, nil, false, fmt.Errorf("lambda: parameter is not an identifier")
			}
			params = append(params, s)
			cur = p.Cdr
		}
		if _, isNil := cur.(Empty); isNil {
			return params, nil, false, nil
		}
		s, ok := cur.(*Symbol)
		if !ok {
			return nil, nil, false, fmt.Errorf("lambda: bad parameter list")
		}
		return params, s, true, nil
	}
	return nil, nil, false, fmt.Errorf("lambda: bad parameter list")
}

// scanBodyNames collects identifiers introduced by internal definitions so
// that they can be pre-bound (letrec* semantics).
func scanBodyNames(body []Value) []*Symbol {
	var out []*Symbol
	var scan func(forms []Value)
	scan = func(forms []Value) {
		for _, f := range forms {
			p, ok := f.(*Pair)
			if !ok {
				return
			}
			s, ok := p.Car.(*Symbol)
			if !ok {
				return
			}
			args, _ := ListToSlice(p.Cdr)
			switch s.Name {
			case "begin":
				scan(args)
			case "define":
				if len(args) == 0 {
					return
				}
				t := args[0]
				for {
					pp, ok := t.(*Pair)
					if !ok {
						break
					}
					t = pp.Car
				}
				if sym, ok := t.(*Symbol); ok {
					out = append(out, sym)
				}
				return
			case "define-values":
				if len(args) == 0 {
					return
				}
				names := args[0]
				for {
					switch n := names.(type) {
					case *Symbol:
						out = append(out, n)
						return
					case *Pair:
						if sym, ok := n.Car.(*Symbol); ok {
							out = append(out, sym)
						}
						names = n.Cdr
						continue
					}
					return
				}
			case "define-syntax", "define-record-type":
				if len(args) > 0 {
					if sym, ok := args[0].(*Symbol); ok {
						out = append(out, sym)
					}
				}
				return
			default:
				return
			}
		}
	}
	scan(body)
	return out
}

func makeClosure(formals Value, body []Value, env *Env) (*Closure, error) {
	params, rest, hasRest, err := parseFormals(formals)
	if err != nil {
		return nil, err
	}
	c := &Closure{Env: env}
	c.Clauses = []ClosureClause{{
		Params:    params,
		Rest:      rest,
		HasRest:   hasRest,
		Body:      body,
		BodyNames: scanBodyNames(body),
	}}
	return c, nil
}

func evalLambda(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) < 1 {
		m.Raise(NewError("lambda: missing parameter list"))
		return
	}
	c, err := makeClosure(args[0], args[1:], env)
	if err != nil {
		m.Raise(NewError(err.Error(), form))
		return
	}
	m.Return(c)
}

func evalCaseLambda(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	c := &Closure{Env: env}
	for _, cl := range args {
		p, ok := cl.(*Pair)
		if !ok {
			m.Raise(NewError("case-lambda: bad clause", cl))
			return
		}
		params, rest, hasRest, err := parseFormals(p.Car)
		if err != nil {
			m.Raise(NewError(err.Error(), cl))
			return
		}
		body, _ := ListToSlice(p.Cdr)
		c.Clauses = append(c.Clauses, ClosureClause{
			Params: params, Rest: rest, HasRest: hasRest, Body: body,
			BodyNames: scanBodyNames(body),
		})
	}
	m.Return(c)
}

func evalDefine(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) == 0 {
		m.Raise(NewError("define: missing name"))
		return
	}
	target := args[0]
	body := args[1:]
	// (define (f . args) body...) and curried variants
	if _, ok := target.(*Pair); ok {
		t := target
		for {
			pp, ok := t.(*Pair)
			if !ok {
				break
			}
			t = pp.Car
		}
		name, ok := t.(*Symbol)
		if !ok {
			m.Raise(NewError("define: bad procedure name", target))
			return
		}
		expr := Cons(Intern("lambda"), Cons(target.(*Pair).Cdr, listFromSlice(body)))
		m.EvalWith(expr, env, func(m *Machine, v Value) {
			if c, ok := v.(*Closure); ok && c.Name == "" {
				c.Name = name.Name
			}
			env.Define(name, v)
			m.Return(UnspecifiedValue)
		})
		return
	}
	name, ok := target.(*Symbol)
	if !ok {
		m.Raise(NewError("define: bad target", target))
		return
	}
	var valExpr Value = UnspecifiedValue
	if len(body) == 1 {
		valExpr = body[0]
	} else if len(body) > 1 {
		m.Raise(NewError("define: too many subforms", form))
		return
	}
	m.EvalWith(valExpr, env, func(m *Machine, v Value) {
		if c, ok := v.(*Closure); ok && c.Name == "" {
			c.Name = name.Name
		}
		env.Define(name, v)
		m.Return(UnspecifiedValue)
	})
}

func cdrOf(v Value) Value {
	if p, ok := v.(*Pair); ok {
		return p.Cdr
	}
	return Nil
}

func evalSet(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) != 2 {
		m.Raise(NewError("set!: expected (set! variable expression)", form))
		return
	}
	sym, ok := args[0].(*Symbol)
	if !ok {
		m.Raise(NewError("set!: target is not an identifier", args[0]))
		return
	}
	m.EvalWith(args[1], env, func(m *Machine, v Value) {
		if !env.Set(sym, v) {
			m.Raise(NewError("set!: unbound variable", sym))
			return
		}
		m.Return(UnspecifiedValue)
	})
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
				init = cadr(x)
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
				inits = append(inits, cadr(p))
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
		m.EvalSeq(body, newEnv)
		return
	}
	evalBindings(m, bindings, env, func(m *Machine, syms []*Symbol, vals []Value) {
		newEnv := NewEnv(env)
		for i, s := range syms {
			newEnv.Define(s, vals[i])
		}
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
		m.EvalSeq(body, NewEnv(env))
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
			inits = append(inits, cadr(p))
		}
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

func evalLetValues(m *Machine, form Value, env *Env) { evalLetValuesCommon(m, form, env, false) }
func evalLetStarValues(m *Machine, form Value, env *Env) {
	evalLetValuesCommon(m, form, env, true)
}

// let-values evaluates every producer in the outer environment and then binds
// all the formals; let*-values is expanded into nested call-with-values forms
// so that later producers see the earlier bindings.
func evalLetValuesCommon(m *Machine, form Value, env *Env, sequential bool) {
	args := formArgs(form)
	if len(args) == 0 {
		m.Raise(NewError("let-values: missing bindings", form))
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		m.Raise(NewError("let-values: malformed bindings", args[0]))
		return
	}
	if len(bindings) == 0 {
		m.EvalSeq(args[1:], NewEnv(env))
		return
	}
	if sequential {
		var buildStar func(i int) Value
		buildStar = func(i int) Value {
			if i >= len(bindings) {
				return Cons(Intern("begin"), listFromSlice(args[1:]))
			}
			b, ok := bindings[i].(*Pair)
			if !ok {
				m.Raise(NewError("let*-values: malformed binding", bindings[i]))
				return Nil
			}
			var producer Value = UnspecifiedValue
			if _, isNil := b.Cdr.(Empty); !isNil {
				producer = cadr(b)
			}
			consumer := Cons(Intern("lambda"), Cons(b.Car, List(buildStar(i+1))))
			return List(Intern("call-with-values"), List(Intern("lambda"), Nil, producer), consumer)
		}
		m.Eval(buildStar(0), env)
		return
	}
	var formalsList []Value
	var producers []Value
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			m.Raise(NewError("let-values: malformed binding", b))
			return
		}
		formalsList = append(formalsList, p.Car)
		if _, isNil := p.Cdr.(Empty); isNil {
			producers = append(producers, UnspecifiedValue)
		} else {
			producers = append(producers, cadr(p))
		}
	}
	results := make([][]Value, len(producers))
	i := 0
	var step func()
	step = func() {
		if i >= len(producers) {
			newEnv := NewEnv(env)
			for j, formals := range formalsList {
				if err := bindFormals(newEnv, formals, results[j]); err != nil {
					m.RaiseError(err)
					return
				}
			}
			m.EvalSeq(args[1:], newEnv)
			return
		}
		j := i
		i++
		m.EvalWithMulti(producers[j], env, func(m *Machine, vs []Value) {
			results[j] = vs
			step()
		})
	}
	step()
}

// bindFormals binds a lambda-style formal list to already evaluated values.
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
			if i < len(vals) {
				env.Define(s, vals[i])
			} else {
				env.Define(s, UnspecifiedValue)
			}
			i++
			cur = p.Cdr
		}
		if s, ok := cur.(*Symbol); ok {
			if i <= len(vals) {
				env.Define(s, List(vals[i:]...))
			} else {
				env.Define(s, Nil)
			}
		} else if _, isNil := cur.(Empty); !isNil {
			return NewError("malformed formals", formals)
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
	args := formArgs(form)
	if len(args) < 2 {
		m.Raise(NewError("do: malformed", form))
		return
	}
	specs, ok := ListToSlice(args[0])
	if !ok {
		m.Raise(NewError("do: malformed variable list", args[0]))
		return
	}
	testClause, ok := args[1].(*Pair)
	if !ok {
		m.Raise(NewError("do: malformed test clause", args[1]))
		return
	}
	commands := args[2:]
	loopName := FreshSymbol("do-loop")
	var vars, inits, steps []Value
	for _, s := range specs {
		p, ok := s.(*Pair)
		if !ok {
			m.Raise(NewError("do: malformed variable spec", s))
			return
		}
		items, _ := ListToSlice(p)
		if len(items) < 1 {
			m.Raise(NewError("do: malformed variable spec", s))
			return
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
	tail := append(append([]Value{}, commands...), recur)
	ifExpr := List(Intern("if"), testClause.Car,
		Cons(Intern("begin"), result),
		Cons(Intern("begin"), listFromSlice(tail)))
	lam := Cons(Intern("lambda"), Cons(listFromSlice(vars), List(ifExpr)))
	binding := List(loopName, lam)
	call := Cons(loopName, listFromSlice(inits))
	m.Eval(List(Intern("letrec"), List(binding), call), env)
}

// ---------------------------------------------------------------------------
// Lazy evaluation
// ---------------------------------------------------------------------------

func makeThunk(body []Value, env *Env) *Closure {
	return &Closure{Clauses: []ClosureClause{{Body: body, BodyNames: scanBodyNames(body)}}, Env: env}
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
	args := formArgs(form)
	if len(args) < 1 {
		m.Raise(NewError("parameterize: missing bindings", form))
		return
	}
	bindings, ok := ListToSlice(args[0])
	if !ok {
		m.Raise(NewError("parameterize: malformed bindings", args[0]))
		return
	}
	body := args[1:]
	// (let ((p pe) (v ve) ...) (let ((old (p))) (dynamic-wind (lambda () (p v) ...) (lambda () body) (lambda () (p old) ...))))
	var pSyms, vSyms []*Symbol
	var outerBindings []Value
	var inits []Value
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			m.Raise(NewError("parameterize: malformed binding", b))
			return
		}
		ps := FreshSymbol("param")
		vs := FreshSymbol("val")
		pSyms = append(pSyms, ps)
		vSyms = append(vSyms, vs)
		outerBindings = append(outerBindings, List(ps, p.Car))
		outerBindings = append(outerBindings, List(vs, cadr(p)))
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
		setOld = append(setOld, List(pSyms[i], oldSyms[i]))
	}
	beforeLam := Cons(Intern("lambda"), Cons(Nil, listFromSlice(setNew)))
	thunkLam := Cons(Intern("lambda"), Cons(Nil, listFromSlice(body)))
	afterLam := Cons(Intern("lambda"), Cons(Nil, listFromSlice(setOld)))
	dw := List(Intern("dynamic-wind"), beforeLam, thunkLam, afterLam)
	inner := List(Intern("let"), listFromSlice(oldBindings), dw)
	outer := List(Intern("let"), listFromSlice(outerBindings), inner)
	m.Eval(outer, env)
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

	guardStack := append([]frame(nil), m.stack...)
	guardWinds := append([]*windFrame(nil), m.winds...)
	guardHands := append([]*handlerFrame(nil), m.hands...)

	handler := &Primitive{Name: "guard", MinArgs: 1, MaxArgs: 1, Fn: func(m *Machine, hargs []Value) {
		cond := hargs[0]
		// Escaping from the guard's body must run the dynamic-wind after
		// thunks of every wind frame that is being left.
		target := &Continuation{
			stack: append([]frame(nil), guardStack...),
			winds: append([]*windFrame(nil), guardWinds...),
			hands: append([]*handlerFrame(nil), guardHands...),
			owner: m,
		}
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
	args := formArgs(form)
	if len(args) < 3 {
		m.Raise(NewError("define-record-type: malformed", form))
		return
	}
	typeName, ok := args[0].(*Symbol)
	if !ok {
		m.Raise(NewError("define-record-type: type name is not an identifier", args[0]))
		return
	}
	ctorSpec, ok := args[1].(*Pair)
	if !ok {
		m.Raise(NewError("define-record-type: malformed constructor spec", args[1]))
		return
	}
	ctorName, ok := ctorSpec.Car.(*Symbol)
	if !ok {
		m.Raise(NewError("define-record-type: constructor name is not an identifier", ctorSpec.Car))
		return
	}
	ctorFields := mustSlice(ctorSpec.Cdr)
	for _, f := range ctorFields {
		if _, ok := f.(*Symbol); !ok {
			m.Raise(NewError("define-record-type: constructor field is not an identifier", f))
			return
		}
	}
	predName, ok := args[2].(*Symbol)
	if !ok {
		m.Raise(NewError("define-record-type: predicate name is not an identifier", args[2]))
		return
	}
	rt := &RecordType{Name: typeName.Name}
	fieldIndex := map[string]int{}
	for _, spec := range args[3:] {
		p, ok := spec.(*Pair)
		if !ok {
			m.Raise(NewError("define-record-type: malformed field spec", spec))
			return
		}
		fname, ok := p.Car.(*Symbol)
		if !ok {
			m.Raise(NewError("define-record-type: field name is not an identifier", p.Car))
			return
		}
		items := mustSlice(p.Cdr)
		if len(items) < 1 {
			m.Raise(NewError("define-record-type: missing accessor", spec))
			return
		}
		accName, ok := items[0].(*Symbol)
		if !ok {
			m.Raise(NewError("define-record-type: accessor is not an identifier", items[0]))
			return
		}
		mutable := false
		var modName *Symbol
		if len(items) > 1 {
			modName, ok = items[1].(*Symbol)
			if !ok {
				m.Raise(NewError("define-record-type: modifier is not an identifier", items[1]))
				return
			}
			mutable = true
		}
		fieldIndex[fname.Name] = len(rt.Fields)
		rt.Fields = append(rt.Fields, fname)
		rt.Mutable = append(rt.Mutable, mutable)
		idx := len(rt.Fields) - 1
		env.Define(accName, &Primitive{Name: accName.Name, MinArgs: 1, MaxArgs: 1,
			Fn: func(m *Machine, a []Value) {
				r, ok := a[0].(*Record)
				if !ok || r.Type != rt {
					m.Raise(wrongTypeName("record of type "+rt.Name, a[0], accName.Name))
					return
				}
				m.Return(r.Fields[idx])
			}})
		if modName != nil {
			env.Define(modName, &Primitive{Name: modName.Name, MinArgs: 2, MaxArgs: 2,
				Fn: func(m *Machine, a []Value) {
					r, ok := a[0].(*Record)
					if !ok || r.Type != rt {
						m.Raise(wrongTypeName("record of type "+rt.Name, a[0], modName.Name))
						return
					}
					r.Fields[idx] = a[1]
					m.Return(UnspecifiedValue)
				}})
		}
	}
	for _, f := range ctorFields {
		if _, ok := fieldIndex[f.(*Symbol).Name]; !ok {
			m.Raise(NewError("define-record-type: constructor field is not a record field", f))
			return
		}
	}
	ctorIdx := make([]int, len(ctorFields))
	for i, f := range ctorFields {
		ctorIdx[i] = fieldIndex[f.(*Symbol).Name]
	}
	env.Define(typeName, &RecordTypeDescriptor{Type: rt})
	env.Define(ctorName, &Primitive{Name: ctorName.Name, MinArgs: len(ctorFields), MaxArgs: len(ctorFields),
		Fn: func(m *Machine, a []Value) {
			r := &Record{Type: rt, Fields: make([]Value, len(rt.Fields))}
			for i := range r.Fields {
				r.Fields[i] = UnspecifiedValue
			}
			for i, idx := range ctorIdx {
				r.Fields[idx] = a[i]
			}
			m.Return(r)
		}})
	env.Define(predName, &Primitive{Name: predName.Name, MinArgs: 1, MaxArgs: 1,
		Fn: func(m *Machine, a []Value) {
			r, ok := a[0].(*Record)
			m.Return(BooleanOf(ok && r.Type == rt))
		}})
	m.Return(UnspecifiedValue)
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
		tf, ok := cadr(p).(*Pair)
		if !ok {
			m.Raise(NewError("let-syntax: unsupported transformer", cadr(p)))
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

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
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

// ---------------------------------------------------------------------------
// Libraries
// ---------------------------------------------------------------------------

func evalImport(m *Machine, form Value, env *Env) {
	for _, spec := range formArgs(form) {
		bindings, err := m.ResolveImportSet(spec)
		if err != nil {
			m.RaiseError(err)
			return
		}
		for name, v := range bindings {
			env.Define(name, v)
		}
	}
	m.Return(UnspecifiedValue)
}

func evalDefineLibrary(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) < 1 {
		m.Raise(NewError("define-library: missing library name", form))
		return
	}
	name := LibraryNameString(args[0])
	if name == "" {
		m.Raise(NewError("define-library: malformed library name", args[0]))
		return
	}
	libEnv := NewEnvNamed(nil, name)
	lib := &Library{Name: name, Env: libEnv, Exports: map[*Symbol]Value{}}
	var exportSpecs []Value
	// First pass: imports.
	for _, decl := range args[1:] {
		p, ok := decl.(*Pair)
		if !ok {
			continue
		}
		h, _ := p.Car.(*Symbol)
		if h == nil {
			continue
		}
		switch h.Name {
		case "import":
			for _, spec := range mustSlice(p.Cdr) {
				bindings, err := m.ResolveImportSet(spec)
				if err != nil {
					m.RaiseError(err)
					return
				}
				for n, v := range bindings {
					libEnv.Define(n, v)
				}
			}
		case "export":
			exportSpecs = append(exportSpecs, mustSlice(p.Cdr)...)
		}
	}
	// Second pass: bodies.
	var bodyForms []Value
	var declFileDirs []string
	for _, decl := range args[1:] {
		p, ok := decl.(*Pair)
		if !ok {
			continue
		}
		h, _ := p.Car.(*Symbol)
		if h == nil {
			continue
		}
		switch h.Name {
		case "begin":
			bodyForms = append(bodyForms, mustSlice(p.Cdr)...)
		case "include":
			for _, a := range mustSlice(p.Cdr) {
				s, ok := a.(*String)
				if !ok {
					m.Raise(NewError("include: file name must be a string", a))
					return
				}
				path := m.resolvePath(s.Value())
				fs, err := ReadFileForms(m, path, false)
				if err != nil {
					m.RaiseError(err)
					return
				}
				declFileDirs = append(declFileDirs, dirOf(path))
				bodyForms = append(bodyForms, fs...)
			}
		case "include-library-declarations":
			for _, a := range mustSlice(p.Cdr) {
				s, ok := a.(*String)
				if !ok {
					continue
				}
				path := m.resolvePath(s.Value())
				fs, err := ReadFileForms(m, path, false)
				if err != nil {
					m.RaiseError(err)
					return
				}
				declFileDirs = append(declFileDirs, dirOf(path))
				for _, f := range fs {
					fp, ok := f.(*Pair)
					if !ok {
						continue
					}
					fh, _ := fp.Car.(*Symbol)
					if fh != nil && fh.Name == "export" {
						exportSpecs = append(exportSpecs, mustSlice(fp.Cdr)...)
					} else {
						bodyForms = append(bodyForms, f)
					}
				}
			}
		case "cond-expand":
			for _, cl := range mustSlice(p.Cdr) {
				cp, ok := cl.(*Pair)
				if !ok {
					continue
				}
				if s, ok := cp.Car.(*Symbol); ok && s.Name == "else" {
					bodyForms = append(bodyForms, mustSlice(cp.Cdr)...)
					break
				}
				if FeatureMatch(m, cp.Car) {
					bodyForms = append(bodyForms, mustSlice(cp.Cdr)...)
					break
				}
			}
		}
	}
	m.Libraries[name] = lib
	for _, d := range declFileDirs {
		m.AddLoadPath(d)
	}
	// The export resolution frame must be installed before the body runs:
	// evaluation is stack based, so a frame pushed afterwards would run
	// first.
	m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, v Value) {
		for range declFileDirs {
			m.PopLoadPath()
		}
		for _, spec := range exportSpecs {
			switch s := spec.(type) {
			case *Symbol:
				val, ok := libEnv.Lookup(Intern(s.Name))
				if !ok {
					m.Raise(NewError("define-library: exported identifier is not bound", s))
					return
				}
				lib.Exports[s] = val
			case *Pair:
				// (rename <internal> <external>)
				items := mustSlice(s)
				if len(items) != 3 {
					m.Raise(NewError("define-library: malformed rename export", spec))
					return
				}
				if kw, ok := items[0].(*Symbol); !ok || kw.Name != "rename" {
					m.Raise(NewError("define-library: malformed rename export", spec))
					return
				}
				internal, ok1 := items[1].(*Symbol)
				external, ok2 := items[2].(*Symbol)
				if !ok1 || !ok2 {
					m.Raise(NewError("define-library: malformed rename export", spec))
					return
				}
				val, ok := libEnv.Lookup(internal)
				if !ok {
					m.Raise(NewError("define-library: exported identifier is not bound", internal))
					return
				}
				lib.Exports[external] = val
			default:
				m.Raise(NewError("define-library: malformed export spec", spec))
				return
			}
		}
		m.Return(UnspecifiedValue)
	}})
	m.EvalSeq(bodyForms, libEnv)
}

// LibraryNameString renders a library name as a canonical string key.
func LibraryNameString(v Value) string {
	items, ok := ListToSlice(v)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		switch x := it.(type) {
		case *Symbol:
			parts = append(parts, x.Name)
		case *Integer:
			parts = append(parts, x.String())
		case *String:
			parts = append(parts, x.Value())
		default:
			return ""
		}
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func platformFeatures() []string {
	return platformFeaturesImpl()
}
