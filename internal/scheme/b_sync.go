// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"sync"
	"sync/atomic"
)

// The (goscheme sync) library is the other half of the concurrency story.
// Channels are the idiomatic way to share state in this dialect — the README
// says so, and the examples show it — but a lock is sometimes the honest tool,
// and Go's sync package is right there underneath.
//
// Every object here is a Go value wrapped for Scheme, so the lock a Scheme
// thread takes is a real Go mutex: it may be taken in one interpreter thread
// and released in another, which is what (go ...) needs.

// Mutex is a mutual-exclusion lock.  It has no owner: any thread may unlock it,
// which makes it a building block rather than a safe-guarded region.
type Mutex struct {
	mu sync.Mutex
}

// SchemeDescribe prints the lock; see re.Describer.
func (*Mutex) SchemeDescribe(Printer) string { return "#<mutex>" }

// WaitGroup waits for a collection of interpreter threads to finish.
type WaitGroup struct {
	mu    sync.Mutex
	count int64
	wg    sync.WaitGroup
}

// SchemeDescribe prints the group; see re.Describer.
func (w *WaitGroup) SchemeDescribe(Printer) string {
	return fmt.Sprintf("#<waitgroup count=%d>", w.count)
}

// Once runs a thunk the first time it is asked, and remembers the value.
type Once struct {
	mu   sync.Mutex
	done bool
	val  Value
}

// SchemeDescribe prints the once; see re.Describer.
func (o *Once) SchemeDescribe(Printer) string {
	state := "not run"
	if o.done {
		state = "done"
	}
	return fmt.Sprintf("#<once %s>", state)
}

// Atomic is an exact integer that may be read and changed from several
// interpreter threads without a lock.
type Atomic struct {
	n atomic.Int64
}

// SchemeDescribe prints the counter; see re.Describer.
func (a *Atomic) SchemeDescribe(Printer) string {
	return fmt.Sprintf("#<atomic %d>", a.n.Load())
}

func installSync(m *Machine) {
	const lib = "(goscheme sync)"

	// ------------------------------------------------------------------ mutex
	m.defSimple("make-mutex", 0, 0, func(a []Value) (Value, error) {
		return &Mutex{}, nil
	}, lib)

	m.defSimple("mutex?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Mutex)
		return BooleanOf(ok), nil
	}, lib)

	m.defSimple("mutex-lock!", 1, 1, func(a []Value) (Value, error) {
		wantMutex("mutex-lock!", a[0]).mu.Lock()
		return UnspecifiedValue, nil
	}, lib)

	m.defSimple("mutex-unlock!", 1, 1, func(a []Value) (Value, error) {
		wantMutex("mutex-unlock!", a[0]).mu.Unlock()
		return UnspecifiedValue, nil
	}, lib)

	// (with-mutex m body ...) is the safe form: the lock is released on the way
	// out however the body leaves, which needs the interpreter's dynamic-wind,
	// so it is written in Scheme.  A thunk plus mutex-lock!/mutex-unlock! is the
	// Go-level equivalent for callers that prefer procedures.
	m.installEmbeddedSource(lib, `
	  (define-syntax with-mutex
	    (syntax-rules ()
	      ((_ mutex body ...)
	       (let ((mu mutex))
	         (dynamic-wind
	           (lambda () (mutex-lock! mu))
	           (lambda () body ...)
	           (lambda () (mutex-unlock! mu)))))))
	`, "with-mutex")

	// ------------------------------------------------------------- wait groups
	m.defSimple("make-waitgroup", 0, 0, func(a []Value) (Value, error) {
		return &WaitGroup{}, nil
	}, lib)

	m.defSimple("waitgroup?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*WaitGroup)
		return BooleanOf(ok), nil
	}, lib)

	m.defSimple("waitgroup-add!", 1, 2, func(a []Value) (Value, error) {
		wg := wantWaitGroup("waitgroup-add!", a[0])
		n := int64(1)
		if len(a) == 2 {
			n = wantExactInt64("waitgroup-add!", a[1])
		}
		wg.mu.Lock()
		defer wg.mu.Unlock()
		if wg.count+n < 0 {
			return nil, errf("waitgroup-add!", "count would go negative")
		}
		wg.count += n
		wg.wg.Add(int(n))
		return UnspecifiedValue, nil
	}, lib)

	m.defSimple("waitgroup-done!", 1, 1, func(a []Value) (Value, error) {
		wg := wantWaitGroup("waitgroup-done!", a[0])
		wg.mu.Lock()
		defer wg.mu.Unlock()
		if wg.count <= 0 {
			return nil, errf("waitgroup-done!", "count is already zero")
		}
		wg.count--
		wg.wg.Done()
		return UnspecifiedValue, nil
	}, lib)

	m.defSimple("waitgroup-count", 1, 1, func(a []Value) (Value, error) {
		wg := wantWaitGroup("waitgroup-count", a[0])
		wg.mu.Lock()
		defer wg.mu.Unlock()
		return Int(wg.count), nil
	}, lib)

	// (waitgroup-wait wg) blocks the calling interpreter thread until the count
	// is zero.  Add everything you are going to add before waiting, as in Go.
	m.defSimple("waitgroup-wait", 1, 1, func(a []Value) (Value, error) {
		wg := wantWaitGroup("waitgroup-wait", a[0])
		wg.wg.Wait()
		return UnspecifiedValue, nil
	}, lib)

	// ------------------------------------------------------------------- once
	m.defSimple("make-once", 0, 0, func(a []Value) (Value, error) {
		return &Once{}, nil
	}, lib)

	m.defSimple("once?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Once)
		return BooleanOf(ok), nil
	}, lib)

	// (once-run! once thunk) runs thunk the first time and returns its value;
	// later calls return the same value without running it again.  A thunk that
	// raises does not count as the first run, so a later call tries again.
	m.def("once-run!", 2, 2, func(m *Machine, a []Value) {
		once := wantOnce("once-run!", a[0])
		thunk := wantProcedure("once-run!", a[1])
		// The lock is held for the whole of the first run, so a second thread
		// waits for it and then sees the remembered value.
		once.mu.Lock()
		if once.done {
			v := once.val
			once.mu.Unlock()
			m.Return(v)
			return
		}
		child := m.Child()
		v, err := child.RunApply(thunk, nil, child.Global)
		if err != nil {
			// Leave it undone and let the caller see the condition; a later
			// call gets another chance.
			once.mu.Unlock()
			m.RaiseError(err)
			return
		}
		once.val, once.done = v, true
		once.mu.Unlock()
		m.Return(v)
	}, lib)

	m.defSimple("once-done?", 1, 1, func(a []Value) (Value, error) {
		once := wantOnce("once-done?", a[0])
		once.mu.Lock()
		defer once.mu.Unlock()
		return BooleanOf(once.done), nil
	}, lib)

	// ----------------------------------------------------------------- atomics
	m.defSimple("make-atomic", 0, 1, func(a []Value) (Value, error) {
		x := &Atomic{}
		if len(a) == 1 {
			x.n.Store(wantExactInt64("make-atomic", a[0]))
		}
		return x, nil
	}, lib)

	m.defSimple("atomic?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Atomic)
		return BooleanOf(ok), nil
	}, lib)

	m.defSimple("atomic-ref", 1, 1, func(a []Value) (Value, error) {
		return Int(wantAtomic("atomic-ref", a[0]).n.Load()), nil
	}, lib)

	m.defSimple("atomic-set!", 2, 2, func(a []Value) (Value, error) {
		wantAtomic("atomic-set!", a[0]).n.Store(wantExactInt64("atomic-set!", a[1]))
		return UnspecifiedValue, nil
	}, lib)

	// (atomic-add! a n) adds and returns the new value.
	m.defSimple("atomic-add!", 2, 2, func(a []Value) (Value, error) {
		x := wantAtomic("atomic-add!", a[0])
		return Int(x.n.Add(wantExactInt64("atomic-add!", a[1]))), nil
	}, lib)

	// (atomic-swap! a v) stores v and returns the previous value.
	m.defSimple("atomic-swap!", 2, 2, func(a []Value) (Value, error) {
		x := wantAtomic("atomic-swap!", a[0])
		return Int(x.n.Swap(wantExactInt64("atomic-swap!", a[1]))), nil
	}, lib)

	// (atomic-compare-and-set! a old new) stores new only if the value is old,
	// and reports whether it did.
	m.defSimple("atomic-compare-and-set!", 3, 3, func(a []Value) (Value, error) {
		x := wantAtomic("atomic-compare-and-set!", a[0])
		old := wantExactInt64("atomic-compare-and-set!", a[1])
		new_ := wantExactInt64("atomic-compare-and-set!", a[2])
		return BooleanOf(x.n.CompareAndSwap(old, new_)), nil
	}, lib)
}

func wantMutex(name string, v Value) *Mutex {
	x, ok := v.(*Mutex)
	if !ok {
		panic(errf(name, "expected a mutex but got %s", WriteToString(v)))
	}
	return x
}

func wantWaitGroup(name string, v Value) *WaitGroup {
	x, ok := v.(*WaitGroup)
	if !ok {
		panic(errf(name, "expected a wait group but got %s", WriteToString(v)))
	}
	return x
}

func wantOnce(name string, v Value) *Once {
	x, ok := v.(*Once)
	if !ok {
		panic(errf(name, "expected a once but got %s", WriteToString(v)))
	}
	return x
}

func wantAtomic(name string, v Value) *Atomic {
	x, ok := v.(*Atomic)
	if !ok {
		panic(errf(name, "expected an atomic but got %s", WriteToString(v)))
	}
	return x
}

// wantExactInt64 accepts an exact integer that fits in an int64, which is what
// the Go counters and atomics underneath can hold.
func wantExactInt64(name string, v Value) int64 {
	i, ok := v.(*Integer)
	if !ok {
		panic(errf(name, "expected an exact integer but got %s", WriteToString(v)))
	}
	n, ok := i.Int64()
	if !ok {
		panic(errf(name, "integer does not fit in 64 bits: %s", WriteToString(v)))
	}
	return n
}
