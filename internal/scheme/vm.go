// SPDX-License-Identifier: MIT

package scheme

import "fmt"

// The bytecode virtual machine.
//
// The interpreter is a tree-walker over an explicit continuation stack (see
// machine.go).  This file adds a second execution path: expressions that the
// compiler (compile.go) can translate run as bytecode here, and anything it
// cannot translate runs in the tree-walker, so the two agree by construction
// rather than by imitation.
//
// The continuation stack is the machine's, and a captured continuation copies
// it and may be invoked more than once.  The VM keeps the same discipline as
// the interpreted frames: an fVM is a *value* that is never mutated, and every
// suspension point writes a fresh one holding its own copy of the operand
// stack.  What is shared between continuations is what Scheme says is shared:
// the cells of variables that set! can change.
//
// A compiled activation is not on the stack while it runs — only while it
// waits for a call it made — so a tail call simply applies the procedure
// without pushing anything, which is what makes tail calls proper here.

type opcode byte

const (
	opConst          opcode = iota // arg1: index into Consts
	opLocal                        // arg1: depth, arg2: slot
	opLocalCell                    // arg1: depth, arg2: slot (the slot holds a cell)
	opLocalCheck                   // as opLocal, but an uninitialized variable is an error
	opLocalCellCheck               // as opLocalCell, with the same check
	opSetLocal                     // arg1: depth, arg2: slot
	opSetCell                      // arg1: depth, arg2: slot
	opNewCell                      // arg1: depth, arg2: slot (wrap the value on top)
	opGlobal                       // arg1: index of a symbol constant
	opSetGlobal                    // arg1: index of a symbol constant
	opDefineGlobal                 // arg1: index of a symbol constant
	opClosure                      // arg1: index of a compiled Code constant
	opInterpClosure                // arg1: index of a (lambda ...) source constant
	opPop
	opEqv       // pops two values, pushes (eqv? a b), for case
	opJump      // arg1: target
	opJumpFalse // arg1: target (pops the test)
	opJumpTrue  // arg1: target (pops the test)
	// The keeping variants leave the test value on the stack, which is what
	// and, or and a cond clause with => need.
	opJumpFalseKeep // arg1: target
	opJumpTrueKeep  // arg1: target
	opCall          // arg1: argument count
	opTailCall      // arg1: argument count
	opReturn
)

// instr is one instruction.  The two operands are interpreted according to the
// opcode: a constant index, a lexical depth and slot, or a jump target.
type instr struct {
	op   opcode
	arg1 int32
	arg2 int32
}

// Code is one compiled body: its instructions, the literals they mention, and
// the shape of the frame they run in.  A lambda's body is a Code of its own,
// held in the parent's constant pool; that tree is what a .scmc file stores.
type Code struct {
	Name   string
	Instrs []instr
	Consts []Value
	// NSlots is the frame size, and Boxed says which slots hold a cell (the
	// variables that set! can change).
	NSlots  int
	Boxed   []bool
	NParams int
	// Params are the parameter symbols, kept so that a closure created from
	// this Code does not allocate a fresh list of placeholders every time.
	Params  []*Symbol
	HasRest bool
	// RestSlot is the slot the rest list is bound to when HasRest.
	RestSlot int
	// Checked lists the slots that must be initialized before use: the
	// variables introduced by internal definitions and by letrec.  Names
	// carries a symbol per slot so that the error can name it.
	Checked []bool
	Names   []*Symbol
}

// cell is a boxed variable: locals that set! can change live in one, so that a
// closure and the frame it came from see the same binding.
type cell struct {
	v Value
}

// vmEnv is one activation frame: its slots and the frame the closure that
// created it was running in.
type vmEnv struct {
	parent *vmEnv
	slots  []Value
}

func (e *vmEnv) up(n int) *vmEnv {
	for ; n > 0 && e != nil; n-- {
		e = e.parent
	}
	return e
}

// fVM is the continuation of a compiled activation that is waiting for the
// value of a call.  Resuming it never writes to it: the instruction pointer,
// the frame and the operand stack are handed to vmRun, which builds a fresh
// fVM at its next suspension point.  A continuation captured above this frame
// can therefore be invoked any number of times.
type fVM struct {
	code    *Code
	ip      int
	env     *vmEnv
	globals *Env
	vals    []Value
}

func (f *fVM) resume(m *Machine, v Value) {
	vals := make([]Value, len(f.vals), len(f.vals)+1)
	copy(vals, f.vals)
	vals = append(vals, v)
	vmRun(m, f.code, f.ip, f.env, f.globals, vals)
}

// startVM begins a compiled activation, which is what applying a compiled
// closure does.  It returns when the activation returns or suspends.
func (m *Machine) startVM(code *Code, env *vmEnv, globals *Env) {
	vmRun(m, code, 0, env, globals, nil)
}

// runCompiledTop runs a compiled top-level chunk in env.  A top-level form has
// a frame of its own, because a let or a named let at the top level allocates
// slots there.
func (m *Machine) runCompiledTop(code *Code, env *Env) {
	slots := make([]Value, code.NSlots)
	for i := range code.Checked {
		if !code.Checked[i] {
			continue
		}
		if code.Boxed[i] {
			slots[i] = &cell{v: Unassigned}
		} else {
			slots[i] = Unassigned
		}
	}
	vmRun(m, code, 0, &vmEnv{slots: slots}, env, nil)
}

// compile compiles a top-level form unless the machine was asked to interpret
// everything or the form uses something the compiler does not handle.
func (m *Machine) compile(form Value, env *Env) (*Code, error) {
	if m.Interpret || compileDisabled {
		return nil, errNotCompiled
	}
	return compileTop(m, form, env)
}

// compileDisabled is a package-wide switch for the tests that want the
// interpreter regardless of how a machine was built.
var compileDisabled = false

var errNotCompiled = fmt.Errorf("not compiled")

// applyCompiled applies a compiled clause: the arguments are bound into a
// frame, with a cell for every parameter that set! can change.
func (m *Machine) applyCompiled(c *Closure, clause *ClosureClause, args []Value) {
	code := clause.Code
	params := clause.Params
	hasRest := clause.HasRest
	slots := make([]Value, code.NSlots)
	for i := range params {
		if code.Boxed[i] {
			slots[i] = &cell{v: args[i]}
		} else {
			slots[i] = args[i]
		}
	}
	if hasRest {
		rest := List(args[len(params):]...)
		if code.Boxed[code.RestSlot] {
			slots[code.RestSlot] = &cell{v: rest}
		} else {
			slots[code.RestSlot] = rest
		}
	}
	for i := range code.Checked {
		if !code.Checked[i] || slots[i] != nil {
			continue
		}
		if code.Boxed[i] {
			slots[i] = &cell{v: Unassigned}
		} else {
			slots[i] = Unassigned
		}
	}
	vmRun(m, code, 0, &vmEnv{parent: c.Vm, slots: slots}, c.Env, nil)
}

// makeCompiledClosure builds the procedure value for a compiled Code.  The
// parameter symbols are only there for arity checking and for the error
// messages the interpreter part of the machine prints, so they need not be
// interned: a compiled clause never binds them in an Env.
func makeCompiledClosure(name string, code *Code, params []*Symbol, hasRest bool, vm *vmEnv, globals *Env) *Closure {
	return &Closure{
		Name: name,
		Clauses: []ClosureClause{{
			Params:  params,
			HasRest: hasRest,
			Code:    code,
		}},
		Env: globals,
		Vm:  vm,
	}
}

// placeholderParams makes the parameter symbols a compiled clause carries for
// its arity.  They are never bound, so they do not need to be interned, and a
// .scmc file does not store them: the loader makes them again.
func placeholderParams(n int) []*Symbol {
	out := make([]*Symbol, n)
	for i := range out {
		out[i] = &Symbol{Name: "arg"}
	}
	return out
}

func popValue(vals []Value) (Value, []Value) {
	return vals[len(vals)-1], vals[:len(vals)-1]
}

// vmRun is the instruction loop.  It runs until the activation returns a value
// (m.Return) or suspends on a call (by pushing an fVM and applying), and it
// never mutates anything it was given: the operand stack it was handed is
// copied into a fresh slice before it is extended.
func vmRun(m *Machine, code *Code, ip int, env *vmEnv, globals *Env, vals []Value) {
	// The operand stack handed in is private to this run (it is either nil or
	// the fresh copy a resumption made), so appending to it cannot disturb a
	// captured continuation.
	instrs := code.Instrs
	for {
		in := instrs[ip]
		ip++
		switch in.op {
		case opConst:
			vals = append(vals, code.Consts[in.arg1])

		case opLocal:
			vals = append(vals, env.up(int(in.arg1)).slots[in.arg2])
		case opLocalCell:
			vals = append(vals, env.up(int(in.arg1)).slots[in.arg2].(*cell).v)
		case opLocalCheck:
			v := env.up(int(in.arg1)).slots[in.arg2]
			if _, un := v.(unassigned); un {
				m.Raise(NewError("variable used before initialization",
					code.Names[in.arg2]))
				return
			}
			vals = append(vals, v)
		case opLocalCellCheck:
			v := env.up(int(in.arg1)).slots[in.arg2].(*cell).v
			if _, un := v.(unassigned); un {
				m.Raise(NewError("variable used before initialization",
					code.Names[in.arg2]))
				return
			}
			vals = append(vals, v)

		case opSetLocal:
			var v Value
			v, vals = popValue(vals)
			env.up(int(in.arg1)).slots[in.arg2] = v
		case opSetCell:
			var v Value
			v, vals = popValue(vals)
			env.up(int(in.arg1)).slots[in.arg2].(*cell).v = v
		case opNewCell:
			var v Value
			v, vals = popValue(vals)
			env.up(int(in.arg1)).slots[in.arg2] = &cell{v: v}

		case opGlobal:
			sym, _ := code.Consts[in.arg1].(*Symbol)
			v, ok := globals.Lookup(sym)
			if !ok {
				m.Raise(NewError("unbound variable", sym))
				return
			}
			if _, un := v.(unassigned); un {
				m.Raise(NewError("variable used before initialization", sym))
				return
			}
			vals = append(vals, v)
		case opSetGlobal:
			sym, _ := code.Consts[in.arg1].(*Symbol)
			var v Value
			v, vals = popValue(vals)
			if !globals.Set(sym, v) {
				m.Raise(NewError("set!: unbound variable", sym))
				return
			}
			vals = append(vals, UnspecifiedValue)
		case opDefineGlobal:
			sym, _ := code.Consts[in.arg1].(*Symbol)
			var v Value
			v, vals = popValue(vals)
			globals.Define(sym, v)
			vals = append(vals, UnspecifiedValue)

		case opClosure:
			sub := code.Consts[in.arg1].(*Code)
			vals = append(vals, makeCompiledClosure(sub.Name, sub, sub.Params,
				sub.HasRest, env, globals))
		case opInterpClosure:
			form := code.Consts[in.arg1]
			cl, err := makeClosure(car(cdr(form)), mustSlice(cdr(cdr(form))), globals)
			if err != nil {
				m.RaiseError(err)
				return
			}
			vals = append(vals, cl)

		case opEqv:
			var a, b Value
			b, vals = popValue(vals)
			a, vals = popValue(vals)
			vals = append(vals, BooleanOf(Eqv(a, b)))

		case opPop:
			vals = vals[:len(vals)-1]

		case opJump:
			ip = int(in.arg1)
		case opJumpFalse:
			var v Value
			v, vals = popValue(vals)
			if IsFalse(v) {
				ip = int(in.arg1)
			}
		case opJumpTrue:
			var v Value
			v, vals = popValue(vals)
			if IsTrue(v) {
				ip = int(in.arg1)
			}
		case opJumpFalseKeep:
			if IsFalse(vals[len(vals)-1]) {
				ip = int(in.arg1)
			}
		case opJumpTrueKeep:
			if IsTrue(vals[len(vals)-1]) {
				ip = int(in.arg1)
			}

		case opCall:
			n := int(in.arg1)
			// The arguments are passed as a view of the operand stack.  That
			// is safe because the continuation below gets its own copy, so
			// this array is not written to again.
			args := vals[len(vals)-n:]
			vals = vals[:len(vals)-n]
			var proc Value
			proc, vals = popValue(vals)
			// Snapshot the state for the resumption; the machine pops this
			// frame before resuming it, and it is never written to.
			var rest []Value
			if len(vals) > 0 {
				rest = make([]Value, len(vals))
				copy(rest, vals)
			}
			m.stack = append(m.stack, &fVM{
				code: code, ip: ip, env: env, globals: globals, vals: rest,
			})
			m.apply(proc, args)
			return

		case opTailCall:
			n := int(in.arg1)
			args := vals[len(vals)-n:]
			vals = vals[:len(vals)-n]
			var proc Value
			proc, vals = popValue(vals)
			m.apply(proc, args)
			return

		case opReturn:
			var v Value
			v, vals = popValue(vals)
			m.Return(v)
			return
		}
	}
}
