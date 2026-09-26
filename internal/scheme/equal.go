package scheme

import "math"

// Eq is the eq? predicate.
func Eq(a, b Value) bool {
	switch x := a.(type) {
	case Boolean:
		y, ok := b.(Boolean)
		return ok && x == y
	case *Symbol:
		return a == b
	case Char:
		y, ok := b.(Char)
		return ok && x == y
	case Empty:
		_, ok := b.(Empty)
		return ok
	case EOF:
		_, ok := b.(EOF)
		return ok
	case Unspecified:
		_, ok := b.(Unspecified)
		return ok
	}
	// Numbers are compared numerically by eq? in most implementations, but
	// the report leaves it unspecified; use eqv? semantics for consistency.
	if IsNumber(a) && IsNumber(b) {
		return Eqv(a, b)
	}
	return a == b
}

// Eqv is the eqv? predicate.
func Eqv(a, b Value) bool {
	switch x := a.(type) {
	case Boolean:
		y, ok := b.(Boolean)
		return ok && x == y
	case *Symbol:
		return a == b
	case Char:
		y, ok := b.(Char)
		return ok && x == y
	case Empty:
		_, ok := b.(Empty)
		return ok
	case EOF:
		_, ok := b.(EOF)
		return ok
	case Unspecified:
		_, ok := b.(Unspecified)
		return ok
	case *Integer:
		y, ok := b.(*Integer)
		return ok && x.String() == y.String()
	case *Rational:
		y, ok := b.(*Rational)
		return ok && x.R.Cmp(y.R) == 0
	case Float:
		y, ok := b.(Float)
		if !ok {
			return false
		}
		if math.IsNaN(float64(x)) || math.IsNaN(float64(y)) {
			return false
		}
		return math.Float64bits(float64(x)) == math.Float64bits(float64(y))
	case *Complex:
		y, ok := b.(*Complex)
		return ok && Eqv(x.Re, y.Re) && Eqv(x.Im, y.Im)
	}
	if _, ok := b.(*Integer); ok {
		return false
	}
	if _, ok := b.(*Rational); ok {
		return false
	}
	if _, ok := b.(Float); ok {
		return false
	}
	if _, ok := b.(*Complex); ok {
		return false
	}
	return a == b
}

// Equal is the equal? predicate.  It terminates on cyclic structures.
func Equal(a, b Value) bool {
	return equalRec(a, b, map[[2]Value]bool{})
}

func equalRec(a, b Value, seen map[[2]Value]bool) bool {
	switch x := a.(type) {
	case *Pair:
		y, ok := b.(*Pair)
		if !ok {
			return false
		}
		key := [2]Value{x, y}
		if seen[key] {
			return true
		}
		seen[key] = true
		return equalRec(x.Car, y.Car, seen) && equalRec(x.Cdr, y.Cdr, seen)
	case *Vector:
		y, ok := b.(*Vector)
		if !ok || len(x.Items) != len(y.Items) {
			return false
		}
		key := [2]Value{x, y}
		if seen[key] {
			return true
		}
		seen[key] = true
		for i := range x.Items {
			if !equalRec(x.Items[i], y.Items[i], seen) {
				return false
			}
		}
		return true
	case *String:
		y, ok := b.(*String)
		if !ok {
			return false
		}
		if len(x.Runes) != len(y.Runes) {
			return false
		}
		for i := range x.Runes {
			if x.Runes[i] != y.Runes[i] {
				return false
			}
		}
		return true
	case *Bytevector:
		y, ok := b.(*Bytevector)
		if !ok || len(x.Bytes) != len(y.Bytes) {
			return false
		}
		for i := range x.Bytes {
			if x.Bytes[i] != y.Bytes[i] {
				return false
			}
		}
		return true
	case *Record:
		y, ok := b.(*Record)
		if !ok || x.Type != y.Type {
			return false
		}
		for i := range x.Fields {
			if !equalRec(x.Fields[i], y.Fields[i], seen) {
				return false
			}
		}
		return true
	case *ErrorObject:
		y, ok := b.(*ErrorObject)
		if !ok {
			return false
		}
		if x.Message != y.Message || len(x.Irritants) != len(y.Irritants) {
			return false
		}
		for i := range x.Irritants {
			if !equalRec(x.Irritants[i], y.Irritants[i], seen) {
				return false
			}
		}
		return true
	}
	if IsNumber(a) && IsNumber(b) {
		return Eqv(a, b)
	}
	return a == b
}
