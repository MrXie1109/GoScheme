// SPDX-License-Identifier: MIT

package scheme

import (
	"sync"
	"sync/atomic"
)

// Env is a lexical environment: a frame of variable bindings with a pointer
// to the enclosing environment.
//
// Environments hold both variable bindings and syntactic bindings (macros);
// this mirrors the R7RS notion that a binding can be either a location or a
// syntactic keyword.  A separate interface for the latter was considered, but
// sharing one table keeps lookup and define-syntax simple and lets a lexical
// variable shadow a macro and vice versa.
// envInline is how many bindings a frame holds in its own fields before it
// falls back to a map.  Most frames — a lambda's parameters, a let's bindings —
// hold a handful, and allocating a map for each of them was a large part of the
// interpreter's allocation.
const envInline = 4

// concurrentThreads counts interpreter threads running right now.  A frame only
// needs its lock when more than one thread can touch it, which in a
// single-threaded program is never, so the count lets every lookup and
// definition skip the lock; a program that uses (go ...) keeps it.
var concurrentThreads atomic.Int32

// enterConcurrency and exitConcurrency bracket a thread that will touch
// environments, so that frames start taking their locks before it runs.
func enterConcurrency() { concurrentThreads.Add(1) }
func exitConcurrency()  { concurrentThreads.Add(-1) }

type Env struct {
	// mu guards this frame.  It is only taken when more than one interpreter
	// thread is running; see concurrentThreads.
	mu     sync.RWMutex
	names  [envInline]*Symbol
	vals   [envInline]Value
	n      int
	vars   map[*Symbol]Value
	parent *Env
	Name   string
	// Library is non-nil for the top level environment of a library.
	Library *Library
}

// unassigned marks a variable that has been declared but not yet given a
// value (letrec, internal defines).
type unassigned struct{}

// Unassigned is the sentinel used for letrec initialisation.
var Unassigned = unassigned{}

// NewEnv creates a fresh environment with the given parent.
func NewEnv(parent *Env) *Env {
	return &Env{parent: parent}
}

// NewEnvNamed creates a named environment (used for libraries and the REPL).
func NewEnvNamed(parent *Env, name string) *Env {
	return &Env{parent: parent, Name: name}
}

// Global returns the outermost environment of the chain.
func (e *Env) Global() *Env {
	for e.parent != nil {
		e = e.parent
	}
	return e
}

// Define binds sym in this frame, replacing any binding it already had.
func (e *Env) Define(sym *Symbol, v Value) {
	locked := concurrentThreads.Load() != 0
	if locked {
		e.mu.Lock()
	}
	for i := 0; i < e.n; i++ {
		if e.names[i] == sym {
			e.vals[i] = v
			if locked {
				e.mu.Unlock()
			}
			return
		}
	}
	if e.vars != nil {
		if _, exists := e.vars[sym]; exists {
			e.vars[sym] = v
			if locked {
				e.mu.Unlock()
			}
			return
		}
	}
	if e.n < envInline {
		e.names[e.n], e.vals[e.n] = sym, v
		e.n++
		if locked {
			e.mu.Unlock()
		}
		return
	}
	if e.vars == nil {
		e.vars = make(map[*Symbol]Value, 8)
	}
	e.vars[sym] = v
	if locked {
		e.mu.Unlock()
	}
}

// DefineName binds a symbol by name.
func (e *Env) DefineName(name string, v Value) {
	e.Define(Intern(name), v)
}

// Has reports whether sym is bound in this frame only.
func (e *Env) Has(sym *Symbol) bool {
	_, ok := e.get(sym)
	return ok
}

// get returns the binding of sym in this frame only.
func (e *Env) get(sym *Symbol) (Value, bool) {
	locked := concurrentThreads.Load() != 0
	if locked {
		e.mu.RLock()
	}
	for i := 0; i < e.n; i++ {
		if e.names[i] == sym {
			v := e.vals[i]
			if locked {
				e.mu.RUnlock()
			}
			return v, true
		}
	}
	v, ok := e.vars[sym]
	if locked {
		e.mu.RUnlock()
	}
	return v, ok
}

// Set updates an existing binding, returning false when unbound.
func (e *Env) Set(sym *Symbol, v Value) bool {
	locked := concurrentThreads.Load() != 0
	for env := e; env != nil; env = env.parent {
		if locked {
			env.mu.Lock()
		}
		for i := 0; i < env.n; i++ {
			if env.names[i] == sym {
				env.vals[i] = v
				if locked {
					env.mu.Unlock()
				}
				return true
			}
		}
		if _, ok := env.vars[sym]; ok {
			env.vars[sym] = v
			if locked {
				env.mu.Unlock()
			}
			return true
		}
		if locked {
			env.mu.Unlock()
		}
	}
	if sym.Mark != 0 {
		if def := markEnvOf(sym.Mark); def != nil {
			if sym.orig != nil && def.Set(sym.orig, v) {
				return true
			}
			return def.Set(sym.Base(), v)
		}
	}
	return false
}

// SetGlobal binds sym in the global frame, creating it when needed.
func (e *Env) SetGlobal(sym *Symbol, v Value) {
	e.Global().Define(sym, v)
}

// Lookup finds the value bound to sym.  Marked identifiers that are not bound
// lexically are resolved through the environment captured by the mark, which
// is what implements referential transparency for macro-introduced
// identifiers.
func (e *Env) Lookup(sym *Symbol) (Value, bool) {
	for env := e; env != nil; env = env.parent {
		if v, ok := env.get(sym); ok {
			return v, true
		}
	}
	if sym.Mark != 0 {
		if def := markEnvOf(sym.Mark); def != nil {
			if sym.orig != nil {
				if v, ok := def.Lookup(sym.orig); ok {
					return v, true
				}
			}
			if v, ok := def.Lookup(sym.Base()); ok {
				return v, true
			}
		}
	}
	return nil, false
}

// LookupLocal searches only the frames from e up to (but excluding) stop.
func (e *Env) LookupLocal(sym *Symbol, stop *Env) (Value, bool) {
	for env := e; env != nil && env != stop; env = env.parent {
		if v, ok := env.get(sym); ok {
			return v, true
		}
	}
	return nil, false
}

// Bound reports whether sym has a binding anywhere in the chain.
func (e *Env) Bound(sym *Symbol) bool {
	_, ok := e.Lookup(sym)
	return ok
}

// Macro returns the syntactic binding of sym, if any.
func (e *Env) Macro(sym *Symbol) (*Macro, bool) {
	v, ok := e.Lookup(sym)
	if !ok {
		return nil, false
	}
	m, ok := v.(*Macro)
	return m, ok
}

// Snapshot returns a copy of all visible bindings, nearest binding wins.
func (e *Env) Snapshot() map[*Symbol]Value {
	out := map[*Symbol]Value{}
	var walk func(*Env)
	seen := map[*Env]bool{}
	walk = func(env *Env) {
		if env == nil || seen[env] {
			return
		}
		seen[env] = true
		locked := concurrentThreads.Load() != 0
		if locked {
			env.mu.RLock()
		}
		for i := 0; i < env.n; i++ {
			if _, dup := out[env.names[i]]; !dup {
				out[env.names[i]] = env.vals[i]
			}
		}
		for k, v := range env.vars {
			if _, dup := out[k]; !dup {
				out[k] = v
			}
		}
		if locked {
			env.mu.RUnlock()
		}
		walk(env.parent)
	}
	walk(e)
	return out
}
