// Package decimal implements the decimal floating-point arithmetic used by
// WithDecimalPrecision on top of the General Decimal Arithmetic implementation
// in internal/third_party/apd, with the context precision set by the caller
// and rounding round-half-even.
//
// It narrows apd to what JSONata numbers can express:
//   - there are no special values (infinities, NaNs or negative zero): a result
//     beyond the float64 range is ErrOverflow, and division by zero or an
//     invalid operation is ErrUndefined, so the caller can fall back to float64,
//     which reports the same error;
//   - magnitudes are limited to those of float64: anything float64 would round
//     to infinity overflows, and anything it would round to zero is zero;
//   - String formats as JavaScript does (1e+21) rather than to-scientific-string.
//
// All work is bounded by the precision and the length of the input: parsing
// keeps at most prec+1 significant digits and caps the exponent, and every
// apd context limits exponents to just beyond the float64 range.
package decimal

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/recolabs/gnata/internal/third_party/apd"
)

// MinPrecision and MaxPrecision bound the significant digits accepted by
// WithDecimalPrecision. 17 digits hold every float64 exactly, so converting
// one never rounds; 100 covers uint256 (78) and keeps fractional powers, the
// costliest operation, to about a millisecond.
const (
	MinPrecision = 17
	MaxPrecision = 100
)

// MaxIntegerBits bounds integers parsed from hex, binary and octal. It is above
// the float64 range, so anything longer always overflows.
const MaxIntegerBits = 1028

// Adjusted exponents (that of the most significant digit, e.g. 2 for 123) are
// limited to the range of float64: above maxExp is an overflow, below minExp is
// zero. The apd contexts allow a little more so that results just outside the
// range are still finite and can be checked exactly.
const (
	maxExp    = 308
	minExp    = -324
	ctxMaxExp = maxExp + 2
	ctxMinExp = minExp - 2
)

// maxParseExp caps the magnitude of a parsed exponent. Anything larger already
// overflows or underflows, and the cap keeps it well within an int32.
const maxParseExp = 1_000_000_000

// undefined lists the apd conditions that mean an operation has no result.
const undefined = apd.DivisionByZero | apd.DivisionImpossible | apd.DivisionUndefined | apd.InvalidOperation

var (
	// ErrOverflow reports a result beyond the range of float64.
	ErrOverflow = errors.New("decimal: overflow")
	// ErrUndefined reports an operation with no result, such as division by
	// zero or a negative number to a fractional power.
	ErrUndefined = errors.New("decimal: undefined")
)

// overflow is 2^1024 - 2^970, the smallest magnitude float64 rounds to
// infinity: everything below it rounds to at most math.MaxFloat64.
var overflow = func() apd.Decimal {
	t := new(big.Int).Lsh(big.NewInt(1), 1024)
	t.Sub(t, new(big.Int).Lsh(big.NewInt(1), 970))
	return *apd.NewWithBigInt(new(apd.BigInt).SetMathBigInt(t), 0)
}()

// underflow is 2^-1075, half the smallest float64 subnormal: float64 rounds
// every magnitude up to it to zero.
var underflow = func() apd.Decimal {
	five := new(big.Int).Exp(big.NewInt(5), big.NewInt(1075), nil)
	return *apd.NewWithBigInt(new(apd.BigInt).SetMathBigInt(five), -1075)
}()

// Decimal is a finite decimal number. Operations never modify their operands,
// so values may share apd coefficients.
type Decimal struct {
	d apd.Decimal
}

func newContext(prec int) *apd.Context {
	return &apd.Context{
		Precision:   uint32(prec),
		MaxExponent: ctxMaxExp,
		MinExponent: ctxMinExp,
		Rounding:    apd.RoundHalfEven,
	}
}

// result applies the float64 range to the outcome of an apd operation.
func result(z *apd.Decimal, cond apd.Condition, err error) (Decimal, error) {
	switch {
	case cond&undefined != 0:
		return Decimal{}, ErrUndefined
	case cond&apd.Overflow != 0 || z.Form == apd.Infinite:
		return Decimal{}, ErrOverflow
	case err != nil && cond&apd.Underflow != 0:
		// apd gives up on a result too small for its exponent range.
		return Decimal{}, nil
	case err != nil || z.Form != apd.Finite:
		return Decimal{}, ErrUndefined
	case z.IsZero():
		return Decimal{}, nil
	}
	switch adj := adjusted(z); {
	case adj > maxExp || adj == maxExp && absCmp(z, &overflow) >= 0:
		return Decimal{}, ErrOverflow
	case adj < minExp || adj == minExp && absCmp(z, &underflow) <= 0:
		return Decimal{}, nil
	}
	return Decimal{d: *z}, nil
}

// parsed is result for a conversion, which reports only whether it succeeded.
func parsed(z *apd.Decimal, cond apd.Condition, err error) (Decimal, bool) {
	d, err := result(z, cond, err)
	return d, err == nil
}

func absCmp(x, y *apd.Decimal) int {
	var a apd.Decimal
	return a.Abs(x).Cmp(y)
}

func adjusted(d *apd.Decimal) int {
	return int(d.Exponent) + int(d.NumDigits()) - 1
}

// NewInt returns n as a Decimal.
func NewInt(n int64) Decimal {
	return Decimal{d: *apd.New(n, 0)}
}

// Sign returns -1, 0 or +1.
func (d Decimal) Sign() int {
	return d.d.Sign()
}

// Parse parses a decimal number in the syntax of strconv.ParseFloat (without
// hex, inf, nan or underscores) and rounds it to prec digits. ok is false for
// invalid syntax or overflow. Digits beyond prec+1 only record whether any are
// nonzero, and the exponent is capped, so the work is linear in len(s) and the
// result bounded.
func Parse[S string | []byte](s S, prec int) (Decimal, bool) {
	i, neg := 0, false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	var (
		digits                []byte
		scale                 int64
		sticky, digit, dotted bool
	)
	for ; i < len(s); i++ {
		c := s[i]
		if c == '.' && !dotted {
			dotted = true
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		digit = true
		switch {
		case len(digits) == 0 && c == '0':
			if dotted {
				scale--
			}
		case len(digits) <= prec:
			if dotted {
				scale--
			}
			digits = append(digits, c)
		default:
			sticky = sticky || c != '0'
			if !dotted {
				scale++
			}
		}
	}
	if !digit {
		return Decimal{}, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		e, ok := parseExp(s[i+1:])
		if !ok {
			return Decimal{}, false
		}
		scale += e
	} else if i != len(s) {
		return Decimal{}, false
	}
	if len(digits) == 0 {
		return Decimal{}, true
	}
	if sticky {
		// One nonzero digit below those kept stands in for all the discarded
		// ones: it breaks an apparent tie without changing any other rounding.
		digits = append(digits, '1')
		scale--
	}
	// Past these bounds the value certainly overflows or is zero.
	scale = max(min(scale, 2*maxParseExp), -2*maxParseExp)
	if adj := scale + int64(len(digits)) - 1; adj > maxExp+1 {
		return Decimal{}, false
	} else if adj < minExp-1 {
		return Decimal{}, true
	}
	var coeff apd.BigInt
	coeff.SetString(string(digits), 10)
	z := apd.NewWithBigInt(&coeff, int32(scale))
	z.Negative = neg
	cond, err := roundTo(z, prec)
	return parsed(z, cond, err)
}

func roundTo(z *apd.Decimal, prec int) (apd.Condition, error) {
	return newContext(prec).Round(z, z)
}

// parseExp parses a signed exponent, capping its magnitude at maxParseExp.
func parseExp[S string | []byte](s S) (int64, bool) {
	i, neg := 0, false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	var e int64
	start := i
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		e = min(e*10+int64(s[i]-'0'), maxParseExp)
	}
	if i == start || i != len(s) {
		return 0, false
	}
	if neg {
		return -e, true
	}
	return e, true
}

// FromValue converts a float64 or json.Number to a Decimal rounded to prec
// digits. A float64 converts via its shortest decimal form, so 0.1 stays 0.1.
func FromValue(v any, prec int) (Decimal, bool) {
	switch n := v.(type) {
	case json.Number:
		return Parse(string(n), prec)
	case float64:
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return Decimal{}, false
		}
		if n == math.Trunc(n) && math.Abs(n) <= 1<<53 {
			return NewInt(int64(n)), true
		}
		var buf [32]byte
		return Parse(strconv.AppendFloat(buf[:0], n, 'g', -1, 64), prec)
	}
	return Decimal{}, false
}

// FromBig returns the integer n rounded to prec digits.
func FromBig(n *big.Int, prec int) (Decimal, bool) {
	z := apd.NewWithBigInt(new(apd.BigInt).SetMathBigInt(n), 0)
	cond, err := roundTo(z, prec)
	return parsed(z, cond, err)
}

// IsCanonical reports whether s is already in the form String returns for
// prec, so that it can be used as is. It lets common literals skip a parse and
// format on every evaluation.
func IsCanonical(s string, prec int) bool {
	neg := s != "" && s[0] == '-'
	if neg {
		s = s[1:]
	}
	intPart, frac, dotted := s, "", false
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac, dotted = s[:i], s[i+1:], true
	}
	n := len(intPart) + len(frac)
	switch {
	case intPart == "" || !allDigits(intPart) || !allDigits(frac):
		return false
	case dotted && (frac == "" || frac[len(frac)-1] == '0'):
		return false
	case intPart[0] == '0' && (len(intPart) > 1 || !dotted):
		return s == "0" && !neg
	case intPart == "0":
		n--
	}
	return n <= prec
}

func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// String returns d without trailing zeros, in plain digits when
// -prec <= adjusted exponent < prec and otherwise in exponent form as in
// JavaScript (1.5e+80).
func (d Decimal) String(prec int) string {
	return d.format(-prec, prec)
}

// JSString returns d as JavaScript's Number.prototype.toString lays it out:
// plain digits when -6 <= adjusted exponent < 21 and otherwise exponent form,
// but with every significant digit of d.
func (d Decimal) JSString() string {
	return d.format(-6, 21)
}

// format writes d without trailing zeros, in plain digits when lo <= adjusted
// exponent < hi and otherwise in exponent form.
func (d Decimal) format(lo, hi int) string {
	if d.Sign() == 0 {
		return "0"
	}
	var r apd.Decimal
	r.Reduce(&d.d)
	form := byte('f')
	if adj := adjusted(&r); adj >= hi || adj < lo {
		form = 'e'
	}
	var buf [48]byte
	return string(r.Append(buf[:0], form))
}

// Value returns d as a canonical json.Number.
func (d Decimal) Value(prec int) json.Number {
	return json.Number(d.String(prec))
}

// Neg returns -d.
func (d Decimal) Neg() Decimal {
	var z apd.Decimal
	z.Neg(&d.d)
	return Decimal{d: z}
}

// Abs returns |d|.
func (d Decimal) Abs() Decimal {
	var z apd.Decimal
	z.Abs(&d.d)
	return Decimal{d: z}
}

// Cmp compares x and y, returning -1, 0 or +1.
func (x Decimal) Cmp(y Decimal) int {
	return x.d.Cmp(&y.d)
}

// Add returns x + y rounded to prec digits.
func (x Decimal) Add(y Decimal, prec int) (Decimal, error) {
	var z apd.Decimal
	cond, err := newContext(prec).Add(&z, &x.d, &y.d)
	return result(&z, cond, err)
}

// Sub returns x - y rounded to prec digits.
func (x Decimal) Sub(y Decimal, prec int) (Decimal, error) {
	var z apd.Decimal
	cond, err := newContext(prec).Sub(&z, &x.d, &y.d)
	return result(&z, cond, err)
}

// Mul returns x × y rounded to prec digits.
func (x Decimal) Mul(y Decimal, prec int) (Decimal, error) {
	var z apd.Decimal
	cond, err := newContext(prec).Mul(&z, &x.d, &y.d)
	return result(&z, cond, err)
}

// Quo returns x / y rounded to prec digits, or ErrUndefined for a zero divisor.
func (x Decimal) Quo(y Decimal, prec int) (Decimal, error) {
	var z apd.Decimal
	cond, err := newContext(prec).Quo(&z, &x.d, &y.d)
	return result(&z, cond, err)
}

// Rem returns the remainder of x / y truncated toward zero, so its sign
// follows x, or ErrUndefined for a zero divisor.
func (x Decimal) Rem(y Decimal, prec int) (Decimal, error) {
	if y.Sign() == 0 {
		return Decimal{}, ErrUndefined
	}
	if x.Sign() == 0 {
		return Decimal{}, nil
	}
	// The remainder is exact whenever the integer quotient fits in the working
	// precision; the exponent range bounds how many digits that can need.
	wp := max(prec, adjusted(&x.d)-adjusted(&y.d)+1)
	var z apd.Decimal
	if _, err := newContext(wp).Rem(&z, &x.d, &y.d); err != nil {
		return Decimal{}, ErrUndefined
	}
	cond, err := roundTo(&z, prec)
	return result(&z, cond, err)
}

// Pow returns x**y rounded to prec digits, or ErrUndefined for 0 to a negative
// power or a negative number to a fractional power.
func (x Decimal) Pow(y Decimal, prec int) (Decimal, error) {
	var z apd.Decimal
	cond, err := newContext(prec).Pow(&z, &x.d, &y.d)
	return result(&z, cond, err)
}

// Sqrt returns the square root of d rounded to prec digits, or ErrUndefined
// for a negative number.
func (d Decimal) Sqrt(prec int) (Decimal, error) {
	var z apd.Decimal
	cond, err := newContext(prec).Sqrt(&z, &d.d)
	return result(&z, cond, err)
}

// Int64 returns d as an int64 when it is an integer in range.
func (d Decimal) Int64() (int64, bool) {
	n, err := d.d.Int64()
	return n, err == nil
}

// Floor returns the greatest integer not above d.
func (d Decimal) Floor(prec int) Decimal {
	var z apd.Decimal
	if _, err := newContext(prec).Floor(&z, &d.d); err != nil {
		return d
	}
	return Decimal{d: z}
}

// Ceil returns the least integer not below d.
func (d Decimal) Ceil(prec int) Decimal {
	var z apd.Decimal
	if _, err := newContext(prec).Ceil(&z, &d.d); err != nil {
		return d
	}
	return Decimal{d: z}
}

// RoundPlaces rounds d half to even to the given number of decimal places,
// which may be negative. places must be bounded by the caller.
func (d Decimal) RoundPlaces(places, prec int) (Decimal, error) {
	if d.Sign() == 0 || -places <= int(d.d.Exponent) {
		return d, nil
	}
	if -places > adjusted(&d.d)+1 {
		return Decimal{}, nil
	}
	// Quantizing keeps at most the digits d already has, plus a carry.
	var z apd.Decimal
	c := newContext(max(prec, int(d.d.NumDigits())+1))
	c.MinExponent, c.MaxExponent = apd.MinExponent, apd.MaxExponent
	if _, err := c.Quantize(&z, &d.d, int32(-places)); err != nil {
		return Decimal{}, ErrUndefined
	}
	cond, err := roundTo(&z, prec)
	return result(&z, cond, err)
}

// Adjusted returns the exponent of the most significant digit of d, e.g. 2 for
// 123, and 0 for zero.
func (d Decimal) Adjusted() int {
	if d.Sign() == 0 {
		return 0
	}
	return adjusted(&d.d)
}

// Shift returns d × 10^k exactly. ok is false if the result is outside the
// exponent range.
func (d Decimal) Shift(k int) (Decimal, bool) {
	if d.Sign() == 0 {
		return Decimal{}, true
	}
	if adj := adjusted(&d.d) + k; adj > maxExp || adj < minExp {
		return Decimal{}, false
	}
	var z apd.Decimal
	z.Set(&d.d)
	z.Exponent += int32(k)
	return Decimal{d: z}, true
}

// Fixed returns |d| rounded half to even to places decimal places, in plain
// digits with exactly places fraction digits, as strconv.FormatFloat does with
// 'f'. places must be non-negative and bounded by the caller.
func (d Decimal) Fixed(places, prec int) (string, error) {
	r, err := d.Abs().RoundPlaces(places, prec)
	if err != nil {
		return "", err
	}
	s := r.d.Text('f')
	frac := max(-int(r.d.Exponent), 0)
	if places > 0 && frac == 0 {
		s += "."
	}
	return s + strings.Repeat("0", places-frac), nil
}
