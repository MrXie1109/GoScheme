// SPDX-License-Identifier: MIT

package scheme

import (
	"math"
	"sort"
)

// The sequence half of (goscheme fast): lists and vectors, plus the folds.
//
// Everything here walks the sequence once, in Go, and every procedure that
// takes a predicate or a comparison knows the builtins it can apply directly
// (see fastLess and fastPred), so a call back into Scheme is the exception.

// seqSlice returns the elements of a list or a vector.  A vector is not
// copied: callers must treat the result as read-only.
func seqSlice(name string, v Value) []Value {
	if s, ok := v.(*Vector); ok {
		return s.Items
	}
	items, ok := ListToSlice(v)
	if !ok {
		panic(errf(name, "expected a proper list or a vector but got %s", WriteToString(v)))
	}
	return items
}

// seqLen is the length of a list or a vector.
func seqLen(name string, v Value) int {
	if s, ok := v.(*Vector); ok {
		return len(s.Items)
	}
	if n := ListLength(v); n >= 0 {
		return n
	}
	panic(errf(name, "expected a proper list or a vector but got %s", WriteToString(v)))
}

// atomKey is a hashable key for an atom, and false for a pair or a vector,
// which have no cheap canonical form.  It is only ever a hint: two values that
// share a key still have to satisfy the comparison before they count as equal,
// so 0.0 and -0.0 stay distinct when equal? says they are.
func atomKey(v Value) (string, bool) {
	switch v.(type) {
	case *Integer, *Rational, Float, *String, *Symbol, Char, Boolean, Empty:
		return WriteToString(v), true
	}
	return "", false
}

// listOf builds a list from a slice.
func listOf(items []Value) Value { return List(items...) }

// valuesOf returns two values, the way SRFI-1's partition and split-at do.
func valuesOf(a, b Value) Value {
	return &MultipleValues{Values: []Value{a, b}}
}

// builtinProc is a primitive from the global environment, for the default
// comparison of a procedure that takes an optional one.
func builtinProc(m *Machine, name string) Value {
	if v, ok := m.Builtin.Lookup(Intern(name)); ok {
		return v
	}
	return False
}

// ---------------------------------------------------------------------------
// Lists
// ---------------------------------------------------------------------------

func installFastLists(m *Machine, lib string) {
	// (last list) is the final element.
	m.defSimple("last", 1, 1, func(a []Value) (Value, error) {
		items := seqSlice("last", a[0])
		if len(items) == 0 {
			panic(errf("last", "expected a non-empty sequence"))
		}
		return items[len(items)-1], nil
	}, lib)

	// (take n list) is the first n elements; (drop n list) is what is left.
	m.defSimple("take", 2, 2, func(a []Value) (Value, error) {
		n := wantIndex("take", a[0])
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("take", "expected a proper list but got %s", WriteToString(a[1])))
		}
		if n > len(items) {
			panic(errf("take", "the list has only %d elements", len(items)))
		}
		return listOf(items[:n]), nil
	}, lib)

	m.defSimple("drop", 2, 2, func(a []Value) (Value, error) {
		n := wantIndex("drop", a[0])
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("drop", "expected a proper list but got %s", WriteToString(a[1])))
		}
		if n > len(items) {
			panic(errf("drop", "the list has only %d elements", len(items)))
		}
		return listOf(items[n:]), nil
	}, lib)

	m.defSimple("take-right", 2, 2, func(a []Value) (Value, error) {
		n := wantIndex("take-right", a[0])
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("take-right", "expected a proper list but got %s", WriteToString(a[1])))
		}
		if n > len(items) {
			panic(errf("take-right", "the list has only %d elements", len(items)))
		}
		return listOf(items[len(items)-n:]), nil
	}, lib)

	m.defSimple("drop-right", 2, 2, func(a []Value) (Value, error) {
		n := wantIndex("drop-right", a[0])
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("drop-right", "expected a proper list but got %s", WriteToString(a[1])))
		}
		if n > len(items) {
			panic(errf("drop-right", "the list has only %d elements", len(items)))
		}
		return listOf(items[:len(items)-n]), nil
	}, lib)

	// (split-at n list) returns the two halves as two values.
	m.defSimple("split-at", 2, 2, func(a []Value) (Value, error) {
		n := wantIndex("split-at", a[0])
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("split-at", "expected a proper list but got %s", WriteToString(a[1])))
		}
		if n > len(items) {
			panic(errf("split-at", "the list has only %d elements", len(items)))
		}
		return valuesOf(listOf(items[:n]), listOf(items[n:])), nil
	}, lib)

	// (list-index pred list) is the index of the first element the predicate
	// accepts, or #f.  A builtin predicate is applied without a Scheme call.
	m.defSimple("list-index", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("list-index", a[0])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("list-index", "expected a proper list but got %s", WriteToString(a[1])))
		}
		for i, v := range items {
			if caller.pred(name, v) {
				return Int(int64(i)), nil
			}
		}
		return False, nil
	}, lib)

	// (find pred list) is the first element the predicate accepts, or #f.
	m.defSimple("find", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("find", a[0])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("find", "expected a proper list but got %s", WriteToString(a[1])))
		}
		for _, v := range items {
			if caller.pred(name, v) {
				return v, nil
			}
		}
		return False, nil
	}, lib)

	// (filter-not pred seq) keeps the elements the predicate rejects.
	m.defSimple("filter-not", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("filter-not", a[0])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		var out listBuilder
		forEachIn("filter-not", a[1], func(v Value) bool {
			if !caller.pred(name, v) {
				out.add(v)
			}
			return true
		})
		return out.list(), nil
	}, lib)

	// (delete x seq [equal?]) removes every element equal to x.
	m.defSimple("delete", 2, 3, func(a []Value) (Value, error) {
		cmp := builtinProc(m, "equal?")
		if len(a) == 3 {
			cmp = wantProcedure("delete", a[2])
		}
		name := builtinName(cmp)
		caller := newFastCaller(m, cmp)
		var out listBuilder
		forEachIn("delete", a[1], func(v Value) bool {
			if !caller.less(name, v, a[0]) {
				out.add(v)
			}
			return true
		})
		return out.list(), nil
	}, lib)

	// (delete-duplicates list [equal?]) keeps the first of each group of equal
	// elements, in order.  The map is keyed by printed form, which is only a
	// sound shortcut for equal?, eqv? and eq?: those are exactly the
	// comparisons under which two atoms with different printed forms are never
	// equal.  Anything else (a procedure of your own, =, string-ci=?) falls
	// back to the linear scan that defines the procedure.
	m.defSimple("delete-duplicates", 1, 2, func(a []Value) (Value, error) {
		items, ok := ListToSlice(a[0])
		if !ok {
			panic(errf("delete-duplicates", "expected a proper list but got %s", WriteToString(a[0])))
		}
		cmp := builtinProc(m, "equal?")
		if len(a) == 2 {
			cmp = wantProcedure("delete-duplicates", a[1])
		}
		name := builtinName(cmp)
		useMap := name == "equal?" || name == "eqv?" || name == "eq?"
		caller := newFastCaller(m, cmp)
		seen := make(map[string][]Value)
		out := make([]Value, 0, len(items))
		linear := func(v Value) bool {
			for _, prev := range out {
				if caller.less(name, prev, v) {
					return true
				}
			}
			return false
		}
		for _, v := range items {
			key, isAtom := atomKey(v)
			if !isAtom || !useMap {
				if !linear(v) {
					out = append(out, v)
				}
				continue
			}
			dup := false
			for _, prev := range seen[key] {
				if caller.less(name, prev, v) {
					dup = true
					break
				}
			}
			if !dup {
				seen[key] = append(seen[key], v)
				out = append(out, v)
			}
		}
		return listOf(out), nil
	}, lib)

	// (partition pred list) returns the matching elements and the rest, in
	// order, as two values.
	m.defSimple("partition", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("partition", a[0])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("partition", "expected a proper list but got %s", WriteToString(a[1])))
		}
		yes := make([]Value, 0, len(items))
		no := make([]Value, 0, len(items))
		for _, v := range items {
			if caller.pred(name, v) {
				yes = append(yes, v)
			} else {
				no = append(no, v)
			}
		}
		return valuesOf(listOf(yes), listOf(no)), nil
	}, lib)

	// (flatten seq) is a list of the atoms in a nested structure, in order.
	m.defSimple("flatten", 1, 1, func(a []Value) (Value, error) {
		var out listBuilder
		var walk func(v Value, depth int)
		walk = func(v Value, depth int) {
			if depth > 10000 {
				panic(errf("flatten", "the structure is nested too deeply (a circular one?)"))
			}
			switch x := v.(type) {
			case *Pair:
				walk(x.Car, depth+1)
				walk(x.Cdr, depth+1)
			case Empty:
			case *Vector:
				for _, e := range x.Items {
					walk(e, depth+1)
				}
			default:
				out.add(v)
			}
		}
		walk(a[0], 0)
		return out.list(), nil
	}, lib)

	// (zip list ...) is a list of the rows of several sequences, stopping at
	// the shortest one; (unzip rows) transposes them back.
	m.defSimple("zip", 1, 32, func(a []Value) (Value, error) {
		cols := make([][]Value, len(a))
		n := -1
		for i, v := range a {
			cols[i] = seqSlice("zip", v)
			if n < 0 || len(cols[i]) < n {
				n = len(cols[i])
			}
		}
		out := make([]Value, n)
		for i := 0; i < n; i++ {
			row := make([]Value, len(cols))
			for j := range cols {
				row[j] = cols[j][i]
			}
			out[i] = listOf(row)
		}
		return listOf(out), nil
	}, lib)

	m.defSimple("unzip", 1, 1, func(a []Value) (Value, error) {
		rows := seqSlice("unzip", a[0])
		table := make([][]Value, len(rows))
		width := -1
		for i, r := range rows {
			table[i] = seqSlice("unzip", r)
			if width < 0 || len(table[i]) < width {
				width = len(table[i])
			}
		}
		if width < 0 {
			return Nil, nil
		}
		out := make([]Value, width)
		for c := 0; c < width; c++ {
			col := make([]Value, len(table))
			for i := range table {
				col[i] = table[i][c]
			}
			out[c] = listOf(col)
		}
		return listOf(out), nil
	}, lib)

	// (chunk list n) cuts a list into lists of n elements, the last one short.
	m.defSimple("chunk", 2, 2, func(a []Value) (Value, error) {
		items, ok := ListToSlice(a[0])
		if !ok {
			panic(errf("chunk", "expected a proper list but got %s", WriteToString(a[0])))
		}
		n := wantIndex("chunk", a[1])
		if n < 1 {
			panic(errf("chunk", "the chunk size must be at least 1"))
		}
		var out []Value
		for i := 0; i < len(items); i += n {
			j := i + n
			if j > len(items) {
				j = len(items)
			}
			out = append(out, listOf(items[i:j]))
		}
		return listOf(out), nil
	}, lib)

	// (assoc-set alist key value [equal?]) returns a new association list with
	// the entry for key replaced in place, or appended when it is missing.
	m.defSimple("assoc-set", 3, 4, func(a []Value) (Value, error) {
		cmp := builtinProc(m, "equal?")
		if len(a) == 4 {
			cmp = wantProcedure("assoc-set", a[3])
		}
		name := builtinName(cmp)
		caller := newFastCaller(m, cmp)
		key, val := a[1], a[2]
		var head, tail *Pair
		found := false
		forEachIn("assoc-set", a[0], func(entry Value) bool {
			p, ok := entry.(*Pair)
			if !ok {
				panic(errf("assoc-set", "expected an association list but got %s", WriteToString(entry)))
			}
			if !found && caller.less(name, p.Car, key) {
				found = true
				entry = Cons(key, val)
			}
			cell := &Pair{Car: entry, Cdr: Nil}
			if head == nil {
				head = cell
			} else {
				tail.Cdr = cell
			}
			tail = cell
			return true
		})
		if !found {
			cell := &Pair{Car: Cons(key, val), Cdr: Nil}
			if head == nil {
				return cell, nil
			}
			tail.Cdr = cell
		}
		return head, nil
	}, lib)

	// (fold-left proc init seq) and (fold-right proc init seq) know the
	// arithmetic builtins, so the common folds never leave Go.
	m.defSimple("fold-left", 3, 3, func(a []Value) (Value, error) {
		proc := wantProcedure("fold-left", a[0])
		name := builtinName(proc)
		caller := newFastCaller(m, proc)
		acc := a[1]
		forEachIn("fold-left", a[2], func(v Value) bool {
			if res, ok := foldOp(name, acc, v); ok {
				acc = res
			} else {
				acc = caller.apply(acc, v)
			}
			return true
		})
		return acc, nil
	}, lib)

	m.defSimple("fold-right", 3, 3, func(a []Value) (Value, error) {
		proc := wantProcedure("fold-right", a[0])
		name := builtinName(proc)
		caller := newFastCaller(m, proc)
		acc := a[1]
		items := seqSlice("fold-right", a[2])
		for i := len(items) - 1; i >= 0; i-- {
			v := items[i]
			if res, ok := foldOp(name, v, acc); ok {
				acc = res
			} else {
				acc = caller.apply(v, acc)
			}
		}
		return acc, nil
	}, lib)

	// (sort-by key list [less?]) sorts by a key computed once per element,
	// rather than by calling the comparison on every pair.
	m.defSimple("sort-by", 2, 3, func(a []Value) (Value, error) {
		key := wantProcedure("sort-by", a[0])
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("sort-by", "expected a proper list but got %s", WriteToString(a[1])))
		}
		less := defaultLess(m)
		if len(a) == 3 {
			less = wantProcedure("sort-by", a[2])
		}
		return listOf(sortByKey(m, key, items, less)), nil
	}, lib)

	// (vector-sort-by key vector [less?]) is the same for a vector.
	m.defSimple("vector-sort-by", 2, 3, func(a []Value) (Value, error) {
		key := wantProcedure("vector-sort-by", a[0])
		vec := wantVector("vector-sort-by", a[1])
		less := defaultLess(m)
		if len(a) == 3 {
			less = wantProcedure("vector-sort-by", a[2])
		}
		return &Vector{Items: sortByKey(m, key, vec.Items, less)}, nil
	}, lib)
}

// sortByKey decorates, sorts and undecorates.  The key procedure runs once per
// element, which is what makes this cheaper than a comparison that computes it
// on both sides of every comparison.
func sortByKey(m *Machine, key Value, items []Value, less Value) []Value {
	type keyed struct{ key, val Value }
	decorated := make([]keyed, len(items))
	caller := newFastCaller(m, key)
	for i, v := range items {
		decorated[i] = keyed{key: caller.apply(v), val: v}
	}
	lessName := builtinName(less)
	cmp := newFastCaller(m, less)
	sort.SliceStable(decorated, func(i, j int) bool {
		return cmp.less(lessName, decorated[i].key, decorated[j].key)
	})
	out := make([]Value, len(decorated))
	for i, d := range decorated {
		out[i] = d.val
	}
	return out
}

// foldOp applies one of the arithmetic builtins directly.  The second result
// reports whether the name is one of them.
func foldOp(name string, acc, v Value) (Value, bool) {
	switch name {
	case "+":
		return NumAdd(acc, v), true
	case "-":
		return NumSub(acc, v), true
	case "*":
		return NumMul(acc, v), true
	case "/":
		return NumDiv(acc, v), true
	case "max":
		if NumCmp(v, acc) > 0 {
			return v, true
		}
		return acc, true
	case "min":
		if NumCmp(v, acc) < 0 {
			return v, true
		}
		return acc, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Vectors
// ---------------------------------------------------------------------------

func installFastVectors(m *Machine, lib string) {
	// (vector-iota count [start [step]]) is the vector version of iota.
	m.defSimple("vector-iota", 1, 3, func(a []Value) (Value, error) {
		count := wantIndex("vector-iota", a[0])
		start := Value(Int(0))
		if len(a) > 1 {
			start = wantNumber("vector-iota", a[1])
		}
		step := Value(Int(1))
		if len(a) > 2 {
			step = wantNumber("vector-iota", a[2])
		}
		out := make([]Value, count)
		cur := start
		for i := 0; i < count; i++ {
			out[i] = cur
			cur = NumAdd(cur, step)
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (range start end [step]) is a list of numbers, end exclusive.
	m.defSimple("range", 2, 3, func(a []Value) (Value, error) {
		start := wantNumber("range", a[0])
		end := wantNumber("range", a[1])
		step := Value(Int(1))
		if len(a) == 3 {
			step = wantNumber("range", a[2])
		}
		sign := NumSign(step)
		if sign == 0 {
			panic(errf("range", "the step must not be zero"))
		}
		var out []Value
		cur := start
		for {
			c := NumCmp(cur, end)
			if sign > 0 && c >= 0 {
				break
			}
			if sign < 0 && c <= 0 {
				break
			}
			out = append(out, cur)
			cur = NumAdd(cur, step)
		}
		return listOf(out), nil
	}, lib)

	// (vector-take n v) and (vector-drop n v) are new vectors.
	m.defSimple("vector-take", 2, 2, func(a []Value) (Value, error) {
		n := wantIndex("vector-take", a[0])
		items := seqSlice("vector-take", a[1])
		if n > len(items) {
			panic(errf("vector-take", "the sequence has only %d elements", len(items)))
		}
		return &Vector{Items: append([]Value(nil), items[:n]...)}, nil
	}, lib)

	m.defSimple("vector-drop", 2, 2, func(a []Value) (Value, error) {
		n := wantIndex("vector-drop", a[0])
		items := seqSlice("vector-drop", a[1])
		if n > len(items) {
			panic(errf("vector-drop", "the sequence has only %d elements", len(items)))
		}
		return &Vector{Items: append([]Value(nil), items[n:]...)}, nil
	}, lib)

	// (vector-concat list-of-vectors) is one vector holding all of them.
	m.defSimple("vector-concat", 1, 1, func(a []Value) (Value, error) {
		parts := seqSlice("vector-concat", a[0])
		total := 0
		vecs := make([]*Vector, len(parts))
		for i, p := range parts {
			v, ok := p.(*Vector)
			if !ok {
				panic(errf("vector-concat", "expected a vector but got %s", WriteToString(p)))
			}
			vecs[i] = v
			total += len(v.Items)
		}
		out := make([]Value, 0, total)
		for _, v := range vecs {
			out = append(out, v.Items...)
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (vector-add a b), (vector-sub a b) and (vector-scale v k) work element
	// by element, in Go.
	m.defSimple("vector-add", 2, 2, func(a []Value) (Value, error) {
		x, y := zipVectors("vector-add", a[0], a[1])
		out := make([]Value, len(x))
		for i := range x {
			out[i] = NumAdd(x[i], y[i])
		}
		return &Vector{Items: out}, nil
	}, lib)

	m.defSimple("vector-sub", 2, 2, func(a []Value) (Value, error) {
		x, y := zipVectors("vector-sub", a[0], a[1])
		out := make([]Value, len(x))
		for i := range x {
			out[i] = NumSub(x[i], y[i])
		}
		return &Vector{Items: out}, nil
	}, lib)

	m.defSimple("vector-scale", 2, 2, func(a []Value) (Value, error) {
		vec := wantVector("vector-scale", a[0])
		k := wantNumber("vector-scale", a[1])
		out := make([]Value, len(vec.Items))
		for i, v := range vec.Items {
			out[i] = NumMul(wantNumber("vector-scale", v), k)
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (vector-norm v) is the Euclidean norm, the square root of the dot
	// product with itself.
	m.defSimple("vector-norm", 1, 1, func(a []Value) (Value, error) {
		vec := wantVector("vector-norm", a[0])
		sum := 0.0
		for _, v := range vec.Items {
			f := asFloat(wantNumber("vector-norm", v))
			sum += f * f
		}
		return Float(math.Sqrt(sum)), nil
	}, lib)

	// (vector-argmin v [less?]) and (vector-argmax v [less?]) are the indices
	// of the first smallest and largest elements.
	m.defSimple("vector-argmin", 1, 2, func(a []Value) (Value, error) {
		return vectorArg("vector-argmin", m, a, -1)
	}, lib)

	m.defSimple("vector-argmax", 1, 2, func(a []Value) (Value, error) {
		return vectorArg("vector-argmax", m, a, 1)
	}, lib)

	// (vector-index-of v x [equal?]) is the index of the first element equal to
	// x, or #f.
	m.defSimple("vector-index-of", 2, 3, func(a []Value) (Value, error) {
		vec := wantVector("vector-index-of", a[0])
		cmp := builtinProc(m, "equal?")
		if len(a) == 3 {
			cmp = wantProcedure("vector-index-of", a[2])
		}
		name := builtinName(cmp)
		caller := newFastCaller(m, cmp)
		for i, v := range vec.Items {
			if caller.less(name, v, a[1]) {
				return Int(int64(i)), nil
			}
		}
		return False, nil
	}, lib)

	// (vector-chunk v n) cuts a vector into vectors of n elements.
	m.defSimple("vector-chunk", 2, 2, func(a []Value) (Value, error) {
		vec := wantVector("vector-chunk", a[0])
		n := wantIndex("vector-chunk", a[1])
		if n < 1 {
			panic(errf("vector-chunk", "the chunk size must be at least 1"))
		}
		var out []Value
		for i := 0; i < len(vec.Items); i += n {
			j := i + n
			if j > len(vec.Items) {
				j = len(vec.Items)
			}
			out = append(out, &Vector{Items: append([]Value(nil), vec.Items[i:j]...)})
		}
		return listOf(out), nil
	}, lib)

	// (vector-partition pred v) returns the accepted elements and the rest, as
	// two new vectors.
	m.defSimple("vector-partition", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("vector-partition", a[0])
		vec := wantVector("vector-partition", a[1])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		yes := make([]Value, 0, len(vec.Items))
		no := make([]Value, 0, len(vec.Items))
		for _, v := range vec.Items {
			if caller.pred(name, v) {
				yes = append(yes, v)
			} else {
				no = append(no, v)
			}
		}
		return valuesOf(&Vector{Items: yes}, &Vector{Items: no}), nil
	}, lib)

	// (vector-binary-search-insert v key [less?]) is the index where key would
	// be inserted to keep the vector sorted: the lower bound.
	m.defSimple("vector-binary-search-insert", 2, 3, func(a []Value) (Value, error) {
		vec := wantVector("vector-binary-search-insert", a[0])
		key := a[1]
		less := defaultLess(m)
		if len(a) == 3 {
			less = wantProcedure("vector-binary-search-insert", a[2])
		}
		name := builtinName(less)
		caller := newFastCaller(m, less)
		lo, hi := 0, len(vec.Items)
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			if caller.less(name, vec.Items[mid], key) {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return Int(int64(lo)), nil
	}, lib)
}

// zipVectors checks that two vectors have the same length and returns their
// elements.
func zipVectors(name string, a, b Value) ([]Value, []Value) {
	x := wantVector(name, a)
	y := wantVector(name, b)
	if len(x.Items) != len(y.Items) {
		panic(errf(name, "vectors of different lengths"))
	}
	return x.Items, y.Items
}

// vectorArg finds the index of the extreme element: dir is -1 for the smallest
// and 1 for the largest, and ties keep the first.
func vectorArg(name string, m *Machine, a []Value, dir int) (Value, error) {
	vec := wantVector(name, a[0])
	if len(vec.Items) == 0 {
		panic(errf(name, "expected a non-empty vector"))
	}
	less := defaultLess(m)
	if len(a) == 2 {
		less = wantProcedure(name, a[1])
	}
	lessName := builtinName(less)
	caller := newFastCaller(m, less)
	best := 0
	for i := 1; i < len(vec.Items); i++ {
		// The candidate wins when it is strictly before the best in the
		// direction we are looking.
		wins := caller.less(lessName, vec.Items[i], vec.Items[best])
		if dir > 0 {
			wins = caller.less(lessName, vec.Items[best], vec.Items[i])
		}
		if wins {
			best = i
		}
	}
	return Int(int64(best)), nil
}
