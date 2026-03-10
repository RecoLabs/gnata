package evaluator

import (
	"errors"
	"fmt"
	"slices"

	"github.com/recolabs/gnata/internal/parser"
)

func evalFunction(node *parser.Node, input any, env *Environment) (any, error) {
	var fn any
	if node.Procedure != nil {
		var err error
		fn, err = Eval(node.Procedure, input, env)
		if err != nil {
			// When % is used as a function callee (e.g., %(1)), the parent
			// context error should become T1006 (not a function).
			je := &JSONataError{}
			if errors.As(err, &je) && je.Code == "S0217" {
				fn = nil
			} else {
				return nil, err
			}
		}
		// For NodeName procedures (without $): if input lookup yields nil but the name
		// IS in env, return T1005 ("the function has no definition" — accessed without $).
		// If it's completely unknown (not in env either), return T1006.
		if fn == nil && node.Procedure.Type == parser.NodeName {
			if envFn, found := env.Lookup(node.Procedure.Value); found && envFn != nil {
				return nil, &JSONataError{
					Code:    "T1005",
					Message: fmt.Sprintf("attempted to invoke a function that has no definition: %s", node.Procedure.Value),
				}
			}
		}
	}

	args := make([]any, 0, len(node.Arguments))
	for _, argNode := range node.Arguments {
		if argNode.Type == parser.NodePlaceholder {
			args = append(args, nil)
			continue
		}
		val, err := Eval(argNode, input, env)
		if err != nil {
			return nil, err
		}
		args = append(args, val)
	}

	// Validate signature for SignedBuiltins at the direct call site.
	// HOF callbacks bypass this (they go through ApplyFunction instead).
	if sb, ok := fn.(*SignedBuiltin); ok {
		specs, _ := parser.ParseSig(sb.Sig)
		coerced, returnUndefined, sigErr := processCallArgs(specs, args)
		if sigErr != nil {
			return nil, sigErr
		}
		if returnUndefined {
			return nil, nil
		}
		args = coerced
	}

	// Tail-call optimization: if this call is in tail position within a
	// lambda body, return a TailCall sentinel instead of recursing.
	// The trampoline loop in callFunction will catch it.
	if node.Thunk {
		if _, isLambda := fn.(*Lambda); isLambda {
			return &TailCall{Fn: fn, Args: args}, nil
		}
	}

	result, err := callFunction(fn, args, input, env)
	if err != nil {
		return nil, err
	}
	// Normalize Sequence results: jsonata-js collapses Sequence values
	// after every function call. Single-element → scalar, multi → []any.
	// Functions returning *Sequence ($keys, $distinct) rely on this.
	if seq, ok := result.(*Sequence); ok {
		result = CollapseSequence(seq)
	}
	if node.KeepArray {
		switch result.(type) {
		case []any:
			return result, nil
		case nil:
			return nil, nil
		default:
			return []any{result}, nil
		}
	}
	return result, nil
}

func evalLambda(node *parser.Node, input any, env *Environment) (any, error) {
	params := make([]string, 0, len(node.Arguments))
	for _, arg := range node.Arguments {
		params = append(params, arg.Value)
	}
	sig := ""
	if node.Signature != nil {
		sig = node.Signature.Raw
	}
	return &Lambda{
		Params:        params,
		Body:          node.Body,
		Closure:       env,
		Thunk:         node.Thunk,
		Sig:           sig,
		CapturedFocus: input,
	}, nil
}

func evalPartial(node *parser.Node, input any, env *Environment) (any, error) {
	fn, err := Eval(node.Procedure, input, env)
	if err != nil {
		return nil, err
	}
	// For NodeName procedures (without $), distinguish T1007 vs T1008:
	// - if the name is in env (it's a function reference without $), → T1007
	// - if completely unknown → T1008
	if fn == nil && node.Procedure != nil && node.Procedure.Type == parser.NodeName {
		if envFn, found := env.Lookup(node.Procedure.Value); found && envFn != nil {
			return nil, &JSONataError{Code: "T1007", Message: "attempted to partially apply a function referenced without $"}
		}
		return nil, &JSONataError{Code: "T1008", Message: "cannot partially apply a non-function: the function is not defined"}
	}
	// T1007 when fn is nil (undefined); T1008 when fn is a non-function value.
	switch fn.(type) {
	case BuiltinFunction, EnvAwareBuiltin, *Lambda, *SignedBuiltin:
		// OK
	case nil:
		return nil, &JSONataError{Code: "T1007", Message: "attempted to partially apply an undefined function"}
	default:
		return nil, &JSONataError{Code: "T1008", Message: fmt.Sprintf("cannot partially apply a non-function: %T", fn)}
	}

	boundArgs := make([]any, len(node.Arguments))
	isPlaceholder := make([]bool, len(node.Arguments))
	for i, argNode := range node.Arguments {
		if argNode.Type == parser.NodePlaceholder {
			isPlaceholder[i] = true
		} else {
			val, err := Eval(argNode, input, env)
			if err != nil {
				return nil, err
			}
			boundArgs[i] = val
		}
	}

	partial := BuiltinFunction(func(args []any, focus any) (any, error) {
		fullArgs := slices.Clone(boundArgs)
		argIdx := 0
		for i, placeholder := range isPlaceholder {
			if placeholder && argIdx < len(args) {
				fullArgs[i] = args[argIdx]
				argIdx++
			}
		}
		return callFunction(fn, fullArgs, focus, env)
	})
	return partial, nil
}

func callFunction(fn any, args []any, focus any, env *Environment) (any, error) {
	if fn == nil {
		return nil, &JSONataError{Code: "T1006", Message: "attempted to invoke undefined function"}
	}
	counter := env.callCounter()

	// Trampoline loop: if the body returns a TailCall, re-invoke without
	// growing the Go stack. This handles both self-recursion and mutual recursion.
	// The iteration limit (counter.max * tailCallMultiplier) prevents infinite
	// tail-recursive loops from running forever.
	maxIter, iter := counter.max*10000, 0
	ctx := env.Context()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch f := fn.(type) {
		case *SignedBuiltin:
			return f.Fn(args, focus)
		case BuiltinFunction:
			return f(args, focus)
		case EnvAwareBuiltin:
			return f(args, focus, env)
		case *Lambda:
			if f.Sig != "" {
				specs, _ := parser.ParseSig(f.Sig)
				coerced, returnUndefined, err := processCallArgs(specs, args)
				if err != nil {
					return nil, err
				}
				if returnUndefined {
					return nil, nil
				}
				args = coerced
			}
			counter.depth++
			if counter.depth > counter.max {
				counter.depth--
				return nil, &JSONataError{Code: "U1001", Message: fmt.Sprintf("stack overflow error: evaluation exceeded stack depth %d", counter.max)}
			}
			childEnv := NewChildEnvironment(f.Closure)
			childEnv.calls = counter
			for i, param := range f.Params {
				if i < len(args) {
					childEnv.Bind(param, args[i])
				} else {
					childEnv.Bind(param, nil)
				}
			}
			bodyFocus := focus
			if len(f.Params) == 0 && len(args) == 0 {
				bodyFocus = f.CapturedFocus
			}
			result, err := Eval(f.Body, bodyFocus, childEnv)
			counter.depth--
			if err != nil {
				return nil, err
			}
			if tc, ok := result.(*TailCall); ok {
				iter++
				if iter > maxIter {
					return nil, &JSONataError{Code: "U1001", Message: fmt.Sprintf("stack overflow error: evaluation exceeded stack depth %d", counter.max)}
				}
				fn = tc.Fn
				args = tc.Args
				continue
			}
			return result, nil
		default:
			return nil, &JSONataError{Code: "T1006", Message: fmt.Sprintf("not a function: %T", fn)}
		}
	}
}
