# Tools

Programs in this directory are written **in GoScheme** and run by the
interpreter they ship with.  They are not built, and they are not part of the
release: they are scripts, which is the point — a language is only as useful as
the programs people can write in it.

```sh
goscheme scripts/scmc-disassemble.scm prog.scmc
```

## `scmc-disassemble.scm`

Reads a `.scmc` bytecode file and prints what is in it: the chunks that are
still source as themselves, and every compiled body as its instructions with the
literals they mention.  **It is also the largest application in the repository
that is written in GoScheme rather than Go**, which is why it is here: it reads
binary files a byte at a time, decodes Go's varints and zig-zag integers by
hand, parses a tagged recursive format whose values can hold compiled code
inside compiled code, and prints the result.

```sh
$ goscheme compile hello.scm            # writes hello.scmc
$ goscheme scripts/scmc-disassemble.scm hello.scmc
;;; Disassembled from hello.scmc (bytecode version 3)
...
; --- compiled body: <top level>  (slots 0, parameters 0)
;   constants:
  0:
  ; --- compiled body: <top level>  (slots 1, parameters 1)
  ;   slot names: (name)
  ;   constants:
    0: =
    1: 0
    2: 1
  (instructions
    global 0 0
    local 0 0
    const 1 0
    call 2 0
    ...
  )
```

Indentation is two spaces per level, and a nested body — one that lives in
another body's constant pool — is printed at the same level as the constant
holding it, because it is a value of that body rather than a scope inside it.
That keeps a deep program's output narrow: the reference suite disassembles
with a maximum indentation of ten spaces.

* `-o FILE` writes it to a file instead of the terminal.
* `--check FILE` prints a one-line summary and exit status instead of the
  disassembly, so a script can assert that the output is readable Scheme.  This
  is what `make check-disasm` runs, over the R7RS suite and the extension suite.

What it needs beyond R7RS is `(goscheme fast)` for `bit-and` and `bit-shift`,
and binary ports for opening and reading the file.  Everything else is
`(scheme base)`, `(scheme write)`, `(scheme file)` and
`(scheme process-context)` — no part of it is Go.

The format it reads is documented in [docs/bytecode-internals.md](../docs/bytecode-internals.md).
It is worth reading alongside the script: the script is the format as a reader
sees it, and the document is the format as the writer defines it.
