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

func (m *Macro) isLiteral(s *Symbol) bool {
	for _, l := range m.Literals {
		if l.Name == s.Name {
			return true
		}
	}
	return false
}

func (m *Macro) isEllipsis(v Value) bool {
	s, ok := v.(*Symbol)
	return ok && s.Name == m.Ellipsis.Name
}

// Expand applies the macro to the given form (which includes the keyword).
func (m *Macro) Expand(form Value) (Value, error) {
	// Special case: a macro used as an identifier reference `(m)` is just an
	// application; `(m ...)` with no arguments still must match a rule.
	args := form
	for _, rule := range m.Rules {
		binds := map[*Symbol]*matchVal{}
		mt := &matcher{m: m, binds: binds}
		// The keyword position of the pattern is ignored, per R7RS.
		pat := rule.Pattern
		if p, ok := pat.(*Pair); ok {
			pat = Cons(Intern("_"), p.Cdr)
		}
		if mt.match(pat, args) {
			mark := newMark(m.Env)
			return m.instantiate(rule.Template, binds, mark, 0), nil
		}
	}
	return nil, NewError(fmt.Sprintf("%s: no matching syntax-rules pattern", m.Name), form)
}

// ---------------------------------------------------------------------------
// Pattern matching
// ---------------------------------------------------------------------------

func (mt *matcher) match(pat, in Value) bool {
	switch p := pat.(type) {
	case *Symbol:
		if p.Name == "_" && !p.IsMarked() {
			return true
		}
		if mt.m.isLiteral(p) {
			s, ok := in.(*Symbol)
			return ok && s.Name == p.Name
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
	_, hasEllipsis, preEllipsis, postEllipsis, tailPat := splitEllipsis(pat, mt.m.Ellipsis)
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
	subPat := repeatedPatternOf(pat, mt.m.Ellipsis)
	vars := map[*Symbol]bool{}
	collectPatternVars(subPat, mt.m, vars)
	var perIter []map[*Symbol]*matchVal
	for _, it := range mid {
		sub := &matcher{m: mt.m, binds: map[*Symbol]*matchVal{}}
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
func splitEllipsis(pat Value, ellipsis *Symbol) (head Value, found bool, pre, post []Value, tail Value) {
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
		if s, ok := items[i].(*Symbol); ok && s.Name == ellipsis.Name {
			return pat, true, items[:i-1], items[i+1:], tail
		}
	}
	return pat, false, nil, nil, tail
}

func repeatedPatternOf(pat Value, ellipsis *Symbol) Value {
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
		if s, ok := items[i].(*Symbol); ok && s.Name == ellipsis.Name {
			return items[i-1]
		}
	}
	return Nil
}

func collectPatternVars(pat Value, m *Macro, out map[*Symbol]bool) {
	switch p := pat.(type) {
	case *Symbol:
		if p.Name != "_" && !m.isLiteral(p) && p.Name != m.Ellipsis.Name {
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

// instantiate expands a template.  depth is the number of enclosing ellipses.
func (m *Macro) instantiate(tmpl Value, binds map[*Symbol]*matchVal, mark uint64, depth int) Value {
	switch t := tmpl.(type) {
	case *Symbol:
		if mv, ok := binds[t]; ok {
			if mv.isSeq {
				// A pattern variable used without enough ellipses: use the
				// first match (permissive).
				if len(mv.seq) > 0 {
					return m.instantiateValue(mv.seq[0].datumOrNil(), binds, mark, depth)
				}
				return Nil
			}
			return mv.datum
		}
		if t.Name == m.Ellipsis.Name {
			return t
		}
		return renameSymbol(t, mark)
	case *Pair:
		// (... template) escapes the ellipsis.
		if s, ok := t.Car.(*Symbol); ok && s.Name == m.Ellipsis.Name {
			if rest, ok := t.Cdr.(*Pair); ok {
				_, more := rest.Cdr.(Empty)
				if more {
					return m.instantiateNoEscape(rest.Car, binds, mark, depth)
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
			// Is the next element the ellipsis?
			if next, ok := p.Cdr.(*Pair); ok {
				if es, ok := next.Car.(*Symbol); ok && es.Name == m.Ellipsis.Name {
					iters := m.iterationCount(p.Car, binds)
					for i := 0; i < iters; i++ {
						nb := m.subBinds(p.Car, binds, i)
						out = append(out, m.instantiate(p.Car, nb, mark, depth+1))
					}
					cur = next.Cdr
					continue
				}
			}
			out = append(out, m.instantiate(p.Car, binds, mark, depth))
			cur = p.Cdr
		}
		res := listFromSlice(out)
		// Dotted tail
		if _, isNil := cur.(Empty); !isNil {
			tail := m.instantiate(cur, binds, mark, depth)
			res = appendToTail(res, tail)
		}
		return res
	case *Vector:
		lst := m.instantiate(listFromSlice(t.Items), binds, mark, depth)
		items, _ := ListToSlice(lst)
		return NewVectorFrom(items)
	default:
		return tmpl
	}
}

func (m *Macro) instantiateValue(v Value, binds map[*Symbol]*matchVal, mark uint64, depth int) Value {
	return v
}

func (m *Macro) instantiateNoEscape(tmpl Value, binds map[*Symbol]*matchVal, mark uint64, depth int) Value {
	return m.instantiate(tmpl, binds, mark, depth)
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
