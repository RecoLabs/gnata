package evaluator

import (
	"fmt"
	"slices"

	"github.com/recolabs/gnata/internal/parser"
)

// Eval evaluates an AST node against input data in the given environment.
// Returns (nil, nil) for undefined results.
func Eval(node *parser.Node, input any, env *Environment) (any, error) {
	if node == nil {
		return nil, nil
	} else if err := env.Context().Err(); err != nil {
		return nil, err
	} else if node.Group != nil {
		// If the node has a Group expression (A{key:val}), evaluate the base node
		// first, then apply the group-by reduction.
		return evalGroupBy(node, input, env)
	}

	switch node.Type {
	case parser.NodeValue:
		return evalValue(node)
	case parser.NodeString:
		return node.Value, nil
	case parser.NodeNumber:
		return node.NumVal, nil
	case parser.NodeVariable:
		return evalVariable(node, input, env)
	case parser.NodeName:
		return evalName(node, input, env)
	case parser.NodeWildcard:
		return evalWildcard(node, input, env)
	case parser.NodeDescendant:
		return descendantLookup(input), nil
	case parser.NodePath:
		return evalPath(node, input, env)
	case parser.NodeBinary, parser.NodeApply:
		return evalBinary(node, input, env)
	case parser.NodeUnary:
		return evalUnary(node, input, env)
	case parser.NodeBlock:
		return evalBlock(node, input, env)
	case parser.NodeCondition:
		return evalCondition(node, input, env)
	case parser.NodeBind:
		return evalBind(node, input, env)
	case parser.NodeFunction:
		return evalFunction(node, input, env)
	case parser.NodeLambda:
		return evalLambda(node, input, env)
	case parser.NodePartial:
		return evalPartial(node, input, env)
	case parser.NodeSort:
		// If any sort term references %, we need tuple-aware path evaluation so
		// that each item carries its parent context during sorting.
		if slices.ContainsFunc(node.Terms, func(t parser.SortTerm) bool { return nodeHasParentRef(t.Expression) }) {
			return evalSortWithParentTracking(node, input, env)
		}
		return evalSort(node, input, env)
	case parser.NodeRegex:
		return evalRegex(node.Value), nil
	case parser.NodeTransform:
		return evalTransform(node, input, env)
	case parser.NodeParent:
		// % retrieves the parent context value stored by evalPathTuple when it
		// expanded a path step. The parent is stored under parentKey ("%%") in
		// the child environment created by appendTupleResults.
		if val, ok := env.Lookup(parentKey); ok {
			return val, nil
		}
		return nil, &JSONataError{Code: "S0217", Message: "% operator used outside of a valid path context"}
	default:
		return nil, fmt.Errorf("unknown node type: %s", node.Type)
	}
}

// ApplyFunction is the public API used by the standard library to call
// any function value (BuiltinFunction or *Lambda) with the given args.
func ApplyFunction(fn any, args []any, focus any, env *Environment) (any, error) {
	return callFunction(fn, args, focus, env)
}
