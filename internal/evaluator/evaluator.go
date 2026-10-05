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
	} else if err := env.Err(); err != nil {
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
		return evalNumber(node, env), nil
	case parser.NodeVariable:
		return evalVariable(node, input, env)
	case parser.NodeName:
		if node.KeepArray {
			return keepStepResult(evalName(node, input, env))
		}
		return evalName(node, input, env)
	case parser.NodeWildcard:
		return evalWildcard(node, input, env)
	case parser.NodeDescendant:
		return evalDescendant(input, env)
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
		return evalSortNode(node, input, env)
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

// evalSortNode evaluates a sort outside a path. jsonata-js makes the sort
// a step of its left path, so a [] there or on the sort applies to the
// sorted result: o.b[]^($) is [5].
func evalSortNode(node *parser.Node, input any, env *Environment) (any, error) {
	sortFn := evalSort
	// If any sort term references %, we need tuple-aware path evaluation so
	// that each item carries its parent context during sorting.
	if slices.ContainsFunc(node.Terms, func(t parser.SortTerm) bool { return nodeHasParentRef(t.Expression) }) {
		sortFn = evalSortWithParentTracking
	}
	if parser.ChainKeepsArray(node) {
		return keepStepResult(sortFn(node, input, env))
	}
	return sortFn(node, input, env)
}

// keepStepResult applies a [] suffix to a name or sort outside a path, which
// jsonata-js evaluates as a one-step path: o^(b)[] is [o].
func keepStepResult(result any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if seq, ok := result.(*Sequence); ok {
		result = CollapseSequence(seq)
	}
	return keepSingletonArray(result), nil
}

// evalDescendant evaluates the ** operator, enforcing the sequence guardrail
// (if any) on the collected result.
func evalDescendant(input any, env *Environment) (any, error) {
	c := collector{env: env}
	c.descendants(input)
	if c.err != nil {
		return nil, c.err
	}
	return CollapseSequence(&Sequence{Values: c.values}), nil
}

// ApplyFunction is the public API used by the standard library to call
// any function value (BuiltinFunction or *Lambda) with the given args.
func ApplyFunction(fn any, args []any, focus any, env *Environment) (any, error) {
	return callFunction(fn, args, focus, env)
}
