// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"strings"
)

// ---------------------------------------------------------------------------
// A top-level call
// ---------------------------------------------------------------------------

// topCall is a top-level form that is a plain call to a compiled procedure with
// constant arguments.
//
//	(loop 200000 0)
//
// is the whole of a program's work as often as not, and it used to be handed to
// the interpreter along with everything else at the top level — so the compiled
// body was entered once and the loop ran interpreted.  Emitting the call means
// the loop runs natively from the start, which is the difference between a
// benchmark reading 0.95× and 4.9×.
//
// Only literal arguments are accepted.  A form whose arguments have to be
// computed is left to the interpreter, because computing them is evaluation and
// that is the interpreter's job; a literal is a value the compiler already has.
type topCall struct {
	name string
	args []Value
}

// recogniseTopCall reports whether a top-level form calls a compiled procedure
// with literal arguments.
func recogniseTopCall(form Value, compiled map[string]bool) (topCall, bool) {
	p, ok := form.(*Pair)
	if !ok {
		return topCall{}, false
	}
	head, ok := p.Car.(*Symbol)
	if !ok || !compiled[head.Name] {
		return topCall{}, false
	}
	items, ok := ListToSlice(p.Cdr)
	if !ok {
		return topCall{}, false
	}
	for _, a := range items {
		switch a.(type) {
		case *Integer, Boolean, *String, Char, Float, *Rational:
			// A literal the compiler can place in the module.
		default:
			return topCall{}, false
		}
	}
	return topCall{name: head.Name, args: items}, true
}

// emitTopCall writes the call, with the literals boxed as the callee expects.
func (g *irGen) emitTopCall(c topCall, formals []*Symbol, body *strings.Builder, reg func() string) {
	// The callee takes its arguments as (word, tag) pairs; a literal is a
	// fixnum, a boolean or a boxed value, and the boxing is the same the
	// ordinary emitter does for a literal in a body.
	var args []string
	for _, a := range c.args {
		switch v := a.(type) {
		case *Integer:
			if vv, isSmall := v.Small(); isSmall {
				args = append(args, fmt.Sprintf("i64 %d, i64 0", vv))
			} else {
				lit := g.module.stringLiteral(v.String(), "toplit")
				r := reg()
				fmt.Fprintf(body, "  %s = call i64 @gs_box_literal(i8* %s, i64 %d)\n", r, lit, len(v.String()))
				args = append(args, fmt.Sprintf("i64 %s, i64 1", r))
			}
		case Boolean:
			bit := 0
			if bool(v) {
				bit = 1
			}
			args = append(args, fmt.Sprintf("i64 %d, i64 2", bit))
		default:
			// Anything else is boxed by its source text, which is how a string
			// or a character literal crosses.
			text := WriteToString(a)
			lit := g.module.stringLiteral(text, "toplit")
			r := reg()
			fmt.Fprintf(body, "  %s = call i64 @gs_box_literal(i8* %s, i64 %d)\n", r, lit, len(text))
			args = append(args, fmt.Sprintf("i64 %s, i64 1", r))
		}
	}
	out := reg()
	fmt.Fprintf(body, "  %s = call %s @%s(%s)\n", out, gsVal, mangle(c.name), strings.Join(args, ", "))
}

// ---------------------------------------------------------------------------
// Looking through a named let
// ---------------------------------------------------------------------------

// recogniseWalkIn looks for a walk in a procedure body, seeing through a named
// let if there is one.
//
// It returns the shape it found, which is one of the ones the runtime can run;
// the callers below wrap it so that each recogniser can be asked for its own
// kind.
func recogniseWalkIn(name string, formals []*Symbol, body []Value) (loopKind, predKind, walkShape, bool) {
	// Directly, first: a body that is already a walk needs no rewriting.
	if k, p, shape, ok := recogniseAnyWalk(name, formals, body); ok {
		return k, p, shape, true
	}
	// Otherwise a named let, whose body is then the walk.
	nl, ok := parseNamedLet(body)
	if !ok {
		return loopNone, predNone, shapeNone, false
	}
	inv := map[string]bool{}
	for _, s := range nl.outer {
		inv[s.Name] = true
	}
	return recogniseAnyWalkInv(nl.loop, nl.vars, nl.body, inv)
}

// walkShape says which of the recognisers matched, so that the arguments can be
// passed to the right runtime entry point.
type walkShape int

const (
	shapeNone walkShape = iota
	shapeList
	shapeVec
	shapeCount
	shapeUp
	shapeSearch
	shapeMerge
	shapeBuild
)

// recogniseAnyWalk asks each recogniser in turn.
func recogniseAnyWalk(name string, formals []*Symbol, body []Value) (loopKind, predKind, walkShape, bool) {
	return recogniseAnyWalkInv(name, formals, body, nil)
}

// recogniseAnyWalkInv is recogniseAnyWalk with the invariants an enclosing let
// provides.
func recogniseAnyWalkInv(name string, formals []*Symbol, body []Value, inv map[string]bool) (loopKind, predKind, walkShape, bool) {
	// A do loop's recursive call carries the placeholder name, because the do
	// has no name of its own.  Asking each recogniser for the placeholder as
	// well as for the name means the same loop is found either way, and the
	// caller does not have to know which spelling it was written in.
	if name != doLoopPlaceholder {
		if k, p, shape, ok := recogniseAnyWalkInv(doLoopPlaceholder, formals, body, inv); ok {
			return k, p, shape, true
		}
	}
	if w, ok := recogniseListWalk(name, formals, body); ok {
		return w.kind, w.pred, shapeList, true
	}
	if w, ok := recogniseVecWalk(name, formals, body); ok {
		return w.kind, w.pred, shapeVec, true
	}
	if w, ok := recogniseCountLoopInv(name, formals, body, inv); ok {
		return w.kind, predNone, shapeCount, true
	}
	if w, ok := recogniseUpLoop(name, formals, body); ok {
		return w.kind, predNone, shapeUp, true
	}
	if w, ok := recogniseSearch(name, formals, body); ok {
		return loopNone, w.pred, shapeSearch, true
	}
	if _, ok := recogniseMerge(name, formals, body); ok {
		return loopNone, predNone, shapeMerge, true
	}
	if _, ok := recogniseBuild(name, formals, body); ok {
		return loopNone, predNone, shapeBuild, true
	}
	return loopNone, predNone, shapeNone, false
}

// namedLetForm is a loop written as a named let, with the bindings that enclose
// it.
type namedLetForm struct {
	// loop is the let's name, which is the procedure the loop calls.
	loop string
	// vars and body are the loop's parameters and its body.
	vars []*Symbol
	body []Value
	// init are the expressions the loop's parameters start from, in order.
	init []Value
	// outer names the bindings of the lets that enclose the loop, and outerInit
	// their initial expressions.  These are the loop's invariants — a let binds
	// once and nothing in the loop's scope can assign it — so their values can
	// be computed once and handed to the runtime.
	outer     []*Symbol
	outerInit []Value
}

// parseNamedLet recognises a loop written as a named let and collects the
// bindings around it.
//
//	(let ((a 1) (b 2))                     ; enclosing let: invariants
//	  (let inner ((j i) (acc 0))           ; the loop
//	    (if (= j 0) acc (inner (- j 1) (+ acc a b)))))
func parseNamedLet(body []Value) (namedLetForm, bool) {
	var out namedLetForm
	if len(body) != 1 {
		return out, false
	}
	cur, ok := body[0].(*Pair)
	if !ok || !isForm(cur, "let") {
		return out, false
	}
	// Walk inward through anonymous lets, collecting their bindings.  A named
	// let is where the walk ends.
	for {
		parts, _ := ListToSlice(cur.Cdr)
		if len(parts) < 2 {
			return out, false
		}
		if _, isName := parts[0].(*Symbol); isName {
			// The loop itself.
			if len(parts) < 3 {
				return out, false
			}
			out.loop = parts[0].(*Symbol).Name
			binds, ok := ListToSlice(parts[1])
			if !ok {
				return out, false
			}
			for _, b := range binds {
				bp, ok := b.(*Pair)
				if !ok {
					return out, false
				}
				items, _ := ListToSlice(bp)
				if len(items) != 2 {
					return out, false
				}
				sym, ok := items[0].(*Symbol)
				if !ok {
					return out, false
				}
				out.vars = append(out.vars, sym)
				out.init = append(out.init, items[1])
			}
			out.body = parts[2:]
			return out, true
		}
		// An anonymous let: record its bindings, then look at its single body
		// form, which has to be the loop for this to be the shape.
		binds, ok := ListToSlice(parts[0])
		if !ok {
			return out, false
		}
		for _, b := range binds {
			bp, ok := b.(*Pair)
			if !ok {
				return out, false
			}
			items, _ := ListToSlice(bp)
			if len(items) != 2 {
				return out, false
			}
			sym, ok := items[0].(*Symbol)
			if !ok {
				return out, false
			}
			out.outer = append(out.outer, sym)
			out.outerInit = append(out.outerInit, items[1])
		}
		rest := parts[1:]
		if len(rest) != 1 {
			return out, false
		}
		next, ok := rest[0].(*Pair)
		if !ok || !isForm(next, "let") {
			return out, false
		}
		cur = next
	}
}

// letBindings lists a let's binding names, and the set of them for lookup.
func letBindings(letForm Value) ([]*Symbol, map[string]bool) {
	p, ok := letForm.(*Pair)
	if !ok || !isForm(p, "let") {
		return nil, nil
	}
	parts, _ := ListToSlice(p.Cdr)
	if len(parts) < 2 {
		return nil, nil
	}
	// (let NAME ((v i) ...) ...) — skip the name if there is one.
	if _, isName := parts[0].(*Symbol); isName {
		if len(parts) < 3 {
			return nil, nil
		}
		parts = parts[1:]
	}
	bindings, ok := ListToSlice(parts[0])
	if !ok {
		return nil, nil
	}
	var names []*Symbol
	set := map[string]bool{}
	for _, b := range bindings {
		bp, ok := b.(*Pair)
		if !ok {
			return nil, nil
		}
		sym, ok := bp.Car.(*Symbol)
		if !ok {
			return nil, nil
		}
		names = append(names, sym)
		set[sym.Name] = true
	}
	return names, set
}

// emitNamedLetScope evaluates the outer let's bindings that the loop uses and
// returns them as values, in a fixed order.
func (f *irFunc) emitNamedLetScope(names []*Symbol, _ map[string]bool) []irVal {
	_ = names
	return nil
}

// emitLetInits emits the initial values of a let's bindings.
func (f *irFunc) emitLetInits(bindingsVal Value) []irVal {
	bindings, ok := ListToSlice(bindingsVal)
	if !ok {
		return nil
	}
	out := make([]irVal, 0, len(bindings))
	for _, b := range bindings {
		p, ok := b.(*Pair)
		if !ok {
			return nil
		}
		items, _ := ListToSlice(p)
		if len(items) != 2 {
			return nil
		}
		v, err := f.emitExpr(items[1])
		if err != nil {
			return nil
		}
		out = append(out, v)
	}
	return out
}

// vecWalkArgs puts a vector walk's four arguments in the order the runtime
// wants them: vector, index, bound, accumulator.
//
// When the walk was written with all four as parameters, `args` is already in
// that order and is used as it stands — the recogniser recorded which parameter
// played which role, so they are looked up by name rather than assumed to be in
// the order the runtime wants.  When it was written the way people write it —
// the vector closed over, the bound a literal — only the index and the
// accumulator are parameters, and the other two are evaluated here, once,
// before the call.
//
// Returning nil when something cannot be emitted leaves the caller to generate
// the ordinary recursive body.  That is slow but correct, and it is what the
// silent `len(args) != 4` guard in the emitter used to do at the wrong moment:
// the loop was recognised, the walk was never emitted, and the function still
// compiled — as the recursive body that is *slower* than the interpreter.
func (f *irFunc) vecWalkArgs(w vecWalk, args []irVal) []irVal {
	idx, ok := f.locals[w.idxParm.Name]
	if !ok {
		return nil
	}
	acc, ok := f.locals[w.accParm.Name]
	if !ok {
		return nil
	}
	// The four-parameter spelling passes the vector and the bound along on every
	// step, and the recogniser recorded both as parameters.  Reading them from
	// the activation is what keeps that spelling as fast as it was.
	if w.endParm != nil && w.vecParm != nil {
		if vec, ok := f.locals[w.vecParm.Name]; ok {
			if end, ok := f.locals[w.endParm.Name]; ok {
				return []irVal{vec, idx, end, acc}
			}
		}
	}
	vec, err := f.emitExpr(w.vecExpr)
	if err != nil {
		return nil
	}
	end, err := f.emitExpr(w.endExpr)
	if err != nil {
		return nil
	}
	return []irVal{vec, idx, end, acc}
}

// emitWalkCall emits the walk that `body` is, called with the given argument
// values, and records the result as the function's return value.
func (f *irFunc) emitWalkCall(loopName string, vars []*Symbol, body []Value, args, extraVals []irVal, inv map[string]bool) {
	k, pred, shape, ok := recogniseAnyWalkInv(loopName, vars, body, inv)
	if !ok {
		return
	}
	switch shape {
	case shapeList:
		f.emitListWalkArgs(k, pred, args)
	case shapeVec:
		// The walk wants four arguments — vector, index, bound, accumulator —
		// and `args` only matches when the walk took all four as parameters.
		// The 2-argument spelling passes just the index and the accumulator, so
		// the vector and the bound are evaluated here instead.  Silently doing
		// nothing when they do not match is what made this loop recognised and
		// then never emitted: it compiled to the ordinary recursive body, which
		// is slower than the interpreter, and nothing said so.
		if w, ok := recogniseVecWalk(loopName, vars, body); ok {
			f.emitVecWalkArgs(k, pred, f.vecWalkArgs(w, args))
		}
	case shapeCount:
		incl := false
		if w, ok := recogniseCountLoopInv(loopName, vars, body, inv); ok {
			incl = w.inclusive
		}
		f.emitCountLoopArgs(k, args, extraVals, incl)
	case shapeUp:
		if u, ok := recogniseUpLoop(loopName, vars, body); ok {
			f.emitUpLoopArgs(k, args, u)
		}
	case shapeSearch:
		if w, ok := recogniseSearch(loopName, vars, body); ok {
			f.emitSearchArgs(w, args)
		}
	case shapeMerge:
		if w, ok := recogniseMerge(loopName, vars, body); ok {
			f.emitMergeArgs(w, args)
		}
	case shapeBuild:
		if w, ok := recogniseBuild(loopName, vars, body); ok {
			f.emitBuildArgs(w, args)
		}
	}
}

// emitBuildArgs calls the build walk with the list, the tail and the mode.
func (f *irFunc) emitBuildArgs(w buildWalk, args []irVal) {
	if len(args) < 1 {
		return
	}
	tailVal, err := f.emitExpr(w.tail)
	if err != nil {
		return
	}
	mode := buildCopy
	switch {
	case w.filter:
		mode = buildFilter
	case w.op == "+":
		mode = buildSum
	case w.op == "*":
		mode = buildProduct
	case w.op == "cons" && !w.wantElement:
		mode = buildMap
	}
	f.want(gsVal + " @gs_build(i32, " + gsVal + "*, i32)")
	slot := f.allocaArray(2)
	f.storeArg(slot, 0, args[0])
	f.storeArg(slot, 1, tailVal)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_build(i32 %d, %s* %s, i32 %d)\n",
		out, gsVal, int(w.mapPred), gsVal, slot, mode)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitMergeArgs calls the merge walk with the two lists and the comparison.
func (f *irFunc) emitMergeArgs(w mergeWalk, args []irVal) {
	if len(args) != 2 {
		return
	}
	f.want(gsVal + " @gs_merge(i32, " + gsVal + ", " + gsVal + ")")
	a := args[0].bits0(f)
	b := args[1].bits0(f)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_merge(i32 %d, %s %s, %s %s)\n",
		out, gsVal, mergeCode(w.less), gsVal, a, gsVal, b)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitSearchArgs calls the search walk with the list and the two answers.
func (f *irFunc) emitSearchArgs(w searchWalk, args []irVal) {
	if len(args) != 1 {
		return
	}
	// When the answer is the element itself, the runtime supplies it — there is
	// no expression to evaluate, and trying to evaluate `(car lst)` here would
	// fail, because `lst` is a name the loop's own body binds and the emitter
	// has no local for it.
	foundVal := irVal{bits: "0", tag: tagNull}
	if !w.wantElement {
		v, err := f.emitExpr(w.found)
		if err != nil {
			return
		}
		foundVal = v
	}
	missedVal, err := f.emitExpr(w.missed)
	if err != nil {
		return
	}
	wantElem := 0
	if w.wantElement {
		wantElem = 1
	}
	f.want(gsVal + " @gs_search(i32, " + gsVal + "*, i32)")
	slot := f.allocaArray(3)
	f.storeArg(slot, 0, args[0])
	f.storeArg(slot, 1, foundVal)
	f.storeArg(slot, 2, missedVal)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_search(i32 %d, %s* %s, i32 %d)\n",
		out, gsVal, int(w.pred), gsVal, slot, wantElem)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitUpLoopArgs calls the upward walk with (from, end, acc).
//
// The bound is evaluated here, once, rather than passed in with the loop's own
// parameters: it is usually a free variable rather than one of them, and
// evaluating it before the loop is what makes a `do` whose limit is a global
// work without reading the global every iteration.
func (f *irFunc) emitUpLoopArgs(kind loopKind, args []irVal, u upLoop) {
	if len(args) != 2 {
		return
	}
	endVal, err := f.emitExpr(u.end)
	if err != nil {
		return
	}
	// The runtime takes (from, end, acc), which is not the order the loop's own
	// parameters are in — the accumulator comes last because RunUpLoop's
	// signature ends with it, and getting that wrong made the sum come out as a
	// count: the bound arrived as the accumulator and the accumulator as the
	// bound.
	f.want(gsVal + " @gs_uploop(i32, " + gsVal + "*)")
	slot := f.allocaArray(3)
	f.storeArg(slot, 0, args[0]) // the index it starts at
	f.storeArg(slot, 1, endVal)  // the bound it counts up to
	f.storeArg(slot, 2, args[1]) // the accumulator
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_uploop(i32 %d, %s* %s)\n",
		out, gsVal, int(kind), gsVal, slot)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitListWalkArgs calls the list walk with the list and accumulator given.
func (f *irFunc) emitListWalkArgs(kind loopKind, pred predKind, args []irVal) {
	if len(args) != 2 {
		return
	}
	f.want(gsVal + " @gs_walk(i32, i32, " + gsVal + ", " + gsVal + ")")
	a := args[0].bits0(f)
	b := args[1].bits0(f)
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_walk(i32 %d, i32 %d, %s %s, %s %s)\n",
		out, gsVal, int(kind), int(pred), gsVal, a, gsVal, b)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitCountLoopArgs calls the counting walk with the count and accumulator given.
func (f *irFunc) emitCountLoopArgs(kind loopKind, args []irVal, extras []irVal, inclusive bool) {
	if len(args) != 2 {
		return
	}
	incl := 0
	if inclusive {
		incl = 1
	}
	f.want(gsVal + " @gs_countloop(i32, " + gsVal + "*, i32, i32)")
	slot := f.allocaArray(2 + len(extras))
	for i, v := range args {
		f.storeArg(slot, i, v)
	}
	for i, v := range extras {
		f.storeArg(slot, 2+i, v)
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_countloop(i32 %d, %s* %s, i32 %d, i32 %d)\n",
		out, gsVal, int(kind), gsVal, slot, len(extras), incl)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitVecWalkArgs calls the vector walk with all four arguments given.
func (f *irFunc) emitVecWalkArgs(kind loopKind, pred predKind, args []irVal) {
	if len(args) != 4 {
		return
	}
	f.want(gsVal + " @gs_vecwalk(i32, i32, " + gsVal + "*)")
	slot := f.allocaArray(4)
	for i, v := range args {
		f.storeArg(slot, i, v)
	}
	out := f.reg()
	fmt.Fprintf(&f.body, "  %s = call %s @gs_vecwalk(i32 %d, i32 %d, %s* %s)\n",
		out, gsVal, int(kind), int(pred), gsVal, slot)
	f.listWalkDone = true
	f.listWalkVal = f.loadVal(out)
}

// emitNamedLetLoop emits a loop written as a named let.
//
// The loop's starting values and the enclosing lets' bindings are all ordinary
// expressions, evaluated here in the scopes they belong to; only the *loop*
// has to be the recognised shape.  That split matters: `(let ((a (* 2 3))) ...)`
// is fine because evaluating `a` is something the compiler can already do, while
// the loop body is what has to match.
func (f *irFunc) emitNamedLetLoop(nl namedLetForm) {
	inv := map[string]bool{}
	for _, s := range nl.outer {
		inv[s.Name] = true
	}
	if _, _, shape, ok := recogniseAnyWalkInv(nl.loop, nl.vars, nl.body, inv); !ok || shape == shapeNone {
		return
	}
	// The enclosing bindings first, then the loop's own parameters: the loop
	// body may mention both, and the enclosing ones are the invariants.
	outerVals := make([]irVal, 0, len(nl.outerInit))
	for _, e := range nl.outerInit {
		v, err := f.emitExpr(e)
		if err != nil {
			return
		}
		outerVals = append(outerVals, v)
	}
	vals := make([]irVal, 0, len(nl.init))
	for _, e := range nl.init {
		v, err := f.emitExpr(e)
		if err != nil {
			return
		}
		vals = append(vals, v)
	}
	if len(vals) != len(nl.vars) {
		return
	}
	f.emitWalkCall(nl.loop, nl.vars, nl.body, vals, outerVals, inv)
}

// ---------------------------------------------------------------------------
// A do loop
// ---------------------------------------------------------------------------

// doAsCountLoop reads a do loop as a counting loop, which is what it is.
//
//	(do ((i 0 (+ i 1)) (acc 0 (+ acc i))) ((= i n) acc))
//
// The variables become the loop's parameters, the inits its starting values and
// the steps what it passes next — so the loop the do stands for is
//
//	(if (= i n) acc (loop (+ i 1) (+ acc i)))
//
// which is the same shape the named-let recogniser already handles.  Reading it
// here rather than through `doExpansion` is deliberate: that expansion wraps the
// steps in a `guard` to give `(continue)` its meaning, and a `guard` is not a
// shape anything can be recognised from.
//
// A do with a command list is refused.  Those commands run for their effect
// between the test and the steps, and folding that into a walk would mean
// deciding what the effects are, which is the interpreter's job.
func doAsLoop(form Value) (string, []*Symbol, []Value, bool) {
	p, ok := form.(*Pair)
	if !ok || !isForm(p, "do") {
		return "", nil, nil, false
	}
	args, _ := ListToSlice(p.Cdr)
	if len(args) != 2 {
		return "", nil, nil, false // a command list, or malformed
	}
	specs, ok := ListToSlice(args[0])
	if !ok {
		return "", nil, nil, false
	}
	testClause, ok := ListToSlice(args[1])
	if !ok || len(testClause) < 1 {
		return "", nil, nil, false
	}
	var vars []*Symbol
	var steps []Value
	for _, spec := range specs {
		items, ok := ListToSlice(spec)
		if !ok || len(items) != 3 {
			return "", nil, nil, false
		}
		v, ok := items[0].(*Symbol)
		if !ok {
			return "", nil, nil, false
		}
		vars = append(vars, v)
		steps = append(steps, items[2])
	}
	// (if TEST RESULT (loop STEP...))
	//
	// The test's first result is the loop's value.  A do may have several, which
	// makes it a `values` form and not a walk; and the `begin` the expansion
	// would wrap around them is not written here either, because the recognisers
	// accept a base case that is a bare name and would not see through it.
	if len(testClause) != 2 {
		return "", nil, nil, false
	}
	alt := Cons(doLoopSym, listFromSlice(steps))
	body := []Value{List(Intern("if"), testClause[0], testClause[1], alt)}
	// The placeholder is replaced by the caller, which knows the name the
	// recognisers will compare against.
	return doLoopPlaceholder, vars, body, true
}

// doLoopPlaceholder stands where a do loop's recursive call goes, and is
// replaced by the name the recognisers compare against.
//
// A do has no name of its own — its recursive call is an artifact of reading it
// as a loop rather than something written in the source — so one is chosen here.
// It has to be a *Symbol, because that is what the recognisers match on.  The
// spaces are what keep it from colliding with anything a program can write: a
// Scheme identifier cannot contain one.
const doLoopPlaceholder = " do-loop "

// doLoopSym is the symbol a do loop's recursive call carries.
var doLoopSym = Intern(doLoopPlaceholder)
