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
	//
	// Nothing reports them to a user: a compiler reports what it produced, and
	// every gap this one had has been closed.  They are kept because a failing
	// test needs to say *why* it failed, and "not compiled" without a reason is
	// the least useful thing a test can print.
	Refused []string
	// Declined is one line per procedure the cost rule left to the interpreter:
	// it could have been emitted and measurement says emitting it would be
	// slower.  A choice, not a gap, and not something a user needs told about —
	// the compiler made the decision in the program's favour.
	Declined []string
}

// NotCompiled lists every procedure that was not emitted, with its reason,
// whichever kind of reason it was.
//
// The two are separate fields because they are different things — a gap and a
// choice — but a test asking "why is this not compiled" wants both, which is what
// this is for.  Nothing user-facing reads either list; see Refused.
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
	g := &irGen{
		module:            newIRModule(),
		compileEverything: opts.CompileEverything,
		postponed:         map[string]*pureProc{},
	}
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
	// postponed holds what the cost rule declined, in case a compiled caller
	// needs it after all.  See the promotion pass below.
	postponed map[string]*pureProc
	// declined records the procedures the cost rule left to the interpreter.
	// They are kept apart from refused because the two are different things: a
	// refusal is something the generator cannot emit, and a decline is something
	// it chose not to.  Only the first is worth telling a user about.
	declined []string
	// refused records the procedures that were not compiled natively, and why.
	// It is what a failing test prints.
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
	// boxed is one entry per literal whose value does not fit in a machine
	// word.  The value is built once, in main, into the global named here, and
	// a body that mentions the literal loads that global.
	//
	// A literal is a constant: the same `"hello"` denotes the same object for
	// the life of the program.  Building it where it is written meant a loop
	// crossing into the runtime, re-reading the source text, parsing it and
	// allocating a fresh object on every iteration, to compute a value that
	// could not have changed.
	boxed []boxedLiteralEntry
	// callCaches maps a runtime call's name to the global that remembers what
	// that name resolved to.
	//
	// One global per name, not per call site.  The comment at the call site says
	// "per site and not per name" and that is the right *semantic* -- a program
	// may rebind a global, and each site has to see the binding in effect when
	// it first runs.  What made the original cache useless was not the sharing
	// but the *storage*: a stack slot is reset every time its function is
	// entered, and a loop written as tail recursion enters its function once per
	// iteration.  A global is entered once and keeps what it found.
	//
	// Sharing one global per name does change the rebinding behaviour: two sites
	// naming the same procedure now agree, where before each resolved
	// independently.  For the first site to run, that is the same answer; for a
	// program that rebinds a procedure between two call sites it is a different
	// one, and it is the behaviour the cache already had for a *single* site
	// called twice -- the second call did not re-resolve either.  A site whose
	// binding is changed underneath it was already frozen by the first call.
	callCaches map[string]string
}

// promotionGain is what promoting a declined callee is worth: the one crossing
// per call that the compiled caller no longer makes.
//
// A crossing costs a runtime call's arguments and its result — the same unit
// `runtimeCost` is counted in — so the two numbers are comparable, and a callee
// whose own body costs more than that is one compiling would slow down.
const promotionGain = 8

// calleeCost is what compiling a procedure's body costs, in the unit
// runtimeCost uses: the arguments and results of the calls it makes.
func calleeCost(p *pureProc) int {
	return p.cost
}

// removeReason drops a procedure's entry from a report list, which is what
// promotion has to do: the reason was true when it was recorded and is not any
// more.
//
// The list holds "name: why" strings, so the match is on the name and the
// separator rather than on a substring — a procedure called `f` must not take
// the entry for `fold` with it.
func removeReason(list []string, name string) []string {
	prefix := name + ":"
	out := list[:0]
	for _, e := range list {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// callsDropped reports whether a procedure calls one that has no native body.
func callsDropped(p *pureProc, dropped map[string]bool) bool {
	for _, c := range p.calls {
		if dropped[c] {
			return true
		}
	}
	return false
}

// modState is everything about a module that emitting a body changes.
//
// It exists so that a failed attempt can be undone.  Emitting is not a pure
// function of its input -- it allocates registers, interns string constants and
// declares external functions -- so "try again without this procedure" needs the
// module put back the way it was, and these are the pieces that move.
type modState struct {
	body    string
	decls   map[string]bool
	strings map[string]string
	nextReg int
	nextStr int
	nextLbl int
	boxed   int
}

// snapshot records the module's emitting state.
func (m *irModule) snapshot() modState {
	decls := make(map[string]bool, len(m.decls))
	for k, v := range m.decls {
		decls[k] = v
	}
	strs := make(map[string]string, len(m.strings))
	for k, v := range m.strings {
		strs[k] = v
	}
	return modState{
		body:    m.body.String(),
		decls:   decls,
		strings: strs,
		nextReg: m.nextReg,
		nextStr: m.nextString,
		nextLbl: m.nextLabel,
		boxed:   len(m.boxed),
	}
}

// restore puts the module back to a snapshot.
func (m *irModule) restore(st modState) {
	m.body.Reset()
	m.body.WriteString(st.body)
	m.decls = st.decls
	m.strings = st.strings
	m.nextReg = st.nextReg
	m.nextString = st.nextStr
	m.nextLabel = st.nextLbl
	m.boxed = m.boxed[:st.boxed]
}

// callCache names the global that remembers what a called name resolved to, and
// declares it if this is the first call site to ask.
//
// The global is zero-initialised, and zero is the "nothing cached yet" marker
// the runtime already uses, so nothing has to fill it in `main` the way a boxed
// literal does.
func (m *irModule) callCache(op string) string {
	if g, ok := m.callCaches[op]; ok {
		return g
	}
	g := fmt.Sprintf("@.cache%d", len(m.callCaches))
	m.callCaches[op] = g
	return g
}

// boxedLiteralEntry is one constant that the runtime has to build.
type boxedLiteralEntry struct {
	// text is the written form of the datum, which the runtime reads back.
	text string
	// global is the LLVM global that holds the tagged value.
	global string
}

func newIRModule() *irModule {
	return &irModule{
		decls:      map[string]bool{},
		types:      map[string]bool{},
		strings:    map[string]string{},
		callCaches: map[string]string{},
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
	// One global per constant the runtime has to build.  Each is
	// zero-initialised here and filled by main before any form runs, so a body
	// that reads one always sees its value -- and a body cannot run before
	// then, because running one is what the forms in main do.
	for _, e := range m.boxed {
		fmt.Fprintf(&out, "%s = internal global %s zeroinitializer\n", e.global, gsVal)
	}
	// The call caches, in a fixed order rather than map order: Go randomises map
	// iteration and a module whose text differs between two runs cannot be
	// checked by comparing that text, which is how the refactors around this
	// file are verified.
	cacheNames := make([]string, 0, len(m.callCaches))
	for op := range m.callCaches {
		cacheNames = append(cacheNames, op)
	}
	sort.Strings(cacheNames)
	for _, op := range cacheNames {
		fmt.Fprintf(&out, "%s = internal global i64 zeroinitializer\n", m.callCaches[op])
	}
	if len(m.boxed) > 0 || len(cacheNames) > 0 {
		out.WriteString("\n")
	}
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
			//
			// **It is not a final answer.**  The rule asks whether compiling
			// this body pays *for itself*, and the body may be called by a
			// procedure that is being compiled, where the question is different:
			// a compiled caller reaching an interpreted callee crosses the
			// boundary on every call, and that cost lands on the caller rather
			// than on the callee.  `helper` and `caller` below are the case —
			// declining `helper` alone is right, and it made `caller`
			// uncompilable too, which measured 2.58 s against 1.19 s when both
			// were compiled:
			//
			//	(define (helper l) (if (null? l) 0 (string-length "x")))
			//	(define (caller n acc)
			//	  (if (= n 0) acc (caller (- n 1) (+ acc (* n n) (helper '(1))))))
			//
			// So a declined procedure is remembered, and promoted later if a
			// compiled procedure turns out to call it.  What is retained is only
			// the shape the emitter needs; the reason is kept for the report.
			g.declined = append(g.declined, p.name+": "+why)
			g.postponed[p.name] = &pureProc{
				name: p.name, formals: p.formals, body: p.body, calls: r.calls,
				cost: r.runtimeCost,
			}
			continue
		}
		g.pure[p.name] = &pureProc{
			name: p.name, formals: p.formals, body: p.body, calls: r.calls,
			cost: r.runtimeCost,
		}
	}
	// Promote what a compiled procedure needs.
	//
	// The cost rule answers "is compiling this body worth it for itself", and
	// that answer can be wrong once the body has a *compiled caller*.  A
	// compiled caller reaching an interpreted callee crosses the boundary on
	// every call, and the cost lands on the caller — the callee's own body may
	// be cheap enough that the rule declined it, and the pair still loses.
	//
	//	(define (helper l) (if (null? l) 0 (string-length "x")))
	//	(define (caller n acc)
	//	  (if (= n 0) acc (caller (- n 1) (+ acc (* n n) (helper '(1))))))
	//
	// `helper` is one `null?` and one `string-length`, so declining it is right
	// in isolation.  But `caller` cannot be compiled without it — the pre-pass
	// below refuses a caller whose callee has no native body — and the pair
	// measured 2.58 s with `helper` declined against 1.19 s with it compiled.
	// The loss is on the caller's side of the boundary, which is exactly what
	// the rule cannot see from the callee's body.
	//
	// So a declined procedure that a compiled one calls is promoted, and its
	// refusal is withdrawn from the report: it is no longer declined, and
	// saying so would be a lie.  Promotion can pull in a callee of the promoted
	// procedure in turn, so this runs to a fixed point.
	for changed := true; changed; {
		changed = false
		wanted := map[string]bool{}
		for _, p := range g.pure {
			for _, c := range p.calls {
				if _, alive := g.pure[c]; alive {
					continue
				}
				// Only a callee that compiling cannot *hurt* is promoted.
				//
				// Promotion removes one crossing per call — the caller no
				// longer has to enter the interpreter to reach the callee — and
				// it costs whatever compiling the callee's own body costs.  For
				// a body that is mostly calls, that cost is the whole body: the
				// crossings remain and the arguments around them get boxed, so
				// the callee comes out slower than it went in and the one saved
				// crossing does not pay for it.
				//
				// `helper` below is one `null?` and one `string-length`;
				// promoting it took its caller from 2.58 s to 1.07 s.  `ev`, a
				// tree walk that crosses on `car`, `cdr`, `assq`, `cadr` and
				// `caddr`, measured 0.42x of the interpreter once promoted —
				// worse than leaving both to the interpreter.  The two differ in
				// what compiling the *callee* costs, which is what the report
				// already records, so the test is that number against the one
				// call the promotion removes.
				held, ok := g.postponed[c]
				if !ok {
					continue
				}
				if calleeCost(held) > promotionGain {
					continue
				}
				wanted[c] = true
			}
		}
		for name := range wanted {
			g.pure[name] = g.postponed[name]
			delete(g.postponed, name)
			g.declined = removeReason(g.declined, name)
			changed = true
		}
	}
	// Dependency order, with a cycle refused.
	//
	// A cycle of native procedures is *compiled*, not refused.
	//
	// This used to drop every procedure on a cycle, on the reasoning that a
	// topological order cannot contain one.  The order is real but it is not a
	// requirement of the generated code: LLVM resolves a forward reference to a
	// function in the same module, so two procedures that call each other emit
	// as two functions that call each other, with nothing to order.  The
	// topological order below is what the *emitter* walks; a cycle is a case it
	// was never asked to handle rather than a case the machine code cannot
	// express.
	//
	// What made this worth fixing is not that mutual recursion is common but
	// that it was being refused for a reason that was not true.  `even2?` and
	// `odd2?` below are two tail calls to each other, which is the shape tail
	// calls exist for: compiled, 5,000,000 of them run in constant stack in
	// 0.004 s against the interpreter's 0.864 s, a factor of 206.
	//
	//	(define (even2? n) (if (= n 0) #t (odd2? (- n 1))))
	//	(define (odd2? n) (if (= n 0) #f (even2? (- n 1))))
	//
	// A cycle whose members *cannot* be emitted still resolves correctly: the
	// retry pass below drops whatever failed to emit and everything that called
	// it, cycle or not, and that is the mechanism that was doing the work here
	// all along.
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
	// Emission can fail, and a failure has to cascade *before* anything is
	// written.
	//
	// A body that does not emit leaves no function behind, so anything that
	// called it would call a symbol that does not exist -- and that is not a
	// slower program but a module `opt` rejects outright, costing the whole
	// program every native body it had.
	//
	// The cascade cannot be done after the fact.  A procedure's *nested
	// lambdas* are emitted as part of its body, so `msort`'s
	// `(lambda () (split l))` had already written a direct call to
	// `@gs_lam_split` into the module by the time `split` was found to have
	// failed -- and dropping `msort` afterwards left the call behind.
	//
	// So the whole module is emitted into a scratch buffer, and emitted again
	// from scratch once the failures are known.  Each round drops whatever
	// failed and whatever called it; the callee set shrinks every round, so
	// this terminates, and it terminates at the point where everything emitted
	// calls only things that were emitted.  A program that compiles at all
	// settles in two rounds -- one to find the failures, one to confirm there
	// are none -- and the retry only happens for a program that had one.
	alive := map[string]bool{}
	for _, name := range order {
		alive[name] = true
	}
	var emitted []string
	for round := 0; ; round++ {
		saved := m.snapshot()
		emitted = emitted[:0]
		failed := map[string]bool{}
		for _, name := range order {
			if !alive[name] {
				continue
			}
			p := g.pure[name]
			// Only the callees that are still alive may be called natively;
			// everything else goes through the runtime, where it still works.
			live := make([]string, 0, len(p.calls))
			for _, c := range p.calls {
				if alive[c] {
					live = append(live, c)
				}
			}
			if err := g.emitPureFunction(p.name, p.formals, p.body, live); err != nil {
				// Not a reason to fail the compile: the procedure runs, it
				// just runs interpreted.
				g.refused = append(g.refused, name+": "+err.Error())
				failed[name] = true
				continue
			}
			emitted = append(emitted, name)
		}
		if len(failed) == 0 {
			break
		}
		m.restore(saved)
		for name := range failed {
			alive[name] = false
		}
		// A caller of something dropped cannot be emitted either, and this is
		// what makes the retry terminate: `msort` calls `split`, so `split`
		// failing marks `msort` dead on the next round rather than leaving it
		// to fail against a live set that no longer contains `split`.
		for _, name := range order {
			if !alive[name] {
				continue
			}
			for _, c := range g.pure[name].calls {
				if !alive[c] {
					alive[name] = false
					g.refused = append(g.refused,
						name+": it calls "+c+", which was not emitted")
					break
				}
			}
		}
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
	// The constants that do not fit in a machine word are built once here, and
	// every use loads the global they are stored in.
	//
	// This is the only place it can go.  It has to be after the loop above,
	// because emitting a body is what discovers which literals need a global,
	// and it has to be before the forms below, because those forms are what
	// call the bodies.  Writing it at the end of main -- after the forms --
	// left each body reading a zero-initialised global, and `string-length`
	// answered "expected a string but got 0" once per iteration.
	//
	// A literal is a constant: the same `"hello"` denotes the same object for
	// the life of the program, so it is built once and read everywhere.  The
	// first version built it where it was written, which meant a loop crossing
	// into the runtime, re-reading the source text and parsing it, per
	// iteration, to produce a value that could not change.
	for _, e := range m.boxed {
		m.declare("i64 @gs_box_literal(i8*, i64)")
		lit := m.stringLiteral(e.text, "box")
		built := m.reg()
		fmt.Fprintf(&body, "  %s = call i64 @gs_box_literal(i8* %s, i64 %d)\n",
			built, lit, len(e.text))
		fmt.Fprintf(&body, "  store i64 %s, i64* getelementptr (%s, %s* %s, i64 0, i32 0)\n",
			built, gsVal, gsVal, e.global)
		fmt.Fprintf(&body, "  store i64 1, i64* getelementptr (%s, %s* %s, i64 0, i32 1)\n",
			gsVal, gsVal, e.global)
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
	// cost is what compiling this body costs: the arguments and results of the
	// runtime calls it makes.  It is what `notWorthCompiling` looked at, kept
	// so that the promotion pass can ask the same question again with the
	// caller's side of the boundary added.
	cost int
}
