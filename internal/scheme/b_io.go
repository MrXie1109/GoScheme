// SPDX-License-Identifier: MIT

package scheme

import (
	"bufio"
	"io"
)

func (m *Machine) defValue(name string, v Value, libs ...string) Value {
	m.Builtin.DefineName(name, v)
	for _, l := range libs {
		m.addExport(l, name)
	}
	return v
}

func installIO(m *Machine) {
	m.defValue("current-input-port", m.InParam, libBase)
	m.defValue("current-output-port", m.OutParam, libBase)
	m.defValue("current-error-port", m.ErrParam, libBase)

	m.defSimple("eof-object", 0, 0, func(a []Value) (Value, error) {
		return EOFObject, nil
	}, libBase)
	m.defSimple("eof-object?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(EOF)
		return BooleanOf(ok), nil
	}, libBase, libR5RS)

	m.defSimple("port?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Port)
		return BooleanOf(ok), nil
	}, libBase, libR5RS)
	m.defSimple("input-port?", 1, 1, func(a []Value) (Value, error) {
		p, ok := a[0].(*Port)
		return BooleanOf(ok && p.IsInput), nil
	}, libBase, libR5RS)
	m.defSimple("output-port?", 1, 1, func(a []Value) (Value, error) {
		p, ok := a[0].(*Port)
		return BooleanOf(ok && p.IsOut), nil
	}, libBase, libR5RS)
	m.defSimple("textual-port?", 1, 1, func(a []Value) (Value, error) {
		p, ok := a[0].(*Port)
		return BooleanOf(ok && !p.Binary), nil
	}, libBase)
	m.defSimple("binary-port?", 1, 1, func(a []Value) (Value, error) {
		p, ok := a[0].(*Port)
		return BooleanOf(ok && p.Binary), nil
	}, libBase)
	m.defSimple("input-port-open?", 1, 1, func(a []Value) (Value, error) {
		p := wantPort("input-port-open?", a[0])
		return BooleanOf(p.IsInput && !p.closed), nil
	}, libBase)
	m.defSimple("output-port-open?", 1, 1, func(a []Value) (Value, error) {
		p := wantPort("output-port-open?", a[0])
		return BooleanOf(p.IsOut && !p.closed), nil
	}, libBase)
	m.defSimple("close-port", 1, 1, func(a []Value) (Value, error) {
		p := wantPort("close-port", a[0])
		_ = p.Flush()
		_ = p.Close()
		return UnspecifiedValue, nil
	}, libBase, libR5RS)
	m.defSimple("close-input-port", 1, 1, func(a []Value) (Value, error) {
		p := wantInputPort("close-input-port", a[0])
		_ = p.Close()
		return UnspecifiedValue, nil
	}, libBase, libR5RS)
	m.defSimple("close-output-port", 1, 1, func(a []Value) (Value, error) {
		p := wantOutputPort("close-output-port", a[0])
		_ = p.Flush()
		_ = p.Close()
		return UnspecifiedValue, nil
	}, libBase, libR5RS)

	// ------------------------------------------------------- string ports
	m.defSimple("open-input-string", 1, 1, func(a []Value) (Value, error) {
		return NewInputStringPort(wantString("open-input-string", a[0]).Value()), nil
	}, libBase)
	m.defSimple("open-output-string", 0, 0, func(a []Value) (Value, error) {
		return NewOutputStringPort(), nil
	}, libBase)
	m.defSimple("get-output-string", 1, 1, func(a []Value) (Value, error) {
		p := wantPort("get-output-string", a[0])
		return NewString(p.OutputString()), nil
	}, libBase)
	m.defSimple("open-input-bytevector", 1, 1, func(a []Value) (Value, error) {
		return NewInputBytevectorPort(wantBytevector("open-input-bytevector", a[0]).Bytes), nil
	}, libBase)
	m.defSimple("open-output-bytevector", 0, 0, func(a []Value) (Value, error) {
		return NewOutputBytevectorPort(), nil
	}, libBase)
	m.defSimple("get-output-bytevector", 1, 1, func(a []Value) (Value, error) {
		p := wantPort("get-output-bytevector", a[0])
		return NewBytevectorFrom(p.OutputBytes()), nil
	}, libBase)

	// ------------------------------------------------------- input
	m.defSimple("read-char", 0, 1, func(a []Value) (Value, error) {
		p := m.CurIn
		if len(a) == 1 {
			p = wantTextual("read-char", a[0])
			if !p.IsInput {
				panic(errf("read-char", "expected an input port"))
			}
		}
		r, err := p.ReadChar()
		if err != nil {
			if err == io.EOF || err == bufio.ErrBufferFull {
				return EOFObject, nil
			}
			return nil, err
		}
		return Char(r), nil
	}, libBase, libR5RS)
	m.defSimple("peek-char", 0, 1, func(a []Value) (Value, error) {
		p := m.CurIn
		if len(a) == 1 {
			p = wantTextual("peek-char", a[0])
			if !p.IsInput {
				panic(errf("peek-char", "expected an input port"))
			}
		}
		r, err := p.PeekChar()
		if err != nil {
			if err == io.EOF {
				return EOFObject, nil
			}
			return nil, err
		}
		return Char(r), nil
	}, libBase, libR5RS)
	m.defSimple("read-line", 0, 1, func(a []Value) (Value, error) {
		p := m.CurIn
		if len(a) == 1 {
			p = wantTextual("read-line", a[0])
			if !p.IsInput {
				panic(errf("read-line", "expected an input port"))
			}
		}
		line, err := p.LineRead()
		if err != nil {
			if err == io.EOF {
				return EOFObject, nil
			}
			return nil, err
		}
		return NewStringFromRunes(line), nil
	}, libBase)
	m.defSimple("read-string", 1, 2, func(a []Value) (Value, error) {
		k := wantIndex("read-string", a[0])
		p := m.CurIn
		if len(a) == 2 {
			p = wantTextual("read-string", a[1])
			if !p.IsInput {
				panic(errf("read-string", "expected an input port"))
			}
		}
		if k == 0 {
			return NewString(""), nil
		}
		rs, err := p.ReadChars(k)
		if err != nil {
			if err == io.EOF {
				return EOFObject, nil
			}
			return nil, err
		}
		return NewStringFromRunes(rs), nil
	}, libBase)
	m.defSimple("char-ready?", 0, 1, func(a []Value) (Value, error) {
		p := m.CurIn
		if len(a) == 1 {
			p = wantTextual("char-ready?", a[0])
		}
		return BooleanOf(p.Ready()), nil
	}, libBase, libR5RS)
	m.defSimple("read-u8", 0, 1, func(a []Value) (Value, error) {
		p := m.CurIn
		if len(a) == 1 {
			p = wantInputPort("read-u8", a[0])
		}
		b, err := p.ReadByte()
		if err != nil {
			if err == io.EOF {
				return EOFObject, nil
			}
			return nil, err
		}
		return Int(int64(b)), nil
	}, libBase)
	m.defSimple("peek-u8", 0, 1, func(a []Value) (Value, error) {
		p := m.CurIn
		if len(a) == 1 {
			p = wantInputPort("peek-u8", a[0])
		}
		b, err := p.PeekByte()
		if err != nil {
			if err == io.EOF {
				return EOFObject, nil
			}
			return nil, err
		}
		return Int(int64(b)), nil
	}, libBase)
	m.defSimple("u8-ready?", 0, 1, func(a []Value) (Value, error) {
		p := m.CurIn
		if len(a) == 1 {
			p = wantInputPort("u8-ready?", a[0])
		}
		return BooleanOf(p.Ready()), nil
	}, libBase)
	m.defSimple("read-bytevector", 1, 2, func(a []Value) (Value, error) {
		k := wantIndex("read-bytevector", a[0])
		p := m.CurIn
		if len(a) == 2 {
			p = wantInputPort("read-bytevector", a[1])
		}
		if k == 0 {
			return NewBytevector(0), nil
		}
		bs, err := p.ReadBytes(k)
		if err != nil {
			if err == io.EOF {
				return EOFObject, nil
			}
			return nil, err
		}
		return NewBytevectorFrom(bs), nil
	}, libBase)
	m.defSimple("read-bytevector!", 1, 4, func(a []Value) (Value, error) {
		bv := wantBytevector("read-bytevector!", a[0])
		p := m.CurIn
		start, end := 0, len(bv.Bytes)
		if len(a) > 1 {
			p = wantInputPort("read-bytevector!", a[1])
		}
		if len(a) > 2 {
			start = wantIndex("read-bytevector!", a[2])
		}
		if len(a) > 3 {
			end = wantIndex("read-bytevector!", a[3])
		}
		if start > end || end > len(bv.Bytes) {
			panic(errf("read-bytevector!", "invalid range"))
		}
		if start == end {
			return Int(0), nil
		}
		bs, err := p.ReadBytes(end - start)
		if err != nil {
			if err == io.EOF {
				return EOFObject, nil
			}
			return nil, err
		}
		copy(bv.Bytes[start:], bs)
		return Int(int64(len(bs))), nil
	}, libBase)

	// ------------------------------------------------------- output
	m.defSimple("newline", 0, 1, func(a []Value) (Value, error) {
		p := m.CurOut
		if len(a) == 1 {
			p = wantOutputPort("newline", a[0])
		}
		if err := p.WriteRune('\n'); err != nil {
			return nil, NewFileError(err.Error())
		}
		return UnspecifiedValue, nil
	}, libBase, libR5RS)
	m.defSimple("write-char", 1, 2, func(a []Value) (Value, error) {
		p := m.CurOut
		if len(a) == 2 {
			p = wantOutputPort("write-char", a[1])
		}
		if err := p.WriteRune(rune(wantChar("write-char", a[0]))); err != nil {
			return nil, NewFileError(err.Error())
		}
		return UnspecifiedValue, nil
	}, libBase, libR5RS)
	m.defSimple("write-string", 1, 4, func(a []Value) (Value, error) {
		s := wantString("write-string", a[0])
		p := m.CurOut
		start, end := 0, s.Len()
		if len(a) > 1 {
			p = wantOutputPort("write-string", a[1])
		}
		if len(a) > 2 {
			start = wantIndex("write-string", a[2])
		}
		if len(a) > 3 {
			end = wantIndex("write-string", a[3])
		}
		if start > end || end > s.Len() {
			panic(errf("write-string", "invalid range"))
		}
		if err := p.WriteStr(string(s.Runes[start:end])); err != nil {
			return nil, NewFileError(err.Error())
		}
		return UnspecifiedValue, nil
	}, libBase)
	m.defSimple("write-u8", 1, 2, func(a []Value) (Value, error) {
		b := byte(wantU8("write-u8", a[0]))
		p := m.CurOut
		if len(a) == 2 {
			p = wantOutputPort("write-u8", a[1])
		}
		if err := p.WriteBytes([]byte{b}); err != nil {
			return nil, NewFileError(err.Error())
		}
		return UnspecifiedValue, nil
	}, libBase)
	m.defSimple("write-bytevector", 1, 4, func(a []Value) (Value, error) {
		bv := wantBytevector("write-bytevector", a[0])
		p := m.CurOut
		start, end := 0, len(bv.Bytes)
		if len(a) > 1 {
			p = wantOutputPort("write-bytevector", a[1])
		}
		if len(a) > 2 {
			start = wantIndex("write-bytevector", a[2])
		}
		if len(a) > 3 {
			end = wantIndex("write-bytevector", a[3])
		}
		if start > end || end > len(bv.Bytes) {
			panic(errf("write-bytevector", "invalid range"))
		}
		if err := p.WriteBytes(bv.Bytes[start:end]); err != nil {
			return nil, NewFileError(err.Error())
		}
		return UnspecifiedValue, nil
	}, libBase)
	m.defSimple("flush-output-port", 0, 1, func(a []Value) (Value, error) {
		p := m.CurOut
		if len(a) == 1 {
			p = wantOutputPort("flush-output-port", a[0])
		}
		if err := p.Flush(); err != nil {
			return nil, NewFileError(err.Error())
		}
		return UnspecifiedValue, nil
	}, libBase)

	// ------------------------------------------------------- read / write
	m.def("read", 0, 1, func(m *Machine, a []Value) {
		p := m.CurIn
		if len(a) == 1 {
			p = wantTextual("read", a[0])
			if !p.IsInput {
				m.Raise(errf("read", "expected an input port"))
				return
			}
		}
		r := NewReader(p)
		r.Source = p.Name
		v, err := r.Read()
		if err == io.EOF {
			m.Return(EOFObject)
			return
		}
		if err != nil {
			m.RaiseError(err)
			return
		}
		m.Return(v)
	}, libBase, libRead, libR5RS)
	m.defSimple("write", 1, 2, func(a []Value) (Value, error) {
		p := m.CurOut
		if len(a) == 2 {
			p = wantOutputPort("write", a[1])
		}
		if err := p.WriteStr(WriteToString(a[0])); err != nil {
			return nil, NewFileError(err.Error())
		}
		return UnspecifiedValue, nil
	}, libBase, libWrite, libR5RS)
	m.defSimple("write-shared", 1, 2, func(a []Value) (Value, error) {
		p := m.CurOut
		if len(a) == 2 {
			p = wantOutputPort("write-shared", a[1])
		}
		if err := p.WriteStr(WriteSharedToString(a[0])); err != nil {
			return nil, NewFileError(err.Error())
		}
		return UnspecifiedValue, nil
	}, libBase, libWrite)
	m.defSimple("write-simple", 1, 2, func(a []Value) (Value, error) {
		p := m.CurOut
		if len(a) == 2 {
			p = wantOutputPort("write-simple", a[1])
		}
		if err := p.WriteStr(WriteSimpleToString(a[0])); err != nil {
			return nil, NewFileError(err.Error())
		}
		return UnspecifiedValue, nil
	}, libBase, libWrite)
	m.defSimple("display", 1, 2, func(a []Value) (Value, error) {
		p := m.CurOut
		if len(a) == 2 {
			p = wantOutputPort("display", a[1])
		}
		if err := p.WriteStr(DisplayToString(a[0])); err != nil {
			return nil, NewFileError(err.Error())
		}
		return UnspecifiedValue, nil
	}, libBase, libWrite, libR5RS)
}
