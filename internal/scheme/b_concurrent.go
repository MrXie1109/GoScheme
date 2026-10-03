// SPDX-License-Identifier: MIT

package scheme

import (
	"reflect"
	"time"
)

// ---------------------------------------------------------------------------
// Go flavoured concurrency
//
// Channels, (go ...) and (select ...) map onto Go's own concurrency
// primitives.  Every Scheme thread is a goroutine running its own Machine;
// they share the global environment, the ports and the standard parameters,
// all of which are internally synchronised.  Ordinary Scheme data (pairs,
// strings, vectors, records) is *not* synchronised, exactly as in Go: share
// memory by communicating.
// ---------------------------------------------------------------------------

const libChannel = "(goscheme channel)"

// isClosed reports whether the channel has been closed.
func (c *Channel) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// markClosed closes the channel once; it reports whether this call closed it.
func (c *Channel) markClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.closed = true
	close(c.ch)
	return true
}

// ---------------------------------------------------------------------------
// (go body ...) and helpers
// ---------------------------------------------------------------------------

// Child returns a machine that shares this machine's global environment,
// libraries, ports and standard parameters but has its own continuation
// stack.  It is the interpreter thread created by (go ...).
func (m *Machine) Child() *Machine {
	return &Machine{
		Global:    m.Global,
		Builtin:   m.Builtin,
		Libraries: m.Libraries,
		libMu:     m.libMu,
		// The load path is copied so that a thread (or a library being
		// loaded) cannot disturb the directories its parent is searching.
		LoadPath: append([]string(nil), m.LoadPath...),
		Args:     m.Args,
		CurIn:    m.CurIn,
		CurOut:   m.CurOut,
		CurErr:   m.CurErr,
		InParam:  m.InParam,
		OutParam: m.OutParam,
		ErrParam: m.ErrParam,
		// Libraries and the set of libraries being loaded are shared: a cycle
		// must be visible across the threads that load a chain of libraries.
		libExports: m.libExports,
		libLoading: m.libLoading,
		wg:         m.wg,
	}
}

// RunApply applies proc to args on this machine, driving the machine until the
// application completes.  It is used to start an interpreter thread.
func (m *Machine) RunApply(proc Value, args []Value, env *Env) (Value, error) {
	return m.guardedRun(func() (Value, error) {
		// The state is cleared first, so the run starts from empty: the base
		// must be measured before apply, which may push frames of its own.
		m.stack = m.stack[:0]
		m.winds = m.winds[:0]
		m.hands = m.hands[:0]
		baseStack, baseWinds, baseHands := len(m.stack), len(m.winds), len(m.hands)
		m.apply(proc, args)
		return m.runLoop(baseStack, baseWinds, baseHands)
	})
}

// Spawn runs the thunk on a fresh interpreter thread.
func (m *Machine) Spawn(thunk Value) {
	child := m.Child()
	m.wg.Add(1)
	// Count the thread before it starts, so that environments begin taking
	// their locks before it can touch one.
	enterConcurrency()
	go func() {
		defer exitConcurrency()
		defer m.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				child.reportUncaught(r)
			}
		}()
		if _, err := child.RunApply(thunk, nil, child.Global); err != nil {
			child.reportUncaught(err)
		}
	}()
}

// reportUncaught prints an error raised by a thread to the error port instead
// of taking the whole interpreter down.
func (m *Machine) reportUncaught(r interface{}) {
	var msg string
	switch e := r.(type) {
	case *SchemeError:
		msg = e.Error()
	case *ErrorObject:
		msg = e.Error()
	case *ExitError:
		// (exit) inside a thread only ends that thread.
		return
	case error:
		msg = e.Error()
	default:
		msg = WriteToString(r)
	}
	if m.CurErr != nil {
		_ = m.CurErr.WriteStr("go: uncaught error: " + msg + "\n")
	}
}

// evalGo implements (go body ...): spawn a Scheme thread running the body.
func evalGo(m *Machine, form Value, env *Env) {
	body := formArgs(form)
	if len(body) == 0 {
		m.Raise(NewError("go: expected a body"))
		return
	}
	m.Spawn(makeThunk(body, env))
	m.Return(UnspecifiedValue)
}

// ---------------------------------------------------------------------------
// (select ...)
// ---------------------------------------------------------------------------

const (
	selRecv = iota
	selSend
	selAfter
	selElse
)

// selectClause is one `op => handler` clause of a select form.  The op and
// handler are expressions until evalSelect has evaluated them.
type selectClause struct {
	kind    int
	chExpr  Value
	valExpr Value
	msExpr  Value
	handler Value

	channel *Channel
	sendVal Value
	timeout time.Duration
}

// arity is the number of sub-expressions the clause contributes.
func (c *selectClause) arity() int {
	switch c.kind {
	case selSend:
		return 3
	case selRecv, selAfter:
		return 2
	default:
		return 1
	}
}

// evalSelect implements
//
//	(select
//	  (chan-recv! ch)   => (lambda (v) ...)
//	  (chan-send! ch v) => (lambda () ...)
//	  (after ms)        => (lambda () ...)
//	  (else)            => (lambda () ...))
//
// The clauses form a flat sequence of `operation => handler` triples.  Every
// channel expression, send value and handler is evaluated first, then the
// ready operations are raced exactly like Go's select statement.
func evalSelect(m *Machine, form Value, env *Env) {
	items := formArgs(form)
	if len(items) == 0 {
		// Go's `select {}` blocks forever.
		select {}
	}
	specs, err := selectSpecs(items)
	if err != nil {
		m.RaiseError(err)
		return
	}
	var clauses []*selectClause
	var exprs []Value
	for _, sp := range specs {
		clauses = append(clauses, &selectClause{kind: sp.kind})
		exprs = append(exprs, sp.exprs...)
	}
	m.EvalList(exprs, env, func(m *Machine, vals []Value) {
		m.runSelect(clauses, vals)
	})
}

// selectSpec is one clause of a select form before its expressions have been
// evaluated: which operation it is, and the expressions it needs, in the order
// they are evaluated.  The interpreter evaluates them itself and the compiler
// turns each into a thunk; sharing this parse is what keeps the two orders the
// same.
type selectSpec struct {
	kind  int
	exprs []Value
}

// selectSpecs parses the clauses of a select, which are written (operation) =>
// handler.
func selectSpecs(items []Value) ([]selectSpec, error) {
	if len(items)%3 != 0 {
		return nil, NewError("select: clauses are written as (operation) => handler")
	}
	var specs []selectSpec
	for i := 0; i < len(items); i += 3 {
		arrow, ok := items[i+1].(*Symbol)
		if !ok || arrow.Name != "=>" {
			return nil, NewError("select: expected => between an operation and its handler", items[i+1])
		}
		handler := items[i+2]
		sp := selectSpec{}
		switch op := items[i].(type) {
		case *Symbol:
			// A bare `else` is accepted as well as `(else)`.
			if op.Name != "else" {
				return nil, NewError("select: unsupported operation", op)
			}
			sp.kind = selElse
		case *Pair:
			head, _ := op.Car.(*Symbol)
			if head == nil {
				return nil, NewError("select: malformed operation", op)
			}
			opArgs := mustSlice(op.Cdr)
			switch head.Name {
			case "chan-recv!":
				if len(opArgs) != 1 {
					return nil, NewError("select: (chan-recv! channel) expected", op)
				}
				sp.kind, sp.exprs = selRecv, []Value{opArgs[0]}
			case "chan-send!":
				if len(opArgs) != 2 {
					return nil, NewError("select: (chan-send! channel value) expected", op)
				}
				sp.kind, sp.exprs = selSend, []Value{opArgs[0], opArgs[1]}
			case "after":
				if len(opArgs) != 1 {
					return nil, NewError("select: (after milliseconds) expected", op)
				}
				sp.kind, sp.exprs = selAfter, []Value{opArgs[0]}
			case "else":
				if len(opArgs) != 0 {
					return nil, NewError("select: (else) takes no arguments", op)
				}
				sp.kind = selElse
			default:
				return nil, NewError("select: unsupported operation", head)
			}
		default:
			return nil, NewError("select: malformed operation", items[i])
		}
		sp.exprs = append(sp.exprs, handler)
		specs = append(specs, sp)
	}
	return specs, nil
}

func (m *Machine) runSelect(clauses []*selectClause, vals []Value) {
	i := 0
	next := func() Value {
		v := vals[i]
		i++
		return v
	}
	seenAfter, seenElse := 0, 0
	for _, cl := range clauses {
		switch cl.kind {
		case selRecv:
			chv := next()
			cl.handler = next()
			ch, ok := chv.(*Channel)
			if !ok {
				m.Raise(errf("select", "chan-recv!: expected a channel but got %s", WriteToString(chv)))
				return
			}
			cl.channel = ch
		case selSend:
			chv := next()
			sv := next()
			cl.handler = next()
			ch, ok := chv.(*Channel)
			if !ok {
				m.Raise(errf("select", "chan-send!: expected a channel but got %s", WriteToString(chv)))
				return
			}
			if ch.isClosed() {
				m.Raise(errf("select", "chan-send!: channel is closed"))
				return
			}
			cl.channel, cl.sendVal = ch, sv
		case selAfter:
			ms := next()
			cl.handler = next()
			seenAfter++
			if seenAfter > 1 {
				m.Raise(NewError("select: more than one (after ...) clause"))
				return
			}
			cl.timeout = time.Duration(wantIndexIn("select", ms)) * time.Millisecond
		default:
			cl.handler = next()
			seenElse++
			if seenElse > 1 {
				m.Raise(NewError("select: more than one (else) clause"))
				return
			}
		}
	}
	if m.pending != nil {
		return
	}

	cases := make([]reflect.SelectCase, 0, len(clauses))
	for _, cl := range clauses {
		switch cl.kind {
		case selRecv:
			cases = append(cases, reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: reflect.ValueOf(cl.channel.ch),
			})
		case selSend:
			cases = append(cases, reflect.SelectCase{
				Dir:  reflect.SelectSend,
				Chan: reflect.ValueOf(cl.channel.ch),
				Send: reflect.ValueOf(cl.sendVal),
			})
		case selAfter:
			cases = append(cases, reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: reflect.ValueOf(time.After(cl.timeout)),
			})
		default:
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectDefault})
		}
	}

	chosen, recv, recvOK := selectCases(cases)
	if chosen < 0 {
		m.Raise(NewError("select: concurrent send on a closed channel"))
		return
	}
	pc := clauses[chosen]
	if pc.kind == selRecv {
		var v Value = UnspecifiedValue
		if recvOK {
			if got := recv.Interface(); got != nil {
				v = got.(Value)
			}
		}
		m.ApplyWith(pc.handler, []Value{v}, func(m *Machine, v Value) { m.Return(v) })
		return
	}
	m.ApplyWith(pc.handler, nil, func(m *Machine, v Value) { m.Return(v) })
}

// selectCases wraps reflect.Select so that a send racing with a close is
// reported as a Scheme error rather than a Go panic.
func selectCases(cases []reflect.SelectCase) (chosen int, recv reflect.Value, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			chosen = -1
		}
	}()
	return reflect.Select(cases)
}

func wantIndexIn(name string, v Value) int {
	if i, ok := v.(*Integer); ok {
		if n, ok := i.Int64(); ok && n >= 0 {
			return int(n)
		}
	}
	panic(errf(name, "expected a non-negative exact integer but got %s", WriteToString(v)))
}

// ---------------------------------------------------------------------------
// Procedures
// ---------------------------------------------------------------------------

func installConcurrency(m *Machine) {
	m.defSimple("make-channel", 0, 1, func(a []Value) (Value, error) {
		capacity := 0
		if len(a) == 1 {
			capacity = wantIndex("make-channel", a[0])
		}
		return &Channel{Name: "channel", capacity: capacity, ch: make(chan Value, capacity)}, nil
	}, libChannel)

	// (nil-channel) is the Scheme spelling of Go's nil channel: it is never
	// ready, so sending and receiving block for ever and select never picks
	// it.  That is how a program disables one of its own select clauses:
	//
	//   (set! ch (nil-channel))
	//
	// A closed channel, by contrast, is *always* ready — a receive takes the
	// zero value and a send raises — which is exactly why the nil channel is
	// the thing to assign.
	m.defSimple("nil-channel", 0, 0, func(a []Value) (Value, error) {
		return &Channel{Name: "nil channel"}, nil
	}, libChannel)

	m.defSimple("nil-channel?", 1, 1, func(a []Value) (Value, error) {
		c, ok := a[0].(*Channel)
		return BooleanOf(ok && c.ch == nil), nil
	}, libChannel)

	m.defSimple("channel?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Channel)
		return BooleanOf(ok), nil
	}, libChannel)

	// The operations on a channel are chan-something, so this is chan-open?.
	// channel-open? was what 2.5.0 shipped; the alias was removed in 3.1.2, as
	// the notes for 2.6.0 said it would be.
	m.defSimple("chan-open?", 1, 1, func(a []Value) (Value, error) {
		c, ok := a[0].(*Channel)
		if !ok {
			panic(errf("chan-open?", "expected a channel but got %s", WriteToString(a[0])))
		}
		return BooleanOf(!c.isClosed()), nil
	}, libChannel)

	m.def("chan-send!", 2, 2, func(m *Machine, a []Value) {
		c, ok := a[0].(*Channel)
		if !ok {
			m.Raise(errf("chan-send!", "expected a channel but got %s", WriteToString(a[0])))
			return
		}
		if err := channelSend(c, a[1]); err != nil {
			m.RaiseError(err)
			return
		}
		m.Return(UnspecifiedValue)
	}, libChannel)

	m.def("chan-recv!", 1, 1, func(m *Machine, a []Value) {
		c, ok := a[0].(*Channel)
		if !ok {
			m.Raise(errf("chan-recv!", "expected a channel but got %s", WriteToString(a[0])))
			return
		}
		v, ok2 := <-c.ch
		if !ok2 {
			m.Return(&MultipleValues{Values: []Value{UnspecifiedValue, False}})
			return
		}
		if v == nil {
			v = UnspecifiedValue
		}
		m.Return(&MultipleValues{Values: []Value{v, True}})
	}, libChannel)

	m.defSimple("chan-close!", 1, 1, func(a []Value) (Value, error) {
		c, ok := a[0].(*Channel)
		if !ok {
			panic(errf("chan-close!", "expected a channel but got %s", WriteToString(a[0])))
		}
		// Closing a nil channel would be a Go panic ("close of nil channel"),
		// so it is an ordinary condition instead.
		if c.ch == nil {
			panic(errf("chan-close!", "a nil channel cannot be closed"))
		}
		// Closing an already closed channel is a no-op, so that the
		// "close when done" pattern composes with guard.
		c.markClosed()
		return UnspecifiedValue, nil
	}, libChannel)

	m.defSimple("go-wait", 0, 0, func(a []Value) (Value, error) {
		m.wg.Wait()
		return UnspecifiedValue, nil
	}, libChannel)
}

// channelSend sends v, converting a send racing with a close into an error
// instead of a panic.
func channelSend(c *Channel, v Value) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = NewError("chan-send!: channel is closed")
		}
	}()
	if c.isClosed() {
		return NewError("chan-send!: channel is closed")
	}
	c.ch <- v
	return nil
}
