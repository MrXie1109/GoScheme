;;; SPDX-License-Identifier: MIT
;;; Process and filesystem extensions: children as ports, and (goscheme fs).
;;;
;;; The process tests shell out, so they only run on a POSIX system; the
;;; filesystem tests run everywhere.

(import (scheme base) (scheme write) (scheme char) (chibi test)
        (goscheme process) (goscheme fs))

(test-begin "Process and fs")

;; ----------------------------------------------------------------- processes
(if (memq 'unix (features))
    (begin
      (test-begin "Child processes")

      ;; (open-input-process program arg ...) runs the program directly -- no
      ;; shell -- and gives back an input port on its standard output.  The
      ;; ordinary I/O procedures read it, and closing the port waits for the
      ;; child and makes its status available.
      (define echo (open-input-process "echo" "hello"))
      (test #t (input-port? echo))
      (test #t (port? echo))
      (test "hello" (read-line echo))
      (test #t (eof-object? (read-line echo)))
      ;; The child has not been reaped yet, so there is no status.
      (test #f (process-status echo))
      (close-port echo)
      (test 0 (process-status echo))

      ;; A non-zero exit is a status, not an error: nobody raises.
      (define fail (open-input-process "sh" "-c" "exit 3"))
      (close-port fail)
      (test 3 (process-status fail))

      ;; A child killed by a signal reports 128+signal, as system* does.
      (define killed (open-input-process "sh" "-c" "kill -TERM $$"))
      (close-port killed)
      (test 143 (process-status killed))

      ;; A program that cannot be started is a file error.
      (test 'file-error
            (guard (e ((file-error? e) 'file-error))
              (open-input-process "/nonexistent-program-xyz")))

      ;; process-status only understands ports that carry a child.
      (test 'not-a-process
            (guard (e (#t 'not-a-process))
              (process-status (open-input-string "x"))))

      ;; ------------------------------------------------------------ pipeline
      ;; Two children joined by hand: the first's output port feeds the
      ;; second's input port, and no shell is involved.  Closing the output
      ;; port gives the second child end of file on its standard input.
      (define source (open-input-process "echo" "hello pipeline"))
      (define sink (open-output-process "tr" "a-z" "A-Z"))

      (let copy ()
        (let ((line (read-line source)))
          (if (eof-object? line)
              #t
              (begin
                (write-string line sink)
                (newline sink)
                (copy)))))

      (close-output-port sink)
      (close-port source)
      (test 0 (process-status source))
      (test 0 (process-status sink))

      (test-end)))

;; ---------------------------------------------------------------- filesystem
(test-begin "Filesystem")

(define tmp-dir
  (path-join "." (string-append "goscheme-fs-test-"
                                (number->string (current-jiffy)))))

;; Start from a clean slate: this is the same recursive delete the tests use
;; at the end, and it is a no-op when the directory is not there.
(delete-directory-tree tmp-dir)

;; create-directory-tree makes the parents as needed, so one call builds both
;; the temporary directory and its subdirectory.
(define sub-dir (path-join tmp-dir "sub"))
(create-directory-tree sub-dir)
(test #t (file-exists? tmp-dir))
(test #t (file-exists? sub-dir))

(call-with-output-file (path-join tmp-dir "a.txt")
  (lambda (p) (write-string "alpha" p)))
(call-with-output-file (path-join tmp-dir "b.txt")
  (lambda (p) (write-string "bravo" p)))
(call-with-output-file (path-join sub-dir "c.txt")
  (lambda (p) (write-string "charlie" p)))

;; directory-list names the immediate entries, sorted, without full paths.
(test '("a.txt" "b.txt" "sub") (directory-list tmp-dir))
(test '("c.txt") (directory-list sub-dir))

;; glob is sorted by filepath.Glob and returns the empty list for no matches.
(test (list (path-join tmp-dir "a.txt") (path-join tmp-dir "b.txt"))
      (glob (path-join tmp-dir "*.txt")))
(test '() (glob (path-join tmp-dir "*.missing")))
(test 'bad-pattern
      (guard (e ((file-error? e) 'bad-pattern)) (glob "[")))

;; directory-walk visits the root, then every file and directory below it in
;; lexical order.
(define seen '())
(directory-walk tmp-dir (lambda (path) (set! seen (cons path seen))))
(test (list tmp-dir
            (path-join tmp-dir "a.txt")
            (path-join tmp-dir "b.txt")
            sub-dir
            (path-join sub-dir "c.txt"))
      (reverse seen))

;; file-size is exact bytes.
(test 5 (file-size (path-join tmp-dir "a.txt")))
(test 7 (file-size (path-join sub-dir "c.txt")))

;; Missing paths are file errors, not panics.
(test 'no-dir
      (guard (e ((file-error? e) 'no-dir))
        (directory-list (path-join tmp-dir "does-not-exist"))))
(test 'no-file
      (guard (e ((file-error? e) 'no-file))
        (file-size (path-join tmp-dir "does-not-exist"))))
(test 'no-walk
      (guard (e ((file-error? e) 'no-walk))
        (directory-walk (path-join tmp-dir "does-not-exist") (lambda (p) p))))

;; create-directory makes one level and is a no-op when it is already there.
(define one-dir (path-join tmp-dir "one"))
(create-directory one-dir)
(test #t (file-exists? one-dir))
(create-directory one-dir)
(test #t (file-exists? one-dir))
(delete-directory one-dir)
(test #f (file-exists? one-dir))

;; The path helpers are built on path/filepath, so they agree with path-join
;; on every platform.
(test "c" (path-base (path-join "a" "b" "c")))
(test (path-join "a" "b") (path-directory (path-join "a" "b" "c")))
(test ".txt" (path-extension "file.txt"))
(test "" (path-extension "README"))
(test (path-absolute ".") (path-absolute (path-absolute ".")))
(test #t (not (string=? (path-absolute ".") ".")))

;; The recursive delete removes the whole tree.
(delete-directory-tree tmp-dir)
(test #f (file-exists? tmp-dir))

(test-end)

(test-end)
