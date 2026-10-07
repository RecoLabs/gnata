package parser

import "math"

// markFunctionDepths sets Depth on every transform and lambda in the tree
// rooted at n and returns how deeply evaluating n recurses. Evaluating a
// lambda or a transform only builds a function, whose body is accounted for
// when it is called, so each counts as one level here.
func markFunctionDepths(n *Node) int {
	deepest := max(
		deepestOf(n.Steps), deepestOf(n.Expressions), deepestOf(n.LHS), deepestOf(n.Arguments),
		depthOf(n.Body), depthOf(n.Procedure), depthOf(n.Left), depthOf(n.Right),
		depthOf(n.Expression), depthOf(n.Condition), depthOf(n.Then), depthOf(n.Else),
		depthOf(n.Pattern), depthOf(n.Update), depthOf(n.Delete),
	)
	for _, term := range n.Terms {
		deepest = max(deepest, depthOf(term.Expression))
	}
	for _, stage := range n.Stages {
		deepest = max(deepest, depthOf(stage.Expression))
	}
	if n.Group != nil {
		for _, pair := range n.Group.Pairs {
			deepest = max(deepest, depthOf(pair[0]), depthOf(pair[1]))
		}
	}
	switch n.Type {
	case NodeTransform, NodeLambda:
		n.Depth = uint16(min(deepest, math.MaxUint16))
		return 1
	}
	return deepest + 1
}

// depthOf is markFunctionDepths for a child that may be absent.
func depthOf(n *Node) int {
	if n == nil {
		return 0
	}
	return markFunctionDepths(n)
}

// deepestOf returns the deepest of nodes, as markFunctionDepths measures.
func deepestOf(nodes []*Node) int {
	deepest := 0
	for _, n := range nodes {
		deepest = max(deepest, depthOf(n))
	}
	return deepest
}
