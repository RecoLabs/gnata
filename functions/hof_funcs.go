package functions

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
)

// hofArity returns how many of (value, index, array) a HOF passes fn, or
// unknown when FunctionArity does not know. Like jsonata-js's hofFuncArgs, the
// value is always passed.
func hofArity(fn any, unknown int) int {
	arity, known := evaluator.FunctionArity(fn)
	if !known {
		return unknown
	}
	return min(max(arity, 1), 3)
}

// hofItems returns the items a higher-order function iterates, where a nil
// item of Go data is a JSON null rather than undefined. The signature wraps
// any other argument in an array, so one reaches here only from a partial
// application, which jsonata-js does not validate: its loop over the
// argument's length then visits a string's characters (code points here,
// as elsewhere in gnata) and nothing of a number, object or function.
func hofItems(v any) []any {
	switch val := v.(type) {
	case nil, *evaluator.Sequence:
	case string:
		chars := make([]any, 0, len(val))
		for _, r := range val {
			chars = append(chars, string(r))
		}
		return chars
	default:
		if _, isArray := evaluator.AsArray(v); !isArray {
			return nil
		}
	}
	return evaluator.NullItems(evaluator.AppendItems(v))
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
		arr := hofItems(arrVal)

		seq := evaluator.CreateSequence()
		arrAny := slices.Clone(arr)
		callArgs := make([]any, hofArity(fn, 1))
		for i, item := range arr {
			fillHofArgs(callArgs, item, float64(i), arrAny)
			val, err := evalFn(fn, callArgs, env)
			if err != nil {
				return nil, err
			}
			if val != nil {
				seq.Values = append(seq.Values, val)
				if err := env.CheckSequence(len(seq.Values)); err != nil {
					return nil, err
				}
			}
		}
		return seq, nil
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
		arr := hofItems(arrVal)

		seq := evaluator.CreateSequence()
		arrAny := slices.Clone(arr)
		callArgs := make([]any, hofArity(fn, 1))
		for i, item := range arr {
			fillHofArgs(callArgs, item, float64(i), arrAny)
			val, err := evalFn(fn, callArgs, env)
			if err != nil {
				return nil, err
			}
			truthy, err := evaluator.ToBooleanEnv(val, env)
			if err != nil {
				return nil, err
			}
			if truthy {
				seq.Values = append(seq.Values, item)
				if err := env.CheckSequence(len(seq.Values)); err != nil {
					return nil, err
				}
			}
		}
		return seq, nil
	}
}

// ── $single ───────────────────────────────────────────────────────────────────

func makeFnSingle(evalFn EvalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		if len(args) == 0 {
			if focus == nil {
				return nil, nil
			}
			arr := hofItems(focus)
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
		arr := hofItems(args[0])

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
		callArgs := make([]any, hofArity(fn, 1))
		for i, item := range arr {
			fillHofArgs(callArgs, item, float64(i), arrAny)
			val, err := evalFn(fn, callArgs, env)
			if err != nil {
				return nil, err
			}
			truthy, err := evaluator.ToBooleanEnv(val, env)
			if err != nil {
				return nil, err
			}
			if truthy {
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
			if len(args) >= 3 && args[2] != nil {
				initVal = args[2]
				hasInit = true
			}
		}
		if arrVal == nil {
			return nil, nil
		}
		reduceArity, known := evaluator.FunctionArity(fn)
		if !known {
			reduceArity = 2
		}
		reduceArity = min(reduceArity, 4)
		if reduceArity < 2 {
			return nil, &evaluator.JSONataError{Code: "D3050", Message: "$reduce: function must have arity of at least 2"}
		}
		arr := hofItems(arrVal)

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
		callArgs := make([]any, reduceArity)
		for i := startIdx; i < len(arr); i++ {
			callArgs[0] = acc
			callArgs[1] = arr[i]
			if reduceArity > 2 {
				callArgs[2] = float64(i)
			}
			if reduceArity > 3 {
				callArgs[3] = arrAny
			}
			val, err := evalFn(fn, callArgs, env)
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
	// An undefined condition matches the boolean parameter and fails.
	holds, isBool := args[0].(bool)
	if !isBool && args[0] != nil {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$assert: first argument must be a boolean"}
	}
	if !holds {
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
	}
	if evaluator.IsFunction(args[0]) {
		return "function", nil
	}
	return nil, nil
}
