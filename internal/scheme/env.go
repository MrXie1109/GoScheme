package scheme

// Env is a lexical environment: a frame of variable bindings with a pointer
// to the enclosing environment.
//
// Environments hold both variable bindings and syntactic bindings (macros);
// this mirrors the R7RS notion that a binding can be either a location or a
// syntactic keyword.  A separate interface for the latter was considered, but
// sharing one table keeps lookup and define-syntax simple and lets a lexical
// variable shadow a macro and vice versa.
type Env struct {
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
	return &Env{vars: make(map[*Symbol]Value, 8), parent: parent}
}

// NewEnvNamed creates a named environment (used for libraries and the REPL).
func NewEnvNamed(parent *Env, name string) *Env {
	return &Env{vars: make(map[*Symbol]Value, 8), parent: parent, Name: name}
}

// Global returns the outermost environment of the chain.
func (e *Env) Global() *Env {
	for e.parent != nil {
		e = e.parent
	}
	return e
}

// Define binds sym in this frame.
func (e *Env) Define(sym *Symbol, v Value) {
	e.vars[sym] = v
}

// DefineName binds a symbol by name.
func (e *Env) DefineName(name string, v Value) {
	e.vars[Intern(name)] = v
}

// Set updates an existing binding, returning false when unbound.
func (e *Env) Set(sym *Symbol, v Value) bool {
	for env := e; env != nil; env = env.parent {
		if _, ok := env.vars[sym]; ok {
			env.vars[sym] = v
			return true
		}
	}
	if sym.Mark != 0 {
		if def := markEnvOf(sym.Mark); def != nil {
			return def.Set(sym.Base(), v)
		}
	}
	return false
}

// SetGlobal binds sym in the global frame, creating it when needed.
func (e *Env) SetGlobal(sym *Symbol, v Value) {
	e.Global().vars[sym] = v
}

// Lookup finds the value bound to sym.  Marked identifiers that are not bound
// lexically are resolved through the environment captured by the mark, which
// is what implements referential transparency for macro-introduced
// identifiers.
func (e *Env) Lookup(sym *Symbol) (Value, bool) {
	for env := e; env != nil; env = env.parent {
		if v, ok := env.vars[sym]; ok {
			return v, true
		}
	}
	if sym.Mark != 0 {
		if def := markEnvOf(sym.Mark); def != nil {
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
		if v, ok := env.vars[sym]; ok {
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
		for k, v := range env.vars {
			if _, dup := out[k]; !dup {
				out[k] = v
			}
		}
		walk(env.parent)
	}
	walk(e)
	return out
}
