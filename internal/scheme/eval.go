// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
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
		"match":              evalMatch,
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
					expanded, err := mac.Expand(expr, env)
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
				// A variable binding shadows any syntactic keyword.  The
				// operator's value is in hand, so the application can start
				// from it: evaluating the symbol again would repeat the whole
				// environment search, which is the most expensive thing a call
				// does.
				if _, un := v.(unassigned); un {
					// Evaluating the symbol would say the same thing; keep the
					// message identical now that we skip that step.
					m.Raise(NewError("variable used before initialization", s))
					return
				}
				if p, ok := x.Cdr.(*Pair); ok {
					m.stack = append(m.stack, &fAppArgs{op: v, rest: p.Cdr, env: env})
					m.Eval(p.Car, env)
					return
				}
				if _, isNil := x.Cdr.(Empty); isNil {
					m.apply(v, nil)
					return
				}
				m.raiseErrorf("improper argument list")
				return
			} else if fn, isSpecial := specialForms[s.Name]; isSpecial {
				fn(m, expr, env)
				return
			}
		}
		m.stack = append(m.stack, &fAppOp{args: x.Cdr, env: env})
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
	items, _ := ListToSlice(Cdr(form))
	return items
}

func evalBadAux(m *Machine, form Value, env *Env) {
	m.Raise(NewError("invalid use of auxiliary syntax", Car(form)))
}
