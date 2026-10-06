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
//   - Wraps a step that binds outside a path, as in a#$i or a sort whose
//     Left binds #$var, @$var or an ancestor (Tuple), with any subscripts,
//     in a one-step path (see wrapBoundStep).
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
	processed, err := processNode(node)
	if err != nil {
		return nil, err
	}
	return wrapBoundStep(processed), nil
}

// processNode is ProcessAST's pass for node's type, before the group and
// wrapping steps every node shares.
func processNode(node *Node) (*Node, error) {
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

// takePathGroup removes and returns node's group if it groups a whole
// path (see GroupExpr.OnPath).
func takePathGroup(node *Node) *GroupExpr {
	group := node.Group
	if group == nil || !group.OnPath {
		return nil
	}
	node.Group = nil
	return group
}

// joinGroups returns the one group of a and b, raising S0210 if both are
// set: a path or step has at most one group.
func joinGroups(a, b *GroupExpr) (*GroupExpr, error) {
	switch {
	case a == nil:
		return b, nil
	case b == nil:
		return a, nil
	}
	return nil, parseError("S0210", "{", "each step can only have one grouping expression")
}

// processDotBinary flattens a binary(".") node into a path node.
func processDotBinary(node *Node) (*Node, error) {
	// Collect all steps from nested dots.
	steps, group, err := collectPathSteps(node)
	if err != nil {
		return nil, err
	}
	for _, step := range steps {
		if base := StepBase(step); base.Type == NodeNumber || base.Type == NodeValue {
			return nil, parseError("S0213", base.Value, "a number, true, false or null cannot be a path step")
		}
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
		// If the processed node is itself a path, as a wrapped step is,
		// splice its steps.
		steps, group := []*Node{processed}, takePathGroup(processed)
		if processed.Type == NodePath {
			steps = processed.Steps
		}
		// A quoted step, or one with predicates, is a field name.
		for _, step := range steps {
			if base := StepBase(step); base.Type == NodeString {
				base.Type = NodeName
			}
		}
		return steps, group, nil
	}

	steps, group, err := collectPathSteps(node.Left)
	if err != nil {
		return nil, nil, err
	}
	rightSteps, rightGroup, err := collectPathSteps(node.Right)
	if err != nil {
		return nil, nil, err
	}
	if group, err = joinGroups(group, rightGroup); err != nil {
		return nil, nil, err
	}
	if group, err = joinGroups(group, node.Group); err != nil {
		return nil, nil, err
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
		// jsonata-js passes no slots on from either side of ~>, so a % there
		// stays unresolved.
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
	node.KeepSingletonArray = ChainKeepsArray(node.Left)
	node.base = StepBase(node.Left)
	if err := resolvePredicateAncestry(node); err != nil {
		return nil, err
	}
	if path != nil {
		if node.KeepArray {
			path.KeepSingletonArray = true
		}
		return path, nil
	}
	group, err := joinGroups(node.Group, takePathGroup(node.Left))
	node.Group = group
	return node, err
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
	node.KeepSingletonArray = ChainKeepsArray(node.Left)
	if err := resolveSortAncestry(node); err != nil {
		return nil, err
	}
	// A group on a path-like Left groups the sorted result, as in jsonata-js
	// the sort joins the path that carries it: a{k: v}^(c) sorts first.
	node.Group, err = joinGroups(node.Group, takePathGroup(node.Left))
	return node, err
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

// markRootContext flags the steps that start a path evaluated against the
// expression's root input. jsonata-js wraps a root array input, so such a
// step sees the array as one item, while later steps and $ see its items.
// It runs once on the whole expression. With root false, as under a path's
// first step, which sees the root array's items, it flags only wildcards.
func markRootContext(node *Node, root bool) {
	if node == nil {
		return
	}
	switch node.Type {
	case NodePath:
		if len(node.Steps) > 0 {
			first := node.Steps[0]
			if root {
				StepBase(first).RootContext = true
			}
			if first.Type == NodeWildcard {
				first.RootContext = true
			} else {
				markRootContext(first, false)
			}
		}
	case NodeBlock:
		for _, expr := range node.Expressions {
			markRootContext(expr, root)
		}
	case NodeUnary:
		markRootContext(node.Expression, root)
		for _, expr := range node.Expressions {
			markRootContext(expr, root)
		}
		for _, expr := range node.LHS {
			markRootContext(expr, root)
		}
	case NodeBinary, NodeApply:
		markRootContext(node.Left, root)
		if node.Value != "[" {
			markRootContext(node.Right, root)
		}
	case NodeCondition:
		markRootContext(node.Condition, root)
		markRootContext(node.Then, root)
		markRootContext(node.Else, root)
	case NodeBind:
		markRootContext(node.Right, root)
	case NodeFunction, NodePartial:
		for _, arg := range node.Arguments {
			markRootContext(arg, root)
		}
	case NodeLambda:
		// A lambda body runs against the input where the lambda is defined.
		markRootContext(node.Body, root)
	case NodeSort:
		markRootContext(node.Left, root)
	}
}

// wrapBoundStep wraps a step that only path evaluation can run, since it
// tracks a tuple stream, in a one-step path (see wrapStep): a step with a
// #$var, as jsonata-js makes any step a path for it, a name with an @$var,
// a sort whose Left binds #$var, @$var or an ancestor, as in a#$j^(b), or a
// subscripted name that binds an ancestor, as in a[%.b] outside a path. A
// subscript on a wrapped step, as in a#$j^(b)[0], filters that stream, so
// it replaces the step in the path; a chain [p1][p2] extends the same path,
// keeping each wrap O(1).
func wrapBoundStep(node *Node) *Node {
	switch {
	case node.Type == NodePath:
		return node
	case node.Type == NodeSort && node.Tuple:
		return wrapStep(node, pathSlots(node))
	case node.Type == NodeBinary && node.Value == "[" && IsStepPath(node.Left):
		seeking := node.Left.SeekingParent
		node.Left = node.Left.Steps[0]
		return wrapStep(node, seeking)
	case node.Type == NodeBinary && node.Value == "[" && len(predicateSlots(node.Right)) > 0 &&
		isPathLike(node) && StepBase(node).Ancestor != nil:
		return wrapStep(node, pathSlots(node))
	case node.Index != "" || node.Type == NodeName && node.Focus != "":
		return wrapStep(node, pathSlots(node))
	}
	return node
}

// wrapStep returns a one-step path running step, as jsonata-js makes any
// step it evaluates as a tuple stream a path, with the slots seeking past
// it. A group on the path moves to it.
func wrapStep(step *Node, seeking []*Slot) *Node {
	return &Node{
		Type: NodePath, Steps: []*Node{step}, Pos: step.Pos, Tuple: true, Group: takePathGroup(step),
		KeepSingletonArray: ChainKeepsArray(step), SeekingParent: seeking,
	}
}

// IsStepPath reports whether node is a path wrapStep made.
func IsStepPath(node *Node) bool {
	return node != nil && node.Type == NodePath && node.Tuple && len(node.Steps) == 1
}

// ChainKeepsArray reports whether node or a node along its Left chain has a
// [] suffix, or is a path that does. ProcessAST records the answer for a
// subscript's or sort's Left chain in its KeepSingletonArray, so a long
// chain such as a[0][0]... is not rescanned at every link.
func ChainKeepsArray(node *Node) bool {
	return node != nil && (node.KeepArray || node.KeepSingletonArray)
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
