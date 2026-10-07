package evaluator

import (
	"fmt"

	"github.com/recolabs/gnata/internal/parser"
)

// evalChain applies right to piped, whose shape (see argShape) is
// pipedShape, returning the result and its shape.
func evalChain(right *parser.Node, piped any, pipedShape argShape, input any, env *Environment) (any, argShape, error) {
	// Handle right-associative chaining: a ~> (f ~> g) → (a ~> f) ~> g
	if right.Type == parser.NodeBinary && right.Value == "~>" {
		if r1, shape, err := evalChain(right.Left, piped, pipedShape, input, env); err != nil {
			return nil, argShape{}, err
		} else if r1 == nil {
			return nil, argShape{}, nil
		} else {
			return evalChain(right.Right, r1, shape, input, env)
		}
	}
	if right.Type == parser.NodeFunction && right.Procedure != nil {
		fn, err := Eval(right.Procedure, input, env)
		if err != nil {
			return nil, argShape{}, err
		}
		args := append(make([]any, 0, 1+len(right.Arguments)), piped)
		for _, argNode := range right.Arguments {
			if argNode.Type == parser.NodePlaceholder {
				args = append(args, nil)
				continue
			}
			val, err := Eval(argNode, input, env)
			if err != nil {
				return nil, argShape{}, err
			}
			args = append(args, val)
		}
		// Like a direct call, wrap the function arguments, but not the piped
		// value, which jsonata-js passes as it is.
		if reachesLambda(fn) {
			for i, arg := range args[1:] {
				args[i+1] = wrapFunctionArg(arg, env)
			}
		}
		return callShaped(fn, args, pipedShape, input, env, right.KeepArray)
	}
	// Right is a function reference or other expression.
	fn, err := Eval(right, input, env)
	if err != nil {
		return nil, argShape{}, err
	}
	if fn == nil {
		return nil, argShape{}, &JSONataError{Code: "T1006", Message: "attempted to invoke undefined function"}
	}
	if !IsFunction(fn) {
		return nil, argShape{}, &JSONataError{
			Code:    "T2006",
			Message: fmt.Sprintf("the right-hand side of the ~> operator must be a function, got %T", fn),
		}
	}
	// If piped value is itself a function, create a function composition rather
	// than calling fn(piped). e.g. $trim ~> $uppercase creates a composed function.
	// As in jsonata-js's λ($x){ $g($f($x)) }, $f gets one argument and a null
	// context, and $g, a tail call, the context of the call to the composition.
	if IsFunction(piped) {
		return &SignedBuiltin{
			Fn: func(args []any, focus any, _ *Environment) (any, error) {
				var x any
				if len(args) > 0 {
					x = args[0]
				}
				intermediate, err := callFunction(piped, []any{x}, Null, env)
				if err != nil {
					return nil, err
				}
				intermediate = CollapseAndKeep(intermediate, false)
				res, err := callFunction(fn, []any{intermediate}, focus, env)
				if err != nil {
					return nil, err
				}
				return CollapseAndKeep(res, false), nil
			},
			Arity:        1,
			CallsLambdas: isLambda(piped) && isLambda(fn),
		}, argShape{}, nil
	}
	// jsonata-js applies a bare function reference with a null context.
	return callShaped(fn, []any{piped}, pipedShape, Null, env, false)
}

func evalBlock(node *parser.Node, input any, env *Environment) (any, error) {
	childEnv := NewChildEnvironment(env)
	var last any
	for _, expr := range node.Expressions {
		val, err := Eval(expr, input, childEnv)
		if err != nil {
			return nil, err
		}
		last = val
	}
	return settleRaw(last, node.KeepArray), nil
}

func evalCondition(node *parser.Node, input any, env *Environment) (any, error) {
	cond, err := Eval(node.Condition, input, env)
	if err != nil {
		return nil, err
	}
	truthy, err := ToBooleanEnv(cond, env)
	if err != nil {
		return nil, err
	}
	branch := node.Then
	if !truthy {
		branch = node.Else
	}
	return evalSettled(branch, input, env, node.KeepArray)
}

func evalBind(node *parser.Node, input any, env *Environment) (any, error) {
	val, _, err := evalShapedBind(node, input, env)
	return val, err
}
