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
		// jsonata-js wraps a call with a predicate or a # binding in a
		// path, which is not a tail call.
		node.TailContext = jsTail && len(node.Stages) == 0 && node.Index == ""
		// A jsonata-js tail call drops a group on the call ($f(){k: v}), as
		// trampolining does; any other call keeps it, so only one without a
		// group is trampolined.
		node.Thunk = node.TailContext || node.Group == nil
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
		// branches are a and b. In its tail position a chosen call a is
		// returned uncollapsed, as a tail call is (see evalDefaultLeft), but
		// gnata evaluates a once, as the condition, so only b is a tail call.
		if node.Value == "?:" || node.Value == "??" {
			node.Thunk = jsTail
			markTailPosition(node.Right, jsTail)
		}
	case NodeLambda:
		// Nested lambdas get their own tail-call analysis at compile time.
	}
}
