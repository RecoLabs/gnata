package evaluator

import (
	"errors"
	"fmt"
	"slices"

	"github.com/recolabs/gnata/internal/parser"
)

func evalFunction(node *parser.Node, input any, env *Environment) (any, error) {
	result, err := evalFunctionSequence(node, input, env)
	if err != nil {
		return nil, err
	}
	return CollapseAndKeep(result, node.KeepArray), nil
}

// evalFunctionSequence calls the function node without collapsing a
// *Sequence result, so a predicate on the call ($filter(...)[0]) sees the
// sequence's items, matching jsonata-js.
func evalFunctionSequence(node *parser.Node, input any, env *Environment) (any, error) {
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
	var shape argShape
	for i, argNode := range node.Arguments {
		if argNode.Type == parser.NodePlaceholder {
			args = append(args, nil)
			continue
		}
		var val any
		var err error
		if i == 0 {
			val, shape, err = evalShapedArg(argNode, input, env)
		} else {
			val, err = Eval(argNode, input, env)
		}
		if err != nil {
			return nil, err
		}
		args = append(args, val)
	}

	// Validate signature for SignedBuiltins at the direct call site.
	// HOF callbacks bypass this (they go through ApplyFunction instead).
	if sb, ok := fn.(*SignedBuiltin); ok {
		coerced, returnUndefined, sigErr := processCallArgs(sb.ParsedSig, args, input)
		if sigErr != nil {
			return nil, sigErr
		}
		if returnUndefined {
			return nil, nil
		}
		args = coerced
	}

	// Tail-call optimization: if this call is in tail position within a
	// lambda body, return a TailCall sentinel instead of recursing, which
	// callFunction applies (see TailCall).
	if node.Thunk {
		return &TailCall{Fn: fn, Args: args}, nil
	}

	result, err := callFunction(fn, args, input, env)
	if seq, ok := result.(*Sequence); ok && seq.ArgShaped && !shape.sequence() {
		return slices.Clip(seq.Values), err
	}
	return result, err
}

// argShape records whether a call's first argument is a sequence in
// jsonata-js, for a built-in whose result takes that shape (see
// Sequence.ArgShaped): $distinct keeps a plain array a plain array but
// collapses a sequence. An array constructor, a sort, a number-literal pick
// of an array item, a field that one object holds as an array, and a call
// returning anything but a sequence are plain arrays; anything else counts
// as a sequence, a variable included, since gnata keeps no sequence mark on
// stored values.
type argShape struct {
	plain  bool
	last   *parser.Node // a path's last step whose input decides (see lastStepSequence)
	lastIn any
}

func (a argShape) sequence() bool {
	if a.last != nil {
		return lastStepSequence(a.last, a.lastIn)
	}
	return !a.plain
}

// evalShapedArg evaluates a call's first argument and its shape.
func evalShapedArg(node *parser.Node, input any, env *Environment) (any, argShape, error) {
	if node.Group != nil || node.KeepArray {
		v, err := Eval(node, input, env)
		return v, argShape{}, err
	}
	switch node.Type {
	case parser.NodeFunction:
		raw, err := evalFunctionSequence(node, input, env)
		_, seq := raw.(*Sequence)
		return CollapseAndKeep(raw, false), argShape{plain: !seq}, err
	case parser.NodeUnary, parser.NodeSort:
		v, err := Eval(node, input, env)
		return v, argShape{plain: node.Type == parser.NodeSort || node.Value == "["}, err
	case parser.NodeName, parser.NodeBinary:
		v, err := Eval(node, input, env)
		return v, argShape{last: node, lastIn: input}, err
	case parser.NodePath:
		if pathHasTupleStep(node.Steps) {
			v, err := Eval(node, input, env)
			return v, argShape{}, err
		}
		v, lastIn, err := evalPathSimpleLast(node, input, env)
		return v, argShape{last: node.Steps[len(node.Steps)-1], lastIn: lastIn}, err
	case parser.NodeBlock:
		return evalShapedBlock(node, input, env)
	}
	v, err := Eval(node, input, env)
	return v, argShape{}, err
}

// evalShapedBlock is evalBlock, taking the shape of its last expression.
func evalShapedBlock(node *parser.Node, input any, env *Environment) (any, argShape, error) {
	if len(node.Expressions) == 0 {
		return nil, argShape{}, nil
	}
	childEnv := NewChildEnvironment(env)
	last := len(node.Expressions) - 1
	for _, expr := range node.Expressions[:last] {
		if _, err := Eval(expr, input, childEnv); err != nil {
			return nil, argShape{}, err
		}
	}
	v, shape, err := evalShapedArg(node.Expressions[last], input, childEnv)
	return settleRaw(v, false), shape, err
}

// lastStepSequence reports whether a path whose last step is step, mapped
// over in, yields a sequence in jsonata-js. It does unless the step is a
// field that exactly one item holds, as a plain array, which evaluateStep
// returns as is, or a number-literal pick from one item, which returns an
// array item whole. A filter always yields a sequence.
func lastStepSequence(step *parser.Node, in any) bool {
	items, mapped := in.([]any)
	switch {
	case step.Index != "" || step.Focus != "" || step.KeepArray:
		return true
	case step.Type == parser.NodeBinary && step.Value == "[":
		return step.Right.Type != parser.NodeNumber || mapped && len(items) > 1
	case step.Type == parser.NodeSort:
		return false
	case step.Type != parser.NodeName:
		return true
	case !mapped:
		return false
	}
	defined, plain := 0, false
	for _, item := range items {
		if _, nested := AsArray(item); nested {
			return true
		}
		if val, found := MapGet(item, step.Value); found {
			defined++
			if defined > 1 {
				return true
			}
			_, plain = val.([]any)
		}
	}
	return !plain
}

func evalLambda(node *parser.Node, input any, env *Environment) (any, error) {
	params := make([]string, 0, len(node.Arguments))
	for _, arg := range node.Arguments {
		params = append(params, arg.Value)
	}
	sig := ""
	var parsedSig []parser.ParamSpec
	if node.Signature != nil {
		sig = node.Signature.Raw
		parsedSig, _ = parser.ParseSig(sig)
	}
	return &Lambda{
		Params:        params,
		Body:          node.Body,
		Closure:       env,
		Thunk:         node.Thunk,
		Sig:           sig,
		ParsedSig:     parsedSig,
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

// stackOverflowError reports the recursion-depth error for the given counter,
// using D1011 when the limit came from the WithStack guardrail and the
// built-in U1001 otherwise.
func stackOverflowError(counter *callCounter) error {
	if counter.stackIsLimit {
		return &JSONataError{
			Code:    "D1011",
			Message: fmt.Sprintf("Stack overflow error: stack depth exceeded %d. Check for non-terminating recursive function", counter.max),
		}
	}
	return &JSONataError{Code: "U1001", Message: fmt.Sprintf("stack overflow error: evaluation exceeded stack depth %d", counter.max)}
}

// evalLambdaBody evaluates f's body for one call, counting its depth. The
// result is a lambda's TailCall for the trampoline, or the body's value: a
// built-in tail call runs here while the depth is counted, so one calling
// back into a lambda stays within the stack limit.
func evalLambdaBody(f *Lambda, args []any, focus any, env *Environment, counter *callCounter) (any, error) {
	counter.depth++
	defer func() { counter.depth-- }()
	if counter.depth > counter.max {
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
	bodyFocus := focus
	if len(f.Params) == 0 && len(args) == 0 {
		bodyFocus = f.CapturedFocus
	}
	result, err := Eval(f.Body, bodyFocus, childEnv)
	if tc, isTail := result.(*TailCall); isTail && err == nil {
		if _, isLambda := tc.Fn.(*Lambda); !isLambda {
			return callFunction(tc.Fn, tc.Args, focus, env)
		}
	}
	return result, err
}

// callBuiltin calls a builtin with each typed array argument (see
// typedArray) as a plain array. jsonata-js passes the array object
// itself, so a builtin that returns an argument unchanged returns it as is.
func callBuiltin(args []any, call func([]any) (any, error)) (any, error) {
	result, err := call(plainArrayArgs(args))
	if err != nil {
		return nil, err
	}
	if arr, ok := result.([]any); ok {
		for _, arg := range args {
			if typed, ok := typedArray(arg); ok && sameArray(typed, arr) {
				return arg, nil
			}
		}
	}
	return result, nil
}

// sameArray reports whether a and b are the same slice, even when empty:
// $reverse of an empty raw sequence returns that sequence.
func sameArray(a, b []any) bool {
	return len(a) == len(b) && cap(a) > 0 && cap(b) > 0 && &a[:1][0] == &b[:1][0]
}

// plainArrayArgs returns args with each top-level typed array as a plain
// []any, copying args only when one is found.
func plainArrayArgs(args []any) []any {
	out, copied := args, false
	for i, arg := range args {
		if typed, ok := typedArray(arg); ok {
			if !copied {
				out, copied = slices.Clone(args), true
			}
			out[i] = typed
		}
	}
	return out
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
	maxIter, iter := int(counter.max)*10000, 0
	for {
		if err := env.errNow(); err != nil {
			return nil, err
		}
		switch f := fn.(type) {
		case *SignedBuiltin:
			return callBuiltin(args, func(args []any) (any, error) { return f.Fn(args, focus) })
		case BuiltinFunction:
			return callBuiltin(args, func(args []any) (any, error) { return f(args, focus) })
		case EnvAwareBuiltin:
			return callBuiltin(args, func(args []any) (any, error) { return f(args, focus, env) })
		case *Lambda:
			if f.Sig != "" {
				coerced, returnUndefined, err := processCallArgs(f.ParsedSig, args, focus)
				if err != nil {
					return nil, err
				}
				if returnUndefined {
					return nil, nil
				}
				args = coerced
			}
			result, err := evalLambdaBody(f, args, focus, env, counter)
			if err != nil {
				return nil, err
			}
			if tc, isTail := result.(*TailCall); isTail {
				iter++
				if iter > maxIter {
					return nil, &JSONataError{Code: "U1001", Message: fmt.Sprintf("stack overflow error: evaluation exceeded stack depth %d", counter.max)}
				}
				fn, args = tc.Fn, tc.Args
				continue
			}
			return result, nil
		default:
			return nil, &JSONataError{Code: "T1006", Message: fmt.Sprintf("not a function: %T", fn)}
		}
	}
}
