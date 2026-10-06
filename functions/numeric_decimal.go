package functions

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"

	"github.com/recolabs/gnata/internal/decimal"
	"github.com/recolabs/gnata/internal/evaluator"
)

// maxRoundPlaces bounds $round's precision argument; beyond it every in-range
// value is unchanged or rounds to zero.
const maxRoundPlaces = 10000

// maxPictureDigits bounds the digits a $formatNumber picture may ask for, so
// formatting work stays bounded.
const maxPictureDigits = 10000

// decimalFn is a decimal variant of a numeric builtin. A non-nil err is the
// builtin's error, such as an overflow float64 would not detect after rounding
// the operands. Otherwise ok is false when it declines (e.g. an operand out of
// range or an invalid argument), and the float64 builtin runs and reports any
// error.
type decimalFn func(args []any, focus any, prec int) (result any, ok bool, err error)

// withDecimal runs dec when decimal precision is enabled, otherwise fn.
func withDecimal(fn func([]any, any) (any, error), dec decimalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		if prec := env.DecimalPrecision(); prec > 0 {
			if res, ok, err := dec(args, focus, prec); ok || err != nil {
				return res, err
			}
		}
		return fn(args, focus)
	}
}

func errNumberOutOfRange() error {
	return &evaluator.JSONataError{Code: "D1001", Message: "Number out of range"}
}

// decResult turns the outcome of a decimal operation into a decimalFn result:
// ErrOverflow becomes overflowErr, and any other error declines.
func decResult(d decimal.Decimal, err error, prec int, overflowErr func() error) (res any, ok bool, resErr error) {
	switch {
	case err == nil:
		return d.Value(prec), true, nil
	case errors.Is(err, decimal.ErrOverflow):
		return nil, false, overflowErr()
	}
	return nil, false, nil
}

// argOrFocus returns the single optional argument, or the focus without one.
func argOrFocus(args []any, focus any) (any, bool) {
	switch len(args) {
	case 0:
		return focus, true
	case 1:
		return args[0], true
	}
	return nil, false
}

func decNumber(args []any, focus any, prec int) (res any, ok bool, err error) {
	arg, ok := argOrFocus(args, focus)
	if !ok {
		return nil, false, nil
	}
	var s string
	switch v := arg.(type) {
	case json.Number:
		s = v.String()
	case string:
		s = strings.TrimSpace(v)
	default:
		return nil, false, nil
	}
	if base, digitBits := evaluator.RadixPrefix(s); base != 0 {
		// Bound the digits before parsing so a long string cannot become a huge integer.
		if len(s[2:])*digitBits > decimal.MaxIntegerBits {
			return nil, false, nil
		}
		n, ok := new(big.Int).SetString(s[2:], base)
		if !ok || n.Sign() < 0 {
			return nil, false, nil
		}
		d, ok := decimal.FromBig(n, prec)
		return d.Value(prec), ok, nil
	}
	d, ok := decimal.Parse(s, prec)
	return d.Value(prec), ok, nil
}

// decArg returns the first argument as a Decimal. A float64 converts via its
// shortest decimal form, as in arithmetic, so a number gives the same result
// whether it was decoded as float64 or json.Number.
func decArg(args []any, prec int) (decimal.Decimal, bool) {
	if len(args) == 0 {
		return decimal.Decimal{}, false
	}
	return decimal.FromValue(args[0], prec)
}

func decAbs(args []any, _ any, prec int) (res any, ok bool, err error) {
	d, ok := decArg(args, prec)
	return d.Abs().Value(prec), ok, nil
}

func decFloor(args []any, _ any, prec int) (res any, ok bool, err error) {
	d, ok := decArg(args, prec)
	return d.Floor(prec).Value(prec), ok, nil
}

func decCeil(args []any, _ any, prec int) (res any, ok bool, err error) {
	d, ok := decArg(args, prec)
	return d.Ceil(prec).Value(prec), ok, nil
}

func decRound(args []any, _ any, prec int) (res any, ok bool, err error) {
	d, ok := decArg(args, prec)
	if !ok {
		return nil, false, nil
	}
	places := 0
	if len(args) >= 2 && args[1] != nil {
		pf, ok := evaluator.ToFloat64(args[1])
		if !ok {
			return nil, false, nil
		}
		places = int(max(min(pf, maxRoundPlaces), -maxRoundPlaces))
	}
	d, err = d.RoundPlaces(places, prec)
	return decResult(d, err, prec, errNumberOutOfRange)
}

// decNumberArray returns the single non-empty array argument of an aggregate.
func decNumberArray(args []any) ([]any, bool) {
	if len(args) != 1 {
		return nil, false
	}
	arr := toNumberArray(args[0])
	return arr, len(arr) > 0
}

// decTotal sums the single array argument. ok is false when an element is not
// a number in range.
func decTotal(args []any, prec int) (sum decimal.Decimal, n int, ok bool, err error) {
	arr, ok := decNumberArray(args)
	if !ok {
		return sum, 0, false, nil
	}
	for _, v := range arr {
		d, ok := decimal.FromValue(v, prec)
		if !ok {
			return sum, 0, false, nil
		}
		if sum, err = sum.Add(d, prec); err != nil {
			return sum, 0, false, err
		}
	}
	return sum, len(arr), true, nil
}

func decSum(args []any, _ any, prec int) (res any, ok bool, err error) {
	sum, _, ok, err := decTotal(args, prec)
	if !ok && err == nil {
		return nil, false, nil
	}
	return decResult(sum, err, prec, errNumberOutOfRange)
}

func decAverage(args []any, _ any, prec int) (res any, ok bool, err error) {
	sum, n, ok, err := decTotal(args, prec)
	if !ok && err == nil {
		return nil, false, nil
	}
	if err == nil {
		sum, err = sum.Quo(decimal.NewInt(int64(n)), prec)
	}
	return decResult(sum, err, prec, errNumberOutOfRange)
}

// decExtreme returns the largest (want = +1) or smallest (want = -1) element.
func decExtreme(args []any, prec, want int) (res any, ok bool, err error) {
	arr, ok := decNumberArray(args)
	if !ok {
		return nil, false, nil
	}
	best := arr[0]
	for _, v := range arr {
		c, ok := evaluator.DecimalCmp(v, best, prec)
		if !ok {
			return nil, false, nil
		}
		if c == want {
			best = v
		}
	}
	return best, true, nil
}

func decMax(args []any, _ any, prec int) (res any, ok bool, err error) {
	return decExtreme(args, prec, 1)
}

func decMin(args []any, _ any, prec int) (res any, ok bool, err error) {
	return decExtreme(args, prec, -1)
}

// decFormatNumber formats exactly, leaving any error to the float64 builtin.
func decFormatNumber(args []any, _ any, prec int) (res any, ok bool, err error) {
	d, ok := decArg(args, prec)
	if !ok || len(args) < 2 {
		return nil, false, nil
	}
	picture, ok := args[1].(string)
	if !ok {
		return nil, false, nil
	}
	var sp subPicture
	fc, err := numberPicture(&sp, picture, formatNumberOptions(args), d.Sign() < 0)
	ok = err == nil && sp.intMandatory+sp.fracMandatory+sp.fracOptional+sp.intOptional <= maxPictureDigits
	if ok && sp.scale > 0 {
		d, err = d.Mul(decimal.NewInt(scaleMultiplier(sp.scale)), prec)
		ok = err == nil
	}
	if !ok {
		return nil, false, nil
	}
	var result string
	if sp.expMandatory > 0 {
		result, ok = decFormatExponent(d, &sp, fc, prec)
	} else {
		result, err = d.Fixed(sp.fracMandatory+sp.fracOptional, prec)
		result, ok = formatFixed(result, &sp, fc), err == nil
	}
	if !ok {
		return nil, false, nil
	}
	return sp.prefix + applyDigitFamily(result, fc.zeroDigit) + sp.suffix, true, nil
}

// decFormatExponent is formatWithExponent with an exact mantissa, rounded half
// to even.
func decFormatExponent(d decimal.Decimal, sp *subPicture, fc fmtChars, prec int) (string, bool) {
	fracSig := expFracDigits(sp)
	exp := 0
	if d.Sign() != 0 {
		exp = d.Adjusted() + 1 - sp.intMandatory // mantissa below 10^N, or 1 when N is 0
	}
	m, ok := d.Shift(-exp)
	if !ok {
		return "", false
	}
	m, err := m.RoundPlaces(fracSig, prec)
	limit, limitOK := decimal.NewInt(1).Shift(sp.intMandatory)
	if err != nil || !limitOK {
		return "", false
	}
	if m.Abs().Cmp(limit) >= 0 { // rounding carried, e.g. 9.96 → 10.0
		m, _ = m.Shift(-1) // exact, as m is at least 1
		exp++
	}
	s, err := m.Fixed(fracSig, prec)
	return formatExponent(s, exp, sp, fc), err == nil
}

// decFormatBase formats exactly, rounding half to even as jsonata-js does, and
// leaving any error to the float64 builtin.
func decFormatBase(args []any, _ any, prec int) (res any, ok bool, err error) {
	d, ok := decArg(args, prec)
	if !ok {
		return nil, false, nil
	}
	base, ok := formatBaseArg(args)
	if !ok || base < 2 || base > 36 {
		return nil, false, nil
	}
	s, err := d.Fixed(0, prec)
	if ok = err == nil; !ok {
		return nil, false, nil
	}
	n, _ := new(big.Int).SetString(s, 10)
	if d.Sign() < 0 {
		n.Neg(n)
	}
	return n.Text(base), true, nil
}

// decPower computes $power as the ** operator does.
func decPower(args []any, _ any, prec int) (res any, ok bool, err error) {
	if len(args) < 2 {
		return nil, false, nil
	}
	res, err = evaluator.DecimalArith(args[0], args[1], "**", prec)
	switch {
	case err == nil:
		return res, true, nil
	case errors.Is(err, decimal.ErrOverflow):
		return nil, false, &evaluator.JSONataError{Code: "D3061", Message: "$power: result is non-finite"}
	}
	return nil, false, nil
}

func decSqrt(args []any, _ any, prec int) (res any, ok bool, err error) {
	d, ok := decArg(args, prec)
	if !ok {
		return nil, false, nil
	}
	d, err = d.Sqrt(prec)
	return decResult(d, err, prec, errNumberOutOfRange)
}

// decString lays out numbers, including those inside arrays and objects, as
// JavaScript does but with every significant digit.
func decString(args []any, focus any, prec int) (res any, ok bool, err error) {
	res, err = stringify(args, focus, prec)
	return res, true, err
}
