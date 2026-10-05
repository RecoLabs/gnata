package decimal_test

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/recolabs/gnata/internal/decimal"
)

const p16 = 16

const u256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"

// str formats a conversion at p16 digits for comparison, with "!" for ok=false.
func str(d decimal.Decimal, ok bool) string {
	if !ok {
		return "!"
	}
	return d.String(p16)
}

// strErr formats an operation for comparison, with "overflow" for ErrOverflow
// and "!" for ErrUndefined.
func strErr(d decimal.Decimal, err error, prec int) string {
	switch {
	case errors.Is(err, decimal.ErrOverflow):
		return "overflow"
	case err != nil:
		return "!"
	}
	return d.String(prec)
}

func parse(t *testing.T, s string, prec int) decimal.Decimal {
	t.Helper()
	d, ok := decimal.Parse(s, prec)
	if !ok {
		t.Fatalf("Parse(%q) failed", s)
	}
	return d
}

func TestParse(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"0", "0"},
		{"-0", "0"},
		{"+2", "2"},
		{"001", "1"},
		{"1.50", "1.5"},
		{".5", "0.5"},
		{"5.", "5"},
		{"1e3", "1000"},
		{"1E-3", "0.001"},
		{"0.0000001", "0.0000001"},
		{"1e-17", "1e-17"},
		{"1.5e-20", "1.5e-20"},
		{"1e15", "1000000000000000"},
		{"1e16", "1e+16"},
		{"0.1234567890123456789", "0.1234567890123457"},
		{"12345678901234565", "1.234567890123456e+16"},
		{"12345678901234575", "1.234567890123458e+16"},
		{"1234567890123456500001", "1.234567890123457e+21"},
		{"1234567890123456500000", "1.234567890123456e+21"},
		{"1.23456789012345650000000001", "1.234567890123457"},
		{"99999999999999999", "1e+17"},
		{"9999999999999999.5", "1e+16"},
		{"1e308", "1e+308"},
		{"5e-324", "5e-324"},
		{"2.48e-324", "2.48e-324"},
		{"2.47e-324", "0"}, // float64 rounds it to zero
		{"1e-324", "0"},
		{"9e-325", "0"},
		{"1e-400", "0"},
		{"1e-999999999999", "0"},
		{"1e309", "!"},
		{"1e999999999999", "!"},
		{"1e4294967296", "!"}, // wraps to 1e0 in a 32-bit int
		{"1e-4294967296", "0"},
		{"", "!"},
		{"-", "!"},
		{".", "!"},
		{"e5", "!"},
		{"1e", "!"},
		{"1e+", "!"},
		{"1.2.3", "!"},
		{"1_000", "!"},
		{"0x10", "!"},
		{"inf", "!"},
		{"nan", "!"},
	} {
		d, ok := decimal.Parse(c.in, p16)
		if got := str(d, ok); got != c.want {
			t.Errorf("Parse(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestArith(t *testing.T) {
	for _, c := range []struct {
		prec           int
		x, op, y, want string
	}{
		{16, "0.1", "+", "0.2", "0.3"},
		{16, "5", "+", "0", "5"},
		{16, "0", "+", "5", "5"},
		{16, "5", "-", "5", "0"},
		{16, "9999999999999999", "+", "1", "1e+16"},
		{16, "1e100", "+", "1", "1e+100"},
		{16, "1", "+", "1e100", "1e+100"},
		{16, "-1e100", "+", "1", "-1e+100"},
		{16, "1e308", "*", "10", "overflow"},
		{16, "1e-200", "*", "1e-200", "0"},
		{16, "1.5", "*", "-2", "-3"},
		{16, "1", "/", "3", "0.3333333333333333"},
		{16, "2", "/", "3", "0.6666666666666667"},
		{16, "10", "/", "4", "2.5"},
		{16, "0", "/", "5", "0"},
		{16, "1", "/", "0", "!"},
		{16, "1e-300", "/", "1e100", "0"},
		{16, "-7", "%", "3", "-1"},
		{16, "7", "%", "-3", "1"},
		{16, "5.5", "%", "2", "1.5"},
		{16, "0", "%", "3", "0"},
		{16, "1e15", "%", "7", "6"},
		{16, "1e20", "%", "3", "1"},
		{40, "1e50", "%", "7", "2"},
		{16, "1e308", "*", "2", "overflow"},
		{17, "1.7976931348623157e308", "+", "0", "1.7976931348623157e+308"},
		{17, "1.7976931348623157e308", "*", "1.0000000000000001", "overflow"},
		{16, "2", "**", "0.5", "1.414213562373095"},
		{30, "1.0000000000000000001", "**", "1e22", "overflow"}, // float64 rounds the base to 1
		{30, "0.9999999999999999999", "**", "1e300", "0"},
		{30, "1.7976931348623158079e308", "*", "1.0000000000000000001", "overflow"},
		{30, "1.7976931348623158079e308", "+", "1e289", "overflow"},
		{16, "-8", "**", "0.5", "!"},
		{16, "0", "**", "-1", "overflow"},
		{16, "1", "%", "0", "!"},
		{16, "2", "**", "10", "1024"},
		{16, "2", "**", "-2", "0.25"},
		{16, "-2", "**", "3", "-8"},
		{16, "1.1", "**", "2", "1.21"},
		{16, "7", "**", "0", "1"},
		{16, "2", "**", "64", "1.844674407370955e+19"},
		{16, "0", "**", "-1", "overflow"},
		{16, "10", "**", "400", "overflow"},
		{16, "10", "**", "513", "overflow"},
		{16, "1", "**", "-9223372036854775808", "1"},
		{20, "9223372036854775807", "+", "1", "9223372036854775808"},
		{20, "-9223372036854775807", "-", "1", "-9223372036854775808"},
		{20, "4611686018427387904", "*", "2", "9223372036854775808"},
		{20, "99999999999999999999", "*", "3", "3e+20"},
		{20, "1", "-", "12345678901234567891", "-12345678901234567890"},
		{78, u256, "+", "1", "115792089237316195423570985008687907853269984665640564039457584007913129639936"},
		{78, u256, "*", u256, "1.34078079299425970995740249982058461274793658205923933777235614437217640300733e+154"},
		{78, "1", "/", "7", "0.142857142857142857142857142857142857142857142857142857142857142857142857142857"},
	} {
		x, y := parse(t, c.x, c.prec), parse(t, c.y, c.prec)
		var z decimal.Decimal
		var err error
		switch c.op {
		case "+":
			z, err = x.Add(y, c.prec)
		case "-":
			z, err = x.Sub(y, c.prec)
		case "*":
			z, err = x.Mul(y, c.prec)
		case "/":
			z, err = x.Quo(y, c.prec)
		case "%":
			z, err = x.Rem(y, c.prec)
		case "**":
			z, err = x.Pow(y, c.prec)
		}
		if got := strErr(z, err, c.prec); got != c.want {
			t.Errorf("%s %s %s = %s, want %s", c.x, c.op, c.y, got, c.want)
		}
	}
}

func TestCmp(t *testing.T) {
	for _, c := range []struct {
		x, y string
		want int
	}{
		{"1", "2", -1},
		{"2", "1", 1},
		{"0.1", "0.10", 0},
		{"123", "1.23e2", 0},
		{"0", "-0", 0},
		{"-1", "-2", 1},
		{"1e100", "1", 1},
		{"-1e100", "1", -1},
		{"1e-5", "1e-6", 1},
		{"1e-6", "1e-5", -1},
		{"1e-5", "-1", 1},
		{"9.5", "9.000000000000000001", 1},
		{"100000000000000000000000000001", "100000000000000000000000000002", -1},
	} {
		if got := parse(t, c.x, 78).Cmp(parse(t, c.y, 78)); got != c.want {
			t.Errorf("Cmp(%s, %s) = %d, want %d", c.x, c.y, got, c.want)
		}
	}
}

func TestIntegers(t *testing.T) {
	for _, c := range []struct{ in, floor, ceil string }{
		{"2", "2", "2"},
		{"1.5", "1", "2"},
		{"-1.5", "-2", "-1"},
		{"0.1", "0", "1"},
		{"-0.1", "-1", "0"},
		{"1e-20", "0", "1"},
		{"1e20", "1e+20", "1e+20"},
	} {
		d := parse(t, c.in, p16)
		if got := d.Floor(p16).String(p16); got != c.floor {
			t.Errorf("Floor(%s) = %s, want %s", c.in, got, c.floor)
		}
		if got := d.Ceil(p16).String(p16); got != c.ceil {
			t.Errorf("Ceil(%s) = %s, want %s", c.in, got, c.ceil)
		}
	}
	for _, c := range []struct {
		in     string
		places int
		want   string
	}{
		{"2.5", 0, "2"},
		{"3.5", 0, "4"},
		{"-2.5", 0, "-2"},
		{"1.25", 1, "1.2"},
		{"1.35", 1, "1.4"},
		{"1.5", 5, "1.5"},
		{"0.004", 2, "0"},
		{"1250", -2, "1200"},
		{"1350", -2, "1400"},
		{"5", -1, "0"},
		{"15", -1, "20"},
		{"1", -5, "0"},
	} {
		d, err := parse(t, c.in, p16).RoundPlaces(c.places, p16)
		if got := strErr(d, err, p16); got != c.want {
			t.Errorf("RoundPlaces(%s, %d) = %s, want %s", c.in, c.places, got, c.want)
		}
	}
}

func TestFixed(t *testing.T) {
	for _, c := range []struct {
		in     string
		places int
		want   string
	}{
		{"1.5", 3, "1.500"},
		{"0.125", 2, "0.12"},
		{"0.135", 2, "0.14"},
		{"-2.5", 0, "2"},
		{"123", 0, "123"},
		{"0", 2, "0.00"},
		{"0.004", 2, "0.00"},
		{"0.006", 2, "0.01"},
		{"1e-5", 7, "0.0000100"},
		{"1e20", 1, "100000000000000000000.0"},
		{"12345678901234567.89", 2, "12345678901234570.00"},
	} {
		got, err := parse(t, c.in, p16).Fixed(c.places, p16)
		if err != nil || got != c.want {
			t.Errorf("Fixed(%s, %d) = %s, %v, want %s", c.in, c.places, got, err, c.want)
		}
	}
	if _, ok := decimal.Parse("1.7976931348623157e308", 16); ok {
		t.Error("Parse(math.MaxFloat64) at 16 digits rounds above math.MaxFloat64, want overflow")
	}
	if _, ok := decimal.Parse(strings.Repeat("9", 309)+".5", 1000); ok {
		t.Error("Parse(1e309 - 0.5) succeeded, want overflow above math.MaxFloat64")
	}
	for _, c := range []struct {
		in     string
		k, adj int
		want   string
	}{
		{"123", -2, 2, "1.23"},
		{"0.05", 2, -2, "5"},
		{"0", 400, 0, "0"},
		{"1e308", 1, 308, "!"},
		{"5e-324", -1, -324, "!"},
	} {
		d := parse(t, c.in, p16)
		if got := d.Adjusted(); got != c.adj {
			t.Errorf("Adjusted(%s) = %d, want %d", c.in, got, c.adj)
		}
		s, ok := d.Shift(c.k)
		if got := str(s, ok); got != c.want {
			t.Errorf("Shift(%s, %d) = %s, want %s", c.in, c.k, got, c.want)
		}
	}
}

func TestConvert(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
	}{
		{0.1, "0.1"},
		{-2.0, "-2"},
		{1e21, "1e+21"},
		{9007199254740993.0, "9007199254740992"},
		{math.Inf(1), "!"},
		{math.NaN(), "!"},
		{json.Number("1.50"), "1.5"},
		{json.Number("x"), "!"},
		{"1", "!"},
		{1, "!"},
	} {
		d, ok := decimal.FromValue(c.in, p16)
		if got := str(d, ok); got != c.want {
			t.Errorf("FromValue(%v) = %s, want %s", c.in, got, c.want)
		}
	}
	n, _ := new(big.Int).SetString("123456789012345678", 10)
	if d, ok := decimal.FromBig(n, p16); str(d, ok) != "1.234567890123457e+17" {
		t.Errorf("FromBig = %s", str(d, ok))
	}
	if got := decimal.NewInt(-5).Abs().Neg().Value(p16); got != "-5" {
		t.Errorf("NewInt(-5).Abs().Neg() = %s", got)
	}
	if got := decimal.NewInt(5).Abs().Value(p16); got != "5" {
		t.Errorf("NewInt(5).Abs() = %s", got)
	}
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"0", true},
		{"-12", true},
		{"1.5", true},
		{"0.5", true},
		{"1234567890123456", true},
		{"-0", false},
		{"1.50", false},
		{"01", false},
		{".5", false},
		{"1.", false},
		{"1e3", false},
		{"12345678901234567", false},
		{"", false},
		{"-", false},
	} {
		if got := decimal.IsCanonical(c.in, p16); got != c.want {
			t.Errorf("IsCanonical(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestJSString(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"0", "0"},
		{"1e21", "1e+21"},
		{"1e20", "100000000000000000000"},
		{"-1.5e-7", "-1.5e-7"},
		{"0.000001", "0.000001"},
		{"1.2345678901234567e17", "123456789012345670"},
		{u256 + "000", "1.15792089237316195423570985008687907853269984665640564039457584007913129639935e+80"},
	} {
		if got := parse(t, c.in, 78).JSString(); got != c.want {
			t.Errorf("JSString(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestSqrt(t *testing.T) {
	for _, c := range []struct {
		prec     int
		in, want string
	}{
		{16, "2", "1.414213562373095"},
		{40, "2", "1.41421356237309504880168872420969807857"},
		{16, "1e-300", "1e-150"},
		{16, "0", "0"},
		{16, "-1", "!"},
	} {
		z, err := parse(t, c.in, c.prec).Sqrt(c.prec)
		if got := strErr(z, err, c.prec); got != c.want {
			t.Errorf("Sqrt(%s) at %d = %s, want %s", c.in, c.prec, got, c.want)
		}
	}
}
