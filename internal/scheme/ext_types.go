// SPDX-License-Identifier: MIT

package scheme

import (
	"fmt"
	"sync"

	. "github.com/MrXie1109/GoScheme/internal/re"
)

// ---------------------------------------------------------------------------
// The extension objects
//
// These are declared here, next to the procedures that use them, and not in
// internal/re: every one of them is read through its unexported fields by those
// procedures (c.mu, x.method, h.keys ...), and Go allows that only inside the
// package that declares the type.  They are still values the printer has to be
// able to render, which is why each carries the one method below.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Hashtables (extension; not part of R7RS-small but widely used)
// ---------------------------------------------------------------------------

// Channel is a Go channel exposed to Scheme; see b_concurrent.go.
type Channel struct {
	Name     string
	capacity int
	ch       chan Value
	mu       sync.Mutex
	closed   bool
}

// SchemeDescribe prints the channel; see re.Describer.
func (c *Channel) SchemeDescribe(Printer) string {
	state := "open"
	if c.isClosed() {
		state = "closed"
	}
	return fmt.Sprintf("#<channel cap=%d %s>", c.capacity, state)
}

// Hashtable is a hash table keyed by Scheme values.  It is an extension:
// see b_hashtable.go.
type Hashtable struct {
	// Kind is "eq", "eqv" or "equal" and selects the key equivalence.
	//
	// Every table this implementation makes is mutable, so there is no
	// mutability flag: one would always be true, and a field that cannot vary
	// reads like a distinction the language makes when it does not.
	Kind  string
	keys  []Value
	vals  []Value
	dead  []bool
	index map[interface{}][]int
	count int
}

// SchemeDescribe prints the table; see re.Describer.
func (h *Hashtable) SchemeDescribe(Printer) string { return "#<hashtable>" }
