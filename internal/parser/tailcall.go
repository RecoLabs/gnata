package parser

// markTailCalls walks a lambda body and marks the function calls in tail
// position as Thunk, so the trampoline in invokeFunction avoids growing the
// Go stack for tail-recursive and mutually-recursive calls. TailContext marks
// the calls jsonata-js's tailCallOptimize turns into thunks, which take the
// context of the call that entered the lambda.
func markTailCalls(node *Node) {
	markTailPosition(node, true)
}

// markTailPosition marks the tail calls of node, and as TailContext when
// jsTail is set.
func markTailPosition(node *Node, jsTail bool) {
	if node == nil {
		return
	}
	switch node.Type {
	case NodeFunction:
		// gnata groups a call's result ($f(){k: v}), which a trampolined
		// call would bypass; jsonata-js drops the group instead.
		if node.Group == nil {
			node.Thunk = true
			// jsonata-js wraps a call with a predicate or a # binding in a
			// path, which is not a tail call.
			node.TailContext = jsTail && len(node.Stages) == 0 && node.Index == ""
		}
	case NodeCondition:
		markTailPosition(node.Then, jsTail)
		markTailPosition(node.Else, jsTail)
	case NodeBlock:
		if len(node.Expressions) > 0 {
			markTailPosition(node.Expressions[len(node.Expressions)-1], jsTail)
		}
	case NodeBind:
		// jsonata-js does not optimize a bind, but trampolining its right
		// side keeps recursion through it within the stack limit.
		markTailPosition(node.Right, false)
	case NodeBinary:
		// jsonata-js parses a ?: b and a ?? b as conditions on a whose
		// branches are a and b, evaluating a call a twice when it is chosen.
		// gnata evaluates a once, as the condition, so only b is a tail call.
		if node.Value == "?:" || node.Value == "??" {
			markTailPosition(node.Right, jsTail)
		}
	case NodeLambda:
		// Nested lambdas get their own tail-call analysis at compile time.
	}
}
