// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"sync"
)

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
	// The comparison primitives, as instructions.  Emitted where the compiler
	// can see that the operator is still the interpreter's own binding and the
	// call has the arity the instruction takes, so a program that rebinds <
	// gets the general call it asked for.  The operation is the primitive's
	// own — a fixnum fast path, and the primitive itself otherwise — reached
	// without a call, an arity check and an interface method lookup.  arg1 is
	// the argument count.  See arithmeticOps for why + - * are not here.
	opNumLt
	opNumLe
	opNumGt
	opNumGe
	opNumEq
)

// opcodeCount is how many opcodes this interpreter knows.  The reader uses it:
// an instruction from a newer file would otherwise be a silent no-op, because
// the instruction loop's switch has no default case.
const opcodeCount = int(opNumEq) + 1

// guardReRaise is what a compiled guard does when none of its clauses matched:
// it raises the condition again.  It is a value of its own rather than a lookup
// of `raise`, which a program may rebind, and it is what the interpreter's
// evalGuardClauses does too.
var guardReRaise = &Primitive{Name: "guard-re-raise", MinArgs: 1, MaxArgs: 1,
	Fn: func(m *Machine, a []Value) { m.Raise(a[0]) }}

// selectHelper does what evalSelect does once the compiler has turned each
// clause's expressions into thunks: it evaluates them in the interpreter's
// order and runs the selection, so the two paths differ only in who evaluates
// the expressions.
var selectHelper = &Primitive{Name: "select", MinArgs: 0, MaxArgs: -1,
	Fn: func(m *Machine, a []Value) {
		if len(a) == 0 {
			// (select) with no clauses blocks forever, as Go's select {} does.
			select {}
		}
		var clauses []*selectClause
		var exprs []Value
		for i := 0; i < len(a); {
			n, ok := a[i].(*Integer)
			if !ok {
				m.Raise(NewError("select: malformed clause"))
				return
			}
			kind64, _ := n.Int64()
			kind := int(kind64)
			i++
			clauses = append(clauses, &selectClause{kind: kind})
			for k := 0; k < selectExprCount(kind); k++ {
				if i >= len(a) {
					m.Raise(NewError("select: malformed clause"))
					return
				}
				exprs = append(exprs, a[i])
				i++
			}
		}
		// The thunks are applied in order, as the interpreter evaluates the
		// expressions in order.
		vals := make([]Value, len(exprs))
		at := 0
		var step func()
		step = func() {
			if at == len(exprs) {
				m.runSelect(clauses, vals)
				return
			}
			j := at
			at++
			m.ApplyWith(exprs[j], nil, func(mm *Machine, v Value) {
				vals[j] = v
				step()
			})
		}
		step()
	}}

// selectExprCount is how many expressions a clause of each kind has: the
// channel (or the timeout), any value to send, and the handler.  An else
// clause is only its handler.
func selectExprCount(kind int) int {
	switch kind {
	case selSend:
		return 3
	case selElse:
		return 1
	default:
		return 2
	}
}

// matchHelper does what matchClauses does, with the clauses compiled: it
// matches each pattern in turn, and runs the clause's guard and body — thunks
// the compiler built — with the values the pattern bound.  The matching itself
// is matchPattern, the same code the interpreter runs, so the two paths agree
// about what matches and what it binds.
//
// Its arguments are the subject, then four per clause: the pattern, the list of
// variables the body's thunk takes, the guard thunk (or #f), and the body thunk.
var matchHelper = &Primitive{Name: "match", MinArgs: 2, MaxArgs: -1,
	Fn: func(m *Machine, a []Value) {
		subject := a[0]
		clauses := a[1:]
		var try func(i int)
		try = func(i int) {
			if i*4 >= len(clauses) {
				m.Raise(NewError("match: no pattern matched " + WriteToString(subject)))
				return
			}
			pattern := clauses[i*4]
			vars, _ := ListToSlice(clauses[i*4+1])
			guardThunk, bodyThunk := clauses[i*4+2], clauses[i*4+3]
			var binds []matchBinding
			ok, err := matchPattern(pattern, subject, &binds)
			if err != nil {
				m.RaiseError(err)
				return
			}
			if !ok {
				try(i + 1)
				return
			}
			// The thunk's parameters are the pattern's variables, and the
			// values go by name: a branch that did not bind one leaves it
			// unassigned, which reading it reports.
			argv := make([]Value, len(vars))
			for k, name := range vars {
				argv[k] = Unassigned
				for j := len(binds) - 1; j >= 0; j-- {
					if binds[j].sym == name {
						argv[k] = binds[j].val
						break
					}
				}
			}
			if guardThunk != Value(False) {
				m.ApplyWith(guardThunk, argv, func(mm *Machine, gv Value) {
					if IsFalse(gv) {
						try(i + 1)
						return
					}
					mm.apply(bodyThunk, argv)
				})
				return
			}
			m.apply(bodyThunk, argv)
		}
		try(0)
	}}

// bindValues applies a producer and hands every value it returns to a
// consumer, which is what call-with-values does.  It is a value of the
// compiler's own rather than that procedure, because a compiled binding form
// must reach it however the program has rebound the name.
var bindValues = &Primitive{Name: "bind-values", MinArgs: 2, MaxArgs: 2,
	Fn: func(m *Machine, a []Value) {
		m.ApplyWithMulti(a[0], nil, func(mm *Machine, vs []Value) {
			mm.apply(a[1], vs)
		})
	}}

// recordTypeHelper builds what a define-record-type defines and returns the
// values in the order its names appear, for the compiler to store: the work
// itself is recordType, which the interpreter also uses, so both paths make
// the same objects.
var recordTypeHelper = &Primitive{Name: "define-record-type", MinArgs: 1, MaxArgs: 1, Sync: true,
	Fn: func(m *Machine, a []Value) {
		_, values, err := recordType(mustSlice(a[0]))
		if err != nil {
			m.RaiseError(err)
			return
		}
		if len(values) == 1 {
			m.Return(values[0])
			return
		}
		m.Return(&MultipleValues{Values: values})
	}}

// goHelper is what a compiled (go body ...) calls: it spawns a thread running
// the compiled thunk and returns, as evalGo does with the thunk the
// interpreter would have made.
var goHelper = &Primitive{Name: "go", MinArgs: 1, MaxArgs: 1, Sync: true,
	Fn: func(m *Machine, a []Value) {
		m.Spawn(a[0])
		m.Return(UnspecifiedValue)
	}}

// promiseHelper makes the promise (delay e) and (delay-force e) stand for,
// with the thunk the compiler built rather than one the interpreter would walk.
var promiseHelper = &Primitive{Name: "delay", MinArgs: 2, MaxArgs: 2, Sync: true,
	Fn: func(m *Machine, a []Value) {
		isForce, _ := a[1].(Boolean)
		m.Return(&Promise{Thunk: a[0], IsDelayForce: bool(isForce)})
	}}

// caseLambdaHelper assembles a case-lambda from the closures the compiler made
// for its clauses.  Each carries one compiled clause, and the result is one
// procedure with all of them, so picking a clause by arity is the same whether
// the clauses were compiled or not.
var caseLambdaHelper = &Primitive{Name: "case-lambda", MinArgs: 1, MaxArgs: -1, Sync: true,
	Fn: func(m *Machine, a []Value) {
		out := &Closure{}
		for _, v := range a {
			c, ok := v.(*Closure)
			if !ok || len(c.Clauses) != 1 {
				m.Raise(NewError("case-lambda: bad clause"))
				return
			}
			if out.Env == nil {
				out.Env, out.Vm, out.Name = c.Env, c.Vm, c.Name
			}
			out.Clauses = append(out.Clauses, c.Clauses[0])
		}
		m.Return(out)
	}}

// assertFailed raises what (assert e) raises when e is false, with the form as
// the irritant, exactly as the interpreter's evalAssert does.
var assertFailed = &Primitive{Name: "assert", MinArgs: 1, MaxArgs: 1, Sync: true,
	Fn: func(m *Machine, a []Value) {
		m.Raise(NewError("assertion failed", a[0]))
	}}

// guardHelper is what a compiled guard calls: (guard-helper clause-handler
// body), where the first is a procedure of one argument — the condition — and
// the second a thunk.  It does what the interpreter's evalGuard does, in the
// same order: take the continuation the guard will return to, install a
// handler that escapes back to it, run the body, and take the handler away
// again if the body returns.
//
// The guard is a call rather than an inline sequence because the escape has to
// come *back*: a raise unwinds to this frame, and the value the clauses
// produce has to reach whatever was waiting for the guard's value.  A call is
// what puts that waiter on the continuation stack.
var guardHelper = &Primitive{Name: "guard-helper", MinArgs: 2, MaxArgs: 2,
	Fn: func(m *Machine, a []Value) {
		clauses, body := a[0], a[1]
		stack := append([]frame(nil), m.stack...)
		winds := append([]*windFrame(nil), m.winds...)
		hands := append([]*handlerFrame(nil), m.hands...)
		m.framesCopied = true
		escape := &Primitive{Name: "guard-escape", MinArgs: 1, MaxArgs: 1,
			Fn: func(mm *Machine, hargs []Value) {
				// Escaping from the body must run the after thunks of every
				// wind frame that is being left, and then the clauses.
				target := &Continuation{
					stack: stack, winds: winds, hands: hands, owner: mm,
				}
				mm.transferToWith(target, func(mm *Machine) {
					mm.apply(clauses, hargs)
				})
			}}
		m.hands = append(m.hands, &handlerFrame{proc: escape})
		savedLen := len(m.hands)
		m.ApplyWith(body, nil, func(mm *Machine, v Value) {
			if len(mm.hands) >= savedLen {
				mm.hands = mm.hands[:savedLen-1]
			}
			mm.Return(v)
		})
	}}

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

// vmCellsMu guards the cells of variables that set! can change, and is taken
// only while more than one interpreter thread is running — the discipline the
// interpreter's Env uses.  A compiled thread body shares the frames of the
// closure it came from, so a set! in a thread and a read in its parent have to
// be ordered here as they are there; with one thread the atomic load is all it
// costs.
var vmCellsMu sync.Mutex

func cellGet(c *cell) Value {
	if concurrentThreads.Load() != 0 {
		vmCellsMu.Lock()
		defer vmCellsMu.Unlock()
	}
	return c.v
}

func cellSet(c *cell, v Value) {
	if concurrentThreads.Load() != 0 {
		vmCellsMu.Lock()
		defer vmCellsMu.Unlock()
	}
	c.v = v
}

// vmInlineSlots is how many slots a frame keeps inside itself.  A frame whose
// slots are a separate slice is two allocations — the env and the array — and
// this makes the common small frame one: a parameter or two and an internal
// definition.  A bigger frame still falls back to a slice.
//
// Raising it to 8 was tried and is 6% SLOWER: every frame carries the slots
// whether or not it uses them, and a frame that is 4 slots'-worth bigger is
// copied more on every call and on every suspension.  The allocation it saves
// is per call; the copying it costs is per call and per return.
const vmInlineSlots = 4

// vmEnv is one activation frame: its slots and the frame the closure that
// created it was running in.
type vmEnv struct {
	parent *vmEnv
	slots  []Value
	buf    [vmInlineSlots]Value
}

// newVMEnv makes a frame of n slots.  The slots are zeroed either way, which
// is what the checked slots (letrec, internal definitions) rely on.
func newVMEnv(parent *vmEnv, n int) *vmEnv {
	if n <= vmInlineSlots {
		e := &vmEnv{parent: parent}
		e.slots = e.buf[:n]
		return e
	}
	return &vmEnv{parent: parent, slots: make([]Value, n)}
}

func (e *vmEnv) up(n int) *vmEnv {
	for ; n > 0 && e != nil; n-- {
		e = e.parent
	}
	return e
}

// vmInlineVals is how much of a suspended activation's operand stack its
// continuation frame keeps inside itself.  With vmInlineSlots this makes the
// common call allocate one object: the frame.
const vmInlineVals = 4

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
	buf     [vmInlineVals]Value
}

// save keeps the operand stack the activation is leaving behind.  It is a copy
// rather than a slice of the caller's array: that array belongs to the
// activation again the moment the call returns, and the short case — by far
// the common one — needs no allocation at all.
func (f *fVM) save(vals []Value) {
	if len(vals) <= vmInlineVals {
		f.vals = f.buf[:copy(f.buf[:], vals)]
		return
	}
	f.vals = append([]Value(nil), vals...)
}

func (f *fVM) resume(m *Machine, v Value) {
	var vals []Value
	if m.framesCopied {
		// A continuation was captured at some point, so this frame may be
		// reachable from more than one stack: its buffer is not ours to write.
		vals = make([]Value, len(f.vals), len(f.vals)+1)
		copy(vals, f.vals)
		vals = append(vals, v)
	} else {
		// This frame has just been popped from the only stack that holds it,
		// so the value can go into its own buffer.  The slice moves to a fresh
		// array once the buffer is full.
		vals = append(f.vals, v)
	}
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
	e := newVMEnv(nil, code.NSlots)
	slots := e.slots
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
	vmRun(m, code, 0, e, env, nil)
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
	vmRun(m, clause.Code, 0, frameFor(c, clause, args), c.Env, nil)
}

// frameFor binds the arguments of a chosen compiled clause into a fresh frame.
// The values are copied out of args, so the caller's operand stack is free
// again the moment this returns.
func frameFor(c *Closure, clause *ClosureClause, args []Value) *vmEnv {
	code := clause.Code
	params := clause.Params
	env := newVMEnv(c.Vm, code.NSlots)
	slots := env.slots
	for i := range params {
		if code.Boxed[i] {
			slots[i] = &cell{v: args[i]}
		} else {
			slots[i] = args[i]
		}
	}
	if clause.HasRest {
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
	return env
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

// vmCallee is a compiled procedure whose body the VM can enter directly: the
// code, the frame its closure was defined in, and that frame's globals.
type vmCallee struct {
	code    *Code
	env     *vmEnv
	globals *Env
}

// compiledClause picks the clause of a procedure when there is nothing to
// decide: one clause, compiled, and the arity fits.  Everything else — clause
// selection, an interpreted body, an arity error — goes the long way round
// through apply.
func compiledClause(proc Value, args []Value) (vmCallee, bool) {
	c, ok := proc.(*Closure)
	if !ok || len(c.Clauses) != 1 {
		return vmCallee{}, false
	}
	cl := &c.Clauses[0]
	if cl.Code == nil || !arityMatches(cl, len(args)) {
		return vmCallee{}, false
	}
	return vmCallee{code: cl.Code, env: frameFor(c, cl, args), globals: c.Env}, true
}

// syncCall is how a call to a simple primitive (one whose Sync is set) ended.
type syncCall int

const (
	// syncNone: not a simple primitive, so the caller takes the ordinary path.
	syncNone syncCall = iota
	// syncValue: the primitive returned a value.
	syncValue
	// syncRaise: the primitive raised.  A simple primitive pushes no frames,
	// so the caller can still build the continuation frame it would have
	// built before the call and get the same stack.
	syncRaise
)

// callSyncPrimitive runs a primitive that always finishes within the call, so
// that no continuation frame is needed to hold the caller while it runs.  In a
// loop of arithmetic and list access — which is most of what a compiled
// program does — this is the difference between one allocation per call and
// none.
func (m *Machine) callSyncPrimitive(proc Value, args []Value) (Value, syncCall) {
	p, ok := proc.(*Primitive)
	if !ok || !p.Sync {
		return nil, syncNone
	}
	n := len(args)
	if n < p.MinArgs || (p.MaxArgs >= 0 && n > p.MaxArgs) {
		return nil, syncNone // let the general path report the arity error
	}
	m.returning = false
	p.Fn(m, args)
	if !m.returning {
		if m.pending == nil {
			m.Raise(NewError("primitive did not return a value: " + p.Name))
		}
		return nil, syncRaise
	}
	v := m.retVal
	m.retVal, m.returning = nil, false
	return v, syncValue
}

// vmRun is the instruction loop.  It runs the current activation until it
// returns a value (m.Return) or hands control back to the machine (m.apply),
// and it owns the operand stack it was handed: a call to a compiled procedure
// leaves its array to the callee, and a suspension copies what it needs.
func vmRun(m *Machine, code *Code, ip int, env *vmEnv, globals *Env, vals []Value) {
	// The operand stack handed in is private to this activation: nothing that
	// can outlive the call holds it, so appending to it disturbs nothing.
	instrs := code.Instrs
	for {
		// An interrupted evaluation stops here too.  The VM has its own
		// instruction loop, so a check in the interpreter's runLoop would never
		// be reached by compiled code — which is most code — and a cancelled
		// loop would run to completion.  The flag is false unless the REPL is
		// evaluating a form, so running a file pays one load and a branch.
		if m.interruptible() {
			m.RaiseError(ErrInterrupted)
			return
		}
		in := instrs[ip]
		ip++
		switch in.op {
		case opConst:
			vals = append(vals, code.Consts[in.arg1])

		case opLocal:
			vals = append(vals, env.up(int(in.arg1)).slots[in.arg2])
		case opLocalCell:
			vals = append(vals, cellGet(env.up(int(in.arg1)).slots[in.arg2].(*cell)))
		case opLocalCheck:
			v := env.up(int(in.arg1)).slots[in.arg2]
			if _, un := v.(unassigned); un {
				m.Raise(NewError("variable used before initialization",
					code.Names[in.arg2]))
				return
			}
			vals = append(vals, v)
		case opLocalCellCheck:
			v := cellGet(env.up(int(in.arg1)).slots[in.arg2].(*cell))
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
			cellSet(env.up(int(in.arg1)).slots[in.arg2].(*cell), v)
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

		case opNumLt, opNumLe, opNumGt, opNumGe, opNumEq:
			// The arguments are on the stack; the result replaces them.  The
			// fast path handles the case these programs are made of — small
			// exact integers — and anything else falls back to the primitive
			// that implements the operation in full, so the semantics are
			// whatever the standard library says they are.
			n := int(in.arg1)
			base := len(vals) - n
			// The operands are read, not written, so nothing has to be copied
			// out of the way: vals[base:] is only aliased by the array the
			// result is appended to, and appending writes at index base, which
			// is the *first* operand — after every operand has been read by the
			// fast paths below.  The fallback is the case that would trip over
			// it, and it takes its own copy of the operands for that reason.
			args := vals[base:]
			// The overwhelmingly common shape is two small exact integers, and
			// it is worth not going through the variadic helper for it: the
			// helper sets up a loop and a slice, and the whole point of this
			// instruction is that it costs less than the call it replaced.
			if n == 2 {
				if x, ok := args[0].(*Integer); ok && x.small {
					if y, ok := args[1].(*Integer); ok && y.small {
						// Written out rather than called: the instruction loop
						// is far too large for the compiler to inline anything
						// into it, so a helper call here would be paid on
						// every comparison.  That is what the arithmetic
						// instructions measured slower for, and why only the
						// comparisons are instructions.
						a, b := x.i, y.i
						switch in.op {
						case opNumLt:
							vals = append(vals[:base], BooleanOf(a < b))
							continue
						case opNumLe:
							vals = append(vals[:base], BooleanOf(a <= b))
							continue
						case opNumGt:
							vals = append(vals[:base], BooleanOf(a > b))
							continue
						case opNumGe:
							vals = append(vals[:base], BooleanOf(a >= b))
							continue
						case opNumEq:
							vals = append(vals[:base], BooleanOf(a == b))
							continue
						}
					}
				}
			}
			if res, kind := smallIntOp(in.op, args); kind != smallIntNo {
				if kind == smallIntBool {
					vals = append(vals[:base], BooleanOf(res != 0))
				} else {
					vals = append(vals[:base], Int(res))
				}
				continue
			}
			// Not a case the fast path covers — a flonum, a bignum, a
			// rational, or a chain that is not all fixnums.  Rather than
			// reimplementing the numeric tower and its error messages here,
			// the primitive that already implements them is called, with a
			// frame pushed exactly as a general call would, so a raise lands
			// in the right continuation and on the right stack.  The operands
			// are copied first: the array they live in is the one the result
			// is about to be written to.
			heap := make([]Value, len(args))
			copy(heap, args)
			proc := arithmeticPrimitives[in.op]
			if v, res := m.callSyncPrimitive(proc, heap); res == syncValue {
				vals = append(vals[:base], v)
				continue
			}
			f := &fVM{code: code, ip: ip, env: env, globals: globals}
			f.save(vals)
			m.stack = append(m.stack, f)
			return

		case opCall:
			n := int(in.arg1)
			// The arguments are a view of the operand stack, and the frame
			// that suspends this activation keeps its own copy of what is
			// below them, so this array is the activation's again the moment
			// the call returns — which is what lets the simple-primitive path
			// below write the result straight over the arguments.
			args := vals[len(vals)-n:]
			proc := vals[len(vals)-n-1]
			vals = vals[:len(vals)-n-1]
			if v, res := m.callSyncPrimitive(proc, args); res != syncNone {
				if res == syncValue {
					vals = append(vals, v)
					continue
				}
				// The call raised.  A simple primitive pushes no frames, so
				// the frame the caller would have built before the call can
				// be built now: the stack above the raise point is the same.
				f := &fVM{code: code, ip: ip, env: env, globals: globals}
				f.save(vals)
				m.stack = append(m.stack, f)
				return
			}
			f := &fVM{code: code, ip: ip, env: env, globals: globals}
			f.save(vals)
			m.stack = append(m.stack, f)
			if cl, ok := compiledClause(proc, args); ok {
				// Run the callee in this Go frame rather than calling vmRun
				// again: the caller's state is in the frame just pushed, so
				// there is nothing to come back to here.  A loop that calls
				// itself without being in tail position therefore costs a
				// frame on the Scheme stack and nothing on the Go one.
				// The array the caller was using is free: everything that
				// mattered went into the frame just pushed and into the
				// callee's slots, so the callee's operand stack starts in it
				// instead of growing one of its own.
				code, ip, env, globals = cl.code, 0, cl.env, cl.globals
				vals = vals[:0]
				instrs = code.Instrs
				continue
			}
			m.apply(proc, args)
			return

		case opTailCall:
			n := int(in.arg1)
			args := vals[len(vals)-n:]
			proc := vals[len(vals)-n-1]
			if v, res := m.callSyncPrimitive(proc, args); res != syncNone {
				if res == syncValue {
					m.Return(v)
				}
				return
			}
			if cl, ok := compiledClause(proc, args); ok {
				// A tail call replaces this activation: nothing is pushed, so
				// a loop in tail position runs in constant stack.  The
				// operand array is the discarded activation's, which is
				// exactly what the callee wants to start on.
				code, ip, env, globals = cl.code, 0, cl.env, cl.globals
				vals = vals[:0]
				instrs = code.Instrs
				continue
			}
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
