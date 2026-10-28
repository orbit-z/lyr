package jsfloat

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

var tenth = 0.1 // a variable, so 0.1 + 0.2 is float64 arithmetic

// The expected strings are String(x) from Bun (JavaScriptCore).
var cases64 = []struct {
	f    float64
	want string
}{
	{0, "0"},
	{math.Copysign(0, -1), "0"},
	{1, "1"},
	{-1, "-1"},
	{0.1, "0.1"},
	{-0.1, "-0.1"},
	{0.5, "0.5"},
	{2, "2"},
	{10, "10"},
	{100, "100"},
	{123.456, "123.456"},
	{-123.456, "-123.456"},
	{1e6, "1000000"},
	{1e7, "10000000"},
	{1234567, "1234567"},
	{1e20, "100000000000000000000"},
	{1e21, "1e+21"},
	{-1e21, "-1e+21"},
	{1.5e21, "1.5e+21"},
	{123456789012345680000, "123456789012345680000"},
	{999999999999999900000, "999999999999999900000"},
	{1e22, "1e+22"},
	{1e100, "1e+100"},
	{1.7976931348623157e308, "1.7976931348623157e+308"},
	{5e-324, "5e-324"},
	{2.2250738585072014e-308, "2.2250738585072014e-308"},
	{1e-6, "0.000001"},
	{1e-7, "1e-7"},
	{-1e-7, "-1e-7"},
	{1.5e-7, "1.5e-7"},
	{0.0000015, "0.0000015"},
	{0.00001234, "0.00001234"},
	{1.234e-7, "1.234e-7"},
	{1e-300, "1e-300"},
	{tenth + 0.2, "0.30000000000000004"},
	{1.0 / 3, "0.3333333333333333"},
	{2.0 / 3, "0.6666666666666666"},
	{3.14159, "3.14159"},
	{9007199254740993, "9007199254740992"},
	{4.35, "4.35"},
	{0.000123, "0.000123"},
	{12345678901234567890, "12345678901234567000"},
	{1.2345e-5, "0.000012345"},
	{100.5, "100.5"},
	{1e15, "1000000000000000"},
	{1e17, "100000000000000000"},
	{123e18, "123000000000000000000"},
	{1.7e-10, "1.7e-10"},
	{9.9e-7, "9.9e-7"},
	{0.0000010000000000000002, "0.0000010000000000000002"},
	{999999999999999999999, "1e+21"},
	{math.NaN(), "NaN"},
	{math.Inf(1), "Infinity"},
	{math.Inf(-1), "-Infinity"},
}

func TestAppend64(t *testing.T) {
	for _, c := range cases64 {
		if got := Format(c.f, 64); got != c.want {
			t.Errorf("Format(%v) = %q, want %q", c.f, got, c.want)
		}
	}
}

// TestAppendJS checks random float64s against testdata/js.txt: the bit
// pattern in hex and String(x), generated with Bun.
func TestAppendJS(t *testing.T) {
	f, err := os.Open("testdata/js.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		hex, want, _ := strings.Cut(sc.Text(), " ")
		bits, err := strconv.ParseUint(hex, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		x := math.Float64frombits(bits)
		if got := Format(x, 64); got != want {
			t.Errorf("Format(%v) = %q, want %q", x, got, want)
		}
		n++
	}
	if n < 500 {
		t.Fatalf("only %d rows", n)
	}
}

// float32 keeps its shortest float32 digits, laid out the JavaScript way
// (String(Number(digits)) in Bun).
func TestAppend32(t *testing.T) {
	for _, c := range []struct {
		f    float32
		want string
	}{
		{0.1, "0.1"},
		{0.3, "0.3"},
		{1e6, "1000000"},
		{1e-7, "1e-7"},
		{1e21, "1e+21"},
		{16777216, "16777216"},
		{123456.79, "123456.79"},
		{math.MaxFloat32, "3.4028235e+38"},
		{math.SmallestNonzeroFloat32, "1e-45"},
		{1.1754944e-38, "1.1754944e-38"},
		{-2.5, "-2.5"},
	} {
		if got := Format(float64(c.f), 32); got != c.want {
			t.Errorf("Format(float32(%v)) = %q, want %q", c.f, got, c.want)
		}
	}
}

// FuzzAppend checks that the text reads back as the same float and uses
// exponent notation exactly outside 1e-6 <= |f| < 1e21 (9.9e-7 prints
// 9.9e-7 in JavaScript).
func FuzzAppend(f *testing.F) {
	for _, v := range []float64{0.1, 1e21, 1e-7, 9.9e-7, 1e-6, 0.0000010000000000000002, 123.456, 5e-324, 1e300} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, v float64) {
		if math.IsNaN(v) || math.IsInf(v, 0) || v == 0 {
			return
		}
		for _, bits := range []int{64, 32} {
			x := v
			if bits == 32 {
				x = float64(float32(v))
				if math.IsInf(x, 0) || x == 0 {
					continue
				}
			}
			s := Format(x, bits)
			back, err := strconv.ParseFloat(strings.Replace(s, "e+", "e", 1), bits)
			if err != nil || back != x {
				t.Fatalf("Format(%v, %d) = %q, reads back as %v (%v)", x, bits, s, back, err)
			}
			// The notation follows the digits printed (float32(1e-6) is
			// just under 1e-6 but prints 0.000001).
			d, _ := strconv.ParseFloat(strings.Replace(s, "e+", "e", 1), 64)
			a := math.Abs(d)
			if exp := strings.Contains(s, "e"); exp != (a < 1e-6 || a >= 1e21) {
				t.Fatalf("Format(%v, %d) = %q: wrong notation", x, bits, s)
			}
		}
	})
}

func TestAppendAllocs(t *testing.T) {
	buf := make([]byte, 0, 64)
	for _, f := range []float64{0.1, 1e6, 1e-7, 1e21, -123.456, 5e-324} {
		if n := testing.AllocsPerRun(100, func() { buf = Append(buf[:0], f, 64) }); n != 0 {
			t.Errorf("Append(%v) allocs = %v, want 0", f, n)
		}
	}
}

func BenchmarkAppend(b *testing.B) {
	buf := make([]byte, 0, 64)
	for _, f := range []float64{0.1, 123.456, 1e6, 1e-7} {
		b.Run(strconv.FormatFloat(f, 'g', -1, 64), func(b *testing.B) {
			for b.Loop() {
				buf = Append(buf[:0], f, 64)
			}
		})
	}
}
