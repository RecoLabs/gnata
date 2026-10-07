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
	var keys []string
	switch v := args[0].(type) {
	case *evaluator.OrderedMap:
		keys = v.Keys()
	case map[string]any:
		keys = sortedKeyStrings(v)
	default:
		items, isArray := evaluator.AsArray(v)
		if !isArray {
			return evaluator.CreateSequence(), nil
		}
		keys = arrayKeys(items)
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

// arrayKeys returns the keys of the objects in items and, as in jsonata-js,
// in arrays nested in it, each once in the order first seen. A work stack
// instead of recursion keeps deeply nested input off the Go stack, and an
// array met again adds no keys, so it is not walked twice: shared nested
// arrays cannot make the walk exponential, nor cyclic ones endless.
func arrayKeys(items []any) []string {
	var keys []string
	seen := make(map[string]bool)
	type arrayID struct {
		first *any
		n     int
	}
	walked := make(map[arrayID]bool)
	for pending := [][]any{items}; len(pending) > 0; {
		top := pending[len(pending)-1]
		if len(top) == 0 {
			pending = pending[:len(pending)-1]
			continue
		}
		item := top[0]
		pending[len(pending)-1] = top[1:]
		if nested, isArray := evaluator.AsArray(item); isArray {
			if len(nested) > 0 {
				if id := (arrayID{&nested[0], len(nested)}); !walked[id] {
					walked[id] = true
					pending = append(pending, nested)
				}
			}
			continue
		}
		if !evaluator.IsMap(item) {
			continue
		}
		for _, k := range evaluator.MapKeys(item) {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys
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
	arr, isArr := evaluator.AsArray(args[0])
	if !isArr {
		if !evaluator.IsMap(args[0]) {
			return args[0], nil
		}
		objs, err := spreadObject(args[0], nil, env)
		return &evaluator.Sequence{Values: objs}, err
	}
	if len(arr) == 0 {
		return evaluator.CreateSequence(), nil
	}
	// As in jsonata-js, an array's items are spread and appended in turn,
	// nested arrays included, so a spread array is a plain array that does
	// not collapse to one item.
	result := []any{}
	err := evaluator.EachLeaf("spread", arr, -1, env, func(item any) error {
		if evaluator.IsMap(item) {
			var err error
			result, err = spreadObject(item, result, env)
			return err
		}
		result = append(result, evaluator.NilAsNull(item))
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
		var objVal, fn any
		if len(args) > 0 {
			objVal = args[0]
		}
		if len(args) > 1 {
			fn = args[1]
		}
		if objVal == nil {
			return nil, nil
		}
		if !evaluator.IsMap(objVal) {
			// Validation rejects anything else. Called unvalidated, through a
			// partial application, jsonata-js finds no keys in it, except an
			// array's indexes, which gnata does not iterate.
			return nil, nil
		}

		result := evaluator.NewOrderedMap()
		keys := evaluator.MapKeys(objVal)
		callArgs := make([]any, hofArity(fn, 1))
		for _, ks := range keys {
			val, _ := evaluator.MapGet(objVal, ks)
			fillSiftArgs(callArgs, val, ks, objVal)
			res, err := evalFn(fn, callArgs, env)
			if err != nil {
				return nil, err
			}
			truthy, err := evaluator.ToBooleanEnv(res, env)
			if err != nil {
				return nil, err
			}
			if truthy {
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
		var objVal, fn any
		if len(args) > 0 {
			objVal = args[0]
		}
		if len(args) > 1 {
			fn = args[1]
		}
		if objVal == nil {
			return nil, nil
		}
		if !evaluator.IsMap(objVal) {
			// Validation rejects anything else. Called unvalidated, through a
			// partial application, jsonata-js finds no keys in it, except an
			// array's indexes, which gnata does not iterate.
			return nil, nil
		}

		keys := evaluator.MapKeys(objVal)
		seq := evaluator.CreateSequence()
		callArgs := make([]any, hofArity(fn, 2))
		for _, ks := range keys {
			val, _ := evaluator.MapGet(objVal, ks)
			fillSiftArgs(callArgs, val, ks, objVal)
			res, err := evalFn(fn, callArgs, env)
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
	if arr, ok := evaluator.AsArray(args[0]); ok {
		return lookupArray(arr, key, env)
	}
	return fieldOrNull(args[0], key), nil
}

// maxLookupSize caps a $lookup result, as $append caps its own: one
// array value repeated through shared arrays can otherwise allocate far
// more than the input holds before the deadline is next read.
const maxLookupSize = 10_000_000

// lookupArray returns, as jsonata-js lookup does, a sequence of the values
// of key in the objects of arr and of the arrays nested in it, flattening
// array values. It walks the nested arrays with EachLeaf, which keeps deeply
// nested input off the Go stack, polls Err as arrays shared at several
// nesting levels can make the walk exponential in the expression's size,
// and reports an array that contains itself.
func lookupArray(arr []any, key string, env *evaluator.Environment) (any, error) {
	seq := evaluator.CreateSequence()
	err := evaluator.EachLeaf("look up", arr, -1, env, func(item any) error {
		val := fieldOrNull(item, key)
		values, isArray := evaluator.AsArray(val)
		switch {
		case val == nil:
			return nil
		case !isArray:
			values = []any{val}
		}
		if err := env.CheckSequence(len(seq.Values) + len(values)); err != nil {
			return err
		}
		if len(seq.Values)+len(values) > maxLookupSize {
			return &evaluator.JSONataError{
				Code:    "D3010",
				Message: fmt.Sprintf("$lookup: result array exceeds maximum size of %d elements", maxLookupSize),
			}
		}
		for _, v := range values {
			seq.Values = append(seq.Values, evaluator.NilAsNull(v))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return seq, nil
}

// fieldOrNull returns the value of key in an object, with a JSON null field
// as Null, or nil when obj is not an object or lacks the key.
func fieldOrNull(obj any, key string) any {
	val, ok := evaluator.MapGet(obj, key)
	if ok && val == nil {
		return evaluator.Null
	}
	return val
}

// ── $clone ────────────────────────────────────────────────────────────────────

// fnClone deep-copies an object or array as jsonata-js does (see
// evaluator.CloneValue).
func fnClone(args []any, _ any, env *evaluator.Environment) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	if len(args) > 1 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$clone: takes 1 argument"}
	}
	if !evaluator.IsArray(args[0]) && !evaluator.IsMap(args[0]) {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$clone: argument must be an object or array"}
	}
	return evaluator.CloneValue(args[0], env)
}
