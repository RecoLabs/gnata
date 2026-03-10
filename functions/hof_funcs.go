package functions

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
)

// hofArgs trims the HOF callback argument list to the function's expected arity.
// For lambdas, use the declared parameter count.
// For built-in functions, default to 1 (value only) since they have their own
// argument validation and may reject unexpected extra args.
func hofArgs(fn, value, index any, arr []any) []any {
	if lambda, ok := fn.(*evaluator.Lambda); ok {
		switch len(lambda.Params) {
		case 0:
			return []any{}
		case 1:
			return []any{value}
		case 2:
			return []any{value, index}
		default:
			return []any{value, index, arr}
		}
	}
	// For built-in functions pass (value) only to avoid arity rejections.
	return []any{value}
}

// ── $map ──────────────────────────────────────────────────────────────────────

func makeFnMap(evalFn EvalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		var arrVal any
		var fn any
		switch len(args) {
		case 0:
			return nil, &evaluator.JSONataError{Code: "D3006", Message: "$map: requires at least 1 argument"}
		case 1:
			arrVal = focus
			fn = args[0]
		default:
			arrVal = args[0]
			fn = args[1]
		}
		if arrVal == nil {
			if len(args) >= 2 {
				return nil, nil
			}
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$map: array argument is undefined"}
		}
		arr := wrapArray(arrVal)

		seq := evaluator.CreateSequence()
		arrAny := slices.Clone(arr)
		for i, item := range arr {
			callArgs := hofArgs(fn, item, float64(i), arrAny)
			val, err := evalFn(fn, callArgs, focus, env)
			if err != nil {
				return nil, err
			}
			if val != nil {
				seq.Values = append(seq.Values, val)
			}
		}
		return evaluator.CollapseSequence(seq), nil
	}
}

// ── $filter ───────────────────────────────────────────────────────────────────

func makeFnFilter(evalFn EvalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		var arrVal any
		var fn any
		switch len(args) {
		case 0:
			return nil, &evaluator.JSONataError{Code: "D3006", Message: "$filter: requires at least 1 argument"}
		case 1:
			arrVal = focus
			fn = args[0]
		default:
			arrVal = args[0]
			fn = args[1]
		}
		if arrVal == nil {
			return nil, nil
		}
		_, inputWasArray := arrVal.([]any)
		arr := wrapArray(arrVal)

		seq := evaluator.CreateSequence()
		arrAny := slices.Clone(arr)
		for i, item := range arr {
			callArgs := hofArgs(fn, item, float64(i), arrAny)
			val, err := evalFn(fn, callArgs, focus, env)
			if err != nil {
				return nil, err
			}
			if evaluator.ToBoolean(val) {
				seq.Values = append(seq.Values, item)
			}
		}
		if inputWasArray {
			if len(seq.Values) == 0 {
				return nil, nil
			}
			out := make([]any, len(seq.Values))
			copy(out, seq.Values)
			return out, nil
		}
		return evaluator.CollapseSequence(seq), nil
	}
}

// ── $single ───────────────────────────────────────────────────────────────────

func makeFnSingle(evalFn EvalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		if len(args) == 0 {
			if focus == nil {
				return nil, nil
			}
			arr := wrapArray(focus)
			if len(arr) == 1 {
				return arr[0], nil
			}
			if len(arr) == 0 {
				return nil, &evaluator.JSONataError{Code: "D3139", Message: "$single: expected 1 item but got 0"}
			}
			return nil, &evaluator.JSONataError{Code: "D3138", Message: fmt.Sprintf("$single: expected 1 item but got %d", len(arr))}
		}
		if args[0] == nil {
			return nil, nil
		}
		arr := wrapArray(args[0])

		if len(args) < 2 || args[1] == nil {
			if len(arr) == 1 {
				return arr[0], nil
			}
			if len(arr) == 0 {
				return nil, &evaluator.JSONataError{Code: "D3139", Message: "$single: expected 1 item but got 0"}
			}
			return nil, &evaluator.JSONataError{Code: "D3138", Message: fmt.Sprintf("$single: expected 1 item but got %d", len(arr))}
		}

		fn := args[1]
		var matched []any
		arrAny := slices.Clone(arr)
		for i, item := range arr {
			callArgs := hofArgs(fn, item, float64(i), arrAny)
			val, err := evalFn(fn, callArgs, focus, env)
			if err != nil {
				return nil, err
			}
			if evaluator.ToBoolean(val) {
				matched = append(matched, item)
			}
		}
		if len(matched) == 1 {
			return matched[0], nil
		}
		if len(matched) == 0 {
			return nil, &evaluator.JSONataError{Code: "D3139", Message: "$single: predicate matched no items, expected 1"}
		}
		return nil, &evaluator.JSONataError{Code: "D3138", Message: fmt.Sprintf("$single: predicate matched %d items, expected 1", len(matched))}
	}
}

// ── $reduce ───────────────────────────────────────────────────────────────────

func makeFnReduce(evalFn EvalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		var arrVal any
		var fn any
		var initVal any
		hasInit := false
		switch len(args) {
		case 0:
			return nil, &evaluator.JSONataError{Code: "D3006", Message: "$reduce: requires at least 1 argument"}
		case 1:
			arrVal = focus
			fn = args[0]
		default:
			arrVal = args[0]
			fn = args[1]
			if len(args) >= 3 {
				initVal = args[2]
				hasInit = true
			}
		}
		if arrVal == nil {
			return nil, nil
		}
		if lambda, ok := fn.(*evaluator.Lambda); ok && len(lambda.Params) < 2 {
			return nil, &evaluator.JSONataError{Code: "D3050", Message: "$reduce: function must have arity of at least 2"}
		}
		arr := wrapArray(arrVal)

		if len(arr) == 0 {
			if hasInit {
				return initVal, nil
			}
			return nil, nil
		}

		var acc any
		startIdx := 0
		if hasInit {
			acc = initVal
		} else {
			acc = arr[0]
			startIdx = 1
		}

		arrAny := slices.Clone(arr)
		for i := startIdx; i < len(arr); i++ {
			var callArgs []any
			if lambda, ok := fn.(*evaluator.Lambda); ok {
				switch len(lambda.Params) {
				case 0, 1:
					callArgs = []any{acc}
				case 2:
					callArgs = []any{acc, arr[i]}
				case 3:
					callArgs = []any{acc, arr[i], float64(i)}
				default:
					callArgs = []any{acc, arr[i], float64(i), arrAny}
				}
			} else {
				callArgs = []any{acc, arr[i]}
			}
			val, err := evalFn(fn, callArgs, focus, env)
			if err != nil {
				return nil, err
			}
			acc = val
		}
		return acc, nil
	}
}

// ── $assert ───────────────────────────────────────────────────────────────────

func fnAssert(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$assert: argument is required"}
	}
	if len(args) > 2 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$assert: takes at most 2 arguments"}
	}
	if _, ok := args[0].(bool); !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$assert: first argument must be a boolean"}
	}
	if !args[0].(bool) {
		msg := "assertion failed"
		if len(args) >= 2 {
			if s, ok := args[1].(string); ok {
				msg = s
			}
		}
		return nil, &evaluator.JSONataError{Code: "D3141", Message: msg}
	}
	return nil, nil
}

// ── $typeOf ───────────────────────────────────────────────────────────────────

func fnTypeOf(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil // undefined
	}
	if evaluator.IsNull(args[0]) {
		return parser.NullJSON, nil
	}
	switch args[0].(type) {
	case float64, json.Number:
		return "number", nil
	case string:
		return "string", nil
	case bool:
		return "boolean", nil
	case []any:
		return "array", nil
	case *evaluator.OrderedMap, map[string]any:
		return "object", nil
	case evaluator.BuiltinFunction, evaluator.EnvAwareBuiltin, *evaluator.Lambda, *evaluator.SignedBuiltin:
		return "function", nil
	default:
		return nil, nil
	}
}
