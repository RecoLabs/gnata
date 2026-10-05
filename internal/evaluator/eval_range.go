package evaluator

import (
	"fmt"
	"math"

	"github.com/recolabs/gnata/internal/decimal"
)

// maxRange caps the items the range operator produces.
const maxRange = 10_000_000

func evalRange(left, right any, env *Environment) (any, error) {
	if prec := env.DecimalPrecision(); prec > 0 {
		if res, ok, err := decimalRange(left, right, env, prec); ok {
			return res, err
		}
	}
	var lnOK, rnOK bool
	var ln, rn float64
	if left != nil {
		ln, lnOK = ToFloat64(left)
		if !lnOK {
			return nil, &JSONataError{Code: "T2003", Message: fmt.Sprintf("left side of range operator (..) must be an integer, got %T", left)}
		}
		if ln != math.Trunc(ln) || math.IsInf(ln, 0) {
			return nil, &JSONataError{Code: "T2003", Message: fmt.Sprintf("left side of range operator (..) must be an integer, got %v", ln)}
		}
	}
	if right != nil {
		rn, rnOK = ToFloat64(right)
		if !rnOK {
			return nil, &JSONataError{Code: "T2004", Message: fmt.Sprintf("right side of range operator (..) must be an integer, got %T", right)}
		}
		if rn != math.Trunc(rn) || math.IsInf(rn, 0) {
			return nil, &JSONataError{Code: "T2004", Message: fmt.Sprintf("right side of range operator (..) must be an integer, got %v", rn)}
		}
	}
	if left == nil || right == nil {
		return nil, nil
	}
	// Range math stays in float64, like jsonata-js: the bounds need not fit an
	// int (which is 32 bits on some wasm targets), and only the item count,
	// already capped below, is converted.
	if ln > rn {
		return nil, nil
	}
	span := rn - ln
	if err := checkRangeSpan(span, env); err != nil {
		return nil, err
	}
	n := int(span) + 1
	result := make([]any, n)
	for i := range n {
		if i%10000 == 0 {
			if err := env.Err(); err != nil {
				return nil, err
			}
		}
		result[i] = ln + float64(i)
	}
	return result, nil
}

// checkRangeSpan applies the sequence guardrail and the range cap to a range
// of span+1 items.
func checkRangeSpan(span float64, env *Environment) error {
	if err := env.CheckSequence(ToIntClamped(span + 1)); err != nil {
		return err
	}
	if span >= maxRange {
		return &JSONataError{Code: "D2014", Message: fmt.Sprintf("range operator (..) must not exceed %d items", maxRange)}
	}
	return nil
}

// decimalRange is the range operator under decimal precision, so that bounds
// beyond 2^53 are exact. ok is false when a bound is missing or not a number
// in range, leaving it to the float64 path.
func decimalRange(left, right any, env *Environment, prec int) (res any, ok bool, err error) {
	l, lok := decimal.FromValue(left, prec)
	r, rok := decimal.FromValue(right, prec)
	if !lok || !rok {
		return nil, false, nil
	}
	if l.Floor(prec).Cmp(l) != 0 {
		return nil, true, &JSONataError{
			Code: "T2003", Message: fmt.Sprintf("left side of range operator (..) must be an integer, got %v", left),
		}
	}
	if r.Floor(prec).Cmp(r) != 0 {
		return nil, true, &JSONataError{
			Code: "T2004", Message: fmt.Sprintf("right side of range operator (..) must be an integer, got %v", right),
		}
	}
	if l.Cmp(r) > 0 {
		return nil, true, nil
	}
	span := math.Inf(1)
	if d, err := r.Sub(l, prec); err == nil {
		if n, ok := d.Int64(); ok {
			span = float64(n)
		}
	}
	if err := checkRangeSpan(span, env); err != nil {
		return nil, true, err
	}
	result := make([]any, int(span)+1)
	for i := range result {
		if i%10000 == 0 {
			if err := env.Err(); err != nil {
				return nil, true, err
			}
		}
		item, err := l.Add(decimal.NewInt(int64(i)), prec)
		if err != nil {
			return nil, true, &JSONataError{Code: "D1001", Message: "Number out of range"}
		}
		result[i] = item.Value(prec)
	}
	return result, true, nil
}
