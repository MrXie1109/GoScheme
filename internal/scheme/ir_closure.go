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
	// A captured value is normally read here and *copied* into the closure, and
	// the copy is what makes it a value: a later assignment in the enclosing
	// scope must not be visible through the closure.
	//
	// A `letrec*` binding is the one thing that has to be captured by
	// *reference* instead, and it is not a refinement — it is what the form
	// means.  The slot exists before the initialisers run, so a lambda created
	// by an earlier initialiser captures a location that is filled in later;
	// copying the value at creation would copy the uninitialised slot, which is
	// exactly the bug that made the old `letrec*`-to-`let*` rewrite wrong.
	//
	//	(letrec* ((mean (lambda (f g) (f (/ (sum g ton) n))))   ; n comes later
	//	          (n    (sum (lambda (x) 1) ton)))
	//	  (mean values values))
	//
	// A by-reference capture is marked and the read moves into the closure's
	// body: the reference is to a slot in the enclosing *frame*, and a compiled
	// frame is a machine frame that is gone once the procedure returns — so what
	// is stored is the closure's own storage for it, written at creation from
	// the slot's current contents and updated by a `set!`.  What makes the
	// forward reference work is that the *slot* is what the earlier lambda
	// records, and the initialiser for `n` fills that same slot before anything
	// can call the closure.
	capVals := make([]irVal, len(free))
	for i, name := range free {
		// A `letrec*` binding is captured as the **cell**, not as the value in
		// it, and that is the whole reason the cell exists.  The closure may be
		// created before the binding's initialiser has run — `(letrec* ((a
		// (lambda () (b))) (b ...)))` — so reading the value now would read an
		// empty cell.  What the closure records is where `b` will be, and by the
		// time anything can call it, it is there.
		//
		// The capture is marked so that the closure's body fetches from the cell
		// on every read rather than treating the handle as the value.
		v := f.locals[name]
		if v.cell {
			capVals[i] = v
			continue
		}
		capVals[i] = v
	}

	// The body, with its captures appended to its parameters.  A capture is a
	// parameter of the emitted function, so reading one inside the body costs a
	// register and no boundary crossing.
	name := f.lambdaNames.next()
	// Whether the body assigns one of its captures, which decides whether the
	// closure's own handle has to be passed to it: reading a capture needs only
	// the parameter it arrives as, while writing one has to reach the closure's
	// storage.  A closure that only reads does not take the extra argument, so
	// the common case stays as cheap as it was.
	needsSelf := assignsCapture(body, free)

	// ---- the inner function -------------------------------------------------
	//
	// The body is emitted as its **own** irFunc rather than by saving and
	// restoring fields on this one, and that is the whole point of this block.
	//
	// Saving and restoring was how it worked, and it leaked: three fields —
	// currentBlock, arity and tailReturned — were never saved, and each one
	// produced invalid LLVM.  currentBlock was the worst: an `if` inside the
	// lambda left this function's block tracking pointing at a block in the
	// *inner* function, so the outer function's next phi named a label that did
	// not exist there.  `(define (make-adder n) (if (positive? n) (lambda (x)
	// (+ x n)) (lambda (x) (- x n))))` — an ordinary procedure — failed to
	// compile with "use of undefined value %arith.done_3".
	//
	// A field that is not carried over is now a compile error rather than a
	// wrong module, which is the property that makes this shape worth the extra
	// lines.  See innerFunc for what is inherited and why.
	inner := f.innerFunc(name, len(params)+len(free), needsSelf)
	for _, p := range params {
		inner.locals[p.Name] = irVal{
			bits: "%p_" + mangleName(p.Name) + ".bits",
			tag:  "%p_" + mangleName(p.Name) + ".tag",
		}
	}
	for _, cap := range free {
		// A capture arrives as a parameter, and a `letrec*` capture arrives as
		// the *cell's* handle — so the body reads through it, which is the point.
		inner.locals[cap] = irVal{
			bits: "%c_" + mangleName(cap) + ".bits",
			tag:  "%c_" + mangleName(cap) + ".tag",
			cell: f.locals[cap].cell,
		}
	}
	// Which names are captures and where they live, so that an assignment to one
	// is emitted as a write through the closure rather than as a rebinding of the
	// parameter it arrived as.
	for i, cap := range free {
		inner.captures[cap] = i
	}
	if needsSelf {
		inner.selfHandle = "%self"
	}

	// The signature.  The captures come after the parameters, and the closure's
	// own handle last — reading a capture needs nothing more than the parameter
	// it arrives as, while *writing* one has to reach the closure's storage,
	// because a parameter cannot be assigned through and an assignment to it
	// would be local to the call.
	sig := &strings.Builder{}
	fmt.Fprintf(sig, "define %s @%s(", gsVal, mangle(name))
	first := true
	sep := func() {
		if !first {
			sig.WriteString(", ")
		}
		first = false
	}
	for _, p := range params {
		sep()
		fmt.Fprintf(sig, "i64 %%p_%s.bits, i64 %%p_%s.tag", mangleName(p.Name), mangleName(p.Name))
	}
	for _, cap := range free {
		sep()
		fmt.Fprintf(sig, "i64 %%c_%s.bits, i64 %%c_%s.tag", mangleName(cap), mangleName(cap))
	}
	if needsSelf {
		sep()
		sig.WriteString("i64 %self")
	}
	sig.WriteString(") {\nentry:\n")

	inner.tail = true
	var ret irVal
	for i, b := range body {
		inner.tail = i == len(body)-1
		v, err := inner.emitExpr(b)
		if err != nil {
			return irVal{}, err
		}
		ret = v
	}
	// The return value is aggregated while the inner counters are still in
	// force: the pack needs SSA values of its own, and asking for them after the
	// function is finished would number them here.
	packed := ret.bits0(inner)
	epilogue := inner.entry.String() + inner.body.String()
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
			h, i, gsVal, v.bits0(f))
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
		f.selfHandle, idx, gsVal, val.bits0(f))
	return nil
}

// emitDelay emits `delay` and `delay-force`.
//
//	(delay EXPR)        =>  a promise over (lambda () EXPR)
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
		out, gsVal, gsVal, thunk.bits0(f), fv)
	return f.loadVal(out), nil
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
		out, gsVal, lit, len(clauses), gsVal, thunk.bits0(f))
	return f.loadVal(out), nil
}

// innerFunc builds the irFunc for a lambda emitted inside this one.
//
// It exists so that every field of a fresh function is accounted for.  The
// alternative — reusing this irFunc and saving and restoring the fields around
// the inner emission — is what the emitter used to do, and it silently leaked
// three of them.  With a constructor, a field that is neither set here nor
// inherited is one Go's zero value makes obviously wrong (an empty body, a
// counter at zero), and a field that *should* be inherited and is not has to be
// written down as absent rather than forgotten.
//
// What is inherited, and why:
//
//	mod          the module being built.  A lambda body may need a string
//	             constant of its own, and there is one module.
//	lambdaNames  the counter that keeps emitted lambdas' symbols distinct.
//	             Sharing it is what makes `f_lam0`, `f_lam1` unique across the
//	             whole procedure rather than per function.
//	lambdaDepth  one deeper, so that a pathological nest is stopped.
//
// What is *not* inherited:
//
//	body, entry       the inner function's own text.
//	label, nextReg    its own counters; %v7 belongs to the function that first
//	                  used it.
//	currentBlock      it starts in `entry` and tracks its own blocks.  This is
//	                  the field whose absence produced invalid IR.
//	nextSlot, slots   its own allocas, which belong in its own entry block.
//	arity             its own ABI arity, which `musttail` is checked against.
//	tail, tailReturned  its own position; a body is emitted in tail position.
//	locals           replaced by the caller with the parameters and captures,
//	                  which is the whole of the inner scope.
//	captures, selfHandle  set by the caller, since they are properties of the
//	                  closure rather than of this function.
//	calls, self      the inner function may call the outer procedures, but that
//	                  set is per emitted function and the caller adds to it.
func (f *irFunc) innerFunc(name string, arity int, needsSelf bool) *irFunc {
	inner := &irFunc{
		name:         name,
		mod:          f.mod,
		params:       nil,
		locals:       map[string]irVal{},
		label:        0,
		nextReg:      0,
		currentBlock: "entry",
		calls:        append([]string(nil), f.calls...),
		self:         name,
		arity:        arity,
		lambdaNames:  f.lambdaNames, // shared, not copied: see the field
		lambdaDepth:  f.lambdaDepth + 1,
		captureNames: map[string]string{},
		captures:     map[string]int{},

		tail: true,
	}
	// The adapter's own entry block is written by the caller, so the first thing
	// the inner body writes is an instruction in `entry` — which is what
	// currentBlock says.
	return inner
}
