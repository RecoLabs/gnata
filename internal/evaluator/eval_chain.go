package evaluator

import (
	"fmt"

	"github.com/recolabs/gnata/internal/parser"
)

func evalChain(right *parser.Node, piped, input any, env *Environment) (any, error) {
	// Handle right-associative chaining: a ~> (f ~> g) → (a ~> f) ~> g
	if right.Type == parser.NodeBinary && right.Value == "~>" {
		if r1, err := evalChain(right.Left, piped, input, env); err != nil {
			return nil, err
		} else if r1 == nil {
			return nil, nil
		} else {
			return evalChain(right.Right, r1, input, env)
		}
	}
	if right.Type == parser.NodeFunction && right.Procedure != nil {
		fn, err := Eval(right.Procedure, input, env)
		if err != nil {
			return nil, err
		}
		args := append(make([]any, 0, 1+len(right.Arguments)), piped)
		for _, argNode := range right.Arguments {
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
		result, err := callFunction(fn, args, input, env)
		if err != nil {
			return nil, err
		}
		return CollapseAndKeep(result, right.KeepArray), nil
	}
	// Right is a function reference or other expression.
	fn, err := Eval(right, input, env)
	if err != nil {
		return nil, err
	}
	// If right side is a regex map, test piped value against it.
	// MapGet returns (nil, false) for non-maps, so no explicit type guard needed.
	if _, hasPattern := MapGet(fn, "pattern"); hasPattern {
		regexMap, _ := fn.(map[string]any)
		if regexMap == nil {
			if om, ok := fn.(*OrderedMap); ok {
				regexMap = om.ToMap()
			}
		}
		return applyRegexTest(piped, regexMap)
	}
	// Validate right side is callable.
	switch fn.(type) {
	case BuiltinFunction, EnvAwareBuiltin, *Lambda, *SignedBuiltin:
		// OK
	case nil:
		return nil, &JSONataError{Code: "T1006", Message: "attempted to invoke undefined function"}
	default:
		return nil, &JSONataError{Code: "T2006", Message: fmt.Sprintf("the right-hand side of the ~> operator must be a function, got %T", fn)}
	}
	// If piped value is itself a function, create a function composition rather
	// than calling fn(piped). e.g. $trim ~> $uppercase creates a composed function.
	switch piped.(type) {
	case BuiltinFunction, EnvAwareBuiltin, *Lambda, *SignedBuiltin:
		return BuiltinFunction(func(args []any, focus any) (any, error) {
			intermediate, err := callFunction(piped, args, focus, env)
			if err != nil {
				return nil, err
			}
			intermediate = CollapseAndKeep(intermediate, false)
			res, err := callFunction(fn, []any{intermediate}, focus, env)
			if err != nil {
				return nil, err
			}
			return CollapseAndKeep(res, false), nil
		}), nil
	}
	result, err := callFunction(fn, []any{piped}, input, env)
	if err != nil {
		return nil, err
	}
	return CollapseAndKeep(result, false), nil
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
	return last, nil
}

func evalCondition(node *parser.Node, input any, env *Environment) (any, error) {
	cond, err := Eval(node.Condition, input, env)
	if err != nil {
		return nil, err
	}
	if ToBoolean(cond) {
		return Eval(node.Then, input, env)
	}
	if node.Else != nil {
		return Eval(node.Else, input, env)
	}
	return nil, nil
}

func evalBind(node *parser.Node, input any, env *Environment) (any, error) {
	val, err := Eval(node.Right, input, env)
	if err != nil {
		return nil, err
	}
	env.Bind(node.Left.Value, val)
	return val, nil
}
