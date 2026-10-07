// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// SRFI-1, the list library, as the (srfi 1) builtin library.
//
// SRFI-1 and (goscheme fast) overlap in sixteen procedures.  Rather than keep
// two implementations that could drift apart — or worse, disagree depending on
// which import came last, since this implementation lets a later import shadow
// an earlier one — the overlapping names are registered once and exported from
// both libraries, and the predicates and comparisons run through the same fast
// lane (a builtin predicate is applied in Go, a written one is called once per
// element).
//
// The linear-update procedures (the ones with a trailing !) are allowed to be
// pure by SRFI-1, so most are aliases of their pure counterparts; reverse! and
// append! really do reuse the pairs, because that is cheap to do correctly.

const srfi1Lib = "(srfi 1)"

// alias exports a binding that is already registered from another library, so
// that both libraries denote the same location.
func (m *Machine) alias(lib, name string) {
	v, ok := m.Builtin.Lookup(Intern(name))
	if !ok {
		panic("goscheme: " + lib + " wants to export " + name + ", which is not defined")
	}
	m.defValue(name, v, lib)
}

// aliasAs exports the binding of from under the name to.  SRFI-1's
// linear-update procedures are allowed to be pure, so most of them are their
// pure counterpart under a different name.
func (m *Machine) aliasAs(lib, to, from string) {
	v, ok := m.Builtin.Lookup(Intern(from))
	if !ok {
		panic("goscheme: " + lib + " wants to export " + to + " as " + from + ", which is not defined")
	}
	m.defValue(to, v, lib)
}

// aliasAll is alias for a list of names.
func (m *Machine) aliasAll(lib string, names ...string) {
	for _, n := range names {
		m.alias(lib, n)
	}
}

// srfiRows turns the list arguments of a variadic procedure into slices.  SRFI-1
// requires the lists to have the same length, so a mismatch is an error rather
// than a silent stop at the shortest.
// srfiRows reads one or more sequences into rows of equal length, so that the
// caller can walk them by position.
//
// A vector is a sequence here as much as a list is: SRFI-1's procedures take
// lists, but the compiler's own SRFI-133 lanes take vectors, and both go through
// this.  Accepting a vector here is what lets the two share one scan loop —
// before, `srfiPredIndex` and `anyEvery` each carried an inline copy of the loop
// for a single vector beside the general one for lists, and the two had to be
// kept in step by hand.
func srfiRows(name string, lists []Value) [][]Value {
	rows := make([][]Value, len(lists))
	n := -1
	for i, l := range lists {
		var items []Value
		switch seq := l.(type) {
		case *Vector:
			items = seq.Items
		default:
			var ok bool
			items, ok = ListToSlice(l)
			if !ok {
				panic(errf(name, "expected a proper list but got %s", WriteToString(l)))
			}
		}
		if n < 0 {
			n = len(items)
		} else if len(items) != n {
			panic(errf(name, "expected lists of the same length"))
		}
		rows[i] = items
	}
	return rows
}

// foldOpElem is the fold lane for builtins in SRFI-1's argument order, which is
// (element accumulator) rather than the (accumulator element) of R6RS fold-left.
func foldOpElem(name string, elem, acc Value) (Value, bool) {
	switch name {
	case "+":
		return NumAdd(elem, acc), true
	case "-":
		return NumSub(elem, acc), true
	case "*":
		return NumMul(elem, acc), true
	case "/":
		return NumDiv(elem, acc), true
	case "max":
		if NumCmp(elem, acc) > 0 {
			return elem, true
		}
		return acc, true
	case "min":
		if NumCmp(elem, acc) < 0 {
			return elem, true
		}
		return acc, true
	}
	return nil, false
}

func installSRFI1(m *Machine) {
	const lib = srfi1Lib

	// ------------------------------------------------------------------ shared
	// The same bindings (srfi 1) shares with (scheme base) and (goscheme fast).
	// They are the SRFI-1 versions already: member and assoc take an optional
	// comparison, map and for-each are variadic, and the fast procedures below
	// have SRFI-1's semantics.
	m.aliasAll(lib, "map", "for-each", "member", "assoc", "make-list", "list-copy")
	m.aliasAll(lib,
		"iota", "take", "drop", "take-right", "drop-right", "split-at", "last",
		"filter", "partition", "find", "delete", "delete-duplicates", "zip")

	// ------------------------------------------------------------ constructors
	// (xcons d a) is (cons a d): the flipped cons that makes a folder out of a
	// cons.
	m.defSimple("xcons", 2, 2, func(a []Value) (Value, error) {
		return Cons(a[1], a[0]), nil
	}, lib)

	// (cons* x ... tail) is (cons x1 (cons x2 ... tail)); with one argument it
	// is that argument.
	m.defSimple("cons*", 1, -1, func(a []Value) (Value, error) {
		res := a[len(a)-1]
		for i := len(a) - 2; i >= 0; i-- {
			res = Cons(a[i], res)
		}
		return res, nil
	}, lib)

	// (list-tabulate n proc) builds a list of (proc i) for i below n.
	m.defSimple("list-tabulate", 2, 2, func(a []Value) (Value, error) {
		n := wantIndex("list-tabulate", a[0])
		proc := wantProcedure("list-tabulate", a[1])
		caller := newFastCaller(m, proc)
		out := make([]Value, n)
		for i := 0; i < n; i++ {
			out[i] = caller.apply(Int(int64(i)))
		}
		return List(out...), nil
	}, lib)

	// (circular-list x ...) is a list whose last pair points back at the first.
	m.defSimple("circular-list", 1, -1, func(a []Value) (Value, error) {
		items := append([]Value(nil), a...)
		head := &Pair{Car: items[0], Cdr: Nil}
		tail := head
		for _, v := range items[1:] {
			cell := &Pair{Car: v, Cdr: Nil}
			tail.Cdr = cell
			tail = cell
		}
		tail.Cdr = head
		return head, nil
	}, lib)

	// ------------------------------------------------------------- predicates
	// (proper-list? x), (circular-list? x) and (dotted-list? x) partition every
	// value; the walk uses Brent's cycle detection so a circular list is
	// answered rather than followed forever.
	m.defSimple("proper-list?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(ListLength(a[0]) >= 0), nil
	}, lib)

	m.defSimple("circular-list?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(isCircular(a[0])), nil
	}, lib)

	m.defSimple("dotted-list?", 1, 1, func(a []Value) (Value, error) {
		if ListLength(a[0]) >= 0 || isCircular(a[0]) {
			return False, nil
		}
		return True, nil
	}, lib)

	m.defSimple("not-pair?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Pair)
		return BooleanOf(!ok), nil
	}, lib)

	// (null-list? x) is #t for the empty list, #f for a pair, and an error for
	// anything that is not a list at all.
	m.defSimple("null-list?", 1, 1, func(a []Value) (Value, error) {
		switch a[0].(type) {
		case Empty:
			return True, nil
		case *Pair:
			return False, nil
		}
		panic(errf("null-list?", "expected a list but got %s", WriteToString(a[0])))
	}, lib)

	// (length+ x) is the length, or #f for a circular list.
	m.defSimple("length+", 1, 1, func(a []Value) (Value, error) {
		if isCircular(a[0]) {
			return False, nil
		}
		n := ListLength(a[0])
		if n < 0 {
			panic(errf("length+", "expected a list but got %s", WriteToString(a[0])))
		}
		return Int(int64(n)), nil
	}, lib)

	// (count pred list ...) counts the positions where the predicate is true
	// for the elements of every list, and is exported from (goscheme fast) as
	// well.  A single vector is accepted, as the fast sequences are.
	m.defSimple("count", 2, -1, func(a []Value) (Value, error) {
		return srfiPredIndex(m, "count", a, true, false, false)
	}, lib, "(goscheme fast)")

	// (list-index pred list ...) is the first such position, or #f.
	m.defSimple("list-index", 2, -1, func(a []Value) (Value, error) {
		return srfiPredIndex(m, "list-index", a, false, false, false)
	}, lib, "(goscheme fast)")

	// (list= elt= list ...) compares the lists element by element and requires
	// the same length.
	m.defSimple("list=", 2, -1, func(a []Value) (Value, error) {
		eq := wantProcedure("list=", a[0])
		name := builtinName(eq)
		caller := newFastCaller(m, eq)
		// list= answers #f for lists of different lengths; it is the folds and
		// maps that make that an error.
		rows := make([][]Value, len(a)-1)
		for i, l := range a[1:] {
			items, ok := ListToSlice(l)
			if !ok {
				panic(errf("list=", "expected a proper list but got %s", WriteToString(l)))
			}
			rows[i] = items
		}
		for i := 1; i < len(rows); i++ {
			if len(rows[i]) != len(rows[0]) {
				return False, nil
			}
			for j := range rows[0] {
				if !caller.less(name, rows[0][j], rows[i][j]) {
					return False, nil
				}
			}
		}
		return True, nil
	}, lib)

	// -------------------------------------------------------------- selectors
	ordinals := []string{"first", "second", "third", "fourth", "fifth", "sixth",
		"seventh", "eighth", "ninth", "tenth"}
	for i, name := range ordinals {
		index := i
		m.defSimple(name, 1, 1, func(a []Value) (Value, error) {
			items, ok := ListToSlice(a[0])
			if !ok {
				panic(errf(name, "expected a proper list but got %s", WriteToString(a[0])))
			}
			if index >= len(items) {
				panic(errf(name, "the list is shorter than %d elements", index+1))
			}
			return items[index], nil
		}, lib)
	}

	m.defSimple("car+cdr", 1, 1, func(a []Value) (Value, error) {
		p, ok := a[0].(*Pair)
		if !ok {
			panic(errf("car+cdr", "expected a pair but got %s", WriteToString(a[0])))
		}
		return &MultipleValues{Values: []Value{p.Car, p.Cdr}}, nil
	}, lib)

	m.defSimple("last-pair", 1, 1, func(a []Value) (Value, error) {
		p, ok := a[0].(*Pair)
		if !ok {
			panic(errf("last-pair", "expected a non-empty list but got %s", WriteToString(a[0])))
		}
		seen := map[*Pair]bool{}
		for {
			next, ok := p.Cdr.(*Pair)
			if !ok {
				return p, nil
			}
			if seen[next] {
				// A circular list has no last pair; SRFI-1 leaves this an
				// error, and hanging would be worse.
				panic(errf("last-pair", "expected a finite list but got a circular one"))
			}
			seen[p] = true
			p = next
		}
	}, lib)

	// The linear-update procedures are allowed to be pure, so they are their
	// pure counterpart.
	m.aliasAs(lib, "take!", "take")
	m.aliasAs(lib, "drop-right!", "drop-right")
	m.aliasAs(lib, "split-at!", "split-at")

	// ------------------------------------------------------------------- misc
	m.defSimple("append-reverse", 2, 2, func(a []Value) (Value, error) {
		items, ok := ListToSlice(a[0])
		if !ok {
			panic(errf("append-reverse", "expected a proper list but got %s", WriteToString(a[0])))
		}
		res := a[1]
		for _, v := range items {
			res = Cons(v, res)
		}
		return res, nil
	}, lib)

	// (reverse! list) reverses the pairs themselves and returns the new head.
	m.defSimple("reverse!", 1, 1, func(a []Value) (Value, error) {
		var prev Value = Nil
		cur := a[0]
		seen := map[*Pair]bool{}
		for {
			p, ok := cur.(*Pair)
			if !ok {
				if _, isNil := cur.(Empty); !isNil {
					panic(errf("reverse!", "expected a proper list"))
				}
				return prev, nil
			}
			if seen[p] {
				panic(errf("reverse!", "expected a finite list but got a circular one"))
			}
			seen[p] = true
			next := p.Cdr
			p.Cdr = prev
			prev = p
			cur = next
		}
	}, lib)

	// (append! list ...) splices the lists together by mutating their last
	// pairs; the last list is shared, as append shares its last argument.
	m.defSimple("append!", 0, -1, func(a []Value) (Value, error) {
		// head stays a nil interface until a non-empty list is found, so that
		// the first splice can tell whether it is splicing onto anything.
		var head Value
		var tail *Pair
		for i, l := range a {
			if i == len(a)-1 {
				if tail == nil {
					return l, nil
				}
				tail.Cdr = l
				return head, nil
			}
			p, ok := l.(*Pair)
			if !ok {
				if _, isNil := l.(Empty); isNil {
					continue
				}
				panic(errf("append!", "expected a proper list but got %s", WriteToString(l)))
			}
			if head == nil {
				head = p
			} else {
				tail.Cdr = p
			}
			for {
				next, ok := p.Cdr.(*Pair)
				if !ok {
					if _, isNil := p.Cdr.(Empty); !isNil {
						panic(errf("append!", "expected a proper list"))
					}
					break
				}
				p = next
			}
			tail = p
		}
		return Nil, nil
	}, lib)

	// (concatenate list-of-lists) appends them all.
	m.defSimple("concatenate", 1, 1, func(a []Value) (Value, error) {
		parts := seqSlice("concatenate", a[0])
		var out []Value
		for _, p := range parts {
			items, ok := ListToSlice(p)
			if !ok {
				panic(errf("concatenate", "expected a list of lists but got %s", WriteToString(p)))
			}
			out = append(out, items...)
		}
		return List(out...), nil
	}, lib)

	m.aliasAs(lib, "concatenate!", "concatenate")
	m.aliasAs(lib, "append-reverse!", "append-reverse")

	// (unzip1 lis) ... (unzip5 lis) take a list of tuples apart.
	for k := 1; k <= 5; k++ {
		width := k
		name := fmt.Sprintf("unzip%d", width)
		m.defSimple(name, 1, 1, func(a []Value) (Value, error) {
			items, ok := ListToSlice(a[0])
			if !ok {
				panic(errf(name, "expected a proper list but got %s", WriteToString(a[0])))
			}
			cols := make([]Value, width)
			for c := 0; c < width; c++ {
				col := make([]Value, len(items))
				for i, item := range items {
					v := item
					for step := 0; step <= c; step++ {
						p, ok := v.(*Pair)
						if !ok {
							panic(errf(name, "expected tuples of at least %d elements", width))
						}
						if step == c {
							col[i] = p.Car
						}
						v = p.Cdr
					}
				}
				cols[c] = List(col...)
			}
			if width == 1 {
				return cols[0], nil
			}
			return &MultipleValues{Values: cols}, nil
		}, lib)
	}

	// -------------------------------------------------------------- iteration
	// (fold kons knil list ...) folds left to right, calling (kons element
	// accumulator) — note the order, which is SRFI-1's and not fold-left's.
	m.defSimple("fold", 3, -1, func(a []Value) (Value, error) {
		kons := wantProcedure("fold", a[0])
		acc := a[1]
		name := builtinName(kons)
		rows := srfiRows("fold", a[2:])
		if _, known := foldOpElem(name, Int(0), Int(0)); known && len(rows) == 1 {
			for _, elem := range rows[0] {
				acc, _ = foldOpElem(name, elem, acc)
			}
			return acc, nil
		}
		caller := newFastCaller(m, kons)
		for i := range rows[0] {
			args := make([]Value, 0, len(rows)+1)
			for _, row := range rows {
				args = append(args, row[i])
			}
			args = append(args, acc)
			acc = caller.apply(args...)
		}
		return acc, nil
	}, lib)

	// (fold-right kons knil list ...) folds right to left, also with (element
	// accumulator).  It is exported from (goscheme fast) too, which used to
	// have a three-argument version of its own.
	m.defSimple("fold-right", 3, -1, func(a []Value) (Value, error) {
		kons := wantProcedure("fold-right", a[0])
		acc := a[1]
		name := builtinName(kons)
		rows := srfiRows("fold-right", a[2:])
		if _, known := foldOp(name, Int(0), Int(0)); known && len(rows) == 1 {
			for i := len(rows[0]) - 1; i >= 0; i-- {
				acc, _ = foldOp(name, rows[0][i], acc)
			}
			return acc, nil
		}
		caller := newFastCaller(m, kons)
		for i := len(rows[0]) - 1; i >= 0; i-- {
			args := make([]Value, 0, len(rows)+1)
			for _, row := range rows {
				args = append(args, row[i])
			}
			args = append(args, acc)
			acc = caller.apply(args...)
		}
		return acc, nil
	}, lib)

	// (pair-fold kons knil list ...) is fold over the successive pairs.
	m.defSimple("pair-fold", 3, -1, func(a []Value) (Value, error) {
		kons := wantProcedure("pair-fold", a[0])
		acc := a[1]
		caller := newFastCaller(m, kons)
		cursors := make([]Value, len(a)-2)
		copy(cursors, a[2:])
		for {
			args := make([]Value, 0, len(cursors)+1)
			done := false
			for _, c := range cursors {
				if _, ok := c.(*Pair); !ok {
					done = true
					break
				}
				args = append(args, c)
			}
			if done {
				// All of them end together, because the lengths must match.
				for _, c := range cursors {
					if _, ok := c.(*Pair); ok {
						panic(errf("pair-fold", "expected lists of the same length"))
					}
				}
				return acc, nil
			}
			args = append(args, acc)
			acc = caller.apply(args...)
			for i, c := range cursors {
				cursors[i] = c.(*Pair).Cdr
			}
		}
	}, lib)

	m.defSimple("pair-fold-right", 3, -1, func(a []Value) (Value, error) {
		kons := wantProcedure("pair-fold-right", a[0])
		acc := a[1]
		caller := newFastCaller(m, kons)
		rows := make([][]*Pair, len(a)-2)
		for i, l := range a[2:] {
			p, ok := l.(*Pair)
			if !ok {
				if _, isNil := l.(Empty); isNil {
					return acc, nil
				}
				panic(errf("pair-fold-right", "expected a proper list but got %s", WriteToString(l)))
			}
			for {
				rows[i] = append(rows[i], p)
				next, ok := p.Cdr.(*Pair)
				if !ok {
					break
				}
				p = next
			}
		}
		n := -1
		for _, row := range rows {
			if n < 0 {
				n = len(row)
			} else if len(row) != n {
				panic(errf("pair-fold-right", "expected lists of the same length"))
			}
		}
		for i := n - 1; i >= 0; i-- {
			args := make([]Value, 0, len(rows)+1)
			for _, row := range rows {
				args = append(args, row[i])
			}
			args = append(args, acc)
			acc = caller.apply(args...)
		}
		return acc, nil
	}, lib)

	// (reduce f ridentity list) is a fold that uses the first element as the
	// seed, so an empty list yields ridentity.
	m.defSimple("reduce", 3, 3, func(a []Value) (Value, error) {
		f := wantProcedure("reduce", a[0])
		items, ok := ListToSlice(a[2])
		if !ok {
			panic(errf("reduce", "expected a proper list but got %s", WriteToString(a[2])))
		}
		if len(items) == 0 {
			return a[1], nil
		}
		name := builtinName(f)
		acc := items[0]
		if _, known := foldOpElem(name, Int(0), Int(0)); known {
			for _, elem := range items[1:] {
				acc, _ = foldOpElem(name, elem, acc)
			}
			return acc, nil
		}
		caller := newFastCaller(m, f)
		for _, elem := range items[1:] {
			acc = caller.apply(elem, acc)
		}
		return acc, nil
	}, lib)

	m.defSimple("reduce-right", 3, 3, func(a []Value) (Value, error) {
		f := wantProcedure("reduce-right", a[0])
		items, ok := ListToSlice(a[2])
		if !ok {
			panic(errf("reduce-right", "expected a proper list but got %s", WriteToString(a[2])))
		}
		if len(items) == 0 {
			return a[1], nil
		}
		name := builtinName(f)
		acc := items[len(items)-1]
		if _, known := foldOp(name, Int(0), Int(0)); known {
			for i := len(items) - 2; i >= 0; i-- {
				acc, _ = foldOp(name, items[i], acc)
			}
			return acc, nil
		}
		caller := newFastCaller(m, f)
		for i := len(items) - 2; i >= 0; i-- {
			acc = caller.apply(items[i], acc)
		}
		return acc, nil
	}, lib)

	// (unfold p f g seed [tail-gen]) conses (f seed) while (p seed) is false,
	// stepping with g; the tail is (tail-gen seed) or the empty list.
	m.defSimple("unfold", 4, 5, func(a []Value) (Value, error) {
		stop := wantProcedure("unfold", a[0])
		mapf := wantProcedure("unfold", a[1])
		step := wantProcedure("unfold", a[2])
		seed := a[3]
		stopName := builtinName(stop)
		stopCaller := newFastCaller(m, stop)
		mapCaller := newFastCaller(m, mapf)
		stepCaller := newFastCaller(m, step)
		var out listBuilder
		for !stopCaller.pred(stopName, seed) {
			out.add(mapCaller.apply(seed))
			seed = stepCaller.apply(seed)
		}
		if len(a) == 5 {
			tail := newFastCaller(m, wantProcedure("unfold", a[4]))
			out.add(tail.apply(seed))
		}
		return out.list(), nil
	}, lib)

	// (unfold-right p f g seed [tail]) builds the list from the right, so the
	// tail comes last.
	m.defSimple("unfold-right", 4, 5, func(a []Value) (Value, error) {
		stop := wantProcedure("unfold-right", a[0])
		mapf := wantProcedure("unfold-right", a[1])
		step := wantProcedure("unfold-right", a[2])
		seed := a[3]
		stopName := builtinName(stop)
		stopCaller := newFastCaller(m, stop)
		mapCaller := newFastCaller(m, mapf)
		stepCaller := newFastCaller(m, step)
		var collected []Value
		for !stopCaller.pred(stopName, seed) {
			collected = append(collected, mapCaller.apply(seed))
			seed = stepCaller.apply(seed)
		}
		var res Value = Nil
		if len(a) == 5 {
			res = a[4]
		}
		for _, v := range collected {
			res = Cons(v, res)
		}
		return res, nil
	}, lib)

	// (append-map f list ...) is (apply append (map f list ...)).
	m.defSimple("append-map", 2, -1, func(a []Value) (Value, error) {
		f := wantProcedure("append-map", a[0])
		caller := newFastCaller(m, f)
		rows := srfiRows("append-map", a[1:])
		var out []Value
		for i := range rows[0] {
			args := make([]Value, 0, len(rows))
			for _, row := range rows {
				args = append(args, row[i])
			}
			items, ok := ListToSlice(caller.apply(args...))
			if !ok {
				panic(errf("append-map", "the procedure must return a proper list"))
			}
			out = append(out, items...)
		}
		return List(out...), nil
	}, lib)

	m.aliasAs(lib, "append-map!", "append-map")

	// (filter-map f list ...) keeps the non-#f results.
	m.defSimple("filter-map", 2, -1, func(a []Value) (Value, error) {
		f := wantProcedure("filter-map", a[0])
		caller := newFastCaller(m, f)
		rows := srfiRows("filter-map", a[1:])
		var out listBuilder
		for i := range rows[0] {
			args := make([]Value, 0, len(rows))
			for _, row := range rows {
				args = append(args, row[i])
			}
			if v := caller.apply(args...); IsTrue(v) {
				out.add(v)
			}
		}
		return out.list(), nil
	}, lib)

	// (pair-for-each proc list ...) calls proc on the successive pairs.
	m.defSimple("pair-for-each", 2, -1, func(a []Value) (Value, error) {
		f := wantProcedure("pair-for-each", a[0])
		caller := newFastCaller(m, f)
		cursors := make([]Value, len(a)-1)
		copy(cursors, a[1:])
		for {
			args := make([]Value, 0, len(cursors))
			for _, c := range cursors {
				if _, ok := c.(*Pair); !ok {
					return UnspecifiedValue, nil
				}
				args = append(args, c)
			}
			caller.apply(args...)
			for i, c := range cursors {
				cursors[i] = c.(*Pair).Cdr
			}
		}
	}, lib)

	// (map! proc list ...) and (map-in-order proc list ...) are allowed to be
	// pure, so they are map.
	m.aliasAs(lib, "map!", "map")
	m.aliasAs(lib, "map-in-order", "map")

	// --------------------------------------------------------------- filtering
	// (remove pred list) is filter-not under SRFI-1's name.
	m.defSimple("remove", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("remove", a[0])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		var out listBuilder
		forEachIn("remove", a[1], func(v Value) bool {
			if !caller.pred(name, v) {
				out.add(v)
			}
			return true
		})
		return out.list(), nil
	}, lib)

	m.aliasAs(lib, "filter!", "filter")
	m.aliasAs(lib, "partition!", "partition")
	m.aliasAs(lib, "remove!", "remove")
	m.aliasAs(lib, "delete!", "delete")
	m.aliasAs(lib, "delete-duplicates!", "delete-duplicates")

	// --------------------------------------------------------------- searching
	// (find-tail pred list) is the first pair whose car the predicate accepts.
	m.defSimple("find-tail", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("find-tail", a[0])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		cur := a[1]
		seen := map[*Pair]bool{}
		for {
			p, ok := cur.(*Pair)
			if !ok {
				if _, isNil := cur.(Empty); isNil {
					return False, nil
				}
				panic(errf("find-tail", "expected a proper list but got %s", WriteToString(a[1])))
			}
			if seen[p] {
				return False, nil
			}
			seen[p] = true
			if caller.pred(name, p.Car) {
				return p, nil
			}
			cur = p.Cdr
		}
	}, lib)

	m.defSimple("take-while", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("take-while", a[0])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("take-while", "expected a proper list but got %s", WriteToString(a[1])))
		}
		n := 0
		for n < len(items) && caller.pred(name, items[n]) {
			n++
		}
		return List(items[:n]...), nil
	}, lib)

	m.defSimple("drop-while", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("drop-while", a[0])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		items, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("drop-while", "expected a proper list but got %s", WriteToString(a[1])))
		}
		n := 0
		for n < len(items) && caller.pred(name, items[n]) {
			n++
		}
		return List(items[n:]...), nil
	}, lib)

	m.aliasAs(lib, "take-while!", "take-while")

	// (span pred list) returns the longest initial run that satisfies pred and
	// the rest, as two values; (break pred list) does it for the first element
	// that satisfies pred.
	span := func(name string, want bool) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			pred := wantProcedure(name, a[0])
			predName := builtinName(pred)
			caller := newFastCaller(m, pred)
			items, ok := ListToSlice(a[1])
			if !ok {
				panic(errf(name, "expected a proper list but got %s", WriteToString(a[1])))
			}
			n := 0
			for n < len(items) && caller.pred(predName, items[n]) == want {
				n++
			}
			return &MultipleValues{Values: []Value{List(items[:n]...), List(items[n:]...)}}, nil
		}
	}
	m.defSimple("span", 2, 2, span("span", true), lib)
	m.defSimple("break", 2, 2, span("break", false), lib)
	m.aliasAs(lib, "span!", "span")
	m.aliasAs(lib, "break!", "break")

	// ------------------------------------------------------------------ alists
	m.defSimple("alist-copy", 1, 1, func(a []Value) (Value, error) {
		items, ok := ListToSlice(a[0])
		if !ok {
			panic(errf("alist-copy", "expected a proper list but got %s", WriteToString(a[0])))
		}
		out := make([]Value, len(items))
		for i, entry := range items {
			p, ok := entry.(*Pair)
			if !ok {
				panic(errf("alist-copy", "expected an association list but got %s", WriteToString(entry)))
			}
			out[i] = Cons(p.Car, p.Cdr)
		}
		return List(out...), nil
	}, lib)

	// ------------------------------------------------------- set operations
	// SRFI-1 leaves the order of these results unspecified; these preserve the
	// order of their inputs (and the duplicates of the first list), which is
	// deterministic and therefore testable.
	lset := func(name string, body func(caller *fastCaller, eqName string, rows [][]Value) Value) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			eq := wantProcedure(name, a[0])
			eqName := builtinName(eq)
			caller := newFastCaller(m, eq)
			var lists []Value
			if len(a) > 1 {
				lists = a[1:]
			}
			rows := make([][]Value, len(lists))
			for i, l := range lists {
				items, ok := ListToSlice(l)
				if !ok {
					panic(errf(name, "expected a proper list but got %s", WriteToString(l)))
				}
				rows[i] = items
			}
			return body(caller, eqName, rows), nil
		}
	}
	contains := func(caller *fastCaller, eqName string, items []Value, x Value) bool {
		for _, v := range items {
			if caller.less(eqName, v, x) {
				return true
			}
		}
		return false
	}
	subset := func(caller *fastCaller, eqName string, small, big []Value) bool {
		for _, v := range small {
			if !contains(caller, eqName, big, v) {
				return false
			}
		}
		return true
	}

	m.defSimple("lset<=", 1, -1, lset("lset<=", func(caller *fastCaller, eqName string, rows [][]Value) Value {
		for i := 1; i < len(rows); i++ {
			if !subset(caller, eqName, rows[i], rows[i-1]) {
				return False
			}
		}
		return True
	}), lib)

	m.defSimple("lset=", 1, -1, lset("lset=", func(caller *fastCaller, eqName string, rows [][]Value) Value {
		for i := 1; i < len(rows); i++ {
			if !subset(caller, eqName, rows[i], rows[i-1]) ||
				!subset(caller, eqName, rows[i-1], rows[i]) {
				return False
			}
		}
		return True
	}), lib)

	// lset-adjoin is the odd one out: its arguments after the list are
	// elements, not lists.
	m.defSimple("lset-adjoin", 2, -1, func(a []Value) (Value, error) {
		eq := wantProcedure("lset-adjoin", a[0])
		eqName := builtinName(eq)
		caller := newFastCaller(m, eq)
		base, ok := ListToSlice(a[1])
		if !ok {
			panic(errf("lset-adjoin", "expected a proper list but got %s", WriteToString(a[1])))
		}
		base = append([]Value(nil), base...)
		for _, x := range a[2:] {
			if !contains(caller, eqName, base, x) {
				base = append(base, x)
			}
		}
		return List(base...), nil
	}, lib)

	m.defSimple("lset-union", 0, -1, lset("lset-union", func(caller *fastCaller, eqName string, rows [][]Value) Value {
		var out []Value
		for _, row := range rows {
			for _, x := range row {
				if !contains(caller, eqName, out, x) {
					out = append(out, x)
				}
			}
		}
		return List(out...)
	}), lib)

	m.defSimple("lset-intersection", 1, -1, lset("lset-intersection", func(caller *fastCaller, eqName string, rows [][]Value) Value {
		var out []Value
		for _, x := range rows[0] {
			keep := true
			for _, other := range rows[1:] {
				if !contains(caller, eqName, other, x) {
					keep = false
					break
				}
			}
			if keep {
				out = append(out, x)
			}
		}
		return List(out...)
	}), lib)

	m.defSimple("lset-difference", 1, -1, lset("lset-difference", func(caller *fastCaller, eqName string, rows [][]Value) Value {
		var out []Value
		for _, x := range rows[0] {
			drop := false
			for _, other := range rows[1:] {
				if contains(caller, eqName, other, x) {
					drop = true
					break
				}
			}
			if !drop {
				out = append(out, x)
			}
		}
		return List(out...)
	}), lib)

	m.defSimple("lset-xor", 0, -1, lset("lset-xor", func(caller *fastCaller, eqName string, rows [][]Value) Value {
		var out []Value
		for _, row := range rows {
			// An element survives one more list when it was in exactly one of
			// the lists seen so far, which is a running symmetric difference.
			for _, x := range row {
				found := -1
				for i, v := range out {
					if caller.less(eqName, v, x) {
						found = i
						break
					}
				}
				if found >= 0 {
					out = append(out[:found], out[found+1:]...)
				} else {
					out = append(out, x)
				}
			}
		}
		return List(out...)
	}), lib)

	m.defSimple("lset-diff+intersection", 1, -1, lset("lset-diff+intersection", func(caller *fastCaller, eqName string, rows [][]Value) Value {
		var diff, common []Value
		for _, x := range rows[0] {
			inAll := true
			for _, other := range rows[1:] {
				if !contains(caller, eqName, other, x) {
					inAll = false
					break
				}
			}
			if inAll {
				common = append(common, x)
			} else {
				diff = append(diff, x)
			}
		}
		return &MultipleValues{Values: []Value{List(diff...), List(common...)}}
	}), lib)

	// ------------------------------------------------------- any, every (SRFI-1)
	// These are the SRFI-1 versions, shared with (goscheme fast): they return
	// what the predicate returned rather than a boolean, and they accept one or
	// more lists.  A single vector is still accepted, as the fast library's
	// sequences are.
	m.defSimple("any", 2, -1, func(a []Value) (Value, error) {
		return anyEvery(m, "any", a, false)
	}, lib, "(goscheme fast)")

	m.defSimple("every", 2, -1, func(a []Value) (Value, error) {
		return anyEvery(m, "every", a, true)
	}, lib, "(goscheme fast)")
}

// srfiPredIndex is the shared body of count and list-index: it walks one or
// more lists in parallel, applying the predicate to every element of a
// position, and either counts the positions that pass or reports the first one.
func srfiPredIndex(m *Machine, name string, a []Value, countAll, right, skip bool) (Value, error) {
	pred := wantProcedure(name, a[0])
	predName := builtinName(pred)
	caller := newFastCaller(m, pred)
	rows := srfiRows(name, a[1:])
	length := len(rows[0])
	// matches reports whether position i satisfies the test, and `skip` inverts
	// it so that the same scan serves vector-skip as well as vector-index.
	//
	// skip is a parameter rather than a comparison against name.  Deriving it
	// from the name would make a rename change what the procedure does, silently
	// and with no test able to see it.
	matches := func(i int) bool {
		args := make([]Value, 0, len(rows))
		for _, row := range rows {
			args = append(args, row[i])
		}
		// With several sequences the predicate is applied to the elements of a
		// position in one call, the way SRFI-1 says.
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

// anyEvery is the shared body of any and every.  all selects every.
func anyEvery(m *Machine, name string, a []Value, all bool) (Value, error) {
	pred := wantProcedure(name, a[0])
	predName := builtinName(pred)
	caller := newFastCaller(m, pred)
	// The value of the predicate, and whether it counted as true.  A builtin
	// predicate answers with #t or #f anyway, so the fast lane loses nothing.
	value := func(v Value) (Value, bool) {
		if predName != "" {
			if res, ok := fastPred(predName, v); ok {
				return BooleanOf(res), res
			}
		}
		res := caller.apply(v)
		return res, IsTrue(res)
	}
	rows := srfiRows(name, a[1:])
	var last Value = True
	for i := range rows[0] {
		args := make([]Value, 0, len(rows))
		for _, row := range rows {
			args = append(args, row[i])
		}
		var res Value
		var truth bool
		if len(args) == 1 {
			res, truth = value(args[0])
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

// isCircular reports whether v is a chain of pairs that loops.  It uses the
// tortoise and the hare, so it terminates on a circular list and on any finite
// one.
func isCircular(v Value) bool {
	slow, fast := v, v
	for {
		a, ok := fast.(*Pair)
		if !ok {
			return false
		}
		fast = a.Cdr
		b, ok := fast.(*Pair)
		if !ok {
			return false
		}
		fast = b.Cdr
		slow = slow.(*Pair).Cdr
		if fast == slow {
			return true
		}
	}
}

func init() { registerInstaller(installSRFI1) }
