package parser

// ProcessAST runs the post-processing pass over the raw Pratt-parsed tree,
// transforming it into a form suitable for evaluation.
//
// Current transformations:
//   - Flattens nested binary(".") nodes into path nodes with Steps slices.
//   - Propagates KeepSingletonArray when any step has KeepArray=true.
//   - Marks last-step array constructors as ConsArray (jsonata-js consarray).
//   - Attaches group expressions from path-step binary("{") to the path.
//   - Recursively processes all child nodes.
//
// It cannot fail: of the static errors jsonata-js raises during its
// processAST, the parser reports S0208-S0212 and S0214-S0216, and the
// evaluator reports S0213 and S0217.
func ProcessAST(node *Node) *Node {
	if node == nil {
		return nil
	}

	switch node.Type {
	case NodeBinary:
		if node.Value == "." {
			return processDotBinary(node)
		}
		node.Left = ProcessAST(node.Left)
		node.Right = ProcessAST(node.Right)

	case NodeUnary:
		node.Expression = ProcessAST(node.Expression)
		processAll(node.Expressions)
		processAll(node.LHS)

	case NodeBlock:
		processBlockChildren(node)

	case NodeFunction, NodePartial:
		node.Procedure = ProcessAST(node.Procedure)
		processAll(node.Arguments)

	case NodeLambda:
		node.Body = ProcessAST(node.Body)
		markTailCalls(node.Body)

	case NodeCondition:
		node.Condition = ProcessAST(node.Condition)
		node.Then = ProcessAST(node.Then)
		node.Else = ProcessAST(node.Else)

	case NodeBind:
		node.Left = ProcessAST(node.Left)
		node.Right = ProcessAST(node.Right)

	case NodeTransform:
		node.Pattern = ProcessAST(node.Pattern)
		node.Update = ProcessAST(node.Update)
		node.Delete = ProcessAST(node.Delete)

	case NodeSort:
		node.Left = ProcessAST(node.Left)
		for i := range node.Terms {
			node.Terms[i].Expression = ProcessAST(node.Terms[i].Expression)
		}

	case NodePath:
		processPathChildren(node)

	default:
		// Leaf nodes (name, string, number, value, variable, wildcard, descendant, parent, regex).
		// Still need to process any attached Group expression.
		processGroup(node.Group)
	}
	return node
}

func processAll(nodes []*Node) {
	for i, n := range nodes {
		nodes[i] = ProcessAST(n)
	}
}

// processGroup processes group-by key/value pairs so nested dot expressions
// within them are resolved.
func processGroup(group *GroupExpr) {
	if group == nil {
		return
	}
	for i, pair := range group.Pairs {
		group.Pairs[i][0] = ProcessAST(pair[0])
		group.Pairs[i][1] = ProcessAST(pair[1])
	}
}

// processDotBinary flattens a binary(".") node into a path node.
func processDotBinary(node *Node) *Node {
	steps := collectPathSteps(node)

	path := &Node{
		Type:  NodePath,
		Value: node.Value,
		Steps: steps,
		Pos:   node.Pos,
		Group: node.Group, // propagate group-by expression (A.B{key:val})
	}
	markUnaryArraySteps(steps)

	// Propagate KeepSingletonArray when any step (or a subscript step's left side)
	// has KeepArray=true. This covers both A[].B and A[][filter].B patterns.
	for _, s := range steps {
		if s.KeepArray {
			path.KeepSingletonArray = true
			break
		}
		// A[][filter] — the KeepArray flag is on the Name/Var left of the subscript.
		if s.Type == NodeBinary && s.Value == "[" && s.Left != nil && s.Left.KeepArray {
			path.KeepSingletonArray = true
			break
		}
	}

	processGroup(path.Group)
	return path
}

// collectPathSteps recursively collects steps from binary(".") nodes.
func collectPathSteps(node *Node) []*Node {
	if node.Type != NodeBinary || node.Value != "." {
		// Leaf step — process it.
		processed := ProcessAST(node)
		// If the processed node is itself a path, splice its steps.
		if processed.Type == NodePath {
			return processed.Steps
		}
		promoteQuotedPathNames(processed)
		if processed.Type == NodeString {
			processed = &Node{Type: NodeName, Value: processed.Value, Pos: processed.Pos}
		}
		return []*Node{processed}
	}

	leftSteps := collectPathSteps(node.Left)
	rightSteps := collectPathSteps(node.Right)

	// The # and @ infix operators set Index/Focus on the binary "." node
	// itself (e.g. (Account.Order)#$o). Propagate these to the last left
	// step so the binding survives path flattening.
	if len(leftSteps) > 0 {
		last := leftSteps[len(leftSteps)-1]
		if node.Index != "" {
			last.Index = node.Index
		}
		if node.Focus != "" {
			last.Focus = node.Focus
		}
	}

	return append(leftSteps, rightSteps...)
}

func promoteQuotedPathNames(n *Node) {
	for n != nil && n.Type == NodeBinary && n.Value == "[" {
		if n.Left != nil && n.Left.Type == NodeString {
			left := n.Left
			n.Left = &Node{
				Type:      NodeName,
				Value:     left.Value,
				Pos:       left.Pos,
				KeepArray: left.KeepArray,
				Group:     left.Group,
				Index:     left.Index,
				Focus:     left.Focus,
			}
			return
		}
		n = n.Left
	}
}

// processBlockChildren recursively processes a block node.
func processBlockChildren(node *Node) {
	for i, expr := range node.Expressions {
		part := ProcessAST(expr)
		node.Expressions[i] = part
		if part.ConsArray || (part.Type == NodePath && len(part.Steps) > 0 && part.Steps[0].ConsArray) {
			node.ConsArray = true
		}
	}
}

// markUnaryArraySteps flags first/last path steps that are array constructors
// so evaluation can treat them as cons arrays (jsonata-js processAST).
func markUnaryArraySteps(steps []*Node) {
	if len(steps) == 0 {
		return
	}
	last := steps[len(steps)-1]
	if isUnaryArrayCtor(last) {
		last.ConsArray = true
	}
}

func isUnaryArrayCtor(n *Node) bool {
	return n != nil && n.Type == NodeUnary && n.Value == "["
}

// processPathChildren recursively processes an existing path node.
func processPathChildren(node *Node) {
	for i, step := range node.Steps {
		node.Steps[i] = ProcessAST(step)
		if node.Steps[i].KeepArray {
			node.KeepSingletonArray = true
		}
	}
	markUnaryArraySteps(node.Steps)
	processGroup(node.Group)
}
