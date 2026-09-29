// SPDX-License-Identifier: MIT

package scheme

func installVectors(m *Machine) {
	m.defSimple("vector?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Vector)
		return BooleanOf(ok), nil
	}, libBase, libR5RS)
	m.defSimple("make-vector", 1, 2, func(a []Value) (Value, error) {
		n := wantIndex("make-vector", a[0])
		var fill Value = UnspecifiedValue
		if len(a) == 2 {
			fill = a[1]
		}
		v := NewVector(n)
		for i := range v.Items {
			v.Items[i] = fill
		}
		return v, nil
	}, libBase, libR5RS)
	m.defSimple("vector", 0, -1, func(a []Value) (Value, error) {
		return NewVectorFrom(a), nil
	}, libBase, libR5RS)
	m.defSimple("vector-length", 1, 1, func(a []Value) (Value, error) {
		return Int(int64(len(wantVector("vector-length", a[0]).Items))), nil
	}, libBase, libR5RS)
	m.defSimple("vector-ref", 2, 2, func(a []Value) (Value, error) {
		v := wantVector("vector-ref", a[0])
		i := wantIndex("vector-ref", a[1])
		if i >= len(v.Items) {
			panic(errf("vector-ref", "index %d out of range for a vector of length %d", i, len(v.Items)))
		}
		return v.Items[i], nil
	}, libBase, libR5RS)
	m.defSimple("vector-set!", 3, 3, func(a []Value) (Value, error) {
		v := wantVector("vector-set!", a[0])
		i := wantIndex("vector-set!", a[1])
		if i >= len(v.Items) {
			panic(errf("vector-set!", "index out of range"))
		}
		v.Items[i] = a[2]
		return UnspecifiedValue, nil
	}, libBase, libR5RS)
	m.defSimple("vector->list", 1, 3, func(a []Value) (Value, error) {
		v := wantVector("vector->list", a[0])
		start, end := 0, len(v.Items)
		if len(a) > 1 {
			start = wantIndex("vector->list", a[1])
		}
		if len(a) > 2 {
			end = wantIndex("vector->list", a[2])
		}
		if start > end || end > len(v.Items) {
			panic(errf("vector->list", "invalid range"))
		}
		return List(v.Items[start:end]...), nil
	}, libBase, libR5RS)
	m.defSimple("list->vector", 1, 1, func(a []Value) (Value, error) {
		return NewVectorFrom(wantList("list->vector", a[0])), nil
	}, libBase, libR5RS)
	m.defSimple("vector-copy", 1, 3, func(a []Value) (Value, error) {
		v := wantVector("vector-copy", a[0])
		start, end := 0, len(v.Items)
		if len(a) > 1 {
			start = wantIndex("vector-copy", a[1])
		}
		if len(a) > 2 {
			end = wantIndex("vector-copy", a[2])
		}
		if start > end || end > len(v.Items) {
			panic(errf("vector-copy", "invalid range"))
		}
		return NewVectorFrom(v.Items[start:end]), nil
	}, libBase)
	m.defSimple("vector-copy!", 3, 5, func(a []Value) (Value, error) {
		to := wantVector("vector-copy!", a[0])
		at := wantIndex("vector-copy!", a[1])
		from := wantVector("vector-copy!", a[2])
		start, end := 0, len(from.Items)
		if len(a) > 3 {
			start = wantIndex("vector-copy!", a[3])
		}
		if len(a) > 4 {
			end = wantIndex("vector-copy!", a[4])
		}
		if start > end || end > len(from.Items) || at+(end-start) > len(to.Items) {
			panic(errf("vector-copy!", "invalid range"))
		}
		copy(to.Items[at:], from.Items[start:end])
		return UnspecifiedValue, nil
	}, libBase)
	m.defSimple("vector-append", 0, -1, func(a []Value) (Value, error) {
		var items []Value
		for _, v := range a {
			items = append(items, wantVector("vector-append", v).Items...)
		}
		return NewVectorFrom(items), nil
	}, libBase)
	m.defSimple("vector-fill!", 2, 4, func(a []Value) (Value, error) {
		v := wantVector("vector-fill!", a[0])
		start, end := 0, len(v.Items)
		if len(a) > 2 {
			start = wantIndex("vector-fill!", a[2])
		}
		if len(a) > 3 {
			end = wantIndex("vector-fill!", a[3])
		}
		if start > end || end > len(v.Items) {
			panic(errf("vector-fill!", "invalid range"))
		}
		for i := start; i < end; i++ {
			v.Items[i] = a[1]
		}
		return UnspecifiedValue, nil
	}, libBase)

	// ------------------------------------------------------------ bytevectors
	m.defSimple("bytevector?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Bytevector)
		return BooleanOf(ok), nil
	}, libBase)
	m.defSimple("make-bytevector", 1, 2, func(a []Value) (Value, error) {
		n := wantIndex("make-bytevector", a[0])
		var fill byte
		if len(a) == 2 {
			fill = byte(wantU8("make-bytevector", a[1]))
		}
		b := NewBytevector(n)
		for i := range b.Bytes {
			b.Bytes[i] = fill
		}
		return b, nil
	}, libBase)
	m.defSimple("bytevector", 0, -1, func(a []Value) (Value, error) {
		b := NewBytevector(len(a))
		for i, v := range a {
			b.Bytes[i] = byte(wantU8("bytevector", v))
		}
		return b, nil
	}, libBase)
	m.defSimple("bytevector-length", 1, 1, func(a []Value) (Value, error) {
		return Int(int64(len(wantBytevector("bytevector-length", a[0]).Bytes))), nil
	}, libBase)
	m.defSimple("bytevector-u8-ref", 2, 2, func(a []Value) (Value, error) {
		b := wantBytevector("bytevector-u8-ref", a[0])
		i := wantIndex("bytevector-u8-ref", a[1])
		if i >= len(b.Bytes) {
			panic(errf("bytevector-u8-ref", "index out of range"))
		}
		return Int(int64(b.Bytes[i])), nil
	}, libBase)
	m.defSimple("bytevector-u8-set!", 3, 3, func(a []Value) (Value, error) {
		b := wantBytevector("bytevector-u8-set!", a[0])
		i := wantIndex("bytevector-u8-set!", a[1])
		if i >= len(b.Bytes) {
			panic(errf("bytevector-u8-set!", "index out of range"))
		}
		b.Bytes[i] = byte(wantU8("bytevector-u8-set!", a[2]))
		return UnspecifiedValue, nil
	}, libBase)
	m.defSimple("bytevector-copy", 1, 3, func(a []Value) (Value, error) {
		b := wantBytevector("bytevector-copy", a[0])
		start, end := 0, len(b.Bytes)
		if len(a) > 1 {
			start = wantIndex("bytevector-copy", a[1])
		}
		if len(a) > 2 {
			end = wantIndex("bytevector-copy", a[2])
		}
		if start > end || end > len(b.Bytes) {
			panic(errf("bytevector-copy", "invalid range"))
		}
		return NewBytevectorFrom(b.Bytes[start:end]), nil
	}, libBase)
	m.defSimple("bytevector-copy!", 3, 5, func(a []Value) (Value, error) {
		to := wantBytevector("bytevector-copy!", a[0])
		at := wantIndex("bytevector-copy!", a[1])
		from := wantBytevector("bytevector-copy!", a[2])
		start, end := 0, len(from.Bytes)
		if len(a) > 3 {
			start = wantIndex("bytevector-copy!", a[3])
		}
		if len(a) > 4 {
			end = wantIndex("bytevector-copy!", a[4])
		}
		if start > end || end > len(from.Bytes) || at+(end-start) > len(to.Bytes) {
			panic(errf("bytevector-copy!", "invalid range"))
		}
		copy(to.Bytes[at:], from.Bytes[start:end])
		return UnspecifiedValue, nil
	}, libBase)
	m.defSimple("bytevector-append", 0, -1, func(a []Value) (Value, error) {
		var out []byte
		for _, v := range a {
			out = append(out, wantBytevector("bytevector-append", v).Bytes...)
		}
		return NewBytevectorFrom(out), nil
	}, libBase)
}

func wantU8(name string, v Value) int {
	i, ok := v.(*Integer)
	if !ok {
		panic(errf(name, "expected a byte but got %s", WriteToString(v)))
	}
	n, ok := i.Int64()
	if !ok || n < 0 || n > 255 {
		panic(errf(name, "byte value out of range: %s", WriteToString(v)))
	}
	return int(n)
}
