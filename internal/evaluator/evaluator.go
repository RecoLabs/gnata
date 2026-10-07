package evaluator

import (
	"fmt"

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
			return keepStepResult(evalBoundedName(node, input, env))
		}
		return evalBoundedName(node, input, env)
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
		// The step the parser resolved % to binds its slot's label to its
		// input; a % no step resolved, as in a lambda, is undefined.
		val, _ := env.Lookup(node.Slot.Label)
		return val, nil
	default:
		return nil, fmt.Errorf("unknown node type: %s", node.Type)
	}
}

// evalSortNode evaluates a sort outside a path. jsonata-js makes the sort
// a step of its left path, so a [] there or on the sort applies to the
// sorted result: o.b[]^($) is [5].
func evalSortNode(node *parser.Node, input any, env *Environment) (any, error) {
	result, err := evalSortStep(node, input, env)
	if seq, ok := result.(*Sequence); ok && err == nil {
		return CollapseSequence(seq), nil
	}
	return result, err
}

// evalSortStep is evalSortNode for a sort whose result a later stage or
// path step reads: a lone array item stays a one-item sequence.
func evalSortStep(node *parser.Node, input any, env *Environment) (any, error) {
	if parser.ChainKeepsArray(node) {
		return keepStepResult(evalSort(node, input, env))
	}
	return evalSort(node, input, env)
}

// keepStepResult applies a [] suffix to a name or sort outside a path, which
// jsonata-js evaluates as a one-step path: o^(b)[] is [o].
func keepStepResult(result any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if seq, ok := result.(*Sequence); ok {
		if len(seq.Values) == 1 {
			return KeptArray{seq.Values[0]}, nil
		}
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

// ApplyFunction is the public API used by the standard library to call a
// function argument with the given args. Like jsonata-js, which wraps a
// function argument in a closure that applies it with a null context, it
// calls fn with a null context. The result is an item the calling function
// holds (see holdResult). A builtin callback spends the nesting budget,
// since it can recurse on the Go stack with no lambda call, as $sort does
// as $sort's comparator. A lambda is bounded by the call depth, and a
// wrapper by the call depth or the budget it spends itself.
func ApplyFunction(fn any, args []any, env *Environment) (any, error) {
	call := func() (any, error) { return callFunction(fn, args, Null, env) }
	var result any
	var err error
	if reachesLambda(fn) {
		result, err = call()
	} else {
		result, err = env.callCounter().callNested(1, call)
	}
	if err != nil {
		return nil, err
	}
	return holdResult(result), nil
}
