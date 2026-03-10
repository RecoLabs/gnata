package functions

import (
	"fmt"
	"maps"
	"slices"

	"github.com/recolabs/gnata/internal/evaluator"
)

// ── $keys ─────────────────────────────────────────────────────────────────────

func fnKeys(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	var keys []string
	seen := make(map[string]bool)
	switch v := args[0].(type) {
	case *evaluator.OrderedMap:
		keys = v.Keys()
	case map[string]any:
		keys = sortedKeyStrings(v)
	case []any:
		for _, item := range v {
			if evaluator.IsMap(item) {
				for _, k := range evaluator.MapKeys(item) {
					if !seen[k] {
						seen[k] = true
						keys = append(keys, k)
					}
				}
			}
		}
	default:
		return nil, nil
	}
	seq := evaluator.CreateSequence()
	for _, k := range keys {
		seq.Values = append(seq.Values, k)
	}
	return seq, nil
}

func sortedKeyStrings(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}

func sortedKeys(m map[string]any) []any {
	ks := sortedKeyStrings(m)
	result := make([]any, len(ks))
	for i, k := range ks {
		result[i] = k
	}
	return result
}

// ── $values ───────────────────────────────────────────────────────────────────

func fnValues(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	switch v := args[0].(type) {
	case *evaluator.OrderedMap:
		result := make([]any, 0, v.Len())
		v.Range(func(_ string, val any) bool {
			result = append(result, val)
			return true
		})
		return result, nil
	case map[string]any:
		keys := sortedKeys(v)
		result := make([]any, len(keys))
		for i, k := range keys {
			result[i] = v[k.(string)]
		}
		return result, nil
	case []any:
		var result []any
		for _, item := range v {
			if evaluator.IsMap(item) {
				for _, k := range evaluator.MapKeys(item) {
					val, _ := evaluator.MapGet(item, k)
					result = append(result, val)
				}
			}
		}
		if result == nil {
			return nil, nil
		}
		return result, nil
	default:
		return nil, nil
	}
}

// ── $spread ───────────────────────────────────────────────────────────────────

func fnSpread(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	spreadOne := func(obj any) []any {
		keys := evaluator.MapKeys(obj)
		result := make([]any, len(keys))
		for i, k := range keys {
			om := evaluator.NewOrderedMap()
			v, _ := evaluator.MapGet(obj, k)
			om.Set(k, v)
			result[i] = om
		}
		return result
	}
	if evaluator.IsMap(args[0]) {
		return spreadOne(args[0]), nil
	}
	if arr, ok := args[0].([]any); ok {
		var result []any
		for _, item := range arr {
			if evaluator.IsMap(item) {
				result = append(result, spreadOne(item)...)
			} else {
				result = append(result, item)
			}
		}
		if result == nil {
			return []any{}, nil
		}
		return result, nil
	}
	return args[0], nil
}

// ── $merge ────────────────────────────────────────────────────────────────────

func fnMerge(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	arr, ok := args[0].([]any)
	if !ok {
		if evaluator.IsMap(args[0]) {
			return args[0], nil
		}
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$merge: argument must be an array of objects"}
	}
	result := evaluator.NewOrderedMap()
	for _, item := range arr {
		if !evaluator.IsMap(item) {
			return nil, &evaluator.JSONataError{Code: "T0412", Message: "$merge: array elements must be objects"}
		}
		evaluator.MapRange(item, func(k string, v any) bool {
			result.Set(k, v)
			return true
		})
	}
	return result, nil
}

func siftArgs(fn, value any, key string, obj any) []any {
	if lambda, ok := fn.(*evaluator.Lambda); ok {
		switch len(lambda.Params) {
		case 0:
			return []any{}
		case 1:
			return []any{value}
		case 2:
			return []any{value, key}
		default:
			return []any{value, key, obj}
		}
	}
	return []any{value}
}

// ── $sift ─────────────────────────────────────────────────────────────────────

func makeFnSift(evalFn EvalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		var objVal any
		var fn any
		switch len(args) {
		case 0:
			return nil, &evaluator.JSONataError{Code: "D3006", Message: "$sift: requires at least 1 argument"}
		case 1:
			objVal = focus
			fn = args[0]
		default:
			objVal = args[0]
			fn = args[1]
		}
		if objVal == nil {
			return nil, nil
		}
		if !evaluator.IsMap(objVal) {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$sift: argument 1 must be an object"}
		}

		result := evaluator.NewOrderedMap()
		keys := evaluator.MapKeys(objVal)
		for _, ks := range keys {
			val, _ := evaluator.MapGet(objVal, ks)
			callArgs := siftArgs(fn, val, ks, objVal)
			res, err := evalFn(fn, callArgs, focus, env)
			if err != nil {
				return nil, err
			}
			if evaluator.ToBoolean(res) {
				result.Set(ks, val)
			}
		}
		if result.Len() == 0 {
			return nil, nil
		}
		return result, nil
	}
}

// ── $each ─────────────────────────────────────────────────────────────────────

func makeFnEach(evalFn EvalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		var objVal any
		var fn any
		switch len(args) {
		case 0:
			return nil, &evaluator.JSONataError{Code: "D3006", Message: "$each: requires at least 1 argument"}
		case 1:
			objVal = focus
			fn = args[0]
		default:
			objVal = args[0]
			fn = args[1]
		}
		if objVal == nil {
			return nil, nil
		}
		if !evaluator.IsMap(objVal) {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$each: argument 1 must be an object"}
		}

		keys := evaluator.MapKeys(objVal)
		result := make([]any, 0, len(keys))
		for _, ks := range keys {
			val, _ := evaluator.MapGet(objVal, ks)
			res, err := evalFn(fn, []any{val, ks}, focus, env)
			if err != nil {
				return nil, err
			}
			if res != nil {
				result = append(result, res)
			}
		}
		return result, nil
	}
}

// ── $error ────────────────────────────────────────────────────────────────────

func fnError(args []any, _ any) (any, error) {
	msg := "an error was thrown"
	if len(args) > 1 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$error: takes at most 1 argument"}
	}
	if len(args) == 1 && args[0] != nil {
		s, ok := args[0].(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$error: argument must be a string"}
		}
		msg = s
	}
	return nil, &evaluator.JSONataError{Code: "D3137", Message: msg}
}

// ── $lookup ───────────────────────────────────────────────────────────────────

func fnLookup(args []any, _ any) (any, error) {
	if len(args) < 2 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$lookup: requires 2 arguments"}
	}
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	key, ok := args[1].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: fmt.Sprintf("$lookup: key must be a string, got %T", args[1])}
	}

	if evaluator.IsMap(args[0]) {
		val, exists := evaluator.MapGet(args[0], key)
		if !exists {
			return nil, nil
		}
		return val, nil
	}
	if arr, ok := args[0].([]any); ok {
		var result []any
		for _, item := range arr {
			if evaluator.IsMap(item) {
				if val, exists := evaluator.MapGet(item, key); exists {
					result = append(result, val)
				}
			}
		}
		if len(result) == 0 {
			return nil, nil
		}
		if len(result) == 1 {
			return result[0], nil
		}
		return result, nil
	}
	return nil, nil
}
