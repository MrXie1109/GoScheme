package scheme

// installControl provides the procedure-calling procedures: apply, map,
// continuations, multiple values, exceptions and promises.
func installControl(m *Machine) {
	m.def("apply", 2, -1, func(m *Machine, a []Value) {
		proc := wantProcedure("apply", a[0])
		flat := append([]Value{}, a[1:len(a)-1]...)
		last, ok := ListToSlice(a[len(a)-1])
		if !ok {
			m.Raise(errf("apply", "last argument must be a proper list"))
			return
		}
		flat = append(flat, last...)
		m.apply(proc, flat)
	}, libBase, libR5RS)

	m.def("map", 2, -1, func(m *Machine, a []Value) { mapLists(m, a, "map", true) }, libBase, libR5RS)
	m.def("for-each", 2, -1, func(m *Machine, a []Value) { mapLists(m, a, "for-each", false) }, libBase, libR5RS)

	m.def("string-map", 2, -1, func(m *Machine, a []Value) { mapStrings(m, a, "string-map", true) }, libBase)
	m.def("string-for-each", 2, -1, func(m *Machine, a []Value) { mapStrings(m, a, "string-for-each", false) }, libBase)
	m.def("vector-map", 2, -1, func(m *Machine, a []Value) { mapVectors(m, a, "vector-map", true) }, libBase)
	m.def("vector-for-each", 2, -1, func(m *Machine, a []Value) { mapVectors(m, a, "vector-for-each", false) }, libBase)

	// ------------------------------------------------------- continuations
	m.def("call-with-current-continuation", 1, 1, func(m *Machine, a []Value) {
		proc := wantProcedure("call-with-current-continuation", a[0])
		k := &Continuation{
			stack: append([]frame(nil), m.stack...),
			winds: append([]*windFrame(nil), m.winds...),
			hands: append([]*handlerFrame(nil), m.hands...),
			owner: m,
		}
		m.apply(proc, []Value{k})
	}, libBase, libR5RS)
	m.def("call/cc", 1, 1, func(m *Machine, a []Value) {
		proc := wantProcedure("call/cc", a[0])
		k := &Continuation{
			stack: append([]frame(nil), m.stack...),
			winds: append([]*windFrame(nil), m.winds...),
			hands: append([]*handlerFrame(nil), m.hands...),
			owner: m,
		}
		m.apply(proc, []Value{k})
	}, libBase, libR5RS)

	m.defSimple("values", 0, -1, func(a []Value) (Value, error) {
		if len(a) == 1 {
			return a[0], nil
		}
		return &MultipleValues{Values: append([]Value(nil), a...)}, nil
	}, libBase, libR5RS)
	m.def("call-with-values", 2, 2, func(m *Machine, a []Value) {
		producer := wantProcedure("call-with-values", a[0])
		consumer := wantProcedure("call-with-values", a[1])
		m.ApplyWithMulti(producer, nil, func(m *Machine, vs []Value) {
			m.apply(consumer, vs)
		})
	}, libBase, libR5RS)

	// ------------------------------------------------------- dynamic-wind
	m.def("dynamic-wind", 3, 3, func(m *Machine, a []Value) {
		before := wantProcedure("dynamic-wind", a[0])
		thunk := wantProcedure("dynamic-wind", a[1])
		after := wantProcedure("dynamic-wind", a[2])
		m.stack = append(m.stack, &fDynamicWindPush{before: before, after: after, thunk: thunk})
		m.apply(before, nil)
	}, libBase, libR5RS)

	// ------------------------------------------------------- exceptions
	m.def("with-exception-handler", 2, 2, func(m *Machine, a []Value) {
		handler := wantProcedure("with-exception-handler", a[0])
		thunk := wantProcedure("with-exception-handler", a[1])
		m.hands = append(m.hands, &handlerFrame{proc: handler})
		saved := len(m.hands)
		m.stack = append(m.stack, &fGeneric{fn: func(m *Machine, v Value) {
			if len(m.hands) >= saved {
				m.hands = m.hands[:saved-1]
			}
			m.Return(v)
		}})
		m.apply(thunk, nil)
	}, libBase)
	m.def("raise", 1, 1, func(m *Machine, a []Value) {
		m.Raise(a[0])
	}, libBase)
	m.def("raise-continuable", 1, 1, func(m *Machine, a []Value) {
		m.pending = &SchemeError{Condition: a[0], Continuable: true}
	}, libBase)

	// ------------------------------------------------------- parameters
	m.def("make-parameter", 1, 2, func(m *Machine, a []Value) {
		p := &Parameter{Name: "parameter"}
		if len(a) == 2 && !IsFalse(a[1]) {
			converter := wantProcedure("make-parameter", a[1])
			p.Converter = converter
			m.ApplyWith(converter, []Value{a[0]}, func(m *Machine, v Value) {
				p.values = []Value{v}
				m.Return(p)
			})
			return
		}
		if len(a) == 2 {
			p.Converter = False
		}
		p.values = []Value{a[0]}
		m.Return(p)
	}, libBase)

	// ------------------------------------------------------- promises
	m.def("force", 1, 1, func(m *Machine, a []Value) {
		p, ok := a[0].(*Promise)
		if !ok {
			m.Return(a[0])
			return
		}
		if p.Done {
			m.Return(p.Value)
			return
		}
		m.stack = append(m.stack, &fForce{p: p})
		m.apply(p.Thunk, nil)
	}, libBase, libR5RS)
	m.defSimple("promise?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Promise)
		return BooleanOf(ok), nil
	}, libBase)
	m.defSimple("make-promise", 1, 1, func(a []Value) (Value, error) {
		if p, ok := a[0].(*Promise); ok {
			return p, nil
		}
		return &Promise{Done: true, Value: a[0]}, nil
	}, libBase)

	m.def("call-with-port", 2, 2, func(m *Machine, a []Value) {
		p := wantPort("call-with-port", a[0])
		proc := wantProcedure("call-with-port", a[1])
		m.ApplyWith(proc, []Value{p}, func(m *Machine, v Value) {
			_ = p.Close()
			m.Return(v)
		})
	}, libBase)
}

// fForce implements force, including the iterative chaining required by
// delay-force.
type fForce struct {
	p     *Promise
	chain []*Promise
}

func (f *fForce) resume(m *Machine, v Value) {
	if f.p.IsDelayForce {
		if inner, ok := v.(*Promise); ok && !inner.Done {
			f.chain = append(f.chain, f.p)
			f.p = inner
			m.stack = append(m.stack, f)
			m.apply(inner.Thunk, nil)
			return
		}
	}
	f.p.Done = true
	f.p.Value = v
	for i := len(f.chain) - 1; i >= 0; i-- {
		f.chain[i].Done = true
		f.chain[i].Value = v
	}
	m.Return(v)
}

func mapLists(m *Machine, a []Value, name string, collect bool) {
	proc := wantProcedure(name, a[0])
	lists := append([]Value(nil), a[1:]...)
	if len(lists) == 0 {
		m.Raise(errf(name, "expected at least one list"))
		return
	}
	var result []Value
	var step func()
	step = func() {
		cars := make([]Value, len(lists))
		next := make([]Value, len(lists))
		for i, l := range lists {
			p, ok := l.(*Pair)
			if !ok {
				if _, isNil := l.(Empty); isNil {
					finishMap(m, name, collect, result)
					return
				}
				m.Raise(errf(name, "expected a proper list but got %s", WriteToString(l)))
				return
			}
			cars[i] = p.Car
			next[i] = p.Cdr
		}
		lists = next
		if collect {
			m.ApplyWith(proc, cars, func(m *Machine, v Value) {
				result = append(result, v)
				step()
			})
		} else {
			m.ApplyWith(proc, cars, func(m *Machine, v Value) { step() })
		}
	}
	step()
}

func finishMap(m *Machine, name string, collect bool, result []Value) {
	if collect {
		m.Return(List(result...))
		return
	}
	m.Return(UnspecifiedValue)
}

func mapStrings(m *Machine, a []Value, name string, collect bool) {
	proc := wantProcedure(name, a[0])
	strs := make([]*String, len(a)-1)
	for i, v := range a[1:] {
		strs[i] = wantString(name, v)
	}
	n := -1
	for _, s := range strs {
		if n < 0 || s.Len() < n {
			n = s.Len()
		}
	}
	if n < 0 {
		n = 0
	}
	var out []rune
	i := 0
	var step func()
	step = func() {
		if i >= n {
			if collect {
				m.Return(NewStringFromRunes(out))
			} else {
				m.Return(UnspecifiedValue)
			}
			return
		}
		chars := make([]Value, len(strs))
		for j, s := range strs {
			chars[j] = Char(s.Runes[i])
		}
		i++
		if collect {
			m.ApplyWith(proc, chars, func(m *Machine, v Value) {
				out = append(out, rune(wantChar(name, v)))
				step()
			})
		} else {
			m.ApplyWith(proc, chars, func(m *Machine, v Value) { step() })
		}
	}
	step()
}

func mapVectors(m *Machine, a []Value, name string, collect bool) {
	proc := wantProcedure(name, a[0])
	vecs := make([]*Vector, len(a)-1)
	for i, v := range a[1:] {
		vecs[i] = wantVector(name, v)
	}
	n := -1
	for _, v := range vecs {
		if n < 0 || len(v.Items) < n {
			n = len(v.Items)
		}
	}
	if n < 0 {
		n = 0
	}
	var out []Value
	i := 0
	var step func()
	step = func() {
		if i >= n {
			if collect {
				m.Return(NewVectorFrom(out))
			} else {
				m.Return(UnspecifiedValue)
			}
			return
		}
		items := make([]Value, len(vecs))
		for j, v := range vecs {
			items[j] = v.Items[i]
		}
		i++
		if collect {
			m.ApplyWith(proc, items, func(m *Machine, v Value) {
				out = append(out, v)
				step()
			})
		} else {
			m.ApplyWith(proc, items, func(m *Machine, v Value) { step() })
		}
	}
	step()
}
