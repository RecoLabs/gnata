package evaluator

import (
	"fmt"
	"math"
)

func evalRange(left, right any, env *Environment) (any, error) {
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
	if err := env.CheckSequence(ToIntClamped(span + 1)); err != nil {
		return nil, err
	}
	const maxRange = 10_000_000
	if span >= maxRange {
		return nil, &JSONataError{Code: "D2014", Message: fmt.Sprintf("range operator (..) must not exceed %d items", maxRange)}
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
