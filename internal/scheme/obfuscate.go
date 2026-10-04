// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"math/rand"
)

// Obfuscation.
//
// A compiled file carries more than the instructions: every body has a name for
// the disassembler and for stack traces, every slot has the name of the
// variable that lives in it, and every global reference is a symbol.  None of
// that is needed to run the program, and all of it says what the program is —
// a slot called "password" next to a call to string=? is a sentence.
//
// Obfuscate rewrites a compiled program to remove the names and to renumber the
// constants, so that reading the file says what it *does* and not what it
// *means*:
//
//   - body names become "b0", "b1", … in the order the bodies appear;
//   - slot names and parameter names are dropped, so a slot has a number and
//     nothing else;
//   - the constant pool of each body is shuffled, and every instruction's
//     operand is rewritten to the new index — the instructions are the program
//     and are not touched, but which constant sits at index 0 is no longer a
//     clue about where the code begins.
//
// What it is not: it does not encrypt, and it does not pretend to.  Strings and
// numbers the program uses are still in the file, because the program needs
// them; a determined reader can still follow the instructions.  It removes the
// signposts, not the road.  Compiling with -obfuscate twice produces different
// files, because the shuffle is seeded from the clock.
//
// The file stays a valid .scmc file of the same version, and the interpreter
// reads it without knowing it was obfuscated — the names were never needed to
// run it, which is why this is a pass over the compiled program rather than a
// change to the format.

// Obfuscate removes the identifying names from a compiled program and shuffles
// its constant pools, in place.  It returns the number of bodies it renamed, so
// that a caller can report what happened.
func Obfuscate(p *Program) int {
	o := &obfuscator{names: map[*Code]string{}, rng: rand.New(rand.NewSource(rand.Int63()))}
	n := 0
	for i := range p.Chunks {
		n += o.chunk(&p.Chunks[i])
	}
	return n
}

type obfuscator struct {
	names map[*Code]string
	rng   *rand.Rand
	next  int
}

// bodyName returns the name to give a body: a counter, so that the order of the
// bodies is still visible (it is the order of the instructions that refer to
// them) but nothing else is.
func (o *obfuscator) bodyName(c *Code) string {
	if n, ok := o.names[c]; ok {
		return n
	}
	n := fmt.Sprintf("b%d", o.next)
	o.next++
	o.names[c] = n
	return n
}

func (o *obfuscator) chunk(c *Chunk) int {
	// A chunk that is still source keeps its source: it is a form that teaches
	// the compiler something, and the interpreter has to read it as it is
	// written.  Its names are therefore still in the file, which is a limit of
	// obfuscating a file that is not wholly compiled — and it is reported
	// rather than hidden, by counting only the compiled bodies.
	n := o.code(c.Code)
	for i := range c.Steps {
		n += o.chunk(&c.Steps[i])
	}
	return n
}

func (o *obfuscator) code(c *Code) int {
	if c == nil {
		return 0
	}
	c.Name = o.bodyName(c)
	// The slots keep their count and lose their names.  Names is not purely
	// decorative: opLocalCheck reads it to say which variable was used before
	// it was initialized, so it has to stay one symbol per slot — an array of
	// nils would panic the moment such a program was run.  The symbols are
	// replaced rather than removed, so the message is still a message and the
	// name in it is not the program's.
	if c.Names != nil {
		for i := range c.Names {
			c.Names[i] = Intern(fmt.Sprintf("v%d", i))
		}
	}
	// The parameters are symbols a closure is built from, and nothing reads
	// their names, so they can go.
	c.Params = nil
	n := 1
	// The constants are walked once, and the walk descends into the bodies it
	// finds — a nested body is renamed here, before its parent's pool is
	// shuffled.  Doing the descent twice would shuffle a pool that the
	// instructions had already been rewritten for, and the second permutation
	// would leave every operand naming the wrong constant.
	for _, k := range c.Consts {
		if sub, ok := k.(*Code); ok {
			n += o.code(sub)
			continue
		}
		o.value(k)
	}
	o.shuffleConsts(c)
	return n
}

// value walks a constant, renaming the bodies inside data structures.  A
// quoted list holds no code, but a quasiquote can compile a body into a
// template, so the walk has to go all the way down.
func (o *obfuscator) value(v Value) {
	switch x := v.(type) {
	case *Code:
		o.code(x)
	case *Pair:
		o.value(x.Car)
		o.value(x.Cdr)
	case *Vector:
		for _, e := range x.Items {
			o.value(e)
		}
	}
}

// shuffleConsts permutes the constant pool and rewrites the instructions that
// name constants.  Two things make this more than a rotation:
//
//   - the operand is the index *from the end* for the constants that a body
//     pushes with opConst, and the interpreter reads it that way, so only the
//     values are moved and the instruction operands are rewritten to match;
//   - six instructions index the pool, and all six have to be rewritten: the
//     three that push or define a constant, the one that pushes a symbol to
//     look up, and the two that build a closure from a compiled body.
func (o *obfuscator) shuffleConsts(c *Code) {
	n := len(c.Consts)
	if n < 2 {
		return
	}
	// A permutation, built by shuffling the indices.
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	o.rng.Shuffle(n, func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })

	// old index -> new index
	moved := make([]Value, n)
	for i, p := range perm {
		moved[p] = c.Consts[i]
	}
	// perm[i] is where the constant that was at i went, so the instruction
	// operand that named i has to name perm[i].
	c.Consts = moved
	for i := range c.Instrs {
		in := &c.Instrs[i]
		switch in.op {
		case opConst, opClosure, opInterpClosure,
			opGlobal, opSetGlobal, opDefineGlobal:
			if int(in.arg1) < n {
				in.arg1 = int32(perm[in.arg1])
			}
		}
	}
}

// ObfuscateGlobals renames the global variables a program defines, where it can
// prove that nothing outside the compiled program refers to them by name.
//
// A global is looked up at run time by symbol, so its name is part of the
// program in a way a slot name is not — which is why this is separate from
// Obfuscate and conservative:
//
//   - a name that appears in a *source* chunk is not renamed, because the chunk
//     is evaluated by the interpreter as written and would look up the old name;
//   - a name the program never defines is not renamed: it belongs to a library,
//     and the library knows it by its own name;
//   - a name that is not a valid identifier after renaming would be useless, so
//     the replacements are ordinary symbols ("g0", "g1", …).
//
// It cannot be complete, and does not claim to be: a program that builds a
// symbol from a string and looks it up will not be caught, and the name it
// builds stays as it was.  What this buys is that the *stored* program does not
// read like a description of itself.
func ObfuscateGlobals(p *Program, m *Machine) int {
	defined := map[string]bool{}
	used := map[string]bool{}
	fromSource := map[string]bool{}

	var scanValue func(Value)
	var scanCode func(*Code)
	scanValue = func(v Value) {
		switch x := v.(type) {
		case *Code:
			scanCode(x)
		case *Pair:
			if s, ok := x.Car.(*Symbol); ok {
				used[s.Name] = true
			}
			scanValue(x.Car)
			scanValue(x.Cdr)
		case *Vector:
			for _, e := range x.Items {
				scanValue(e)
			}
		}
	}
	scanCode = func(c *Code) {
		if c == nil {
			return
		}
		for i := range c.Instrs {
			in := &c.Instrs[i]
			switch in.op {
			case opGlobal, opSetGlobal:
				if s, ok := constSym(c, in.arg1); ok {
					used[s] = true
				}
			case opDefineGlobal:
				if s, ok := constSym(c, in.arg1); ok {
					defined[s] = true
					used[s] = true
				}
			}
		}
		for _, k := range c.Consts {
			if sub, ok := k.(*Code); ok {
				scanCode(sub)
			}
		}
	}
	for i := range p.Chunks {
		var walk func(*Chunk)
		walk = func(c *Chunk) {
			if c.Form != nil {
				// A source chunk is read by the interpreter, so every symbol it
				// mentions keeps its name.
				collectDatumSymbols(c.Form, fromSource)
			}
			scanCode(c.Code)
			for j := range c.Steps {
				walk(&c.Steps[j])
			}
		}
		walk(&p.Chunks[i])
	}

	// The names that can be renamed: defined by the program, not mentioned by
	// any source chunk, and not a name the global environment already had
	// before the program ran (that would be a library's).
	rename := map[string]string{}
	next := 0
	for name := range defined {
		if fromSource[name] {
			continue
		}
		if _, builtin := m.Builtin.Lookup(Intern(name)); builtin {
			continue
		}
		rename[name] = fmt.Sprintf("g%d", next)
		next++
	}
	if len(rename) == 0 {
		return 0
	}
	// Rewrite the symbol constants in every compiled body.  Symbols are interned
	// and shared, so a constant is replaced rather than mutated: the machine
	// that compiled this program keeps its own symbols.
	var rewrite func(*Code)
	rewrite = func(c *Code) {
		if c == nil {
			return
		}
		for i := range c.Instrs {
			in := &c.Instrs[i]
			switch in.op {
			case opGlobal, opSetGlobal, opDefineGlobal:
				if s, ok := constSym(c, in.arg1); ok {
					if newName, ok := rename[s]; ok {
						c.Consts[in.arg1] = Intern(newName)
					}
				}
			}
		}
		for _, k := range c.Consts {
			if sub, ok := k.(*Code); ok {
				rewrite(sub)
			}
		}
	}
	for i := range p.Chunks {
		var walk func(*Chunk)
		walk = func(c *Chunk) {
			rewrite(c.Code)
			for j := range c.Steps {
				walk(&c.Steps[j])
			}
		}
		walk(&p.Chunks[i])
	}
	return len(rename)
}

// constSym returns the name of the symbol constant an instruction names.
func constSym(c *Code, idx int32) (string, bool) {
	if int(idx) >= len(c.Consts) {
		return "", false
	}
	s, ok := c.Consts[idx].(*Symbol)
	if !ok {
		return "", false
	}
	return s.Name, true
}

// collectSymbols records every symbol in a datum.
func collectDatumSymbols(v Value, into map[string]bool) {
	switch x := v.(type) {
	case *Symbol:
		into[x.Name] = true
	case *Pair:
		collectDatumSymbols(x.Car, into)
		collectDatumSymbols(x.Cdr, into)
	case *Vector:
		for _, e := range x.Items {
			collectDatumSymbols(e, into)
		}
	}
}
