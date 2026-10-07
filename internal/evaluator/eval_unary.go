package evaluator

import (
	"fmt"

	"github.com/recolabs/gnata/internal/decimal"
	"github.com/recolabs/gnata/internal/parser"
)

func evalUnary(node *parser.Node, input any, env *Environment) (any, error) {
	switch node.Value {
	case "-":
		val, err := Eval(node.Expression, input, env)
		if err != nil {
			return nil, err
		}
		if val == nil {
			return nil, nil
		}
		if prec := env.DecimalPrecision(); prec > 0 {
			if d, ok := decimal.FromValue(val, prec); ok {
				return d.Neg().Value(prec), nil
			}
		}
		f, ok := ToFloat64(val)
		if !ok {
			return nil, &JSONataError{
				Code:    "D1002",
				Message: fmt.Sprintf("the operand of the unary - operator must evaluate to a number, got %T", val),
			}
		}
		return -f, nil

	case "[":
		// Array constructor.
		result := make([]any, 0, len(node.Expressions))
		for _, expr := range node.Expressions {
			val, err := Eval(expr, input, env)
			if err != nil {
				return nil, err
			}
			if val == nil {
				continue
			}
			// Determine whether this expression is an explicit array constructor
			// (NodeUnary "["). Explicit inner arrays are preserved as single elements.
			// All other expressions that return arrays/sequences are spread.
			isExplicitArray := isArrayConstructor(expr)
			// jsonata-js appends every other value with $append, which counts
			// against the sequence guardrail; a nested constructor is pushed.
			if !isExplicitArray {
				if err := env.CheckSequence(len(result) + appendLength(val)); err != nil {
					return nil, err
				}
			}
			switch v := val.(type) {
			case *Sequence:
				if v.ConsArray || isExplicitArray {
					result = append(result, CollapseSequence(v))
				} else {
					result = append(result, v.Values...)
				}
			case []any, ConsArray, KeptArray, RawSequence:
				if isExplicitArray {
					result = append(result, val)
				} else {
					arr, _ := AsArray(v)
					result = append(result, arr...)
				}
			default:
				result = append(result, val)
			}
		}
		if node.ConsArray {
			return ConsArray(result), nil
		}
		return result, nil

	case "{":
		return evalObjectConstructor(node, input, env)

	default:
		return nil, fmt.Errorf("unknown unary operator: %s", node.Value)
	}
}

func evalObjectConstructor(node *parser.Node, input any, env *Environment) (any, error) {
	// As jsonata-js evaluateGroupExpression, an object constructor over an
	// array groups its items, unless the array is the root input jsonata-js
	// wraps as one item, which its pairs then see unwrapped.
	if seq, ok := input.(*Sequence); ok {
		input = CollapseSequence(seq)
	}
	if items, ok := AsArray(input); ok {
		if !isRootInput(items, env) {
			return groupItems(objectPairs(node.LHS), items, env)
		}
		env = unwrapRoot(env)
	}
	result := NewOrderedMap()
	for i := 0; i+1 < len(node.LHS); i += 2 {
		keyNode := node.LHS[i]
		valNode := node.LHS[i+1]

		keyVal, err := Eval(keyNode, input, env)
		if err != nil {
			return nil, err
		}
		if keyVal == nil {
			continue
		}
		key, ok := keyVal.(string)
		if !ok {
			return nil, &JSONataError{Code: "T1003", Message: fmt.Sprintf("key expression must evaluate to a string, got %T", keyVal)}
		}
		if result.Has(key) {
			return nil, &JSONataError{Code: "D1009", Message: fmt.Sprintf("duplicate key: %q", key)}
		}
		val, err := Eval(valNode, input, env)
		if err != nil {
			return nil, err
		}
		if seq, ok2 := val.(*Sequence); ok2 {
			val = CollapseSequence(seq)
		}
		if val == nil {
			continue
		}
		result.Set(key, val)
	}
	return result, nil
}

// isArrayConstructor reports whether n is a [...] constructor, possibly
// subscripted as in [[1,2]][0], which jsonata-js pushes into an enclosing
// constructor as one value.
func isArrayConstructor(n *parser.Node) bool {
	for isSubscript(n) && n.Left != nil {
		n = n.Left
	}
	return n.Type == parser.NodeUnary && n.Value == "["
}
