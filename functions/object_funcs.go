package functions

import (
	"fmt"
	"maps"
	"slices"

	"github.com/recolabs/gnata/internal/evaluator"
)

// ── $keys ─────────────────────────────────────────────────────────────────────

func fnKeys(args []any, _ any, env *evaluator.Environment) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	keys, err := appendKeys(args[0], make(map[string]bool), env)
	if err != nil {
		return nil, err
	}
	if err := env.CheckSequence(len(keys)); err != nil {
		return nil, err
	}
	seq := evaluator.CreateSequence()
	for _, k := range keys {
		seq.Values = append(seq.Values, k)
	}
	return seq, nil
}

// appendKeys returns the keys of v not yet seen; as in jsonata-js, the keys
// of an array are those of its items, nested arrays included.
func appendKeys(v any, seen map[string]bool, env *evaluator.Environment) ([]string, error) {
	var keys []string
	add := func(obj any) error {
		for _, k := range evaluator.MapKeys(obj) {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		return nil
	}
	if arr, ok := evaluator.AsArray(v); ok {
		err := evaluator.EachLeaf(arr, -1, env, add)
		return keys, err
	}
	return keys, add(v)
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

func fnSpread(args []any, _ any, env *evaluator.Environment) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	return spreadValue(args[0], env)
}

// spreadValue splits an object into one-key objects. As in jsonata-js, an
// array's items are spread and appended in turn, nested arrays included, so
// a spread array is a plain array that does not collapse to one item.
func spreadValue(v any, env *evaluator.Environment) (any, error) {
	arr, isArr := evaluator.AsArray(v)
	if !isArr {
		if !evaluator.IsMap(v) {
			return v, nil
		}
		objs, err := spreadObject(v, nil, env)
		return &evaluator.Sequence{Values: objs}, err
	}
	if len(arr) == 0 {
		return evaluator.CreateSequence(), nil
	}
	result := []any{}
	err := evaluator.EachLeaf(arr, -1, env, func(item any) error {
		if evaluator.IsMap(item) {
			var err error
			result, err = spreadObject(item, result, env)
			return err
		}
		result = append(result, nullIfNil(item))
		return env.CheckSequence(len(result))
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// spreadObject appends a one-key object to dst for each key of obj.
func spreadObject(obj any, dst []any, env *evaluator.Environment) ([]any, error) {
	keys := evaluator.MapKeys(obj)
	if err := env.CheckSequence(len(dst) + len(keys)); err != nil {
		return nil, err
	}
	for _, k := range keys {
		om := evaluator.NewOrderedMap()
		val, _ := evaluator.MapGet(obj, k)
		om.Set(k, val)
		dst = append(dst, om)
	}
	return dst, nil
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
		return nil, &evaluator.JSONataError{Code: "T0412", Message: "$merge: argument must be an array of objects"}
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

func fillSiftArgs(buf []any, value any, key string, obj any) {
	switch len(buf) {
	case 0:
	case 1:
		buf[0] = value
	case 2:
		buf[0] = value
		buf[1] = key
	default:
		buf[0] = value
		buf[1] = key
		buf[2] = obj
	}
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
		callArgs := hofArgsBuf(hofArity(fn))
		for _, ks := range keys {
			val, _ := evaluator.MapGet(objVal, ks)
			fillSiftArgs(callArgs, val, ks, objVal)
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
		seq := evaluator.CreateSequence()
		callArgs := hofArgsBuf(max(hofArity(fn), 2))
		for _, ks := range keys {
			val, _ := evaluator.MapGet(objVal, ks)
			fillSiftArgs(callArgs, val, ks, objVal)
			res, err := evalFn(fn, callArgs, focus, env)
			if err != nil {
				return nil, err
			}
			if res != nil {
				seq.Values = append(seq.Values, res)
				if err := env.CheckSequence(len(seq.Values)); err != nil {
					return nil, err
				}
			}
		}
		return seq, nil
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

func fnLookup(args []any, _ any, env *evaluator.Environment) (any, error) {
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
	return lookupValue(args[0], key, env)
}

// nullIfNil returns JSON null for a Go nil, as a nil value in data decoded
// with encoding/json is a JSON null.
func nullIfNil(v any) any {
	if v == nil {
		return evaluator.Null
	}
	return v
}

// lookupValue returns an object's value for key. As in jsonata-js, an
// array's items are looked up in turn, nested arrays included, and array
// values are flattened into the result.
func lookupValue(v any, key string, env *evaluator.Environment) (any, error) {
	arr, isArr := evaluator.AsArray(v)
	if !isArr {
		if val, exists := evaluator.MapGet(v, key); exists {
			return nullIfNil(val), nil
		}
		return nil, nil
	}
	seq := evaluator.CreateSequence()
	err := evaluator.EachLeaf(arr, -1, env, func(item any) error {
		val, exists := evaluator.MapGet(item, key)
		if !exists {
			return nil
		}
		seq.Values = append(seq.Values, wrapArray(nullIfNil(val))...)
		return env.CheckSequence(len(seq.Values))
	})
	if err != nil {
		return nil, err
	}
	return seq, nil
}
