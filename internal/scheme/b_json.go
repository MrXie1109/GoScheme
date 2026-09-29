// SPDX-License-Identifier: MIT

package scheme

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// The (goscheme json) library puts Go's encoding/json behind Scheme.  JSON is
// the interchange format of the web, and Go's implementation is strict and
// well tested, so there is no reason to write another parser.
//
// The mapping between the two data models is:
//
//	JSON object      <-> hash table with string keys (equal? equivalence)
//	JSON array       <-> vector
//	JSON string      <-> Scheme string
//	JSON number      <-> exact integer when the literal is integral, else float
//	JSON true/false  <-> #t / #f
//	JSON null        <-> the symbol 'null
//
// The choice of the symbol 'null for JSON null follows Racket's json library;
// a program tests for it with (eq? v 'null).  Integral literals become exact
// integers of arbitrary precision, so a big integer survives a round trip
// instead of being rounded to the nearest float; only literals with a fraction
// or an exponent become floats.  Lists are written as JSON arrays but read back
// as vectors, so an object/array round trip is value-preserving, not
// representation-preserving.

func init() { registerInstaller(installJSON) }

func installJSON(m *Machine) {
	const lib = "(goscheme json)"

	// (json-parse string) parses one JSON value.  A malformed document, or one
	// with trailing data after the value, raises a normal Scheme condition.
	m.defSimple("json-parse", 1, 1, func(a []Value) (Value, error) {
		s := wantString("json-parse", a[0]).Value()
		v, err := jsonDecodeString(s)
		if err != nil {
			return nil, NewError("json-parse: " + err.Error())
		}
		return v, nil
	}, lib)

	// (json-write value) renders value as a JSON document.  Values that have no
	// JSON representation raise a condition that names the offender.
	m.defSimple("json-write", 1, 1, func(a []Value) (Value, error) {
		raw, err := jsonFromScheme(a[0], map[Value]bool{})
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(raw)
		if err != nil {
			return nil, NewError("json-write: " + err.Error())
		}
		return NewString(string(out)), nil
	}, lib)
}

// jsonDecodeString decodes exactly one JSON value and rejects trailing data.
func jsonDecodeString(s string) (Value, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var raw interface{}
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected data after the top-level value")
		}
		return nil, err
	}
	return jsonToScheme(raw)
}

// jsonToScheme converts a decoded Go value into its Scheme representation.
func jsonToScheme(raw interface{}) (Value, error) {
	switch x := raw.(type) {
	case nil:
		return Intern("null"), nil
	case bool:
		return BooleanOf(x), nil
	case string:
		return NewString(x), nil
	case json.Number:
		return jsonNumberToScheme(x.String())
	case []interface{}:
		items := make([]Value, len(x))
		for i, it := range x {
			v, err := jsonToScheme(it)
			if err != nil {
				return nil, err
			}
			items[i] = v
		}
		return NewVectorFrom(items), nil
	case map[string]interface{}:
		h := NewHashtable("equal")
		for k, it := range x {
			v, err := jsonToScheme(it)
			if err != nil {
				return nil, err
			}
			h.set(NewString(k), v)
		}
		return h, nil
	}
	return nil, fmt.Errorf("unsupported JSON value %T", raw)
}

// jsonNumberToScheme keeps an integral literal exact.  A big integer is built
// with math/big rather than rounded through float64, so no precision is lost;
// a literal with a fraction or exponent is a float.
func jsonNumberToScheme(s string) (Value, error) {
	if !strings.ContainsAny(s, ".eE") {
		if b, ok := new(big.Int).SetString(s, 10); ok {
			return BigInt(b), nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, err
	}
	return Float(f), nil
}

// jsonFromScheme converts a Scheme value into the tree encoding/json knows how
// to marshal.  seen guards against cyclic pairs and vectors, which would
// otherwise recurse forever.
func jsonFromScheme(v Value, seen map[Value]bool) (interface{}, error) {
	switch x := v.(type) {
	case Empty:
		return []interface{}{}, nil
	case Boolean:
		return bool(x), nil
	case *String:
		return x.Value(), nil
	case *Symbol:
		if x.Name == "null" {
			return nil, nil
		}
	case *Integer:
		// json.Number is emitted verbatim, so an arbitrary-precision integer
		// keeps all its digits.
		return json.Number(x.String()), nil
	case Float:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, errf("json-write", "cannot write %s as JSON", WriteToString(v))
		}
		return f, nil
	case *Vector:
		if seen[v] {
			return nil, errf("json-write", "cannot write a cyclic vector as JSON")
		}
		seen[v] = true
		defer delete(seen, v)
		out := make([]interface{}, len(x.Items))
		for i, it := range x.Items {
			e, err := jsonFromScheme(it, seen)
			if err != nil {
				return nil, err
			}
			out[i] = e
		}
		return out, nil
	case *Pair:
		if seen[v] {
			return nil, errf("json-write", "cannot write a cyclic list as JSON")
		}
		seen[v] = true
		defer delete(seen, v)
		out := []interface{}{}
		cur := v
		for {
			p, ok := cur.(*Pair)
			if !ok {
				if _, done := cur.(Empty); done {
					break
				}
				return nil, errf("json-write", "cannot write the improper list %s as JSON", WriteToString(v))
			}
			e, err := jsonFromScheme(p.Car, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
			cur = p.Cdr
		}
		return out, nil
	case *Hashtable:
		if seen[v] {
			return nil, errf("json-write", "cannot write a cyclic hash table as JSON")
		}
		seen[v] = true
		defer delete(seen, v)
		keys, vals := x.entries()
		obj := make(map[string]interface{}, len(keys))
		for i := range keys {
			k, ok := keys[i].(*String)
			if !ok {
				return nil, errf("json-write", "object keys must be strings but got %s", WriteToString(keys[i]))
			}
			e, err := jsonFromScheme(vals[i], seen)
			if err != nil {
				return nil, err
			}
			obj[k.Value()] = e
		}
		return obj, nil
	}
	return nil, errf("json-write", "cannot write %s as JSON", WriteToString(v))
}
