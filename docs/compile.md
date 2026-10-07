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
| a recognised loop — list walk, vector walk, count up or down, search, merge, build, combine, `do`, named `let`, optionally filtered | one runtime call for the whole loop |
| a top-level call to a compiled procedure, with literal arguments | a direct machine call |
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
  interpreter, so the speedup depends on the program.  What it is good at is
  arithmetic and loops: tree recursion whose results feed arithmetic (the shape
  of `fib`) is **4.3×**, a tail loop **4.1×**, and a list or vector walk between
  4 and 10× depending on the size.  A procedure whose body is mostly library
  calls would be *slower*, which is why the compiler declines to compile one — a
  body whose only work is a call into the runtime, or whose every accumulated
  value comes from one, is left to the interpreter.  [docs/performance.md](performance.md)
  has the tables and the reasons.
- **A loop has to be *recognised* to be fast.**  A walk over a list or a vector,
  or a loop that counts down, is compiled as one call that runs the whole loop in
  the runtime.  A loop of a shape the recogniser does not accept still compiles
  and still runs correctly, but it crosses the boundary once per element and will
  be no faster than the interpreter — which is why the set of accepted shapes is
  documented one by one in [docs/performance.md](performance.md#doing-the-loop-in-one-call-which-is-where-the-speed-comes-from).
- **Tail calls are jumps, and that is a correctness requirement rather than an
  optimization.** `musttail` is emitted for a call in tail position, so a loop
  written as recursion runs in constant stack as R7RS requires. Without it the
  generated code consumed a frame per iteration and a loop of 90000 iterations
  segfaulted where the interpreter returned the right answer.
- **No cross-compilation.** `compile` targets the host. The interpreter itself
  still cross-compiles to six targets.
- **The toolchain must be installed.** `opt`, `llc` and a C compiler, with the
  LLVM version the IR is written for.
- **Mutual recursion is compiled.** Two procedures that call each other used to
  be dropped, on the reasoning that "two procedures that call each other cannot
  both be defined first, and LLVM will not accept either ordering". The premise
  is false: LLVM resolves a forward reference to a function in the same module,
  so a cycle emits as functions that call each other with nothing to order. It is
  worth compiling for the reason tail calls exist — `even2?`/`odd2?` are two tail
  calls to each other, and 5,000,000 of them run in constant stack in 0.004 s
  against the interpreter's 0.704 s.
- **A `letrec*` that refers forward is compiled, with the semantics it has.** It
  is still *not* rewritten into a `let*` — that was the old bug, and the rewrite
  is still forbidden for this shape. Instead every binding gets a heap cell
  before any initialiser runs, so a lambda created by an earlier initialiser
  captures the *location* of a name bound later:

  ```scheme
  (letrec* ((mean (lambda (f g) (f (/ (sum g ton) n))))   ; sum and n come later
            (sum  (lambda (g ton) ...))
            (n    (sum (lambda (x) 1) ton)))
    ...)
  ```

  This is the R7RS test suite's `means`, whose result now matches the interpreter
  exactly. It is what the interpreter gets for free by sharing one environment
  frame that later bindings are added to.

  What it is **not** is a relaxation of the check. The old rule refused this
  shape because a `let*` rewrite really would have been wrong — and it was wrong
  in a way that crashed rather than a way that was slow. The fix was to give the
  form its actual semantics, not to accept the rewrite.
- **Control cannot be unwound through a compiled frame, and the way to do it is
  known but not implemented.** A compiled body is a machine function with its own
  frame and no continuation on the interpreter's stack, so a transfer that has to
  return into it — or escape past several of them — has nothing to return to.

  The mechanism that solves this is LLVM's SJLJ exception handling: *"for each
  function which does exception processing ... that function registers itself on
  a global frame list. When exceptions are unwinding, the runtime uses this list
  to identify which functions need processing"*, and control returns to the
  function through a **landing pad** — the block that corresponds to the `catch`
  of a `try`/`catch`. A version of this was built here: each body with a checked
  call announced itself on entry, checked on the way out, and returned the
  handler's value from a landing pad.

  It works for a raise that crosses **one** compiled frame and loses the
  handler's value across three, and the reason is inlining. `opt -O2` merges an
  inlined chain's checks — one frame where the emitter made three — so a scheme
  keyed on frames has nothing left to count, and no frame can tell whether it is
  the last. `noinline` on those bodies did not fix it, which means the
  interaction is not only inlining and was not diagnosed. The work was reverted
  rather than left half-working: a refusal is a slower program, and a wrong
  answer is a wrong one.

  What it would take to finish is either a frame chain the runtime walks (the
  doc's actual scheme, rather than a count) or exception handling emitted through
  LLVM's own `invoke`/`landingpad`, which brings a personality function and the
  Itanium ABI with it.
- **Control cannot be unwound through a compiled frame.** A compiled body is a
  machine function with its own frame and no continuation on the interpreter's
  stack, so a transfer that has to return into it — or escape past several of
  them — has nothing to return to. A body mentioning `call/cc`,
  `call-with-current-continuation`, `dynamic-wind`, `with-exception-handler`,
  `guard`, `raise` or `raise-continuable` is refused and runs in the interpreter.

  This is checked rather than assumed, and the boundary is narrower than the
  family suggests. A `raise` in **tail position** in a compiled body is correct:
  it crosses into the runtime to find its handler, and the runtime has the
  handler stack. A `guard` whose body merely crosses is correct too, because
  `emitGuard` emits it as a thunk the runtime runs. What fails is a `raise` in
  **non-tail position** where the handler lies outside more than one compiled
  frame:

  ```scheme
  (define (inner n) (if (< n 0) (raise 'deep) (* n 2)))
  (define (middle n) (+ 1 (inner n)))
  (define (outer n)  (+ 10 (middle n)))
  (guard (e (#t e)) (outer -1))
  ;; interpreter: (caught deep)
  ;; compiled:     panic: asFloat: not a real number
  ```

  Unwinding from `inner` past `middle` and `outer` must resume two compiled
  frames that are not on the interpreter's stack, and the machine's state on the
  way back is not the state it left. The whole family is refused, which costs the
  tail-position case that would have worked; telling the two apart needs an
  escape analysis this generator does not have, and a slower program is not a
  wrong one.
- **A continuation cannot escape a compiled caller.** A runtime call made from
  compiled code is synchronous — it has no frame to resume into — so a
  continuation captured inside such a call cannot outlive it. A compiled body
  has no interpreter frames at all, so `call/cc` in one is handed a continuation
  describing only the runtime's stack; invoking it later resumes the interpreter
  at a point the compiled frame has already left. A body that mentions `call/cc`,
  `call-with-current-continuation`, `dynamic-wind`, `with-exception-handler`,
  `raise`, `raise-continuable` or `guard` is therefore **refused**, and runs in
  the interpreter.

  This is a *soundness* refusal, kept apart from the cost rule that declines a
  procedure for speed, and the two must not be confused: turning the cost rule
  off with `-compile-all` is a decision about speed and must never be able to
  turn a correct program into a wrong one. It could, until this refusal was
  added — the cost rule happened to decline `count-to` below, so the bug was
  invisible on the default path:

  ```scheme
  (define (count-to n)
    (define k #f)
    (define v (call/cc (lambda (c) (set! k c) 0)))
    (if (< i n) (k (+ v 1)) v))   ; compiled: "attempt to apply non-procedure", interpreted: 4
  ```

  `TestCompileAllIsStillCorrectWhenItCannotCompile` pins it, and it fails if the
  refusal is removed.
- **A named let is emitted whole or not at all.** A named let's loop variables
  exist only as the recognised walk's parameters, so a body that matches no walk
  shape cannot be handed to the ordinary emitter — doing so reported `slow`,
  `fast` and `acc` as undefined. It is now an emission failure, which costs that
  procedure its native body and leaves the rest of the program compiled.
- **An emission failure cascades.** A body that does not emit leaves no function
  behind, so anything that called it would call a symbol that does not exist —
  which is not a slower program but a module `opt` rejects, costing the program
  every native body it had. The call graph is closed *before* emitting, and a
  procedure whose callee is dropped is dropped with it.
- **A big binary, unless it is linked dynamically.** By default the runtime is a
  shared library and a compiled program is **about 16 KB**: it holds its own
  machine code and nothing else, and several programs on one machine share one
  copy of the interpreter. `-static` links the runtime in instead and gives
  about 8 MB — the same as a packed script, because both are mostly the same
  interpreter — for a program that has to run where the library is not
  installed. See §8.
- **No inlining across the boundary, no type inference, no unboxing beyond the
  fixnum case.** Each of these would be a project of its own, and each would risk
  the correctness property in §1.

## 8. Linking: shared by default, static on request

The runtime is the interpreter, and it is several megabytes. Whether a program
carries it is the difference between 16 KB and 8 MB:

| | program | needs at run time |
| --- | --- | --- |
| default (`-buildmode=c-shared`) | ~16 KB | `libgoscheme.so` in the cache, found by rpath |
| `-static` (`-buildmode=c-archive`) | ~8 MB | nothing |

Both libraries are built on demand and cached under the user's cache directory,
keyed to the build flags, so switching modes does not reuse the other one's
library. Both are built with `-s -w`: without it the archive was 27 MB rather
than 10 MB, and because a shared library is *loaded* rather than linked, the
debug information would be paid for on every load instead of discarded once.

**The rpath is what makes a shared program runnable.** The link records the
cache directory, so a program runs where it was built. Moving it elsewhere means
either putting the library where the loader looks or telling the loader where it
is: `LD_LIBRARY_PATH` on Linux, `DYLD_LIBRARY_PATH` on macOS. Copying the
library beside the program is enough on Windows, which searches the program's
own directory, and is *not* enough on Linux, which does not — the rpath has to
say so. A missing library is a load-time error naming it, not a silent
misbehaviour.

The static form is what the release notes and the older documentation describe,
and it is the right choice for one program shipped to a machine that has no
GoScheme on it. The shared form is the right default for someone building and
running programs in place, which is what `goscheme compile` is usually for.

## 9. Checking the work

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
