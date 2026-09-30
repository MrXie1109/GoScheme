# (goscheme time)

The `(goscheme time)` library fills in what R7RS-small leaves out: it sleeps,
reads the wall clock and a monotonic clock, and formats, parses and decomposes
timestamps.  It is built on Go's `time` package, durations are plain numbers of
milliseconds — the same unit `(after ms)` takes — and timestamps are exact
integer milliseconds since the Unix epoch in UTC.

```scheme
(import (scheme base) (goscheme time))
```

## Procedures

### Clocks and sleeping

| Procedure | Arguments | Description |
|---|---|---|
| `current-millisecond` | `(current-millisecond)` | Returns the current wall-clock time as an exact integer number of milliseconds since the Unix epoch (UTC).  It reads the system clock, so it can move backwards if the clock is adjusted. |
| `monotonic-millisecond` | `(monotonic-millisecond)` | Returns an exact integer number of milliseconds from a monotonic clock whose origin is arbitrary (fixed when the process starts), so only differences between two readings are meaningful.  It never goes backwards when the system clock is adjusted, which makes it the right clock for measuring intervals. |
| `sleep` | `(sleep ms)` | Suspends the calling interpreter thread for ms milliseconds.  ms may be exact or inexact, and a fractional part is honored at nanosecond resolution.  Zero returns immediately and a negative value does nothing.  Returns an unspecified value. |

### Formatting and parsing

| Procedure | Arguments | Description |
|---|---|---|
| `time-format` | `(time-format ms layout)` | Renders ms (an exact integer of milliseconds since the Unix epoch) as a string in UTC, using Go's reference layout — not strftime.  Raises an error if ms is not an exact integer. |
| `time-parse` | `(time-parse string layout)` | The inverse: parses string with the same Go reference layout in UTC and returns an exact integer of milliseconds since the epoch, or `#f` if string does not match layout.  An explicit zone offset in string is honored. |

### Calendar decomposition

| Procedure | Arguments | Description |
|---|---|---|
| `time-utc-parts` | `(time-utc-parts ms)` | Decomposes ms (an exact integer) in UTC and returns an association list of seven `(symbol . exact-integer)` pairs in the order year, month, day, hour, minute, second, weekday.  year is the full year; month is 1–12; day is 1–31; hour is 0–23; minute and second are 0–59; weekday is 0 for Sunday through 6 for Saturday, Go's convention.  Raises an error if ms is not an exact integer. |

## Notes

* The unit everywhere is the millisecond, matching the `(after ms)` clause of
  `select`, so one number means the same thing across the library.
* The layout is Go's reference layout, whose example instant is
  `Mon Jan 2 15:04:05 MST 2006`; `"2006-01-02 15:04:05"` is the ISO-like
  spelling.  It is not strftime, so `%Y` and friends are ordinary literal
  text, and a `.000` component adds fractional seconds.
* Formatting and calendar decomposition demand an exact integer, so `1.0`
  raises, while sleeping accepts any real.  The finest unit these procedures
  expose is the millisecond; a parse of a finer layout is truncated to
  milliseconds.
* Parsing returns `#f` on a mismatch instead of raising.  Fields the
  layout does not mention take Go's zero values — year 0, January 1,
  00:00:00 UTC — so a time-only layout yields a large negative number of
  milliseconds, and dates before 1970 are negative.
* The wall clock can jump; use the monotonic clock to time an interval.

## Example

```scheme
(import (scheme base) (scheme write) (goscheme time))

;; Deterministic: parse a fixed instant, then format and decompose it.
(define ms (time-parse "2023-11-14 22:13:20" "2006-01-02 15:04:05"))

(write ms)                                                  ; => 1700000000000
(write (time-format ms "2006-01-02T15:04:05Z"))             ; => "2023-11-14T22:13:20Z"
(write (time-utc-parts ms))                                 ; => ((year . 2023) (month . 11) (day . 14) (hour . 22) (minute . 13) (second . 20) (weekday . 2))
```
