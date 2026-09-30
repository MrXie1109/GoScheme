#!/usr/bin/env goscheme
;;; SPDX-License-Identifier: MIT
;;; Files and directories -- (goscheme fs).
;;;
;;; Run with:  goscheme examples/fs.scm
;;;
;;; The example builds a small tree in the system's temporary directory and
;;; removes it again, so running it leaves nothing behind.

(import (scheme base) (scheme write) (goscheme fs))

(define (show label . values)
  (display label)
  (for-each (lambda (v) (display " ") (write v)) values)
  (newline))

;;; ------------------------------------------------------------ a place to play
(define temp-root
  (or (get-environment-variable "TMPDIR")
      (get-environment-variable "TEMP")
      "/tmp"))
(define root
  (path-join temp-root (string-append "goscheme-fs-example-"
                                      (number->string (current-jiffy)))))
(create-directory-tree (path-join root "docs"))
(create-directory (path-join root "src"))
(show "made:" (path-absolute root))

;;; ----------------------------------------------------------------- writing
(define (spit path text)
  (call-with-output-file path (lambda (port) (display text port))))

(spit (path-join root "README.md") "the project\n")
(spit (path-join root "docs" "guide.md") "how to use it\n")
(spit (path-join root "docs" "notes.txt") "scratch\n")
(spit (path-join root "src" "main.scm") "(display \"hi\")\n")

;;; ---------------------------------------------------------------- listing
(show "the top level:" (directory-list root))
(show "markdown one level down:" (glob (path-join root "*" "*.md")))
(show "and in a named subdirectory:" (glob (path-join root "docs" "*.md")))

;; directory-walk visits the root and everything under it, in order.
(define seen '())
(directory-walk root (lambda (path) (set! seen (cons (path-base path) seen))))
(show "everything under it:" (reverse seen))

;;; ------------------------------------------------------------ path helpers
(define guide (path-join root "docs" "guide.md"))
(show "join:" (path-base guide))
(show "directory:" (= (string-length (path-directory guide)) (- (string-length guide) 9)))
(show "extension:" (path-extension guide) (path-extension (path-join root "README.md")))
(show "size of that file:" (file-size guide))
(show "a missing file raises:" (guard (e (#t 'no-such-file)) (file-size (path-join root "nope"))))

;;; --------------------------------------------------------------- tidying up
;; A directory has to be empty before delete-directory will take it; the -tree
;; form is the recursive one.
(delete-file (path-join root "src" "main.scm"))
(delete-directory (path-join root "src"))
(delete-directory-tree root)
(show "cleaned up:" (file-exists? root))

(newline)
(display "fs: end of tour")
(newline)
