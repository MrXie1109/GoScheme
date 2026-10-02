// SPDX-License-Identifier: MIT

package scheme

// (goscheme match) — pattern matching.
//
// A macro would be the obvious way to write this, and syntax-rules cannot do
// it: a clause such as ((1) 'one) has to compare against the literal 1, and
// syntax-rules has no way to say "any self-evaluating datum here" — a macro
// pattern matches one fixed form, so every literal would need its own rule.
// This is therefore a special form, which also gives the error messages
// somewhere to point when a pattern is malformed.
//
// The patterns are: _, a symbol (which binds), (quote datum) for a literal,
// any self-evaluating literal, (), (p1 p2 ... . rest), #(p1 ...), and the
// combinators (and p ...), (or p ...) and (not p).
//
// A clause is (pattern body ...), and a guard is the first form of the body:
//
//	(match n
//	  (n (guard (> n 5)) 'big)      ; the guard follows the pattern, as a
//	  (n (guard (> n 0)) 'small)    ; sibling of it, not inside it
//	  (else 'other))
//
// Writing ((n (guard test)) body) instead makes the pattern the two-element
// list (n (guard test)), which matches almost nothing — the parentheses are
// the whole difference, so the rule is worth stating plainly.  A body that
// genuinely begins with a one-clause guard expression should be wrapped in
// begin.

// matchBinding is one variable bound by a pattern.
type matchBinding struct {
	sym *Symbol
	val Value
}

const matchLib = "(goscheme match)"

func installMatch(m *Machine) {
	m.defSyntax(matchLib, "match")
}

// evalMatch evaluates (match expr clause ...).
func evalMatch(m *Machine, form Value, env *Env) {
	args := formArgs(form)
	if len(args) < 2 {
		m.Raise(NewError("match: expected an expression and at least one clause"))
		return
	}
	subject, clauses := args[0], args[1:]
	// The subject is evaluated once, and the clauses see its value.
	m.EvalWith(subject, env, func(m *Machine, v Value) {
		matchClauses(m, clauses, v, env)
	})
}

// matchClauses tries each clause in order.
func matchClauses(m *Machine, clauses []Value, v Value, env *Env) {
	for i, clause := range clauses {
		pat, guard, body, err := parseMatchClause(clause)
		if err != nil {
			m.RaiseError(err)
			return
		}
		var binds []matchBinding
		ok, err := matchPattern(pat, v, &binds)
		if err != nil {
			m.RaiseError(err)
			return
		}
		if !ok {
			continue
		}
		clauseEnv := NewEnv(env)
		for _, b := range binds {
			clauseEnv.Define(b.sym, b.val)
		}
		if guard == nil {
			m.EvalSeq(body, clauseEnv)
			return
		}
		// A guard is evaluated with the pattern's bindings in scope; when it is
		// false the search continues with the clauses that follow.
		rest := clauses[i+1:]
		m.EvalWith(guard, clauseEnv, func(m *Machine, gv Value) {
			if IsFalse(gv) {
				matchClauses(m, rest, v, env)
				return
			}
			m.EvalSeq(body, clauseEnv)
		})
		return
	}
	m.Raise(NewError("match: no pattern matched " + WriteToString(v)))
}

// parseMatchClause splits (pattern body ...) or (pattern (guard test) body ...).
func parseMatchClause(clause Value) (pat, guard Value, body []Value, err error) {
	items, ok := ListToSlice(clause)
	if !ok || len(items) < 2 {
		return nil, nil, nil, NewError("match: a clause is (pattern body ...)")
	}
	pat, body = items[0], items[1:]
	if p, ok := body[0].(*Pair); ok {
		if s, ok := p.Car.(*Symbol); ok && s.Name == "guard" {
			rest := mustSlice(p.Cdr)
			if len(rest) != 1 {
				return nil, nil, nil, NewError("match: (guard test) takes one expression")
			}
			guard, body = rest[0], body[1:]
			if len(body) == 0 {
				return nil, nil, nil, NewError("match: a guarded clause needs a body")
			}
		}
	}
	return pat, guard, body, nil
}

// matchPatternVars lists the variables a pattern binds, in the order
// matchPattern binds them.  A compiled clause uses it to build the thunk its
// body runs in: the values are passed by name, because a branch of an `or`
// binds a different set from the branch beside it.
func matchPatternVars(pat Value) []*Symbol {
	var out []*Symbol
	var walk func(p Value)
	var list func(p Value)
	walk = func(p Value) {
		switch t := p.(type) {
		case *Symbol:
			if t.Name != "_" && t.Name != "else" {
				out = append(out, t)
			}
		case *Pair:
			if sym, ok := t.Car.(*Symbol); ok {
				switch sym.Name {
				case "quote", "not":
					return
				case "and", "or":
					for _, sub := range mustSlice(t.Cdr) {
						walk(sub)
					}
					return
				}
			}
			list(p)
		case *Vector:
			for _, e := range t.Items {
				walk(e)
			}
		}
	}
	list = func(p Value) {
		cur := p
		for {
			cp, ok := cur.(*Pair)
			if !ok {
				break
			}
			walk(cp.Car)
			cur = cp.Cdr
		}
		walk(cur) // an improper tail is a pattern of its own
	}
	walk(pat)
	return out
}

// matchPattern reports whether v matches pat, appending the bindings it makes.
func matchPattern(pat, v Value, binds *[]matchBinding) (bool, error) {
	switch p := pat.(type) {
	case *Symbol:
		// _ is the wildcard, and else is accepted as the catch-all spelling a
		// cond user reaches for.  Every other symbol binds.
		if p.Name == "_" || p.Name == "else" {
			return true, nil
		}
		*binds = append(*binds, matchBinding{sym: p, val: v})
		return true, nil

	case *Pair:
		if s, ok := p.Car.(*Symbol); ok {
			switch s.Name {
			case "quote":
				rest := mustSlice(p.Cdr)
				if len(rest) != 1 {
					return false, NewError("match: quote takes one datum")
				}
				return Equal(rest[0], v), nil
			case "and":
				for _, sub := range mustSlice(p.Cdr) {
					ok, err := matchPattern(sub, v, binds)
					if err != nil || !ok {
						return false, err
					}
				}
				return true, nil
			case "or":
				// The first branch that matches wins, and brings its bindings.
				for _, sub := range mustSlice(p.Cdr) {
					trial := append([]matchBinding(nil), (*binds)...)
					ok, err := matchPattern(sub, v, &trial)
					if err != nil {
						return false, err
					}
					if ok {
						*binds = trial
						return true, nil
					}
				}
				return false, nil
			case "not":
				rest := mustSlice(p.Cdr)
				if len(rest) != 1 {
					return false, NewError("match: not takes one pattern")
				}
				trial := append([]matchBinding(nil), (*binds)...)
				ok, err := matchPattern(rest[0], v, &trial)
				if err != nil {
					return false, err
				}
				return !ok, nil
			}
		}
		// A list pattern: walk both pair chains, then match what is left.
		var cur Value = pat
		var rest Value = v
		for {
			cp, ok := cur.(*Pair)
			if !ok {
				break
			}
			rp, ok := rest.(*Pair)
			if !ok {
				return false, nil
			}
			ok, err := matchPattern(cp.Car, rp.Car, binds)
			if err != nil || !ok {
				return false, err
			}
			cur, rest = cp.Cdr, rp.Cdr
		}
		if _, isNil := cur.(Empty); isNil {
			_, valueNil := rest.(Empty)
			return valueNil, nil
		}
		// An improper tail: a pattern such as (a b . rest).
		return matchPattern(cur, rest, binds)

	case *Vector:
		items, ok := v.(*Vector)
		if !ok || len(items.Items) != len(p.Items) {
			return false, nil
		}
		for i := range p.Items {
			ok, err := matchPattern(p.Items[i], items.Items[i], binds)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}

	// Anything else is a literal: numbers, strings, characters, booleans.
	return Equal(pat, v), nil
}

func init() { registerInstaller(installMatch) }
