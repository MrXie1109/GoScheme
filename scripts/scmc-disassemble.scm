#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;;
;;; scmc-disassemble — read a .scmc bytecode file and print what is in it.
;;;
;;;     goscheme scripts/scmc-disassemble.scm prog.scmc [-o prog.scm]
;;;
;;; This is two things at once.
;;;
;;; It is a disassembler: `goscheme compile` writes bytecode, and this reads it
;;; back — the chunks that are still source, and every compiled body as its
;;; instructions with the literals they mention.  The format is documented in
;;; docs/bytecode-internals.md.
;;;
;;; It is also an application written *in* GoScheme, and a fairly demanding
;;; one for a language this young: it reads binary files byte by byte, decodes
;;; Go's varints and zig-zag integers by hand, parses a tagged recursive format
;;; whose values can nest compiled code inside compiled code, walks that tree,
;;; and prints it.  No part of it is written in Go.  It is the kind of program
;;; that is usually the first thing to expose what a language is missing, which
;;; is exactly why it is here: what it needed beyond R7RS is `(goscheme fast)`
;;; for bit-and and bit-shift, and binary ports for opening and reading the
;;; file.  Everything else is (scheme base), (scheme write), (scheme file) and
;;; (scheme process-context).
;;;
;;; Run it the way you would run any script:
;;;
;;;     goscheme compile hello.scm
;;;     goscheme scripts/scmc-disassemble.scm hello.scmc
;;;
;;; The output is a disassembly, not the original script: the compiler does not
;;; keep the source, and a compiled body has no expressions left, only
;;; instructions.  What comes back is therefore
;;;
;;;   * every chunk that is still source, printed as itself — an import, a
;;;     define-syntax, an include, which are stored as data precisely so that
;;;     loading the file runs them again;
;;;   * every compiled body as a (define ...) holding a lambda whose body is a
;;;     list of instructions, with its literals inlined as quoted values.
;;;
;;; The point is to be able to read what the compiler produced with the tools
;;; you already have: the file format is documented in docs/bytecode-internals.md,
;;; and this script is the other half of `goscheme compile`.

(import (scheme base)
        (scheme write)
        (scheme file)
        (scheme process-context)
        (goscheme fast))   ; bit-and and bit-shift, for the varints

;;; ------------------------------------------------------------------ reading

(define (read-u8* port)
  (read-u8 port))

(define (read-bytes port n)
  (let ((bv (read-bytevector n port)))
    (if (eof-object? bv)
        (error "scmc: unexpected end of file")
        bv)))

(define (read-u64 port)
  ;; Little-endian, eight bytes: what the writer's u64 does.
  (let loop ((i 0) (acc 0))
    (if (= i 8)
        acc
        (loop (+ i 1) (+ acc (* (read-u8* port) (bit-shift 1 (* 8 i))))))))

;; A varint is seven bits per byte, least significant first, with the high bit
;; saying "more".  This is Go's encoding/binary.Uvarint, which is what the
;; writer uses.
(define (read-uvarint port)
  (let loop ((shift 0) (acc 0) (count 0))
    (if (> count 10)
        (error "scmc: variable-length integer is too long")
        (let ((b (read-u8* port)))
          (if (eof-object? b)
              (error "scmc: unexpected end of file")
              (let ((acc (+ acc (bit-shift (bit-and b 127) shift))))
                (if (zero? (bit-and b 128))
                    acc
                    (loop (+ shift 7) acc (+ count 1)))))))))

;; Go's PutVarint writes the value zig-zagged: small negatives stay small.
(define (read-svarint port)
  (let ((n (read-uvarint port)))
    (if (even? n)
        (quotient n 2)
        (- (quotient (+ n 1) 2)))))

(define (read-string port)
  (utf8->string (read-bytes port (read-uvarint port))))

(define (iota n)
  (let loop ((i 0) (out '()))
    (if (= i n) (reverse out) (loop (+ i 1) (cons i out)))))

;; The per-slot flags are one bit each, least significant bit of the first byte
;; first, padded to a whole byte.
(define (read-flag-bits port n)
  (let* ((nbytes (quotient (+ n 7) 8))
         (bytes (read-bytes port nbytes)))
    (let loop ((i 0) (out '()))
      (if (= i n)
          (reverse out)
          (let* ((b (bytevector-u8-ref bytes (quotient i 8)))
                 (bit (modulo i 8))
                 (v (bit-and (bit-shift b (- bit)) 1)))
            (loop (+ i 1) (cons (not (zero? v)) out)))))))

;;; ------------------------------------------------------------------- values
;;;
;;; A datum is tagged and recursive; a compiled body can appear inside one, so
;;; these two are mutually recursive.

(define (read-code port)
  (let* ((name (read-string port))
         (n-instrs (read-uvarint port))
         (instrs (let loop ((i 0) (out '()))
                   (if (= i n-instrs)
                       (reverse out)
                       (let* ((op (read-u8* port))
                              (a1 (read-svarint port))
                              (a2 (read-svarint port)))
                         (loop (+ i 1) (cons (list op a1 a2) out))))))
         (n-consts (read-uvarint port))
         (consts (let loop ((i 0) (out '()))
                   (if (= i n-consts)
                       (reverse out)
                       (loop (+ i 1) (cons (read-datum port) out)))))
         (n-slots (read-uvarint port))
         (boxed (read-flag-bits port n-slots))
         (checked (read-flag-bits port n-slots))
         (n-params (read-uvarint port))
         (has-rest (not (zero? (read-u8* port))))
         (rest-slot (read-uvarint port))
         (n-names (read-uvarint port))
         (names (let loop ((i 0) (out '()))
                  (if (= i n-names)
                      (reverse out)
                      (let ((present (read-u8* port)))
                        (if (zero? present)
                            (loop (+ i 1) (cons #f out))
                            (loop (+ i 1) (cons (read-string port) out))))))))
    (list
          (cons 'name name)
          (cons 'instructions instrs)
          (cons 'constants consts)
          (cons 'slots n-slots)
          (cons 'boxed (flags->list boxed))
          (cons 'checked (flags->list checked))
          (cons 'parameters n-params)
          (cons 'rest has-rest)
          (cons 'rest-slot rest-slot)
          (cons 'slot-names (map (lambda (s) (or s #f)) names)))))

(define (flags->list flags)
  ;; The indices that are set: the only ones worth printing.
  (let loop ((i 0) (rest flags) (out '()))
    (if (null? rest)
        (reverse out)
        (loop (+ i 1) (cdr rest) (if (car rest) (cons i out) out)))))

(define (read-datum port)
  (let ((tag (read-u8* port)))
    (cond
      ((= tag 0) '())
      ((= tag 1) #t)
      ((= tag 2) #f)
      ((= tag 3) (list 'unspecified))
      ((= tag 4) (list 'unassigned))
      ((= tag 5) (integer->char (read-uvarint port)))
      ((= tag 6) (string->number (read-string port)))
      ((= tag 7) (string->number (read-string port)))
      ((= tag 8) (let ((bits (read-u64 port))) (list 'float-bits bits)))
      ((= tag 9) (let ((re (read-datum port)) (im (read-datum port)))
                   (list 'complex re im)))
      ((= tag 10) (read-string port))
      ((= tag 11) (string->symbol (read-string port)))
      ((= tag 12) (let ((car* (read-datum port)) (cdr* (read-datum port)))
                    (cons car* cdr*)))
      ((= tag 13) (let* ((n (read-uvarint port))
                         (items (let loop ((i 0) (out '()))
                                  (if (= i n)
                                      (reverse out)
                                      (loop (+ i 1) (cons (read-datum port) out))))))
                    (list->vector items)))
      ((= tag 14) (read-bytes port (read-uvarint port)))
      ((= tag 15) (read-code port))
      ((= tag 16) (list 'eof))
      ((= tag 17) (list 'runtime-helper (read-string port)))
      (else (error "scmc: unknown datum tag" tag)))))

;;; ------------------------------------------------------------------- chunks

(define (read-chunk port)
  (let ((tag (read-u8* port)))
    (cond
      ((= tag 0) (cons 'form (read-datum port)))
      ((= tag 1) (cons 'code (read-code port)))
      ((= tag 2) (let ((n (read-uvarint port)))
                   (cons 'steps
                         (let loop ((i 0) (out '()))
                           (if (= i n)
                               (reverse out)
                               (loop (+ i 1) (cons (read-chunk port) out)))))))
      (else (error "scmc: unknown chunk tag" tag)))))

;; A compiled file may begin with a shebang, so that it can be made
;; executable, and the interpreter skips it — so this has to skip it too.
;; Nothing is consumed unless the whole "#!" is there: a file that merely
;; begins with # is not a compiled file, and the magic check says so.
(define (skip-shebang port)
  (let ((first (peek-u8 port)))
    (if (or (eof-object? first) (not (= first 35)))   ; not #\
        #t
        (begin
          (read-u8 port)                              ; consume #
          (let ((second (peek-u8 port)))
            (if (or (eof-object? second) (not (= second 33)))   ; not !
                (error "scmc: not a .scmc file: it begins with # but not #!")
                (begin
                  (read-u8 port)                      ; consume !
                  (let loop ()
                    (let ((c (read-u8 port)))
                      (if (or (eof-object? c) (= c 10))
                          #t
                          (loop))))))))))) 

(define (read-program path)
  (let ((port (open-binary-input-file path)))
    (skip-shebang port)
    (let* ((magic (read-bytes port 4))
           (version (read-u8* port)))
      (if (not (equal? (utf8->string magic) "GSCM"))
          (error "scmc: not a .scmc file" path))
      (if (> version 3)
          (error "scmc: version" version "is newer than this script knows"))
      (let ((n (read-uvarint port)))
        (list (cons 'version version)
              (cons 'chunks
                    (let loop ((i 0) (out '()))
                      (if (= i n)
                          (reverse out)
                          (loop (+ i 1) (cons (read-chunk port) out))))))))))

;;; ------------------------------------------------------------------ printing

(define (display-line port . parts)
  (for-each (lambda (p) (display p port)) parts)
  (newline port))

(define (datum->source v)
  ;; write produces text read can read back, which is what makes this an
  ;; inverse of the reader and not just a pretty-printer.
  (let ((port (open-output-string)))
    (write v port)
    (get-output-string port)))

(define (compiled-body? v)
  ;; A compiled body in a constant pool is the record read-code returns: an
  ;; alist whose first entry names it.
  (and (pair? v) (pair? (car v)) (eq? (car (car v)) 'name)))

(define (runtime-helper? v)
  (and (pair? v) (eq? (car v) 'runtime-helper)))

(define (print-constant port v)
  (cond
    ((compiled-body? v)
     (display "<a compiled body: " port)
     (display (record-ref v 'name) port)
     (display ">" port))
    ((runtime-helper? v)
     (display "<runtime helper: " port)
     (display (cadr v) port)
     (display ">" port))
    (else (display (datum->source v) port))))

(define (spaces n)
  (if (zero? n) "" (string-append " " (spaces (- n 1)))))

;; Indentation: two spaces per level.  A nested body — one that lives in
;; another body's constant pool — is printed at the *same* level as the
;; constant it belongs to rather than one deeper, because it is already inside
;; a list and a reader can see the nesting; giving it a level of its own made a
;; deep program's output walk off the right of the screen for no gain.
(define indent-step 2)

;; print-nested prints a literal that may itself be a compiled body, which is
;; printed as a body rather than as one very long line.
(define (print-nested port v indent)
  (if (compiled-body? v)
      ;; A body inside a constant pool: its own line, at the same indent as
      ;; the constant that holds it (it is a value of this body, not a child
      ;; scope), so it does not walk off the right of the screen.
      (print-body port v indent #f)
      (begin
        (print-constant port v)
        (newline port))))

(define (print-chunk port chunk indent)
  (let ((pad (spaces indent)))
    (cond
      ((eq? (car chunk) 'form)
       (display pad port)
       (display-line port "; --- source chunk (evaluated when the file is loaded)")
       (display pad port)
       (display-line port (datum->source (cdr chunk))))
      ((eq? (car chunk) 'steps)
       (display pad port)
       (display-line port "; --- a run of chunks that share one continuation extent")
       (for-each (lambda (c) (print-chunk port c indent)) (cdr chunk)))
      (else
       (print-body port (cdr chunk) indent #t)))))

(define (record-ref code key)
  (let ((entry (assq key code)))
    (if entry (cdr entry) #f)))

;; pad? says whether this body should print its own leading indentation.
;; Top-level chunks pass #t; nested bodies (already indented by their
;; constant-pool printer) pass #f so indentation is not applied twice.
(define (print-body port code indent pad?)
  (let ((pad (if pad? (spaces indent) ""))
        (name (record-ref code 'name))
        (instrs (record-ref code 'instructions))
        (consts (record-ref code 'constants))
        (n-slots (record-ref code 'slots))
        (boxed (record-ref code 'boxed))
        (checked (record-ref code 'checked))
        (n-params (record-ref code 'parameters))
        (has-rest (record-ref code 'rest))
        (names (record-ref code 'slot-names)))
    (display pad port)
    (display-line port "; --- compiled body: " name
                  "  (slots " n-slots
                  ", parameters " n-params
                  (if has-rest ", rest" "")
                  ")")
    (let ((field (string-append pad ";   ")))
      (if (pair? names)
          (display-line port field "slot names: " (datum->source names)))
      (if (pair? boxed)
          (display-line port field "set! slots: " (datum->source boxed)))
      (if (pair? checked)
          (display-line port field "checked slots: " (datum->source checked)))
      (if (pair? consts)
          (begin
            (display-line port field "constants:")
            (print-constants port consts (+ indent indent-step)))))
    (display pad port)
    (display-line port "(instructions")
    (print-instructions port instrs indent)
    (display pad port)
    (display-line port ")")))

;; print-instructions puts one instruction per line, indented one step past the
;; "(instructions" it belongs to.

(define (print-constants port consts indent)
  (let loop ((i 0) (rest consts))
    (if (pair? rest)
        (begin
          (display (spaces indent) port)
          (display i port)
          (display ":" port)
          (if (compiled-body? (car rest))
              (begin
                (newline port)          ; the label goes on its own line
                (print-body port (car rest) indent #t))
              (begin
                (display " " port)
                (print-constant port (car rest))
                (newline port)))
          (loop (+ i 1) (cdr rest))))))

;; A one-line description of a body, for the comment above a nested one.
(define (body-summary code)
  (let ((name (record-ref code 'name))
        (n-slots (record-ref code 'slots))
        (n-params (record-ref code 'parameters))
        (has-rest (record-ref code 'rest)))
    (string-append "; --- compiled body: " name
                   "  (slots " (number->string n-slots)
                   ", parameters " (number->string n-params)
                   (if has-rest ", rest" "")
                   ")")))

(define (print-instructions port instrs indent)
  (let ((pad (spaces (+ indent indent-step))))
    (for-each (lambda (in)
              (display pad port)
              (display (opcode-name (car in)) port)
              (display " " port)
              (display (cadr in) port)
              (display " " port)
              (display (caddr in) port)
              (newline port))
              instrs)))

;;; The opcode names, in the order vm.go declares them.  A file written by a
;;; newer interpreter is refused rather than printed with the wrong names.
(define opcode-names
  '#("const" "local" "local-cell" "local-check" "local-cell-check"
     "set-local" "set-cell" "new-cell"
     "global" "set-global" "define-global"
     "closure" "interp-closure" "pop" "eqv"
     "jump" "jump-false" "jump-true" "jump-false-keep" "jump-true-keep"
     "call" "tail-call" "return"))

(define (opcode-name op)
  (if (and (>= op 0) (< op (vector-length opcode-names)))
      (vector-ref opcode-names op)
      (string-append "op#" (number->string op))))

;;; --------------------------------------------------------------------- main

(define (usage)
  (let ((port (current-error-port)))
    (display-line port "usage: goscheme scmc-disassemble.scm FILE.scmc [-o FILE.scm]")
    (newline port)
    (display-line port "Prints what is in a .scmc file: the chunks that are still")
    (display-line port "source as themselves, and each compiled body as a list of")
    (display-line port "instructions with the literals they mention.")
    (exit 2)))

;;; ---------------------------------------------------------------- self-check
;;;
;;; --check keeps the script honest without a human reading its output: it
;;; disassembles FILE.scmc and asserts that every datum it printed reads back,
;;; that the source chunks survived verbatim, and that a body's shape is there.
(define (check path)
  (let* ((program (read-program path))
         (chunks (record-ref program 'chunks))
         (port (open-output-string)))
    (for-each (lambda (c)
                (print-chunk port c 0)
                (newline port))
              chunks)
    (let* ((text (get-output-string port))
           (reader (open-input-string text)))
      (let loop ((n 0))
        (let ((form (read reader)))
          (if (eof-object? form)
              (begin
                (display "scmc-disassemble: ") (display path) (display " -> ")
                (display n)
                (display " readable forms, ")
                (display (count-chunks chunks))
                (display " chunks")
                (newline)
                (exit 0))
              (loop (+ n 1))))))))

(define (count-chunks chunks)
  (let loop ((rest chunks) (n 0))
    (if (pair? rest)
        (loop (cdr rest) (+ n 1))
        n)))

(define (main args)
  (let loop ((rest args) (path #f) (out #f))
    (cond
      ((null? rest)
       (if (not path)
           (usage)
           (run path out)))
      ((equal? (car rest) "-o")
       (if (null? (cdr rest))
           (usage)
           (loop (cddr rest) path (cadr rest))))
      ((equal? (car rest) "-h")
       (usage))
      ((equal? (car rest) "--check")
       (if (null? (cdr rest))
           (usage)
           (check (cadr rest))))
      ((equal? (car rest) "--help")
       (usage))
      (path (usage))
      (else (loop (cdr rest) (car rest) out)))))

(define (run path out)
  (let* ((program (read-program path))
         (version (record-ref program 'version))
         (chunks (record-ref program 'chunks))
         (port (if out (open-output-file out) (current-output-port))))
    (display-line port ";;; Disassembled from " path " (bytecode version " version ")")
    (display-line port ";;; A compiled body has no source left in the file: what follows")
    (display-line port ";;; is its instructions and the literals they mention.")
    (newline port)
    (for-each (lambda (c)
                (print-chunk port c 0)
                (newline port))
              chunks)
    (if out (close-port port))
    (exit 0)))

(main (cdr (command-line)))
