// Package jsfloat formats floats as JavaScript's Number.prototype.toString
// does (ECMA-262 Number::toString), the text Astro renders for a number.
// The runtime and the compiler's constant folding share it, so a folded
// constant prints the same bytes as a runtime write.
package jsfloat

import (
	"math"
	"strconv"
)

// Append appends f in JavaScript's form, with the shortest digits that
// read back as the same float of bitSize bits (32 or 64): 0.1, 1000000,
// 1e-7, 1e+21, NaN, Infinity, -Infinity; -0 prints 0. It doesn't allocate
// beyond growing b.
func Append(b []byte, f float64, bitSize int) []byte {
	switch {
	case f != f:
		return append(b, "NaN"...)
	case math.IsInf(f, 1):
		return append(b, "Infinity"...)
	case math.IsInf(f, -1):
		return append(b, "-Infinity"...)
	case f == 0:
		return append(b, '0')
	}
	if f < 0 {
		b = append(b, '-')
		f = -f
	}
	// The shortest digits in exponent form, "d.ddde±XX" ("de±XX" for one
	// digit), rearranged in place: f = 0.d1d2…dk × 10^n, as ECMA-262
	// Number::toString states its cases.
	start := len(b)
	b = strconv.AppendFloat(b, f, 'e', -1, bitSize)
	ep := len(b) - 4 // 'e' (the exponent has 2 or 3 digits)
	if b[ep] != 'e' {
		ep--
	}
	e := int(b[ep+2]-'0')*10 + int(b[ep+3]-'0')
	if ep+4 < len(b) {
		e = e*10 + int(b[ep+4]-'0')
	}
	if b[ep+1] == '-' {
		e = -e
	}
	n := e + 1
	k := 1 // digits
	if ep > start+1 {
		k = ep - start - 1
	}
	switch {
	case k <= n && n <= 21: // an integer: the digits, then zeros
		if k > 1 {
			copy(b[start+1:], b[start+2:ep])
		}
		b = b[:start+k]
		for range n - k {
			b = append(b, '0')
		}
	case 0 < n && n <= 21: // "ddd.ddd": move the '.' n-1 digits right
		copy(b[start+1:], b[start+2:start+1+n])
		b[start+n] = '.'
		b = b[:ep]
	case -6 < n && n <= 0: // "0.000ddd": "0." and -n zeros, then the digits
		if k > 1 {
			copy(b[start+1:], b[start+2:ep])
		}
		b = b[:start+k]
		z := 2 - n
		for range z {
			b = append(b, '0')
		}
		copy(b[start+z:], b[start:start+k])
		for i := start; i < start+z; i++ {
			b[i] = '0'
		}
		b[start+1] = '.'
	default: // exponent form, without the exponent's leading zero
		if b[ep+2] == '0' {
			b[ep+2] = b[ep+3]
			b = b[:ep+3]
		}
	}
	return b
}

// Format returns f in JavaScript's form (see Append).
func Format(f float64, bitSize int) string {
	var buf [32]byte
	return string(Append(buf[:0], f, bitSize))
}
