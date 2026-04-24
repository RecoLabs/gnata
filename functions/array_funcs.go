package functions

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"slices"

	"github.com/recolabs/gnata/internal/evaluator"
)

// ── $count ────────────────────────────────────────────────────────────────────

func fnCount(args []any, _ any) (any, error) {
	if len(args) > 1 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$count: takes exactly 1 argument"}
	}
	if len(args) == 0 || args[0] == nil {
		return float64(0), nil
	}
	switch v := args[0].(type) {
	case []any:
		return float64(len(v)), nil
	default:
		return float64(1), nil
	}
}

// ── $append ───────────────────────────────────────────────────────────────────

func fnAppend(args []any, _ any) (any, error) {
	if len(args) < 2 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$append: requires 2 arguments"}
	}
	// If either argument is undefined, return the other unchanged.
	if args[0] == nil {
		return args[1], nil
	}
	if args[1] == nil {
		return args[0], nil
	}
	a := wrapArray(args[0])
	b := wrapArray(args[1])
	const maxAppendSize = 10_000_000
	if len(a)+len(b) > maxAppendSize {
		return nil, &evaluator.JSONataError{
			Code:    "D3010",
			Message: fmt.Sprintf("$append: result array exceeds maximum size of %d elements", maxAppendSize),
		}
	}
	return slices.Concat(a, b), nil
}

func wrapArray(v any) []any {
	if v == nil {
		return []any{}
	}
	if arr, ok := v.([]any); ok {
		return arr
	}
	if seq, ok := v.(*evaluator.Sequence); ok {
		return evaluator.CollapseToSlice(seq)
	}
	return []any{v}
}

// tryAsArray returns a []any if v is an array-like type ([]any or *Sequence),
// or nil if v is a scalar. Used for auto-mapping: functions that expect a
// scalar can map over arrays when one is provided.
func tryAsArray(v any) []any {
	switch val := v.(type) {
	case []any:
		return val
	case *evaluator.Sequence:
		return evaluator.CollapseToSlice(val)
	default:
		return nil
	}
}

// ── $sort ─────────────────────────────────────────────────────────────────────

func makeFnSort(evalFn EvalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		var arrVal any
		var fn any
		switch len(args) {
		case 0:
			arrVal = focus
		case 1:
			switch args[0].(type) {
			case evaluator.BuiltinFunction, evaluator.EnvAwareBuiltin, *evaluator.Lambda, *evaluator.SignedBuiltin:
				// arg is a function → use focus as the array
				arrVal = focus
				fn = args[0]
			default:
				arrVal = args[0]
			}
		default:
			arrVal = args[0]
			fn = args[1]
		}
		if arrVal == nil {
			return nil, nil
		}
		arr := wrapArray(arrVal)

		if fn == nil && len(arr) > 0 {
			allNum := true
			allStr := true
			for _, item := range arr {
				if _, ok := evaluator.ToFloat64(item); !ok {
					allNum = false
				}
				if _, ok := item.(string); !ok {
					allStr = false
				}
			}
			if !allNum && !allStr {
				return nil, &evaluator.JSONataError{
					Code:    "D3070",
					Message: "$sort: each element of the array must be of the same type - strings or numbers",
				}
			}
		}
		sorted := slices.Clone(arr)
		var cmpFn func(a, b any) (int, error)
		if fn != nil {
			// JSONata $sort comparator: fn(a, b) returns true when a should come
			// before b. We call fn(b, a) and map true→-1 (a<b), false→0 (a>=b).
			// SortItemsErr only tests < 0, so +1 is unnecessary.
			sortArgs := make([]any, 2)
			cmpFn = func(a, b any) (int, error) {
				sortArgs[0] = b
				sortArgs[1] = a
				result, err := evalFn(fn, sortArgs, focus, env)
				if err != nil {
					return 0, err
				}
				if evaluator.ToBoolean(result) {
					return -1, nil
				}
				return 0, nil
			}
		} else {
			cmpFn = defaultCompare
		}
		if err := evaluator.SortItemsErr(sorted, cmpFn); err != nil {
			return nil, err
		}
		return sorted, nil
	}
}

func defaultCompare(a, b any) (int, error) {
	if av, aOk := evaluator.ToFloat64(a); aOk {
		if bv, bOk := evaluator.ToFloat64(b); bOk {
			if av < bv {
				return -1, nil
			} else if av > bv {
				return 1, nil
			}
			return 0, nil
		}
	}
	if av, aOk := a.(string); aOk {
		if bv, bOk := b.(string); bOk {
			if av < bv {
				return -1, nil
			} else if av > bv {
				return 1, nil
			}
			return 0, nil
		}
	}
	return 0, fmt.Errorf("$sort: cannot compare %T and %T", a, b)
}

// ── $reverse ──────────────────────────────────────────────────────────────────

func fnReverse(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	arr := wrapArray(args[0])
	result := slices.Clone(arr)
	slices.Reverse(result)
	return result, nil
}

// ── $shuffle ──────────────────────────────────────────────────────────────────

func fnShuffle(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	arr := wrapArray(args[0])
	result := slices.Clone(arr)
	rand.Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})
	return result, nil
}

// ── $distinct ─────────────────────────────────────────────────────────────────

func fnDistinct(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	if evaluator.IsNull(args[0]) {
		return evaluator.Null, nil
	}
	arr, ok := args[0].([]any)
	if !ok {
		if seq, ok2 := args[0].(*evaluator.Sequence); ok2 {
			arr = evaluator.CollapseToSlice(seq)
		} else {
			return args[0], nil
		}
	}
	if len(arr) <= 1 {
		// No dedup needed — return as plain array. Sequence wrapping
		// (and thus singleton-unwrap) only applies when dedup actually
		// reduced a multi-element input.
		return arr, nil
	}
	result := make([]any, 0, len(arr))
	seen := make(map[any]bool, len(arr))
	var complexItems []any
	for _, v := range arr {
		switch tv := v.(type) {
		case string, float64, bool:
			if !seen[v] {
				seen[v] = true
				result = append(result, v)
			}
		case json.Number:
			f, err := tv.Float64()
			key := any(tv.String())
			if err == nil {
				key = f
			}
			if !seen[key] {
				seen[key] = true
				result = append(result, v)
			}
		default:
			if v == nil || evaluator.IsNull(v) {
				key := fmt.Sprintf("__nil_%T", v)
				if !seen[key] {
					seen[key] = true
					result = append(result, v)
				}
				continue
			}
			found := false
			for _, existing := range complexItems {
				if evaluator.DeepEqual(v, existing) {
					found = true
					break
				}
			}
			if !found {
				complexItems = append(complexItems, v)
				result = append(result, v)
			}
		}
	}
	return &evaluator.Sequence{Values: result}, nil
}

// ── $flatten ──────────────────────────────────────────────────────────────────

func fnFlatten(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	arr := wrapArray(args[0])

	depth := -1 // unlimited
	if len(args) >= 2 && args[1] != nil {
		df, ok := args[1].(float64)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$flatten: depth argument must be a number"}
		}
		depth = int(df)
	}

	return flattenArray(arr, depth), nil
}

func flattenArray(arr []any, depth int) []any {
	result := make([]any, 0, len(arr))
	for _, v := range arr {
		if nested, ok := v.([]any); ok && depth != 0 {
			nextDepth := depth - 1
			if depth < 0 {
				nextDepth = -1
			}
			result = append(result, flattenArray(nested, nextDepth)...)
		} else {
			result = append(result, v)
		}
	}
	return result
}

// ── $zip ──────────────────────────────────────────────────────────────────────

func fnZip(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return []any{}, nil
	}
	// Determine shortest length.
	minLen := -1
	arrays := make([][]any, 0, len(args))
	for _, arg := range args {
		if arg == nil {
			arr := []any{}
			arrays = append(arrays, arr)
			if minLen < 0 || len(arr) < minLen {
				minLen = len(arr)
			}
			continue
		}
		arr := wrapArray(arg)
		arrays = append(arrays, arr)
		if minLen < 0 || len(arr) < minLen {
			minLen = len(arr)
		}
	}
	if minLen <= 0 {
		return []any{}, nil
	}
	result := make([]any, minLen)
	for i := range minLen {
		tuple := make([]any, len(arrays))
		for j, arr := range arrays {
			if i < len(arr) {
				tuple[j] = arr[i]
			}
		}
		result[i] = tuple
	}
	return result, nil
}
