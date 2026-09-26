package scheme

import (
	"math"
	"math/big"
)

// ---------------------------------------------------------------------------
// Numeric predicates
// ---------------------------------------------------------------------------

func installNumbers(m *Machine) {
	m.defSimple("number?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(IsNumber(a[0])), nil
	}, libBase, libR5RS)
	m.defSimple("complex?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(IsNumber(a[0])), nil
	}, libBase, libR5RS)
	m.defSimple("real?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(IsReal(a[0])), nil
	}, libBase, libR5RS)
	m.defSimple("rational?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(IsRationalVal(a[0])), nil
	}, libBase, libR5RS)
	m.defSimple("integer?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(IsIntegerVal(a[0])), nil
	}, libBase, libR5RS)
	m.defSimple("exact?", 1, 1, func(a []Value) (Value, error) {
		wantNumber("exact?", a[0])
		return BooleanOf(IsExact(a[0])), nil
	}, libBase, libR5RS)
	m.defSimple("inexact?", 1, 1, func(a []Value) (Value, error) {
		wantNumber("inexact?", a[0])
		return BooleanOf(IsInexact(a[0])), nil
	}, libBase, libR5RS)
	m.defSimple("exact-integer?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(IsExactInteger(a[0])), nil
	}, libBase)
	m.defSimple("exact-rational?", 1, 1, func(a []Value) (Value, error) {
		switch a[0].(type) {
		case *Integer, *Rational:
			return True, nil
		}
		return False, nil
	}, libBase)
	m.defSimple("nan?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(numAny(wantNumber("nan?", a[0]), func(f float64) bool { return math.IsNaN(f) })), nil
	}, libBase)
	m.defSimple("infinite?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(numAny(wantNumber("infinite?", a[0]), func(f float64) bool { return math.IsInf(f, 0) })), nil
	}, libBase)
	m.defSimple("finite?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(numAll(wantNumber("finite?", a[0]), func(f float64) bool {
			return !math.IsInf(f, 0) && !math.IsNaN(f)
		})), nil
	}, libBase)

	// ------------------------------------------------------------- comparison
	m.defSimple("=", 2, -1, func(a []Value) (Value, error) {
		for i := range a {
			wantNumber("=", a[i])
		}
		for i := 1; i < len(a); i++ {
			if !NumEq(a[i-1], a[i]) {
				return False, nil
			}
		}
		return True, nil
	}, libBase, libR5RS)
	cmp := func(name string, ok func(c int) bool) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			for i := range a {
				if !IsReal(a[i]) {
					panic(errf(name, "expected a real number but got %s", WriteToString(a[i])))
				}
			}
			for i := 1; i < len(a); i++ {
				c := NumCmp(a[i-1], a[i])
				if c == 2 || !ok(c) {
					return False, nil
				}
			}
			return True, nil
		}
	}
	m.defSimple("<", 2, -1, cmp("<", func(c int) bool { return c < 0 }), libBase, libR5RS)
	m.defSimple(">", 2, -1, cmp(">", func(c int) bool { return c > 0 }), libBase, libR5RS)
	m.defSimple("<=", 2, -1, cmp("<=", func(c int) bool { return c <= 0 }), libBase, libR5RS)
	m.defSimple(">=", 2, -1, cmp(">=", func(c int) bool { return c >= 0 }), libBase, libR5RS)

	m.defSimple("zero?", 1, 1, func(a []Value) (Value, error) {
		wantNumber("zero?", a[0])
		return BooleanOf(NumSign(a[0]) == 0), nil
	}, libBase, libR5RS)
	m.defSimple("positive?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(NumSign(wantReal("positive?", a[0])) > 0), nil
	}, libBase, libR5RS)
	m.defSimple("negative?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(NumSign(wantReal("negative?", a[0])) < 0), nil
	}, libBase, libR5RS)
	m.defSimple("odd?", 1, 1, func(a []Value) (Value, error) {
		i := wantInteger("odd?", a[0])
		return BooleanOf(i.Big().Bit(0) == 1), nil
	}, libBase, libR5RS)
	m.defSimple("even?", 1, 1, func(a []Value) (Value, error) {
		i := wantInteger("even?", a[0])
		return BooleanOf(i.Big().Bit(0) == 0), nil
	}, libBase, libR5RS)

	// ------------------------------------------------------------ arithmetic
	m.defSimple("+", 0, -1, func(a []Value) (Value, error) {
		var acc Value = Int(0)
		for _, v := range a {
			acc = NumAdd(acc, wantNumber("+", v))
		}
		return acc, nil
	}, libBase, libR5RS)
	m.defSimple("*", 0, -1, func(a []Value) (Value, error) {
		var acc Value = Int(1)
		for _, v := range a {
			acc = NumMul(acc, wantNumber("*", v))
		}
		return acc, nil
	}, libBase, libR5RS)
	m.defSimple("-", 1, -1, func(a []Value) (Value, error) {
		wantNumber("-", a[0])
		if len(a) == 1 {
			return NumSub(Int(0), a[0]), nil
		}
		acc := a[0]
		for _, v := range a[1:] {
			acc = NumSub(acc, wantNumber("-", v))
		}
		return acc, nil
	}, libBase, libR5RS)
	m.defSimple("/", 1, -1, func(a []Value) (Value, error) {
		wantNumber("/", a[0])
		if len(a) == 1 {
			return NumDiv(Int(1), a[0]), nil
		}
		acc := a[0]
		for _, v := range a[1:] {
			acc = NumDiv(acc, wantNumber("/", v))
		}
		return acc, nil
	}, libBase, libR5RS)
	m.defSimple("abs", 1, 1, func(a []Value) (Value, error) {
		v := wantReal("abs", a[0])
		if NumSign(v) < 0 {
			return NumSub(Int(0), v), nil
		}
		return v, nil
	}, libBase, libR5RS)
	m.defSimple("max", 1, -1, func(a []Value) (Value, error) {
		best := wantReal("max", a[0])
		inexact := IsInexact(a[0])
		for _, v := range a[1:] {
			wantReal("max", v)
			if IsInexact(v) {
				inexact = true
			}
			if NumCmp(v, best) > 0 {
				best = v
			}
		}
		if inexact && IsExact(best) {
			return Inexact(best), nil
		}
		return best, nil
	}, libBase, libR5RS)
	m.defSimple("min", 1, -1, func(a []Value) (Value, error) {
		best := wantReal("min", a[0])
		inexact := IsInexact(a[0])
		for _, v := range a[1:] {
			wantReal("min", v)
			if IsInexact(v) {
				inexact = true
			}
			if NumCmp(v, best) < 0 {
				best = v
			}
		}
		if inexact && IsExact(best) {
			return Inexact(best), nil
		}
		return best, nil
	}, libBase, libR5RS)
	m.defSimple("gcd", 0, -1, func(a []Value) (Value, error) {
		acc := big.NewInt(0)
		inexact := false
		for _, v := range a {
			iv := wantIntegerLike("gcd", v)
			if IsInexact(v) {
				inexact = true
			}
			acc = intGcd(acc, iv.Big())
		}
		res := Value(BigInt(acc))
		if inexact {
			return Inexact(res), nil
		}
		return res, nil
	}, libBase, libR5RS)
	m.defSimple("lcm", 0, -1, func(a []Value) (Value, error) {
		acc := big.NewInt(1)
		inexact := false
		for _, v := range a {
			if IsInexact(v) {
				inexact = true
			}
			b := wantIntegerLike("lcm", v).Big()
			if b.Sign() == 0 {
				return Int(0), nil
			}
			g := intGcd(acc, b)
			acc = new(big.Int).Div(new(big.Int).Mul(acc, b), g)
			acc.Abs(acc)
		}
		if len(a) == 0 {
			return Int(1), nil
		}
		res := Value(BigInt(acc))
		if inexact {
			return Inexact(res), nil
		}
		return res, nil
	}, libBase, libR5RS)
	m.defSimple("square", 1, 1, func(a []Value) (Value, error) {
		return NumMul(wantNumber("square", a[0]), a[0]), nil
	}, libBase)

	// ------------------------------------------------------------ division
	m.defSimple("floor/", 2, 2, func(a []Value) (Value, error) {
		q, r := divMod("floor/", a[0], a[1], true)
		return &MultipleValues{Values: []Value{q, r}}, nil
	}, libBase)
	m.defSimple("truncate/", 2, 2, func(a []Value) (Value, error) {
		q, r := divMod("truncate/", a[0], a[1], false)
		return &MultipleValues{Values: []Value{q, r}}, nil
	}, libBase)
	m.defSimple("floor-quotient", 2, 2, func(a []Value) (Value, error) {
		q, _ := divMod("floor-quotient", a[0], a[1], true)
		return q, nil
	}, libBase)
	m.defSimple("floor-remainder", 2, 2, func(a []Value) (Value, error) {
		_, r := divMod("floor-remainder", a[0], a[1], true)
		return r, nil
	}, libBase)
	m.defSimple("truncate-quotient", 2, 2, func(a []Value) (Value, error) {
		q, _ := divMod("truncate-quotient", a[0], a[1], false)
		return q, nil
	}, libBase)
	m.defSimple("truncate-remainder", 2, 2, func(a []Value) (Value, error) {
		_, r := divMod("truncate-remainder", a[0], a[1], false)
		return r, nil
	}, libBase)
	m.defSimple("quotient", 2, 2, func(a []Value) (Value, error) {
		q, _ := divMod("quotient", a[0], a[1], false)
		return q, nil
	}, libR5RS)
	m.defSimple("remainder", 2, 2, func(a []Value) (Value, error) {
		_, r := divMod("remainder", a[0], a[1], false)
		return r, nil
	}, libR5RS)
	m.defSimple("modulo", 2, 2, func(a []Value) (Value, error) {
		_, r := divMod("modulo", a[0], a[1], true)
		return r, nil
	}, libR5RS)

	// ------------------------------------------------------- rational parts
	m.defSimple("numerator", 1, 1, func(a []Value) (Value, error) {
		switch x := wantReal("numerator", a[0]).(type) {
		case *Integer:
			return x, nil
		case *Rational:
			return BigInt(x.R.Num()), nil
		case Float:
			r, _ := ToBigRat(Exact(x))
			f, _ := new(big.Float).SetInt(r.Num()).Float64()
			return Float(f), nil
		}
		panic(errf("numerator", "bad argument"))
	}, libBase)
	m.defSimple("denominator", 1, 1, func(a []Value) (Value, error) {
		switch x := wantReal("denominator", a[0]).(type) {
		case *Integer:
			return Int(1), nil
		case *Rational:
			return BigInt(x.R.Denom()), nil
		case Float:
			r, _ := ToBigRat(Exact(x))
			f, _ := new(big.Float).SetInt(r.Denom()).Float64()
			return Float(f), nil
		}
		panic(errf("denominator", "bad argument"))
	}, libBase)

	// ------------------------------------------------------------ rounding
	m.defSimple("floor", 1, 1, func(a []Value) (Value, error) {
		return roundOp("floor", a[0], math.Floor, ratFloor)
	}, libBase, libR5RS)
	m.defSimple("ceiling", 1, 1, func(a []Value) (Value, error) {
		return roundOp("ceiling", a[0], math.Ceil, ratCeil)
	}, libBase, libR5RS)
	m.defSimple("truncate", 1, 1, func(a []Value) (Value, error) {
		return roundOp("truncate", a[0], math.Trunc, ratTrunc)
	}, libBase, libR5RS)
	m.defSimple("round", 1, 1, func(a []Value) (Value, error) {
		return roundOp("round", a[0], math.RoundToEven, ratRound)
	}, libBase, libR5RS)
	m.defSimple("rationalize", 2, 2, func(a []Value) (Value, error) {
		x := wantReal("rationalize", a[0])
		y := wantReal("rationalize", a[1])
		inexact := IsInexact(x) || IsInexact(y)
		xr := Exact(x)
		yr := Exact(y)
		if IsInexact(yr) || IsInexact(xr) {
			panic(errf("rationalize", "cannot convert to exact"))
		}
		absy := yr
		if NumSign(absy) < 0 {
			absy = NumSub(Int(0), absy)
		}
		lo, _ := ToBigRat(NumSub(xr, absy))
		hi, _ := ToBigRat(NumAdd(xr, absy))
		res := simplestRational(lo, hi)
		v := normRat(res)
		if inexact {
			return Inexact(v), nil
		}
		return v, nil
	}, libBase)

	m.defSimple("exact", 1, 1, func(a []Value) (Value, error) {
		return Exact(wantNumber("exact", a[0])), nil
	}, libBase)
	m.defSimple("inexact", 1, 1, func(a []Value) (Value, error) {
		return Inexact(wantNumber("inexact", a[0])), nil
	}, libBase)
	m.defSimple("exact->inexact", 1, 1, func(a []Value) (Value, error) {
		return Inexact(wantNumber("exact->inexact", a[0])), nil
	}, libR5RS, libBase)
	m.defSimple("inexact->exact", 1, 1, func(a []Value) (Value, error) {
		return Exact(wantNumber("inexact->exact", a[0])), nil
	}, libR5RS, libBase)

	m.defSimple("exact-integer-sqrt", 1, 1, func(a []Value) (Value, error) {
		s, r := ExactIntegerSqrt(wantInteger("exact-integer-sqrt", a[0]).Big())
		return &MultipleValues{Values: []Value{BigInt(s), BigInt(r)}}, nil
	}, libBase)

	// ------------------------------------------------------- transcendental
	m.defSimple("exp", 1, 1, func(a []Value) (Value, error) {
		v := wantNumber("exp", a[0])
		if IsExact(v) && NumSign(v) == 0 {
			return Int(1), nil
		}
		if c, ok := v.(*Complex); ok {
			return complexExp(c), nil
		}
		return Float(math.Exp(asFloat(RealPart(v)))), nil
	}, libBase, libInexact)
	m.defSimple("log", 1, 2, func(a []Value) (Value, error) {
		v := wantNumber("log", a[0])
		var res Value
		if c, ok := v.(*Complex); ok {
			res = complexLog(c)
		} else {
			f := asFloat(RealPart(v))
			if f < 0 || (IsExact(v) && NumSign(v) < 0) {
				re := math.Log(math.Abs(f))
				res = NormalizeComplex(Float(re), Float(math.Pi))
			} else if f == 0 {
				panic(errf("log", "logarithm of zero"))
			} else {
				if IsExact(v) && NumEq(v, Int(1)) {
					res = Int(0)
				} else {
					res = Float(math.Log(f))
				}
			}
		}
		if len(a) == 2 {
			base := wantNumber("log", a[1])
			return NumDiv(res, logOf(base)), nil
		}
		return res, nil
	}, libBase, libInexact)
	m.defSimple("sin", 1, 1, func(a []Value) (Value, error) { return trig("sin", a[0], math.Sin) }, libBase, libInexact)
	m.defSimple("cos", 1, 1, func(a []Value) (Value, error) { return trig("cos", a[0], math.Cos) }, libBase, libInexact)
	m.defSimple("tan", 1, 1, func(a []Value) (Value, error) { return trig("tan", a[0], math.Tan) }, libBase, libInexact)
	m.defSimple("asin", 1, 1, func(a []Value) (Value, error) { return trig("asin", a[0], math.Asin) }, libBase, libInexact)
	m.defSimple("acos", 1, 1, func(a []Value) (Value, error) { return trig("acos", a[0], math.Acos) }, libBase, libInexact)
	m.defSimple("atan", 1, 2, func(a []Value) (Value, error) {
		if len(a) == 2 {
			y := asFloat(wantReal("atan", a[0]))
			x := asFloat(wantReal("atan", a[1]))
			return Float(math.Atan2(y, x)), nil
		}
		return trig("atan", a[0], math.Atan)
	}, libBase, libInexact)
	m.defSimple("sqrt", 1, 1, func(a []Value) (Value, error) {
		v := wantNumber("sqrt", a[0])
		switch x := v.(type) {
		case *Integer:
			if x.Sign() >= 0 {
				s, r := ExactIntegerSqrt(x.Big())
				if r.Sign() == 0 {
					return BigInt(s), nil
				}
				return Float(math.Sqrt(x.Float64())), nil
			}
			neg := new(big.Int).Neg(x.Big())
			s, r := ExactIntegerSqrt(neg)
			if r.Sign() == 0 {
				return NormalizeComplex(Int(0), BigInt(s)), nil
			}
			nf, _ := neg.Float64()
			return NormalizeComplex(Float(0), Float(math.Sqrt(nf))), nil
		case *Rational:
			if x.R.Sign() >= 0 {
				n := new(big.Int).Sqrt(x.R.Num())
				d := new(big.Int).Sqrt(x.R.Denom())
				if new(big.Int).Mul(n, n).Cmp(x.R.Num()) == 0 && new(big.Int).Mul(d, d).Cmp(x.R.Denom()) == 0 {
					return normRat(new(big.Rat).SetFrac(n, d)), nil
				}
				f, _ := x.R.Float64()
				return Float(math.Sqrt(f)), nil
			}
			f, _ := x.R.Float64()
			return NormalizeComplex(Float(0), Float(math.Sqrt(-f))), nil
		case Float:
			f := float64(x)
			if f < 0 {
				return NormalizeComplex(Float(0), Float(math.Sqrt(-f))), nil
			}
			return Float(math.Sqrt(f)), nil
		case *Complex:
			return complexSqrt(x), nil
		}
		panic(errf("sqrt", "bad argument"))
	}, libBase, libInexact)
	m.defSimple("expt", 2, 2, func(a []Value) (Value, error) {
		return numExpt(wantNumber("expt", a[0]), wantNumber("expt", a[1])), nil
	}, libBase, libR5RS)

	// -------------------------------------------------------------- complex
	m.defSimple("make-rectangular", 2, 2, func(a []Value) (Value, error) {
		re := wantReal("make-rectangular", a[0])
		im := wantReal("make-rectangular", a[1])
		return NormalizeComplex(re, im), nil
	}, libBase, libComplex)
	m.defSimple("make-polar", 2, 2, func(a []Value) (Value, error) {
		mag := asFloat(wantReal("make-polar", a[0]))
		ang := asFloat(wantReal("make-polar", a[1]))
		return NormalizeComplex(Float(mag*math.Cos(ang)), Float(mag*math.Sin(ang))), nil
	}, libBase, libComplex)
	m.defSimple("real-part", 1, 1, func(a []Value) (Value, error) {
		wantNumber("real-part", a[0])
		return RealPart(a[0]), nil
	}, libBase, libComplex)
	m.defSimple("imag-part", 1, 1, func(a []Value) (Value, error) {
		wantNumber("imag-part", a[0])
		_, im := ComplexParts(a[0])
		return im, nil
	}, libBase, libComplex)
	m.defSimple("magnitude", 1, 1, func(a []Value) (Value, error) {
		re, im := ComplexParts(wantNumber("magnitude", a[0]))
		re2 := NumMul(re, re)
		im2 := NumMul(im, im)
		s := NumAdd(re2, im2)
		return numSqrt(s), nil
	}, libBase, libComplex)
	m.defSimple("angle", 1, 1, func(a []Value) (Value, error) {
		re, im := ComplexParts(wantNumber("angle", a[0]))
		return Float(math.Atan2(asFloat(im), asFloat(re))), nil
	}, libBase, libComplex)

	// ------------------------------------------------------- number->string
	m.defSimple("number->string", 1, 2, func(a []Value) (Value, error) {
		radix := 10
		if len(a) == 2 {
			radix = wantIndex("number->string", a[1])
		}
		if radix != 2 && radix != 8 && radix != 10 && radix != 16 {
			panic(errf("number->string", "radix must be 2, 8, 10 or 16"))
		}
		return NewString(FormatNumber(wantNumber("number->string", a[0]), radix)), nil
	}, libBase, libR5RS)
	m.defSimple("string->number", 1, 2, func(a []Value) (Value, error) {
		s := wantString("string->number", a[0]).Value()
		radix := 10
		if len(a) == 2 {
			radix = wantIndex("string->number", a[1])
			if radix != 2 && radix != 8 && radix != 10 && radix != 16 {
				panic(errf("string->number", "radix must be 2, 8, 10 or 16"))
			}
		}
		if s == "" {
			return False, nil
		}
		if v, ok := ParseNumber(s, radix); ok {
			return v, nil
		}
		return False, nil
	}, libBase, libR5RS)
}

// wantIntegerLike accepts exact integers and inexact integral reals.
func wantIntegerLike(name string, v Value) *Integer {
	if i, ok := v.(*Integer); ok {
		return i
	}
	switch x := v.(type) {
	case Float:
		f := float64(x)
		if math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) {
			panic(errf(name, "expected an integer but got %s", WriteToString(v)))
		}
		return BigInt(Exact(x).(*Integer).Big())
	}
	panic(errf(name, "expected an integer but got %s", WriteToString(v)))
}

// divMod computes the quotient and remainder of two integers (exact or
// inexact).  The result is inexact when either operand is inexact.
func divMod(name string, a, b Value, floorMode bool) (Value, Value) {
	ai := wantIntegerLike(name, a)
	bi := wantIntegerLike(name, b)
	if bi.IsZero() {
		panic(errf(name, "division by zero"))
	}
	inexact := IsInexact(a) || IsInexact(b)
	var q, r *Integer
	if floorMode {
		q, r = FloorDivMod(ai, bi)
	} else {
		q, r = TruncDivMod(ai, bi)
	}
	var qv, rv Value = q, r
	if inexact {
		qv, rv = Inexact(q), Inexact(r)
	}
	return qv, rv
}

// numAny applies pred to every real component and reports whether any
// component satisfies it.
func numAny(v Value, pred func(float64) bool) bool {
	re, im := ComplexParts(v)
	if pred(asFloat(re)) {
		return true
	}
	return pred(asFloat(im))
}

// numAll applies pred to every real component.
func numAll(v Value, pred func(float64) bool) bool {
	re, im := ComplexParts(v)
	return pred(asFloat(re)) && pred(asFloat(im))
}

// ---------------------------------------------------------------------------
// Numeric helpers
// ---------------------------------------------------------------------------

func roundOp(name string, v Value, ff func(float64) float64, rf func(*big.Rat) *big.Rat) (Value, error) {
	switch x := RealPart(wantReal(name, v)).(type) {
	case *Integer:
		return x, nil
	case *Rational:
		return normRat(rf(x.R)), nil
	case Float:
		return Float(ff(float64(x))), nil
	}
	panic(errf(name, "bad argument"))
}

func ratFloor(r *big.Rat) *big.Rat {
	q := new(big.Int).Quo(r.Num(), r.Denom())
	if !r.IsInt() && r.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return new(big.Rat).SetInt(q)
}

func ratCeil(r *big.Rat) *big.Rat {
	q := new(big.Int).Quo(r.Num(), r.Denom())
	if !r.IsInt() && r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return new(big.Rat).SetInt(q)
}

func ratTrunc(r *big.Rat) *big.Rat {
	return new(big.Rat).SetInt(new(big.Int).Quo(r.Num(), r.Denom()))
}

func ratRound(r *big.Rat) *big.Rat {
	fl := ratFloor(r)
	diff := new(big.Rat).Sub(r, fl)
	half := big.NewRat(1, 2)
	c := diff.Cmp(half)
	switch {
	case c < 0:
		return fl
	case c > 0:
		return new(big.Rat).Add(fl, big.NewRat(1, 1))
	default:
		// round to even
		if fl.Num().Bit(0) == 0 {
			return fl
		}
		return new(big.Rat).Add(fl, big.NewRat(1, 1))
	}
}

func simplestRational(lo, hi *big.Rat) *big.Rat {
	if lo.Cmp(hi) > 0 {
		lo, hi = hi, lo
	}
	if lo.Sign() > 0 {
		return simplestPositive(lo, hi)
	}
	if hi.Sign() < 0 {
		return new(big.Rat).Neg(simplestPositive(new(big.Rat).Neg(hi), new(big.Rat).Neg(lo)))
	}
	return big.NewRat(0, 1)
}

func simplestPositive(lo, hi *big.Rat) *big.Rat {
	fl := ratFloor(lo)
	if lo.Cmp(fl) == 0 {
		return fl
	}
	fh := ratFloor(hi)
	if fl.Cmp(fh) < 0 {
		return new(big.Rat).Add(fl, big.NewRat(1, 1))
	}
	a := new(big.Rat).Sub(hi, fh)
	b := new(big.Rat).Sub(lo, fl)
	if a.Sign() == 0 || b.Sign() == 0 {
		return new(big.Rat).Add(fl, big.NewRat(1, 1))
	}
	inner := simplestPositive(new(big.Rat).Inv(a), new(big.Rat).Inv(b))
	return new(big.Rat).Add(fl, new(big.Rat).Inv(inner))
}

func numSqrt(v Value) Value {
	switch x := v.(type) {
	case *Integer:
		if x.Sign() >= 0 {
			s, r := ExactIntegerSqrt(x.Big())
			if r.Sign() == 0 {
				return BigInt(s)
			}
			return Float(math.Sqrt(x.Float64()))
		}
		xf := x.Float64()
		return NormalizeComplex(Float(0), Float(math.Sqrt(-xf)))
	case *Rational:
		f, _ := x.R.Float64()
		if f >= 0 {
			return Float(math.Sqrt(f))
		}
		return NormalizeComplex(Float(0), Float(math.Sqrt(-f)))
	case Float:
		f := float64(x)
		if f < 0 {
			return NormalizeComplex(Float(0), Float(math.Sqrt(-f)))
		}
		return Float(math.Sqrt(f))
	}
	return Float(math.NaN())
}

func complexSqrt(c *Complex) Value {
	re := asFloat(c.Re)
	im := asFloat(c.Im)
	mag := math.Hypot(re, im)
	sr := math.Sqrt((mag + re) / 2)
	si := math.Sqrt((mag - re) / 2)
	if im < 0 {
		si = -si
	}
	return NormalizeComplex(Float(sr), Float(si))
}

func complexExp(c *Complex) Value {
	re := asFloat(c.Re)
	im := asFloat(c.Im)
	e := math.Exp(re)
	return NormalizeComplex(Float(e*math.Cos(im)), Float(e*math.Sin(im)))
}

func complexLog(c *Complex) Value {
	re := asFloat(c.Re)
	im := asFloat(c.Im)
	return NormalizeComplex(Float(math.Log(math.Hypot(re, im))), Float(math.Atan2(im, re)))
}

func logOf(v Value) Value {
	f := asFloat(RealPart(v))
	if f <= 0 {
		return Float(math.NaN())
	}
	return Float(math.Log(f))
}

func trig(name string, v Value, fn func(float64) float64) (Value, error) {
	if _, ok := v.(*Complex); ok {
		panic(errf(name, "complex arguments are not supported"))
	}
	rv := wantReal(name, v)
	if IsExact(rv) {
		if NumSign(rv) == 0 {
			switch name {
			case "sin", "tan", "asin", "atan":
				return Int(0), nil
			case "cos":
				return Int(1), nil
			case "acos":
				return Float(math.Pi / 2), nil
			}
		}
		if name == "cos" && NumEq(rv, Int(0)) {
			return Int(1), nil
		}
	}
	f := asFloat(rv)
	res := fn(f)
	if math.IsNaN(res) {
		panic(errf(name, "result is not a real number"))
	}
	return Float(res), nil
}

func numExpt(base, exp Value) Value {
	// Exact integer exponent.
	if ei, ok := exp.(*Integer); ok {
		ex, _ := ei.Int64()
		if ei.Big().IsInt64() {
			if _, isComplex := base.(*Complex); !isComplex {
				if IsExact(base) {
					br, _ := ToBigRat(base)
					if ex >= 0 {
						n := new(big.Int).Exp(br.Num(), big.NewInt(ex), nil)
						d := new(big.Int).Exp(br.Denom(), big.NewInt(ex), nil)
						return normRat(new(big.Rat).SetFrac(n, d))
					}
					if br.Sign() == 0 {
						panic(errf("expt", "zero cannot be raised to a negative power"))
					}
					n := new(big.Int).Exp(br.Num(), big.NewInt(-ex), nil)
					d := new(big.Int).Exp(br.Denom(), big.NewInt(-ex), nil)
					return normRat(new(big.Rat).SetFrac(d, n))
				}
				f := asFloat(base)
				return Float(math.Pow(f, float64(ex)))
			}
		}
	}
	if IsExact(base) && IsExact(exp) && NumSign(base) == 0 && NumSign(exp) > 0 {
		return Int(0)
	}
	if IsExact(base) && NumEq(base, Int(1)) {
		return Int(1)
	}
	if c, ok := exp.(*Complex); ok {
		return complexExpt(base, c)
	}
	if c, ok := base.(*Complex); ok {
		return complexExpt(c, exp)
	}
	b := asFloat(base)
	e := asFloat(exp)
	if b < 0 {
		// Negative base with fractional exponent: complex result.
		return NormalizeComplex(Float(math.Pow(-b, e)*math.Cos(math.Pi*e)),
			Float(math.Pow(-b, e)*math.Sin(math.Pi*e)))
	}
	return Float(math.Pow(b, e))
}

func complexExpt(base, exp Value) Value {
	br, bi := ComplexParts(base)
	logB := complexLogParts(br, bi)
	er, ei := ComplexParts(exp)
	// exp * log(base)
	re := NumSub(NumMul(er, logB[0]), NumMul(ei, logB[1]))
	im := NumAdd(NumMul(er, logB[1]), NumMul(ei, logB[0]))
	if isExactZero(im) {
		return Float(math.Exp(asFloat(re)))
	}
	return complexExpParts(re, im)
}

func complexLogParts(re, im Value) [2]Value {
	fr := asFloat(re)
	fi := asFloat(im)
	return [2]Value{Float(math.Log(math.Hypot(fr, fi))), Float(math.Atan2(fi, fr))}
}

func complexExpParts(re, im Value) Value {
	fr := asFloat(re)
	fi := asFloat(im)
	e := math.Exp(fr)
	return NormalizeComplex(Float(e*math.Cos(fi)), Float(e*math.Sin(fi)))
}
