// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
)

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
				// Keep scanning: every internal definition is pre-bound, and
				// stopping at the first one meant a later name resolved to an
				// outer binding of the same name instead of being unassigned.
			case "define-values":
				if len(args) == 0 {
					return
				}
			collectValues:
				for names := args[0]; ; {
					switch n := names.(type) {
					case *Symbol:
						out = append(out, n)
						break collectValues
					case *Pair:
						if sym, ok := n.Car.(*Symbol); ok {
							out = append(out, sym)
						}
						names = n.Cdr
						continue
					}
					break collectValues
				}
			case "define-syntax":
				if len(args) > 0 {
					if sym, ok := args[0].(*Symbol); ok {
						out = append(out, sym)
					}
				}
			case "define-record-type":
				// Every name it defines, not just the type: they are all bound
				// by the same form, and a reference to the constructor or an
				// accessor elsewhere in the body has to see a binding of its
				// own rather than a global.
				if names, _, err := recordType(args); err == nil {
					out = append(out, names...)
				}
			default:
				return
			}
		}
	}
	scan(body)
	return out
}

// duplicateVar reports the variable a binding form names twice, or nil when
// every name is distinct, which is what R7RS requires: a duplicate is an error
// rather than a silent last-one-wins.
//
// It is the single answer to that question.  The interpreter and the compiler
// both ask it — the compiler to report the error while it is compiling, the
// interpreter to raise it while it is binding — and having two copies of the
// check meant having two places to keep the rule in step by hand.
func duplicateVar(syms []*Symbol) *Symbol {
	seen := map[*Symbol]bool{}
	for _, s := range syms {
		if seen[s] {
			return s
		}
		seen[s] = true
	}
	return nil
}

// checkDuplicateVars reports a variable that one binding form names twice.
func checkDuplicateVars(syms []*Symbol) error {
	if s := duplicateVar(syms); s != nil {
		return NewError("duplicate variable in the same binding form", s)
	}
	return nil
}

// prepBody pre-binds the names introduced by internal definitions, which is
// what gives a body letrec* semantics: a definition may refer to a later one,
// and referring to it before it is initialised is an error rather than a
// silent fallback to an outer binding of the same name.
func prepBody(env *Env, body []Value) {
	for _, s := range scanBodyNames(body) {
		if !env.Has(s) {
			env.Define(s, Unassigned)
		}
	}
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
	// (define (f . args) body...) and the curried form
	// (define ((f a) b) body...), which is (define (f a) (lambda (b) body...)).
	if _, ok := target.(*Pair); ok {
		inner := target.(*Pair)
		var expr Value = Cons(Intern("lambda"), Cons(inner.Cdr, listFromSlice(body)))
		t := inner.Car
		for {
			p, isPair := t.(*Pair)
			if !isPair {
				break
			}
			expr = List(Intern("lambda"), p.Cdr, expr)
			t = p.Car
		}
		name, ok := t.(*Symbol)
		if !ok {
			m.Raise(NewError("define: bad procedure name", target))
			return
		}
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
