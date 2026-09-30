// SPDX-License-Identifier: MIT

package scheme

import (
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// The numeric tower
//
//	Integer   exact integer, small (int64) with automatic promotion to big.Int
//	Rational  exact rational (big.Rat, always in lowest terms, never integral)
//	Float     inexact real (float64)
//	Complex   complex number whose parts are any of the above
// ---------------------------------------------------------------------------

// Integer is an exact integer.
type Integer struct {
	small bool
	i     int64
	b     *big.Int // valid when !small
}

// Rational is an exact non-integral rational number.
type Rational struct {
	R *big.Rat
}

// Float is an inexact real number.
type Float float64

// Complex is a complex number.
type Complex struct {
	Re Value
	Im Value
}

// Int builds an exact integer from an int64.
func Int(i int64) *Integer { return &Integer{small: true, i: i} }

// BigInt builds an exact integer from a big.Int, normalising to a small
// integer when the value fits.
func BigInt(b *big.Int) *Integer {
	if b.IsInt64() {
		return Int(b.Int64())
	}
	return &Integer{b: new(big.Int).Set(b)}
}

// Big returns the value of x as a big.Int (a fresh copy for small integers).
func (x *Integer) Big() *big.Int {
	if x.small {
		return big.NewInt(x.i)
	}
	return x.b
}

// Int64 returns the value and whether it fits in an int64.
func (x *Integer) Int64() (int64, bool) {
	if x.small {
		return x.i, true
	}
	if x.b.IsInt64() {
		return x.b.Int64(), true
	}
	return 0, false
}

// Float64 returns the value converted to a float64.
func (x *Integer) Float64() float64 {
	if x.small {
		return float64(x.i)
	}
	f, _ := new(big.Float).SetInt(x.b).Float64()
	return f
}

// Sign returns -1, 0 or 1.
func (x *Integer) Sign() int {
	if x.small {
		switch {
		case x.i < 0:
			return -1
		case x.i > 0:
			return 1
		}
		return 0
	}
	return x.b.Sign()
}

// IsZero reports whether x is zero.
func (x *Integer) IsZero() bool {
	if x.small {
		return x.i == 0
	}
	return x.b.Sign() == 0
}

// String renders the integer in base 10.
func (x *Integer) String() string {
	if x.small {
		return strconv.FormatInt(x.i, 10)
	}
	return x.b.String()
}

// IsExact reports whether v is an exact number.
func IsExact(v Value) bool {
	switch v.(type) {
	case *Integer, *Rational:
		return true
	case *Complex:
		c := v.(*Complex)
		return IsExact(c.Re) && IsExact(c.Im)
	}
	return false
}

// IsInexact reports whether v is an inexact number.
func IsInexact(v Value) bool {
	switch v.(type) {
	case Float:
		return true
	case *Complex:
		c := v.(*Complex)
		return IsInexact(c.Re) || IsInexact(c.Im)
	}
	return false
}

// IsNumber reports whether v is a number.
func IsNumber(v Value) bool {
	switch v.(type) {
	case *Integer, *Rational, Float, *Complex:
		return true
	}
	return false
}

func isExactZero(v Value) bool {
	switch x := v.(type) {
	case *Integer:
		return x.IsZero()
	case *Rational:
		return x.R.Sign() == 0
	}
	return false
}

// IsReal reports whether v is a real number in the sense of R7RS: complex
// numbers whose imaginary part is an exact zero are real.
func IsReal(v Value) bool {
	if c, ok := v.(*Complex); ok {
		return isExactZero(c.Im) && IsNumber(c.Re)
	}
	return IsNumber(v)
}

// RealPart returns the real component of a real number (v must be real).
func RealPart(v Value) Value {
	if c, ok := v.(*Complex); ok {
		return c.Re
	}
	return v
}

// ToBigRat converts an exact number to a big.Rat; ok is false for inexact or
// complex values.
func ToBigRat(v Value) (*big.Rat, bool) {
	switch x := v.(type) {
	case *Integer:
		return new(big.Rat).SetInt(x.Big()), true
	case *Rational:
		return new(big.Rat).Set(x.R), true
	}
	return nil, false
}

// normRat converts a big.Rat into the canonical exact representation.
func normRat(r *big.Rat) Value {
	if r.IsInt() {
		return BigInt(r.Num())
	}
	return &Rational{R: r}
}

// ExactToValue converts a big.Rat into the canonical exact representation.
func ExactToValue(r *big.Rat) Value { return normRat(r) }

// ---------------------------------------------------------------- predicates

// IsIntegerVal reports whether v is an integer (exact or inexact).
func IsIntegerVal(v Value) bool {
	switch x := v.(type) {
	case *Integer, *Rational:
		if r, ok := x.(*Rational); ok {
			return r.R.IsInt()
		}
		return true
	case Float:
		f := float64(x)
		return !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f)
	case *Complex:
		return isExactZero(x.Im) && IsIntegerVal(x.Re)
	}
	return false
}

// IsRationalVal reports whether v is a rational (exact or inexact real).
func IsRationalVal(v Value) bool {
	switch x := v.(type) {
	case *Integer, *Rational:
		return true
	case Float:
		f := float64(x)
		return !math.IsInf(f, 0) && !math.IsNaN(f)
	case *Complex:
		return isExactZero(x.Im) && IsRationalVal(x.Re)
	}
	return false
}

// IsExactInteger reports whether v is an exact integer.
func IsExactInteger(v Value) bool {
	_, ok := v.(*Integer)
	return ok
}

// ------------------------------------------------------------------ utilities

// asFloat converts a real number to float64.
func asFloat(v Value) float64 {
	switch x := RealPart(v).(type) {
	case *Integer:
		return x.Float64()
	case *Rational:
		f, _ := x.R.Float64()
		return f
	case Float:
		return float64(x)
	}
	panic("asFloat: not a real number")
}

// negInt negates a small integer.
func negInt(i int64) *Integer {
	if i == math.MinInt64 {
		return BigInt(new(big.Int).Neg(big.NewInt(i)))
	}
	return Int(-i)
}

func addInt(a, b *Integer) *Integer {
	if a.small && b.small {
		s := a.i + b.i
		if (s > a.i) == (b.i > 0) {
			return Int(s)
		}
	}
	return BigInt(new(big.Int).Add(a.Big(), b.Big()))
}

func subInt(a, b *Integer) *Integer {
	if a.small && b.small {
		d := a.i - b.i
		if (d < a.i) == (b.i > 0) {
			return Int(d)
		}
	}
	return BigInt(new(big.Int).Sub(a.Big(), b.Big()))
}

func mulInt(a, b *Integer) *Integer {
	if a.small && b.small {
		if p, ok := mul64(a.i, b.i); ok {
			return Int(p)
		}
	}
	return BigInt(new(big.Int).Mul(a.Big(), b.Big()))
}

func mul64(x, y int64) (int64, bool) {
	neg := false
	ux, uy := uint64(x), uint64(y)
	if x < 0 {
		neg = !neg
		ux = uint64(-x)
	}
	if y < 0 {
		neg = !neg
		uy = uint64(-y)
	}
	hi, lo := bits.Mul64(ux, uy)
	if hi != 0 {
		return 0, false
	}
	if neg {
		if lo > 1<<63 {
			return 0, false
		}
		return -int64(lo), true
	}
	if lo > math.MaxInt64 {
		return 0, false
	}
	return int64(lo), true
}

// ------------------------------------------------------------------ generic ops

// NumAdd returns the sum of two numbers.
func NumAdd(a, b Value) Value {
	if _, ok := a.(*Complex); ok {
		return complexOp(a, b, NumAdd, NumAdd)
	}
	if _, ok := b.(*Complex); ok {
		return complexOp(a, b, NumAdd, NumAdd)
	}
	if ia, ok := a.(*Integer); ok {
		if ib, ok := b.(*Integer); ok {
			return addInt(ia, ib)
		}
	}
	if IsExact(a) && IsExact(b) {
		ra, _ := ToBigRat(a)
		rb, _ := ToBigRat(b)
		return normRat(new(big.Rat).Add(ra, rb))
	}
	return Float(asFloat(a) + asFloat(b))
}

// NumSub returns a - b.
func NumSub(a, b Value) Value {
	if _, ok := a.(*Complex); ok {
		return complexOp(a, b, NumSub, NumSub)
	}
	if _, ok := b.(*Complex); ok {
		return complexOp(a, b, NumSub, NumSub)
	}
	if ia, ok := a.(*Integer); ok {
		if ib, ok := b.(*Integer); ok {
			return subInt(ia, ib)
		}
	}
	if IsExact(a) && IsExact(b) {
		ra, _ := ToBigRat(a)
		rb, _ := ToBigRat(b)
		return normRat(new(big.Rat).Sub(ra, rb))
	}
	return Float(asFloat(a) - asFloat(b))
}

// NumMul returns the product of two numbers.
func NumMul(a, b Value) Value {
	if _, ok := a.(*Complex); ok {
		return complexOp(a, b, NumMul, NumMul)
	}
	if _, ok := b.(*Complex); ok {
		return complexOp(a, b, NumMul, NumMul)
	}
	if ia, ok := a.(*Integer); ok {
		if ib, ok := b.(*Integer); ok {
			return mulInt(ia, ib)
		}
	}
	if IsExact(a) && IsExact(b) {
		ra, _ := ToBigRat(a)
		rb, _ := ToBigRat(b)
		return normRat(new(big.Rat).Mul(ra, rb))
	}
	return Float(asFloat(a) * asFloat(b))
}

// NumDiv returns a / b using exact rational arithmetic when both are exact.
func NumDiv(a, b Value) Value {
	if _, ok := a.(*Complex); ok {
		return complexDiv(a, b)
	}
	if _, ok := b.(*Complex); ok {
		return complexDiv(a, b)
	}
	if IsExact(a) && IsExact(b) {
		ra, _ := ToBigRat(a)
		rb, _ := ToBigRat(b)
		if rb.Sign() == 0 {
			panic(NewError("division by zero", a, b))
		}
		return normRat(new(big.Rat).Quo(ra, rb))
	}
	fb := asFloat(b)
	if fb == 0 {
		fa := asFloat(a)
		if fa == 0 || math.IsNaN(fa) {
			return Float(math.NaN())
		}
		sign := 1
		if math.Signbit(fa) != math.Signbit(fb) {
			sign = -1
		}
		return Float(math.Inf(sign))
	}
	return Float(asFloat(a) / fb)
}

// complexOp applies op component-wise.
func complexOp(a, b Value, opRe, opIm func(x, y Value) Value) Value {
	ar, ai := ComplexParts(a)
	br, bi := ComplexParts(b)
	return NormalizeComplex(opRe(ar, br), opIm(ai, bi))
}

// complexDiv divides two numbers, possibly complex.
func complexDiv(a, b Value) Value {
	ar, ai := ComplexParts(a)
	br, bi := ComplexParts(b)
	// (a+bi)/(c+di) = ((ac+bd) + (bc-ad)i) / (c^2+d^2)
	den := NumAdd(NumMul(br, br), NumMul(bi, bi))
	re := NumDiv(NumAdd(NumMul(ar, br), NumMul(ai, bi)), den)
	im := NumDiv(NumSub(NumMul(ai, br), NumMul(ar, bi)), den)
	return NormalizeComplex(re, im)
}

// ComplexParts splits a number into its real and imaginary components.
func ComplexParts(v Value) (re, im Value) {
	if c, ok := v.(*Complex); ok {
		return c.Re, c.Im
	}
	return v, Int(0)
}

// NormalizeComplex collapses a complex number with exact zero imaginary part
// into a real number.
func NormalizeComplex(re, im Value) Value {
	if isExactZero(im) {
		return re
	}
	return &Complex{Re: re, Im: im}
}

// ------------------------------------------------------------------ comparison

// NumEq reports numeric equality.  When one operand is exact and the other
// inexact the inexact operand is converted to exact, as R7RS 6.2.6
// recommends, so that = remains transitive.
func NumEq(a, b Value) bool {
	if _, ok := a.(*Complex); ok {
		ar, ai := ComplexParts(a)
		br, bi := ComplexParts(b)
		return NumEq(ar, br) && NumEq(ai, bi)
	}
	if _, ok := b.(*Complex); ok {
		ar, ai := ComplexParts(a)
		br, bi := ComplexParts(b)
		return NumEq(ar, br) && NumEq(ai, bi)
	}
	if IsExact(a) && IsExact(b) {
		ar, aok := ToBigRat(a)
		br, bok := ToBigRat(b)
		if aok && bok {
			return ar.Cmp(br) == 0
		}
	}
	if IsInexact(a) && IsInexact(b) {
		return asFloat(a) == asFloat(b)
	}
	fa, fb := asFloat(a), asFloat(b)
	if math.IsInf(fa, 0) || math.IsInf(fb, 0) || math.IsNaN(fa) || math.IsNaN(fb) {
		return fa == fb
	}
	ea, eb := a, b
	if IsInexact(ea) {
		ea = Exact(ea)
	}
	if IsInexact(eb) {
		eb = Exact(eb)
	}
	ra, aok := ToBigRat(ea)
	rb, bok := ToBigRat(eb)
	if aok && bok {
		return ra.Cmp(rb) == 0
	}
	return fa == fb
}

// NumCmp compares two real numbers; returns -1, 0 or 1.  NaN comparisons
// return 2 (unordered).
func NumCmp(a, b Value) int {
	a, b = RealPart(a), RealPart(b)
	fa, fb := asFloat(a), asFloat(b)
	if math.IsNaN(fa) || math.IsNaN(fb) {
		return 2
	}
	// Compare exactly whenever both sides are finite, including a mixed pair:
	// turning the exact side into a float loses precision above 2^53, which
	// made (< a b) and (= a b) disagree.
	if ar, ok := exactRat(a); ok {
		if br, ok := exactRat(b); ok {
			return ar.Cmp(br)
		}
	}
	switch {
	case fa < fb:
		return -1
	case fa > fb:
		return 1
	}
	return 0
}

// exactRat is the exact value of a real number, including that of a finite
// float.  It reports false for an infinity or a NaN, which have no exact value.
func exactRat(v Value) (*big.Rat, bool) {
	if r, ok := ToBigRat(v); ok {
		return r, true
	}
	if f, ok := v.(Float); ok {
		ff := float64(f)
		if math.IsInf(ff, 0) || math.IsNaN(ff) {
			return nil, false
		}
		if r := new(big.Rat).SetFloat64(ff); r != nil {
			return r, true
		}
	}
	return nil, false
}

// NumSign returns -1, 0 or 1 for a real number.
func NumSign(v Value) int {
	v = RealPart(v)
	if IsExact(v) {
		switch x := v.(type) {
		case *Integer:
			return x.Sign()
		case *Rational:
			return x.R.Sign()
		}
	}
	f := asFloat(v)
	switch {
	case math.IsNaN(f):
		// Not a number is neither zero nor positive nor negative, so it gets a
		// value of its own: reporting 0 made (zero? +nan.0) true.
		return signNaN
	case f < 0:
		return -1
	case f > 0:
		return 1
	}
	return 0
}

// signNaN is what NumSign reports for a NaN.
const signNaN = 2

// ------------------------------------------------------------------ conversion

// Exact converts an inexact number to an exact one.
func Exact(v Value) Value {
	switch x := v.(type) {
	case *Integer, *Rational:
		return x
	case Float:
		f := float64(x)
		if math.IsInf(f, 0) || math.IsNaN(f) {
			panic(NewError("exact: cannot convert to exact", v))
		}
		r := new(big.Rat)
		r.SetFloat64(f)
		return normRat(r)
	case *Complex:
		return NormalizeComplex(Exact(x.Re), Exact(x.Im))
	}
	panic(wrongType("number", v))
}

// Inexact converts an exact number to an inexact one.
func Inexact(v Value) Value {
	switch x := v.(type) {
	case *Integer, *Rational:
		return Float(asFloat(x))
	case Float:
		return x
	case *Complex:
		return NormalizeComplex(Inexact(x.Re), Inexact(x.Im))
	}
	panic(wrongType("number", v))
}

// -------------------------------------------------------------- float printing

// FormatFloat renders a float64 the way Scheme expects.
func FormatFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "+inf.0"
	}
	if math.IsInf(f, -1) {
		return "-inf.0"
	}
	if math.IsNaN(f) {
		return "+nan.0"
	}
	abs := math.Abs(f)
	if f == math.Trunc(f) && abs < 1e21 {
		return strconv.FormatFloat(f, 'f', 1, 64)
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	// Guarantee the result reads back as inexact: it must contain '.' or an
	// exponent, and when it uses an exponent the mantissa keeps a decimal
	// point so that it round-trips as an inexact number.
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		if !strings.Contains(s[:i], ".") {
			s = s[:i] + ".0" + s[i:]
		}
		return s
	}
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// FormatNumber renders a number in the given radix (2, 8, 10 or 16).
func FormatNumber(v Value, radix int) string {
	switch x := v.(type) {
	case *Integer:
		return x.Big().Text(radix)
	case *Rational:
		return x.R.Num().Text(radix) + "/" + x.R.Denom().Text(radix)
	case Float:
		if radix != 10 {
			panic(NewError("number->string: radix must be 10 for inexact numbers", v))
		}
		return FormatFloat(float64(x))
	case *Complex:
		re, im := FormatNumber(x.Re, radix), FormatNumber(x.Im, radix)
		if !strings.HasPrefix(im, "-") && !strings.HasPrefix(im, "+") {
			im = "+" + im
		}
		if s, ok := x.Im.(Float); ok && strings.HasPrefix(FormatFloat(float64(s)), "+inf") {
			im = "+inf.0"
		}
		return re + im + "i"
	}
	panic(wrongType("number", v))
}

// ---------------------------------------------------------------- exact math

func intGcd(a, b *big.Int) *big.Int {
	g := new(big.Int).GCD(nil, nil, new(big.Int).Abs(a), new(big.Int).Abs(b))
	return g
}

// ExactIntegerSqrt returns the integer square root and remainder.
func ExactIntegerSqrt(n *big.Int) (*big.Int, *big.Int) {
	if n.Sign() < 0 {
		panic(NewError("exact-integer-sqrt: negative argument", BigInt(n)))
	}
	s := new(big.Int).Sqrt(n)
	r := new(big.Int).Sub(n, new(big.Int).Mul(s, s))
	return s, r
}

// FloorDivMod performs floor division.
func FloorDivMod(a, b *Integer) (*Integer, *Integer) {
	if b.IsZero() {
		panic(NewError("division by zero"))
	}
	ab, bb := a.Big(), b.Big()
	q, r := new(big.Int).QuoRem(ab, bb, new(big.Int))
	if r.Sign() != 0 && (r.Sign() < 0) != (bb.Sign() < 0) {
		q.Sub(q, big.NewInt(1))
		r.Add(r, bb)
	}
	return BigInt(q), BigInt(r)
}

// TruncDivMod performs truncating division.
func TruncDivMod(a, b *Integer) (*Integer, *Integer) {
	if b.IsZero() {
		panic(NewError("division by zero"))
	}
	ab, bb := a.Big(), b.Big()
	q, r := new(big.Int).QuoRem(ab, bb, new(big.Int))
	return BigInt(q), BigInt(r)
}
