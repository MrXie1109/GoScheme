// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"sort"
	"strings"

	. "github.com/MrXie1109/GoScheme/internal/re"
)

// Emitting a `lambda` that appears where a value is wanted.
//
// A top-level `(define (f x) ...)` is emitted as a function and registered under
// its name, and the interpreter finds it through the closure it already had.
// That works because the name is known to both sides. A `lambda` in an
// expression position has no name — `(define (make n) (lambda (x) (+ x n)))`
// returns a procedure, and what the caller holds is a value — so it needs a
// value to hold, and the value has to carry the variables the body closed over.
//
// **The representation is the runtime's.** A compiled lambda becomes a real
// `re.Closure`, made by the runtime, which means `(procedure? (make 1))` is true,
// `map` accepts it, `display` prints it the way it prints any procedure, and the
// Go collector owns its memory. The alternative — an environment struct allocated
// in the generated code — would need its own lifetime management beside a
// collector that moves objects, which is the one thing this ABI exists to avoid.
//
// The shape is:
//
//	the body, emitted with its free variables appended to its parameters
//	(define %gs.val @gs_lam_make_lam0(i64 %x.bits, i64 %x.tag,
//	                                  i64 %c_n.bits, i64 %c_n.tag) { ... })
//
//	the lambda, emitted where the value is wanted
//	  %h = call i64 @gs_closure_new(i8* bitcast(... nameless_lam0_entry ...), i64 1)
//	  call void @gs_closure_set(i64 %h, i64 0, %gs.val <the captured n>)
//	  ; the result is a handle to the closure
//
//	the call, emitted where the operator is not a name
//	  %r = call %gs.val @gs_closure_apply(i64 %h, i64 1, %gs.val* %args)
//
// so a captured variable is passed to the body as an extra argument and read
// from the parameter list, which costs nothing beyond the register it lives in.
// That is why the capture list is sorted and shared between the lambda and its
// body: they have to agree on the order, and the only way to guarantee that is
// to compute it once.
//
// What this does *not* do is support `set!` of a captured variable. An SSA
// parameter cannot be assigned through, and a boxed representation is a
// different design; `emitSet` documents the same limit from the other side.
// The scan refuses a body that assigns a variable some lambda closes over, so
// the case is declined rather than emitted wrongly.

// lambdasEmitted counts the lambdas one procedure has emitted, so that each gets
// a distinct symbol.
type lambdaNamer struct {
	owner string
	n     int
}

// next returns a name for one emitted lambda.  The name mentions the procedure it
// was written in, which is what makes a compiler error or a disassembly readable
// — `gs_lam_make_lam0` says where to look.
func (l *lambdaNamer) next() string {
	name := fmt.Sprintf("%s_lam%d", l.owner, l.n)
	l.n++
	return name
}

// freeVariables returns the names a lambda body refers to that are not bound
// inside it, sorted.
//
// Sorted rather than in order of appearance because the lambda and its body are
// emitted at different times and both have to agree; a sort is the cheapest
// thing that cannot drift.  Only names that the enclosing procedure has as
// locals are returned — a global is reached by name at the point of use and
// needs no capture, which is what keeps `(lambda (x) (+ x g))` from capturing
// anything.
func freeVariables(formals []*Symbol, body []Value, outer map[string]irVal) []string {
	bound := map[string]bool{}
	for _, s := range formals {
		bound[s.Name] = true
	}
	seen := map[string]bool{}
	var walk func(v Value)
	walk = func(v Value) {
		switch x := v.(type) {
		case *Symbol:
			if !bound[x.Name] && !seen[x.Name] {
				if _, isLocal := outer[x.Name]; isLocal {
					seen[x.Name] = true
				}
			}
		case *Pair:
			if isForm(x, "quote") {
				return
			}
			// A nested lambda's own parameters are not free in this one, but a
			// variable it captures from further out is — so the walk continues
			// into a nested lambda with its parameters added to the bound set.
			if s, ok := x.Car.(*Symbol); ok && s.Name == "lambda" {
				parts, _ := ListToSlice(x.Cdr)
				if len(parts) >= 1 {
					inner, _ := ListToSlice(parts[0])
					for _, p := range inner {
						if ps, ok := p.(*Symbol); ok {
							wasBound := bound[ps.Name]
							bound[ps.Name] = true
							defer func(n string, had bool) {
								if !had {
									delete(bound, n)
								}
							}(ps.Name, wasBound)
						}
					}
				}
			}
			items, _ := ListToSlice(x)
			for _, a := range items {
				walk(a)
			}
		case *Vector:
			for _, a := range x.Items {
				walk(a)
			}
		}
	}
	for _, b := range body {
		walk(b)
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// emitLambda emits a lambda as a closure value.
//
// The captured values are read from the *enclosing* function's locals and stored
// into the closure when it is made, so a lambda in a loop captures the value the
// variable had at that iteration — which is what the interpreter does, and what
// makes a closure made inside a loop behave the way the report says.
func (f *irFunc) emitLambda(form *Pair) (irVal, error) {
	if f.lambdaDepth > 8 {
		// A nested lambda would otherwise emit a function per level, and a
		// program that nests deeply enough to matter is one whose closures are
		// better interpreted than expanded.  The bound is a guard against a
		// pathological input rather than a limit anyone should meet.
		return irVal{}, fmt.Errorf("ir: lambdas nested more than 8 deep")
	}
	parts, _ := ListToSlice(form.Cdr)
	if len(parts) < 1 {
		return irVal{}, fmt.Errorf("ir: a lambda with no parameter list")
	}
	formals, ok := ListToSlice(parts[0])
	if !ok {
		return irVal{}, fmt.Errorf("ir: a lambda with a dotted parameter list")
	}
	params := make([]*Symbol, 0, len(formals))
	for _, p := range formals {
		s, ok := p.(*Symbol)
		if !ok {
			return irVal{}, fmt.Errorf("ir: a lambda parameter that is not a name")
		}
		params = append(params, s)
	}
	body := parts[1:]
	if len(body) == 0 {
		return irVal{}, fmt.Errorf("ir: a lambda with no body")
	}
	free := freeVariables(params, body, f.locals)

	// The captured values are read here, in the enclosing scope, and stored into
	// the closure below.  Reading them now rather than in the body is what makes
	// the capture a *value*: a later assignment to the variable in the enclosing
	// scope must not be visible through the closure.
	capVals := make([]irVal, len(free))
	for i, name := range free {
		capVals[i] = f.locals[name]
	}

	// The body, with its captures appended to its parameters.  A capture is a
	// parameter of the emitted function, so reading one inside the body costs a
	// register and no boundary crossing.
	name := f.lambdaNames.next()
	savedNames := f.captureNames
	savedCaptures := f.captures
	savedSelfHandle := f.selfHandle
	f.captureNames = map[string]string{}
	sig := &strings.Builder{}
	fmt.Fprintf(sig, "define %s @%s(", gsVal, mangle(name))
	for i, p := range params {
		if i > 0 {
			sig.WriteString(", ")
		}
		fmt.Fprintf(sig, "i64 %%p_%s.bits, i64 %%p_%s.tag", p.Name, p.Name)
	}
	for i, cap := range free {
		if len(params) > 0 || i > 0 {
			sig.WriteString(", ")
		}
		fmt.Fprintf(sig, "i64 %%c_%s.bits, i64 %%c_%s.tag", cap, cap)
	}
	// The closure's own handle comes last, and only when the body assigns one of
	// its captures.  Reading a capture needs nothing more than the parameter it
	// arrives as; *writing* one has to reach the closure's storage, because a
	// parameter cannot be assigned through — an assignment to it would be local
	// to the call, and `(let ((n 0)) (lambda () (set! n (+ n 1)) n))` returned 1
	// every time instead of counting.
	//
	// So the handle is passed when it is needed and not otherwise, which keeps
	// the common case — a closure that only reads — free of it.
	needsSelf := assignsCapture(body, free)
	if needsSelf {
		if len(params) > 0 || len(free) > 0 {
			sig.WriteString(", ")
		}
		sig.WriteString("i64 %self")
	}
	sig.WriteString(") {\nentry:\n")
	saved := f.locals
	f.locals = map[string]irVal{}
	for _, p := range params {
		f.locals[p.Name] = irVal{
			bits: "%p_" + p.Name + ".bits",
			tag:  "%p_" + p.Name + ".tag",
		}
	}
	for _, cap := range free {
		f.locals[cap] = irVal{
			bits: "%c_" + cap + ".bits",
			tag:  "%c_" + cap + ".tag",
		}
	}
	// Which names are captures and where they live, so that an assignment to one
	// can be emitted as a write through the closure rather than as a rebinding of
	// the parameter it arrived as.
	f.captures = map[string]int{}
	for i, cap := range free {
		f.captures[cap] = i
	}
	if needsSelf {
		f.selfHandle = "%self"
	} else {
		f.selfHandle = ""
	}
	// The inner function gets its own body *and* its own entry block: an alloca
	// belongs to the function that uses it, and reusing the outer one's would
	// leave the inner body referring to a slot declared in a different function.
	savedBody, savedEntry := f.body, f.entry
	savedTail := f.tail
	savedNextSlot := f.nextSlot
	savedSlots := f.slots
	// The SSA and label counters restart too: %v16 and %entry_3 belong to the
	// function that first used them, and an inner function numbering its own
	// values from the outer's counter leaves gaps that read as mistakes and,
	// worse, makes two functions share a name when the outer continues after.
	savedLabel, savedNextReg := f.label, f.nextReg
	f.body = strings.Builder{}
	f.entry = strings.Builder{}
	f.nextSlot = 0
	f.slots = nil
	f.label, f.nextReg = 0, 0
	f.tail = true
	var ret irVal
	for i, b := range body {
		f.tail = i == len(body)-1
		v, err := f.emitExpr(b)
		if err != nil {
			f.locals, f.body, f.tail = saved, savedBody, savedTail
			f.entry, f.nextSlot, f.slots = savedEntry, savedNextSlot, savedSlots
			f.label, f.nextReg = savedLabel, savedNextReg
			f.captureNames = savedNames
			f.captures = savedCaptures
			f.selfHandle = savedSelfHandle
			return irVal{}, err
		}
		ret = v
	}
	// The return value is aggregated *before* the counters are restored: the
	// pack needs SSA values of its own, and asking for them afterwards numbers
	// them in the enclosing function while the `ret` that uses them is written
	// into this one.  That produced a `ret` naming a register that belonged to
	// another function, which LLVM reported as a type error three hundred
	// instructions later.
	packed := f.aggregate(ret)
	epilogue := f.entry.String() + f.body.String()
	f.body, f.entry = savedBody, savedEntry
	f.nextSlot, f.slots = savedNextSlot, savedSlots
	f.label, f.nextReg = savedLabel, savedNextReg
	f.locals = saved
	f.tail = savedTail
	f.captureNames = savedNames
	f.captures = savedCaptures
	f.selfHandle = savedSelfHandle
	epilogue += fmt.Sprintf("  ret %s %s\n}\n\n", gsVal, packed)
	f.mod.body.WriteString(sig.String())
	f.mod.body.WriteString(epilogue)

	// The adapter, which is the uniform entry point the runtime calls.  A closure
	// is invoked through this rather than through the body directly, because the
	// runtime calls one ABI for every compiled procedure and the captures have
	// already been folded into the argument array by gs_closure_apply.
	adapter := adapterName(name)
	f.emitClosureAdapter(adapter, name, len(params)+len(free), needsSelf)

	// And the closure itself, where the lambda was written.
	f.want("i64 @gs_closure_new(i64, i64, i64, i64)")
	f.want("void @gs_closure_set(i64, i64, " + gsVal + ")")
	// The code pointer crosses as an integer, which is what the runtime's
	// gs_closure_new takes: a function pointer is data here, and the runtime
	// casts it back where a call is a call.  Passing a null pointer instead —
	// which this did at first, with the address computed and then not used —
	// built a closure that called address zero, and the failure was a
	// segmentation fault inside cgo rather than anything that named the cause.
	code := f.reg()
	fmt.Fprintf(&f.body, "  %s = ptrtoint %s (i64, %s*)* @%s to i64\n",
		code, gsVal, gsVal, adapter)
	h := f.reg()
	selfFlag := 0
	if needsSelf {
		selfFlag = 1
	}
	fmt.Fprintf(&f.body, "  %s = call i64 @gs_closure_new(i64 %s, i64 %d, i64 %d, i64 %d)\n",
		h, code, len(free), len(params), selfFlag)
	for i, v := range capVals {
		fmt.Fprintf(&f.body, "  call void @gs_closure_set(i64 %s, i64 %d, %s %s)\n",
			h, i, gsVal, f.aggregate(v))
	}
	return irVal{bits: h, tag: tagHandle}, nil
}

// emitClosureAdapter writes the uniform entry point for an emitted lambda.
//
// It takes the argument array the runtime passes and forwards each element to the
// body as a (word, tag) pair — the same convention gs_register uses for a
// top-level procedure, for the same reason: one function-pointer type covers
// every arity, so the runtime does not need to know how many parameters a
// procedure has.
func (f *irFunc) emitClosureAdapter(adapter, body string, arity int, needsSelf bool) {
	if arity > 32 {
		return
	}
	ad := &strings.Builder{}
	fmt.Fprintf(ad, "define %s @%s(i64 %%n, %s* %%args) {\nentry:\n", gsVal, adapter, gsVal)
	args := make([]string, 0, arity)
	for i := 0; i < arity; i++ {
		fmt.Fprintf(ad, "  %%e%d = getelementptr %s, %s* %%args, i64 %d\n", i, gsVal, gsVal, i)
		fmt.Fprintf(ad, "  %%w%d = load %s, %s* %%e%d\n", i, gsVal, gsVal, i)
		fmt.Fprintf(ad, "  %%b%d = extractvalue %s %%w%d, 0\n", i, gsVal, i)
		fmt.Fprintf(ad, "  %%t%d = extractvalue %s %%w%d, 1\n", i, gsVal, i)
		args = append(args, fmt.Sprintf("i64 %%b%d, i64 %%t%d", i, i))
	}
	all := strings.Join(args, ", ")
	if needsSelf {
		// The closure's own handle arrives as one more element of the argument
		// array, appended by gs_closure_apply after the caller's arguments and
		// the captures.  It cannot be recovered from the array's address, which
		// is what this used to pass — an address is not a handle, and the runtime
		// rejected it as an out-of-range capture index.
		fmt.Fprintf(ad, "  %%eh = getelementptr %s, %s* %%args, i64 %d\n", gsVal, gsVal, arity)
		fmt.Fprintf(ad, "  %%wh = load %s, %s* %%eh\n", gsVal, gsVal)
		fmt.Fprintf(ad, "  %%h = extractvalue %s %%wh, 0\n", gsVal)
		if all != "" {
			all += ", "
		}
		all += "i64 %h"
	}
	fmt.Fprintf(ad, "  %%r = call %s @%s(%s)\n", gsVal, mangle(body), all)
	fmt.Fprintf(ad, "  ret %s %%r\n}\n\n", gsVal)
	f.mod.body.WriteString(ad.String())
}

// emitClosureApply calls a procedure value.
//
// This is the path for an operator that is not a name — `((make 1) 2)`, `(f x)`
// where f is a parameter.  A compiled procedure and an interpreted one are both
// callable this way, because the value carries which it is.
func (f *irFunc) emitClosureApply(proc irVal, vals []irVal) (irVal, error) {
	f.want(gsVal + " @gs_closure_apply(i64, i64, " + gsVal + "*)")
	if proc.tag != tagHandle {
		// A procedure that is not a handle is not a procedure at all — the
		// interpreter raises for it, and this must not quietly answer something
		// else.  It is reachable: `(1 2)` is a program.
		if proc.tag == tagFixnum {
			return irVal{}, fmt.Errorf("ir: a call whose operator is a number")
		}
	}
	slot := f.allocaArray(len(vals))
	for i, v := range vals {
		f.storeArg(slot, i, v)
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_closure_apply(i64 %s, i64 %d, %s* %s)\n",
		out, gsVal, proc.bits, len(vals), gsVal, slot)
	return f.loadVal(out), nil
}

// aggregate is toAggregate under the name this file uses for it.
func (f *irFunc) aggregate(v irVal) string { return f.toAggregate(v) }

// emitDelay emits `delay` and `delay-force`.
//
//	(delay EXPR)        =>  (make-promise (lambda () EXPR))
//	(delay-force EXPR)  =>  the same, marked so that forcing chains iteratively
//
// A promise is the interpreter's object and forcing one runs Scheme code, so the
// wrapping is a runtime call.  The body is not: it compiles like any other
// expression, and it is the part worth compiling — a delayed computation that
// does arithmetic should not be interpreted just because forcing it is.
func (f *irFunc) emitDelay(x *Pair, force bool) (irVal, error) {
	args, _ := ListToSlice(x.Cdr)
	if len(args) != 1 {
		return irVal{}, fmt.Errorf("ir: delay takes one expression")
	}
	lam := Cons(Intern("lambda"), listFromSlice([]Value{Empty{}, args[0]}))
	thunk, err := f.emitLambda(lam)
	if err != nil {
		return irVal{}, err
	}
	f.want(gsVal + " @gs_delay(" + gsVal + ", i64)")
	fv := "0"
	if force {
		fv = "1"
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_delay(%s %s, i64 %s)\n",
		out, gsVal, gsVal, f.toAggregate(thunk), fv)
	return f.loadVal(out), nil
}

// emitComputedCall emits a call whose operator is an expression.
//
//	((make 1) 2)      the operator is a call
//	(f 2)             the operator is a parameter holding a procedure
//
// The operator is evaluated first and the arguments after it, which is the order
// Scheme evaluates them in — and it matters, because both may have effects.  The
// callee then goes to the runtime, which knows how to apply whatever it is: a
// compiled closure, an interpreted one, a primitive, a continuation.  That is
// the same division gs_call makes for a callee known by name, and it is what
// makes a compiled procedure and an interpreted one interchangeable as values.
func (f *irFunc) emitComputedCall(x *Pair) (irVal, error) {
	outerTail := f.tail
	f.tail = false
	proc, err := f.emitExpr(x.Car)
	f.tail = outerTail
	if err != nil {
		return irVal{}, err
	}
	args, _ := ListToSlice(x.Cdr)
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
	return f.emitClosureApply(proc, vals)
}

// assignsCapture reports whether a lambda body assigns any of the names it
// captured.
//
// It decides whether the emitted body needs its own closure handle: reading a
// capture needs only the parameter it arrives as, but assigning one has to write
// through the closure.  Passing the handle only when it is used keeps the common
// case — a closure that reads what it captured — as cheap as it was.
//
// The walk looks through the body rather than at its top level, because the
// assignment is usually inside the `if` of a loop.  A `set!` inside a *nested*
// lambda is not this lambda's assignment, so a quoted form and an inner
// parameter list are both respected.
func assignsCapture(body []Value, free []string) bool {
	if len(free) == 0 {
		return false
	}
	isFree := map[string]bool{}
	for _, n := range free {
		isFree[n] = true
	}
	var walk func(v Value) bool
	walk = func(v Value) bool {
		p, ok := v.(*Pair)
		if !ok {
			return false
		}
		if isForm(p, "quote") {
			return false
		}
		if s, ok := p.Car.(*Symbol); ok {
			switch s.Name {
			case "set!":
				args, _ := ListToSlice(p.Cdr)
				if len(args) == 2 {
					if target, ok := args[0].(*Symbol); ok && isFree[target.Name] {
						return true
					}
				}
				return false
			case "lambda":
				// A nested lambda's captures are its own; whether it assigns one
				// is that lambda's question, asked when it is emitted.
				inner, _ := ListToSlice(p.Cdr)
				if len(inner) >= 2 {
					return false
				}
			}
		}
		items, _ := ListToSlice(p)
		for _, a := range items {
			if walk(a) {
				return true
			}
		}
		return false
	}
	for _, b := range body {
		if walk(b) {
			return true
		}
	}
	return false
}

// emitCaptureSet writes a captured variable through the closure that owns it.
//
// The value crosses tagged like any other, and the index is the position the
// capture was given when the closure was made — the same index the body reads it
// from at entry, which is why one list serves both directions.
func (f *irFunc) emitCaptureSet(idx int, val irVal) error {
	f.want("void @gs_closure_set(i64, i64, " + gsVal + ")")
	fmt.Fprintf(&f.body, "  call void @gs_closure_set(i64 %s, i64 %d, %s %s)\n",
		f.selfHandle, idx, gsVal, f.toAggregate(val))
	return nil
}

// emitGuard emits a `guard`.
//
//	(guard (VAR (TEST BODY ...) ...) BODY ...)
//
// The body is emitted as a compiled thunk — a closure of no arguments — and the
// whole form becomes one call into the runtime, which runs the thunk under the
// interpreter's own handler machinery.  That is the split the runtime's gs_guard
// documents: the part worth compiling is the body, and the part that is about
// conditions rather than about arithmetic stays where `raise` looks for handlers.
//
// The clauses cross as their own source text.  An encoding of clause structure
// across the C ABI would be a second representation of Scheme to keep in step
// with the first, and the text round trip is one a top-level form already makes.
func (f *irFunc) emitGuard(x *Pair) (irVal, error) {
	// The form arrives already expanded — expandBody ran over the whole
	// procedure before anything looked at it — so this works from it directly
	// rather than expanding again.  Re-expanding was wrong in a way that took a
	// while to see: expandDerived has no `guard` case, so it walked into the
	// clause list and rebuilt it, and the body it handed back was not the one
	// the caller had.
	args, _ := ListToSlice(x.Cdr)
	if len(args) < 1 {
		return irVal{}, fmt.Errorf("ir: guard with no clauses")
	}
	// The clauses cross as their own source text.  An encoding of clause
	// structure across the C ABI would be a second representation of Scheme to
	// keep in step with the first, and the text round trip is one a top-level
	// form already makes.  A clause shape this does not understand — `=>`, a
	// bare test — is then the interpreter's business rather than a refusal here.
	clauses := WriteToString(args[0])

	// The body as a thunk: a closure of no arguments holding the compiled body.
	// A closure rather than a plain function because the runtime applies it, and
	// what the runtime holds is a value.
	body := args[1:]
	if len(body) == 0 {
		return irVal{}, fmt.Errorf("ir: guard with no body")
	}
	lam := Cons(Intern("lambda"), listFromSlice(append([]Value{Empty{}}, body...)))
	thunk, err := f.emitLambda(lam)
	if err != nil {
		return irVal{}, err
	}
	f.want(gsVal + " @gs_guard(i8*, i64, " + gsVal + ")")
	lit := f.mod.stringLiteral(clauses, "guard")
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_guard(i8* %s, i64 %d, %s %s)\n",
		out, gsVal, lit, len(clauses), gsVal, f.toAggregate(thunk))
	return f.loadVal(out), nil
}

// emitComputedCall emits a call whose operator is an expression.
//
//	((make 1) 2)      the operator is a call
//	(f 2)             the operator is a parameter holding a procedure
//
// The operator is evaluated first and the arguments after it, which is the order
// Scheme evaluates them in — and it matters, because both may have effects.  The
// callee then goes to the runtime, which knows how to apply whatever it is: a
// compiled closure, an interpreted one, a primitive, a continuation.  That is
// the same division gs_call makes for a callee known by name, and it is what
// makes a compiled procedure and an interpreted one interchangeable as values.
