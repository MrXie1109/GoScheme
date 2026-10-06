// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

func installLists(m *Machine) {
	m.defSimple("pair?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Pair)
		return BooleanOf(ok), nil
	}, libBase, libR5RS)
	m.defSimple("cons", 2, 2, func(a []Value) (Value, error) {
		return Cons(a[0], a[1]), nil
	}, libBase, libR5RS)
	m.defSimple("car", 1, 1, func(a []Value) (Value, error) {
		return wantPair("car", a[0]).Car, nil
	}, libBase, libR5RS)
	m.defSimple("cdr", 1, 1, func(a []Value) (Value, error) {
		return wantPair("cdr", a[0]).Cdr, nil
	}, libBase, libR5RS)
	m.defSimple("set-car!", 2, 2, func(a []Value) (Value, error) {
		wantPair("set-car!", a[0]).Car = a[1]
		return UnspecifiedValue, nil
	}, libBase, libR5RS)
	m.defSimple("set-cdr!", 2, 2, func(a []Value) (Value, error) {
		wantPair("set-cdr!", a[0]).Cdr = a[1]
		return UnspecifiedValue, nil
	}, libBase, libR5RS)
	m.defSimple("null?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(Empty)
		return BooleanOf(ok), nil
	}, libBase, libR5RS)
	m.defSimple("list?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(IsList(a[0])), nil
	}, libBase, libR5RS)
	m.defSimple("list", 0, -1, func(a []Value) (Value, error) {
		return List(a...), nil
	}, libBase, libR5RS)
	m.defSimple("length", 1, 1, func(a []Value) (Value, error) {
		n := ListLength(a[0])
		if n < 0 {
			panic(errf("length", "expected a proper list"))
		}
		return Int(int64(n)), nil
	}, libBase, libR5RS)
	m.defSimple("append", 0, -1, func(a []Value) (Value, error) {
		if len(a) == 0 {
			return Nil, nil
		}
		var out []Value
		for _, l := range a[:len(a)-1] {
			items, ok := ListToSlice(l)
			if !ok {
				panic(errf("append", "expected a proper list but got %s", WriteToString(l)))
			}
			out = append(out, items...)
		}
		res := a[len(a)-1]
		for i := len(out) - 1; i >= 0; i-- {
			res = Cons(out[i], res)
		}
		return res, nil
	}, libBase, libR5RS)
	m.defSimple("reverse", 1, 1, func(a []Value) (Value, error) {
		items := wantList("reverse", a[0])
		var res Value = Nil
		for _, it := range items {
			res = Cons(it, res)
		}
		return res, nil
	}, libBase, libR5RS)
	m.defSimple("list-tail", 2, 2, func(a []Value) (Value, error) {
		k := wantIndex("list-tail", a[1])
		cur := a[0]
		for i := 0; i < k; i++ {
			p, ok := cur.(*Pair)
			if !ok {
				panic(errf("list-tail", "list is too short"))
			}
			cur = p.Cdr
		}
		return cur, nil
	}, libBase)
	m.defSimple("list-ref", 2, 2, func(a []Value) (Value, error) {
		k := wantIndex("list-ref", a[1])
		cur := a[0]
		for i := 0; i < k; i++ {
			p, ok := cur.(*Pair)
			if !ok {
				panic(errf("list-ref", "index out of range"))
			}
			cur = p.Cdr
		}
		p, ok := cur.(*Pair)
		if !ok {
			panic(errf("list-ref", "index out of range"))
		}
		return p.Car, nil
	}, libBase)
	m.defSimple("list-set!", 3, 3, func(a []Value) (Value, error) {
		k := wantIndex("list-set!", a[1])
		cur := a[0]
		for i := 0; i < k; i++ {
			p, ok := cur.(*Pair)
			if !ok {
				panic(errf("list-set!", "index out of range"))
			}
			cur = p.Cdr
		}
		p, ok := cur.(*Pair)
		if !ok {
			panic(errf("list-set!", "index out of range"))
		}
		p.Car = a[2]
		return UnspecifiedValue, nil
	}, libBase)
	m.defSimple("list-copy", 1, 1, func(a []Value) (Value, error) {
		return listCopy(a[0]), nil
	}, libBase)
	m.defSimple("make-list", 1, 2, func(a []Value) (Value, error) {
		n := wantIndex("make-list", a[0])
		var fill Value = UnspecifiedValue
		if len(a) == 2 {
			fill = a[1]
		}
		var res Value = Nil
		for i := 0; i < n; i++ {
			res = Cons(fill, res)
		}
		return res, nil
	}, libBase)

	m.defSimple("memq", 2, 2, func(a []Value) (Value, error) { return memGeneric(a[0], a[1], Eq, "memq") }, libBase, libR5RS)
	m.defSimple("memv", 2, 2, func(a []Value) (Value, error) { return memGeneric(a[0], a[1], Eqv, "memv") }, libBase, libR5RS)
	m.def("member", 2, 3, func(m *Machine, a []Value) {
		if len(a) == 2 {
			v, err := memGeneric(a[0], a[1], Equal, "member")
			if err != nil {
				m.RaiseError(err)
				return
			}
			m.Return(v)
			return
		}
		obj, proc := a[0], a[2]
		cur := a[1]
		var step func()
		step = func() {
			switch c := cur.(type) {
			case *Pair:
				m.ApplyWith(proc, []Value{obj, c.Car}, func(m *Machine, v Value) {
					if IsTrue(v) {
						m.Return(c)
						return
					}
					cur = c.Cdr
					step()
				})
			case Empty:
				m.Return(False)
			default:
				m.Raise(errf("member", "expected a proper list"))
			}
		}
		step()
	}, libBase, libR5RS)
	m.defSimple("assq", 2, 2, func(a []Value) (Value, error) { return assGeneric(a[0], a[1], Eq, "assq") }, libBase, libR5RS)
	m.defSimple("assv", 2, 2, func(a []Value) (Value, error) { return assGeneric(a[0], a[1], Eqv, "assv") }, libBase, libR5RS)
	m.def("assoc", 2, 3, func(m *Machine, a []Value) {
		if len(a) == 2 {
			v, err := assGeneric(a[0], a[1], Equal, "assoc")
			if err != nil {
				m.RaiseError(err)
				return
			}
			m.Return(v)
			return
		}
		obj, proc := a[0], a[2]
		cur := a[1]
		var step func()
		step = func() {
			switch c := cur.(type) {
			case *Pair:
				entry, ok := c.Car.(*Pair)
				if !ok {
					m.Raise(errf("assoc", "expected a list of pairs"))
					return
				}
				m.ApplyWith(proc, []Value{obj, entry.Car}, func(m *Machine, v Value) {
					if IsTrue(v) {
						m.Return(entry)
						return
					}
					cur = c.Cdr
					step()
				})
			case Empty:
				m.Return(False)
			default:
				m.Raise(errf("assoc", "expected a proper list"))
			}
		}
		step()
	}, libBase, libR5RS)

	installCxr(m)
}

func installCxr(m *Machine) {
	patterns := []string{}
	for _, n := range []int{2, 3, 4} {
		var gen func(prefix string, depth int)
		gen = func(prefix string, depth int) {
			if depth == 0 {
				patterns = append(patterns, prefix)
				return
			}
			gen(prefix+"a", depth-1)
			gen(prefix+"d", depth-1)
		}
		gen("", n)
	}
	for _, pat := range patterns {
		name := "c" + pat + "r"
		libs := []string{libBase}
		if len(pat) > 2 {
			libs = []string{libCxr}
		} else {
			libs = append(libs, libR5RS)
		}
		pat := pat
		m.defSimple(name, 1, 1, func(a []Value) (Value, error) {
			v := a[0]
			// Apply from the rightmost letter to the leftmost.
			for i := len(pat) - 1; i >= 0; i-- {
				p, ok := v.(*Pair)
				if !ok {
					panic(errf(name, "expected a pair but got %s", WriteToString(v)))
				}
				if pat[i] == 'a' {
					v = p.Car
				} else {
					v = p.Cdr
				}
			}
			return v, nil
		}, libs...)
	}
}

func listCopy(v Value) Value {
	switch x := v.(type) {
	case *Pair:
		return Cons(x.Car, listCopy(x.Cdr))
	case Empty:
		return Nil
	}
	return v
}

func memGeneric(obj, lst Value, eq func(a, b Value) bool, name string) (Value, error) {
	cur := lst
	for {
		switch c := cur.(type) {
		case *Pair:
			if eq(obj, c.Car) {
				return c, nil
			}
			cur = c.Cdr
		case Empty:
			return False, nil
		default:
			panic(errf(name, "expected a proper list but got %s", WriteToString(lst)))
		}
	}
}

func assGeneric(obj, lst Value, eq func(a, b Value) bool, name string) (Value, error) {
	cur := lst
	for {
		switch c := cur.(type) {
		case *Pair:
			entry, ok := c.Car.(*Pair)
			if !ok {
				panic(errf(name, "expected a list of pairs"))
			}
			if eq(obj, entry.Car) {
				return entry, nil
			}
			cur = c.Cdr
		case Empty:
			return False, nil
		default:
			panic(errf(name, "expected a proper list but got %s", WriteToString(lst)))
		}
	}
}
