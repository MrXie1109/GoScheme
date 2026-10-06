// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// SRFI-133, the vector library, as the (srfi 133) builtin library.
//
// R7RS already provides vector, make-vector, vector?, vector-length,
// vector-ref, vector-copy, vector-copy!, vector-append, vector-fill!,
// vector-map, vector-for-each, vector->list, list->vector, vector->string and
// string->vector with the same meaning SRFI-133 gives them, so (srfi 133)
// exports those bindings rather than copies.  The names it shares with
// (goscheme fast) — vector-swap!, vector-reverse!, vector-binary-search and
// vector-partition — are handled the same way, with the fast procedure widened
// where SRFI-133 specifies more.

const srfi133Lib = "(srfi 133)"

func installSRFI133(m *Machine) {
	const lib = srfi133Lib

	// ------------------------------------------------------------ from R7RS
	m.aliasAll(lib,
		"vector", "make-vector", "vector?", "vector-length", "vector-ref",
		"vector-copy", "vector-copy!", "vector-append", "vector-fill!",
		"vector-map", "vector-for-each", "vector->list", "list->vector",
		"vector->string", "string->vector")
	// ... and from (goscheme fast).
	m.aliasAll(lib, "vector-swap!", "vector-reverse!", "vector-binary-search")

	// ------------------------------------------------------------ predicates
	// (vector-empty? v) is #t for a zero-length vector.
	m.defSimple("vector-empty?", 1, 1, func(a []Value) (Value, error) {
		vec := wantVector("vector-empty?", a[0])
		return BooleanOf(len(vec.Items) == 0), nil
	}, lib)

	// (vector= elt= v ...) compares the vectors element by element; different
	// lengths are #f rather than an error.
	m.defSimple("vector=", 2, -1, func(a []Value) (Value, error) {
		eq := wantProcedure("vector=", a[0])
		eqName := builtinName(eq)
		caller := newFastCaller(m, eq)
		first := wantVector("vector=", a[1])
		for _, other := range a[2:] {
			vec := wantVector("vector=", other)
			if len(vec.Items) != len(first.Items) {
				return False, nil
			}
			for i := range vec.Items {
				if !caller.less(eqName, first.Items[i], vec.Items[i]) {
					return False, nil
				}
			}
		}
		return True, nil
	}, lib)

	// ------------------------------------------------------------ selectors
	// (subvector v start end) is a new vector holding that half-open range.
	m.defSimple("subvector", 3, 3, func(a []Value) (Value, error) {
		vec := wantVector("subvector", a[0])
		start, end := wantSliceRange("subvector", a[1], a[2], len(vec.Items))
		return &Vector{Items: append([]Value(nil), vec.Items[start:end]...)}, nil
	}, lib)

	// (reverse-list->vector list) is (list->vector (reverse list)).
	m.defSimple("reverse-list->vector", 1, 1, func(a []Value) (Value, error) {
		items, ok := ListToSlice(a[0])
		if !ok {
			panic(errf("reverse-list->vector", "expected a proper list but got %s", WriteToString(a[0])))
		}
		out := make([]Value, len(items))
		for i, v := range items {
			out[len(items)-1-i] = v
		}
		return &Vector{Items: out}, nil
	}, lib)

	// ------------------------------------------------------- constructors
	// (vector-unfold f length seed ...) calls f with the index and the seeds
	// and expects it to return the element followed by the new seeds.
	m.defSimple("vector-unfold", 2, -1, func(a []Value) (Value, error) {
		return vectorUnfold(m, "vector-unfold", a, false)
	}, lib)

	m.defSimple("vector-unfold-right", 2, -1, func(a []Value) (Value, error) {
		return vectorUnfold(m, "vector-unfold-right", a, true)
	}, lib)

	// (vector-reverse-copy v [start [end]]) copies the range reversed.
	m.defSimple("vector-reverse-copy", 1, 3, func(a []Value) (Value, error) {
		vec := wantVector("vector-reverse-copy", a[0])
		start, end := optionalSliceRange("vector-reverse-copy", a, len(vec.Items))
		out := make([]Value, end-start)
		for i := start; i < end; i++ {
			out[end-1-i] = vec.Items[i]
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (vector-concatenate list-of-vectors) is one vector holding all of them.
	m.defSimple("vector-concatenate", 1, 1, func(a []Value) (Value, error) {
		parts := seqSlice("vector-concatenate", a[0])
		var out []Value
		for _, p := range parts {
			out = append(out, wantVector("vector-concatenate", p).Items...)
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (vector-append-subvectors v1 start1 end1 ...) concatenates ranges.
	m.defSimple("vector-append-subvectors", 1, -1, func(a []Value) (Value, error) {
		if len(a) == 0 || len(a)%3 != 0 {
			panic(errf("vector-append-subvectors", "expected a vector and a start and end for each of them"))
		}
		var out []Value
		for i := 0; i < len(a); i += 3 {
			vec := wantVector("vector-append-subvectors", a[i])
			start, end := wantSliceRange("vector-append-subvectors", a[i+1], a[i+2], len(vec.Items))
			out = append(out, vec.Items[start:end]...)
		}
		return &Vector{Items: out}, nil
	}, lib)

	// -------------------------------------------------------------- iteration
	// (vector-fold kons knil vec ...) calls (kons state e1 e2 ...) from the
	// left; the state comes first, which is the opposite order to SRFI-1's
	// list fold.
	m.defSimple("vector-fold", 3, -1, func(a []Value) (Value, error) {
		return vectorFold(m, "vector-fold", a, false)
	}, lib)

	// (vector-fold-right kons knil vec ...) calls (kons e1 e2 ... state) from
	// the right, so the state comes last.
	m.defSimple("vector-fold-right", 3, -1, func(a []Value) (Value, error) {
		return vectorFold(m, "vector-fold-right", a, true)
	}, lib)

	// (vector-map! proc vec1 vec2 ...) stores (proc e1 e2 ...) back into vec1
	// and returns it.
	m.defSimple("vector-map!", 2, -1, func(a []Value) (Value, error) {
		proc := wantProcedure("vector-map!", a[0])
		target := wantVector("vector-map!", a[1])
		rest := make([][]Value, len(a)-1)
		for i, v := range a[1:] {
			rest[i] = wantVector("vector-map!", v).Items
			if len(rest[i]) != len(target.Items) {
				panic(errf("vector-map!", "vectors of different lengths"))
			}
		}
		caller := newFastCaller(m, proc)
		for i := range target.Items {
			args := make([]Value, 0, len(rest))
			for _, items := range rest {
				args = append(args, items[i])
			}
			target.Items[i] = caller.apply(args...)
		}
		return target, nil
	}, lib)

	// (vector-count pred vec ...) counts the positions where every element
	// passes.
	m.defSimple("vector-count", 2, -1, func(a []Value) (Value, error) {
		return vectorPredIndex(m, "vector-count", a, true, false, false)
	}, lib)

	// -------------------------------------------------------------- searching
	m.defSimple("vector-index", 2, -1, func(a []Value) (Value, error) {
		return vectorPredIndex(m, "vector-index", a, false, false, false)
	}, lib)

	m.defSimple("vector-index-right", 2, -1, func(a []Value) (Value, error) {
		return vectorPredIndex(m, "vector-index-right", a, false, true, false)
	}, lib)

	m.defSimple("vector-skip", 2, -1, func(a []Value) (Value, error) {
		return vectorPredIndex(m, "vector-skip", a, false, false, true)
	}, lib)

	m.defSimple("vector-skip-right", 2, -1, func(a []Value) (Value, error) {
		return vectorPredIndex(m, "vector-skip-right", a, false, true, true)
	}, lib)

	// (vector-any pred vec ...) is the first true value the predicate
	// returns, and (vector-every pred vec ...) the last one, or #f; both take
	// the elements of a position in one call.
	m.defSimple("vector-any", 2, -1, func(a []Value) (Value, error) {
		return vectorAnyEvery(m, "vector-any", a, false)
	}, lib)

	m.defSimple("vector-every", 2, -1, func(a []Value) (Value, error) {
		return vectorAnyEvery(m, "vector-every", a, true)
	}, lib)

	// (vector-partition pred vec) returns two values: a new vector holding the
	// accepted elements followed by the rejected ones, and the number that
	// were accepted.  This is SRFI-133's shape, and the (goscheme fast)
	// library exports the same binding.
	m.defSimple("vector-partition", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("vector-partition", a[0])
		vec := wantVector("vector-partition", a[1])
		predName := builtinName(pred)
		caller := newFastCaller(m, pred)
		out := make([]Value, len(vec.Items))
		kept := 0
		for _, v := range vec.Items {
			if caller.pred(predName, v) {
				out[kept] = v
				kept++
			}
		}
		rest := kept
		for _, v := range vec.Items {
			if !caller.pred(predName, v) {
				out[rest] = v
				rest++
			}
		}
		return &MultipleValues{Values: []Value{&Vector{Items: out}, Int(int64(kept))}}, nil
	}, lib, "(goscheme fast)")
}

// wantSliceRange validates a range given as two exact integers.
func wantSliceRange(name string, startVal, endVal Value, length int) (int, int) {
	start := wantIndex(name, startVal)
	end := wantIndex(name, endVal)
	if start > end || end > length {
		panic(errf(name, "the range %d..%d is out of bounds for a vector of %d elements", start, end, length))
	}
	return start, end
}

// optionalSliceRange reads an optional start and end, defaulting to the whole
// vector.
func optionalSliceRange(name string, a []Value, length int) (int, int) {
	start, end := 0, length
	if len(a) > 1 {
		start = wantIndex(name, a[1])
	}
	if len(a) > 2 {
		end = wantIndex(name, a[2])
	}
	if start > end || end > length {
		panic(errf(name, "the range %d..%d is out of bounds for a vector of %d elements", start, end, length))
	}
	return start, end
}

// vectorUnfold is the shared body of vector-unfold and vector-unfold-right.
// The procedure returns several values: the element and the next seeds.
func vectorUnfold(m *Machine, name string, a []Value, right bool) (Value, error) {
	f := wantProcedure(name, a[0])
	length := wantIndex(name, a[1])
	seeds := append([]Value(nil), a[2:]...)
	out := make([]Value, length)
	caller := newFastCaller(m, f)
	for step := 0; step < length; step++ {
		index := step
		if right {
			index = length - 1 - step
		}
		args := make([]Value, 0, len(seeds)+1)
		args = append(args, Int(int64(index)))
		args = append(args, seeds...)
		res := caller.apply(args...)
		// With no seeds there is nothing to thread, and a procedure that
		// returns one value returns it plainly rather than through values.
		values := []Value{res}
		if mv, ok := res.(*MultipleValues); ok {
			values = mv.Values
		}
		if len(values) != len(seeds)+1 {
			panic(errf(name, "the procedure returned %d values but %d are needed", len(values), len(seeds)+1))
		}
		out[index] = values[0]
		copy(seeds, values[1:])
	}
	return &Vector{Items: out}, nil
}

// vectorFold is the shared body of vector-fold and vector-fold-right.
func vectorFold(m *Machine, name string, a []Value, right bool) (Value, error) {
	kons := wantProcedure(name, a[0])
	acc := a[1]
	rows := make([][]Value, len(a)-2)
	length := -1
	for i, v := range a[2:] {
		rows[i] = wantVector(name, v).Items
		if length < 0 {
			length = len(rows[i])
		} else if len(rows[i]) != length {
			panic(errf(name, "vectors of different lengths"))
		}
	}
	caller := newFastCaller(m, kons)
	for step := 0; step < length; step++ {
		i := step
		if right {
			i = length - 1 - step
		}
		args := make([]Value, 0, len(rows)+1)
		if !right {
			args = append(args, acc)
		}
		for _, row := range rows {
			args = append(args, row[i])
		}
		if right {
			args = append(args, acc)
		}
		acc = caller.apply(args...)
	}
	return acc, nil
}

// vectorPredIndex is the shared body of vector-count, vector-index,
// vector-index-right, vector-skip and vector-skip-right.
//
// countAll scans the whole vector and counts; otherwise the first match is
// returned.  right scans from the other end.  skip inverts the test, so the
// procedure finds the first element that *fails* the predicate.
//
// skip is a parameter rather than a comparison against name.  Deriving it from
// the name would make a rename change what the procedure does, silently and
// with no test able to see it; every caller says which behaviour it wants, and
// the two booleans beside it already worked that way.
func vectorPredIndex(m *Machine, name string, a []Value, countAll, right, skip bool) (Value, error) {
	pred := wantProcedure(name, a[0])
	predName := builtinName(pred)
	caller := newFastCaller(m, pred)
	rows := make([][]Value, len(a)-1)
	length := -1
	for i, v := range a[1:] {
		rows[i] = wantVector(name, v).Items
		if length < 0 {
			length = len(rows[i])
		} else if len(rows[i]) != length {
			panic(errf(name, "vectors of different lengths"))
		}
	}
	matches := func(i int) bool {
		args := make([]Value, 0, len(rows))
		for _, row := range rows {
			args = append(args, row[i])
		}
		pass := false
		if len(args) == 1 {
			pass = caller.pred(predName, args[0])
		} else {
			pass = IsTrue(caller.apply(args...))
		}
		return pass != skip
	}
	found := int64(0)
	for step := 0; step < length; step++ {
		i := step
		if right {
			i = length - 1 - step
		}
		if matches(i) {
			if !countAll {
				return Int(int64(i)), nil
			}
			found++
		}
	}
	if countAll {
		return Int(found), nil
	}
	return False, nil
}

// vectorAnyEvery is the shared body of vector-any and vector-every.
func vectorAnyEvery(m *Machine, name string, a []Value, all bool) (Value, error) {
	pred := wantProcedure(name, a[0])
	predName := builtinName(pred)
	caller := newFastCaller(m, pred)
	rows := make([][]Value, len(a)-1)
	length := -1
	for i, v := range a[1:] {
		rows[i] = wantVector(name, v).Items
		if length < 0 {
			length = len(rows[i])
		} else if len(rows[i]) != length {
			panic(errf(name, "vectors of different lengths"))
		}
	}
	var last Value = True
	for i := 0; i < length; i++ {
		args := make([]Value, 0, len(rows))
		for _, row := range rows {
			args = append(args, row[i])
		}
		var res Value
		var truth bool
		if len(args) == 1 && predName != "" {
			if quick, ok := fastPred(predName, args[0]); ok {
				res, truth = BooleanOf(quick), quick
			} else {
				res = caller.apply(args...)
				truth = IsTrue(res)
			}
		} else {
			res = caller.apply(args...)
			truth = IsTrue(res)
		}
		if all && !truth {
			return False, nil
		}
		if !all && truth {
			return res, nil
		}
		last = res
	}
	if !all {
		return False, nil
	}
	return last, nil
}

func init() { registerInstaller(installSRFI133) }
