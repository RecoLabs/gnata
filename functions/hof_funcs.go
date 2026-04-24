package functions

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
)

// hofArity returns the callback argument count for the given HOF function.
// For lambdas, uses the declared parameter count (capped at 3).
// For built-in functions, defaults to 1 (value only).
func hofArity(fn any) int {
	if lambda, ok := fn.(*evaluator.Lambda); ok {
		n := len(lambda.Params)
		if n > 3 {
			return 3
		}
		return n
	}
	return 1
}

// hofArgsBuf allocates a reusable buffer for HOF callback arguments.
func hofArgsBuf(arity int) []any {
	if arity == 0 {
		return nil
	}
	return make([]any, arity)
}

// fillHofArgs populates a pre-allocated argument buffer for a HOF callback.
func fillHofArgs(buf []any, value, index any, arr []any) {
	switch len(buf) {
	case 0:
	case 1:
		buf[0] = value
	case 2:
		buf[0] = value
		buf[1] = index
	default:
		buf[0] = value
		buf[1] = index
		buf[2] = arr
	}
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
		callArgs := hofArgsBuf(hofArity(fn))
		for i, item := range arr {
			fillHofArgs(callArgs, item, float64(i), arrAny)
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
		callArgs := hofArgsBuf(hofArity(fn))
		for i, item := range arr {
			fillHofArgs(callArgs, item, float64(i), arrAny)
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
		callArgs := hofArgsBuf(hofArity(fn))
		for i, item := range arr {
			fillHofArgs(callArgs, item, float64(i), arrAny)
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
		var reduceArity int
		if lambda, ok := fn.(*evaluator.Lambda); ok {
			reduceArity = max(min(len(lambda.Params), 4), 1)
		} else {
			reduceArity = 2
		}
		callArgs := make([]any, reduceArity)
		for i := startIdx; i < len(arr); i++ {
			callArgs[0] = acc
			if reduceArity > 1 {
				callArgs[1] = arr[i]
			}
			if reduceArity > 2 {
				callArgs[2] = float64(i)
			}
			if reduceArity > 3 {
				callArgs[3] = arrAny
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
