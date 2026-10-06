// SPDX-License-Identifier: MIT

package scheme

import (
	. "github.com/MrXie1109/GoScheme/internal/re"
	"math"
	"math/big"
	"math/bits"
	"sort"
)

// Batch arithmetic, statistics and number theory, in Go.
//
// The vector procedures here are the batch lane: one Go pass over the whole
// vector instead of one interpreted step per element.  There is no SIMD behind
// them, but the loop, the type checks and the boxing of intermediate results
// all disappear, and an in-place variant writes into the caller's buffer
// instead of allocating a new one.

// ---------------------------------------------------------------------------
// Statistics
// ---------------------------------------------------------------------------

func installFastStats(m *Machine, lib string) {
	// (mean seq) is the sum divided by the count; over exact numbers the
	// result is exact, so (mean '(1 2)) is 3/2 rather than 1.5.
	m.defSimple("mean", 1, 1, func(a []Value) (Value, error) {
		var acc Value = Int(0)
		n := 0
		forEachIn("mean", a[0], func(v Value) bool {
			acc = NumAdd(acc, wantNumber("mean", v))
			n++
			return true
		})
		if n == 0 {
			panic(errf("mean", "expected a non-empty sequence"))
		}
		return NumDiv(acc, Int(int64(n))), nil
	}, lib)

	// (median seq) sorts a copy in Go and takes the middle element, averaging
	// the two middle ones when the count is even.
	m.defSimple("median", 1, 1, func(a []Value) (Value, error) {
		nums := sortedNumbers("median", a[0])
		n := len(nums)
		if n == 0 {
			panic(errf("median", "expected a non-empty sequence"))
		}
		mid := n / 2
		if n%2 == 1 {
			return nums[mid], nil
		}
		return NumDiv(NumAdd(nums[mid-1], nums[mid]), Int(2)), nil
	}, lib)

	// (percentile seq p) is the nearest-rank percentile: the element at rank
	// ceil(p/100 * n), so p=50 is the median and p=100 the largest element.
	m.defSimple("percentile", 2, 2, func(a []Value) (Value, error) {
		p := AsFloat(wantNumber("percentile", a[1]))
		if p < 0 || p > 100 {
			panic(errf("percentile", "the percentile must be between 0 and 100"))
		}
		nums := sortedNumbers("percentile", a[0])
		n := len(nums)
		if n == 0 {
			panic(errf("percentile", "expected a non-empty sequence"))
		}
		rank := int(math.Ceil(p / 100 * float64(n)))
		if rank < 1 {
			rank = 1
		}
		if rank > n {
			rank = n
		}
		return nums[rank-1], nil
	}, lib)

	// (variance seq) and (stddev seq) are the population statistics; they are
	// inexact even when the input is exact.
	m.defSimple("variance", 1, 1, func(a []Value) (Value, error) {
		return Float(populationVariance("variance", a[0])), nil
	}, lib)

	m.defSimple("stddev", 1, 1, func(a []Value) (Value, error) {
		return Float(math.Sqrt(populationVariance("stddev", a[0]))), nil
	}, lib)

	// (mode seq) is the most frequent element, the first one to reach that
	// count when there is a tie.
	m.defSimple("mode", 1, 1, func(a []Value) (Value, error) {
		counts := make(map[string]int)
		values := make(map[string]Value)
		bestKey := ""
		best := 0
		n := 0
		forEachIn("mode", a[0], func(v Value) bool {
			k := WriteToString(v)
			counts[k]++
			if _, seen := values[k]; !seen {
				values[k] = v
			}
			if counts[k] > best {
				best = counts[k]
				bestKey = k
			}
			n++
			return true
		})
		if n == 0 {
			panic(errf("mode", "expected a non-empty sequence"))
		}
		return values[bestKey], nil
	}, lib)
}

// sortedNumbers collects a sequence of numbers and sorts it in Go.
func sortedNumbers(name string, v Value) []Value {
	var nums []Value
	forEachIn(name, v, func(x Value) bool {
		nums = append(nums, wantNumber(name, x))
		return true
	})
	sort.SliceStable(nums, func(i, j int) bool { return NumCmp(nums[i], nums[j]) < 0 })
	return nums
}

// populationVariance computes mean((x - mean)^2) in float64.
func populationVariance(name string, v Value) float64 {
	var sum, sumSq float64
	n := 0
	forEachIn(name, v, func(x Value) bool {
		f := AsFloat(wantNumber(name, x))
		sum += f
		sumSq += f * f
		n++
		return true
	})
	if n == 0 {
		panic(errf(name, "expected a non-empty sequence"))
	}
	mean := sum / float64(n)
	return sumSq/float64(n) - mean*mean
}

// ---------------------------------------------------------------------------
// Number theory and bit operations
// ---------------------------------------------------------------------------

func installFastNumberTheory(m *Machine, lib string) {
	bitFold := func(name string, start int64, op func(acc, x *big.Int) *big.Int) func([]Value) (Value, error) {
		return func(a []Value) (Value, error) {
			acc := big.NewInt(start)
			for _, v := range a {
				acc = op(acc, wantBigInt(name, v))
			}
			return BigInt(acc), nil
		}
	}
	m.defSimple("bit-and", 0, 64, bitFold("bit-and", -1, func(acc, x *big.Int) *big.Int {
		return new(big.Int).And(acc, x)
	}), lib)
	m.defSimple("bit-or", 0, 64, bitFold("bit-or", 0, func(acc, x *big.Int) *big.Int {
		return new(big.Int).Or(acc, x)
	}), lib)
	m.defSimple("bit-xor", 0, 64, bitFold("bit-xor", 0, func(acc, x *big.Int) *big.Int {
		return new(big.Int).Xor(acc, x)
	}), lib)

	m.defSimple("bit-not", 1, 1, func(a []Value) (Value, error) {
		return BigInt(new(big.Int).Not(wantBigInt("bit-not", a[0]))), nil
	}, lib)

	// (bit-shift n count) shifts left by a positive count and right, keeping
	// the sign, by a negative one.
	m.defSimple("bit-shift", 2, 2, func(a []Value) (Value, error) {
		n := wantBigInt("bit-shift", a[0])
		shift := wantBigInt("bit-shift", a[1])
		if !shift.IsInt64() {
			panic(errf("bit-shift", "the shift count is too large"))
		}
		count := shift.Int64()
		if count > 100_000_000 || count < -100_000_000 {
			panic(errf("bit-shift", "the shift count is too large"))
		}
		if count >= 0 {
			return BigInt(new(big.Int).Lsh(n, uint(count))), nil
		}
		return BigInt(new(big.Int).Rsh(n, uint(-count))), nil
	}, lib)

	// (bit-count n) is the number of set bits in the magnitude of n.
	m.defSimple("bit-count", 1, 1, func(a []Value) (Value, error) {
		n := new(big.Int).Abs(wantBigInt("bit-count", a[0]))
		total := 0
		for _, w := range n.Bits() {
			total += bits.OnesCount(uint(w))
		}
		return Int(int64(total)), nil
	}, lib)

	// (integer-length n) is the number of bits in the magnitude of n.
	m.defSimple("integer-length", 1, 1, func(a []Value) (Value, error) {
		n := new(big.Int).Abs(wantBigInt("integer-length", a[0]))
		return Int(int64(n.BitLen())), nil
	}, lib)

	// (expt-mod base exponent modulus) is modular exponentiation: no huge
	// intermediate power is ever built.
	m.defSimple("expt-mod", 3, 3, func(a []Value) (Value, error) {
		base := wantBigInt("expt-mod", a[0])
		exp := wantBigInt("expt-mod", a[1])
		mod := wantBigInt("expt-mod", a[2])
		if exp.Sign() < 0 {
			panic(errf("expt-mod", "the exponent must not be negative"))
		}
		if mod.Sign() <= 0 {
			panic(errf("expt-mod", "the modulus must be positive"))
		}
		return BigInt(new(big.Int).Exp(base, exp, mod)), nil
	}, lib)

	// (isqrt n) is the integer square root, the floor of the real one.
	m.defSimple("isqrt", 1, 1, func(a []Value) (Value, error) {
		n := wantBigInt("isqrt", a[0])
		if n.Sign() < 0 {
			panic(errf("isqrt", "expected a non-negative integer but got %s", WriteToString(a[0])))
		}
		return BigInt(new(big.Int).Sqrt(n)), nil
	}, lib)

	// (prime? n) is Baillie-PSW with extra Miller-Rabin rounds.
	m.defSimple("prime?", 1, 1, func(a []Value) (Value, error) {
		return BooleanOf(probablyPrime(wantBigInt("prime?", a[0]))), nil
	}, lib)

	// (primes limit) sieves the primes below limit into a list.
	m.defSimple("primes", 1, 1, func(a []Value) (Value, error) {
		limit := wantIndex("primes", a[0])
		if limit > 50_000_000 {
			panic(errf("primes", "the limit is too large for a sieve (50,000,000 at most)"))
		}
		return listOf(sieve(limit)), nil
	}, lib)

	// (factor n) is the prime factorisation of n, ascending, with
	// multiplicity; 1 has none.
	m.defSimple("factor", 1, 1, func(a []Value) (Value, error) {
		n := wantBigInt("factor", a[0])
		if n.Sign() <= 0 {
			panic(errf("factor", "expected a positive integer but got %s", WriteToString(a[0])))
		}
		return listOf(factorize(n)), nil
	}, lib)

	// (clamp x low high) keeps x inside the range, and (sign x) is -1, 0 or 1.
	m.defSimple("clamp", 3, 3, func(a []Value) (Value, error) {
		x := wantNumber("clamp", a[0])
		lo := wantNumber("clamp", a[1])
		hi := wantNumber("clamp", a[2])
		if NumCmp(lo, hi) > 0 {
			panic(errf("clamp", "the low bound is above the high one"))
		}
		if NumCmp(x, lo) < 0 {
			return lo, nil
		}
		if NumCmp(x, hi) > 0 {
			return hi, nil
		}
		return x, nil
	}, lib)

	m.defSimple("sign", 1, 1, func(a []Value) (Value, error) {
		return Int(int64(NumSign(wantNumber("sign", a[0])))), nil
	}, lib)
}

// wantBigInt accepts an exact integer of any size.
func wantBigInt(name string, v Value) *big.Int {
	if i, ok := v.(*Integer); ok {
		return i.Big()
	}
	panic(errf(name, "expected an exact integer but got %s", WriteToString(v)))
}

func probablyPrime(n *big.Int) bool {
	if n.Sign() <= 0 {
		return false
	}
	if n.Cmp(big.NewInt(2)) < 0 {
		return false
	}
	return n.ProbablyPrime(20)
}

// sieve returns the primes below limit.
func sieve(limit int) []Value {
	if limit < 2 {
		return nil
	}
	composite := make([]bool, limit)
	out := make([]Value, 0, limit/8)
	for i := 2; i < limit; i++ {
		if composite[i] {
			continue
		}
		out = append(out, Int(int64(i)))
		for j := i * i; j < limit && j > 0; j += i {
			composite[j] = true
		}
	}
	return out
}

// factorize returns the prime factors of n in ascending order.  Small factors
// are divided out first, then Pollard's rho splits whatever is left, so a
// large semiprime does not need trial division to its square root.
func factorize(n *big.Int) []Value {
	var out []Value
	one := big.NewInt(1)
	zero := big.NewInt(0)
	rem := new(big.Int).Set(n)
	for _, p := range []int64{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37} {
		bp := big.NewInt(p)
		for {
			q, r := new(big.Int).QuoRem(rem, bp, new(big.Int))
			if r.Cmp(zero) != 0 {
				break
			}
			out = append(out, Int(p))
			rem = q
		}
	}
	var split func(x *big.Int)
	split = func(x *big.Int) {
		if x.Cmp(one) == 0 {
			return
		}
		if probablyPrime(x) {
			out = append(out, BigInt(x))
			return
		}
		d := pollardRho(x)
		split(d)
		split(new(big.Int).Quo(x, d))
	}
	split(rem)
	sort.SliceStable(out, func(i, j int) bool {
		return NumCmp(out[i], out[j]) < 0
	})
	return out
}

// pollardRho finds a non-trivial factor of the composite n with Brent's
// variant of Pollard's rho.
func pollardRho(n *big.Int) *big.Int {
	one := big.NewInt(1)
	two := big.NewInt(2)
	if n.Bit(0) == 0 {
		return two
	}
	for c := int64(1); ; c++ {
		bc := big.NewInt(c)
		x := big.NewInt(2)
		y := big.NewInt(2)
		d := big.NewInt(1)
		for d.Cmp(one) == 0 {
			x = addModSquared(x, bc, n)
			y = addModSquared(addModSquared(y, bc, n), bc, n)
			d = new(big.Int).GCD(nil, nil, new(big.Int).Sub(x, y), n)
			if d.Cmp(n) == 0 {
				break
			}
		}
		if d.Cmp(n) != 0 && d.Cmp(one) != 0 {
			return d
		}
	}
}

// addModSquared computes (x*x + c) mod n.
func addModSquared(x, c, n *big.Int) *big.Int {
	sq := new(big.Int).Mul(x, x)
	sq.Add(sq, c)
	return sq.Mod(sq, n)
}

// ---------------------------------------------------------------------------
// Batch arithmetic on vectors
// ---------------------------------------------------------------------------

func installFastBatch(m *Machine, lib string) {
	// (vector-mul a b) and (vector-div a b) are element by element.
	m.defSimple("vector-mul", 2, 2, func(a []Value) (Value, error) {
		x, y := zipVectors("vector-mul", a[0], a[1])
		out := make([]Value, len(x))
		for i := range x {
			out[i] = NumMul(x[i], y[i])
		}
		return &Vector{Items: out}, nil
	}, lib)

	m.defSimple("vector-div", 2, 2, func(a []Value) (Value, error) {
		x, y := zipVectors("vector-div", a[0], a[1])
		out := make([]Value, len(x))
		for i := range x {
			out[i] = NumDiv(x[i], y[i])
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (vector-negate v) and (vector-abs v) are new vectors.
	m.defSimple("vector-negate", 1, 1, func(a []Value) (Value, error) {
		vec := wantVector("vector-negate", a[0])
		out := make([]Value, len(vec.Items))
		for i, v := range vec.Items {
			out[i] = NumSub(Int(0), wantNumber("vector-negate", v))
		}
		return &Vector{Items: out}, nil
	}, lib)

	m.defSimple("vector-abs", 1, 1, func(a []Value) (Value, error) {
		vec := wantVector("vector-abs", a[0])
		out := make([]Value, len(vec.Items))
		for i, v := range vec.Items {
			v = wantNumber("vector-abs", v)
			out[i] = absNumber(v)
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (vector-clamp v low high) is the batch version of clamp.
	m.defSimple("vector-clamp", 3, 3, func(a []Value) (Value, error) {
		vec := wantVector("vector-clamp", a[0])
		lo := wantNumber("vector-clamp", a[1])
		hi := wantNumber("vector-clamp", a[2])
		if NumCmp(lo, hi) > 0 {
			panic(errf("vector-clamp", "the low bound is above the high one"))
		}
		out := make([]Value, len(vec.Items))
		for i, v := range vec.Items {
			out[i] = clampNumber(wantNumber("vector-clamp", v), lo, hi)
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (vector-prefix-sum v) is the running total: element i is the sum of the
	// elements up to and including i.
	m.defSimple("vector-prefix-sum", 1, 1, func(a []Value) (Value, error) {
		vec := wantVector("vector-prefix-sum", a[0])
		out := make([]Value, len(vec.Items))
		var acc Value = Int(0)
		for i, v := range vec.Items {
			acc = NumAdd(acc, wantNumber("vector-prefix-sum", v))
			out[i] = acc
		}
		return &Vector{Items: out}, nil
	}, lib)

	// (vector-equal? a b) compares element by element in Go.
	m.defSimple("vector-equal?", 1, 32, func(a []Value) (Value, error) {
		first := wantVector("vector-equal?", a[0])
		for _, other := range a[1:] {
			vec := wantVector("vector-equal?", other)
			if len(vec.Items) != len(first.Items) {
				return False, nil
			}
			for i := range vec.Items {
				if !Equal(vec.Items[i], first.Items[i]) {
					return False, nil
				}
			}
		}
		return True, nil
	}, lib)

	// (vector-compare a b [less?]) is the lexicographic order, as -1, 0 or 1.
	m.defSimple("vector-compare", 2, 3, func(a []Value) (Value, error) {
		x := wantVector("vector-compare", a[0])
		y := wantVector("vector-compare", a[1])
		less := defaultLess(m)
		if len(a) == 3 {
			less = wantProcedure("vector-compare", a[2])
		}
		name := builtinName(less)
		caller := newFastCaller(m, less)
		n := len(x.Items)
		if len(y.Items) < n {
			n = len(y.Items)
		}
		for i := 0; i < n; i++ {
			switch {
			case caller.less(name, x.Items[i], y.Items[i]):
				return Int(-1), nil
			case caller.less(name, y.Items[i], x.Items[i]):
				return Int(1), nil
			}
		}
		switch {
		case len(x.Items) < len(y.Items):
			return Int(-1), nil
		case len(x.Items) > len(y.Items):
			return Int(1), nil
		}
		return Int(0), nil
	}, lib)

	// The in-place variants write into the caller's vector and return it, so a
	// numeric inner loop allocates nothing.
	inPlace := func(name string, op func(dst, x, y Value) Value) {
		m.defSimple(name, 2, 2, func(a []Value) (Value, error) {
			x, y := zipVectors(name, a[0], a[1])
			for i := range x {
				x[i] = op(x[i], x[i], y[i])
			}
			return a[0], nil
		}, lib)
	}
	inPlace("vector-add!", func(_, x, y Value) Value { return NumAdd(x, y) })
	inPlace("vector-sub!", func(_, x, y Value) Value { return NumSub(x, y) })
	inPlace("vector-mul!", func(_, x, y Value) Value { return NumMul(x, y) })
	inPlace("vector-div!", func(_, x, y Value) Value { return NumDiv(x, y) })

	m.defSimple("vector-scale!", 2, 2, func(a []Value) (Value, error) {
		vec := wantVector("vector-scale!", a[0])
		k := wantNumber("vector-scale!", a[1])
		for i, v := range vec.Items {
			vec.Items[i] = NumMul(wantNumber("vector-scale!", v), k)
		}
		return vec, nil
	}, lib)

	m.defSimple("vector-negate!", 1, 1, func(a []Value) (Value, error) {
		vec := wantVector("vector-negate!", a[0])
		for i, v := range vec.Items {
			vec.Items[i] = NumSub(Int(0), wantNumber("vector-negate!", v))
		}
		return vec, nil
	}, lib)

	m.defSimple("vector-abs!", 1, 1, func(a []Value) (Value, error) {
		vec := wantVector("vector-abs!", a[0])
		for i, v := range vec.Items {
			vec.Items[i] = absNumber(wantNumber("vector-abs!", v))
		}
		return vec, nil
	}, lib)

	m.defSimple("vector-clamp!", 3, 3, func(a []Value) (Value, error) {
		vec := wantVector("vector-clamp!", a[0])
		lo := wantNumber("vector-clamp!", a[1])
		hi := wantNumber("vector-clamp!", a[2])
		if NumCmp(lo, hi) > 0 {
			panic(errf("vector-clamp!", "the low bound is above the high one"))
		}
		for i, v := range vec.Items {
			vec.Items[i] = clampNumber(wantNumber("vector-clamp!", v), lo, hi)
		}
		return vec, nil
	}, lib)
}

// absNumber is the magnitude of a number, through the numeric tower.
func absNumber(v Value) Value {
	if NumSign(v) < 0 {
		return NumSub(Int(0), v)
	}
	return v
}

// clampNumber keeps v between lo and hi.
func clampNumber(v, lo, hi Value) Value {
	if NumCmp(v, lo) < 0 {
		return lo
	}
	if NumCmp(v, hi) > 0 {
		return hi
	}
	return v
}
