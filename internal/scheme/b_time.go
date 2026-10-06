// SPDX-License-Identifier: MIT

package scheme

import "time"

// The (goscheme time) library fills in what R7RS-small leaves out: the report
// offers current-second and current-jiffy, whose epochs are deliberately
// unspecified, and nothing else.  Real programs want to sleep, format a
// timestamp, or take a date apart.
//
// Durations are plain numbers of milliseconds, matching the (after ms) clause
// of select, so the same number means the same thing everywhere.

// monotonicOrigin anchors the monotonic clock.  It is read once when the
// package loads, which is close enough to process start.
var monotonicOrigin = time.Now()

func init() { registerInstaller(installTime) }

func installTime(m *Machine) {
	const lib = "(goscheme time)"

	// (sleep ms) suspends the calling interpreter thread.  Zero is legal and
	// returns immediately; a negative duration does nothing.
	// (sleep ms) waits, and Ctrl-C ends the wait.  It is a def rather than a
	// defSimple because being interruptible needs the machine: a primitive that
	// cannot see the cancel channel has to sleep to the end, and "anything can
	// be abandoned with Ctrl-C" is the REPL's promise.
	m.def("sleep", 1, 1, func(m *Machine, a []Value) {
		ms := asFloat(wantReal("sleep", a[0]))
		if ms > 0 && !m.sleepFor(time.Duration(ms*float64(time.Millisecond))) {
			m.RaiseError(interruptedErr())
			return
		}
		m.Return(UnspecifiedValue)
	}, lib)

	// (current-millisecond) is milliseconds since the Unix epoch, UTC.
	m.defSimple("current-millisecond", 0, 0, func(a []Value) (Value, error) {
		return Int(time.Now().UnixMilli()), nil
	}, lib)

	// (monotonic-millisecond) reads the process's monotonic clock.  The origin
	// is arbitrary, so only differences between two readings are meaningful;
	// it never goes backwards when the system clock is adjusted.
	m.defSimple("monotonic-millisecond", 0, 0, func(a []Value) (Value, error) {
		return Int(time.Since(monotonicOrigin).Milliseconds()), nil
	}, lib)

	// (time-format ms format) renders ms in UTC using Go's reference layout,
	// e.g. "2006-01-02 15:04:05" for an ISO-like timestamp.  The layout is
	// Go's, not strftime's; see the time package documentation.
	m.defSimple("time-format", 2, 2, func(a []Value) (Value, error) {
		ms := wantExactInt64("time-format", a[0])
		layout := wantString("time-format", a[1]).Value()
		return NewString(time.UnixMilli(ms).UTC().Format(layout)), nil
	}, lib)

	// (time-parse string format) is the inverse: milliseconds since the epoch,
	// or #f when string does not match format.  Parsing is in UTC, so a
	// format/parse round trip is exact.
	m.defSimple("time-parse", 2, 2, func(a []Value) (Value, error) {
		s := wantString("time-parse", a[0]).Value()
		layout := wantString("time-parse", a[1]).Value()
		t, err := time.ParseInLocation(layout, s, time.UTC)
		if err != nil {
			return False, nil
		}
		return Int(t.UnixMilli()), nil
	}, lib)

	// (time-utc-parts ms) decomposes ms into an association list keyed by
	// symbols, so that a program can compute with the pieces:
	//
	//	((year . 2023) (month . 11) (day . 14) (hour . 22)
	//	 (minute . 13) (second . 20) (weekday . 2))
	//
	// year is the full year; month is 1-12; day is 1-31; hour, minute and
	// second are 0-23, 0-59 and 0-59; weekday is 0 for Sunday through 6 for
	// Saturday (Go's convention).  All values are exact integers.
	m.defSimple("time-utc-parts", 1, 1, func(a []Value) (Value, error) {
		ms := wantExactInt64("time-utc-parts", a[0])
		t := time.UnixMilli(ms).UTC()
		names := [...]string{"year", "month", "day", "hour", "minute", "second", "weekday"}
		nums := [...]int64{
			int64(t.Year()), int64(t.Month()), int64(t.Day()),
			int64(t.Hour()), int64(t.Minute()), int64(t.Second()),
			int64(t.Weekday()),
		}
		items := make([]Value, len(names))
		for i, name := range names {
			items[i] = Cons(Intern(name), Int(nums[i]))
		}
		return List(items...), nil
	}, lib)
}
