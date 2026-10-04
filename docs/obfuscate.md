# Obfuscating a compiled program

`goscheme compile -obfuscate` writes a `.scmc` file that runs exactly like the
one without the flag and does not read like a description of the program it came
from.

```sh
goscheme compile prog.scm -o prog.scmc -obfuscate
chmod +x prog.scmc          # compile sets the execute bits already
./prog.scmc
```

**It is not encryption.** That sentence is first because everything else in this
file is a consequence of it. What follows is what it does, what it does not do,
who it stops, and how to check that it worked.

## What it changes

A compiled file carries more than its instructions. Every body has a name for
the disassembler and error messages, every slot has the name of the variable
that lives in it, and every global reference is a symbol. None of that is needed
to run the program and all of it says what the program is: a slot called
`password` next to a call to `string=?` is a sentence.

| | before | after |
|---|---|---|
| compiled bodies | `check-password` | `b0`, `b1`, … |
| slot names | `("p")` | `("v0")` |
| globals the program defines | `secret-key` | `g0`, `g1`, … |
| constant pool order | as compiled | shuffled |

Two details are worth knowing because they explain the shape of the output:

- **Slot names are replaced, not removed.** `opLocalCheck` reads them to report
  *which* variable was used before it was initialized, so the array has to stay
  one symbol per slot. The message survives; the name in it does not. (Deleting
  the array instead makes such a program panic — that was a real bug, caught by
  running the whole test corpus obfuscated.)
- **A global the program defines is renamed only when nothing else refers to it
  by name.** A name mentioned in a source chunk keeps its name, because that
  chunk is evaluated by the interpreter as written; a name the program does not
  define belongs to a library, which knows it by its own name.

The constant pools are shuffled, so compiling the same source twice gives
different bytes, and the position of a constant says nothing about where the
code begins.

## What it does not do

Read this part before deciding to rely on it.

- **Strings and numbers stay.** The program needs them. `(display "ACCESS
  GRANTED")` still says `ACCESS GRANTED` in the file.
- **The instructions stay.** A reader can still follow the control flow, and
  `scripts/scmc-disassemble.scm` — which is written in GoScheme and ships with
  the interpreter — prints the whole thing. Obfuscation removes the vocabulary,
  not the grammar.
- **A symbol built at run time is not caught.** `(string->symbol "secret")`
  produces a name no obfuscation pass can know about, and any global it names
  keeps its name in the file.
- **It is not a licence enforcement mechanism.** MIT lets anyone redistribute
  your program either way. This is about raising the cost of reading it, not
  about preventing it.
- **It is not deterministic.** Every build differs. If you need reproducible
  builds, do not use it — or hash the obfuscated output and keep the hash.

## Who it stops

Being honest about the threat model is the useful part:

| reader | what they get |
|---|---|
| someone who runs `strings` on the file | nothing useful — the names are gone |
| someone who opens it in an editor | a wall of bytes |
| someone willing to run the disassembler | the program's structure, without its vocabulary |
| someone who reads the disassembly patiently | most of what the program does |

So it is worth doing when the goal is to keep a casual reader from learning what
the program is *about* — the names of its parts, the shape of its data, the
domain it is written for. It is not worth doing if the goal is to keep a
determined reader from learning what it does; nothing short of not shipping the
program achieves that.

## Checking that it worked

The names should be gone, and the program should behave identically. Both are
easy to check:

```sh
# the program still runs, and prints what it printed before
goscheme compile prog.scm -o plain.scmc
goscheme compile prog.scm -o obfuscated.scmc -obfuscate
diff <(./plain.scmc) <(./obfuscated.scmc)     # no difference

# the names are gone
strings obfuscated.scmc | grep -c secret-key  # 0
```

If your program defines a global and you want to see whether it was renamed,
disassemble both files and compare — the obfuscated one has `g0` where the other
has the name:

```sh
goscheme scripts/scmc-disassemble.scm plain.scmc      | grep -i password
goscheme scripts/scmc-disassemble.scm obfuscated.scmc | head -30
```

One thing to expect when comparing output: an error message that names the
procedure it happened in is renamed with it. `procedure: wrong number of
arguments` becomes something like `b1: wrong number of arguments` — which body
number it is depends on the program. The behaviour is the same; the label is
not.

## What it costs

**Nothing at run time.** The names were never used to execute the program, so
the interpreter does not know or care that they are gone. Measured on the
`sort` benchmark, best of five: 10.52 ms obfuscated against 10.19 ms not, which
is noise — the two builds are identical apart from their names, and the run does
not look at those.

**Nothing at read time either**, and a slightly smaller file: version 5 of the
format keeps one copy of each name in a symbol table rather than writing the name
wherever it was used, and an obfuscated file has the same table with shorter
entries.

## When not to use it

- **A library, or anything you want people to read.** Obfuscation is the
  opposite of the point. [open-source.md](open-source.md) asks people to ship
  the `.scm` alongside the `.scmc`, and there is no version of that request that
  applies to a file you have deliberately made unreadable.
- **Reproducible builds.** The shuffle is seeded from the clock.
- **Anything where the security of the program depends on the file being
  unreadable.** It is not that kind of tool. If a secret is in the program, the
  secret is in the file, whatever the flag says.
