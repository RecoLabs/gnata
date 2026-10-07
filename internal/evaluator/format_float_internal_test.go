package evaluator

import (
	"math"
	"math/rand/v2"
	"strconv"
	"testing"
)

// toPrecision15String is FormatFloat by its definition: a non-integer
// rounded as Number(n.toPrecision(15)), then written as JSON.stringify does.
func toPrecision15String(n float64) string {
	if n == 0 {
		return "0"
	}
	if n != math.Trunc(n) {
		abs := math.Abs(n)
		if isPrecision15Tie(abs) {
			abs = math.Nextafter(abs, math.Inf(1))
		}
		rounded, err := strconv.ParseFloat(strconv.FormatFloat(abs, 'g', 15, 64), 64)
		if err != nil {
			panic(err)
		}
		n = math.Copysign(rounded, n)
	}
	b, err := appendJSONFloat(nil, n, 64)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestFormatFloatMatchesToPrecision15(t *testing.T) {
	cases := []float64{
		0.1 + 0.2, 1234.5678, 0.5, -0.5, 1, -1, 100, 1e21, 9.99999999999999e20, 1e-6, 9.99999e-7,
		1e-7, 1.5e-7, 123456789012345678, 0.123456789012345, 0.1234567890123456, 1.0000000000000002,
		2.9999999999999996, 0.000001234567890123456, 5e-324, math.MaxFloat64, -math.SmallestNonzeroFloat64,
		0.30000000000000004, 1.234567890123455, 8.5, 1.2345678901234567e-5, 4.35, 1.005,
		-1.4140766451707103e-308, 2.2250738585072009e-308,
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 200_000 {
		var n float64
		switch rng.IntN(3) {
		case 0:
			n = math.Float64frombits(rng.Uint64())
		case 1:
			n = float64(rng.Int64N(1e12)) / math.Pow10(rng.IntN(16))
		default:
			n = (rng.Float64() - 0.5) * math.Pow10(rng.IntN(50)-25)
		}
		if !math.IsNaN(n) && !math.IsInf(n, 0) {
			cases = append(cases, n)
		}
	}
	for _, n := range cases {
		if got, want := FormatFloat(n), toPrecision15String(n); got != want {
			t.Fatalf("FormatFloat(%v) = %q, want %q", n, got, want)
		}
		if got, want := roundJS(n), roundJSByDefinition(n); got != want {
			t.Fatalf("roundJS(%v) = %v, want %v", n, got, want)
		}
	}
}

func roundJSByDefinition(n float64) float64 {
	rounded, err := strconv.ParseFloat(toPrecision15String(n), 64)
	if err != nil {
		panic(err)
	}
	return rounded
}
