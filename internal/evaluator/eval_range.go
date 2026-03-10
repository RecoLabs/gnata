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
		if ln != math.Trunc(ln) {
			return nil, &JSONataError{Code: "T2003", Message: fmt.Sprintf("left side of range operator (..) must be an integer, got %v", ln)}
		}
	}
	if right != nil {
		rn, rnOK = ToFloat64(right)
		if !rnOK {
			return nil, &JSONataError{Code: "T2004", Message: fmt.Sprintf("right side of range operator (..) must be an integer, got %T", right)}
		}
		if rn != math.Trunc(rn) {
			return nil, &JSONataError{Code: "T2004", Message: fmt.Sprintf("right side of range operator (..) must be an integer, got %v", rn)}
		}
	}
	if left == nil || right == nil {
		return nil, nil
	}
	lo := int(ln)
	hi := int(rn)
	if lo > hi {
		return nil, nil
	}
	const maxRange = 10_000_000
	if hi-lo >= maxRange {
		return nil, &JSONataError{Code: "D2014", Message: fmt.Sprintf("range operator (..) must not exceed %d items", maxRange)}
	}
	result := make([]any, 0, hi-lo+1)
	ctx := env.Context()
	for i := lo; i <= hi; i++ {
		if i%10000 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		result = append(result, float64(i))
	}
	return result, nil
}
