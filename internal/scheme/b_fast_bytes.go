// SPDX-License-Identifier: MIT

package scheme

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	. "github.com/MrXie1109/GoScheme/internal/re"
	"hash/crc32"
	mathrand "math/rand/v2"
	"strings"
)

// Hashing, encodings and randomness, all of them a Go call away.
//
// The byte procedures are the batch lane for binary data: bytes-xor, bytes-and,
// bytes-or and bytes-not walk two bytevectors in one Go loop, which is what a
// checksum, a mask or a simple cipher needs.

// bytesOf accepts a string or a bytevector as binary data.
func bytesOf(name string, v Value) []byte {
	switch x := v.(type) {
	case *String:
		return []byte(x.Value())
	case *Bytevector:
		return x.Bytes
	}
	panic(errf(name, "expected a string or a bytevector but got %s", WriteToString(v)))
}

func hexOf(sum []byte) Value { return NewString(hex.EncodeToString(sum)) }

func installFastHashing(m *Machine, lib string) {
	// (sha256 data), (sha1 data), (sha512 data) and (md5 data) hash a string or
	// a bytevector and return lowercase hex.  md5 and sha1 are here for
	// protocols that still ask for them, not for new designs.
	m.defSimple("sha1", 1, 1, func(a []Value) (Value, error) {
		sum := sha1.Sum(bytesOf("sha1", a[0]))
		return hexOf(sum[:]), nil
	}, lib)

	m.defSimple("sha512", 1, 1, func(a []Value) (Value, error) {
		sum := sha512.Sum512(bytesOf("sha512", a[0]))
		return hexOf(sum[:]), nil
	}, lib)

	m.defSimple("md5", 1, 1, func(a []Value) (Value, error) {
		sum := md5.Sum(bytesOf("md5", a[0]))
		return hexOf(sum[:]), nil
	}, lib)

	// (crc32 data) is the IEEE checksum as an exact integer.
	m.defSimple("crc32", 1, 1, func(a []Value) (Value, error) {
		return Int(int64(crc32.ChecksumIEEE(bytesOf("crc32", a[0])))), nil
	}, lib)

	// (hmac-sha256 key message) is the keyed hash, as lowercase hex.
	m.defSimple("hmac-sha256", 2, 2, func(a []Value) (Value, error) {
		mac := hmac.New(sha256.New, bytesOf("hmac-sha256", a[0]))
		mac.Write(bytesOf("hmac-sha256", a[1]))
		return hexOf(mac.Sum(nil)), nil
	}, lib)
}

func installFastEncoding(m *Machine, lib string) {
	m.defSimple("base32-encode", 1, 1, func(a []Value) (Value, error) {
		return NewString(base32.StdEncoding.EncodeToString(wantBytevector("base32-encode", a[0]).Bytes)), nil
	}, lib)

	m.defSimple("base32-decode", 1, 1, func(a []Value) (Value, error) {
		s := strings.ToUpper(wantString("base32-decode", a[0]).Value())
		buf, err := base32.StdEncoding.DecodeString(s)
		if err != nil {
			panic(errf("base32-decode", "%s", err.Error()))
		}
		return NewBytevectorFrom(buf), nil
	}, lib)

	// The URL-safe base64 alphabet, which is what a token or a query parameter
	// wants.  Decoding accepts the padded and the raw form.
	m.defSimple("base64url-encode", 1, 1, func(a []Value) (Value, error) {
		return NewString(base64.URLEncoding.EncodeToString(wantBytevector("base64url-encode", a[0]).Bytes)), nil
	}, lib)

	m.defSimple("base64url-decode", 1, 1, func(a []Value) (Value, error) {
		s := wantString("base64url-decode", a[0]).Value()
		buf, err := base64.URLEncoding.DecodeString(s)
		if err != nil {
			if buf, err = base64.RawURLEncoding.DecodeString(s); err != nil {
				panic(errf("base64url-decode", "%s", err.Error()))
			}
		}
		return NewBytevectorFrom(buf), nil
	}, lib)

	// (bytes-xor a b) XORs a with b, cycling b when it is shorter: a one-byte b
	// is the mask, a long one is the key.
	m.defSimple("bytes-xor", 2, 2, func(a []Value) (Value, error) {
		x := wantBytevector("bytes-xor", a[0]).Bytes
		y := wantBytevector("bytes-xor", a[1]).Bytes
		if len(y) == 0 {
			panic(errf("bytes-xor", "the mask must not be empty"))
		}
		out := make([]byte, len(x))
		for i := range x {
			out[i] = x[i] ^ y[i%len(y)]
		}
		return NewBytevectorFrom(out), nil
	}, lib)

	// The bitwise batch: bytes-and, bytes-or and bytes-not need equal lengths.
	m.defSimple("bytes-and", 2, 2, func(a []Value) (Value, error) {
		x, y := zipBytes("bytes-and", a[0], a[1])
		out := make([]byte, len(x))
		for i := range x {
			out[i] = x[i] & y[i]
		}
		return NewBytevectorFrom(out), nil
	}, lib)

	m.defSimple("bytes-or", 2, 2, func(a []Value) (Value, error) {
		x, y := zipBytes("bytes-or", a[0], a[1])
		out := make([]byte, len(x))
		for i := range x {
			out[i] = x[i] | y[i]
		}
		return NewBytevectorFrom(out), nil
	}, lib)

	m.defSimple("bytes-not", 1, 1, func(a []Value) (Value, error) {
		x := wantBytevector("bytes-not", a[0]).Bytes
		out := make([]byte, len(x))
		for i := range x {
			out[i] = ^x[i]
		}
		return NewBytevectorFrom(out), nil
	}, lib)

	m.defSimple("bytes-reverse", 1, 1, func(a []Value) (Value, error) {
		x := wantBytevector("bytes-reverse", a[0]).Bytes
		out := make([]byte, len(x))
		for i, b := range x {
			out[len(x)-1-i] = b
		}
		return NewBytevectorFrom(out), nil
	}, lib)

	// (bytes-index bytevector subsequence) is the first index where the
	// subsequence starts, or #f.
	m.defSimple("bytes-index", 2, 2, func(a []Value) (Value, error) {
		x := wantBytevector("bytes-index", a[0]).Bytes
		sub := wantBytevector("bytes-index", a[1]).Bytes
		if len(sub) == 0 {
			return Int(0), nil
		}
		for i := 0; i+len(sub) <= len(x); i++ {
			match := true
			for j := range sub {
				if x[i+j] != sub[j] {
					match = false
					break
				}
			}
			if match {
				return Int(int64(i)), nil
			}
		}
		return False, nil
	}, lib)

	// (bytevector-fill! bytevector byte [start [end]]) writes one byte through
	// a range, in place.
	m.defSimple("bytevector-fill!", 2, 4, func(a []Value) (Value, error) {
		bv := wantBytevector("bytevector-fill!", a[0])
		b := byte(wantU8("bytevector-fill!", a[1]))
		start, end := 0, len(bv.Bytes)
		if len(a) > 2 {
			start = wantIndexIn("bytevector-fill!", a[2])
		}
		if len(a) > 3 {
			end = wantIndexIn("bytevector-fill!", a[3])
		}
		if start > end || end > len(bv.Bytes) {
			panic(errf("bytevector-fill!", "the range is out of bounds"))
		}
		for i := start; i < end; i++ {
			bv.Bytes[i] = b
		}
		return bv, nil
	}, lib)

	// Conversions between bytevectors and vectors of exact integers, which is
	// how a program hands binary data to the vector batch lane.
	m.defSimple("vector->bytevector", 1, 1, func(a []Value) (Value, error) {
		vec := wantVector("vector->bytevector", a[0])
		out := make([]byte, len(vec.Items))
		for i, v := range vec.Items {
			out[i] = byte(wantU8("vector->bytevector", v))
		}
		return NewBytevectorFrom(out), nil
	}, lib)

	m.defSimple("bytevector->vector", 1, 1, func(a []Value) (Value, error) {
		bv := wantBytevector("bytevector->vector", a[0])
		out := make([]Value, len(bv.Bytes))
		for i, b := range bv.Bytes {
			out[i] = Int(int64(b))
		}
		return &Vector{Items: out}, nil
	}, lib)
}

// zipBytes checks that two bytevectors have the same length.
func zipBytes(name string, a, b Value) ([]byte, []byte) {
	x := wantBytevector(name, a).Bytes
	y := wantBytevector(name, b).Bytes
	if len(x) != len(y) {
		panic(errf(name, "bytevectors of different lengths"))
	}
	return x, y
}

func installFastRandom(m *Machine, lib string) {
	// (random-int n) is uniform in [0, n), (random-float) is uniform in [0, 1)
	// and (random-choice seq) picks one element.
	m.defSimple("random-int", 1, 1, func(a []Value) (Value, error) {
		n := wantIndex("random-int", a[0])
		if n < 1 {
			panic(errf("random-int", "the bound must be at least 1"))
		}
		return Int(int64(mathrand.IntN(n))), nil
	}, lib)

	m.defSimple("random-float", 0, 0, func(a []Value) (Value, error) {
		return Float(mathrand.Float64()), nil
	}, lib)

	m.defSimple("random-choice", 1, 1, func(a []Value) (Value, error) {
		items := seqSlice("random-choice", a[0])
		if len(items) == 0 {
			panic(errf("random-choice", "expected a non-empty sequence"))
		}
		return items[mathrand.IntN(len(items))], nil
	}, lib)

	// (random-string length [alphabet]) is a uniform string over the alphabet,
	// which defaults to the alphanumerics.
	m.defSimple("random-string", 1, 2, func(a []Value) (Value, error) {
		n := wantIndex("random-string", a[0])
		alphabet := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		if len(a) == 2 {
			alphabet = wantString("random-string", a[1]).Value()
		}
		if alphabet == "" {
			panic(errf("random-string", "the alphabet must not be empty"))
		}
		letters := []rune(alphabet)
		out := make([]rune, n)
		for i := range out {
			out[i] = letters[mathrand.IntN(len(letters))]
		}
		return NewStringFromRunes(out), nil
	}, lib)

	// (shuffle list) is a new list in random order; (vector-shuffle! vector)
	// shuffles in place, and (vector-sample vector n) takes n distinct
	// elements at random.
	m.defSimple("shuffle", 1, 1, func(a []Value) (Value, error) {
		items, ok := ListToSlice(a[0])
		if !ok {
			panic(errf("shuffle", "expected a proper list but got %s", WriteToString(a[0])))
		}
		out := append([]Value(nil), items...)
		mathrand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return listOf(out), nil
	}, lib)

	m.defSimple("vector-shuffle!", 1, 1, func(a []Value) (Value, error) {
		vec := wantVector("vector-shuffle!", a[0])
		mathrand.Shuffle(len(vec.Items), func(i, j int) {
			vec.Items[i], vec.Items[j] = vec.Items[j], vec.Items[i]
		})
		return vec, nil
	}, lib)

	m.defSimple("vector-sample", 2, 2, func(a []Value) (Value, error) {
		vec := wantVector("vector-sample", a[0])
		n := wantIndex("vector-sample", a[1])
		if n > len(vec.Items) {
			panic(errf("vector-sample", "asked for %d of only %d elements", n, len(vec.Items)))
		}
		perm := mathrand.Perm(len(vec.Items))
		out := make([]Value, n)
		for i := 0; i < n; i++ {
			out[i] = vec.Items[perm[i]]
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (uuid) is a random version-4 UUID in the usual textual form.
	m.defSimple("uuid", 0, 0, func(a []Value) (Value, error) {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic(errf("uuid", "%s", err.Error()))
		}
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		return NewString(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])), nil
	}, lib)
}
