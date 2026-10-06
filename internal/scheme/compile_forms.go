// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
)

// The larger special forms, each compiled by its own method.
//
// They used to be cases inside special's switch, which had grown to 563 lines
// and 28 forms — long enough that finding the one you wanted meant reading the
// whole thing.  The switch keeps the dispatch and the small forms, where seeing
// them side by side is the point, and a form whose compilation takes more than
// a screenful is a method it calls.  That is the shape letForm and condForm
// already had.

// matchForm compiles the match form, which special dispatches here.

func (c *comp) matchForm(args []Value, tail bool) bool {

	if len(args) < 2 {
		c.fail("match: expected an expression and at least one clause")
		return true
	}
	// The subject, then four values per clause: the pattern, the variables
	// its body takes, the guard (or #f) and the body.  The matching stays
	// in matchPattern; what compiles is the guard and the body, which is
	// where the work in a match is.
	type matchClause struct {
		pattern Value
		vars    []*Symbol
		guard   Value
		body    []Value
	}
	var parsed []matchClause
	for _, cl := range args[1:] {
		pattern, guard, body, err := parseMatchClause(cl)
		if err != nil {
			c.fail("%v", err)
			return true
		}
		parsed = append(parsed, matchClause{
			pattern: pattern, vars: matchPatternVars(pattern),
			guard: guard, body: body,
		})
	}
	// The operator goes on the stack first, then the subject, then the
	// clauses — the layout a call expects.
	c.emit(opConst, c.konst(matchHelper), 0)
	c.expr(args[0], false)
	argc := int32(1) // the subject; the helper is the operator
	for _, cl := range parsed {
		formals := make([]Value, len(cl.vars))
		for i, v := range cl.vars {
			formals[i] = v
		}
		formalList := listFromSlice(formals)
		c.emit(opConst, c.konst(cl.pattern), 0)
		c.emit(opConst, c.konst(formalList), 0)
		if cl.guard != nil {
			thunk := c.boundThunk(formalList, []Value{cl.guard}, "match guard")
			if thunk == nil {
				return true
			}
			c.emit(opClosure, c.konst(thunk), 0)
		} else {
			c.emit(opConst, c.konst(False), 0)
		}
		thunk := c.boundThunk(formalList, cl.body, "match")
		if thunk == nil {
			return true
		}
		c.emit(opClosure, c.konst(thunk), 0)
		argc += 4
	}
	if tail {
		c.emit(opTailCall, argc, 0)
	} else {
		c.emit(opCall, argc, 0)
	}
	return true

}

// guardForm compiles the guard form, which special dispatches here.

func (c *comp) guardForm(args []Value, tail bool) bool {

	if len(args) < 1 {
		c.fail("guard: missing clause list")
		return true
	}
	spec, ok := args[0].(*Pair)
	if !ok {
		c.fail("guard: malformed clause list")
		return true
	}
	varSym, ok := spec.Car.(*Symbol)
	if !ok {
		c.fail("guard: condition variable is not an identifier")
		return true
	}
	clauses := mustSlice(spec.Cdr)
	// (guard-helper <clauses> <body>): the clauses are a procedure of the
	// condition and the body a thunk, both compiled, and the helper
	// installs the handler around the call the same way the interpreter
	// installs it around its own evaluation.
	clauseCode := c.bodyWithFormals(List(varSym), clauses, "guard", func(sub *comp) {
		sub.guardClauses(varSym, clauses, true)
	})
	bodyCode := c.bodyWithFormals(Empty{}, args[1:], "guard body", func(sub *comp) {
		sub.body(args[1:], true)
	})
	if clauseCode == nil || bodyCode == nil {
		return true
	}
	c.emit(opConst, c.konst(guardHelper), 0)
	c.emit(opClosure, c.konst(clauseCode), 0)
	c.emit(opClosure, c.konst(bodyCode), 0)
	if tail {
		c.emit(opTailCall, 2, 0)
	} else {
		c.emit(opCall, 2, 0)
	}
	return true

}

// selectForm compiles the select form, which special dispatches here.

func (c *comp) selectForm(args []Value, tail bool) bool {

	specs, err := selectSpecs(args)
	if err != nil {
		c.fail("%v", err)
		return true
	}
	c.emit(opConst, c.konst(selectHelper), 0)
	argc := int32(0)
	for _, sp := range specs {
		c.emit(opConst, c.konst(Int(int64(sp.kind))), 0)
		argc++
		for _, e := range sp.exprs {
			thunk := c.bodyWithFormals(Empty{}, []Value{e}, "select", func(sub *comp) {
				sub.body([]Value{e}, true)
			})
			if thunk == nil {
				return true
			}
			c.emit(opClosure, c.konst(thunk), 0)
			argc++
		}
	}
	if tail {
		c.emit(opTailCall, argc, 0)
	} else {
		c.emit(opCall, argc, 0)
	}
	return true

}

// defineRecordTypeForm compiles the define-record-type form, which special dispatches here.

func (c *comp) defineRecordTypeForm(args []Value, tail bool) bool {

	names, _, err := recordType(args)
	if err != nil {
		c.fail("%v", err)
		return true
	}
	// The helper builds the type and its procedures; the compiler knows
	// their names, so it can store each where a binding of that name
	// lives — a slot in this body, or a global at the top level.
	call := List(recordTypeHelper, List(Intern("quote"), listFromSlice(args)))
	producer := c.bodyWithFormals(Empty{}, []Value{call}, "define-record-type", func(sub *comp) {
		sub.body([]Value{call}, true)
	})
	if producer == nil {
		return true
	}
	c.bindValuesFrom(names, Empty{}, producer, false)
	return true

}
