package parser

import "slices"

// ProcessAST runs the post-processing pass over the raw Pratt-parsed tree,
// transforming it into a form suitable for evaluation.
//
// Current transformations:
//   - Flattens nested binary(".") nodes into path nodes with Steps slices.
//   - Propagates KeepSingletonArray when any step has KeepArray=true.
//   - Marks array-constructor steps as ConsArray (jsonata-js consarray).
//   - Attaches a group on a path, or on anything jsonata-js makes a path,
//     to the whole path: it applies after all its steps and sorts.
//   - Flags sorts whose Left binds #$var, @$var or an ancestor (Tuple) and
//     wraps them, with any subscripts, in a one-step path (see wrapBoundStep).
//   - Resolves each % to the step whose input it reads (see ancestry.go).
//   - Recursively processes all child nodes.
func ProcessAST(node *Node) (*Node, error) {
	if node == nil {
		return nil, nil
	}
	// collectPathSteps processes a path's groups, inner dots' included.
	if node.Group != nil && (node.Type != NodeBinary || node.Value != ".") {
		if err := processGroup(node.Group); err != nil {
			return nil, err
		}
	}

	switch node.Type {
	case NodeBinary:
		if node.Value == "." {
			return processDotBinary(node)
		}
		processed, err := processBinaryChildren(node)
		if err != nil {
			return nil, err
		}
		return wrapBoundStep(processed), nil

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
		processed, err := processSortChildren(node)
		if err != nil {
			return nil, err
		}
		return wrapBoundStep(processed), nil

	case NodePath:
		return processPathChildren(node)

	default:
		// Leaf nodes (name, string, number, value, variable, wildcard, descendant, parent, regex).
		return node, nil
	}
}

// processGroup processes a group's key and value expressions.
func processGroup(group *GroupExpr) error {
	for i, pair := range group.Pairs {
		for j, expr := range pair {
			processed, err := ProcessAST(expr)
			if err != nil {
				return err
			}
			group.Pairs[i][j] = processed
		}
	}
	return nil
}

// errTwoGroups is S0210, for a path or step given a second group.
func errTwoGroups() error {
	return parseError("S0210", "{", "each step can only have one grouping expression")
}

// processDotBinary flattens a binary(".") node into a path node.
func processDotBinary(node *Node) (*Node, error) {
	// Collect all steps from nested dots.
	steps, group, err := collectPathSteps(node)
	if err != nil {
		return nil, err
	}

	path := &Node{
		Type:  NodePath,
		Value: node.Value,
		Steps: steps,
		Pos:   node.Pos,
		Group: group,
	}
	markUnaryArraySteps(steps)
	markSubscriptStages(steps)
	if err := resolvePathAncestry(path); err != nil {
		return nil, err
	}

	// Propagate KeepSingletonArray when any step (or a subscript step's left side)
	// has KeepArray=true. This covers both A[].B and A[][filter].B patterns.
	// A[][filter] and a[]^(b) keep the flag on a node along the step's Left chain.
	if slices.ContainsFunc(steps, ChainKeepsArray) {
		path.KeepSingletonArray = true
	}
	return path, nil
}

// collectPathSteps recursively collects steps from binary(".") nodes, with
// the path's group: one on a dot, or on a leading step jsonata-js makes a
// path (as in a{k: v}.c), which then groups the whole path's result.
func collectPathSteps(node *Node) ([]*Node, *GroupExpr, error) {
	if node.Type != NodeBinary || node.Value != "." {
		// Leaf step — process it.
		processed, err := ProcessAST(node)
		if err != nil {
			return nil, nil, err
		}
		// If the processed node is itself a path, splice its steps.
		if processed.Type == NodePath {
			return processed.Steps, processed.Group, nil
		}
		promoteQuotedPathNames(processed)
		if processed.Type == NodeString {
			processed.Type = NodeName
		}
		var group *GroupExpr
		if processed.Group != nil && processed.Group.OnPath {
			group, processed.Group = processed.Group, nil
		}
		return []*Node{processed}, group, nil
	}

	steps, group, err := collectPathSteps(node.Left)
	if err != nil {
		return nil, nil, err
	}
	rightSteps, rightGroup, err := collectPathSteps(node.Right)
	if err != nil {
		return nil, nil, err
	}
	for _, g := range []*GroupExpr{rightGroup, node.Group} {
		if g == nil {
			continue
		}
		if group != nil {
			return nil, nil, errTwoGroups()
		}
		group = g
	}
	if node.Group != nil {
		if err := processGroup(node.Group); err != nil {
			return nil, nil, err
		}
	}

	// The # and @ infix operators set Index/Focus on the binary "." node
	// itself after a group, as in a.b{k: v}#$i. jsonata-js binds them on
	// the path's last step, a #$var after its predicates.
	steps = append(steps, rightSteps...)
	last := steps[len(steps)-1]
	if node.Index != "" {
		last.Index = node.Index
	}
	if node.Focus != "" {
		if last.Type == NodeBinary && last.Value == "[" {
			return nil, nil, parseError("S0215", "@", "the @ operator cannot follow a predicate expression")
		}
		last.Focus = node.Focus
	}
	return steps, group, nil
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
	switch node.Value {
	case "[":
		return processSubscript(node)
	case "~>":
	default:
		node.SeekingParent = concatSlots(node.Left, node.Right)
	}
	return node, nil
}

// processSubscript finishes a subscript whose operands are processed. A
// group on a path-like Left, as in a{k: v}[0], moves to the subscript, so it
// applies after it; on a path, as in a.b{k: v}[0], the subscript becomes a
// stage of the last step, as jsonata-js applies it there.
func processSubscript(node *Node) (*Node, error) {
	path := node.Left
	if path.Type != NodePath || IsStepPath(path) {
		path = nil
	} else {
		last := len(path.Steps) - 1
		node.Left = path.Steps[last]
		path.Steps[last] = node
		node.PathStage = last > 0
	}
	if err := resolvePredicateAncestry(node); err != nil {
		return nil, err
	}
	if path != nil {
		if node.KeepArray {
			path.KeepSingletonArray = true
		}
		return path, nil
	}
	if group := node.Left.Group; group != nil && group.OnPath {
		if node.Group != nil {
			return nil, errTwoGroups()
		}
		node.Group, node.Left.Group = group, nil
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
	node.SeekingParent = concatSlots(append(append([]*Node{node.Expression}, node.Expressions...), node.LHS...)...)
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
	node.SeekingParent = concatSlots(node.Expressions...)
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

// markSubscriptStages flags the subscripts of each path step after the
// first. jsonata-js applies them to the step's collapsed result, unlike a
// predicate on the first step or a call outside a path, which sees the
// call's whole sequence: a.$map([$], $keys)[0] picks the first key of each
// item.
func markSubscriptStages(steps []*Node) {
	for _, step := range steps[min(1, len(steps)):] {
		for n := step; n != nil && n.Type == NodeBinary && n.Value == "["; n = n.Left {
			n.PathStage = true
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
	node.SeekingParent = concatSlots(node.Arguments...)
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
	node.SeekingParent = concatSlots(node.Condition, node.Then, node.Else)
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
	node.SeekingParent = exprSlots(node.Right)
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
	node.NoBinds = !containsBind(node.Pattern) && !containsBind(node.Update) && !containsBind(node.Delete)
	return node, nil
}

// containsBind reports whether node or any node under it binds a variable.
func containsBind(node *Node) bool {
	if node == nil {
		return false
	}
	if node.Type == NodeBind {
		return true
	}
	if slices.ContainsFunc([]*Node{
		node.Left, node.Right, node.Expression, node.Condition, node.Then, node.Else,
		node.Procedure, node.Body, node.Pattern, node.Update, node.Delete,
	}, containsBind) {
		return true
	}
	for _, children := range [][]*Node{node.Steps, node.Expressions, node.LHS, node.Arguments} {
		if slices.ContainsFunc(children, containsBind) {
			return true
		}
	}
	if slices.ContainsFunc(node.Terms, func(t SortTerm) bool { return containsBind(t.Expression) }) ||
		slices.ContainsFunc(node.Stages, func(st Stage) bool { return containsBind(st.Expression) }) {
		return true
	}
	return node.Group != nil && slices.ContainsFunc(node.Group.Pairs, func(p [2]*Node) bool {
		return containsBind(p[0]) || containsBind(p[1])
	})
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
	if err := resolveSortAncestry(node); err != nil {
		return nil, err
	}
	// A group on a path-like Left groups the sorted result, as in jsonata-js
	// the sort joins the path that carries it: a{k: v}^(c) sorts first.
	if group := node.Left.Group; group != nil && group.OnPath {
		if node.Group != nil {
			return nil, errTwoGroups()
		}
		node.Group, node.Left.Group = group, nil
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
		if ChainKeepsArray(node.Steps[i]) {
			node.KeepSingletonArray = true
		}
	}
	markUnaryArraySteps(node.Steps)
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
	if ast.Type == NodeParent || len(exprSlots(ast)) > 0 {
		return nil, errNoParent(ast)
	}
	markRootContext(ast, true)
	return ast, nil
}

// markRootContext flags the wildcard and ancestor steps that start a path
// evaluated against the expression's root input. jsonata-js wraps a root
// array input, so such a step sees the array as one item, while later steps
// and $ see its items. It runs once on the whole expression. With
// ancestors false, as under a path's first step, which sees the root
// array's items, it flags only wildcards.
func markRootContext(node *Node, ancestors bool) {
	if node == nil {
		return
	}
	switch node.Type {
	case NodePath:
		if len(node.Steps) > 0 {
			first := node.Steps[0]
			if base := stepBase(first); ancestors && base.Ancestor != nil {
				base.RootContext = true
			}
			if first.Type == NodeWildcard {
				first.RootContext = true
			} else {
				markRootContext(first, false)
			}
		}
	case NodeBlock:
		for _, expr := range node.Expressions {
			markRootContext(expr, ancestors)
		}
	case NodeUnary:
		markRootContext(node.Expression, ancestors)
		for _, expr := range node.Expressions {
			markRootContext(expr, ancestors)
		}
		for _, expr := range node.LHS {
			markRootContext(expr, ancestors)
		}
	case NodeBinary, NodeApply:
		markRootContext(node.Left, ancestors)
		if node.Value != "[" {
			markRootContext(node.Right, ancestors)
		}
	case NodeCondition:
		markRootContext(node.Condition, ancestors)
		markRootContext(node.Then, ancestors)
		markRootContext(node.Else, ancestors)
	case NodeBind:
		markRootContext(node.Right, ancestors)
	case NodeFunction, NodePartial:
		for _, arg := range node.Arguments {
			markRootContext(arg, ancestors)
		}
	case NodeLambda:
		// A lambda body runs against the input where the lambda is defined.
		markRootContext(node.Body, ancestors)
	case NodeSort:
		markRootContext(node.Left, ancestors)
	}
}

// wrapBoundStep wraps a step that only path evaluation can run, since it
// tracks a tuple stream, in a one-step path (see wrapStep): a sort whose
// Left binds #$var, @$var or an ancestor, as in a#$j^(b), or a subscripted
// name that binds an ancestor, as in a[%.b] outside a path. A subscript on
// a wrapped step, as in a#$j^(b)[0], filters that stream, so it replaces the
// step in the path; a chain [p1][p2] extends the same path, keeping each
// wrap O(1).
func wrapBoundStep(node *Node) *Node {
	switch {
	case node.Type == NodeSort && node.Tuple:
		return wrapStep(node)
	case node.Type == NodeBinary && node.Value == "[" && IsStepPath(node.Left):
		keep := node.KeepArray || node.Left.KeepSingletonArray
		node.Left = node.Left.Steps[0]
		path := wrapStep(node)
		path.KeepSingletonArray = keep
		return path
	case node.Type == NodeBinary && node.Value == "[" && len(predicateSlots(node.Right)) > 0 &&
		isPathLike(node) && stepBase(node).Ancestor != nil:
		return wrapStep(node)
	}
	return node
}

// wrapStep returns a one-step path running step, as jsonata-js makes any
// step it evaluates as a tuple stream a path. A group on the path moves to
// it.
func wrapStep(step *Node) *Node {
	path := &Node{
		Type: NodePath, Steps: []*Node{step}, Pos: step.Pos, Tuple: true,
		KeepSingletonArray: ChainKeepsArray(step), SeekingParent: pathSlots(step),
	}
	if step.Group != nil && step.Group.OnPath {
		path.Group, step.Group = step.Group, nil
	}
	return path
}

// IsStepPath reports whether node is a path wrapStep made.
func IsStepPath(node *Node) bool {
	return node != nil && node.Type == NodePath && node.Tuple && len(node.Steps) == 1
}

// ChainKeepsArray reports whether node or a node along its Left chain has a
// [] suffix, or is a path that does.
func ChainKeepsArray(node *Node) bool {
	for ; node != nil; node = node.Left {
		if node.KeepArray || node.Type == NodePath && node.KeepSingletonArray {
			return true
		}
	}
	return false
}

// hasBinding reports whether node or any node in its Left, Right or Steps
// subtrees has a #$var, @$var or ancestor binding. A processed sort or
// wrapStep path answers from its Tuple flag, so a chain of sorts is checked
// in linear time.
func hasBinding(node *Node) bool {
	if node == nil {
		return false
	}
	if node.Index != "" || node.Focus != "" || node.Tuple || node.Ancestor != nil || node.TupleResult {
		return true
	}
	if node.Type == NodeSort {
		return false
	}
	return hasBinding(node.Left) || hasBinding(node.Right) || slices.ContainsFunc(node.Steps, hasBinding)
}
