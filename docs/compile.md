# The native compiler: what it compiles, how it says so, and what it cannot do

This document records how `goscheme compile` turns a Scheme script into a native
executable, which decisions were made and why, and where the limits are. It
exists so that the same ground does not have to be re-covered later, and so that
"why is this procedure not compiled" has a written answer.

## 1. The goal, and the constraint that shapes it

The goal is ordinary: run Scheme faster by running some of it as machine code.

The constraint is the promise the rest of the project already makes. GoScheme
implements R7RS-small, which is a large language: proper tail calls,
continuations, `dynamic-wind`, macros, records, libraries, the full numeric
tower. A compiler that had to handle all of that would be a second
implementation of the language, and the two implementations would disagree —
which is the one thing a compiler must never do.

So the compiler does not try. It compiles the part of a program where machine
code provably means the same thing as the interpreter, and hands everything else
to the interpreter, which is already written and already correct.

## 2. What gets compiled

A procedure is compiled when its body is a computation over its parameters,
its constants and its globals:

| Accepted | How |
| --- | --- |
| `+ - *` on numbers | machine add/sub/mul with an overflow test |
| `= < > <= >=` | machine compare when both are words, runtime otherwise |
| `zero? positive? negative? even? odd? abs min max not` | machine, with a runtime path |
| `quotient remainder modulo` | machine divide, with a runtime path |
| `if` | branch and a phi |
| `let`, `let*` | the binding stays an SSA value |
| `begin`, `and`, `or` | sequencing and short-circuit branches |
| `quote` of a literal | the literal itself |
| a call to another compiled procedure | a direct machine call |
| self-recursion | a direct machine call |
| a call to anything else | a call into the runtime |
| a global read | a read from the runtime, at the point of use |

Everything else — `set!`, `lambda`, `define`, `do`, `case`, `guard`,
`parameterize`, a macro that was not expanded, a continuation — is refused, and
the procedure runs interpreted.

**Refusal is per body, not per expression, for forms; and per expression for
calls.** That distinction matters and is easy to get backwards:

- A call the compiler cannot emit is **not** a refusal. `(begin (display n) (* n
  n))` compiles: `display` becomes a call into the runtime and the multiplication
  is machine code. Refusing the whole body over the `display` would give up the
  multiplication beside it for nothing.
- A form the compiler cannot emit **is** a refusal. `set!` is syntax, not a
  procedure, so there is no runtime call to make on its behalf.

## 3. How a value crosses the boundary

This is the part the rest of the design rests on.

A compiled function computes in machine words. A Scheme exact integer is
unbounded, so a machine word is not always enough — and a compiler that assumed
otherwise would silently wrap where the interpreter promotes.

So a value crossing the boundary is **tagged**:

```llvm
%gs.val = type { i64, i64 }
```

The first word is the value; the second says what the first word means.

| Tag | Meaning |
| --- | --- |
| 0 | a fixnum: the word is the number |
| 1 | a handle: the word indexes the runtime's value table |
| 2 | a boolean: the word is 0 or 1 |

A result too large for a machine word becomes a handle the moment it is produced,
so `(* 100000000000 100000000000)` computes 10^22 correctly in compiled code by
handing the value to the runtime and carrying a reference to it. Nothing is
truncated and no case has to end in "this cannot be represented".

A boolean needs its own tag rather than travelling as the fixnum 0 or 1, because
`(= 1 1)` and `1` are different values: one prints as `#t`, the other as `1`.

## 4. The ABI

Every compiled procedure has the same signature, whatever its arity:

```llvm
%gs.val @gs_lam_name(i64 %n, %gs.val* %args)
```

One signature means one function-pointer type, which means registering a compiled
procedure needs a single `bitcast` rather than one per arity.

The runtime is the C ABI of the `re/` package, which is built with `go build
-buildmode=c-archive` into `libgoscheme.a` and linked into the program:

| Entry point | Used for |
| --- | --- |
| `gs_init`, `gs_finish` | starting and stopping the interpreter |
| `gs_eval_source` | running a top-level form the compiler did not compile |
| `gs_register` | making a compiled body reachable from Scheme |
| `gs_arith` | arithmetic that left the range of a machine word |
| `gs_truthy`, `gs_num_eq`, `gs_num_lt`, `gs_num_le` | questions about a handle |
| `gs_global` | reading a global, at the point of use |
| `gs_call` | calling a procedure the compiler did not emit |
| `gs_box_literal` | a literal too large for a machine word |

## 5. Registering, without which the machine code is dead

`main` hands the top-level forms to the interpreter one at a time. Nothing in
`main` calls a compiled body — so a module that only *defines* functions has
produced dead code, and the program runs entirely on the interpreter while
appearing to be compiled.

Registration is what connects the two. `main` registers each body by name and
address before the first form runs, the runtime records them, and after each
top-level form it attaches the recorded body to the procedure that form defined.
A call then reaches the machine code from either engine:

- the bytecode VM asks at its own call site, because a compiled call does not go
  through `apply` at all;
- the tree walker asks in `applyClosure`.

A body that declines the call — an argument count it was not built for — falls
through to the interpreted body, which was never removed.

## 6. Ordering, and the one cycle that cannot be compiled

LLVM wants a function defined before the first call to it, and a forward
declaration is not available: declaring a function and then defining it is a
redefinition error. So procedures are emitted in dependency order.

Two procedures that call each other cannot both be defined first. That cycle is
refused, and the refusal cascades: a procedure calling a dropped one can no
longer be compiled either, because its body would call something that is not
there. Both passes run until nothing changes, so the answer does not depend on
the order a Go map happens to iterate in.

## 7. What it cannot do

Written down because a limit that is not documented is a bug report waiting to
happen.

- **It is a hybrid.** Part of the program is machine code and part is the
  interpreter. This is by design, but it means the speedup depends on the
  program: arithmetic-heavy code improves a lot, IO-heavy code barely at all.
- **No cross-compilation.** `compile` targets the host. The interpreter itself
  still cross-compiles to six targets.
- **The toolchain must be installed.** `opt`, `llc` and a C compiler, with the
  LLVM version the IR is written for.
- **A continuation cannot escape a compiled caller.** A runtime call made from
  compiled code is synchronous — it has no frame to resume into — so a
  continuation captured inside such a call cannot outlive it. The procedures the
  compiler accepts are ones that cannot contain `call/cc`.
- **A big binary.** The linked program carries the interpreter and the Go
  runtime: roughly 19 MB, against 11 MB for a packed script.
- **No inlining across the boundary, no type inference, no unboxing beyond the
  fixnum case.** Each of these would be a project of its own, and each would risk
  the correctness property in §1.

## 8. Checking the work

The property that matters is not "the generated IR is well-formed" but "the
compiled program computes what the interpreter computes". The tests are built
around that:

- `internal/scheme/ir_test.go` pins what is accepted, what is refused and why,
  the shape of a tagged value, the dependency order, the cycle, and that a global
  is read once per use rather than once per procedure.
- `cmd/goscheme` runs the whole pipeline — source, IR, `opt`, `llc`, link — and
  compares a compiled program's output against the interpreter's, on the cases
  where an optimization is most likely to become a second answer: arithmetic that
  leaves a machine word, comparisons of such values, and calls from one compiled
  procedure to another.

A caution learned the hard way, recorded because it is invisible from the
outside: **a module can verify cleanly and still be dead code.** Inspecting the
IR proves nothing about whether it runs. Every claim about the native path in
this document was checked by running a compiled program, and one of them was
wrong for exactly this reason.
