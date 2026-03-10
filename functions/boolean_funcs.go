package functions

import "github.com/recolabs/gnata/internal/evaluator"

// ── $boolean ──────────────────────────────────────────────────────────────────

func fnBoolean(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$boolean: argument is required"}
	}
	if len(args) > 1 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$boolean: takes exactly 1 argument"}
	}
	if args[0] == nil {
		return nil, nil // undefined in → undefined out
	}
	return evaluator.ToBoolean(args[0]), nil
}

// ── $not ──────────────────────────────────────────────────────────────────────

func fnNot(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$not: argument is required"}
	}
	if args[0] == nil {
		return nil, nil // undefined in → undefined out
	}
	return !evaluator.ToBoolean(args[0]), nil
}

// ── $exists ───────────────────────────────────────────────────────────────────

func fnExists(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$exists: argument is required"}
	}
	if len(args) > 1 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$exists: takes exactly 1 argument"}
	}
	return args[0] != nil, nil
}
