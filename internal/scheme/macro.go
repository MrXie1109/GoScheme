// SPDX-License-Identifier: MIT

package scheme

import (
	"errors"
	"fmt"
)

// Macro is a syntactic binding created by define-syntax / let-syntax.  Only
// syntax-rules transformers are provided, which is the full extent of the
// R7RS-small macro system.
type Macro struct {
	Name     string
	Literals []*Symbol
	Rules    []MacroRule
	Env      *Env
	Ellipsis *Symbol
	// IsLetrecSyntax influences nothing at expansion time in this
	// implementation, but is recorded for error messages.
	Kind string
}

// MacroRule is one pattern/template pair.
type MacroRule struct {
	Pattern  Value
	Template Value
}

// matchVal is the result of matching one pattern variable.
type matchVal struct {
	isSeq bool
	seq   []matchVal
	datum Value
}

type matcher struct {
	// useEnv is where the macro is being used, which is what a literal such as
	// else has to be compared in: R7RS compares the binding, not the name.
	useEnv   *Env
	m        *Macro
	binds    map[*Symbol]*matchVal
	literals map[string]*Symbol
}

// NewSyntaxRules builds a syntax-rules macro.
func NewSyntaxRules(name string, ellipsis *Symbol, literals []*Symbol, rules []MacroRule, env *Env) *Macro {
	if ellipsis == nil {
		ellipsis = Intern("...")
	}
	return &Macro{Name: name, Ellipsis: ellipsis, Literals: literals, Rules: rules, Env: env}
}

// isLiteral reports whether s is one of the macro's literal identifiers.
// Literals are compared by identity (or by binding), not merely by name, so
// that an identifier introduced by an enclosing macro expansion does not
// capture an unrelated literal of the same name written at the use site.
func (m *Macro) isLiteral(s *Symbol) bool {
	for _, l := range m.Literals {
		if l == s {
			return true
		}
		if l.Name != s.Name {
			continue
		}
		if l.Mark == s.Mark {
			return true
		}
		if l.Mark == 0 && s.Mark == 0 {
			return true
		}
		lv, lok := m.Env.Lookup(l)
		sv, sok := m.Env.Lookup(s)
		if lok && sok && Equal(lv, sv) {
			return true
		}
	}
	return false
}

// isEllipsis reports whether s acts as the ellipsis identifier.  A literal
// takes priority over the ellipsis (R7RS 4.3.2).
func (m *Macro) isEllipsis(v Value) bool {
	s, ok := v.(*Symbol)
	if !ok || s.Name != m.Ellipsis.Name {
		return false
	}
	return !m.isLiteral(s)
}

// Expand applies the macro to the given form (which includes the keyword).
func (m *Macro) Expand(form Value, useEnv *Env) (Value, error) {
	// Special case: a macro used as an identifier reference `(m)` is just an
	// application; `(m ...)` with no arguments still must match a rule.
	args := form
	for _, rule := range m.Rules {
		binds := map[*Symbol]*matchVal{}
		mt := &matcher{m: m, binds: binds, useEnv: useEnv}
		// Both the pattern and the form start with the keyword, which is
		// ignored per R7RS: match the tails.
		patTail := cdr(rule.Pattern)
		formTail := cdr(args)
		if patTail == nil || formTail == nil {
			continue
		}
		if mt.match(patTail, formTail) {
			mark := newMark(m.Env)
			return m.instantiate(rule.Template, binds, mark, 0), nil
		}
	}
	return nil, NewError(fmt.Sprintf("%s: no matching syntax-rules pattern", m.Name), form)
}

// sameBinding reports whether an identifier at the use site refers to the same
// binding as the literal at the macro definition site.  Two names that are both
// unbound are the same binding (the common case: else, =>, ...); if either is
// bound, they must be bound to the same thing, which is what makes
// (let ((else #f)) (macro-using-else)) not match.
func (m *Macro) sameBinding(defSym, useSym *Symbol, useEnv *Env) bool {
	defVal, defBound := m.Env.Lookup(defSym)
	if useEnv == nil {
		return defSym.Name == useSym.Name
	}
	useVal, useBound := useEnv.Lookup(useSym)
	if defBound != useBound {
		return false
	}
	if !defBound {
		return defSym.Name == useSym.Name
	}
	return defVal == useVal
}

// ---------------------------------------------------------------------------
// Pattern matching
// ---------------------------------------------------------------------------

func (mt *matcher) match(pat, in Value) bool {
	switch p := pat.(type) {
	case *Symbol:
		if mt.m.isLiteral(p) {
			s, ok := in.(*Symbol)
			if !ok {
				return false
			}
			return mt.m.sameBinding(p, s, mt.useEnv)
		}
		if p.Name == "_" && !p.IsMarked() {
			return true
		}
		if mv, exists := mt.binds[p]; exists {
			return sameDatum(mv.datum, in)
		}
		mt.binds[p] = &matchVal{datum: in}
		return true
	case *Pair, Empty:
		return mt.matchList(pat, in)
	case *Vector:
		v, ok := in.(*Vector)
		if !ok {
			return false
		}
		return mt.matchList(listFromSlice(p.Items), listFromSlice(v.Items))
	default:
		return sameDatum(pat, in)
	}
}

func listFromSlice(items []Value) Value {
	var res Value = Nil
	for i := len(items) - 1; i >= 0; i-- {
		res = Cons(items[i], res)
	}
	return res
}

// matchList matches a (possibly dotted) list pattern against a value.
func (mt *matcher) matchList(pat, in Value) bool {
	_, hasEllipsis, preEllipsis, postEllipsis, tailPat := splitEllipsis(pat, mt.m)
	if !hasEllipsis {
		// Ordinary list pattern (possibly dotted).
		for {
			switch p := pat.(type) {
			case Empty:
				_, ok := in.(Empty)
				return ok
			case *Pair:
				ip, ok := in.(*Pair)
				if !ok {
					return false
				}
				if !mt.match(p.Car, ip.Car) {
					return false
				}
				pat, in = p.Cdr, ip.Cdr
				continue
			default:
				return mt.match(pat, in)
			}
		}
	}
	items, tail, improper := listParts(in)
	nPre := len(preEllipsis)
	nPost := len(postEllipsis)
	if improper {
		if tailPat == nil {
			return false
		}
	} else if tailPat != nil {
		tail = Nil
	}
	if len(items) < nPre+nPost {
		return false
	}
	for i, p := range preEllipsis {
		if !mt.match(p, items[i]) {
			return false
		}
	}
	mid := items[nPre : len(items)-nPost]
	subPat := repeatedPatternOf(pat, mt.m)
	vars := map[*Symbol]bool{}
	collectPatternVars(subPat, mt.m, vars)
	var perIter []map[*Symbol]*matchVal
	for _, it := range mid {
		sub := &matcher{m: mt.m, binds: map[*Symbol]*matchVal{}, useEnv: mt.useEnv}
		if !sub.match(subPat, it) {
			return false
		}
		perIter = append(perIter, sub.binds)
	}
	for v := range vars {
		seq := make([]matchVal, len(perIter))
		for i, b := range perIter {
			mv, ok := b[v]
			if !ok {
				return false
			}
			seq[i] = *mv
		}
		mt.binds[v] = &matchVal{isSeq: true, seq: seq}
	}
	for i, p := range postEllipsis {
		if !mt.match(p, items[len(items)-nPost+i]) {
			return false
		}
	}
	if tailPat != nil {
		if !mt.match(tailPat, tail) {
			return false
		}
	}
	return true
}

// splitEllipsis returns the list elements before and after the single
// ellipsis, and the dotted tail pattern.
func splitEllipsis(pat Value, m *Macro) (head Value, found bool, pre, post []Value, tail Value) {
	var items []Value
	cur := pat
	for {
		p, ok := cur.(*Pair)
		if !ok {
			break
		}
		items = append(items, p.Car)
		cur = p.Cdr
	}
	tail = cur
	if _, isNil := cur.(Empty); isNil {
		tail = nil
	}
	for i := 1; i < len(items); i++ {
		if m.isEllipsis(items[i]) {
			return pat, true, items[:i-1], items[i+1:], tail
		}
	}
	return pat, false, nil, nil, tail
}

func repeatedPatternOf(pat Value, m *Macro) Value {
	var items []Value
	cur := pat
	for {
		p, ok := cur.(*Pair)
		if !ok {
			break
		}
		items = append(items, p.Car)
		cur = p.Cdr
	}
	for i := 1; i < len(items); i++ {
		if m.isEllipsis(items[i]) {
			return items[i-1]
		}
	}
	return Nil
}

func collectPatternVars(pat Value, m *Macro, out map[*Symbol]bool) {
	switch p := pat.(type) {
	case *Symbol:
		if p.Name != "_" && !m.isLiteral(p) && !m.isEllipsis(p) {
			out[p] = true
		}
	case *Pair:
		collectPatternVars(p.Car, m, out)
		collectPatternVars(p.Cdr, m, out)
	case *Vector:
		for _, it := range p.Items {
			collectPatternVars(it, m, out)
		}
	}
}

// listParts splits a value into its elements plus a non-list tail.
func listParts(v Value) (items []Value, tail Value, improper bool) {
	for {
		p, ok := v.(*Pair)
		if !ok {
			break
		}
		items = append(items, p.Car)
		v = p.Cdr
	}
	if _, isNil := v.(Empty); isNil {
		return items, nil, false
	}
	return items, v, true
}

// sameDatum implements the `equal?` comparison used for pattern constants.
func sameDatum(a, b Value) bool {
	return Equal(a, b)
}

// ---------------------------------------------------------------------------
// Template instantiation
// ---------------------------------------------------------------------------

// instantiate expands a template.  depth is the number of enclosing
// ellipses; esc is true inside a (... template) escape, where ellipses are
// ordinary identifiers.
func (m *Macro) instantiate(tmpl Value, binds map[*Symbol]*matchVal, mark uint64, depth int) Value {
	return m.instantiateEsc(tmpl, binds, mark, depth, false)
}

func (m *Macro) instantiateEsc(tmpl Value, binds map[*Symbol]*matchVal, mark uint64, depth int, esc bool) Value {
	switch t := tmpl.(type) {
	case *Symbol:
		if mv, ok := binds[t]; ok {
			if mv.isSeq {
				if len(mv.seq) > 0 {
					return mv.seq[0].datumOrNil()
				}
				return Nil
			}
			return mv.datum
		}
		if m.isEllipsis(t) {
			return t
		}
		return renameSymbol(t, mark)
	case *Pair:
		// (... template) escapes the ellipsis.
		if !esc {
			if m.isEllipsis(t.Car) {
				if rest, ok := t.Cdr.(*Pair); ok {
					if _, more := rest.Cdr.(Empty); more {
						return m.instantiateEsc(rest.Car, binds, mark, depth, true)
					}
				}
			}
		}
		var out []Value
		cur := Value(t)
		for {
			p, ok := cur.(*Pair)
			if !ok {
				break
			}
			if !esc {
				if next, ok := p.Cdr.(*Pair); ok {
					if m.isEllipsis(next.Car) {
						iters := m.iterationCount(p.Car, binds)
						for i := 0; i < iters; i++ {
							nb := m.subBinds(p.Car, binds, i)
							out = append(out, m.instantiateEsc(p.Car, nb, mark, depth+1, false))
						}
						cur = next.Cdr
						continue
					}
				}
			}
			out = append(out, m.instantiateEsc(p.Car, binds, mark, depth, esc))
			cur = p.Cdr
		}
		res := listFromSlice(out)
		if _, isNil := cur.(Empty); !isNil {
			tail := m.instantiateEsc(cur, binds, mark, depth, esc)
			res = appendToTail(res, tail)
		}
		return res
	case *Vector:
		lst := m.instantiateEsc(listFromSlice(t.Items), binds, mark, depth, esc)
		items, _ := ListToSlice(lst)
		return NewVectorFrom(items)
	default:
		return tmpl
	}
}

func appendToTail(lst, tail Value) Value {
	items, _ := ListToSlice(lst)
	res := tail
	for i := len(items) - 1; i >= 0; i-- {
		res = Cons(items[i], res)
	}
	return res
}

func (mv *matchVal) datumOrNil() Value {
	if mv.isSeq {
		return Nil
	}
	return mv.datum
}

// iterationCount computes how many times a sub-template with an ellipsis must
// be expanded.
func (m *Macro) iterationCount(tmpl Value, binds map[*Symbol]*matchVal) int {
	count := -1
	var walk func(v Value)
	walk = func(v Value) {
		switch t := v.(type) {
		case *Symbol:
			if mv, ok := binds[t]; ok && mv.isSeq {
				if count < 0 {
					count = len(mv.seq)
				} else if count != len(mv.seq) {
					count = 0
				}
			}
		case *Pair:
			// Skip escaped ellipsis sections.
			walk(t.Car)
			walk(t.Cdr)
		case *Vector:
			for _, it := range t.Items {
				walk(it)
			}
		}
	}
	walk(tmpl)
	if count < 0 {
		return 0
	}
	return count
}

// subBinds returns a binding set where every sequence variable appearing in
// the template is replaced by its i-th match.
func (m *Macro) subBinds(tmpl Value, binds map[*Symbol]*matchVal, i int) map[*Symbol]*matchVal {
	out := make(map[*Symbol]*matchVal, len(binds))
	for k, v := range binds {
		out[k] = v
	}
	seen := map[*Symbol]bool{}
	var walk func(v Value)
	walk = func(v Value) {
		switch t := v.(type) {
		case *Symbol:
			if seen[t] {
				return
			}
			seen[t] = true
			if mv, ok := binds[t]; ok && mv.isSeq {
				if i < len(mv.seq) {
					out[t] = &mv.seq[i]
				} else {
					out[t] = &matchVal{datum: Nil}
				}
			}
		case *Pair:
			walk(t.Car)
			walk(t.Cdr)
		case *Vector:
			for _, it := range t.Items {
				walk(it)
			}
		}
	}
	walk(tmpl)
	return out
}

// ---------------------------------------------------------------------------
// Parsing syntax-rules
// ---------------------------------------------------------------------------

// ParseSyntaxRules parses the body of a syntax-rules form:
//
//	(syntax-rules [ellipsis] (literal ...) (pattern template) ...)
func ParseSyntaxRules(name string, form Value, env *Env) (*Macro, error) {
	rest := cdr(form)
	if rest == nil {
		return nil, errors.New("malformed syntax-rules")
	}
	ellipsis := Intern("...")
	if s, ok := car(rest).(*Symbol); ok && s.Name != "(" {
		// optional custom ellipsis
		ellipsis = s
		rest = cdr(rest)
	}
	litsVal := car(rest)
	lits, ok := ListToSlice(litsVal)
	if !ok {
		return nil, errors.New("syntax-rules: bad literal list")
	}
	var literals []*Symbol
	for _, l := range lits {
		s, ok := l.(*Symbol)
		if !ok {
			return nil, errors.New("syntax-rules: literal is not an identifier")
		}
		literals = append(literals, s)
	}
	var rules []MacroRule
	for _, r := range mustSlice(cdr(rest)) {
		items, ok := ListToSlice(r)
		if !ok || len(items) != 2 {
			return nil, errors.New("syntax-rules: each rule must be (pattern template)")
		}
		rules = append(rules, MacroRule{Pattern: items[0], Template: items[1]})
	}
	return NewSyntaxRules(name, ellipsis, literals, rules, env), nil
}

func mustSlice(v Value) []Value {
	items, _ := ListToSlice(v)
	return items
}
