// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
	"strings"
)

// The small SRFI libraries that are mostly syntax: (srfi 2) and-let*, (srfi 8)
// receive, (srfi 26) cut and cute, and (srfi 111) boxes — the one of the four
// that needs a Go object, because a box is a mutable cell.
//
// The macros are written in Scheme and compiled into the binary by
// installEmbeddedSource, so they work in an executable built by `goscheme
// build` on a machine that has none of this repository's files.

const srfi2Source = `
(define-syntax and-let*
  (syntax-rules ()
    ;; The claws are one list operand and everything after it is the body;
    ;; splicing them without those parentheses would make body forms look like
    ;; more claws.
    ((_ (claws ...) body ...) (and-let*-aux (claws ...) body ...))))

(define-syntax and-let*-aux
  (syntax-rules ()
    ;; No claws at all: #t, and a body turns it into a begin.
    ((_ ()) #t)
    ((_ () body1 body2 ...) (begin body1 body2 ...))
    ;; The last claw with no body supplies the value.
    ((_ ((var expr))) (let ((var expr)) (and var)))
    ((_ ((expr))) (and expr))
    ((_ (var)) (and var))
    ;; Otherwise test the claw and go on.
    ((_ ((var expr) rest ...) body ...)
     (let ((var expr)) (and var (and-let*-aux (rest ...) body ...))))
    ((_ ((expr) rest ...) body ...)
     (and expr (and-let*-aux (rest ...) body ...)))
    ((_ (var rest ...) body ...)
     (and var (and-let*-aux (rest ...) body ...)))))
`

const srfi8Source = `
(define-syntax receive
  (syntax-rules ()
    ((_ formals expression body ...)
     (call-with-values (lambda () expression) (lambda formals body ...)))))
`

// cut and cute need one fresh name per <> and per evaluated argument.  The
// templates below draw those names from a pool written into the macro: template
// identifiers are renamed by the expander, so each expansion gets its own
// distinct names, and the pool is what makes several slots in one expansion
// distinct from each other.
const srfi26Source = `
(define-syntax cut
  (syntax-rules ()
    ((_ arg ...) (cut-aux (s1 s2 s3 s4 s5 s6 s7 s8) () () arg ...))))

(define-syntax cut-aux
  (syntax-rules (<> <...>)
    ;; No arguments left: build the procedure.
    ((_ (avail ...) (formals ...) (call ...))
     (lambda (formals ...) (call ...)))
    ;; A slot takes the next name.
    ((_ (next more ...) (formals ...) (call ...) <> rest ...)
     (cut-aux (more ...) (formals ... next) (call ... next) rest ...))
    ;; The rest of the arguments go to one name and are applied.
    ((_ (next more ...) (formals ...) (call ...) <...> rest ...)
     (lambda (formals ... . next) (apply call ... next)))
    ;; Anything else is evaluated where it stands.
    ((_ (avail ...) (formals ...) (call ...) arg rest ...)
     (cut-aux (avail ...) (formals ...) (call ... arg) rest ...))))

(define-syntax cute
  (syntax-rules ()
    ((_ arg ...)
     (cute-aux (s1 s2 s3 s4 s5 s6 s7 s8) (t1 t2 t3 t4 t5 t6 t7 t8) () () () arg ...))))

(define-syntax cute-aux
  (syntax-rules (<> <...>)
    ((_ (avail ...) (temps ...) (formals ...) (binds ...) (call ...))
     (let (binds ...) (lambda (formals ...) (call ...))))
    ((_ (next more ...) (temps ...) (formals ...) (binds ...) (call ...) <> rest ...)
     (cute-aux (more ...) (temps ...) (formals ... next) (binds ...) (call ... next) rest ...))
    ((_ (avail ...) (next more ...) (formals ...) (binds ...) (call ...) <...> rest ...)
     (let (binds ...) (lambda (formals ... . next) (apply call ... next))))
    ((_ (avail ...) (next more ...) (formals ...) (binds ...) (call ...) arg rest ...)
     (cute-aux (avail ...) (more ...) (formals ...) (binds ... (next arg)) (call ... next) rest ...))))
`

// Box is the SRFI-111 box: one mutable cell.
type Box struct {
	Value Value
}

// SchemeDescribe prints the box and what is in it, using the printer it is
// handed so that display and write read the same inside the box as outside;
// see re.Describer.
func (b *Box) SchemeDescribe(p Printer) string {
	var sb strings.Builder
	sb.WriteString("#<box ")
	p.Print(&sb, b.Value)
	sb.WriteString(">")
	return sb.String()
}

func installSRFI2(m *Machine, lib string) {
	m.installEmbeddedSource(lib, srfi2Source, "and-let*")
}

func installSRFI8(m *Machine, lib string) {
	m.installEmbeddedSource(lib, srfi8Source, "receive")
}

func installSRFI26(m *Machine, lib string) {
	m.installEmbeddedSource(lib, srfi26Source, "cut", "cute")
}

func installSRFI111(m *Machine, lib string) {
	// (box value) makes a fresh box holding value.
	m.defSimple("box", 1, 1, func(a []Value) (Value, error) {
		return &Box{Value: a[0]}, nil
	}, lib)

	// (unbox box) is the value inside it.
	m.defSimple("unbox", 1, 1, func(a []Value) (Value, error) {
		b, ok := a[0].(*Box)
		if !ok {
			panic(errf("unbox", "expected a box but got %s", WriteToString(a[0])))
		}
		return b.Value, nil
	}, lib)

	// (set-box! box value) stores a new value and returns an unspecified value.
	m.defSimple("set-box!", 2, 2, func(a []Value) (Value, error) {
		b, ok := a[0].(*Box)
		if !ok {
			panic(errf("set-box!", "expected a box but got %s", WriteToString(a[0])))
		}
		b.Value = a[1]
		return UnspecifiedValue, nil
	}, lib)

	m.defSimple("box?", 1, 1, func(a []Value) (Value, error) {
		_, ok := a[0].(*Box)
		return BooleanOf(ok), nil
	}, lib)
}

func init() {
	registerInstaller(func(m *Machine) {
		installSRFI2(m, "(srfi 2)")
		installSRFI8(m, "(srfi 8)")
		installSRFI26(m, "(srfi 26)")
		installSRFI111(m, "(srfi 111)")
	})
}
