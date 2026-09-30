# (goscheme json)

The `(goscheme json)` library converts between JSON documents and Scheme
values for the two things every program that talks to the web needs: reading a
response and writing a request body.  It is a thin layer over Go's
`encoding/json`, so parsing is strict and well tested, and it needs no external
dependencies.

```scheme
(import (scheme base) (goscheme json))
```

## Procedures

### Parsing

| Procedure | Arguments | Description |
|---|---|---|
| `json-parse` | `(json-parse string)` | Parses the single JSON value in string and returns its Scheme form: an object becomes an `equal?` hash table with string keys, an array a vector, a JSON string a Scheme string, `true`/`false` the booleans `#t`/`#f`, `null` the symbol `null`, and a number either an exact integer or an inexact real.  An integral literal — one with no `.`, `e` or `E` — becomes an exact integer of arbitrary precision, so a big integer survives intact; a literal with a fraction or an exponent becomes a float64.  Raises an error when string is not a string, is not valid JSON, or has trailing data after the top-level value. |

### Writing

| Procedure | Arguments | Description |
|---|---|---|
| `json-write` | `(json-write value)` | Renders value as one compact JSON document (no insignificant whitespace) and returns it as a Scheme string.  Accepts a hash table whose keys are all strings as a JSON object, a vector or proper list as an array (the empty list is `[]`), a string, an exact integer (written digit for digit, so arbitrary precision survives a round trip), a finite inexact real, `#t`/`#f`, and the symbol `null`.  Object keys are emitted in sorted order.  Raises an error naming the offender for anything else: another symbol, a character, a procedure, an improper list, a hash table with a non-string key, a non-finite real, or a cyclic vector, list or hash table. |

## Notes

* Objects decode to `equal?` hash tables with string keys, and parsing returns
  fresh mutable tables; JSON member order is not preserved, and the writer
  re-emits keys in sorted order, not insertion order.
* A list writes as a JSON array but reads back as a vector, so an
  object/array round trip preserves JSON values, not the Scheme
  representation.  An association list is **not** accepted as an object: a
  list of pairs is an improper list to the writer and raises.
* Number precision: integral literals stay exact no matter how many digits
  they have; a literal with a fraction or an exponent is parsed as a float64,
  so precision beyond a double is lost.  Because JSON has no NaN or infinity,
  the writer rejects `+nan.0` and `+inf.0`.
* Parsing requires exactly one top-level value; a second value or any
  garbage after it raises.  Errors are ordinary Scheme conditions and can be
  caught with `guard`.

## Example

```scheme
(import (scheme base) (scheme write) (goscheme json))

(define doc
  (json-parse "{\"name\":\"Ada\",\"born\":18151210,\"tags\":[\"math\",null,true]}"))

(display (hash-table-ref doc "name")) (newline)            ; => Ada
(write (hash-table-ref doc "born")) (newline)              ; => 18151210, an exact integer
(write (vector-ref (hash-table-ref doc "tags") 1)) (newline) ; => null

;; A vector and a proper list both write as JSON arrays, and the writer
;; sorts object keys.
(write (json-write (vector 1 'null #f "x"))) (newline)     ; => "[1,null,false,\"x\"]"
```
