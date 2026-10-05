package parser

import "math"

// markTransformDepths sets Depth on every transform in the tree rooted at n
// and returns how deeply evaluating n recurses. Evaluating a lambda or a
// transform only builds a function, whose body is accounted for when it is
// called, so each counts as one level here.
func markTransformDepths(n *Node) int {
	if n == nil {
		return 0
	}
	deepest := 0
	forEachChild(n, func(child *Node) {
		deepest = max(deepest, markTransformDepths(child))
	})
	switch n.Type {
	case NodeTransform:
		n.Depth = uint16(min(deepest, math.MaxUint16))
		return 1
	case NodeLambda:
		return 1
	}
	return deepest + 1
}

// forEachChild calls visit on every child node of n.
func forEachChild(n *Node, visit func(*Node)) {
	for _, children := range [][]*Node{n.Steps, n.Expressions, n.LHS, n.Arguments} {
		for _, child := range children {
			visit(child)
		}
	}
	for _, child := range []*Node{
		n.Body, n.Procedure, n.Left, n.Right, n.Expression, n.Condition, n.Then, n.Else, n.Pattern, n.Update, n.Delete,
	} {
		if child != nil {
			visit(child)
		}
	}
	for _, term := range n.Terms {
		visit(term.Expression)
	}
	for _, stage := range n.Stages {
		visit(stage.Expression)
	}
	if n.Group != nil {
		for _, pair := range n.Group.Pairs {
			visit(pair[0])
			visit(pair[1])
		}
	}
}
