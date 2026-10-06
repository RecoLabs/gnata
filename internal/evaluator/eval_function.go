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

	// jsonata-js runs a tail call from the trampoline of the call that
	// entered the lambda, so it takes that call's context.
	callFocus := input
	if node.TailContext {
		callFocus = env.callCounter().applyFocus
	}

	// jsonata-js applies a function argument with a null context, except in
	// a tail call, whose arguments its trampoline passes as they are. Named
	// builtins already apply function arguments with a null context.
	if !node.TailContext && reachesLambda(fn) {
		for i, arg := range args {
			args[i] = wrapFunctionArg(arg, env)
		}
	}

	// Tail-call optimization: if this call is in tail position within a
	// lambda body, return a TailCall sentinel instead of recursing.
	// The trampoline loop in invokeFunction will catch it.
	if node.Thunk {
		if lambda, applied := wrappedLambda(fn); applied {
			return &TailCall{Fn: lambda, Args: args, Focus: Null}, nil
		}
		if _, isLambda := fn.(*Lambda); isLambda {
			return &TailCall{Fn: fn, Args: args, Focus: callFocus}, nil
		}
	}

	result, err := callFunction(fn, args, callFocus, env)
	if err != nil {
		return nil, err
	}
	return CollapseAndKeep(result, node.KeepArray), nil
}

func evalLambda(node *parser.Node, input any, env *Environment) (any, error) {
	params := make([]string, 0, len(node.Arguments))
	for _, arg := range node.Arguments {
		params = append(params, arg.Value)
	}
	sig := ""
	var parsedSig []parser.ParamSpec
	var contextSig *ContextSig
	if node.Signature != nil {
		sig = node.Signature.Raw
		parsedSig, _ = parser.ParseSig(sig)
		contextSig = newContextSig(parsedSig)
	}
	return &Lambda{
		Params:        params,
		Body:          node.Body,
		Closure:       env,
		Thunk:         node.Thunk,
		Sig:           sig,
		ParsedSig:     parsedSig,
		Context:       contextSig,
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
	}
	switch fn.(type) {
	case BuiltinFunction, EnvAwareBuiltin, *Lambda, *SignedBuiltin:
		// OK
	case nil:
		return nil, &JSONataError{Code: "T1008", Message: "cannot partially apply a non-function: the function is not defined"}
	default:
		return nil, &JSONataError{Code: "T1008", Message: fmt.Sprintf("cannot partially apply a non-function: %T", fn)}
	}

	boundArgs := make([]any, len(node.Arguments))
	isPlaceholder := make([]bool, len(node.Arguments))
	placeholders := 0
	for i, argNode := range node.Arguments {
		if argNode.Type == parser.NodePlaceholder {
			isPlaceholder[i] = true
			placeholders++
		} else {
			val, err := Eval(argNode, input, env)
			if err != nil {
				return nil, err
			}
			boundArgs[i] = val
		}
	}

	// jsonata-js makes a partial application a lambda with one parameter per
	// placeholder, and applies the function it wraps without validating its
	// arguments.
	partial := &SignedBuiltin{
		Fn: func(args []any, focus any, _ *Environment) (any, error) {
			fullArgs := slices.Clone(boundArgs)
			argIdx := 0
			for i, placeholder := range isPlaceholder {
				if placeholder && argIdx < len(args) {
					fullArgs[i] = args[argIdx]
					argIdx++
				}
			}
			return invokeFunction(fn, fullArgs, focus, env, false)
		},
		Arity:        placeholders,
		CallsLambdas: isLambda(fn),
	}
	return partial, nil
}

// wrapFunctionArg wraps a function passed as an argument to a lambda so it
// is applied with a null context, as jsonata-js applies the closure it wraps
// function arguments in.
func wrapFunctionArg(arg any, env *Environment) any {
	if !isCallable(arg) {
		return arg
	}
	sb, isSigned := arg.(*SignedBuiltin)
	if isSigned && sb.Argument != nil {
		return arg
	}
	arity, known := FunctionArity(arg)
	if !known {
		arity = -1
	}
	return &SignedBuiltin{
		Fn: func(args []any, _ any, _ *Environment) (any, error) {
			return callFunction(arg, args, Null, env)
		},
		Arity:    arity,
		Argument: arg,
		// A partial application or composition of lambdas calls only
		// lambdas too, and as no wrapper wraps another argument wrapper, a
		// chain of these stays two wrappers long.
		CallsLambdas: isLambda(arg) || isSigned && sb.CallsLambdas,
	}
}

// FunctionArity returns the number of parameters fn declares: a lambda's
// parameters, or a builtin's, partial application's or composition's
// jsonata-js arity. known is false for a custom function, whose arity gnata
// cannot see.
func FunctionArity(fn any) (arity int, known bool) {
	switch f := fn.(type) {
	case *Lambda:
		return len(f.Params), true
	case *SignedBuiltin:
		return f.Arity, f.Arity >= 0
	}
	return 0, false
}

// reachesLambda reports whether calling fn can evaluate a lambda body with
// its arguments: fn is a lambda, or a partial application, composition or
// function argument (a SignedBuiltin without a name).
func reachesLambda(fn any) bool {
	switch f := fn.(type) {
	case *Lambda:
		return true
	case *SignedBuiltin:
		return f.isWrapper()
	}
	return false
}

// isCallable reports whether v is a function value. Unlike sigSymbol, it
// does not count a regex.
func isCallable(v any) bool {
	switch v.(type) {
	case BuiltinFunction, EnvAwareBuiltin, *Lambda, *SignedBuiltin:
		return true
	}
	return false
}

// isLambda reports whether fn is a lambda.
func isLambda(fn any) bool {
	_, ok := fn.(*Lambda)
	return ok
}

// wrappedLambda returns the lambda that fn wraps as a function argument.
func wrappedLambda(fn any) (*Lambda, bool) {
	sb, isSigned := fn.(*SignedBuiltin)
	if !isSigned {
		return nil, false
	}
	lambda, isLambda := sb.Argument.(*Lambda)
	return lambda, isLambda
}

// stackOverflowError reports the recursion-depth error for the given counter,
// using D1011 when the limit came from the WithStack guardrail and the
// built-in U1001 otherwise.
func stackOverflowError(counter *callCounter) error {
	if counter.flags&stackIsLimit != 0 {
		return &JSONataError{
			Code:    "D1011",
			Message: fmt.Sprintf("Stack overflow error: stack depth exceeded %d. Check for non-terminating recursive function", counter.max),
		}
	}
	return &JSONataError{Code: "U1001", Message: fmt.Sprintf("stack overflow error: evaluation exceeded stack depth %d", counter.max)}
}

// callWrapper calls a partial application, composition or argument wrapper,
// which calls the function it wraps on the Go stack.
func callWrapper(f *SignedBuiltin, args []any, focus any, env *Environment, counter *callCounter) (any, error) {
	if f.CallsLambdas {
		return f.Fn(args, focus, env)
	}
	return counter.callNested(1, func() (any, error) {
		return f.Fn(args, focus, env)
	})
}

// callFunction applies fn as jsonata-js's apply does: focus is the call's
// context, which fills a missing context argument before the arguments are
// validated.
func callFunction(fn any, args []any, focus any, env *Environment) (any, error) {
	return invokeFunction(fn, args, focus, env, true)
}

// invokeFunction is callFunction, checking the first call's arguments only
// when checked is set. Tail calls are always checked.
func invokeFunction(fn any, args []any, focus any, env *Environment, checked bool) (any, error) {
	counter := env.callCounter()

	// Trampoline loop: if the body returns a TailCall, re-invoke without
	// growing the Go stack. This handles both self-recursion and mutual recursion.
	// The iteration limit (counter.max * tailCallMultiplier) prevents infinite
	// tail-recursive loops from running forever.
	maxIter, iter := int(counter.max)*10000, 0
	for {
		if err := env.errNow(); err != nil {
			return nil, err
		}
		if fn == nil {
			return nil, &JSONataError{Code: "T1006", Message: "attempted to invoke undefined function"}
		}
		if checked {
			var returnUndefined bool
			var err error
			if args, returnUndefined, err = checkCallArgs(fn, args, focus); err != nil || returnUndefined {
				return nil, err
			}
		}
		checked = true
		switch f := fn.(type) {
		case *SignedBuiltin:
			if !f.isWrapper() {
				return f.Fn(args, focus, env)
			}
			return callWrapper(f, args, focus, env, counter)
		case BuiltinFunction:
			return f(args, focus)
		case EnvAwareBuiltin:
			return f(args, focus, env)
		case *Lambda:
			counter.depth++
			if counter.depth > counter.max {
				counter.depth--
				return nil, stackOverflowError(counter)
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
			outerFocus := counter.applyFocus
			counter.applyFocus = focus
			result, err := Eval(f.Body, f.CapturedFocus, childEnv)
			counter.applyFocus = outerFocus
			counter.depth--
			if err != nil {
				return nil, err
			}
			if tc, ok := result.(*TailCall); ok {
				iter++
				if iter > maxIter {
					return nil, &JSONataError{Code: "U1001", Message: fmt.Sprintf("stack overflow error: evaluation exceeded stack depth %d", counter.max)}
				}
				fn, args, focus = tc.Fn, tc.Args, tc.Focus
				continue
			}
			return result, nil
		default:
			return nil, &JSONataError{Code: "T1006", Message: fmt.Sprintf("not a function: %T", fn)}
		}
	}
}
