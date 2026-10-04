// SPDX-License-Identifier: MIT

package scheme

// Reading and writing .scmc files: a compiled program as bytes.
//
// A program is a sequence of chunks.  A chunk is either compiled Code, which
// the VM runs, or a source form, which is evaluated — and that is how the
// forms that teach the compiler something (import, define-syntax,
// define-record-type) survive a round trip: they are stored as source and run
// when the file is loaded, while everything the compiler understood is stored
// as bytecode and never parsed again.
//
// The format is deliberately simple: a magic number and a version, then the
// chunks, then the instructions with their literals.  Literals are tagged and
// recursive, so a quoted list or vector is stored as itself.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/big"
)

// Program is a compiled program: the chunks in source order.
type Program struct {
	Chunks []Chunk
}

// Chunk is one piece of a program: compiled when Code is not nil, and source
// to evaluate otherwise.  Steps, when non-empty, is a run of chunks that must
// share one continuation extent — the ordinary top-level forms of a file, some
// of which the compiler took and some of which it declined.
type Chunk struct {
	Code  *Code
	Form  Value
	Steps []Chunk
	// ran marks a chunk that was already run while the program was compiled,
	// in the machine that compiled it: a form that teaches the compiler
	// something has to be evaluated for the rest of the file to compile, and
	// running it a second time at load would repeat its side effects — an
	// `include` would print twice.  It is never written to a file, because a
	// file is read by a machine that has run nothing.
	ran bool
}

const (
	bytecodeMagic = "GSCM"
	// bytecodeVersion 2 added the Steps chunk.  Version 1 files are still read:
	// nothing else about the format changed.
	// 3 added the primitive reference in a constant pool.  Older files are
	// still read; a file from a newer version is refused rather than misread.
	bytecodeVersion = 3
)

// Compiled reports how many of the program's top-level forms are bytecode, and
// how many there are, counting the steps of a mixed chunk.
func (p *Program) Compiled() (compiled, total int) {
	var count func(c Chunk)
	count = func(c Chunk) {
		switch {
		case len(c.Steps) > 0:
			for _, s := range c.Steps {
				count(s)
			}
		case c.Code != nil:
			compiled++
			total++
		default:
			total++
		}
	}
	for _, c := range p.Chunks {
		count(c)
	}
	return compiled, total
}

// CompileProgram compiles the forms of a program.  Forms that affect the
// compile-time environment — import, define-syntax, include — are evaluated as
// they are met, because the compiler has to know the macros and bindings they
// introduce; they are also kept as source chunks so that loading the file
// performs them again for the run.
func CompileProgram(m *Machine, forms []Value, env *Env) (*Program, error) {
	prog := &Program{}
	// Consecutive ordinary forms become one chunk holding a begin, because
	// that is what running the same file does (see RunForms): a continuation
	// captured in one top-level form has to span the rest of the program, and
	// a chunk boundary would end it.  A form that teaches the compiler
	// something — an import, a define-syntax — has to be a chunk of its own so
	// that it runs before the code after it is compiled and loaded.
	var group []Value
	flush := func() {
		if len(group) == 0 {
			return
		}
		form := group[0]
		if len(group) > 1 {
			form = Cons(Intern("begin"), listFromSlice(group))
		}
		if code, err := compileTop(m, form, env); err == nil {
			prog.Chunks = append(prog.Chunks, Chunk{Code: code})
			group = nil
			return
		}
		// The group as a whole did not compile, because a body is all or
		// nothing and one form in it uses something the compiler declines.
		// The forms can still be compiled one at a time — the compiler taking
		// what it can — as long as they run in one extent, which is what a
		// Steps chunk does.  Without this a single `do` or `guard` anywhere in
		// a file would send the whole file down the source path.
		steps := make([]Chunk, 0, len(group))
		for _, f := range group {
			if code, err := compileTop(m, f, env); err == nil {
				steps = append(steps, Chunk{Code: code})
			} else {
				steps = append(steps, Chunk{Form: f})
			}
		}
		if len(steps) == 1 {
			prog.Chunks = append(prog.Chunks, steps[0])
		} else {
			prog.Chunks = append(prog.Chunks, Chunk{Steps: steps})
		}
		group = nil
	}
	for _, form := range forms {
		if teachingForm(form, m, env) {
			flush()
			// Interpreted, not compiled: the point of this run is its effect
			// on the environment — the macro it defines, the library it loads
			// — and a compiled define-syntax expands its uses at compile time
			// and defines nothing.
			if _, err := m.runInterpreted(form, env); err != nil {
				return nil, err
			}
			prog.Chunks = append(prog.Chunks, Chunk{Form: form, ran: true})
			continue
		}
		group = append(group, form)
	}
	flush()
	return prog, nil
}

// teachingForm reports whether a top-level form changes what the compiler
// knows, and so has to be run while compiling: an import, a macro definition,
// an include — or a macro call that expands into one of those.  The R7RS
// suite's
//
//	(define-syntax be-like-begin1
//	  (syntax-rules () ((_ name) (define-syntax name ...))))
//	(be-like-begin1 sequence1)
//
// is the second kind: the forms after it can only be compiled once sequence1
// is a macro, and compiling them first turned (sequence1 0 1 2 3) into a call
// to a binding that turned out to be syntax.
func teachingForm(form Value, m *Machine, env *Env) bool {
	if syntacticTeachingForm(form) {
		return true
	}
	if p, ok := form.(*Pair); ok {
		if s, ok := p.Car.(*Symbol); ok && s.Name == "cond-expand" {
			return condExpandIsTeaching(m, form)
		}
	}
	p, ok := form.(*Pair)
	if !ok {
		return false
	}
	sym, ok := p.Car.(*Symbol)
	if !ok {
		return false
	}
	v, ok := env.Lookup(sym)
	if !ok {
		return false
	}
	mac, ok := v.(*Macro)
	if !ok {
		return false
	}
	expanded, err := mac.Expand(form, env)
	if err != nil {
		return false // the compiler will report it when it compiles the form
	}
	return syntacticTeachingForm(expanded)
}

// condExpandIsTeaching runs the same choice cond-expand would, to see whether
// what it chose teaches the compiler something: an import or a define-syntax
// inside a cond-expand has to be run while compiling like any other.
func condExpandIsTeaching(m *Machine, form Value) bool {
	p, ok := form.(*Pair)
	if !ok {
		return false
	}
	forms, ok := condExpandBody(m, mustSlice(p.Cdr))
	if !ok {
		return false
	}
	for _, f := range forms {
		if teachingForm(f, m, m.Global) {
			return true
		}
	}
	return false
}

// syntacticTeachingForm is the check that needs no expansion.
func syntacticTeachingForm(form Value) bool {
	p, ok := form.(*Pair)
	if !ok {
		return false
	}
	s, ok := p.Car.(*Symbol)
	if !ok {
		return false
	}
	switch s.Name {
	case "import", "define-syntax", "include", "include-ci", "define-library":
		return true
	case "begin":
		// A top-level begin may hold definitions of macros, which have to be
		// run for the rest to compile.
		items, _ := ListToSlice(p.Cdr)
		for _, it := range items {
			if syntacticTeachingForm(it) {
				return true
			}
		}
	}
	return false
}

// RunChunk runs one chunk of a program in env.
func (m *Machine) RunChunk(c Chunk, env *Env) (Value, error) {
	return m.guardedRun(func() (Value, error) {
		baseStack, baseWinds, baseHands := len(m.stack), len(m.winds), len(m.hands)
		switch {
		case len(c.Steps) > 0:
			m.startSteps(c.Steps, env)
		case c.Code != nil:
			m.runCompiledTop(c.Code, env)
		default:
			m.Eval(c.Form, env)
		}
		return m.runLoop(baseStack, baseWinds, baseHands)
	})
}

// fSteps runs the chunks of a mixed group in order and in one extent: each
// step's value is discarded except the last one's, exactly as the forms of a
// begin are.  A continuation captured in one step therefore covers the rest of
// the group, which is the whole reason the steps are not chunks of their own.
type fSteps struct {
	steps []Chunk
	env   *Env
}

func (f *fSteps) resume(m *Machine, v Value) { m.startSteps(f.steps, f.env) }

// startSteps begins the first of steps and leaves the rest to follow it.  A
// step that was already run while the program was compiled — a form that taught
// the compiler something — is passed over.
func (m *Machine) startSteps(steps []Chunk, env *Env) {
	for len(steps) > 0 && steps[0].ran {
		steps = steps[1:]
	}
	if len(steps) == 0 {
		m.Return(UnspecifiedValue)
		return
	}
	first := steps[0]
	if len(first.Steps) > 0 {
		// A step that is itself a run of steps — a group the compiler took
		// form by form.  It belongs to this extent too, so its steps simply
		// join the queue; running them as a chunk of their own would end the
		// extent, and mistaking the chunk for a form (its Form is nil) ran
		// nothing at all.
		joined := make([]Chunk, 0, len(first.Steps)+len(steps)-1)
		joined = append(joined, first.Steps...)
		joined = append(joined, steps[1:]...)
		m.startSteps(joined, env)
		return
	}
	if len(steps) > 1 {
		m.stack = append(m.stack, &fSteps{steps: steps[1:], env: env})
	}
	if first.Code != nil {
		m.runCompiledTop(first.Code, env)
	} else {
		m.Eval(first.Form, env)
	}
}

// RunProgram runs every chunk of a program, in order.
func (m *Machine) RunProgram(p *Program, env *Env) (Value, error) {
	result := Value(UnspecifiedValue)
	for _, c := range p.Chunks {
		if c.ran {
			continue // already run, while this machine compiled the program
		}
		v, err := m.RunChunk(c, env)
		if err != nil {
			return nil, err
		}
		result = v
	}
	return result, nil
}

// RunFormsCompiled runs the forms of a file the way `goscheme compile` writes
// them: a form that teaches the compiler something is evaluated and becomes a
// chunk of its own, the ordinary forms are compiled in groups, and a group the
// compiler cannot take whole is compiled form by form.  It is what running a
// script does, and it is the program a .scmc file holds.
//
// Running a file used to go through RunForms, which makes the whole file one
// body — and since a body is all or nothing, one `import` sent every form in
// the file to the tree-walker.  Every suite file starts with an import, so the
// suites were being interpreted in both modes, and so was most of what anyone
// runs.
//
// A machine that has been asked to interpret everything has nothing to compile
// and keeps the tree-walker's semantics exactly: the whole file as one body.
func (m *Machine) RunFormsCompiled(forms []Value, env *Env) (Value, error) {
	if m.Interpret || compileDisabled {
		return m.RunForms(forms, env)
	}
	return m.guardedRun(func() (Value, error) {
		baseStack, baseWinds, baseHands := len(m.stack), len(m.winds), len(m.hands)
		if err := m.startForms(forms, env); err != nil {
			return nil, err
		}
		return m.runLoop(baseStack, baseWinds, baseHands)
	})
}

// startForms begins the forms of a file in the *current* evaluation, compiling
// them the way RunFormsCompiled does.  The machine's own loop drives them, so
// this is what a primitive that loads a file uses: `load` cannot call
// RunFormsCompiled, because that would start a second evaluation inside the one
// that called it.
func (m *Machine) startForms(forms []Value, env *Env) error {
	if m.Interpret || compileDisabled {
		m.EvalSeq(forms, env)
		return nil
	}
	prog, err := CompileProgram(m, forms, env)
	if err != nil {
		return err
	}
	m.startSteps(prog.Chunks, env)
	return nil
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// WriteBytecode writes a compiled program.
// bytecodeShebang is written at the head of a compiled file so that it can be
// run directly, the way a script can:
//
//	goscheme compile prog.scm -o prog.scmc && chmod +x prog.scmc && ./prog.scmc
//
// The reader skips it.  It is a line of text rather than part of the format, so
// a file that has been through the interpreter is still a valid compiled file
// whichever way it is opened, and a reader that does not know about it reports
// a bad magic number rather than reading nonsense.
const bytecodeShebang = "#!" + bytecodeInterpreter + "\n"

// bytecodeInterpreter is the program named in the shebang.  A compiled file
// says "goscheme" because that is the name the interpreter is installed under;
// a file run through a differently named binary still works, because the
// shebang is only consulted by the kernel when the file is executed.
const bytecodeInterpreter = "/usr/bin/env goscheme"

func WriteBytecode(w io.Writer, p *Program) error {
	bw := &byteWriter{w: &bufWriter{w: w}}
	bw.raw([]byte(bytecodeShebang))
	bw.raw([]byte(bytecodeMagic))
	bw.u8(bytecodeVersion)
	bw.uvarint(uint64(len(p.Chunks)))
	for _, c := range p.Chunks {
		bw.chunk(c)
	}
	if bw.err != nil {
		return bw.err
	}
	return bw.w.Flush()
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// ReadBytecode reads a compiled program.
func ReadBytecode(r io.Reader) (*Program, error) {
	// A shebang line, if the file was written to be executable, is read and
	// discarded.  It is a line of text rather than part of the format, so the
	// magic number still has to follow it; anything else that begins with #! is
	// not a compiled file, and the magic check below says so.
	buffered := bufio.NewReader(r)
	if head, err := buffered.Peek(2); err == nil && string(head) == "#!" {
		if _, err := buffered.ReadString('\n'); err != nil {
			return nil, fmt.Errorf("bytecode: unterminated #! line")
		}
	}
	br := &byteReader{r: buffered}
	magic := make([]byte, 4)
	if _, err := io.ReadFull(br.r, magic); err != nil {
		return nil, fmt.Errorf("bytecode: %v", err)
	}
	if string(magic) != bytecodeMagic {
		return nil, fmt.Errorf("bytecode: not a .scmc file")
	}
	if v := br.u8(); v < 1 || v > bytecodeVersion {
		return nil, fmt.Errorf("bytecode: version %d, but this interpreter speaks %d", v, bytecodeVersion)
	}
	n := br.uvarint()
	if br.err != nil {
		return nil, br.err
	}
	prog := &Program{Chunks: make([]Chunk, 0, n)}
	for i := uint64(0); i < n; i++ {
		c, err := br.chunk()
		if err != nil {
			return nil, err
		}
		prog.Chunks = append(prog.Chunks, c)
	}
	return prog, nil
}

// ---------------------------------------------------------------------------
// The binary reader and writer
// ---------------------------------------------------------------------------

type byteWriter struct {
	w   *bufWriter
	err error
}

// bufWriter buffers the bytes of a file, so that a large constant pool does
// not become one write per byte.
type bufWriter struct {
	w   io.Writer
	buf []byte
}

func (b *bufWriter) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if len(b.buf) >= 64*1024 {
		return len(p), b.Flush()
	}
	return len(p), nil
}

func (b *bufWriter) Flush() error {
	if len(b.buf) == 0 {
		return nil
	}
	_, err := b.w.Write(b.buf)
	b.buf = b.buf[:0]
	return err
}

func (b *byteWriter) raw(p []byte) {
	if b.err != nil {
		return
	}
	_, b.err = b.w.Write(p)
}

func (b *byteWriter) u8(v byte) { b.raw([]byte{v}) }

func (b *byteWriter) uvarint(v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	b.raw(tmp[:n])
}

func (b *byteWriter) svarint(v int64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutVarint(tmp[:], v)
	b.raw(tmp[:n])
}

func (b *byteWriter) str(s string) {
	b.uvarint(uint64(len(s)))
	b.raw([]byte(s))
}

func (b *byteWriter) u64(v uint64) {
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], v)
	b.raw(tmp[:])
}

// chunk writes one chunk: its tag and its contents.
func (b *byteWriter) chunk(c Chunk) {
	switch {
	case len(c.Steps) > 0:
		b.u8(2)
		b.uvarint(uint64(len(c.Steps)))
		for _, s := range c.Steps {
			b.chunk(s)
		}
	case c.Code != nil:
		b.u8(1)
		b.code(c.Code)
	default:
		b.u8(0)
		b.datum(c.Form, map[interface{}]bool{})
	}
}

func (b *byteWriter) code(c *Code) {
	b.str(c.Name)
	b.uvarint(uint64(len(c.Instrs)))
	for _, in := range c.Instrs {
		b.u8(byte(in.op))
		b.svarint(int64(in.arg1))
		b.svarint(int64(in.arg2))
	}
	b.uvarint(uint64(len(c.Consts)))
	for _, k := range c.Consts {
		b.datum(k, map[interface{}]bool{})
	}
	b.uvarint(uint64(c.NSlots))
	b.bits(c.Boxed, c.NSlots)
	b.bits(c.Checked, c.NSlots)
	b.uvarint(uint64(c.NParams))
	if c.HasRest {
		b.u8(1)
	} else {
		b.u8(0)
	}
	b.uvarint(uint64(c.RestSlot))
	// The slot names are only for error messages, but they are what a user
	// sees, so they travel with the code.
	b.uvarint(uint64(len(c.Names)))
	for _, s := range c.Names {
		if s == nil {
			b.u8(0)
			continue
		}
		b.u8(1)
		b.str(s.Name)
	}
}

func (b *byteWriter) bits(set []bool, n int) {
	var cur byte
	for i := 0; i < n; i++ {
		if i < len(set) && set[i] {
			cur |= 1 << uint(i%8)
		}
		if i%8 == 7 {
			b.u8(cur)
			cur = 0
		}
	}
	if n%8 != 0 {
		b.u8(cur)
	}
}

// Datum tags.  The numbers are part of the file format.
const (
	tEmpty      = 0
	tTrue       = 1
	tFalse      = 2
	tUnspec     = 3
	tUnassigned = 4
	tChar       = 5
	tInt        = 6
	tRational   = 7
	tFloat      = 8
	tComplex    = 9
	tString     = 10
	tSymbol     = 11
	tPair       = 12
	tVector     = 13
	tBytevector = 14
	tCode       = 15
	tEof        = 16
	// tPrimitive is a reference to one of the interpreter's own runtime
	// helpers, by name.  A compiled program needs a few of them — the guard
	// helper, for one — and they are values the compiler puts in a constant
	// pool, so the file has to be able to name them.  Only the helpers in
	// internalPrimitives can be written: nothing else may put a primitive in
	// a constant pool, and nothing a program can write can name one.
	tPrimitive = 17
)

// internalPrimitives are the runtime helpers a compiled file may refer to by
// name.  They are looked up here rather than in an environment, so a program
// can neither see them nor rebind them, and the reader needs no machine.
var internalPrimitives = map[string]*Primitive{
	"guard-helper":       guardHelper,
	"guard-re-raise":     guardReRaise,
	"do-continue":        doContinue,
	"delay":              promiseHelper,
	"case-lambda":        caseLambdaHelper,
	"assert":             assertFailed,
	"go":                 goHelper,
	"bind-values":        bindValues,
	"define-record-type": recordTypeHelper,
	"select":             selectHelper,
	"match":              matchHelper,
}

func (b *byteWriter) datum(v Value, seen map[interface{}]bool) {
	if b.err != nil {
		return
	}
	switch x := v.(type) {
	case Empty:
		b.u8(tEmpty)
	case Boolean:
		if bool(x) {
			b.u8(tTrue)
		} else {
			b.u8(tFalse)
		}
	case Unspecified:
		b.u8(tUnspec)
	case unassigned:
		b.u8(tUnassigned)
	case Char:
		b.u8(tChar)
		b.uvarint(uint64(x))
	case *Integer:
		b.u8(tInt)
		b.str(x.Big().String())
	case *Rational:
		b.u8(tRational)
		b.str(x.R.RatString())
	case Float:
		b.u8(tFloat)
		b.u64(math.Float64bits(float64(x)))
	case *Complex:
		b.u8(tComplex)
		b.datum(x.Re, seen)
		b.datum(x.Im, seen)
	case *String:
		b.u8(tString)
		b.str(x.Value())
	case *Symbol:
		b.u8(tSymbol)
		b.str(x.Name)
	case *Pair:
		if seen[x] {
			b.err = fmt.Errorf("bytecode: cannot store cyclic data")
			return
		}
		seen[x] = true
		b.u8(tPair)
		b.datum(x.Car, seen)
		b.datum(x.Cdr, seen)
		delete(seen, x)
	case *Vector:
		if seen[x] {
			b.err = fmt.Errorf("bytecode: cannot store cyclic data")
			return
		}
		seen[x] = true
		b.u8(tVector)
		b.uvarint(uint64(len(x.Items)))
		for _, e := range x.Items {
			b.datum(e, seen)
		}
		delete(seen, x)
	case *Bytevector:
		b.u8(tBytevector)
		b.uvarint(uint64(len(x.Bytes)))
		b.raw(x.Bytes)
	case *Code:
		b.u8(tCode)
		b.code(x)
	case *Primitive:
		if _, ok := internalPrimitives[x.Name]; !ok {
			b.err = fmt.Errorf("bytecode: cannot store the primitive %s", x.Name)
			return
		}
		b.u8(tPrimitive)
		b.str(x.Name)
	case EOF:
		b.u8(tEof)
	case nil:
		b.u8(tUnspec)
	default:
		b.err = fmt.Errorf("bytecode: cannot store %s", WriteToString(v))
	}
}

type byteReader struct {
	r   io.Reader
	err error
	one [1]byte
	buf [8]byte
}

func (b *byteReader) u8() byte {
	if b.err != nil {
		return 0
	}
	_, b.err = io.ReadFull(b.r, b.one[:])
	return b.one[0]
}

func (b *byteReader) uvarint() uint64 {
	if b.err != nil {
		return 0
	}
	v, err := binary.ReadUvarint(b)
	if err != nil {
		b.err = err
		return 0
	}
	return v
}

func (b *byteReader) svarint() int64 {
	if b.err != nil {
		return 0
	}
	v, err := binary.ReadVarint(b)
	if err != nil {
		b.err = err
		return 0
	}
	return v
}

// ReadByte implements io.ByteReader, which is what the varint readers need.
func (b *byteReader) ReadByte() (byte, error) {
	if b.err != nil {
		return 0, b.err
	}
	_, err := io.ReadFull(b.r, b.one[:])
	if err != nil {
		b.err = err
		return 0, err
	}
	return b.one[0], nil
}

func (b *byteReader) u64() uint64 {
	if b.err != nil {
		return 0
	}
	if _, b.err = io.ReadFull(b.r, b.buf[:8]); b.err != nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b.buf[:8])
}

func (b *byteReader) str() string {
	n := b.uvarint()
	if b.err != nil || n > 1<<30 {
		if b.err == nil {
			b.err = fmt.Errorf("bytecode: bad string length")
		}
		return ""
	}
	buf := make([]byte, n)
	if _, b.err = io.ReadFull(b.r, buf); b.err != nil {
		return ""
	}
	return string(buf)
}

func (b *byteReader) bits(n int) []bool {
	out := make([]bool, n)
	cur := byte(0)
	for i := 0; i < n; i++ {
		if i%8 == 0 {
			cur = b.u8()
		}
		out[i] = cur&(1<<uint(i%8)) != 0
	}
	return out
}

// chunk reads one chunk, which is a form, compiled code, or a run of chunks
// that share an extent.
func (b *byteReader) chunk() (Chunk, error) {
	switch b.u8() {
	case 0:
		return Chunk{Form: b.datum()}, b.err
	case 1:
		return Chunk{Code: b.code()}, b.err
	case 2:
		n := b.uvarint()
		if b.err != nil {
			return Chunk{}, b.err
		}
		// A nested run must be long enough to be worth the frame, and cannot
		// itself be nested: nothing writes one that way.
		if n < 2 {
			return Chunk{}, fmt.Errorf("bytecode: a chunk run of %d", n)
		}
		steps := make([]Chunk, 0, n)
		for i := uint64(0); i < n; i++ {
			c, err := b.chunk()
			if err != nil {
				return Chunk{}, err
			}
			if len(c.Steps) > 0 {
				return Chunk{}, fmt.Errorf("bytecode: nested chunk run")
			}
			steps = append(steps, c)
		}
		return Chunk{Steps: steps}, nil
	default:
		return Chunk{}, fmt.Errorf("bytecode: bad chunk tag")
	}
}

func (b *byteReader) code() *Code {
	c := &Code{}
	c.Name = b.str()
	n := b.uvarint()
	c.Instrs = make([]instr, n)
	for i := range c.Instrs {
		op := b.u8()
		a1 := b.svarint()
		a2 := b.svarint()
		if int(op) >= opcodeCount {
			b.err = fmt.Errorf("bytecode: unknown opcode %d", op)
			return c
		}
		c.Instrs[i] = instr{op: opcode(op), arg1: int32(a1), arg2: int32(a2)}
	}
	nc := b.uvarint()
	c.Consts = make([]Value, nc)
	for i := range c.Consts {
		c.Consts[i] = b.datum()
	}
	c.NSlots = int(b.uvarint())
	c.Boxed = b.bits(c.NSlots)
	c.Checked = b.bits(c.NSlots)
	c.NParams = int(b.uvarint())
	c.HasRest = b.u8() == 1
	c.RestSlot = int(b.uvarint())
	nn := b.uvarint()
	c.Names = make([]*Symbol, nn)
	for i := range c.Names {
		if b.u8() == 0 {
			continue
		}
		c.Names[i] = Intern(b.str())
	}
	// A compiled clause carries parameter symbols for its arity checks, and
	// they are not stored: they are placeholders that are never bound.
	c.Params = placeholderParams(c.NParams)
	return c
}

func (b *byteReader) datum() Value {
	switch b.u8() {
	case tEmpty:
		return Nil
	case tTrue:
		return True
	case tFalse:
		return False
	case tUnspec:
		return UnspecifiedValue
	case tUnassigned:
		return Unassigned
	case tChar:
		return Char(b.uvarint())
	case tInt:
		v, ok := new(big.Int).SetString(b.str(), 10)
		if !ok {
			b.err = fmt.Errorf("bytecode: bad integer")
			return nil
		}
		return BigInt(v)
	case tRational:
		r, ok := new(big.Rat).SetString(b.str())
		if !ok {
			b.err = fmt.Errorf("bytecode: bad rational")
			return nil
		}
		return &Rational{R: r}
	case tFloat:
		return Float(math.Float64frombits(b.u64()))
	case tComplex:
		re := b.datum()
		im := b.datum()
		return &Complex{Re: re, Im: im}
	case tString:
		return NewString(b.str())
	case tSymbol:
		return Intern(b.str())
	case tPair:
		car := b.datum()
		cdr := b.datum()
		return Cons(car, cdr)
	case tVector:
		n := b.uvarint()
		items := make([]Value, n)
		for i := range items {
			items[i] = b.datum()
		}
		return &Vector{Items: items}
	case tBytevector:
		n := b.uvarint()
		buf := make([]byte, n)
		if _, b.err = io.ReadFull(b.r, buf); b.err != nil {
			return nil
		}
		return NewBytevectorFrom(buf)
	case tCode:
		return b.code()
	case tEof:
		return EOFObject
	case tPrimitive:
		name := b.str()
		p, ok := internalPrimitives[name]
		if !ok {
			b.err = fmt.Errorf("bytecode: no runtime helper called %s", name)
			return nil
		}
		return p
	}
	b.err = fmt.Errorf("bytecode: bad datum tag")
	return nil
}
