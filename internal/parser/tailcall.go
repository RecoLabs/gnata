package parser

// markTailCalls walks a lambda body and sets Thunk=true on function call
// nodes that are in tail position. This enables the trampoline in callFunction
// to avoid growing the Go stack for tail-recursive and mutually-recursive calls.
func markTailCalls(node *Node) {
	if node == nil {
		return
	}
	markTailPosition(node)
}

func markTailPosition(node *Node) {
	if node == nil {
		return
	}
	switch node.Type {
	case NodeFunction:
		node.Thunk = true
	case NodeCondition:
		markTailPosition(node.Then)
		markTailPosition(node.Else)
	case NodeBlock:
		if len(node.Expressions) > 0 {
			markTailPosition(node.Expressions[len(node.Expressions)-1])
		}
	case NodeBind:
		markTailPosition(node.Right)
	case NodeLambda:
		// Nested lambdas get their own tail-call analysis at compile time.
	}
}
