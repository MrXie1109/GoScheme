// SPDX-License-Identifier: MIT

package scheme

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"sort"
	"strings"
	"unicode"
)

// The (goscheme fast) library: the jobs Scheme does slowly, written in Go.
//
// Two things make the difference.  The first is doing the loop, the indexing
// and the copying natively instead of as interpreted steps.  The second, and
// usually the bigger one, is not calling back into Scheme for every element: a
// sort or a filter whose predicate is a builtin — (< a b), even?, string? and
// the like — runs entirely in Go, and only a programmer-written predicate pays
// for a call per element.

func installFast(m *Machine) {
	const lib = "(goscheme fast)"
	installFastOrdering(m, lib)
	installFastSequences(m, lib)
	installFastLists(m, lib)
	installFastVectors(m, lib)
	installFastBatch(m, lib)
	installFastSelection(m, lib)
	installFastAggregates(m, lib)
	installFastStats(m, lib)
	installFastNumberTheory(m, lib)
	installFastStrings(m, lib)
	installFastText(m, lib)
	installFastBytes(m, lib)
	installFastHashing(m, lib)
	installFastEncoding(m, lib)
	installFastRandom(m, lib)
}

// ---------------------------------------------------------------------------
// Recognising builtins, so that they can be applied without a Scheme call
// ---------------------------------------------------------------------------

// defaultLess is the < primitive, which the ordering procedures use when no
// comparison is given.
func defaultLess(m *Machine) Value {
	return builtinProc(m, "<")
}

// builtinName is the name of a primitive, or "" for anything else.
func builtinName(v Value) string {
	if p, ok := v.(*Primitive); ok {
		return p.Name
	}
	return ""
}

// fastLess applies one of the comparison builtins directly.  The second result
// reports whether the name is one of them; the caller falls back to a Scheme
// call when it is not.
func fastLess(name string, a, b Value) (bool, bool) {
	switch name {
	case "<":
		return NumCmp(a, b) == -1, true
	case "<=":
		c := NumCmp(a, b)
		return c == -1 || c == 0, true
	case ">":
		return NumCmp(a, b) == 1, true
	case ">=":
		c := NumCmp(a, b)
		return c == 1 || c == 0, true
	case "=":
		return NumEq(a, b), true
	case "string<?":
		return wantString("string<?", a).Value() < wantString("string<?", b).Value(), true
	case "string<=?":
		return wantString("string<=?", a).Value() <= wantString("string<=?", b).Value(), true
	case "string>?":
		return wantString("string>?", a).Value() > wantString("string>?", b).Value(), true
	case "string>=?":
		return wantString("string>=?", a).Value() >= wantString("string>=?", b).Value(), true
	case "string=?":
		return wantString("string=?", a).Value() == wantString("string=?", b).Value(), true
	case "string-ci<?":
		return strings.ToLower(wantString("string-ci<?", a).Value()) <
			strings.ToLower(wantString("string-ci<?", b).Value()), true
	case "char<?":
		return wantChar("char<?", a) < wantChar("char<?", b), true
	case "char=?":
		return wantChar("char=?", a) == wantChar("char=?", b), true
	case "equal?":
		return Equal(a, b), true
	case "eqv?":
		return Eqv(a, b), true
	case "eq?":
		return Eq(a, b), true
	}
	return false, false
}

// fastPred applies one of the predicate builtins directly.
func fastPred(name string, v Value) (bool, bool) {
	switch name {
	case "even?":
		n, ok := v.(*Integer)
		if !ok {
			panic(errf("even?", "expected an exact integer but got %s", WriteToString(v)))
		}
		i, _ := n.Int64()
		return i%2 == 0, true
	case "odd?":
		n, ok := v.(*Integer)
		if !ok {
			panic(errf("odd?", "expected an exact integer but got %s", WriteToString(v)))
		}
		i, _ := n.Int64()
		return i%2 != 0, true
	case "zero?":
		return NumSign(v) == 0, true
	case "positive?":
		return NumSign(v) == 1, true
	case "negative?":
		return NumSign(v) == -1, true
	case "number?":
		return IsNumber(v), true
	case "integer?":
		_, ok := v.(*Integer)
		return ok, true
	case "string?":
		_, ok := v.(*String)
		return ok, true
	case "symbol?":
		_, ok := v.(*Symbol)
		return ok, true
	case "char?":
		_, ok := v.(Char)
		return ok, true
	case "boolean?":
		_, ok := v.(Boolean)
		return ok, true
	case "pair?":
		_, ok := v.(*Pair)
		return ok, true
	case "null?":
		_, ok := v.(Empty)
		return ok, true
	case "list?":
		return IsList(v), true
	case "vector?":
		_, ok := v.(*Vector)
		return ok, true
	case "not":
		return IsFalse(v), true
	}
	return false, false
}

// callFast is the fallback for a predicate or comparison the fast lane does not
// know: it calls the Scheme procedure on a machine of its own, which is what
// lets a Go loop drive Scheme code without disturbing the caller's
// continuation.
type fastCaller struct {
	m     *Machine
	proc  Value
	child *Machine
}

func newFastCaller(m *Machine, proc Value) *fastCaller {
	return &fastCaller{m: m, proc: proc, child: m.Child()}
}

// apply calls the procedure and returns its value.
func (c *fastCaller) apply(args ...Value) Value {
	v, err := c.child.RunApply(c.proc, args, c.child.Global)
	if err != nil {
		// Panicking turns the condition into one raised by the primitive that
		// is running, which is what the caller sees either way.
		panic(err)
	}
	return v
}

// call applies the procedure and reports its value as a truth value.
func (c *fastCaller) call(args ...Value) bool {
	return IsTrue(c.apply(args...))
}

// less is a comparison function that uses the fast lane when it can.
func (c *fastCaller) less(name string, a, b Value) bool {
	if name != "" {
		if res, ok := fastLess(name, a, b); ok {
			return res
		}
	}
	return c.call(a, b)
}

// pred is a predicate function that uses the fast lane when it can.
func (c *fastCaller) pred(name string, v Value) bool {
	if name != "" {
		if res, ok := fastPred(name, v); ok {
			return res
		}
	}
	return c.call(v)
}

// ---------------------------------------------------------------------------
// Ordering
// ---------------------------------------------------------------------------

func installFastOrdering(m *Machine, lib string) {
	// (sort list [less?]) returns a new, sorted list; the default is <.
	m.defSimple("sort", 1, 2, func(a []Value) (Value, error) {
		items := wantList("sort", a[0])
		less := defaultLess(m)
		if len(a) == 2 {
			less = wantProcedure("sort", a[1])
		}
		sorted := append([]Value(nil), items...)
		sortValues(sorted, less, m, "sort")
		return List(sorted...), nil
	}, lib)

	// (vector-sort vector [less?]) returns a new sorted vector.
	m.defSimple("vector-sort", 1, 2, func(a []Value) (Value, error) {
		vec := wantVector("vector-sort", a[0])
		less := defaultLess(m)
		if len(a) == 2 {
			less = wantProcedure("vector-sort", a[1])
		}
		out := append([]Value(nil), vec.Items...)
		sortValues(out, less, m, "vector-sort")
		return &Vector{Items: out}, nil
	}, lib)

	// (sort! vector [less?]) sorts in place and returns the vector.
	m.defSimple("sort!", 1, 2, func(a []Value) (Value, error) {
		vec := wantVector("sort!", a[0])
		less := defaultLess(m)
		if len(a) == 2 {
			less = wantProcedure("sort!", a[1])
		}
		sortValues(vec.Items, less, m, "sort!")
		return vec, nil
	}, lib)

	// (vector-binary-search vector key [comparison]) reports the index of the
	// first element equal to key, or #f.  The comparison may be a predicate in
	// the style of <, which is what (goscheme fast) has always taken, or
	// SRFI-133's three-valued function returning -1, 0 or 1; the first call
	// decides which, so both libraries can share this binding.
	m.defSimple("vector-binary-search", 2, 3, func(a []Value) (Value, error) {
		vec := wantVector("vector-binary-search", a[0])
		key := a[1]
		cmp := defaultLess(m)
		if len(a) == 3 {
			cmp = wantProcedure("vector-binary-search", a[2])
		}
		name := builtinName(cmp)
		caller := newFastCaller(m, cmp)
		threeWay, decided := false, false
		compare := func(x Value) int {
			if name != "" {
				if res, ok := fastLess(name, x, key); ok {
					if res {
						return -1
					}
					if back, ok := fastLess(name, key, x); ok && back {
						return 1
					}
					return 0
				}
			}
			res := caller.apply(x, key)
			if !decided {
				decided = true
				switch res.(type) {
				case *Integer, *Rational, Float:
					threeWay = true
				}
			}
			if threeWay {
				return NumSign(res)
			}
			if IsTrue(res) {
				return -1
			}
			if IsTrue(caller.apply(key, x)) {
				return 1
			}
			return 0
		}
		// The lower bound keeps the first of several equal elements.
		lo, hi := 0, len(vec.Items)
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			if compare(vec.Items[mid]) < 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo < len(vec.Items) && compare(vec.Items[lo]) == 0 {
			return Int(int64(lo)), nil
		}
		return False, nil
	}, lib)
}

// sortValues sorts with the fast lane for a builtin comparison, and one child
// machine for a programmer-written one.
func sortValues(items []Value, less Value, m *Machine, name string) {
	if len(items) < 2 {
		return
	}
	lessName := builtinName(less)
	caller := newFastCaller(m, less)
	sort.SliceStable(items, func(i, j int) bool {
		return caller.less(lessName, items[i], items[j])
	})
	_ = name
}

// ---------------------------------------------------------------------------
// Sequences
// ---------------------------------------------------------------------------

func installFastSequences(m *Machine, lib string) {
	// (iota count [start [step]]) builds a list, in Go rather than by consing
	// one element per interpreted step.
	m.defSimple("iota", 1, 3, func(a []Value) (Value, error) {
		count := wantIndex("iota", a[0])
		start := Value(Int(0))
		if len(a) > 1 {
			start = wantNumber("iota", a[1])
		}
		step := Value(Int(1))
		if len(a) > 2 {
			step = wantNumber("iota", a[2])
		}
		out := make([]Value, count)
		cur := start
		for i := 0; i < count; i++ {
			out[i] = cur
			cur = NumAdd(cur, step)
		}
		return List(out...), nil
	}, lib)

	m.defSimple("vector-reverse", 1, 1, func(a []Value) (Value, error) {
		vec := wantVector("vector-reverse", a[0])
		out := make([]Value, len(vec.Items))
		for i, v := range vec.Items {
			out[len(out)-1-i] = v
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (vector-reverse! vector [start [end]]) reverses in place and returns the
	// vector; (srfi 133) exports the same binding, which is why the range is
	// there.
	m.defSimple("vector-reverse!", 1, 3, func(a []Value) (Value, error) {
		vec := wantVector("vector-reverse!", a[0])
		start, end := optionalSliceRange("vector-reverse!", a, len(vec.Items))
		for i, j := start, end-1; i < j; i, j = i+1, j-1 {
			vec.Items[i], vec.Items[j] = vec.Items[j], vec.Items[i]
		}
		return vec, nil
	}, lib)

	m.defSimple("vector-swap!", 3, 3, func(a []Value) (Value, error) {
		vec := wantVector("vector-swap!", a[0])
		i := wantIndex("vector-swap!", a[1])
		j := wantIndex("vector-swap!", a[2])
		if i >= len(vec.Items) || j >= len(vec.Items) {
			panic(errf("vector-swap!", "index out of range"))
		}
		vec.Items[i], vec.Items[j] = vec.Items[j], vec.Items[i]
		return UnspecifiedValue, nil
	}, lib)
}

// forEachIn walks a list or a vector, without copying it into a slice first:
// that copy was most of the work for the aggregates and selections.  The
// function stops early when fn returns false.
func forEachIn(name string, v Value, fn func(Value) bool) {
	switch seq := v.(type) {
	case *Vector:
		for _, x := range seq.Items {
			if !fn(x) {
				return
			}
		}
	case Empty:
	case *Pair:
		for p := Value(seq); ; {
			pp, ok := p.(*Pair)
			if !ok {
				if _, isNil := p.(Empty); !isNil {
					panic(errf(name, "expected a proper list but got %s", WriteToString(v)))
				}
				return
			}
			if !fn(pp.Car) {
				return
			}
			p = pp.Cdr
		}
	default:
		panic(errf(name, "expected a list or a vector but got %s", WriteToString(v)))
	}
}

// listBuilder collects values into a list as they arrive, so that a selection
// needs one pass and no intermediate slice.
type listBuilder struct {
	head, tail *Pair
}

func (b *listBuilder) add(v Value) {
	cell := &Pair{Car: v, Cdr: Nil}
	if b.head == nil {
		b.head = cell
	} else {
		b.tail.Cdr = cell
	}
	b.tail = cell
}

func (b *listBuilder) list() Value {
	if b.head == nil {
		return Nil
	}
	return b.head
}

// ---------------------------------------------------------------------------
// Selection
// ---------------------------------------------------------------------------

func installFastSelection(m *Machine, lib string) {
	// (filter pred list) keeps the elements the predicate accepts.
	m.defSimple("filter", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("filter", a[0])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		var out listBuilder
		forEachIn("filter", a[1], func(v Value) bool {
			if caller.pred(name, v) {
				out.add(v)
			}
			return true
		})
		return out.list(), nil
	}, lib)

	// (filter! pred vector) keeps the accepted elements in place and returns the
	// number kept, leaving the rest of the vector untouched.
	m.defSimple("vector-filter!", 2, 2, func(a []Value) (Value, error) {
		pred := wantProcedure("vector-filter!", a[0])
		vec := wantVector("vector-filter!", a[1])
		name := builtinName(pred)
		caller := newFastCaller(m, pred)
		n := 0
		for _, v := range vec.Items {
			if caller.pred(name, v) {
				vec.Items[n] = v
				n++
			}
		}
		return Int(int64(n)), nil
	}, lib)

	// count, list-index and fold-right are SRFI-1's, and are defined there and
	// exported from this library as well, so that the two cannot disagree.

	// any and every live in (srfi 1), which is where their SRFI-1 semantics —
	// the predicate's own value, one or more lists — come from; they are
	// exported from this library too, so they are not defined twice.
}

// ---------------------------------------------------------------------------
// Aggregates
// ---------------------------------------------------------------------------

func installFastAggregates(m *Machine, lib string) {
	m.defSimple("sum", 1, 1, func(a []Value) (Value, error) {
		var acc Value = Int(0)
		forEachIn("sum", a[0], func(v Value) bool {
			acc = NumAdd(acc, wantNumber("sum", v))
			return true
		})
		return acc, nil
	}, lib)

	m.defSimple("product", 1, 1, func(a []Value) (Value, error) {
		var acc Value = Int(1)
		forEachIn("product", a[0], func(v Value) bool {
			acc = NumMul(acc, wantNumber("product", v))
			return true
		})
		return acc, nil
	}, lib)

	m.defSimple("min-of", 1, 1, func(a []Value) (Value, error) {
		best := Value(nil)
		forEachIn("min-of", a[0], func(v Value) bool {
			v = wantNumber("min-of", v)
			if best == nil || NumCmp(v, best) == -1 {
				best = v
			}
			return true
		})
		if best == nil {
			panic(errf("min-of", "expected a non-empty sequence"))
		}
		return best, nil
	}, lib)

	m.defSimple("max-of", 1, 1, func(a []Value) (Value, error) {
		best := Value(nil)
		forEachIn("max-of", a[0], func(v Value) bool {
			v = wantNumber("max-of", v)
			if best == nil || NumCmp(v, best) == 1 {
				best = v
			}
			return true
		})
		if best == nil {
			panic(errf("max-of", "expected a non-empty sequence"))
		}
		return best, nil
	}, lib)

	// (vector-dot a b) is the inner product of two vectors, which is the shape
	// numeric code asks for most often.
	m.defSimple("vector-dot", 2, 2, func(a []Value) (Value, error) {
		x := wantVector("vector-dot", a[0])
		y := wantVector("vector-dot", a[1])
		if len(x.Items) != len(y.Items) {
			panic(errf("vector-dot", "vectors of different lengths"))
		}
		var acc Value = Int(0)
		for i := range x.Items {
			acc = NumAdd(acc, NumMul(wantNumber("vector-dot", x.Items[i]),
				wantNumber("vector-dot", y.Items[i])))
		}
		return acc, nil
	}, lib)
}

// ---------------------------------------------------------------------------
// Strings
// ---------------------------------------------------------------------------

func installFastStrings(m *Machine, lib string) {
	// (string-split string [separator [limit]]) splits on separator, which is
	// "" by default to split into characters.  A limit keeps the tail whole:
	// with a limit of 2, "a:b:c" splits into "a" and "b:c".
	m.defSimple("string-split", 1, 3, func(a []Value) (Value, error) {
		s := wantString("string-split", a[0]).Value()
		sep := ""
		if len(a) > 1 {
			sep = wantString("string-split", a[1]).Value()
		}
		parts := strings.Split(s, sep)
		if len(a) == 3 {
			limit := wantIndex("string-split", a[2])
			if limit < 1 {
				panic(errf("string-split", "the limit must be at least 1"))
			}
			parts = strings.SplitN(s, sep, limit)
		}
		out := make([]Value, len(parts))
		for i, p := range parts {
			out[i] = NewString(p)
		}
		return List(out...), nil
	}, lib)

	m.defSimple("string-join", 1, 2, func(a []Value) (Value, error) {
		items := wantList("string-join", a[0])
		sep := ""
		if len(a) == 2 {
			sep = wantString("string-join", a[1]).Value()
		}
		parts := make([]string, len(items))
		for i, v := range items {
			parts[i] = wantString("string-join", v).Value()
		}
		return NewString(strings.Join(parts, sep)), nil
	}, lib)

	// (string-contains string substring) is the index of the substring in
	// characters, or #f.  Characters, not bytes: the interpreter counts runes.
	m.defSimple("string-contains", 2, 2, func(a []Value) (Value, error) {
		s := []rune(wantString("string-contains", a[0]).Value())
		sub := []rune(wantString("string-contains", a[1]).Value())
		if len(sub) == 0 {
			return Int(0), nil
		}
		for i := 0; i+len(sub) <= len(s); i++ {
			if string(s[i:i+len(sub)]) == string(sub) {
				return Int(int64(i)), nil
			}
		}
		return False, nil
	}, lib)

	m.defSimple("string-index", 2, 2, func(a []Value) (Value, error) {
		s := []rune(wantString("string-index", a[0]).Value())
		want := rune(wantChar("string-index", a[1]))
		for i, r := range s {
			if r == want {
				return Int(int64(i)), nil
			}
		}
		return False, nil
	}, lib)

	m.defSimple("string-prefix?", 2, 2, func(a []Value) (Value, error) {
		return BooleanOf(strings.HasPrefix(wantString("string-prefix?", a[0]).Value(),
			wantString("string-prefix?", a[1]).Value())), nil
	}, lib)

	m.defSimple("string-suffix?", 2, 2, func(a []Value) (Value, error) {
		return BooleanOf(strings.HasSuffix(wantString("string-suffix?", a[0]).Value(),
			wantString("string-suffix?", a[1]).Value())), nil
	}, lib)

	trim := func(name string, left, right bool) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			s := wantString(name, a[0]).Value()
			cutset := ""
			if len(a) == 2 {
				cutset = wantString(name, a[1]).Value()
			}
			switch {
			case cutset == "":
				if left && right {
					return NewString(strings.TrimSpace(s)), nil
				}
				if left {
					return NewString(strings.TrimLeftFunc(s, unicode.IsSpace)), nil
				}
				return NewString(strings.TrimRightFunc(s, unicode.IsSpace)), nil
			case left && right:
				return NewString(strings.Trim(s, cutset)), nil
			case left:
				return NewString(strings.TrimLeft(s, cutset)), nil
			default:
				return NewString(strings.TrimRight(s, cutset)), nil
			}
		}
	}
	m.defSimple("string-trim", 1, 2, trim("string-trim", true, true), lib)
	m.defSimple("string-trim-left", 1, 2, trim("string-trim-left", true, false), lib)
	m.defSimple("string-trim-right", 1, 2, trim("string-trim-right", false, true), lib)

	m.defSimple("string-replace", 3, 3, func(a []Value) (Value, error) {
		s := wantString("string-replace", a[0]).Value()
		from := wantString("string-replace", a[1]).Value()
		to := wantString("string-replace", a[2]).Value()
		if from == "" {
			panic(errf("string-replace", "the string to replace must not be empty"))
		}
		return NewString(strings.ReplaceAll(s, from, to)), nil
	}, lib)

	pad := func(name string, left bool) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			s := []rune(wantString(name, a[0]).Value())
			width := wantIndex(name, a[1])
			pad := ' '
			if len(a) == 3 {
				pad = rune(wantChar(name, a[2]))
			}
			if len(s) >= width {
				return NewString(string(s)), nil
			}
			fill := strings.Repeat(string(pad), width-len(s))
			if left {
				return NewString(fill + string(s)), nil
			}
			return NewString(string(s) + fill), nil
		}
	}
	m.defSimple("string-pad-left", 2, 3, pad("string-pad-left", true), lib)
	m.defSimple("string-pad-right", 2, 3, pad("string-pad-right", false), lib)
}

// ---------------------------------------------------------------------------
// Bytes: hashing and encodings
// ---------------------------------------------------------------------------

func installFastBytes(m *Machine, lib string) {
	// (sha256 data) hashes a string or a bytevector and returns lowercase hex.
	m.defSimple("sha256", 1, 1, func(a []Value) (Value, error) {
		var data []byte
		switch v := a[0].(type) {
		case *String:
			data = []byte(v.Value())
		case *Bytevector:
			data = v.Bytes
		default:
			panic(errf("sha256", "expected a string or a bytevector but got %s", WriteToString(a[0])))
		}
		sum := sha256.Sum256(data)
		return NewString(hex.EncodeToString(sum[:])), nil
	}, lib)

	// (random-bytes count) is count bytes from the system's random source,
	// which is what a token or a key needs.
	m.defSimple("random-bytes", 1, 1, func(a []Value) (Value, error) {
		n := wantIndex("random-bytes", a[0])
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			panic(errf("random-bytes", "%s", err.Error()))
		}
		return NewBytevectorFrom(buf), nil
	}, lib)

	m.defSimple("hex-encode", 1, 1, func(a []Value) (Value, error) {
		return NewString(hex.EncodeToString(wantBytevector("hex-encode", a[0]).Bytes)), nil
	}, lib)

	m.defSimple("hex-decode", 1, 1, func(a []Value) (Value, error) {
		s := wantString("hex-decode", a[0]).Value()
		buf, err := hex.DecodeString(s)
		if err != nil {
			panic(errf("hex-decode", "%s", err.Error()))
		}
		return NewBytevectorFrom(buf), nil
	}, lib)

	m.defSimple("base64-encode", 1, 1, func(a []Value) (Value, error) {
		return NewString(base64.StdEncoding.EncodeToString(wantBytevector("base64-encode", a[0]).Bytes)), nil
	}, lib)

	m.defSimple("base64-decode", 1, 1, func(a []Value) (Value, error) {
		s := wantString("base64-decode", a[0]).Value()
		buf, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			panic(errf("base64-decode", "%s", err.Error()))
		}
		return NewBytevectorFrom(buf), nil
	}, lib)
}

func init() { registerInstaller(installFast) }
