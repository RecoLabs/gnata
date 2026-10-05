package parser

// ProcessAST runs the post-processing pass over the raw Pratt-parsed tree,
// transforming it into a form suitable for evaluation.
//
// Current transformations:
//   - Flattens nested binary(".") nodes into path nodes with Steps slices.
//   - Propagates KeepSingletonArray when any step has KeepArray=true.
//   - Marks array-constructor steps as ConsArray (jsonata-js consarray).
//   - Attaches group expressions from path-step binary("{") to the path.
//   - Recursively processes all child nodes.
func ProcessAST(node *Node) (*Node, error) {
	if node == nil {
		return nil, nil
	}

	switch node.Type {
	case NodeBinary:
		if node.Value == "." {
			return processDotBinary(node)
		}
		return processBinaryChildren(node)

	case NodeUnary:
		return processUnaryChildren(node)

	case NodeBlock:
		return processBlockChildren(node)

	case NodeFunction, NodePartial:
		return processFunctionChildren(node)

	case NodeLambda:
		return processLambdaChildren(node)

	case NodeCondition:
		return processConditionChildren(node)

	case NodeBind:
		return processBindChildren(node)

	case NodeTransform:
		return processTransformChildren(node)

	case NodeSort:
		return processSortChildren(node)

	case NodePath:
		return processPathChildren(node)

	default:
		// Leaf nodes (name, string, number, value, variable, wildcard, descendant, parent, regex).
		// Still need to process any attached Group expression.
		if node.Group != nil {
			var err error
			for i, pair := range node.Group.Pairs {
				node.Group.Pairs[i][0], err = ProcessAST(pair[0])
				if err != nil {
					return nil, err
				}
				node.Group.Pairs[i][1], err = ProcessAST(pair[1])
				if err != nil {
					return nil, err
				}
			}
		}
		return node, nil
	}
}

// processDotBinary flattens a binary(".") node into a path node.
func processDotBinary(node *Node) (*Node, error) {
	// Collect all steps from nested dots.
	steps, err := collectPathSteps(node)
	if err != nil {
		return nil, err
	}

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

	// Process group-by key/value pairs so nested dot expressions within them are resolved.
	if path.Group != nil {
		for i, pair := range path.Group.Pairs {
			path.Group.Pairs[i][0], err = ProcessAST(pair[0])
			if err != nil {
				return nil, err
			}
			path.Group.Pairs[i][1], err = ProcessAST(pair[1])
			if err != nil {
				return nil, err
			}
		}
	}

	return path, nil
}

// collectPathSteps recursively collects steps from binary(".") nodes.
func collectPathSteps(node *Node) ([]*Node, error) {
	if node.Type != NodeBinary || node.Value != "." {
		// Leaf step — process it.
		processed, err := ProcessAST(node)
		if err != nil {
			return nil, err
		}
		// If the processed node is itself a path, splice its steps.
		if processed.Type == NodePath {
			return processed.Steps, nil
		}
		promoteQuotedPathNames(processed)
		if processed.Type == NodeString {
			processed = &Node{Type: NodeName, Value: processed.Value, Pos: processed.Pos}
		}
		return []*Node{processed}, nil
	}

	leftSteps, err := collectPathSteps(node.Left)
	if err != nil {
		return nil, err
	}
	rightSteps, err := collectPathSteps(node.Right)
	if err != nil {
		return nil, err
	}

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

	return append(leftSteps, rightSteps...), nil
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

// processBinaryChildren recursively processes a non-dot binary node.
func processBinaryChildren(node *Node) (*Node, error) {
	var err error
	node.Left, err = ProcessAST(node.Left)
	if err != nil {
		return nil, err
	}
	node.Right, err = ProcessAST(node.Right)
	if err != nil {
		return nil, err
	}
	return node, nil
}

// processUnaryChildren recursively processes a unary node.
func processUnaryChildren(node *Node) (*Node, error) {
	var err error
	if node.Expression != nil {
		node.Expression, err = ProcessAST(node.Expression)
		if err != nil {
			return nil, err
		}
	}
	for i, expr := range node.Expressions {
		node.Expressions[i], err = ProcessAST(expr)
		if err != nil {
			return nil, err
		}
	}
	for i, n := range node.LHS {
		node.LHS[i], err = ProcessAST(n)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

// processBlockChildren recursively processes a block node.
func processBlockChildren(node *Node) (*Node, error) {
	var err error
	for i, expr := range node.Expressions {
		node.Expressions[i], err = ProcessAST(expr)
		if err != nil {
			return nil, err
		}
		part := node.Expressions[i]
		if part.ConsArray || (part.Type == NodePath && len(part.Steps) > 0 && part.Steps[0].ConsArray) {
			node.ConsArray = true
		}
	}
	return node, nil
}

// markUnaryArraySteps flags array-constructor steps as cons arrays, so each
// array they build stays one value in later steps. jsonata-js flags a path's
// first and last constructor steps, and since a.[b,c].d parses as
// (a.[b,c]).d, every constructor after the first was once a last step. A
// leading constructor in a longer path stays unflagged: jsonata-js evaluates
// it once and maps later steps over its items, which gnata does for a plain
// array.
func markUnaryArraySteps(steps []*Node) {
	for i, step := range steps {
		if (i > 0 || len(steps) == 1) && isUnaryArrayCtor(step) {
			step.ConsArray = true
		}
	}
}

func isUnaryArrayCtor(n *Node) bool {
	return n != nil && n.Type == NodeUnary && n.Value == "["
}

// processFunctionChildren recursively processes a function/partial node.
func processFunctionChildren(node *Node) (*Node, error) {
	var err error
	node.Procedure, err = ProcessAST(node.Procedure)
	if err != nil {
		return nil, err
	}
	for i, arg := range node.Arguments {
		node.Arguments[i], err = ProcessAST(arg)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

// processLambdaChildren recursively processes a lambda node.
func processLambdaChildren(node *Node) (*Node, error) {
	var err error
	node.Body, err = ProcessAST(node.Body)
	if err != nil {
		return nil, err
	}
	markTailCalls(node.Body)
	return node, nil
}

// processConditionChildren recursively processes a condition node.
func processConditionChildren(node *Node) (*Node, error) {
	var err error
	node.Condition, err = ProcessAST(node.Condition)
	if err != nil {
		return nil, err
	}
	node.Then, err = ProcessAST(node.Then)
	if err != nil {
		return nil, err
	}
	if node.Else != nil {
		node.Else, err = ProcessAST(node.Else)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

// processBindChildren recursively processes a bind node.
func processBindChildren(node *Node) (*Node, error) {
	var err error
	node.Left, err = ProcessAST(node.Left)
	if err != nil {
		return nil, err
	}
	node.Right, err = ProcessAST(node.Right)
	if err != nil {
		return nil, err
	}
	return node, nil
}

// processTransformChildren recursively processes a transform node.
func processTransformChildren(node *Node) (*Node, error) {
	var err error
	node.Pattern, err = ProcessAST(node.Pattern)
	if err != nil {
		return nil, err
	}
	node.Update, err = ProcessAST(node.Update)
	if err != nil {
		return nil, err
	}
	if node.Delete != nil {
		node.Delete, err = ProcessAST(node.Delete)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

// processSortChildren recursively processes a sort node.
func processSortChildren(node *Node) (*Node, error) {
	var err error
	node.Left, err = ProcessAST(node.Left)
	if err != nil {
		return nil, err
	}
	for i, term := range node.Terms {
		node.Terms[i].Expression, err = ProcessAST(term.Expression)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

// processPathChildren recursively processes an existing path node.
func processPathChildren(node *Node) (*Node, error) {
	var err error
	for i, step := range node.Steps {
		node.Steps[i], err = ProcessAST(step)
		if err != nil {
			return nil, err
		}
		if node.Steps[i].KeepArray {
			node.KeepSingletonArray = true
		}
	}
	markUnaryArraySteps(node.Steps)
	// Process group-by key/value pairs.
	if node.Group != nil {
		for i, pair := range node.Group.Pairs {
			node.Group.Pairs[i][0], err = ProcessAST(pair[0])
			if err != nil {
				return nil, err
			}
			node.Group.Pairs[i][1], err = ProcessAST(pair[1])
			if err != nil {
				return nil, err
			}
		}
	}
	return node, nil
}

// ParseAndProcess parses src and runs the post-parse passes, returning the
// tree the evaluator runs.
func ParseAndProcess(src string) (*Node, error) {
	ast, err := NewParser(src).Parse()
	if err != nil {
		return nil, err
	}
	if ast, err = ProcessAST(ast); err != nil {
		return nil, err
	}
	markRootContext(ast)
	return ast, nil
}

// markRootContext flags the wildcard steps that start a path evaluated
// against the expression's root input. jsonata-js wraps a root array input,
// so such a step sees the array as one item, while later steps and $ see its
// items. It runs once on the whole expression.
func markRootContext(node *Node) {
	if node == nil {
		return
	}
	switch node.Type {
	case NodePath:
		if len(node.Steps) > 0 {
			if first := node.Steps[0]; first.Type == NodeWildcard {
				first.RootContext = true
			} else {
				markRootContext(first)
			}
		}
	case NodeBlock:
		for _, expr := range node.Expressions {
			markRootContext(expr)
		}
	case NodeUnary:
		markRootContext(node.Expression)
		for _, expr := range node.Expressions {
			markRootContext(expr)
		}
		for _, expr := range node.LHS {
			markRootContext(expr)
		}
	case NodeBinary, NodeApply:
		markRootContext(node.Left)
		if node.Value != "[" {
			markRootContext(node.Right)
		}
	case NodeCondition:
		markRootContext(node.Condition)
		markRootContext(node.Then)
		markRootContext(node.Else)
	case NodeBind:
		markRootContext(node.Right)
	case NodeFunction, NodePartial:
		for _, arg := range node.Arguments {
			markRootContext(arg)
		}
	case NodeLambda:
		// A lambda body runs against the input where the lambda is defined.
		markRootContext(node.Body)
	case NodeSort:
		markRootContext(node.Left)
	}
}
