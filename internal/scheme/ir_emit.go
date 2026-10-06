// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Emitting native code for a pure procedure
// ---------------------------------------------------------------------------

// irFunc is one native function being generated.
type irFunc struct {
	// name is the symbol the function is emitted under, which the runtime is
	// also told about so that a call through a variable finds the native code.
	name string
	// mod is the module being built, which a helper needs when it has to
	// introduce a string constant or a declaration partway through a body.
	mod *irModule
	// params are the formals, in order, as tagged SSA values.
	params []irVal
	// locals maps a name to the tagged value holding it.
	locals map[string]irVal
	// entry collects the allocas, which LLVM wants in the entry block, and
	// body the rest of the instructions.
	entry strings.Builder
	body  strings.Builder
	// slots are the alloca'd locals, and nextSlot counts them.
	slots    []string
	nextSlot int
	// label counts the basic blocks within this function, and nextReg the
	// SSA values.
	label   int
	nextReg int
	// currentBlock is the basic block the emitter is writing into, which a phi
	// node needs to name its predecessor.
	currentBlock string
	// calls are the natively-compiled procedures this one may call, and self is
	// its own name, which a recursive call names.
	calls []string
	self  string
	// arity is how many arguments this function takes, which `musttail` has to
	// match.
	arity int
	// listWalkResult is set when the body was recognised as a list walk and
	// emitted as one call into the runtime, in which case the ordinary
	// expression emitter is not run at all.
	listWalkDone bool
	listWalkVal  irVal
	// lambdaNames gives each `lambda` emitted inside this function a distinct
	// symbol to be emitted under, and lambdaDepth bounds how deep they may nest.
	// See ir_closure.go.
	lambdaNames lambdaNamer
	lambdaDepth int
	// captureNames is the rename in force while a lambda body is being emitted:
	// a captured variable arrives as a parameter whose name is the original, so
	// this is normally empty and exists for the case where it cannot be.
	captureNames map[string]string
	// captures maps a captured variable's name to its index in the enclosing
	// closure, and is non-empty only while a lambda body is being emitted.  An
	// assignment to one of these names is written through the closure rather
	// than rebinding the parameter it arrived as: the parameter is a copy, so
	// assigning it would be local to the call and the counter would never count.
	captures map[string]int
	// selfHandle is the closure's own handle as an SSA value, or "" when the
	// body does not assign any of its captures and so does not need it.
	selfHandle string
	// tail is true while the expression being emitted is in tail position: its
	// value is the value of the whole function, so a call there can be a jump
	// rather than a call.
	//
	// This is not an optimization here, it is the language.  R7RS requires
	// proper tail calls, and the interpreter and the bytecode VM both provide
	// them, so a compiled loop that grew the stack per iteration would be a
	// program that segfaults instead of one that runs — which is what happened
	// before this existed.
	tail bool
	// tailReturned is set when a tail call has already left the function by
	// jumping, so that the emitter does not write a return after it.
	tailReturned bool
}

// want declares a runtime function the body being emitted needs.  Declaring it
// once is enough, and a body that never needs one never mentions it.
func (f *irFunc) want(sig string) { f.mod.declare(sig) }

// emitPureFunction writes a native LLVM function for a pure procedure.
//
// Every value in it is an i64: a Scheme fixnum.  That is the whole point of
// accepting only a pure body — a machine integer is a Scheme exact integer
// exactly as long as nothing can turn it into something else, and the only
// things the accepted body can do are arithmetic on parameters, constants, and
// calls to procedures that are pure in the same way.  Overflow is the one way
// out, and the generated code checks for it and takes the runtime path (see
// emitCheckedArith), because a Scheme integer that leaves the fixnum range
// becomes a bignum rather than wrapping.
func (g *irGen) emitPureFunction(name string, formals []*Symbol, body []Value, calls []string) error {
	f := &irFunc{
		name:         name,
		mod:          g.module,
		self:         name,
		arity:        len(formals),
		locals:       map[string]irVal{},
		calls:        calls,
		currentBlock: "entry",
		lambdaNames:  lambdaNamer{owner: name},
	}
	// Each argument arrives as two plain integers — its word and its tag —
	// rather than as one tagged struct.
	//
	// The tagged struct is how a value is *carried*, and it stays that way
	// everywhere else.  It is the wrong thing to pass, though, because a struct
	// hides the tag from the optimizer: the callee loads a tag it cannot know
	// anything about, so every comparison and every branch on truth keeps its
	// runtime path.  A loop that adds two numbers then called gs_num_eq and
	// gs_truthy on every iteration to ask a question whose answer was already
	// known, and it ran slower than the bytecode VM.
	//
	// Two integers fix that.  A tag that is passed as its own argument can be
	// constant-propagated across the call, so `(+ acc i)` in a recursive loop
	// compiles to an add and the check disappears.  The cost is that the
	// function-pointer type is now per arity, which gs_register's wrapper
	// absorbs — see the adapter emitted below.
	var sig strings.Builder
	fmt.Fprintf(&sig, "define %s @%s(", gsVal, mangle(name))
	for i, p := range formals {
		if i > 0 {
			sig.WriteString(", ")
		}
		fmt.Fprintf(&sig, "i64 %%p_%s.bits, i64 %%p_%s.tag", p.Name, p.Name)
	}
	sig.WriteString(") {\n")
	sig.WriteString("entry:\n")
	m := g.module
	m.gsValType()
	for _, p := range formals {
		v := irVal{
			bits: fmt.Sprintf("%%p_%s.bits", p.Name),
			tag:  fmt.Sprintf("%%p_%s.tag", p.Name),
		}
		f.params = append(f.params, v)
		f.locals[p.Name] = v
	}
	// The intrinsics a checked operation uses.  They are declared here rather
	// than on demand because a missing declaration is reported at the *call*,
	// which is a misleading place to learn about it.
	m.declare("{ i64, i1 } @llvm.sadd.with.overflow.i64(i64, i64)")
	m.declare("{ i64, i1 } @llvm.ssub.with.overflow.i64(i64, i64)")
	m.declare("{ i64, i1 } @llvm.smul.with.overflow.i64(i64, i64)")

	// A recursive call names this function and needs no declaration of it: a
	// define is visible to its own body, and adding a declare as well is a
	// redefinition error — which `opt -passes=verify` said the first time this
	// was tried, and is the reason the check is part of the build below.

	// A body that is a whole walk is emitted as one call that runs the walk in
	// the runtime, rather than as a loop that crosses the boundary for every
	// element.  This is checked first because it replaces the body entirely.
	//
	// The walk's own parameters are the arguments, and there are no invariants —
	// a body that needs those is a named let, which is handled below.
	//
	// There is deliberately one dispatch here and it goes through emitWalkCall,
	// which asks the recognisers in one place.  An earlier version listed the
	// shapes again at this call site, and the two lists drifted: `do` and the
	// searching walk were recognised and never emitted, which is a loop that
	// looks like it should be compiled and silently is not.
	if _, isWalk := walkParams(name, formals, body); isWalk {
		args := make([]irVal, 0, len(formals))
		for _, s := range formals {
			args = append(args, irVal{
				bits: "%p_" + s.Name + ".bits",
				tag:  "%p_" + s.Name + ".tag",
			})
		}
		f.emitWalkCall(name, formals, body, args, nil, nil)
	} else if nl, isLet := parseNamedLet(body); isLet {
		f.emitNamedLetLoop(nl)
	}

	// The body is in tail position: whatever it evaluates to is what the
	// function returns, so a call at the end of it can be a jump.
	var val irVal
	var err error
	if f.listWalkDone {
		val = f.listWalkVal
	} else {
		f.tail = true
		val, err = f.emitExpr(Cons(Intern("begin"), listFromSlice(body)))
		if err != nil {
			return err
		}
	}
	// A tail call has already returned, by jumping — an ordinary `ret` after it
	// would be unreachable, and LLVM rejects a `musttail` that is not followed
	// immediately by a return.  So the return is written only when the body
	// finished some other way.
	tailReturned := f.tailReturned
	f.tailReturned = false
	var boxed string
	if !tailReturned {
		boxed = val.bits0(f)
	}
	var out strings.Builder
	out.WriteString(sig.String())
	out.WriteString(f.entry.String())
	out.WriteString(f.body.String())
	if tailReturned {
		out.WriteString("  unreachable\n}\n\n")
	} else {
		fmt.Fprintf(&out, "  ret %s %s\n}\n\n", gsVal, boxed)
	}
	g.module.body.WriteString(out.String())

	// The adapter: the uniform entry point the runtime calls, which unpacks the
	// argument array into the pairs the body takes.
	//
	// It exists because two shapes are needed and they are not the same.  The
	// body's shape is the one the optimizer wants — tags as separate integers,
	// so they can be constant-propagated across a call.  The runtime's shape is
	// the one a single function-pointer type can describe, because the runtime
	// cannot know an arity at compile time.  One forwarding function per
	// procedure is the whole cost of having both, and it is not on a hot path
	// that stays compiled: a native-to-native call goes straight to the body.
	var ad strings.Builder
	fmt.Fprintf(&ad, "define %s @%s(i64 %%n, %s* %%args) {\n",
		gsVal, adapterName(name), gsVal)
	ad.WriteString("entry:\n")
	var argList []string
	for i := range formals {
		elem := fmt.Sprintf("%%e%d", i)
		word := fmt.Sprintf("%%w%d", i)
		tag := fmt.Sprintf("%%t%d", i)
		// The pointer, then the value it points at: a getelementptr alone does
		// not read anything.
		fmt.Fprintf(&ad, "  %s = getelementptr %s, %s* %%args, i64 %d\n", elem, gsVal, gsVal, i)
		fmt.Fprintf(&ad, "  %s = load %s, %s* %s\n", word, gsVal, gsVal, elem)
		fmt.Fprintf(&ad, "  %s = extractvalue %s %s, 0\n", fmt.Sprintf("%%b%d", i), gsVal, word)
		fmt.Fprintf(&ad, "  %s = extractvalue %s %s, 1\n", tag, gsVal, word)
		argList = append(argList, fmt.Sprintf("i64 %%b%d, i64 %s", i, tag))
	}
	fmt.Fprintf(&ad, "  %%r = call %s @%s(%s)\n", gsVal, mangle(name), strings.Join(argList, ", "))
	fmt.Fprintf(&ad, "  ret %s %%r\n}\n\n", gsVal)
	g.module.body.WriteString(ad.String())

	g.native++
	return nil
}

// adapterName is the symbol of the uniform entry point registered for a
// procedure, which forwards to its body.
func adapterName(name string) string { return mangle(name) + "_entry" }

// irVal is a Scheme value as the generated code holds it: two SSA registers,
// one for the word and one for its tag.
//
// Carrying the tag alongside the word, rather than assuming the word is the
// value, is what makes the native path total.  An arithmetic result that does
// not fit a machine word becomes a handle at the moment it is produced, and
// every later operation on it goes back to the runtime — so the compiler never
// has to answer "what if this does not fit", because there is an answer.
type irVal struct {
	bits string // the number, or the handle
	tag  string // tagFixnum or tagHandle, as an i64
}

// fixnumVal is an irVal holding a constant machine integer.
func fixnumVal(n int64) irVal {
	return irVal{bits: fmt.Sprintf("%d", n), tag: tagFixnum}
}

// boolVal is an irVal holding #t or #f, which is a value of its own kind rather
// than the numbers 1 and 0.
func boolVal(b bool) irVal {
	if b {
		return irVal{bits: "1", tag: tagBoolean}
	}
	return irVal{bits: "0", tag: tagBoolean}
}

// unspecifiedVal is an irVal holding the unspecified value, which is what a body
// with nothing to return yields: an `if` with no alternative, or a `cond` whose
// clauses all failed.
//
// It carries no payload, so the bits are zero, and the tag is what makes it
// distinguishable from the fixnum 0 and from `()`.  Those are both values a
// program can print and compare; this one is the absence of an answer.
func unspecifiedVal() irVal {
	return irVal{bits: "0", tag: tagUnspecified}
}

// truthVal converts an i1 into a Scheme boolean.
func (f *irFunc) truthVal(cond string) irVal {
	return irVal{bits: f.zext(cond), tag: tagBoolean}
}

// isConstFixnum reports whether the value is a literal the generator can fold,
// which is what makes a branch on a constant condition free.
func (v irVal) isConstFixnum() bool {
	return v.tag == tagFixnum && v.bits != "" && v.bits[0] != '%'
}

// emitExpr generates code for one expression and returns the SSA value it
// computes.
func (f *irFunc) emitExpr(e Value) (irVal, error) {
	switch x := e.(type) {
	case *Integer:
		// A literal too large for a machine word is not a compile-time error:
		// it is a value that lives as a handle, which is exactly what the tag
		// is for.  The runtime boxes it once, at entry to the form that uses
		// it, so the literal still appears in the emitted code as a constant.
		xv, xSmall := x.Small()
		if !xSmall {
			return f.boxedLiteral(x.String())
		}
		return irVal{bits: fmt.Sprintf("%d", xv), tag: tagFixnum}, nil
	case Boolean:
		return boolVal(bool(x)), nil
	case *Symbol:
		if v, ok := f.locals[x.Name]; ok {
			return v, nil
		}
		// Not a parameter or a let binding, so it is a global: read it now,
		// where it is used, because anything between here and the last read may
		// have assigned it.
		return f.emitGlobalRead(x.Name)
	case Empty:
		return irVal{bits: "0", tag: tagNull}, nil
	case *Pair:
		return f.emitForm(x)
	default:
		// Any other self-evaluating literal — a string, a character, a float, a
		// rational, a vector — is boxed by its source text and handed over as a
		// handle.
		//
		// The generated code cannot build one, because that would mean this
		// knowing how each is represented, but it does not have to: the literal
		// is already written down, and the runtime can read what the compiler
		// read.  Without this, `(string-length "abc")` would refuse a body over
		// an argument rather than over what the body does.
		if isSelfEvaluating(e) {
			return f.boxedLiteral(WriteToString(e))
		}
		return irVal{}, fmt.Errorf("ir: cannot emit %s", TypeName(e))
	}
}

// isSelfEvaluating reports whether a value stands for itself, so that writing it
// out and reading it back gives the same value.
//
// A symbol does not — it is a name, and the one it refers to is the runtime's
// business — and neither does a pair, which is a form to be evaluated.  Every
// other literal does, which is what makes boxing one safe.
func isSelfEvaluating(v Value) bool {
	switch v.(type) {
	// A character is a value type — `type Char rune` — so it is listed as
	// itself and not as *Char, which matches nothing.
	case *Integer, Float, *Rational, *Complex, *String, Char, Boolean,
		*Vector, *Bytevector:
		return true
	}
	return false
}

// emitForm handles a call form.
func (f *irFunc) emitForm(x *Pair) (irVal, error) {
	head, _ := x.Car.(*Symbol)
	if head == nil {
		// The operator is an expression rather than a name: `((make 1) 2)`, or
		// `(f x)` where f is a parameter.  It is evaluated and then called, which
		// is the one case where the callee is not known at compile time.
		return f.emitComputedCall(x)
	}
	args, _ := ListToSlice(x.Cdr)
	switch head.Name {
	case "do":
		// A do loop whose shape is a recognised walk is emitted as that walk.
		// The expansion the interpreter uses wraps the steps in a `guard` for
		// `(continue)`, and a guard is not a shape anything can be recognised
		// from, so the loop is read from the source.
		if loopName, vars, body, ok := doAsLoop(x); ok {
			if _, _, shape, ok := recogniseAnyWalk(loopName, vars, body); ok && shape != shapeNone {
				// The inits are what the variables start at.
				specs, _ := ListToSlice(args[0])
				vals := make([]irVal, 0, len(vars))
				good := true
				for _, spec := range specs {
					items, _ := ListToSlice(spec)
					if len(items) != 3 {
						good = false
						break
					}
					v, err := f.emitExpr(items[1])
					if err != nil {
						good = false
						break
					}
					vals = append(vals, v)
				}
				if good && len(vals) == len(vars) {
					f.emitWalkCall(loopName, vars, body, vals, nil, nil)
					if f.listWalkDone {
						return f.listWalkVal, nil
					}
				}
			}
		}
		// A do that got this far was not recognised as a loop — the scan only
		// lets through the ones it recognised — so reaching here means the two
		// disagree, and saying so is better than emitting something wrong.
		return irVal{}, fmt.Errorf("ir: a do loop that the scan accepted but this cannot emit")
	case "begin":
		// Only the last part is in tail position: the earlier ones are evaluated
		// for their effect and their values are discarded.
		var last irVal
		var err error
		for i, a := range args {
			saved := f.tail
			if i != len(args)-1 {
				f.tail = false
			}
			last, err = f.emitExpr(a)
			f.tail = saved
			if err != nil {
				return irVal{}, err
			}
		}
		return last, nil
	case "if":
		return f.emitIf(args)
	case "let", "let*":
		return f.emitLet(args, head.Name == "let*")
	case "and", "or":
		return f.emitAndOr(args, head.Name == "and")
	case "quote":
		if len(args) != 1 {
			return irVal{}, fmt.Errorf("ir: quote takes one part")
		}
		return f.emitQuoted(args[0])
	case "set!":
		return f.emitSet(args)
	case "lambda":
		return f.emitLambda(x)
	case "guard":
		return f.emitGuard(x)
	}
	// Anything else is a call — unless it is a form rather than a procedure.
	// `set!`, `lambda`, `define` and the rest are syntax, not values, so
	// emitting one as a call would ask the runtime for a procedure that does
	// not exist.  The scan already refuses these; this is the same check at the
	// place that would otherwise get it wrong.
	if isSyntax(head.Name) {
		return irVal{}, fmt.Errorf("ir: %s is a form, not a call this can emit", head.Name)
	}
	return f.emitCall(head.Name, args)
}

// isSyntax reports whether a name is a special form or a macro rather than a
// procedure.
//
// The list is the one the interpreter's own reader works from, so a form added
// to the language is refused here too rather than quietly becoming a call to a
// procedure of the same name.  A macro is included because a `let-syntax` body
// has been expanded by the time a top-level procedure is compiled, and one that
// has not is not something this can emit.
func isSyntax(name string) bool {
	switch name {
	case "quote", "quasiquote", "unquote", "unquote-splicing",
		"if", "set!", "define", "lambda", "begin", "let", "let*", "letrec",
		"letrec*", "let-values", "let*-values", "define-values", "do", "cond",
		"case", "when", "unless", "and", "or", "delay", "delay-force",
		"parameterize", "guard", "assert", "define-syntax", "let-syntax",
		"letrec-syntax", "syntax-rules", "define-record-type", "case-lambda",
		"cons-stream", "the-environment", "define-library", "import",
		"include", "include-ci", "cond-expand", "else", "=>",
		// `match` is syntax this generator has no rule for, and it has to be
		// listed or its clauses are taken for a call's arguments: walking them
		// found `(else ...)` and reported "else is a form, not a call this can
		// compile", which names the wrong thing and hides the real one.
		"match":
		return true
	}
	return false
}

// mangle turns a Scheme procedure name into the LLVM symbol its native body is
// emitted under.
func mangle(name string) string {
	return "gs_lam_" + mangleName(name)
}

// emitIf emits a conditional.  Both arms produce a tagged value, and the result
// is selected by the branch — which is what a phi node is for, and why it takes
// two of them: the tag is as much a part of the value as the word.
func (f *irFunc) emitIf(args []Value) (irVal, error) {
	if len(args) < 2 {
		return irVal{}, fmt.Errorf("ir: if takes two or three parts")
	}
	test, err := f.emitExpr(args[0])
	if err != nil {
		return irVal{}, err
	}
	cond, err := f.truthOf(test)
	if err != nil {
		return irVal{}, err
	}
	thenLabel := f.freshLabel("then")
	elseLabel := f.freshLabel("else")
	endLabel := f.freshLabel("endif")
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", cond, thenLabel, elseLabel)

	// An arm that ends in a tail call has left the function, so it has no value
	// to bring to the join — and must not be named as a predecessor of the phi
	// there, because it does not branch to it.  Each arm is therefore recorded
	// only if it arrived.
	type arm struct {
		val  irVal
		from string
	}
	var arms []arm

	f.block(thenLabel)
	armVal, armFrom, returned, err := f.emitArm(args, 1, f.tail)
	if err != nil {
		return irVal{}, err
	}
	if !returned {
		fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)
		arms = append(arms, arm{armVal, armFrom})
	}

	f.block(elseLabel)
	if len(args) > 2 {
		var elseVal irVal
		var elseFrom string
		var elseReturned bool
		elseVal, elseFrom, elseReturned, err = f.emitArm(args, 2, f.tail)
		if err != nil {
			return irVal{}, err
		}
		if !elseReturned {
			fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)
			arms = append(arms, arm{elseVal, elseFrom})
		}
	} else {
		// With no else arm the result is unspecified, which is a value of its
		// own rather than a stand-in.  This used to yield the fixnum 0, on the
		// reasoning that "no accepted body can observe it" — and a body can:
		// `(define (f n) (if (< n 0) 1))` compiled printed `0` where the
		// interpreter printed `#!unspecified`, and once `cond` was rewritten
		// into `if`, every `cond` whose clauses all failed printed the same
		// wrong thing.  The report says a program must not *rely* on the value
		// of an unspecified result; it does not say the value may be a number
		// that the program then prints.
		fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)
		arms = append(arms, arm{unspecifiedVal(), f.currentBlock})
	}

	f.block(endLabel)
	if len(arms) == 0 {
		// Both arms returned, so nothing reaches the join: it is unreachable and
		// the function is finished.  An `unreachable` there is what tells LLVM
		// so, and the value it returns is never used.
		f.body.WriteString("  unreachable\n")
		f.tailReturned = true
		return irVal{bits: "0", tag: tagFixnum}, nil
	}
	if len(arms) == 1 {
		// One arm returned and the other did not, so there is nothing to join:
		// the surviving arm's value is the result.  A phi with one entry would
		// be legal but pointless, and the register is what the caller wants.
		return arms[0].val, nil
	}
	bits := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i64 [ %s, %%%s ], [ %s, %%%s ]\n",
		bits, arms[0].val.bits, arms[0].from, arms[1].val.bits, arms[1].from)
	tag := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i64 [ %s, %%%s ], [ %s, %%%s ]\n",
		tag, arms[0].val.tag, arms[0].from, arms[1].val.tag, arms[1].from)
	return irVal{bits: bits, tag: tag}, nil
}

// emitArm emits one arm of an if, given the index of the part to emit, and
// reports the value, the block it ended in, and whether a tail call has already
// left the function from there.
func (f *irFunc) emitArm(args []Value, i int, tail bool) (irVal, string, bool, error) {
	saved := f.tail
	f.tail = tail
	f.tailReturned = false
	v, err := f.emitExpr(args[i])
	returned := f.tailReturned
	f.tail = saved
	f.tailReturned = false
	if err != nil {
		return irVal{}, "", false, err
	}
	return v, f.currentBlock, returned, nil
}

// emitAndOr emits and/or, which return the value that decided them rather than
// a boolean.
func (f *irFunc) emitAndOr(args []Value, isAnd bool) (irVal, error) {
	if len(args) == 0 {
		// (and) is #t and (or) is #f: booleans, not the numbers one and zero.
		return boolVal(isAnd), nil
	}
	if len(args) == 1 {
		return f.emitExpr(args[0])
	}
	// The chain short-circuits, so each step is a branch that either continues
	// or yields that step's own value.
	//
	// Every step's value has to be joinable, and they are not all computed in
	// the same block: a step that is itself a checked addition finishes in the
	// join block of its own overflow test.  So each one stores its two words
	// into slots and the phi at the end reads the slots, which is what makes the
	// predecessors well defined without the emitter having to reason about which
	// block each value landed in.
	bitsSlot := f.alloca()
	tagSlot := f.alloca()
	endLabel := f.freshLabel("andor.end")
	for i, a := range args {
		andSaved := f.tail
		if i != len(args)-1 {
			f.tail = false
		}
		v, err := f.emitExpr(a)
		f.tail = andSaved
		if err != nil {
			return irVal{}, err
		}
		fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", v.bits, bitsSlot)
		fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", v.tag, tagSlot)
		if i == len(args)-1 {
			break
		}
		cond, err := f.truthOf(v)
		if err != nil {
			return irVal{}, err
		}
		if !isAnd {
			// `or` continues while the value is false, so the test flips.
			not := f.reg()
			fmt.Fprintf(&f.body, "  %s = xor i1 %s, true\n", not, cond)
			cond = not
		}
		next := f.freshLabel("andor.next")
		keep := f.freshLabel("andor.keep")
		fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", cond, next, keep)
		f.block(keep)
		fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)
		f.block(next)
	}
	fmt.Fprintf(&f.body, "  br label %%%s\n", endLabel)
	f.block(endLabel)
	bits := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", bits, bitsSlot)
	tag := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", tag, tagSlot)
	return irVal{bits: bits, tag: tag}, nil
}

// emitLet emits a let: each binding is computed and kept, because a body may
// read it more than once.
func (f *irFunc) emitLet(args []Value, sequential bool) (irVal, error) {
	if len(args) < 1 {
		return irVal{}, fmt.Errorf("ir: a let with no bindings")
	}
	bindings, _ := ListToSlice(args[0])
	saved := map[string]irVal{}
	for k, v := range f.locals {
		saved[k] = v
	}
	// A tagged value is two words, so a binding that has to survive a call is
	// spilled rather than kept in a register — a call is where an SSA value
	// would otherwise have to be live across a block boundary.
	for _, b := range bindings {
		p := b.(*Pair)
		items, _ := ListToSlice(p)
		name := items[0].(*Symbol)
		val, err := f.emitExpr(items[1])
		if err != nil {
			return irVal{}, err
		}
		if sequential {
			// Each binding is visible to the next, so bind as we go.
			f.locals[name.Name] = val
			continue
		}
		// A parallel let: the initialisers all see the outer scope, so the
		// value is held aside and bound after all of them are computed.
		f.locals[name.Name] = val
	}
	// As with begin, only the last part of a let body is in tail position.
	body := args[1:]
	var last irVal
	var err error
	for i, b := range body {
		bodySaved := f.tail
		if i != len(body)-1 {
			f.tail = false
		}
		last, err = f.emitExpr(b)
		f.tail = bodySaved
		if err != nil {
			return irVal{}, err
		}
	}
	f.locals = saved
	return last, nil
}

// emitSet emits `(set! NAME VALUE)`.
//
// A local that is assigned is still an SSA value: rebinding the name to the new
// value is what an assignment *is* here, and every later read of the name in the
// same block then sees it.  That is correct for straight-line code and for a
// loop body, which is where an assignment to a local almost always appears.
//
// The check is the local map, and what it gives is the scope: a name that is not
// there is not a local of this procedure, so the assignment is to a global and
// belongs to the runtime — which owns the global environment and may have to
// create the binding.  That is the same division the reads use, and it means a
// `set!` of a global still compiles the body around it rather than stopping it.
//
// What is *not* handled is a captured variable: if NAME is a local that some
// `lambda` in this body closes over, then a `set!` has to be visible through the
// closure, and an SSA rebinding is not.  A `lambda` in an expression position is
// not emitted at all yet, so no such body reaches here — and when closures are
// emitted this has to be revisited rather than extended, because the
// representation of a mutable captured variable is the thing that changes.
func (f *irFunc) emitSet(args []Value) (irVal, error) {
	if len(args) != 2 {
		return irVal{}, fmt.Errorf("ir: set! takes a name and a value")
	}
	name, ok := args[0].(*Symbol)
	if !ok {
		return irVal{}, fmt.Errorf("ir: set! needs a name")
	}
	val, err := f.emitExpr(args[1])
	if err != nil {
		return irVal{}, err
	}
	if _, local := f.locals[name.Name]; local {
		// A captured variable is a local of this function too — it arrived as a
		// parameter — but assigning the parameter would only change the copy this
		// call was given.  The write has to reach the closure's own storage, and
		// the closure's handle is what does it.
		//
		// Getting this wrong is silent and wrong rather than slow:
		// `(let ((n 0)) (lambda () (set! n (+ n 1)) n))` returned 1 on every call
		// instead of counting, because the addition happened and the store did
		// not.  Nothing reported it and nothing could have, because the body
		// compiled.
		if idx, isCapture := f.captures[name.Name]; isCapture && f.selfHandle != "" {
			if err := f.emitCaptureSet(idx, val); err != nil {
				return irVal{}, err
			}
			// The parameter this call was given is stale once the store has
			// happened, so the local is rebound: a later read in the same call
			// must see the assignment, which is what the interpreter does.
			f.locals[name.Name] = val
			return unspecifiedVal(), nil
		}
		f.locals[name.Name] = val
		return unspecifiedVal(), nil
	}
	// A global: the runtime owns the global environment and does the assignment,
	// because the binding may not exist yet and creating it is the runtime's
	// business.  The name goes over as text, which is the same form every other
	// by-name entry point uses.
	if err := f.emitGlobalSet(name.Name, val); err != nil {
		return irVal{}, err
	}
	return unspecifiedVal(), nil
}

// emitGlobalSet assigns a top-level binding by name.
//
// The value crosses tagged like any other, and the name crosses as a string:
// the runtime looks the binding up in the global environment, which is what has
// to happen for a name this procedure does not own.
func (f *irFunc) emitGlobalSet(name string, val irVal) error {
	f.want("void @gs_set_global(i8*, i64, " + gsVal + ")")
	lit := f.mod.stringLiteral(name, "set"+name)
	agg := f.toAggregate(val)
	fmt.Fprintf(&f.body, "  call void @gs_set_global(i8* %s, i64 %d, %s %s)\n",
		lit, len(name), gsVal, agg)
	return nil
}

// toAggregate packs a (word, tag) pair into the %gs.val struct an entry point
// takes by value.  Callers that pass an argument array use storeArg instead,
// which writes the two words into the array directly and skips the intermediate
// aggregate.
func (f *irFunc) toAggregate(v irVal) string {
	first := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s undef, i64 %s, 0\n", first, gsVal, v.bits)
	second := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s %s, i64 %s, 1\n", second, gsVal, first, v.tag)
	return second
}

// emitQuoted emits a quoted literal.
//
// The three immediate representations are emitted inline, because they cost
// nothing: a fixnum is a machine word, and a boolean and the empty list are a
// word with a tag.  Everything else — a string, a character, a symbol, a pair, a
// vector, a bytevector, an inexact number — is written into the module as source
// text and boxed once by the runtime, which is what `gs_box_literal` does and
// what it has always done for an integer too large to be a machine word.
//
// This used to accept numbers only, and refused the rest with "a quoted value
// that is not a number".  That is a hole rather than a design: `'(cond (a b))`
// is a datum, and a program that carries a list of symbols as a constant is not
// doing anything the compiler cannot express — it has a reader and the compiler
// is allowed to use it.  Boxed once per evaluation of the literal rather than
// once per program, which is the same thing the interpreter does.
//
// The text is the written form of the datum, which the reader reads back.  That
// round trip is already relied on elsewhere — a top-level form is handed to the
// interpreter as source — so it is a path rather than a new one.
func (f *irFunc) emitQuoted(v Value) (irVal, error) {
	switch x := v.(type) {
	case *Integer:
		xv, xSmall := x.Small()
		if !xSmall {
			return f.boxedLiteral(x.String())
		}
		return fixnumVal(xv), nil
	case Boolean:
		return boolVal(bool(x)), nil
	case Empty:
		return irVal{bits: "0", tag: tagNull}, nil
	}
	return f.boxedLiteral(WriteToString(v))
}

// freshLabel returns a basic-block label unique within this function.
func (f *irFunc) freshLabel(prefix string) string {
	f.label++
	return fmt.Sprintf("%s_%d", prefix, f.label)
}

func (f *irFunc) reg() string {
	f.nextReg++
	return fmt.Sprintf("%%v%d", f.nextReg)
}

// emitCall emits a call: an arithmetic operation, a comparison, or a call to a
// procedure that was itself compiled natively.
//
// The arithmetic is **checked**.  A Scheme exact integer is unbounded, so
// `(* n (fact (- n 1)))` on a machine word can overflow where Scheme would have
// promoted to a bignum; the generated code tests for it and, when it happens,
// calls the runtime, which is the interpreter and knows about bignums.  That is
// what makes it safe to compile a procedure natively without proving anything
// about the size of its values.
func (f *irFunc) emitCall(op string, args []Value) (irVal, error) {
	// A call whose result is one of its arguments is not emitted at all; see
	// ir_identity.go.  This comes first because the point is to not generate the
	// arguments: emitting them and discarding the call would save the crossing
	// and nothing else, and the crossings are not the bulk of what a loop like
	// `(car (list acc))` spends its time on — the `alloca`s and `store`s that
	// box its arguments are.
	if kept, work, ok := identity(op, args); ok {
		outerTail := f.tail
		for _, e := range work {
			// The elided half's arguments still have to run for their effect,
			// before the kept value is computed.
			f.tail = false
			if _, err := f.emitExpr(e); err != nil {
				f.tail = outerTail
				return irVal{}, err
			}
		}
		f.tail = outerTail
		return f.emitExpr(kept)
	}
	// The arguments are not in tail position, even when the call is: their
	// values are needed *by* this call, so a call of their own has to return
	// rather than jump.  Leaving the flag set let `(square (square x))` emit the
	// inner call as the tail call and return from there, so the outer one never
	// ran — a wrong answer, not a slow one.
	outerTail := f.tail
	vals := make([]irVal, 0, len(args))
	for _, a := range args {
		f.tail = false
		v, err := f.emitExpr(a)
		f.tail = outerTail
		if err != nil {
			return irVal{}, err
		}
		vals = append(vals, v)
	}
	f.tail = outerTail
	// The operators are inlined, so their arity is checked here rather than by
	// the runtime that would otherwise catch it.  A call to anything else may
	// have any number of arguments, including none — `(newline)` is a call like
	// any other.
	if pureOperator(op) && len(vals) == 0 {
		return irVal{}, fmt.Errorf("ir: %s takes at least one argument", op)
	}
	switch op {
	case "+", "-", "*":
		acc := vals[0]
		if len(vals) == 1 {
			if op == "-" {
				return f.emitCheckedArith(op, fixnumVal(0), acc)
			}
			return acc, nil // (+ x) is x, (* x) is x
		}
		var err error
		for _, v := range vals[1:] {
			acc, err = f.emitCheckedArith(op, acc, v)
			if err != nil {
				return irVal{}, err
			}
		}
		return acc, nil
	case "=", "<", ">", "<=", ">=":
		// Chained comparison, as Scheme has it: (< 1 2 3) is (< 1 2) and (< 2 3).
		if len(vals) < 2 {
			return fixnumVal(1), nil
		}
		acc := "1"
		for i := 0; i+1 < len(vals); i++ {
			cmp, err := f.emitCompare(op, vals[i], vals[i+1])
			if err != nil {
				return irVal{}, err
			}
			both := f.reg()
			fmt.Fprintf(&f.body, "  %s = and i64 %s, %s\n", both, acc, cmp)
			acc = both
		}
		return irVal{bits: acc, tag: tagBoolean}, nil
	case "zero?", "positive?", "negative?":
		// These ask a question about a number, and a number that does not fit a
		// machine word is a handle — so the question goes to the runtime when
		// the tag says so.
		cmp := map[string]string{"zero?": "eq", "positive?": "sgt", "negative?": "slt"}[op]
		return f.numericTest(vals[0], cmp, "0")
	case "not":
		cond, err := f.truthOf(vals[0])
		if err != nil {
			return irVal{}, err
		}
		not := f.reg()
		fmt.Fprintf(&f.body, "  %s = xor i1 %s, true\n", not, cond)
		return f.truthVal(not), nil
	case "abs":
		// abs of the most negative word overflows, which is the one case the
		// checked path has to catch.
		neg := f.reg()
		fmt.Fprintf(&f.body, "  %s = sub i64 0, %s\n", neg, vals[0].bits)
		cmp, err := f.numericTest(vals[0], "slt", "0")
		if err != nil {
			return irVal{}, err
		}
		cond := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", cond, cmp.bits)
		out := f.reg()
		fmt.Fprintf(&f.body, "  %s = select i1 %s, i64 %s, i64 %s\n",
			out, cond, neg, vals[0].bits)
		return irVal{bits: out, tag: tagFixnum}, nil
	case "min", "max":
		acc := vals[0]
		for _, v := range vals[1:] {
			less, err := f.compareTagged("<", v, acc)
			if err != nil {
				return irVal{}, err
			}
			cond := f.reg()
			fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", cond, less)
			if op == "max" {
				not := f.reg()
				fmt.Fprintf(&f.body, "  %s = xor i1 %s, true\n", not, cond)
				cond = not
			}
			out := f.reg()
			fmt.Fprintf(&f.body, "  %s = select i1 %s, i64 %s, i64 %s\n",
				out, cond, v.bits, acc.bits)
			// The tags are equal whenever both operands are numbers of the same
			// kind, and min/max of two fixnums is a fixnum — so the tag of the
			// winner is the tag of whichever operand won.
			tag := f.reg()
			fmt.Fprintf(&f.body, "  %s = select i1 %s, i64 %s, i64 %s\n",
				tag, cond, v.tag, acc.tag)
			acc = irVal{bits: out, tag: tag}
		}
		return acc, nil
	}
	// A name that is a local is a *value* — a procedure the body was given or
	// made, not a procedure this can call by name.  `(let ((g (lambda (x) x)))
	// (g 1))` has no global g, and calling one by name asked the runtime for a
	// binding that does not exist: it reported "g: undefined" where the
	// interpreter answered.  A local that holds a procedure is applied as the
	// value it is.
	if local, ok := f.locals[op]; ok {
		return f.emitClosureApply(local, vals)
	}
	// A call to another procedure: native when that procedure was compiled,
	// and a call into the runtime when it was not.  The runtime call is what
	// lets a body with one unemittable part still be compiled: `(begin (display
	// n) (* n n))` runs `display` through the interpreter and multiplies in
	// machine code.
	if f.known(op) || op == f.self {
		return f.emitNativeCall(op, vals)
	}
	return f.emitRuntimeCall(op, vals)
}

// emitRuntimeCall calls a procedure the runtime owns, by name.
//
// The arguments are boxed to cross the boundary and the result is read back,
// which is the same translation every other runtime entry point does.  What is
// worth more care is finding the procedure: a call site is inside a loop as
// often as not, and resolving the name on every iteration costs a string
// conversion, a symbol interning and an environment walk each time.
//
// So each call site owns a slot, initially null, in which the runtime caches
// what the name resolved to.  The lookup happens once per site rather than once
// per call, and a body that calls `string-append` in a loop pays for the
// arguments and the call and nothing else.  The slot is per site and not per
// name because a program may rebind a global between two sites, and each site
// has to see the binding in effect when it first runs.
func (f *irFunc) emitRuntimeCall(op string, vals []irVal) (irVal, error) {
	f.want(gsVal + " @gs_call(i8*, i64, " + gsVal + "*, i64*)")
	name := f.mod.stringLiteral(op, "call"+op)
	cache := f.allocaPtr()
	slot := f.allocaArray(len(vals))
	for i, v := range vals {
		f.storeArg(slot, i, v)
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_call(i8* %s, i64 %d, %s* %s, i64* %s)\n",
		out, gsVal, name, len(vals), gsVal, slot, cache)
	return f.loadVal(out), nil
}

// emitGlobalRead reads a top-level binding by name.
//
// The read happens where the name appears rather than once at entry, because a
// global is mutable and the body may call something that assigns it.  Caching it
// would make a compiled procedure see the value from before the call, which the
// interpreter would not.
//
// An unbound name is the runtime's to report: it may be defined later, or by a
// library the program loads, and refusing to compile a body over a name that is
// not yet bound would reject a program that runs perfectly well.
func (f *irFunc) emitGlobalRead(name string) (irVal, error) {
	f.want(gsVal + " @gs_global(i8*)")
	lit := f.mod.stringLiteral(name, "global"+name)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_global(i8* %s)\n", out, gsVal, lit)
	return f.loadVal(out), nil
}

// storeArg writes one tagged value into an argument array.
func (f *irFunc) storeArg(slot string, i int, v irVal) {
	gp := f.reg()
	fmt.Fprintf(&f.body, "  %s = getelementptr %s, %s* %s, i64 %d\n",
		gp, gsVal, gsVal, slot, i)
	bp := f.reg()
	fmt.Fprintf(&f.body, "  %s = getelementptr %s, %s* %s, i64 0, i32 0\n",
		bp, gsVal, gsVal, gp)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", v.bits, bp)
	tp := f.reg()
	fmt.Fprintf(&f.body, "  %s = getelementptr %s, %s* %s, i64 0, i32 1\n",
		tp, gsVal, gsVal, gp)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", v.tag, tp)
}

// loadVal takes a gs.val apart into the two registers the emitter carries.
func (f *irFunc) loadVal(v string) irVal {
	bits := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 0\n", bits, gsVal, v)
	tag := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 1\n", tag, gsVal, v)
	return irVal{bits: bits, tag: tag}
}

// emitNativeCall calls another compiled procedure.
//
// The arguments go through an array because every compiled body has the same
// signature — `%gs.val f(i64 n, %gs.val *args)` — which is what lets one pointer
// type stand for all of them.  The array is built in the caller's frame.
func (f *irFunc) emitNativeCall(op string, vals []irVal) (irVal, error) {
	// The arguments go as (word, tag) pairs, which is what lets the tags be
	// constant-propagated into the callee instead of being hidden inside an
	// aggregate.  Nothing is boxed and no array is built: a call in a loop
	// costs the call itself.
	var args strings.Builder
	for i, v := range vals {
		if i > 0 {
			args.WriteString(", ")
		}
		fmt.Fprintf(&args, "i64 %s, i64 %s", v.bits, v.tag)
	}
	// In tail position the call is a jump: `musttail` tells LLVM the frame can
	// be reused, which is what makes a loop written as recursion run in constant
	// stack, as R7RS says it must.  LLVM enforces the claim rather than trusting
	// it — a `musttail` it cannot honour is an error, not a silent ordinary
	// call — so a mistake here fails the build instead of the program.
	//
	// `tail` alone would be a hint LLVM may ignore, and a hint is not a
	// guarantee: the loop that motivated this overflowed the stack with `tail`.
	// `musttail` is only available when the caller and the callee have the same
	// parameter count: LLVM reuses the frame, and it cannot when the two frames
	// are different shapes.  It says so rather than dropping the requirement —
	// "cannot guarantee tail call due to mismatched parameter counts" — so a
	// tail call between procedures of different arity is emitted as an ordinary
	// call.  That is correct and only loses the frame reuse; the arity that
	// matters for the language is the self-recursive one, and a procedure
	// calling itself always matches.
	if f.tail && len(vals) == f.arity {
		// `musttail` has to be followed immediately by the `ret` that gives its
		// value back — LLVM is strict about this, and a branch in between is an
		// error rather than a missed optimization.  So the return is written
		// here, and the emitter records that the function has already returned.
		out := f.reg()
		fmt.Fprintf(&f.body, "  %s = musttail call %s @%s(%s)\n",
			out, gsVal, mangle(op), args.String())
		fmt.Fprintf(&f.body, "  ret %s %s\n", gsVal, out)
		f.tailReturned = true
		return irVal{bits: "0", tag: tagFixnum}, nil
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @%s(%s)\n",
		out, gsVal, mangle(op), args.String())
	return f.loadVal(out), nil
}

// truthOf turns a tagged value into an i1 a branch can use.
//
// Scheme's only false value is #f, and an accepted body produces numbers, so a
// fixnum is always true — but "always true" is not the same as "known at
// compile time".  A comparison is itself a fixnum, and its *value* decides the
// branch, so a value whose tag is a constant still has to be tested.  Getting
// that wrong made every comparison test as true, which turned `(if (= n 0) ...)`
// into a branch that was always taken.
func (f *irFunc) truthOf(v irVal) (string, error) {
	// A literal: the answer is known here.
	if v.isConstFixnum() {
		if v.bits == "0" {
			return "false", nil
		}
		return "true", nil
	}
	// A fixnum whose value is not a literal: only the word needs testing, since
	// a fixnum is never #f.
	if v.tag == tagFixnum {
		out := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", out, v.bits)
		return out, nil
	}
	// A boolean is #f exactly when its word is zero, so the test is the word —
	// no call needed.  Without this, `(if (= i 0) ...)` asked the runtime whether
	// the *result of a comparison* was true, once per iteration, which is the
	// most common shape in any Scheme program.
	if v.tag == tagBoolean {
		out := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", out, v.bits)
		return out, nil
	}
	f.want("i64 @gs_truthy(" + gsVal + ")")
	isFixnum := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", isFixnum, v.tag, tagFixnum)
	askLabel := f.freshLabel("truth.ask")
	okLabel := f.freshLabel("truth.ok")
	doneLabel := f.freshLabel("truth.done")
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", isFixnum, okLabel, askLabel)

	f.block(okLabel)
	okFrom := f.currentBlock
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(askLabel)
	asked := f.reg()
	fmt.Fprintf(&f.body, "  %s = call i64 @gs_truthy(%s %s)\n", asked, gsVal, v.bits0(f))
	askedBool := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", askedBool, asked)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i1 [ true, %%%s ], [ %s, %%%s ]\n",
		out, okFrom, askedBool, askLabel)
	return out, nil
}

// bits0 rebuilds a whole gs_val from an irVal, which a runtime call that takes
// one needs.
func (v irVal) bits0(f *irFunc) string {
	first := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s undef, i64 %s, 0\n", first, gsVal, v.bits)
	second := f.reg()
	fmt.Fprintf(&f.body, "  %s = insertvalue %s %s, i64 %s, 1\n", second, gsVal, first, v.tag)
	return second
}

// numericTest asks a question whose answer a fixnum can give directly, and
// hands it to the runtime when the value is a handle.
//
// The two answers are computed in separate blocks and joined, rather than
// asking the runtime and ignoring the result — a handle and a fixnum are both
// reachable, and a program that only ever uses small numbers must not pay for
// the other case.
func (f *irFunc) numericTest(v irVal, pred, other string) (irVal, error) {
	if v.isConstFixnum() && other == "0" {
		// A literal against zero is decided here rather than at run time.  The
		// answer depends on the literal's *value*, not on the fact that it is
		// one, and reading only the tag is what made `(zero? 5)` true.
		zero, err := strconv.ParseInt(v.bits, 10, 64)
		if err != nil {
			return irVal{}, fmt.Errorf("ir: %s is not a machine integer", v.bits)
		}
		yes := map[string]bool{
			"eq":  zero == 0,
			"sgt": zero > 0,
			"slt": zero < 0,
		}[pred]
		return boolVal(yes), nil
	}
	fast := f.reg()
	switch pred {
	case "eq":
		fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", fast, v.bits, other)
	case "sgt":
		fmt.Fprintf(&f.body, "  %s = icmp sgt i64 %s, %s\n", fast, v.bits, other)
	case "slt":
		fmt.Fprintf(&f.body, "  %s = icmp slt i64 %s, %s\n", fast, v.bits, other)
	}
	isFixnum := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", isFixnum, v.tag, tagFixnum)
	slowLabel := f.freshLabel("numtest.slow")
	doneLabel := f.freshLabel("numtest.done")
	// The block the fast answer comes from, which the phi has to name: the
	// branch is about to leave it.
	fastFrom := f.currentBlock
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", isFixnum, doneLabel, slowLabel)

	f.block(slowLabel)
	// A handle: the runtime compares it.  `zero?` and friends are the same
	// question as a comparison against zero.
	var asked string
	switch pred {
	case "eq":
		f.want("i64 @gs_num_eq(" + gsVal + ", " + gsVal + ")")
		asked = f.callNumCompare("gs_num_eq", v, fixnumVal(0))
	case "sgt":
		// x > 0 is 0 < x, and only `<` is an entry point.
		f.want("i64 @gs_num_lt(" + gsVal + ", " + gsVal + ")")
		asked = f.callNumCompare("gs_num_lt", fixnumVal(0), v)
	case "slt":
		f.want("i64 @gs_num_lt(" + gsVal + ", " + gsVal + ")")
		asked = f.callNumCompare("gs_num_lt", v, fixnumVal(0))
	}
	askedBool := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", askedBool, asked)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i1 [ %s, %%%s ], [ %s, %%%s ]\n",
		out, fast, fastFrom, askedBool, slowLabel)
	return f.truthVal(out), nil
}

// callNumCompare calls one of the runtime's comparisons on two tagged values.
func (f *irFunc) callNumCompare(fn string, a, b irVal) string {
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call i64 @%s(%s %s, %s %s)\n",
		out, fn, gsVal, a.bits0(f), gsVal, b.bits0(f))
	return out
}

// compareTagged is a comparison of two tagged values, as 0 or 1.
func (f *irFunc) compareTagged(op string, a, b irVal) (string, error) {
	return f.emitCompare(op, a, b)
}

// boxedLiteral turns a constant too large for a machine word into a handle.
//
// The literal is written as a Scheme number and handed to the runtime, which
// builds the value: reproducing bignum construction in the generated code would
// mean the code knowing how a bignum is stored.
func (f *irFunc) boxedLiteral(text string) (irVal, error) {
	f.want("i64 @gs_box_literal(i8*, i64)")
	lit := f.mod.stringLiteral(text, "lit")
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call i64 @gs_box_literal(i8* %s, i64 %d)\n",
		out, lit, len(text))
	return irVal{bits: out, tag: tagHandle}, nil
}

// known reports whether a name is one the generated code can call directly.
func (f *irFunc) known(name string) bool {
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

// zext widens an i1 to the i64 a Scheme value is.
func (f *irFunc) zext(cmp string) string {
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = zext i1 %s to i64\n", out, cmp)
	return out
}

// emitCompare emits an ordering comparison as 0 or 1.
//
// Two fixnums are compared directly, which is the whole fast path.  Anything
// else goes to the runtime, because a handle's number may be larger than a
// machine word and the comparison has to be the exact one — and because the
// operands may not even be integers.
//
// `>` and `>=` are not runtime entry points: they are `<` and `<=` with the
// operands swapped, which is why the swap is written out here rather than left
// to each caller.
func (f *irFunc) emitCompare(op string, a, b irVal) (string, error) {
	pred := map[string]string{
		"=": "eq", "<": "slt", ">": "sgt", "<=": "sle", ">=": "sge",
	}[op]
	if pred == "" {
		return "", fmt.Errorf("ir: %s is not a comparison", op)
	}
	// Both operands are known to be machine words, so the machine comparison is
	// the whole answer and there is nothing to branch for.
	//
	// This matters more than it looks.  A tag that is a literal zero is the
	// common case — every value a compiled body computes locally is one — and
	// emitting the runtime path anyway put a call to gs_num_eq and a call to
	// gs_truthy inside the loop of `(define (loop i acc) (if (= i 0) ...))`.
	// The optimizer could not remove them, because the tag arrives through a phi
	// and it cannot prove which branch reaches the test.
	if a.tag == tagFixnum && b.tag == tagFixnum {
		fast := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp %s i64 %s, %s\n", fast, pred, a.bits, b.bits)
		return f.zext(fast), nil
	}
	// One operand is a literal fixnum and the other may be a handle.
	//
	// A handle is never equal to a fixnum: the runtime hands back a handle only
	// for a value that does not fit a machine word, so a value that *would*
	// compare equal to a small constant is always a fixnum.  For a comparison
	// whose answer is therefore decided by the tag — `=` against a constant —
	// the machine comparison of a handle would be meaningless and the answer is
	// simply false.
	//
	// This is the shape of every loop test in Scheme, `(= i 0)` above all, and
	// it is the difference between a loop that calls the runtime to ask and one
	// that does not.
	if op == "=" {
		// Whichever side is the literal, the other may be a handle.
		lit, other := irVal{}, irVal{}
		switch {
		case b.isConstFixnum() && a.tag != tagFixnum:
			lit, other = b, a
		case a.isConstFixnum() && b.tag != tagFixnum:
			lit, other = a, b
		}
		if lit.bits != "" {
			fast := f.reg()
			fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", fast, other.bits, lit.bits)
			isFixnum := f.reg()
			fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", isFixnum, other.tag, tagFixnum)
			out := f.reg()
			fmt.Fprintf(&f.body, "  %s = and i1 %s, %s\n", out, fast, isFixnum)
			return f.zext(out), nil
		}
	}
	fast := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp %s i64 %s, %s\n", fast, pred, a.bits, b.bits)

	// The fast answer only stands when both operands are machine words.
	bothFixnum := f.reg()
	tags := f.reg()
	fmt.Fprintf(&f.body, "  %s = or i64 %s, %s\n", tags, a.tag, b.tag)
	fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", bothFixnum, tags, tagFixnum)

	slowLabel := f.freshLabel("cmp.slow")
	doneLabel := f.freshLabel("cmp.done")
	// The block the fast answer comes from, which the phi has to name: the
	// branch is about to leave it.
	fastFrom := f.currentBlock
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", bothFixnum, doneLabel, slowLabel)

	f.block(slowLabel)
	f.want("i64 @gs_num_eq(" + gsVal + ", " + gsVal + ")")
	f.want("i64 @gs_num_lt(" + gsVal + ", " + gsVal + ")")
	f.want("i64 @gs_num_le(" + gsVal + ", " + gsVal + ")")
	// The table is indexed into a variable first, and deliberately so: written
	// as `fn, swap := map[...]{...}[op]` the two-result form of a map index
	// takes over, so `swap` would be the *presence* of the key — always true
	// here — rather than the field.  That silently reversed every `>` and `>=`.
	compare := map[string]struct {
		name string
		swap bool
	}{
		"=":  {"gs_num_eq", false},
		"<":  {"gs_num_lt", false},
		">":  {"gs_num_lt", true},
		"<=": {"gs_num_le", false},
		">=": {"gs_num_le", true},
	}[op]
	lhs, rhs := a, b
	if compare.swap {
		lhs, rhs = b, a
	}
	asked := f.callNumCompare(compare.name, lhs, rhs)
	askedBool := f.reg()
	fmt.Fprintf(&f.body, "  %s = icmp ne i64 %s, 0\n", askedBool, asked)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = phi i1 [ %s, %%%s ], [ %s, %%%s ]\n",
		out, fast, fastFrom, askedBool, slowLabel)
	// A plain 0 or 1: the caller chains comparisons with `and i64` and wraps the
	// finished chain in a boolean tag.
	return f.zext(out), nil
}

// emitCheckedArith emits a +, - or * that falls back to the runtime when the
// machine-word result would not be the Scheme result.
//
// The check is LLVM's own overflow intrinsic, which sets a flag rather than
// trapping: `llvm.sadd.with.overflow.i64` returns the sum and a boolean, and the
// generated code tests the boolean.  Without it, `(* 1000000000000
// 1000000000000)` would produce a wrapped negative number where Scheme produces
// 10^24, and a compiled program would disagree with the interpreter — which is
// the one thing a compiler must never do.
//
// The operands are not assumed to be machine words: if either carries a handle
// tag the runtime does the arithmetic, which is what makes an exact integer of
// any size reachable from compiled code.  The two results are two words each,
// so they are spilled to slots and the join loads them — a phi node over four
// values would have to name the right predecessor for each, and the alloca is
// folded away again on the fast path.
func (f *irFunc) emitCheckedArith(op string, a, b irVal) (irVal, error) {
	intr := map[string]string{
		"+": "llvm.sadd.with.overflow.i64",
		"-": "llvm.ssub.with.overflow.i64",
		"*": "llvm.smul.with.overflow.i64",
	}[op]
	if intr == "" {
		return irVal{}, fmt.Errorf("ir: %s is not a checked operation", op)
	}
	// Both operands must be machine words for the intrinsic to mean anything —
	// unless the tags say they already are, which they do whenever the operands
	// were computed locally.  Asking anyway is what put an unreachable runtime
	// check on every arithmetic operation in every loop, and the optimizer could
	// not remove it because a tag arriving through a phi is not something it can
	// prove.
	knownFixnums := a.tag == tagFixnum && b.tag == tagFixnum
	var bitsSlot, tagSlot string
	if !knownFixnums {
		bitsSlot = f.alloca()
		tagSlot = f.alloca()
	}

	fastLabel := f.freshLabel("arith.fast")
	slowLabel := f.freshLabel("arith.slow")
	doneLabel := f.freshLabel("arith.done")

	if !knownFixnums {
		tags := f.reg()
		fmt.Fprintf(&f.body, "  %s = or i64 %s, %s\n", tags, a.tag, b.tag)
		bothFixnum := f.reg()
		fmt.Fprintf(&f.body, "  %s = icmp eq i64 %s, %s\n", bothFixnum, tags, tagFixnum)
		fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", bothFixnum, fastLabel, slowLabel)
	}

	f.block(fastLabel)
	// The intrinsic returns { i64, i1 }; take it apart with extractvalue.
	pair := f.reg()
	fmt.Fprintf(&f.body, "  %s = call { i64, i1 } @%s(i64 %s, i64 %s)\n",
		pair, intr, a.bits, b.bits)
	val := f.reg()
	over := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue { i64, i1 } %s, 0\n", val, pair)
	fmt.Fprintf(&f.body, "  %s = extractvalue { i64, i1 } %s, 1\n", over, pair)
	// No overflow: the word is the answer.  Overflow: the runtime's, which is
	// the same block a handle operand goes to.
	okLabel := f.freshLabel("arith.ok")
	fmt.Fprintf(&f.body, "  br i1 %s, label %%%s, label %%%s\n", over, slowLabel, okLabel)
	f.block(okLabel)
	if knownFixnums {
		// Nothing to join: the fast path is the only way here, and the answer is
		// a machine word by construction.
		return irVal{bits: val, tag: tagFixnum}, nil
	}
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", val, bitsSlot)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", tagFixnum, tagSlot)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(slowLabel)
	f.want(gsVal + " @gs_arith(i32, " + gsVal + ", " + gsVal + ")")
	slow := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_arith(i32 %d, %s %s, %s %s)\n",
		slow, gsVal, arithCode(op), gsVal, a.bits0(f), gsVal, b.bits0(f))
	slowBits := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 0\n", slowBits, gsVal, slow)
	slowTag := f.reg()
	fmt.Fprintf(&f.body, "  %s = extractvalue %s %s, 1\n", slowTag, gsVal, slow)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", slowBits, bitsSlot)
	fmt.Fprintf(&f.body, "  store i64 %s, i64* %s\n", slowTag, tagSlot)
	fmt.Fprintf(&f.body, "  br label %%%s\n", doneLabel)

	f.block(doneLabel)
	outBits := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", outBits, bitsSlot)
	outTag := f.reg()
	fmt.Fprintf(&f.body, "  %s = load i64, i64* %s\n", outTag, tagSlot)
	return irVal{bits: outBits, tag: outTag}, nil
}

// block starts a basic block and records it as the one being written.
//
// The emitter has to know which block it is in, because a phi node names its
// predecessors by block, and an expression may well finish somewhere other than
// where it started: checked arithmetic branches through a fast and a slow path
// before it produces its value, so the value's block is the join, not the block
// the branch was written in.
func (f *irFunc) block(label string) {
	f.currentBlock = label
	fmt.Fprintf(&f.body, "%s:\n", label)
}

// alloca makes a slot.  LLVM requires the alloca instruction to be in the entry
// block, so the declaration is collected here and written out when the function
// is assembled — the body is generated before the entry block is final.
func (f *irFunc) alloca() string {
	f.nextSlot++
	name := fmt.Sprintf("%%slot%d", f.nextSlot)
	fmt.Fprintf(&f.entry, "  %s = alloca i64\n", name)
	f.slots = append(f.slots, name)
	return name
}

// allocaPtr makes a slot holding one integer, which a call site uses to cache
// what its name resolved to: a handle into the runtime's value table, or zero
// for "not resolved yet".
func (f *irFunc) allocaPtr() string {
	f.nextSlot++
	name := fmt.Sprintf("%%cache%d", f.nextSlot)
	fmt.Fprintf(&f.entry, "  %s = alloca i64\n", name)
	fmt.Fprintf(&f.entry, "  store i64 0, i64* %s\n", name)
	f.slots = append(f.slots, name)
	return name
}

// allocaArray makes a slot for n tagged values, which is how arguments are
// passed to another compiled procedure.
//
// The array is in the caller's frame and lives only for the call, so this is an
// alloca rather than an allocation: a compiled call allocates nothing.
func (f *irFunc) allocaArray(n int) string {
	f.nextSlot++
	name := fmt.Sprintf("%%arr%d", f.nextSlot)
	fmt.Fprintf(&f.entry, "  %s = alloca %s, i64 %d\n", name, gsVal, n)
	f.slots = append(f.slots, name)
	return name
}

// emitCheckedNeg emits a negation that falls back to the runtime on overflow,
// which is the one case int64 cannot represent: -(most negative word).
func (f *irFunc) emitCheckedNeg(a irVal) (irVal, error) {
	return f.emitCheckedArith("-", fixnumVal(0), a)
}

// arithCode is the small integer the runtime uses to know which operation the
// fallback is for.
func arithCode(op string) int {
	switch op {
	case "+":
		return 0
	case "-":
		return 1
	case "*":
		return 2
	}
	return -1
}

// topLevelProcedure recognises (define (name args...) body...), the shape a
// procedure definition has when it can be compiled natively.
//
// Only that shape: (define name (lambda ...)) is the same thing written
// differently and is not recognised, because the scan would have to look
// through the lambda and the emitted symbol would have to match what the
// runtime bound — which it can, but keeping to one shape is one fewer thing to
// get wrong in a compiler whose contract is that it never changes what a
// program means.
func topLevelProcedure(form Value) (name string, formals []*Symbol, body []Value, ok bool) {
	p, isPair := form.(*Pair)
	if !isPair {
		return "", nil, nil, false
	}
	head, isSym := p.Car.(*Symbol)
	if !isSym || head.Name != "define" {
		return "", nil, nil, false
	}
	items, _ := ListToSlice(p.Cdr)
	if len(items) < 2 {
		return "", nil, nil, false
	}
	sig, isPair := items[0].(*Pair)
	if !isPair {
		return "", nil, nil, false
	}
	headSym, isSym := sig.Car.(*Symbol)
	if !isSym {
		return "", nil, nil, false
	}
	params, isList := ListToSlice(sig.Cdr)
	if !isList {
		return "", nil, nil, false // a rest parameter: not this shape
	}
	for _, prm := range params {
		s, isSym := prm.(*Symbol)
		if !isSym {
			return "", nil, nil, false
		}
		formals = append(formals, s)
	}
	return headSym.Name, formals, items[1:], true
}

// paramTypes is the LLVM parameter list for n i64 parameters.
func paramTypes(n int) string {
	if n == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("i64, ", n), ", ")
}
