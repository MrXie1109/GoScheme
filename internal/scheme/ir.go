// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"runtime"
	"sort"
	"strings"
)

// Generating LLVM IR.
//
// The compiler in compile.go turns a Scheme form into bytecode for this process
// to run; this turns it into LLVM IR for a program that runs on its own.  They
// are two back ends over one front end: both read the same forms, and the
// generated code calls the same runtime, so a program compiled to a native
// executable and the same program interpreted here agree about what it means.
//
// The shape of the generated program is a **hybrid**, which is the decision
// this file is built around:
//
//   - a **pure function** — one whose body only computes from its parameters and
//     constants, with no side effects and no call out of that set — is compiled
//     to native LLVM: real registers, real branches, a real call.  `(define
//     (add a b) (+ a b))` becomes four instructions, and the integers in it are
//     machine words rather than heap objects.
//   - **anything else** — a macro, a closure over mutable state, a call to a
//     builtin, a continuation — becomes a call into the runtime library, which
//     is the interpreter: the same evaluator this process runs, compiled into a
//     Go archive and linked in.
//
// The second half is what keeps the language whole.  A compiler that refused
// what it could not translate would be a compiler for a subset; one that hands
// the hard form to the runtime keeps `call/cc`, `dynamic-wind`, macros and every
// library working, and gets the native speed where a program spends its time.

// IRProgram is a generated program: the LLVM IR, and what was learned while
// generating it.
type IRProgram struct {
	// IR is the module, as LLVM assembly.
	IR string
	// Native is how many procedures were compiled to native code.
	Native int
	// Runtime is how many top-level forms were left to the interpreter, which
	// is the other half of the split.  A caller reports both, because "why is
	// this program not fast" is usually answered by them.
	Runtime int
	// TopNative is how many top-level forms were emitted as a call into
	// compiled code rather than handed to the interpreter.  It is counted apart
	// from Native, which counts procedures, because a form that runs natively
	// is not a procedure and the two numbers answer different questions.
	TopNative int
	// Refused is one line per procedure the generator could not emit, saying what
	// stopped it.  These are gaps.
	Refused []string
	// Declined is one line per procedure the cost rule left to the interpreter
	// deliberately: it could have been emitted and measurement says emitting it
	// would be slower.  These are choices, and they are not reported by default
	// because there is nothing for a user to act on — the compiler already made
	// the decision that keeps the program fast, and saying so reads as a defect.
	Declined []string
}

// NotCompiled lists every procedure that was not emitted, with its reason,
// whichever kind of reason it was.
//
// A caller that only wants "what did not compile" wants this; the split between
// Refused and Declined matters to a *report*, which is why they are separate
// fields, but not to a test asking whether a reason was given.
func (p *IRProgram) NotCompiled() []string {
	out := make([]string, 0, len(p.Refused)+len(p.Declined))
	out = append(out, p.Refused...)
	out = append(out, p.Declined...)
	return out
}

// CompiledAnything reports whether any of the program became machine code.
//
// A program can have no compiled procedures and still have compiled top-level
// forms — `(loop 200000 0)` after a definition is exactly that — so both counts
// are part of the answer.  This is the question a caller has to ask before it
// can tell a user that nothing was compiled.
func (p *IRProgram) CompiledAnything() bool { return p.Native > 0 || p.TopNative > 0 }

// CompileToIR reads a script and generates a native program for it.
//
// The script is read here rather than by the runtime, so that a syntax error is
// reported by the compiler with the compiler's message and no tool is invoked.
func CompileToIR(source, name string) (*IRProgram, error) {
	return CompileToIRWith(source, name, Options{})
}

// Options are what a caller may ask for beyond the default behaviour.
type Options struct {
	// CompileEverything emits every procedure whose body the generator
	// understands, ignoring the cost rule that otherwise leaves a body with
	// nothing to gain from machine code to the interpreter.
	//
	// It is not the better default and it is not a bug fix: a body whose only
	// work is a call into the runtime is genuinely slower compiled, measured at
	// 2.1x to 3.1x on the shapes the rule refuses.  What it is for is the case
	// where the caller knows better than the rule — a procedure that is called
	// rarely and whose *other* calls are to procedures that did compile, or one
	// kept hot by a profiler rather than by intuition.
	//
	// The rule is a judgement about cost, and a judgement should be visible and
	// overridable rather than silent.
	CompileEverything bool
}

// CompileToIRWith is CompileToIR with the caller's options.
func CompileToIRWith(source, name string, opts Options) (*IRProgram, error) {
	r := NewStringReader(source)
	if name != "" {
		r.Source = name
	}
	forms, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	g := &irGen{module: newIRModule(), compileEverything: opts.CompileEverything}
	if err := g.program(forms); err != nil {
		return nil, err
	}
	return &IRProgram{
		IR:        g.module.String(),
		Native:    g.native,
		Runtime:   g.runtime,
		TopNative: g.topNative,
		Refused:   g.refused,
		Declined:  g.declined,
	}, nil
}

// irGen holds the state of one generation.
type irGen struct {
	module  *irModule
	native  int
	runtime int
	// topNative counts the top-level forms emitted as a call into compiled
	// code.  Such a form is part of the program that runs natively even though
	// it is not a procedure, which is why it is counted apart from native:
	// native is the number the panel means by "native procedures".
	topNative int
	// pure holds the procedures that can be compiled natively, by name, and is
	// what the dependency order is computed from.
	pure map[string]*pureProc
	// compileEverything turns off the cost rule; see Options.
	compileEverything bool
	// declined records the procedures the cost rule left to the interpreter.
	// They are kept apart from refused because the two are different things: a
	// refusal is something the generator cannot emit, and a decline is something
	// it chose not to.  Only the first is worth telling a user about.
	declined []string
	// refused records the procedures that were not compiled natively, and why.
	// It is what `--emit-llvm` explains and what a user asking "why is this
	// slow" needs.
	refused []string
}

// ---------------------------------------------------------------------------
// The module builder
// ---------------------------------------------------------------------------

// irModule builds one LLVM module.  It is a string builder with the bookkeeping
// a module needs: the declarations already emitted, the string constants, and a
// counter for the registers and labels that have to be unique within a function.
type irModule struct {
	declsBuf  strings.Builder
	typesBuf  strings.Builder
	body      strings.Builder
	decls     map[string]bool
	types     map[string]bool
	strings   map[string]string // a string constant, and the global holding it
	nextReg   int
	nextLabel int
	// nextString counts the string constants, so that two of them interned with
	// the same hint get different names.
	nextString int
}

func newIRModule() *irModule {
	return &irModule{
		decls:   map[string]bool{},
		types:   map[string]bool{},
		strings: map[string]string{},
	}
}

// reg returns a fresh SSA register name.
func (m *irModule) reg() string {
	m.nextReg++
	return fmt.Sprintf("%%t%d", m.nextReg)
}

// freshLabel returns a fresh basic-block label.
func (m *irModule) freshLabel(prefix string) string {
	m.nextLabel++
	return fmt.Sprintf("%s%d", prefix, m.nextLabel)
}

// declare records an external function the module calls, once.  Emitting the
// same declare twice is an LLVM error, and a program that calls display in two
// places is the normal case.
func (m *irModule) declare(sig string) {
	if m.decls[sig] {
		return
	}
	m.decls[sig] = true
	fmt.Fprintf(&m.declsBuf, "declare %s\n", sig)
}

// typeDecl writes a named type definition, which is not a declaration: LLVM
// writes it as `%name = type { ... }` with no `declare` in front.
func (m *irModule) typeDecl(def string) {
	if m.types[def] {
		return
	}
	m.types[def] = true
	fmt.Fprintf(&m.typesBuf, "%s\n", def)
}

// String returns the module as LLVM assembly.
//
// The order is the one LLVM wants: the target, then the named types (a function
// signature mentioning one cannot appear before it), then the declarations, then
// the bodies.  Declarations are sorted so that two compilations of the same
// program produce the same file, which is what makes the output diffable.
func (m *irModule) String() string {
	var out strings.Builder
	out.WriteString("; Generated by goscheme compile.\n")
	out.WriteString("; Every call to a runtime function below is a call into the\n")
	out.WriteString("; interpreter, which is linked into the program.\n")
	fmt.Fprintf(&out, "target triple = %q\n\n", irTargetTriple())
	if len(m.types) > 0 {
		out.WriteString(m.typesBuf.String())
		out.WriteString("\n")
	}
	for _, sig := range m.sortedDecls() {
		fmt.Fprintf(&out, "declare %s\n", sig)
	}
	out.WriteString("\n")
	out.WriteString(m.body.String())
	return out.String()
}

func (m *irModule) sortedDecls() []string {
	out := make([]string, 0, len(m.decls))
	for d := range m.decls {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// irTargetTriple is the triple of the machine being compiled for.  The compiler
// runs where the program will run, so it is the host's; cross-compilation would
// read it from a flag.
func irTargetTriple() string {
	arch := "x86_64"
	switch runtime.GOARCH {
	case "arm64":
		arch = "aarch64"
	case "386":
		arch = "i386"
	}
	os_ := "linux-gnu"
	switch runtime.GOOS {
	case "darwin":
		os_ = "apple-darwin"
	case "windows":
		os_ = "windows-gnu"
	}
	return arch + "-unknown-" + os_
}

// ---------------------------------------------------------------------------
// Generating a program
// ---------------------------------------------------------------------------

// program generates the module for a sequence of top-level forms.
//
// Each form becomes a call from `main` into the runtime, in order, because a
// top-level form may define a global, print something, or capture a
// continuation that spans the rest of the file — all of which are the runtime's
// business.  A **pure procedure** among them is additionally compiled to native
// code, and the calls to it are direct; the definition still goes to the
// runtime, so that a program which looks the procedure up by name at run time
// finds it.
//
// That is the hybrid in its simplest form, and the shape of everything below:
// native where the compiler can prove a procedure is a pure function of its
// arguments, the runtime everywhere else.  The alternative — compiling whole
// forms natively — would have to answer "what does this form mean" for every
// special form, macro and library in the language, which is the interpreter's
// job and is already written.
func (g *irGen) program(forms []Value) error {
	m := g.module
	// The runtime entry points.  They are the C ABI of the RE layer.
	m.declare("void @gs_init(i32, i8**)")
	m.declare("void @gs_finish()")
	m.declare("i64 @gs_eval_source(i8*, i64, i8*)")
	m.declare("void @gs_note_native(i8*, i32)")
	// The registration entry point: the name of the procedure, how many
	// arguments it takes, and a pointer to its native body.
	m.declare("void @gs_register(i8*, i64, i8*)")

	var body strings.Builder
	body.WriteString("define i32 @main(i32 %argc, i8** %argv) {\n")
	body.WriteString("entry:\n")
	fmt.Fprintf(&body, "  call void @gs_init(i32 %%argc, i8** %%argv)\n")

	// A top-level (define (name args...) body...) whose body is a pure
	// computation is compiled to a native function *as well*: the form is still
	// handed to the runtime, so that a program which looks the name up at run
	// time finds it, and the native body is what a direct call reaches.
	//
	// "As well" is what makes this safe without analysing the whole program:
	// the runtime definition is the same procedure, so a native call and an
	// interpreted call agree by construction, and a body the scan refuses is
	// simply not emitted.
	//
	// The functions are emitted in dependency order, and that is a requirement
	// rather than tidiness: LLVM wants a function defined before the first call
	// to it, and a forward declaration is not an option — declaring a function
	// that is later defined is a redefinition error.  So `fact` is written
	// before whatever calls it, and a cycle (two procedures that call each
	// other) is refused rather than emitted in an order LLVM will reject.
	g.pure = map[string]*pureProc{}
	var order []string
	// Which procedures are worth scanning at all: a definition of the shape the
	// generator can emit.  The set is collected before any body is scanned,
	// because a body calling another procedure needs to know whether that
	// procedure is a candidate — `(square 3)` is a call worth following, and
	// `(display 3)` is not.
	candidates := map[string]bool{}
	type proc struct {
		name    string
		formals []*Symbol
		body    []Value
	}
	var procs []proc
	for _, form := range forms {
		name, formals, body, ok := topLevelProcedure(form)
		if !ok {
			continue
		}
		if !candidates[name] {
			candidates[name] = true
			procs = append(procs, proc{name, formals, body})
		}
	}
	for _, p := range procs {
		// The derived syntax is rewritten into the core syntax it means, once,
		// before anything looks at the body.  Everything downstream — the walk
		// recognisers, the pure-body scan and the emitter — then sees only core
		// forms, so `cond`, `case`, `when` and `unless` need no rule anywhere
		// else.  See ir_derived.go for why the compiler has to do this itself.
		p.body = expandBody(p.body)
		// A recognised walk is accepted before the body is scanned, and that
		// order matters: the scan understands the forms it can emit, and a loop
		// written as a named let is not one of them — it is `(let NAME (...) ...)`,
		// which the scanner calls malformed bindings.  The walk is emitted as a
		// whole, so there is nothing in it for the scan to judge.
		if _, _, _, isWalk := recogniseWalkIn(p.name, p.formals, p.body); isWalk {
			g.pure[p.name] = &pureProc{name: p.name, formals: p.formals, body: p.body}
			continue
		}
		r := pureBodyIn(p.name, p.formals, p.body, candidates)
		if !r.ok {
			g.refused = append(g.refused, p.name+": "+r.why)
			continue
		}
		if why := notWorthCompiling(r); why != "" && !g.compileEverything {
			// A refusal for cost is a *choice*, not a gap, and it is kept apart
			// from the ones that are gaps so that a caller can report the two
			// differently.  Telling a user that a procedure was "left to the
			// interpreter" when the compiler deliberately declined it — because
			// compiling it measured 2.5 times slower — reads as a defect and
			// sends them looking for one.
			g.declined = append(g.declined, p.name+": "+why)
			continue
		}
		g.pure[p.name] = &pureProc{name: p.name, formals: p.formals, body: p.body, calls: r.calls}
	}
	// Dependency order, with a cycle refused.
	//
	// A cycle is refused by dropping every procedure in it, and the drop
	// cascades: a procedure that called a dropped one can no longer be
	// compiled either, because its body would call something that is not
	// there.  Both passes run until nothing changes, which is what makes the
	// answer independent of the order the map happens to iterate in — the
	// first version of this settled a cycle in one pass and could leave a
	// procedure referencing a body it had just deleted.
	for changed := true; changed; {
		changed = false
		// Which procedures are in a cycle?  A depth-first walk that reports a
		// back edge, tagging every name on the path so that the whole cycle is
		// dropped rather than one arbitrary member of it.
		const (
			white = 0 // not visited
			grey  = 1 // on the current path
			black = 2 // done
		)
		state := map[string]int{}
		var path []string
		var cyclic func(name string) bool
		cyclic = func(name string) bool {
			switch state[name] {
			case grey:
				return true
			case black:
				return false
			}
			state[name] = grey
			path = append(path, name)
			for _, c := range g.pure[name].calls {
				if _, ok := g.pure[c]; ok && cyclic(c) {
					return true
				}
			}
			path = path[:len(path)-1]
			state[name] = black
			return false
		}
		for name := range g.pure {
			path = path[:0]
			if cyclic(name) {
				for _, n := range path {
					g.refused = append(g.refused, n+": it is part of a cycle of native procedures")
					delete(g.pure, n)
				}
				changed = true
				break
			}
		}
		if changed {
			continue
		}
		// A procedure whose callee is not native cannot be compiled: its body
		// would call something that is not there.
		for name, p := range g.pure {
			for _, c := range p.calls {
				if _, ok := g.pure[c]; !ok {
					g.refused = append(g.refused, name+": it calls "+c+", which is not compiled natively")
					delete(g.pure, name)
					changed = true
					break
				}
			}
		}
	}
	// A topological order of what is left.  Every procedure here calls only
	// procedures that are still present and are not in a cycle, so the walk
	// terminates and appends each name after the ones it depends on.
	state := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		if state[name] {
			return
		}
		state[name] = true
		for _, c := range g.pure[name].calls {
			if _, ok := g.pure[c]; ok {
				visit(c)
			}
		}
		order = append(order, name)
	}
	// The roots are visited in the order the definitions appeared in the
	// source, not in map order.  The topological requirement is met either way
	// — a procedure is still appended after everything it calls — but Go
	// randomises map iteration, so visiting in map order made the emitted
	// module differ between two runs of the same compiler on the same input.
	// A compiler whose output is not reproducible cannot be checked by
	// comparing its output, which is exactly how the refactors around this
	// file are verified.
	for _, p := range procs {
		if _, ok := g.pure[p.name]; ok {
			visit(p.name)
		}
	}
	// Emit, keeping only what was actually emitted.
	//
	// A body can pass the scan and still fail to generate — an operator the
	// emitter does not handle after all, a type it cannot express — and such a
	// procedure has no function in the module.  Registering it would put a name
	// and an address in the program for a symbol that does not exist, which is
	// not a slower program but a module the assembler rejects.
	var emitted []string
	for _, name := range order {
		p := g.pure[name]
		if err := g.emitPureFunction(p.name, p.formals, p.body, p.calls); err != nil {
			// Not a reason to fail the compile: the procedure runs, it just
			// runs interpreted.
			g.refused = append(g.refused, name+": "+err.Error())
			continue
		}
		emitted = append(emitted, name)
	}
	// Register every native body with the runtime, so that a call the
	// interpreter makes reaches the machine code.
	//
	// Without this the emitted functions are dead code: the top-level forms are
	// handed to the runtime one by one, so nothing in `main` calls them.  The
	// runtime is told the name and the address, and a procedure call whose name
	// matches goes to the native body instead of walking the interpreter's
	// closure.
	//
	// What is registered is the *adapter*, not the body: a body takes its
	// arguments as (word, tag) pairs, which is what lets the optimizer see the
	// tags, while the runtime calls through one uniform shape whatever the
	// arity.  The adapter unpacks the array into the pairs the body wants, and
	// is the single place the two conventions meet.
	for _, name := range emitted {
		lit := m.stringLiteral(name, "proc"+name)
		fmt.Fprintf(&body, "  call void @gs_register(i8* %s, i64 %d, i8* bitcast (%s (i64, %s*)* @%s to i8*))\n",
			lit, len(g.pure[name].formals), gsVal, gsVal, adapterName(name))
	}
	// A top-level form that calls a compiled procedure with literal arguments is
	// emitted as that call, so the procedure runs natively from the first
	// instruction instead of being entered once by the interpreter.  `(loop
	// 200000 0)` is the whole of a program's work as often as not.
	//
	// The call *replaces* the interpreter's evaluation of that form rather than
	// being added to it.  Doing both is what the first version did, on the
	// reasoning that the interpreter's evaluation is what makes the form's
	// effect happen — and that ran the loop twice, which showed up immediately
	// as `globals` dropping to 0.60×.  Nothing is lost by replacing it: the form
	// is a call to a compiled procedure with literal arguments, it is in tail
	// position at the top level so its value is discarded, and the procedure
	// being called cannot have been redefined between here and there.
	compiled := map[string]bool{}
	for _, name := range emitted {
		compiled[name] = true
	}
	for i, form := range forms {
		if c, ok := recogniseTopCall(form, compiled); ok {
			m.declare("i64 @gs_box_literal(i8*, i64)")
			g.emitTopCall(c, g.pure[c.name].formals, &body, m.reg)
			g.topNative++
			continue
		}
		src := WriteToString(form)
		lit := m.stringLiteral(src, fmt.Sprintf("form%d", i))
		// The source of the form, its length, and a name for error messages.
		name := m.stringLiteral(fmt.Sprintf("<top level %d>", i+1), fmt.Sprintf("name%d", i))
		fmt.Fprintf(&body, "  %s = call i64 @gs_eval_source(i8* %s, i64 %d, i8* %s)\n",
			m.reg(), lit, len(src), name)
		// This form is the interpreter's.  Counting it is what lets a caller
		// say that a program came out with nothing compiled instead of leaving
		// the size of the binary to imply otherwise.
		g.runtime++
	}
	// A pure procedure defined at the top level also gets a native body, and
	// the runtime is told about it: the count is what `--emit-llvm`'s reader
	// and the tests use to see that the hybrid happened.
	if g.native > 0 {
		fmt.Fprintf(&body, "  call void @gs_note_native(i8* null, i32 %d)\n", g.native)
	}
	body.WriteString("  call void @gs_finish()\n")
	body.WriteString("  ret i32 0\n")
	body.WriteString("}\n")
	m.body.WriteString(body.String())
	return nil
}

// stringLiteral interns a string constant and returns the global that holds it.
// A Scheme string is UTF-8 bytes with a length, so the constant carries both:
// a NUL inside a Scheme string is a character like any other, and a C string
// would stop there.
func (m *irModule) stringLiteral(s, hint string) string {
	if g, ok := m.strings[s]; ok {
		return g
	}
	// The hint is a hint: two constants may be interned with the same one, and
	// `(display "n=")` beside `(display " sq=")` does exactly that.  The cache
	// above is keyed by content, so equal strings share a global, but unequal
	// ones must not — which means the name has to carry a counter as well as
	// the hint.
	//
	// The hint also comes from a Scheme name as often as not — a procedure
	// called `positive?` gives `procpositive?` — and `?` is not legal in an
	// LLVM global name, so it is mangled here rather than at every call site.
	// This is the only place a hint becomes an identifier, which is what makes
	// it the right place to do both.
	m.nextString++
	name := fmt.Sprintf("@.%s%d", mangleName(hint), m.nextString)
	m.strings[s] = name
	// The bytes, escaped for LLVM assembly, then a NUL so that the constant is
	// also usable as a C string when its length is not needed.
	fmt.Fprintf(&m.body, "%s = private unnamed_addr constant [%d x i8] c\"%s\\00\"\n",
		name, len(s)+1, llvmEscape(s))
	return name
}

// mangleName turns arbitrary text into an identifier LLVM accepts.  A Scheme
// name may hold characters an LLVM identifier cannot, and two names must not
// collide, so anything outside the identifier set is escaped by byte.
func mangleName(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "_%02x", c)
		}
	}
	return b.String()
}

// llvmEscape renders a Go string as the body of an LLVM string constant: the
// printable characters as themselves, everything else as \XX.
func llvmEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString("\\22")
		case c == '\\':
			b.WriteString("\\5C")
		case c >= 0x20 && c < 0x7f:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "\\%02X", c)
		}
	}
	return b.String()
}

// pureProc is a procedure the scan accepted, waiting to be emitted.
type pureProc struct {
	name    string
	formals []*Symbol
	body    []Value
	calls   []string
}
