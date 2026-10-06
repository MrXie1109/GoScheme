// SPDX-License-Identifier: MIT

package scheme

// The bytecode compiler.
//
// It compiles one body — a lambda body or a top-level form — into a Code for
// the VM in vm.go.  Anything it does not handle makes it give up on that
// *body*, and the body then runs in the tree-walker instead.  That is what
// keeps the two execution paths in agreement: the compiler never has to
// imitate the interpreter for a form, it declines it, and the interpreter runs
// the original source.
//
// Macros are expanded at compile time with the same expander the interpreter
// uses, so a macro use compiles to whatever it expands to.  A macro that
// expands to something uncompilable, or a form that is not in the table below,
// makes the enclosing body interpreted.

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// cframe is one activation frame: what the VM will allocate for it.
type cframe struct {
	parent  *cframe
	names   []*Symbol // per slot, for "used before initialization"
	boxed   []bool
	checked []bool
}

func (f *cframe) slot(sym *Symbol, boxed, checked bool) int {
	f.names = append(f.names, sym)
	f.boxed = append(f.boxed, boxed)
	f.checked = append(f.checked, checked)
	return len(f.names) - 1
}

func (f *cframe) grow(sym *Symbol, boxed, checked bool, slot int) {
	for len(f.names) <= slot {
		f.names = append(f.names, nil)
		f.boxed = append(f.boxed, false)
		f.checked = append(f.checked, false)
	}
	f.names[slot] = sym
	f.boxed[slot] = boxed
	f.checked[slot] = checked
}

func (f *cframe) code() *Code {
	return &Code{
		NSlots:  len(f.names),
		Boxed:   append([]bool(nil), f.boxed...),
		Checked: append([]bool(nil), f.checked...),
		Names:   append([]*Symbol(nil), f.names...),
	}
}

// cblock is one lexical block: the names it made visible and the frame slots
// they live in.  A let extends the block list without a new frame; a lambda
// starts a new frame.
type cblock struct {
	parent *cblock
	frame  *cframe
	names  []*Symbol
	slots  []int
	// macros are the syntax bindings this scope introduces — a let-syntax, a
	// letrec-syntax, or a define-syntax in a body.  They live beside the
	// variables because that is how the interpreter's environments hold them:
	// one namespace, and the innermost binding of either kind wins.
	macros []*Symbol
	syn    []*Macro
	// senv is the shadow environment of this scope: a real Env with the same
	// names, kept only so that a macro defined in this scope has somewhere for
	// its marks to point.  A compiled body's variables are slots, so this is
	// how the compiler remembers where a template's identifiers came from.
	senv *Env
}

// pushScope starts a scope of its own: a block whose names are its own, and a
// shadow environment for the macro expander.
func (c *comp) pushScope(frame *cframe) (*cblock, *Env) {
	savedBlock, savedEnv := c.block, c.env
	c.block = &cblock{parent: savedBlock, frame: frame, senv: c.shadowEnv()}
	c.env = c.block.senv
	return savedBlock, savedEnv
}

// shadowEnv makes the shadow environment of a new scope.  Its parent is the
// global one when there is nothing else, because a template's identifiers that
// are not lexical have to resolve to the globals they name — the mark machinery
// follows the chain the macro was defined in.
func (c *comp) shadowEnv() *Env {
	if c.env == nil {
		return NewEnv(c.globals)
	}
	return NewEnv(c.env)
}

func (c *comp) popScope(block *cblock, env *Env) {
	c.block, c.env = block, env
}

// lookupMarked resolves an identifier the macro expander marked.  The mark
// names the environment the template was written in; when that scope is still
// an ancestor of the one being compiled — which it is whenever a macro is used
// inside the scope that defined it — the binding it stands for has a slot here,
// and hygiene comes out right without an environment at run time.
func (c *comp) lookupMarked(sym *Symbol) (*Macro, int, int, *cframe, bool) {
	def := markEnvOf(sym.Mark)
	if def == nil {
		return nil, 0, 0, nil, false
	}
	name := sym.Orig()
	if name == nil {
		name = sym.Base()
	}
	for b := c.block; b != nil; b = b.parent {
		if b.senv != def {
			continue
		}
		for i := len(b.names) - 1; i >= 0; i-- {
			if b.names[i] == name {
				return nil, c.frameDepth(b.frame), b.slots[i], b.frame, true
			}
		}
		for i := len(b.macros) - 1; i >= 0; i-- {
			if b.macros[i] == name {
				return b.syn[i], 0, 0, nil, true
			}
		}
		return nil, 0, 0, nil, false
	}
	return nil, 0, 0, nil, false
}

// lookupMacro finds a lexical syntax binding of sym.
func (c *comp) lookupMacro(sym *Symbol) (*Macro, bool) {
	for b := c.block; b != nil; b = b.parent {
		for i := len(b.macros) - 1; i >= 0; i-- {
			if b.macros[i] == sym {
				return b.syn[i], true
			}
		}
	}
	return nil, false
}

// lookupName finds the innermost binding of sym in the lexical scopes, and says
// which kind it is: a variable or a syntax binding.  They share one namespace,
// as they do in the interpreter's environments, so the innermost binding of
// either kind is the one that counts.
func (c *comp) lookupName(sym *Symbol) (depth, slot int, frame *cframe, mac *Macro, ok bool) {
	for b := c.block; b != nil; b = b.parent {
		for i := len(b.names) - 1; i >= 0; i-- {
			if b.names[i] == sym {
				return c.frameDepth(b.frame), b.slots[i], b.frame, nil, true
			}
		}
		for i := len(b.macros) - 1; i >= 0; i-- {
			if b.macros[i] == sym {
				return 0, 0, nil, b.syn[i], true
			}
		}
	}
	return 0, 0, nil, nil, false
}

// dropLocal removes a variable binding from the scope being compiled, for a
// name that a definition in the same scope turns into something else: the
// interpreter's Define replaces the binding it finds, and a define-syntax of a
// name the body had pre-bound as a variable is the case that matters.
func (c *comp) dropLocal(sym *Symbol) {
	for i := len(c.block.names) - 1; i >= 0; i-- {
		if c.block.names[i] == sym {
			c.block.names = append(c.block.names[:i], c.block.names[i+1:]...)
			c.block.slots = append(c.block.slots[:i], c.block.slots[i+1:]...)
			return
		}
	}
}

// defineMacro records a syntax binding in the scope being compiled.  It is how
// a compiled body knows a macro the interpreter would have put in its
// environment; nothing is emitted, because every use of it is expanded here.
func (c *comp) defineMacro(name *Symbol, mac *Macro) {
	c.block.macros = append(c.block.macros, name)
	c.block.syn = append(c.block.syn, mac)
	if c.env != nil {
		c.env.Define(name, mac)
	}
}

// useEnv is the environment a macro is expanded in: the shadow one, so that a
// template's identifiers resolve the way they would in the interpreter.
func (c *comp) useEnv() *Env {
	if c.env != nil {
		return c.env
	}
	return c.globals
}

// lookup finds sym and reports the lexical depth from the frame being
// compiled, which is what the VM's instructions take.
func (c *comp) lookup(sym *Symbol) (depth, slot int, frame *cframe, ok bool) {
	for b := c.block; b != nil; b = b.parent {
		for i := len(b.names) - 1; i >= 0; i-- {
			if b.names[i] == sym {
				return c.frameDepth(b.frame), b.slots[i], b.frame, true
			}
		}
	}
	return 0, 0, nil, false
}

// frameDepth counts the activations between the frame being compiled and f.
func (c *comp) frameDepth(f *cframe) int {
	n := 0
	for cur := c.frame; cur != nil && cur != f; cur = cur.parent {
		n++
	}
	return n
}

// comp is the state of one compilation.
type comp struct {
	m       *Machine
	globals *Env
	frame   *cframe
	block   *cblock
	code    *Code
	err     error
	// env is the shadow environment of the scope being compiled (see cblock).
	env *Env
	// assigned is the set of variables the body being compiled assigns with
	// set!; exactly those bindings are boxed.
	assigned map[*Symbol]bool
	// caseKey is the slot holding the key of the case clause being compiled.
	caseKey int
}

func newComp(m *Machine, globals *Env) *comp {
	frame := &cframe{}
	return &comp{m: m, globals: globals, frame: frame, code: &Code{}}
}

func (c *comp) fail(format string, args ...interface{}) {
	if c.err == nil {
		c.err = fmt.Errorf(format, args...)
	}
}

func (c *comp) emit(op opcode, arg1, arg2 int32) int {
	at := len(c.code.Instrs)
	c.code.Instrs = append(c.code.Instrs, instr{op: op, arg1: arg1, arg2: arg2})
	return at
}

func (c *comp) here() int32 { return int32(len(c.code.Instrs)) }

func (c *comp) patch(at int, target int32) { c.code.Instrs[at].arg1 = target }

func (c *comp) konst(v Value) int32 {
	c.code.Consts = append(c.code.Consts, v)
	return int32(len(c.code.Consts) - 1)
}

// tempSlot allocates a slot that holds an intermediate value, such as the key
// of a case form.
func (c *comp) tempSlot() int {
	return c.frame.slot(nil, false, false)
}
