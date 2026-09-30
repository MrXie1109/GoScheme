# (goscheme fs)

The (goscheme fs) library supplies the filesystem vocabulary that R7RS-small
leaves out: globbing, listing and walking directories, creating and deleting
directories, splitting and joining paths, and reading a file's size. It is a
thin, path-portable layer over Go's os and path/filepath packages, so every
failure arrives as an ordinary Scheme file condition rather than a Go panic.
Import it alongside (scheme base):

```scheme
(import (scheme base) (goscheme fs))
```

## Procedures

### Listing and matching

| Procedure | Arguments | Description |
|---|---|---|
| `glob` | `(glob pattern)` | Returns a list of path strings matching the shell-style string pattern: `*` and `?` match within one path element, `[...]` is a character class, and `*` does not cross a directory separator. Matches are sorted lexically, a pattern that matches nothing yields the empty list, and a malformed pattern raises a file error. Results keep the form of the pattern, so a relative pattern yields paths relative to the current working directory. |
| `directory-list` | `(directory-list dir)` | Returns a list of strings naming the entries directly inside the directory named by the string dir, sorted by name. The strings are entry names only, not full paths, and subdirectories are not descended into; a missing path or a path that is not a directory raises a file error. |

### Walking and metadata

| Procedure | Arguments | Description |
|---|---|---|
| `directory-walk` | `(directory-walk dir proc)` | Walks the tree rooted at the string dir in depth-first lexical order and calls proc once for each path: first for dir itself, then for every entry, descending into a subdirectory before moving to the next sibling, and reporting directories as well as files. proc receives one argument, the path as a string (dir joined with the relative names, so a relative dir yields relative paths), and its return value is ignored. Returns unspecified after the last call; a missing or unreadable dir raises a file error before the first call. |
| `file-size` | `(file-size path)` | Returns the size in bytes of the file named by path as an exact integer, using os.Stat, so symbolic links are followed. A missing path or any other stat failure raises a file error; for a directory the result is the platform's reported metadata size, not the total size of its contents. |

### Creating and deleting directories

| Procedure | Arguments | Description |
|---|---|---|
| `create-directory` | `(create-directory path)` | Creates the single directory named path with permission bits 0777 reduced by the process umask, and returns unspecified. If anything already exists at path — a directory, and also a regular file — it is silently a no-op; a missing parent directory raises a file error. |
| `create-directory-tree` | `(create-directory-tree path)` | Creates path together with any missing parents, like mkdir -p, using permission bits 0777 reduced by the umask, and returns unspecified; existing directories are left alone. A creation failure raises a file error. |
| `delete-directory` | `(delete-directory path)` | Removes the empty directory named path (or a regular file) with os.Remove and returns unspecified. It is not recursive: a non-empty directory fails with a file error, and so does a missing path. |
| `delete-directory-tree` | `(delete-directory-tree path)` | Recursively removes path and everything below it and returns unspecified. A path that does not exist counts as already gone and is not an error, so the procedure is safe to call for cleanup; a real removal failure raises a file error. |

### Path manipulation

| Procedure | Arguments | Description |
|---|---|---|
| `path-join` | `(path-join part ...)` | Joins one or more path strings with the platform's separator and cleans the result (repeated separators collapsed and `.`/`..` resolved where possible), returning the joined string; it never touches the filesystem. Empty arguments are ignored, and a single argument is returned cleaned. |
| `path-directory` | `(path-directory path)` | Returns the cleaned directory portion of the string path using the platform's separator; a bare name such as "a.txt" yields ".". This is a string operation only. |
| `path-base` | `(path-base path)` | Returns the last element of the string path after trailing separators are dropped, so a directory path yields that directory's own name and the root "/" yields "/". This is a string operation only. |
| `path-extension` | `(path-extension path)` | Returns the suffix of the string path beginning at its final dot, dot included, or the empty string when the final element has no dot; "a.tar.gz" yields ".gz" while ".bashrc" yields ".bashrc". This is a string operation only. |
| `path-absolute` | `(path-absolute path)` | Returns an absolute, cleaned version of the string path by resolving it against the process's current working directory; the path need not exist and is not stat'ed. It raises a file error only when the working directory cannot be determined. |

## Notes

* Every argument must be a string — directory-walk additionally requires a
  procedure — and a wrong type raises an ordinary error rather than a file
  condition. Operating-system failures are raised as file errors, so the
  file-error? predicate recognizes them; the error message names the procedure
  that failed and the path it was given.
* Paths follow Go's path/filepath rules. On Unix the separator is a forward
  slash; on Windows both slash and backslash are accepted and results use
  backslash. Build paths with path-join and take them apart with the other path
  procedures instead of hard-coding a separator, so the program stays portable.
* The path helpers (path-join, path-directory, path-base, path-extension) are
  pure string operations that never read the filesystem, so they work on paths
  that do not exist. path-absolute only consults the current working directory,
  while glob and directory-walk are the ways to discover which paths exist.
* glob does not recurse: a pattern such as "dir/*/*" covers exactly two levels,
  and directory-walk is the way to visit a whole tree. Neither glob nor
  directory-list expands a leading tilde or environment variables.
* create-directory stats its argument before creating, so it silently succeeds
  when the name is already taken by a regular file. delete-directory uses
  os.Remove, which removes a regular file as well as an empty directory; use
  delete-directory-tree when a non-empty tree must go.
* directory-walk collects the whole path list before calling the procedure, so a
  procedure that creates or deletes files during the walk does not change what
  is visited; the trade-off is that a walk over a huge tree builds a large list
  before the first callback runs.
* Directories are created with mode 0777, restricted by the process umask, so
  the effective mode is usually 0755; on Windows the permission bits are mostly
  ignored.

## Example

The example creates everything under a temporary directory and removes it again
at the end, so it writes nothing into the source tree.

```scheme
(import (scheme base) (scheme write) (scheme file) (goscheme fs))

;; Everything happens under a temporary directory, never the source tree.
(define tmp (or (get-environment-variable "TMPDIR")
                (get-environment-variable "TEMP")
                (get-environment-variable "TMP")
                "/tmp"))
(define root (path-join tmp "gs-fs-doc-example"))

(delete-directory-tree root)              ; start clean; missing is a no-op
(create-directory-tree (path-join root "notes" "drafts"))

(call-with-output-file (path-join root "notes" "a.txt")
  (lambda (out) (write-string "hello" out)))
(call-with-output-file (path-join root "notes" "drafts" "b.txt")
  (lambda (out) (write-string "world!" out)))

(display "directory-list: ")
(write (directory-list (path-join root "notes")))       ; names, sorted
(newline)                                               ; => ("a.txt" "drafts")

(display "glob: ")
(write (glob (path-join root "notes" "*.txt")))          ; path strings
(newline)                    ; => one list of the single matched .txt path

(display "walk: ")
(directory-walk root
  (lambda (p) (display (path-base p)) (display " ")))
(newline)          ; => gs-fs-doc-example notes a.txt drafts b.txt

(display "size of a.txt: ")
(write (file-size (path-join root "notes" "a.txt")))     ; exact bytes
(newline)                                               ; => 5

(display "extension: ")
(write (path-extension "archive.tar.gz"))                ; => ".gz"
(newline)

(delete-directory-tree root)              ; remove the whole tree again
(display "cleaned up")
(newline)
```
