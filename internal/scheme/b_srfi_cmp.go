// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"strings"
)

// SRFI-128, comparators, as the (srfi 128) builtin library.
//
// A comparator bundles the four operations a container needs: a type test, an
// equality, an ordering and a hash function.  Any of them may be missing, and
// the procedures here raise rather than invent an answer when the operation a
// caller asks for is the missing one.
//
// The reference page describes exactly this implementation: the standard's
// recursive constructors (make-pair-comparator, make-list-comparator,
// make-vector-comparator) are not provided yet.

const srfi128Lib = "(srfi 128)"

// Comparator is the SRFI-128 comparator object.  Each part is a procedure or
// False, meaning "not provided"; an equality of False means eqv?.
type Comparator struct {
	TypeTest Value
	Equal    Value
	Less     Value
	Hash     Value
}

func wantComparator(name string, v Value) *Comparator {
	c, ok := v.(*Comparator)
	if !ok {
		panic(errf(name, "expected a comparator but got %s", WriteToString(v)))
	}
	return c
}

// equalTo applies the comparator's equality, using the fast lane for a builtin.
func (c *Comparator) equalTo(m *Machine, a, b Value) bool {
	if c.Equal == nil || c.Equal == False {
		return Eqv(a, b)
	}
	if n := builtinName(c.Equal); n != "" {
		if res, ok := fastLess(n, a, b); ok {
			return res
		}
	}
	return IsTrue(newFastCaller(m, c.Equal).apply(a, b))
}

// lessThan applies the comparator's ordering; without one it raises.
func (c *Comparator) lessThan(name string, m *Machine, a, b Value) bool {
	if c.Less == nil || c.Less == False {
		panic(errf(name, "this comparator has no ordering"))
	}
	if n := builtinName(c.Less); n != "" {
		if res, ok := fastLess(n, a, b); ok {
			return res
		}
	}
	return IsTrue(newFastCaller(m, c.Less).apply(a, b))
}

// accepts applies the type test, if there is one.
func (c *Comparator) accepts(m *Machine, v Value) bool {
	if c.TypeTest == nil || c.TypeTest == False {
		return true
	}
	return IsTrue(newFastCaller(m, c.TypeTest).apply(v))
}

// isProcedure reports whether v can be applied.  The branches of
// comparator-if<=> may be either a thunk or a value.
func isProcedure(v Value) bool {
	switch v.(type) {
	case *Primitive, *Closure, *Continuation, *Parameter:
		return true
	}
	return false
}

func installSRFI128(m *Machine) {
	const lib = srfi128Lib

	// The two parameters hashing reads.  They are ordinary R7RS parameters, so
	// parameterize works on them.
	m.installEmbeddedSource(lib, `
	  (define hash-bound (make-parameter 1000000007))
	  (define hash-salt (make-parameter 0))
	`, "hash-bound", "hash-salt")

	// ------------------------------------------------------------ the object
	m.defSimple("make-comparator", 4, 4, func(a []Value) (Value, error) {
		for i, arg := range a {
			if arg == False {
				continue
			}
			if !isProcedure(arg) {
				panic(errf("make-comparator", "argument %d must be a procedure or #f", i+1))
			}
		}
		return &Comparator{TypeTest: a[0], Equal: a[1], Less: a[2], Hash: a[3]}, nil
	}, lib)

	m.defSimple("comparator?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Comparator)
		return BooleanOf(ok), nil
	}, lib)

	m.defSimple("comparator-ordered?", 1, 1, func(a []Value) (Value, error) {
		c := wantComparator("comparator-ordered?", a[0])
		return BooleanOf(c.Less != nil && c.Less != False), nil
	}, lib)

	m.defSimple("comparator-hashable?", 1, 1, func(a []Value) (Value, error) {
		c := wantComparator("comparator-hashable?", a[0])
		return BooleanOf(c.Hash != nil && c.Hash != False), nil
	}, lib)

	for _, part := range []struct {
		name string
		get  func(c *Comparator) Value
	}{
		{"comparator-type-test-predicate", func(c *Comparator) Value { return c.TypeTest }},
		{"comparator-equality-predicate", func(c *Comparator) Value { return c.Equal }},
		{"comparator-ordering-predicate", func(c *Comparator) Value { return c.Less }},
		{"comparator-hash-function", func(c *Comparator) Value { return c.Hash }},
	} {
		part := part
		m.defSimple(part.name, 1, 1, func(a []Value) (Value, error) {
			c := wantComparator(part.name, a[0])
			if v := part.get(c); v != nil {
				return v, nil
			}
			return False, nil
		}, lib)
	}

	m.defSimple("comparator-test-type", 1, 2, func(a []Value) (Value, error) {
		c := wantComparator("comparator-test-type", a[0])
		return BooleanOf(c.accepts(m, a[1])), nil
	}, lib)

	m.defSimple("comparator-check-type", 1, 2, func(a []Value) (Value, error) {
		c := wantComparator("comparator-check-type", a[0])
		if !c.accepts(m, a[1]) {
			panic(errf("comparator-check-type", "the value %s does not pass the type test", WriteToString(a[1])))
		}
		return True, nil
	}, lib)

	// -------------------------------------------------------- the operations
	m.defSimple("=?", 1, -1, func(a []Value) (Value, error) {
		c := wantComparator("=?", a[0])
		for i := 2; i < len(a); i++ {
			if !c.equalTo(m, a[i-1], a[i]) {
				return False, nil
			}
		}
		return True, nil
	}, lib)

	chain := func(name string, accept func(c *Comparator, m *Machine, x, y Value) bool) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			c := wantComparator(name, a[0])
			for i := 2; i < len(a); i++ {
				if !accept(c, m, a[i-1], a[i]) {
					return False, nil
				}
			}
			return True, nil
		}
	}
	m.defSimple("<?", 1, -1, chain("<?", func(c *Comparator, m *Machine, x, y Value) bool {
		return c.lessThan("<?", m, x, y)
	}), lib)
	m.defSimple(">?", 1, -1, chain(">?", func(c *Comparator, m *Machine, x, y Value) bool {
		return c.lessThan(">?", m, y, x)
	}), lib)
	m.defSimple("<=?", 1, -1, chain("<=?", func(c *Comparator, m *Machine, x, y Value) bool {
		return c.lessThan("<=?", m, x, y) || c.equalTo(m, x, y)
	}), lib)
	m.defSimple(">=?", 1, -1, chain(">=?", func(c *Comparator, m *Machine, x, y Value) bool {
		return c.lessThan(">=?", m, y, x) || c.equalTo(m, x, y)
	}), lib)

	// (comparator-if<=> cmp a b less equal greater) picks a branch by comparing
	// a and b; a branch that is a procedure is called, anything else is
	// returned as it stands.
	m.defSimple("comparator-if<=>", 6, 6, func(a []Value) (Value, error) {
		c := wantComparator("comparator-if<=>", a[0])
		var branch Value
		switch {
		case c.equalTo(m, a[1], a[2]):
			branch = a[4]
		case c.lessThan("comparator-if<=>", m, a[1], a[2]):
			branch = a[3]
		default:
			branch = a[5]
		}
		if isProcedure(branch) {
			return newFastCaller(m, branch).apply(), nil
		}
		return branch, nil
	}, lib)

	// ------------------------------------------------------- ready-made ones
	kindComparator := func(kind string) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			var equal Value = builtinProc(m, "equal?")
			switch kind {
			case "eq":
				equal = builtinProc(m, "eq?")
			case "eqv":
				equal = builtinProc(m, "eqv?")
			}
			return &Comparator{Equal: equal, Hash: builtinProc(m, "default-hash")}, nil
		}
	}
	m.defSimple("make-eq-comparator", 0, 0, kindComparator("eq"), lib)
	m.defSimple("make-eqv-comparator", 0, 0, kindComparator("eqv"), lib)
	m.defSimple("make-equal-comparator", 0, 0, kindComparator("equal"), lib)

	// The default comparator orders the atoms by type and refuses the rest,
	// which is what a hash table or a set wants by default.
	m.defSimple("default-less", 2, 2, func(a []Value) (Value, error) {
		return BooleanOf(defaultLessValue(m, "default-less", a[0], a[1])), nil
	})
	m.defSimple("make-default-comparator", 0, 0, func(a []Value) (Value, error) {
		return &Comparator{
			Equal: builtinProc(m, "equal?"),
			Less:  builtinProc(m, "default-less"),
			Hash:  builtinProc(m, "default-hash"),
		}, nil
	}, lib)

	// ------------------------------------------------------------- hashing
	hashFn := func(name string, check func(Value) bool, what string) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			if check != nil && !check(a[0]) {
				panic(errf(name, "expected %s but got %s", what, WriteToString(a[0])))
			}
			return Int(int64(srfiHashValue(m, a[0]))), nil
		}
	}
	isString := func(v Value) bool { _, ok := v.(*String); return ok }
	isSymbol := func(v Value) bool { _, ok := v.(*Symbol); return ok }
	isChar := func(v Value) bool { _, ok := v.(Char); return ok }
	isBool := func(v Value) bool { _, ok := v.(Boolean); return ok }
	isNumber := func(v Value) bool { return IsNumber(v) }

	m.defSimple("default-hash", 1, 1, hashFn("default-hash", nil, ""), lib)
	m.defSimple("boolean-hash", 1, 1, hashFn("boolean-hash", isBool, "a boolean"), lib)
	m.defSimple("char-hash", 1, 1, hashFn("char-hash", isChar, "a character"), lib)
	m.defSimple("string-hash", 1, 1, hashFn("string-hash", isString, "a string"), lib)
	m.defSimple("symbol-hash", 1, 1, hashFn("symbol-hash", isSymbol, "a symbol"), lib)
	m.defSimple("number-hash", 1, 1, hashFn("number-hash", isNumber, "a number"), lib)

	// The case-insensitive hashes fold the case first, so that values that are
	// string-ci=? or char-ci=? hash the same way.
	m.defSimple("char-ci-hash", 1, 1, func(a []Value) (Value, error) {
		if !isChar(a[0]) {
			panic(errf("char-ci-hash", "expected a character but got %s", WriteToString(a[0])))
		}
		return Int(int64(srfiHashString(m, ciLower(string(rune(wantChar("char-ci-hash", a[0]))))))), nil
	}, lib)

	m.defSimple("string-ci-hash", 1, 1, func(a []Value) (Value, error) {
		if !isString(a[0]) {
			panic(errf("string-ci-hash", "expected a string but got %s", WriteToString(a[0])))
		}
		return Int(int64(srfiHashString(m, ciLower(wantString("string-ci-hash", a[0]).Value())))), nil
	}, lib)
}

// defaultLessValue orders the atoms: numbers numerically, strings, characters
// and symbols by their text, booleans by #f before #t.  Anything else is an
// error, so that a container built on the default comparator reports a value it
// cannot order rather than ordering it wrongly.
func defaultLessValue(m *Machine, name string, a, b Value) bool {
	switch x := a.(type) {
	case *Integer, *Rational, Float:
		if !IsNumber(b) {
			panic(errf(name, "cannot compare %s with %s", WriteToString(a), WriteToString(b)))
		}
		return NumCmp(a, b) < 0
	case *String:
		y, ok := b.(*String)
		if !ok {
			panic(errf(name, "cannot compare %s with %s", WriteToString(a), WriteToString(b)))
		}
		return x.Value() < y.Value()
	case Char:
		y, ok := b.(Char)
		if !ok {
			panic(errf(name, "cannot compare %s with %s", WriteToString(a), WriteToString(b)))
		}
		return x < y
	case *Symbol:
		y, ok := b.(*Symbol)
		if !ok {
			panic(errf(name, "cannot compare %s with %s", WriteToString(a), WriteToString(b)))
		}
		return x.Name < y.Name
	case Boolean:
		y, ok := b.(Boolean)
		if !ok {
			panic(errf(name, "cannot compare %s with %s", WriteToString(a), WriteToString(b)))
		}
		return !bool(x) && bool(y)
	case Empty:
		if _, ok := b.(Empty); ok {
			return false
		}
	}
	panic(errf(name, "cannot order %s and %s", WriteToString(a), WriteToString(b)))
}

// srfiHashValue hashes any value into [0, hash-bound), mixing in hash-salt.  It
// is built on the same key function the hash tables use, so a value that is
// equal? to another hashes the same way.
func srfiHashValue(m *Machine, v Value) uint64 {
	return srfiHashString(m, fmt.Sprintf("%v", hashKey(v, "equal")))
}

// srfiHashString is an FNV-1a hash of the text, shifted by hash-salt and
// reduced modulo hash-bound.
func srfiHashString(m *Machine, text string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(text); i++ {
		h ^= uint64(text[i])
		h *= 1099511628211
	}
	h += uint64(paramInt(m, "hash-salt", 0))
	bound := paramInt(m, "hash-bound", 1000000007)
	if bound < 1 {
		bound = 1
	}
	return h % uint64(bound)
}

// paramInt reads an integer parameter's current value.
func paramInt(m *Machine, name string, def int64) int64 {
	v, ok := m.Builtin.Lookup(Intern(name))
	if !ok {
		return def
	}
	p, ok := v.(*Parameter)
	if !ok {
		return def
	}
	i, ok := p.Current().(*Integer)
	if !ok {
		return def
	}
	n, ok := i.Int64()
	if !ok {
		return def
	}
	return n
}

// ciLower is the case folding the case-insensitive hashes use.
func ciLower(s string) string { return strings.ToLower(s) }

func init() { registerInstaller(installSRFI128) }
