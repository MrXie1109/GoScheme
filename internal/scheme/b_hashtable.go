// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Hash tables (extension)
//
// Hash tables are not part of R7RS-small; they are provided as an extension
// modelled on SRFI 69 / R7RS-large so that programs have an efficient
// associative container.  Three equivalence relations are supported, matching
// eq?, eqv? and equal?.
// ---------------------------------------------------------------------------

// equivalenceKind maps a symbol or procedure naming eq?, eqv? or equal? to the
// kind of hash table it asks for.
func equivalenceKind(v Value) (string, bool) {
	name := ""
	switch x := v.(type) {
	case *Primitive:
		name = x.Name
	case *Symbol:
		name = x.Name
	}
	switch strings.TrimSuffix(name, "?") {
	case "eq":
		return "eq", true
	case "eqv":
		return "eqv", true
	case "equal":
		return "equal", true
	}
	return "", false
}

// hashKey maps a value to a comparable Go value, so that it can be used as a
// map key.  For the identity based tables only the value's identity is used;
// for equal?-based tables the key is derived from the structure.
func hashKey(v Value, kind string) interface{} {
	switch x := v.(type) {
	case Boolean:
		return [2]interface{}{"bool", bool(x)}
	case Char:
		return [2]interface{}{"char", rune(x)}
	case Empty:
		return "nil"
	case EOF:
		return "eof"
	case Unspecified:
		return "unspec"
	case *Symbol:
		// Symbols are compared by name by eq?/eqv?/equal?.
		return [2]interface{}{"sym", x.Name}
	case *Integer:
		if kind == "equal" {
			return [2]interface{}{"eqint", x.String()}
		}
		return [2]interface{}{"int", x.String()}
	case *Rational:
		return [2]interface{}{"rat", x.R.RatString()}
	case Float:
		if kind == "equal" {
			return [2]interface{}{"eqflo", FormatFloat(float64(x))}
		}
		return [2]interface{}{"flo", FormatFloat(float64(x))}
	case *Complex:
		return [2]interface{}{"cpx", fmt.Sprintf("%v|%v", hashKey(x.Re, kind), hashKey(x.Im, kind))}
	case *String:
		if kind == "equal" {
			return [2]interface{}{"str", x.Value()}
		}
		return [2]interface{}{"strptr", fmt.Sprintf("%p", x)}
	case *Bytevector:
		if kind == "equal" {
			return [2]interface{}{"bv", string(x.Bytes)}
		}
		return [2]interface{}{"bvptr", fmt.Sprintf("%p", x)}
	case *Pair:
		if kind == "equal" {
			return [2]interface{}{"pair", structuralHash(v, map[Value]bool{})}
		}
		return [2]interface{}{"pairptr", fmt.Sprintf("%p", x)}
	case *Vector:
		if kind == "equal" {
			return [2]interface{}{"vec", structuralHash(v, map[Value]bool{})}
		}
		return [2]interface{}{"vecptr", fmt.Sprintf("%p", x)}
	case *Record:
		if kind == "equal" {
			return [2]interface{}{"rec", fmt.Sprintf("%p", x)}
		}
		return [2]interface{}{"recptr", fmt.Sprintf("%p", x)}
	default:
		return [2]interface{}{"ptr", fmt.Sprintf("%p", v)}
	}
}

// structuralHash builds a printable content hash with cycle protection.
func structuralHash(v Value, seen map[Value]bool) string {
	switch x := v.(type) {
	case *Pair:
		if seen[Value(x)] {
			return "#cycle"
		}
		seen[Value(x)] = true
		s := "(" + structuralHash(x.Car, seen) + "." + structuralHash(x.Cdr, seen) + ")"
		delete(seen, Value(x))
		return s
	case *Vector:
		if seen[Value(x)] {
			return "#cycle"
		}
		seen[Value(x)] = true
		parts := make([]string, len(x.Items))
		for i, it := range x.Items {
			parts[i] = structuralHash(it, seen)
		}
		delete(seen, Value(x))
		return "#[" + strings.Join(parts, ",") + "]"
	default:
		return fmt.Sprintf("%v", hashKey(v, "equal"))
	}
}

// NewHashtable builds an empty hash table with the given equivalence.
func NewHashtable(kind string) *Hashtable {
	if kind == "" {
		kind = "equal"
	}
	return &Hashtable{Kind: kind, index: map[interface{}][]int{}, Mutable: true}
}

func (h *Hashtable) lookup(key Value) (int, bool) {
	k := hashKey(key, h.Kind)
	for _, i := range h.index[k] {
		if h.dead[i] {
			continue
		}
		// The hash only narrows the search: two different keys can share one,
		// and the key equivalence has the last word.  Without this check a
		// collision returned the wrong value, and an eqv? table could even
		// answer for +nan.0, which is not eqv? to itself.
		if tableKeyEqual(key, h.keys[i], h.Kind) {
			return i, true
		}
	}
	return 0, false
}

// tableKeyEqual applies the table's key equivalence.
func tableKeyEqual(a, b Value, kind string) bool {
	switch kind {
	case "eq":
		return Eq(a, b)
	case "eqv":
		return Eqv(a, b)
	}
	return Equal(a, b)
}

func (h *Hashtable) set(key, val Value) {
	if i, ok := h.lookup(key); ok {
		h.vals[i] = val
		return
	}
	k := hashKey(key, h.Kind)
	h.keys = append(h.keys, key)
	h.vals = append(h.vals, val)
	h.dead = append(h.dead, false)
	h.index[k] = append(h.index[k], len(h.keys)-1)
	h.count++
}

func (h *Hashtable) delete(key Value) bool {
	if i, ok := h.lookup(key); ok {
		h.dead[i] = true
		h.count--
		return true
	}
	return false
}

func (h *Hashtable) exists(key Value) bool {
	_, ok := h.lookup(key)
	return ok
}

// entries returns the live entries in insertion order.
func (h *Hashtable) entries() (keys, vals []Value) {
	for i, k := range h.keys {
		if !h.dead[i] {
			keys = append(keys, k)
			vals = append(vals, h.vals[i])
		}
	}
	return keys, vals
}

func (h *Hashtable) size() int { return h.count }

func installHashtables(m *Machine) {
	const lib = "(goscheme hash-table)"

	kindOf := func(kind string) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			return NewHashtable(kind), nil
		}
	}
	m.defSimple("make-eq-hashtable", 0, 1, kindOf("eq"), lib)
	m.defSimple("make-eqv-hashtable", 0, 1, kindOf("eqv"), lib)
	m.defSimple("make-equal-hashtable", 0, 1, kindOf("equal"), lib)
	// (make-hash-table [size-hint [equivalence]]) builds an equal? table, the
	// way SRFI 69's make-hash-table does.  A symbol or procedure naming eq?,
	// eqv? or equal? selects the equivalence; an exact integer is the size
	// hint, which this implementation takes as a hint and does not need.
	// Anything else raises rather than being silently ignored.
	m.defSimple("make-hash-table", 0, 2, func(a []Value) (Value, error) {
		kind := "equal"
		for _, arg := range a {
			if i, ok := arg.(*Integer); ok {
				if _, small := i.Int64(); !small {
					panic(errf("make-hash-table", "the size hint is too large: %s", WriteToString(arg)))
				}
				continue
			}
			if k, ok := equivalenceKind(arg); ok {
				kind = k
				continue
			}
			panic(errf("make-hash-table",
				"expected a size hint or an equivalence (eq?, eqv?, equal?) but got %s",
				WriteToString(arg)))
		}
		return NewHashtable(kind), nil
	}, lib)

	m.defSimple("hashtable?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Hashtable)
		return BooleanOf(ok), nil
	}, lib)
	m.defSimple("hash-table?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Hashtable)
		return BooleanOf(ok), nil
	}, lib)
	m.defSimple("hash-table-size", 1, 1, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-size", "expected a hash table"))
		}
		return Int(int64(h.size())), nil
	}, lib)
	m.defSimple("hash-table-count", 1, 1, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-count", "expected a hash table"))
		}
		return Int(int64(h.size())), nil
	}, lib)
	m.defSimple("hash-table-set!", 3, 3, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-set!", "expected a hash table"))
		}
		h.set(a[1], a[2])
		return UnspecifiedValue, nil
	}, lib)
	m.defSimple("hash-table-delete!", 2, 2, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-delete!", "expected a hash table"))
		}
		h.delete(a[1])
		return UnspecifiedValue, nil
	}, lib)
	m.defSimple("hash-table-exists?", 2, 2, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-exists?", "expected a hash table"))
		}
		return BooleanOf(h.exists(a[1])), nil
	}, lib)
	m.defSimple("hash-table-contains?", 2, 2, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-contains?", "expected a hash table"))
		}
		return BooleanOf(h.exists(a[1])), nil
	}, lib)
	m.defSimple("hash-table-ref/default", 3, 3, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-ref/default", "expected a hash table"))
		}
		if i, ok := h.lookup(a[1]); ok {
			return h.vals[i], nil
		}
		return a[2], nil
	}, lib)
	m.defSimple("hash-table-keys", 1, 1, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-keys", "expected a hash table"))
		}
		keys, _ := h.entries()
		return List(keys...), nil
	}, lib)
	m.defSimple("hash-table-values", 1, 1, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-values", "expected a hash table"))
		}
		_, vals := h.entries()
		return List(vals...), nil
	}, lib)
	m.defSimple("hash-table->alist", 1, 1, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table->alist", "expected a hash table"))
		}
		keys, vals := h.entries()
		items := make([]Value, len(keys))
		for i := range keys {
			items[i] = Cons(keys[i], vals[i])
		}
		return List(items...), nil
	}, lib)
	m.defSimple("hash-table-copy", 1, 1, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-copy", "expected a hash table"))
		}
		cp := NewHashtable(h.Kind)
		keys, vals := h.entries()
		for i := range keys {
			cp.set(keys[i], vals[i])
		}
		return cp, nil
	}, lib)
	m.defSimple("hash-table-clear!", 1, 1, func(a []Value) (Value, error) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			panic(errf("hash-table-clear!", "expected a hash table"))
		}
		h.keys, h.vals, h.dead = nil, nil, nil
		h.index = map[interface{}][]int{}
		h.count = 0
		return UnspecifiedValue, nil
	}, lib)
	m.def("hash-table-walk", 2, 2, func(m *Machine, a []Value) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			m.Raise(errf("hash-table-walk", "expected a hash table"))
			return
		}
		proc := wantProcedure("hash-table-walk", a[1])
		keys, vals := h.entries()
		i := 0
		var step func()
		step = func() {
			if i >= len(keys) {
				m.Return(UnspecifiedValue)
				return
			}
			j := i
			i++
			m.ApplyWith(proc, []Value{keys[j], vals[j]}, func(m *Machine, _ Value) { step() })
		}
		step()
	}, lib)
	m.def("hash-table-ref", 2, 3, func(m *Machine, a []Value) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			m.Raise(errf("hash-table-ref", "expected a hash table"))
			return
		}
		if i, ok := h.lookup(a[1]); ok {
			m.Return(h.vals[i])
			return
		}
		if len(a) == 3 {
			m.ApplyWith(a[2], nil, func(m *Machine, v Value) { m.Return(v) })
			return
		}
		m.Raise(errf("hash-table-ref", "no value associated with key %s", WriteToString(a[1])))
	}, lib)
	m.def("hash-table-update!", 3, 4, func(m *Machine, a []Value) {
		h, ok := a[0].(*Hashtable)
		if !ok {
			m.Raise(errf("hash-table-update!", "expected a hash table"))
			return
		}
		proc := wantProcedure("hash-table-update!", a[2])
		old := Value(UnspecifiedValue)
		found := false
		if i, ok := h.lookup(a[1]); ok {
			old, found = h.vals[i], true
		} else if len(a) == 4 {
			old = a[3]
		}
		m.ApplyWith(proc, []Value{old}, func(m *Machine, v Value) {
			_ = found
			h.set(a[1], v)
			m.Return(UnspecifiedValue)
		})
	}, lib)
	// (alist->hash-table alist [table]) adds the associations to the given
	// table and returns it, or builds a fresh equal? table when none is given.
	m.defSimple("alist->hash-table", 1, 2, func(a []Value) (Value, error) {
		items := wantList("alist->hash-table", a[0])
		h := NewHashtable("equal")
		if len(a) == 2 {
			given, ok := a[1].(*Hashtable)
			if !ok {
				panic(errf("alist->hash-table", "expected a hash table but got %s", WriteToString(a[1])))
			}
			h = given
		}
		for _, it := range items {
			p, ok := it.(*Pair)
			if !ok {
				panic(errf("alist->hash-table", "expected an association list"))
			}
			h.set(p.Car, p.Cdr)
		}
		return h, nil
	}, lib)

	// hash is a convenience used by SRFI 69 style code.
	m.defSimple("hash", 1, 2, func(a []Value) (Value, error) {
		s := fmt.Sprintf("%v", hashKey(a[0], "equal"))
		// A uint64 accumulator keeps the result non-negative without the
		// negation overflowing on the most negative value.
		var h uint64 = 5381
		for i := 0; i < len(s); i++ {
			h = h*33 + uint64(s[i])
		}
		if len(a) == 2 {
			// SRFI 125: with a bound, the result is less than the bound.
			bound := wantIndex("hash", a[1])
			if bound == 0 {
				return nil, errf("hash", "expected a positive bound but got 0")
			}
			return Int(int64(h % uint64(bound))), nil
		}
		return Int(int64(h & maxInt64)), nil
	}, lib)
}

// maxInt64 masks the sign bit, so a hash stays a non-negative exact integer.
const maxInt64 = uint64(1)<<63 - 1
