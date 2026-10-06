// SPDX-License-Identifier: MIT

package scheme

// The numeric tower — Integer, Rational, Float, Complex and every operation on
// them — is in internal/re; this package dot-imports it, so NumAdd, Float and
// the rest are used here under the names they always had.
//
// What is left in this file is the one thing about the tower that the runtime
// environment cannot express: the interrupt.  A computation long enough to
// notice Ctrl-C has to be able to ask whether it has been interrupted, and the
// answer lives on the machine, so the machine is what answers.  re states the
// question as an interface (re.Interrupts) and a *Machine satisfies it here;
// the arithmetic in re then polls it without ever naming a machine.

// Interrupted reports whether this machine's evaluation has been abandoned.
//
// It is re.Interrupts, which is the whole of what the numeric tower needs from
// the machine: a nil *Machine is a machine nobody is watching, and
// interruptibleNow reads that as "do not bother splitting the multiplication".
func (m *Machine) Interrupted() bool {
	return m != nil && m.cancel != nil && m.cancelled()
}
