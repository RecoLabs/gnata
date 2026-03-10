package evaluator

import (
	"slices"

	"github.com/recolabs/gnata/internal/parser"
)

// pathCtx pairs a value with the environment it was produced under.
// Used only for tuple-aware path evaluation (paths containing #$var steps).
type pathCtx struct {
	value any
	env   *Environment
}

// evalPath evaluates a path node by threading each step's result into the next.
// When any step carries a #$var index binding, it switches to tuple-aware
// evaluation so the position variable remains visible in subsequent steps.
func evalPath(node *parser.Node, input any, env *Environment) (any, error) {
	if pathHasTupleStep(node.Steps) {
		return evalPathTuple(node, input, env)
	}
	return evalPathSimple(node, input, env)
}

// pathHasTupleStep returns true when at least one path step requires tuple
// tracking: either the step itself carries a #$var Index, a subscript whose
// left-hand node has an Index, or any step/sub-expression references NodeParent (%).
func pathHasTupleStep(steps []*parser.Node) bool {
	for _, step := range steps {
		if step.Index != "" || step.Focus != "" {
			return true
		}
		// A subscript step whose left child has an Index or Focus binding also requires
		// tuple-aware path evaluation so each element gets its own env for $pos/$var.
		if step.Type == parser.NodeBinary && step.Value == "[" &&
			step.Left != nil && (step.Left.Index != "" || step.Left.Focus != "") {
			return true
		}
		// A sort step whose Left is a path containing #$var bindings needs tuple
		// mode so the index variables survive through the sort into subsequent steps.
		if step.Type == parser.NodeSort && step.Left != nil {
			if nodeHasIndexBinding(step.Left) {
				return true
			}
		}
		// Any step that references % (NodeParent) requires parent-chain tracking.
		if nodeHasParentRef(step) {
			return true
		}
	}
	return false
}

// nodeHasIndexBinding recursively checks whether a node or any of its
// descendants has a non-empty Index or Focus field (#$var or @$var binding).
func nodeHasIndexBinding(node *parser.Node) bool {
	if node == nil {
		return false
	}
	if node.Index != "" || node.Focus != "" {
		return true
	}
	if nodeHasIndexBinding(node.Left) || nodeHasIndexBinding(node.Right) {
		return true
	}
	return slices.ContainsFunc(node.Steps, nodeHasIndexBinding)
}

// groupHasParentRef returns true if any expression in a GroupExpr contains
// a NodeParent (%) reference. This is used to decide whether a path with a
// trailing group expression (A.B.C{...}) needs tuple-aware evaluation.
func groupHasParentRef(grp *parser.GroupExpr) bool {
	if grp == nil {
		return false
	}
	for _, pair := range grp.Pairs {
		if nodeHasParentRef(pair[0]) || nodeHasParentRef(pair[1]) {
			return true
		}
	}
	return false
}

// nodeHasParentRef recursively checks whether an AST node or any of its
// descendants is a NodeParent (%).
func nodeHasParentRef(node *parser.Node) bool {
	return node != nil &&
		(node.Type == parser.NodeParent ||
			nodeHasParentRef(node.Left) || nodeHasParentRef(node.Right) ||
			// Ternary condition/then/else branches may contain % references.
			nodeHasParentRef(node.Condition) || nodeHasParentRef(node.Then) || nodeHasParentRef(node.Else) ||
			slices.ContainsFunc(node.Steps, nodeHasParentRef) ||
			slices.ContainsFunc(node.Expressions, nodeHasParentRef) ||
			slices.ContainsFunc(node.LHS, nodeHasParentRef) ||
			// Sort terms carry their own expression subtrees that may reference %.
			slices.ContainsFunc(node.Terms, func(t parser.SortTerm) bool { return nodeHasParentRef(t.Expression) }) ||
			// Group key/value pairs.
			(node.Group != nil && slices.ContainsFunc(node.Group.Pairs, func(p [2]*parser.Node) bool {
				return nodeHasParentRef(p[0]) || nodeHasParentRef(p[1])
			})))
}

// evalPathSimple is the original step-by-step path evaluation (no index bindings).
func evalPathSimple(node *parser.Node, input any, env *Environment) (any, error) {
	result := input
	prevWasMapper := false
	for i, step := range node.Steps {
		if i > 0 && result == nil {
			return nil, nil
		}
		if seq, ok := result.(*Sequence); ok {
			result = CollapseSequence(seq)
			if i > 0 && result == nil {
				return nil, nil
			}
		}

		// NodeParent (%) requires special handling: look up the parent
		// via the env chain and advance env to the binding env's parent
		// so that chained %.% navigates upward correctly.
		if step.Type == parser.NodeParent {
			parentVal, bindingEnv, ok := env.LookupWithEnv(parentKey)
			if !ok || parentVal == nil {
				return nil, &JSONataError{Code: "S0217", Message: "% operator used outside of a valid path context"}
			}
			result = parentVal
			if pe := bindingEnv.Parent(); pe != nil {
				env = pe
			}
			if _, isJoin := bindingEnv.LookupDirect(parentJoinFlag); isJoin {
				for env != nil {
					if _, joinToo := env.LookupDirect(parentJoinFlag); !joinToo {
						break
					}
					pv, pe, has := env.LookupWithEnv(parentKey)
					if has && pv == parentVal {
						if next := pe.Parent(); next != nil {
							env = next
						} else {
							break
						}
					} else {
						break
					}
				}
			}
			continue
		}

		var err error
		result, err = evalPathStep(step, result, env, prevWasMapper, node.KeepSingletonArray)
		if err != nil {
			return nil, err
		}
		// Empty arrays produced by auto-mapping or variable lookup in a
		// mapping context mean "nothing found" → undefined. However, when
		// the previous step was a direct access (not a mapper), the empty
		// array is a genuine field value (e.g. obj.emptyList) and must be
		// preserved so that $exists sees it as defined.
		// For field-lookup steps (NodeName/NodeString), evalName already
		// distinguishes "nothing found" (returns nil) from "field exists
		// with empty array value" (returns []any{}), so we skip the
		// blanket nil-ification for those step types.
		if arr, ok := result.([]any); ok && len(arr) == 0 && prevWasMapper {
			if step.Type != parser.NodeName && step.Type != parser.NodeString {
				return nil, nil
			}
		}
		_, isArr := result.([]any)
		_, isSeq := result.(*Sequence)
		prevWasMapper = isArr || isSeq
	}

	if node.KeepSingletonArray {
		switch v := result.(type) {
		case []any:
			return v, nil
		default:
			if result != nil {
				return []any{result}, nil
			}
		}
	}
	return result, nil
}

// evalPathTuple evaluates a path that contains one or more #$var index bindings.
//
// It maintains a list of (value, env) contexts so that position variables bound
// at one step remain accessible in all subsequent steps. Crucially, the index
// variable for a step is bound to the POSITION IN THE OUTPUT array (not the
// position of the input context), matching JSONata's tuple-stream semantics.
//
// Step-level Group expressions (e.g. Product{key:val}) are stripped from the
// step and applied at the end via evalTupleGroup, so that per-element envs
// (containing $o, $i, …) are used during key/val evaluation.
func evalPathTuple(node *parser.Node, input any, env *Environment) (any, error) { //nolint:gocyclo,funlen // dispatch
	ctxs := []pathCtx{{value: input, env: env}}

	// finalGroup accumulates the first step-level Group expression encountered.
	// Step-level groups are applied after all contexts have been collected.
	var finalGroup *parser.GroupExpr

	for stepIdx, step := range node.Steps {
		var nextCtxs []pathCtx

		// If this step has an inline Group (e.g. Product{key:val}), strip it so
		// that evalPathStep evaluates the base node without group-by reduction.
		// The group will be applied at the end via evalTupleGroup.
		evalStep := step
		if step.Group != nil && finalGroup == nil {
			finalGroup = step.Group
			cp := *step
			cp.Group = nil
			evalStep = &cp
		}

		// Sort steps must be applied globally to ALL tuples simultaneously so that
		// tuple ordering is preserved (e.g. Account.Order#$o.Product^(ProductID)).
		// We sort the ctxs themselves by evaluating the sort key on each tuple's value.
		if step.Type == parser.NodeSort {
			sorted, err := evalTupleSort(step, ctxs)
			if err != nil {
				return nil, err
			}
			ctxs = sorted
			continue
		}

		// NodeParent (%) steps navigate up the parent chain using the env bindings
		// set by appendTupleResults. The parent value is retrieved via %%, and the
		// new env is the immediate parent of the current env (so that chained %.%
		// can continue walking up the env chain).
		if evalStep.Type == parser.NodeParent {
			for _, ctx := range ctxs {
				// Use LookupWithEnv so we know which env directly holds %%,
				// and can use THAT env's parent as the new env. This matters
				// when evalBlock (or other scope creators) add intermediate
				// environments: childEnv.Parent() = env3a, but env3a still
				// has %% = Product. We need env3a.Parent() = env2a instead.
				parentVal, bindingEnv, ok := ctx.env.LookupWithEnv(parentKey)
				if !ok || parentVal == nil {
					return nil, &JSONataError{Code: "S0217", Message: "% operator used outside of a valid path context"}
				}
				parentEnv := bindingEnv.Parent()
				// When the current binding was made by a join step, skip
				// through any ancestor envs that are ALSO join bindings with
				// the same parent value. Join steps don't change the context
				// level, so consecutive join bindings represent the same
				// navigation depth. Regular steps always count as a level.
				if _, isJoin := bindingEnv.LookupDirect(parentJoinFlag); isJoin {
					for parentEnv != nil {
						if _, joinToo := parentEnv.LookupDirect(parentJoinFlag); !joinToo {
							break
						}
						pv, pe, has := parentEnv.LookupWithEnv(parentKey)
						if has && pv == parentVal {
							parentEnv = pe.Parent()
						} else {
							break
						}
					}
				}
				if parentEnv == nil {
					parentEnv = bindingEnv
				}
				nextCtxs = append(nextCtxs, pathCtx{value: parentVal, env: parentEnv})
			}
			ctxs = nextCtxs
			if len(ctxs) == 0 {
				return nil, nil
			}
			continue
		}

		// Subscript steps whose LEFT is NodeParent (%[predicate]) require special
		// handling: navigate % to the parent first, then apply the predicate using
		// the parent's own env (so that nested % inside the predicate refers to
		// the grandparent correctly).
		if evalStep.Type == parser.NodeBinary && evalStep.Value == "[" &&
			evalStep.Left != nil && evalStep.Left.Type == parser.NodeParent {
			predicate := evalStep.Right
			for _, ctx := range ctxs {
				parentVal, bindingEnv, ok := ctx.env.LookupWithEnv(parentKey)
				if !ok {
					return nil, &JSONataError{Code: "S0217", Message: "% operator used outside of a valid path context"}
				}
				parentEnv := bindingEnv.Parent()
				if parentEnv == nil {
					parentEnv = bindingEnv
				}
				predResult, err := Eval(predicate, parentVal, parentEnv)
				if err != nil {
					return nil, err
				}
				if ToBoolean(predResult) {
					nextCtxs = append(nextCtxs, pathCtx{value: parentVal, env: parentEnv})
				}
			}
			ctxs = nextCtxs
			if len(ctxs) == 0 {
				return nil, nil
			}
			continue
		}

		// Subscript whose Left is a Block containing a path expression, and
		// whose Right (predicate) references %. The block would normally
		// discard per-element parent context, so we evaluate the block's
		// inner path in tuple mode to preserve parent bindings, then apply
		// the predicate per-tuple.
		// Example: (Account.Order.Product)[%.OrderID='order104'].SKU
		if evalStep.Type == parser.NodeBinary && evalStep.Value == "[" &&
			evalStep.Left != nil && evalStep.Left.Type == parser.NodeBlock &&
			nodeHasParentRef(evalStep.Right) {
			predicate := evalStep.Right
			for _, ctx := range ctxs {
				var tupleCtxs []pathCtx
				block := evalStep.Left
				if len(block.Expressions) == 1 && block.Expressions[0].Type == parser.NodePath {
					var err error
					tupleCtxs, err = expandPathTuple(block.Expressions[0].Steps, []pathCtx{ctx})
					if err != nil {
						return nil, err
					}
				} else {
					blockResult, err := Eval(block, ctx.value, ctx.env)
					if err != nil {
						return nil, err
					}
					if blockResult == nil {
						continue
					}
					switch rv := blockResult.(type) {
					case []any:
						for _, item := range rv {
							nextCtxs = append(nextCtxs, pathCtx{value: item, env: ctx.env})
						}
					default:
						nextCtxs = append(nextCtxs, pathCtx{value: blockResult, env: ctx.env})
					}
					tupleCtxs = nextCtxs
					nextCtxs = nil
				}
				for _, tctx := range tupleCtxs {
					predResult, err := Eval(predicate, tctx.value, tctx.env)
					if err != nil {
						return nil, err
					}
					if ToBoolean(predResult) {
						nextCtxs = append(nextCtxs, tctx)
					}
				}
			}
			ctxs = nextCtxs
			if len(ctxs) == 0 {
				return nil, nil
			}
			continue
		}

		// When the step is a Block containing a single Path expression,
		// expand the inner path in tuple mode to preserve parent bindings
		// for the % operator (e.g., Account.(Order.Product).{%.OrderID}).
		if evalStep.Type == parser.NodeBlock &&
			len(evalStep.Expressions) == 1 &&
			evalStep.Expressions[0].Type == parser.NodePath {
			var err error
			nextCtxs, err = expandPathTuple(evalStep.Expressions[0].Steps, ctxs)
			if err != nil {
				return nil, err
			}
			ctxs = nextCtxs
			if len(ctxs) == 0 {
				return nil, nil
			}
			continue
		}

		// Subscript step whose Left has a Focus binding (join operator @):
		// e.g., Contact@$c[$c.ssn = $e.SSN]. We evaluate the Left to get
		// elements, bind each to $focus_var, then apply the predicate with
		// access to both the focus variable and previously bound variables.
		if evalStep.Type == parser.NodeBinary && evalStep.Value == "[" &&
			evalStep.Left != nil && evalStep.Left.Focus != "" {
			predicate := evalStep.Right
			leftNode := evalStep.Left
			focusVar := leftNode.Focus
			indexVar := leftNode.Index
			postFilterIndex := evalStep.Index
			var err error
			if nextCtxs, err = evalJoinFilter(ctxs, nextCtxs, leftNode, predicate, focusVar, indexVar); err != nil {
				return nil, err
			}
			if postFilterIndex != "" {
				for k := range nextCtxs {
					nextCtxs[k].env.Bind(postFilterIndex, float64(k))
				}
			}
			ctxs = nextCtxs
			if len(ctxs) == 0 {
				return nil, nil
			}
			continue
		}

		// Compound subscript after a join-filter: binary "[" whose Left is a
		// binary "[" with Left.Focus set. E.g., books@$b[pred][1] or
		// books@$b[pred][]. Process the inner join-filter first to collect
		// tuples, then apply the outer subscript to the entire tuple collection.
		if evalStep.Type == parser.NodeBinary && evalStep.Value == "[" &&
			evalStep.Left != nil && evalStep.Left.Type == parser.NodeBinary && evalStep.Left.Value == "[" &&
			evalStep.Left.Left != nil && evalStep.Left.Left.Focus != "" {
			// Process the inner join-filter as if it were a standalone step.
			innerStep := evalStep.Left
			predicate := innerStep.Right
			leftNode := innerStep.Left
			focusVar := leftNode.Focus
			indexVar := leftNode.Index
			var err error
			if nextCtxs, err = evalJoinFilter(ctxs, nextCtxs, leftNode, predicate, focusVar, indexVar); err != nil {
				return nil, err
			}

			// Apply the outer subscript to the collected tuples.
			outerExpr := evalStep.Right
			if len(nextCtxs) > 0 {
				outerResult, err := Eval(outerExpr, nextCtxs[0].value, nextCtxs[0].env)
				if err != nil {
					return nil, err
				}
				if idx, ok := outerResult.(float64); ok {
					i := int(idx)
					if i < 0 {
						i = len(nextCtxs) + i
					}
					if i >= 0 && i < len(nextCtxs) {
						nextCtxs = []pathCtx{nextCtxs[i]}
					} else {
						nextCtxs = nil
					}
				}
			}

			ctxs = nextCtxs
			if len(ctxs) == 0 {
				return nil, nil
			}
			continue
		}

		for _, ctx := range ctxs {
			if ctx.value == nil && stepIdx > 0 {
				continue
			}

			// Unwrap sequences.
			val := ctx.value
			if seq, ok := val.(*Sequence); ok {
				val = CollapseSequence(seq)
			}
			if val == nil && stepIdx > 0 {
				continue
			}

			result, err := evalPathStep(evalStep, val, ctx.env, false, node.KeepSingletonArray)
			if err != nil {
				return nil, err
			}
			if result == nil {
				continue
			}

			// Flatten the result into individual (value, env) contexts,
			// binding the step's #$var index to the OUTPUT element position j,
			// and binding the current context value as the parent (%%).
			// Skip parent binding for step 0 when it is $ or $$ (root references
			// don't have a parent context).
			skipParent := stepIdx == 0 &&
				evalStep.Type == parser.NodeVariable &&
				(evalStep.Value == "" || evalStep.Value == "$")
			if skipParent {
				appendTupleResultsNoParent(evalStep, result, ctx.env, &nextCtxs)
			} else {
				appendTupleResults(evalStep, result, ctx.value, ctx.env, &nextCtxs)
			}
		}

		ctxs = nextCtxs
		if len(ctxs) == 0 {
			return nil, nil
		}
	}

	// Determine which group expression to apply (step-level or path-level).
	grp := finalGroup
	if node.Group != nil {
		grp = node.Group
	}
	if grp != nil {
		return evalTupleGroup(grp, ctxs)
	}

	// Collect final values.
	seq := CreateSequence()
	for _, ctx := range ctxs {
		appendToSequence(seq, ctx.value)
	}
	result := CollapseSequence(seq)

	if node.KeepSingletonArray {
		switch v := result.(type) {
		case []any:
			return v, nil
		default:
			if result != nil {
				return []any{result}, nil
			}
		}
	}
	return result, nil
}

// evalSortWithParentTracking handles a top-level NodeSort expression whose sort
// terms reference the % (parent) operator. It evaluates the sort's Left expression
// in tuple mode so that each item retains its parent environment, then sorts them
// using per-item envs (enabling % to reference the parent during comparison).
//
// This handles expressions like: Account.Order.Product.SKU^(%.Price)
// where %. refers to the Product that owns each SKU.
func evalSortWithParentTracking(node *parser.Node, input any, env *Environment) (any, error) {
	if node.Left == nil {
		return nil, nil
	}

	ctxs, err := buildSortCtxs(node.Left, input, env)
	if err != nil {
		return nil, err
	}
	if len(ctxs) == 0 {
		return nil, nil
	}

	sorted := slices.Clone(ctxs)
	if err := SortItemsErr(sorted, func(a, b pathCtx) (int, error) {
		return compareSortTerms(node.Terms, a.value, b.value, a.env, b.env)
	}); err != nil {
		return nil, err
	}

	seq := CreateSequence()
	for _, ctx := range sorted {
		seq.Values = append(seq.Values, ctx.value)
	}
	return CollapseSequence(seq), nil
}

// buildSortCtxs builds the pathCtx slice for a sort expression's left-hand side.
// For path expressions, it splits into prefix + lastStep so parent bindings are preserved.
func buildSortCtxs(left *parser.Node, input any, env *Environment) ([]pathCtx, error) {
	if left.Type != parser.NodePath {
		result, err := Eval(left, input, env)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, nil
		}
		if rv, ok := result.([]any); ok {
			ctxs := make([]pathCtx, len(rv))
			for i, item := range rv {
				ctxs[i] = pathCtx{value: item, env: env}
			}
			return ctxs, nil
		}
		return []pathCtx{{value: result, env: env}}, nil
	}

	steps := left.Steps
	if len(steps) == 0 {
		return nil, nil
	}

	// Pass false for keepSingleton: the original code used a synthetic prefix node
	// whose KeepSingletonArray was always the zero value. Sort prefix walking should
	// not preserve singleton arrays even if the full path has [].
	prefixCtxs, err := walkPrefixSteps(steps[:len(steps)-1], input, env, false)
	if err != nil {
		return nil, err
	}
	return expandLastStep(steps[len(steps)-1], prefixCtxs)
}

// walkPrefixSteps evaluates prefix path steps in tuple mode, returning intermediate contexts.
func walkPrefixSteps(steps []*parser.Node, input any, env *Environment, keepSingleton bool) ([]pathCtx, error) {
	ctxs := []pathCtx{{value: input, env: env}}
	for _, step := range steps {
		var next []pathCtx
		for _, ctx := range ctxs {
			val := ctx.value
			if seq, ok := val.(*Sequence); ok {
				val = CollapseSequence(seq)
			}
			if val == nil {
				continue
			}
			result, err := evalPathStep(step, val, ctx.env, false, keepSingleton)
			if err != nil {
				return nil, err
			}
			if result != nil {
				appendTupleResults(step, result, ctx.value, ctx.env, &next)
			}
		}
		if ctxs = next; len(ctxs) == 0 {
			return nil, nil
		}
	}
	return ctxs, nil
}

// expandLastStep expands prefix contexts via the final step with parent tracking.
func expandLastStep(lastStep *parser.Node, prefixCtxs []pathCtx) ([]pathCtx, error) {
	var ctxs []pathCtx
	for _, ctx := range prefixCtxs {
		val := ctx.value
		if seq, ok := val.(*Sequence); ok {
			val = CollapseSequence(seq)
		}
		if val == nil {
			continue
		}
		result, err := evalPathStep(lastStep, val, ctx.env, false, false)
		if err != nil {
			return nil, err
		}
		if result != nil {
			appendTupleResults(lastStep, result, ctx.value, ctx.env, &ctxs)
		}
	}
	return ctxs, nil
}

// evalTupleSort applies a sort step to a slice of pathCtx in tuple-stream mode.
//
// The sort step may have a Left navigation node (e.g. Product^(ProductID) — where
// Left=NodeName "Product" means "navigate to Product first, THEN sort"). If so:
//  1. For each existing ctx, evaluate Left to get child items.
//  2. Collect all (child, parentEnv) tuples across all contexts.
//  3. Sort all collected tuples globally by the sort terms.
//
// If there is no Left navigation (bare sort e.g. ^($)), sort the existing ctxs
// directly by their own values.
func evalTupleSort(step *parser.Node, ctxs []pathCtx) ([]pathCtx, error) {
	// Determine if we need to navigate via step.Left before sorting.
	needsNavigation := step.Left != nil &&
		step.Left.Type != parser.NodeVariable // bare NodeVar "" means sort-in-place

	if needsNavigation {
		// Expand: navigate via step.Left for each ctx, collect all (child, childEnv) tuples.
		// When step.Left is a multi-step path (possibly with #$var bindings), we must
		// use a mini tuple walk so that index variables survive into the sorted output.
		// A plain Eval would discard the per-element environments.
		var expanded []pathCtx
		if step.Left.Type == parser.NodePath && len(step.Left.Steps) > 0 {
			var err error
			expanded, err = expandPathTuple(step.Left.Steps, ctxs)
			if err != nil {
				return nil, err
			}
		} else {
			for _, ctx := range ctxs {
				result, err := Eval(step.Left, ctx.value, ctx.env)
				if err != nil {
					return nil, err
				}
				if result == nil {
					continue
				}
				appendTupleResults(step.Left, result, ctx.value, ctx.env, &expanded)
			}
		}
		ctxs = expanded
	}

	if len(step.Terms) == 0 {
		return ctxs, nil
	}

	sorted := slices.Clone(ctxs)

	if err := SortItemsErr(sorted, func(a, b pathCtx) (int, error) {
		return compareSortTerms(step.Terms, a.value, b.value, a.env, b.env)
	}); err != nil {
		return nil, err
	}
	return sorted, nil
}

// parentKey is the internal environment key used to store the parent context
// for the % (NodeParent) operator. It uses a character that cannot appear in
// a JSONata identifier so it never collides with user-defined variables.
const (
	parentKey      = "%%"
	parentJoinFlag = "%%j"
)

// evalJoinFilter evaluates a join-filter step: it walks ctxs, evaluates
// leftNode against each context value, resolves the result into individual
// items, binds focusVar (and optionally indexVar) in a child environment,
// then keeps only contexts whose predicate evaluates to true.
// Matching contexts are appended to dst and the updated slice is returned.
func evalJoinFilter(ctxs, dst []pathCtx, leftNode, predicate *parser.Node, focusVar, indexVar string) ([]pathCtx, error) {
	for _, ctx := range ctxs {
		val := ctx.value
		if seq, ok := val.(*Sequence); ok {
			val = CollapseSequence(seq)
		}
		if val == nil {
			continue
		}
		leftResult, err := evalPathStep(leftNode, val, ctx.env, false, false)
		if err != nil {
			return nil, err
		}
		if leftResult == nil {
			continue
		}
		var items []any
		switch rv := leftResult.(type) {
		case []any:
			items = rv
		case *Sequence:
			c := CollapseSequence(rv)
			if arr, ok := c.([]any); ok {
				items = arr
			} else if c != nil {
				items = []any{c}
			}
		default:
			items = []any{leftResult}
		}
		for j, item := range items {
			childEnv := NewChildEnvironment(ctx.env)
			childEnv.Bind(parentKey, ctx.value)
			childEnv.Bind(parentJoinFlag, true)
			childEnv.Bind(focusVar, item)
			if indexVar != "" {
				childEnv.Bind(indexVar, float64(j))
			}
			predResult, err := Eval(predicate, item, childEnv)
			if err != nil {
				return nil, err
			}
			if ToBoolean(predResult) {
				dst = append(dst, pathCtx{value: ctx.value, env: childEnv})
			}
		}
	}
	return dst, nil
}

// appendTupleResults flattens a step's result into individual (value, env) contexts.
// It binds step.Index to the OUTPUT element position j (for #$var bindings),
// step.Focus to each element (for @$var join bindings), and always binds the
// current context value as the parent (%%key) for the % operator.
//
// When step.Focus is set (join operator @), the context VALUE stays at the parent
// level rather than advancing to the result element. This implements lateral-join
// semantics: the variable captures each element, but subsequent path steps continue
// navigating from the parent context.
func appendTupleResults(step *parser.Node, result, parentValue any, parentEnv *Environment, nextCtxs *[]pathCtx) {
	isJoin := step.Focus != ""

	bindAt := func(j int, elem any) *Environment {
		e := NewChildEnvironment(parentEnv)
		e.Bind(parentKey, parentValue)
		if isJoin {
			e.Bind(parentJoinFlag, true)
		}
		if step.Index != "" {
			e.Bind(step.Index, float64(j))
		}
		if step.Focus != "" {
			e.Bind(step.Focus, elem)
		}
		return e
	}

	ctxValue := func(elem any) any {
		if isJoin {
			return parentValue
		}
		return elem
	}

	switch rv := result.(type) {
	case []any:
		for j, elem := range rv {
			*nextCtxs = append(*nextCtxs, pathCtx{value: ctxValue(elem), env: bindAt(j, elem)})
		}
	case *Sequence:
		collapsed := CollapseSequence(rv)
		if collapsed == nil {
			return
		}
		if arr, ok := collapsed.([]any); ok {
			for j, elem := range arr {
				*nextCtxs = append(*nextCtxs, pathCtx{value: ctxValue(elem), env: bindAt(j, elem)})
			}
		} else {
			*nextCtxs = append(*nextCtxs, pathCtx{value: ctxValue(collapsed), env: bindAt(0, collapsed)})
		}
	default:
		*nextCtxs = append(*nextCtxs, pathCtx{value: ctxValue(result), env: bindAt(0, result)})
	}
}

// appendTupleResultsNoParent is like appendTupleResults but does not bind
// parentKey. Used for root-level steps ($ / $$) that have no path parent.
func appendTupleResultsNoParent(step *parser.Node, result any, parentEnv *Environment, nextCtxs *[]pathCtx) {
	bindAt := func(j int, _ any) *Environment {
		e := NewChildEnvironment(parentEnv)
		if step.Index != "" {
			e.Bind(step.Index, float64(j))
		}
		return e
	}
	switch rv := result.(type) {
	case []any:
		for j, elem := range rv {
			*nextCtxs = append(*nextCtxs, pathCtx{value: elem, env: bindAt(j, elem)})
		}
	case *Sequence:
		collapsed := CollapseSequence(rv)
		if collapsed == nil {
			return
		}
		if arr, ok := collapsed.([]any); ok {
			for j, elem := range arr {
				*nextCtxs = append(*nextCtxs, pathCtx{value: elem, env: bindAt(j, elem)})
			}
		} else {
			*nextCtxs = append(*nextCtxs, pathCtx{value: collapsed, env: bindAt(0, collapsed)})
		}
	default:
		*nextCtxs = append(*nextCtxs, pathCtx{value: result, env: bindAt(0, result)})
	}
}

// expandPathTuple runs a mini tuple walk over the given path steps, starting
// from the given ctxs. It returns the resulting (value, env) pairs, preserving
// #$var bindings and parent context. This is factored out so that evalTupleSort
// can evaluate Sort.Left in tuple mode rather than calling Eval (which would
// discard per-element environments).
func expandPathTuple(steps []*parser.Node, ctxs []pathCtx) ([]pathCtx, error) {
	for _, step := range steps {
		var next []pathCtx
		for _, ctx := range ctxs {
			val := ctx.value
			if seq, ok := val.(*Sequence); ok {
				val = CollapseSequence(seq)
			}
			if val == nil {
				continue
			}
			result, err := evalPathStep(step, val, ctx.env, false, false)
			if err != nil {
				return nil, err
			}
			if result == nil {
				continue
			}
			appendTupleResults(step, result, ctx.value, ctx.env, &next)
		}
		ctxs = next
		if len(ctxs) == 0 {
			return nil, nil
		}
	}
	return ctxs, nil
}

// evalTupleGroup evaluates a group expression against a tuple context list.
//
// JSONata group-by semantics: records are grouped by key, then the value
// expression is evaluated once per group with the context set to the array
// of all group members (or a single value when the group has one member).
// This allows aggregate functions like $join or $sum to operate on the
// full group rather than individual records.
func evalTupleGroup(group *parser.GroupExpr, ctxs []pathCtx) (any, error) {
	result := NewOrderedMap()

	for _, pair := range group.Pairs {
		// Phase 1: group ctxs by key.
		type groupEntry struct {
			values []any
			envs   []*Environment
		}
		var keyOrder []string
		groups := map[string]*groupEntry{}

		for _, ctx := range ctxs {
			keyVal, err := Eval(pair[0], ctx.value, ctx.env)
			if err != nil {
				return nil, err
			}
			key, ok := keyVal.(string)
			if !ok {
				return nil, &JSONataError{Code: "T1003", Message: "key expression must evaluate to a string"}
			}
			g, exists := groups[key]
			if !exists {
				g = &groupEntry{}
				groups[key] = g
				keyOrder = append(keyOrder, key)
			}
			g.values = append(g.values, ctx.value)
			g.envs = append(g.envs, ctx.env)
		}

		// Phase 2: evaluate value expression per group.
		for _, key := range keyOrder {
			g := groups[key]
			var groupCtx any
			var groupEnv *Environment
			if len(g.values) == 1 {
				groupCtx = g.values[0]
				groupEnv = g.envs[0]
			} else {
				groupCtx = g.values
				groupEnv = mergeGroupEnvs(g.envs)
			}
			val, err := Eval(pair[1], groupCtx, groupEnv)
			if err != nil {
				return nil, err
			}
			if val == nil {
				continue
			}
			result.Set(key, val)
		}
	}

	if result.Len() == 0 {
		return nil, nil
	}
	return result, nil
}

// mergeGroupEnvs creates a merged environment for a group of records.
// Variables that differ across records are collected into arrays so that
// path navigation in the value expression can operate on all values.
func mergeGroupEnvs(envs []*Environment) *Environment {
	if len(envs) == 0 {
		return nil
	}
	if len(envs) == 1 {
		return envs[0]
	}
	// Find the common ancestor to use as parent of the merged env.
	merged := NewChildEnvironment(envs[0].Parent())

	// Collect variable names from tuple-specific envs only (stop at envs
	// that lack parentKey — those are shared ancestors with built-in bindings).
	varNames := map[string]struct{}{}
	for _, env := range envs {
		for e := env; e != nil; e = e.Parent() {
			if _, has := e.LookupDirect(parentKey); !has {
				break
			}
			e.Range(func(name string, _ any) {
				varNames[name] = struct{}{}
			})
		}
	}

	// For each variable, collect values from each env via Lookup (full chain).
	for name := range varNames {
		if name == parentKey || name == parentJoinFlag {
			if v, ok := envs[0].Lookup(name); ok {
				merged.Bind(name, v)
			}
			continue
		}
		var vals []any
		for _, env := range envs {
			if v, ok := env.Lookup(name); ok {
				vals = append(vals, v)
			}
		}
		if len(vals) == 1 {
			merged.Bind(name, vals[0])
		} else if len(vals) > 0 {
			// Check if all values are identical — if so, keep single value.
			allSame := true
			for _, v := range vals[1:] {
				if !DeepEqual(v, vals[0]) {
					allSame = false
					break
				}
			}
			if allSame {
				merged.Bind(name, vals[0])
			} else {
				merged.Bind(name, vals)
			}
		}
	}
	return merged
}

// evalPathStep evaluates a single path step against input.
// For steps that don't natively handle arrays (like blocks, function calls,
// binary operators, subscripts), it maps the step over each element of an
// array input.
//
// prevWasMapper indicates whether the immediately preceding path step was a
// field-mapping step (NodeName/Wildcard/Descendant). When true, a subscript
// whose left side is a NodeName should be applied per-element (e.g.
// nest0.nest1[0] → [1,3,5,6]). When false (subscript is the first step),
// the subscript applies to the whole collected array (e.g. a[0].b → [1]).
//
// keepSingletonArray, when true, means the path has [] — group steps should
// NOT collapse their 1-element result (e.g. $.[v,e][] for 1 item → [[v,e]]).
func evalPathStep(
	step *parser.Node, input any, env *Environment, prevWasMapper, keepSingletonArray bool,
) (any, error) {
	// Steps that already handle array inputs natively (field lookup, wildcard,
	// descendant, variable, literals, sort) are delegated directly.
	switch step.Type {
	case parser.NodeNumber:
		// S0213: a numeric literal is not a valid path step (use [n] subscript notation instead).
		return nil, &JSONataError{Code: "S0213", Token: step.Value, Message: "invalid step in path: numeric literal is not a field name"}
	case parser.NodeName, parser.NodeWildcard,
		parser.NodeVariable, parser.NodeString, parser.NodeValue,
		parser.NodeSort: // Sort steps must be applied to the full accumulated input, not mapped per-element.
		return Eval(step, input, env)
	case parser.NodeBlock:
		// A block not preceded by a mapping step (prevWasMapper=false) should receive
		// the full input so that $^(age) inside can sort the whole array.
		// A block preceded by a mapper is handled by per-element fallthrough below.
		if !prevWasMapper {
			return Eval(step, input, env)
		}
	case parser.NodeDescendant:
		// The descendant operator in a path (A.**.B) must include A itself in the
		// search so that B can be found at any depth INCLUDING the current level.
		// Arrays are transparent containers: their elements are already included
		// by descendantLookup, so adding the array itself would cause duplicate
		// results when subsequent field lookups auto-map through both the array
		// and its individually-included elements.
		seq := CreateSequence()
		if _, isArr := input.([]any); !isArr {
			appendToSequence(seq, input)
		}
		appendToSequence(seq, descendantLookup(input))
		if len(seq.Values) == 0 {
			return nil, nil
		}
		return seq, nil
	case parser.NodeBinary:
		// Subscript steps map per-element when preceded by a mapping step (prevWasMapper=true).
		// When NOT preceded by a mapper, apply the subscript to the whole collected array.
		if step.Value == "[" && step.Left != nil && !prevWasMapper {
			return Eval(step, input, env)
		}
	}

	// Array constructor steps ([...]) that are NOT preceded by a mapping step
	// are literal expressions that should be evaluated once (e.g. [1,2,3].$).
	// When preceded by a mapper (prevWasMapper=true), they are mapped per-element.
	if step.Type == parser.NodeUnary && step.Value == "[" && !prevWasMapper {
		return Eval(step, input, env)
	}

	// For all other step types (subscripts with field-name left, function calls, etc.),
	// map the step over each element of an array input.
	arr, ok := input.([]any)
	if !ok {
		// Single-item path context for function call: check if lambda context-prepend needed.
		if step.Type == parser.NodeFunction {
			return evalPathFunctionStep(step, input, env)
		}
		return Eval(step, input, env)
	}

	// isGroupStep is true for .[...] — the array-constructor group step.
	// Group steps produce one array per input element that must NOT be flattened;
	// each per-element result is kept as a nested array in the output sequence.
	isGroupStep, seq, evalItem := step.Type == parser.NodeUnary && step.Value == "[", CreateSequence(), Eval
	if step.Type == parser.NodeFunction {
		evalItem = evalPathFunctionStep
	}
	for _, item := range arr {
		val, err := evalItem(step, item, env)
		if err != nil {
			return nil, err
		}
		if val == nil {
			continue
		}
		if isGroupStep {
			// Keep the per-element array as a nested element (no flattening).
			seq.Values = append(seq.Values, val)
			continue
		}
		switch v := val.(type) {
		case []any:
			seq.Values = append(seq.Values, v...)
		case *Sequence:
			seq.Values = append(seq.Values, v.Values...)
		default:
			appendToSequence(seq, val)
		}
	}
	if len(seq.Values) == 0 {
		return nil, nil
	}
	if isGroupStep && keepSingletonArray {
		// With [] (keepSingletonArray), prevent singleton collapse so that a
		// path like $.[v,e][] with 1 input element returns [[v,e]] not [v,e].
		return seq.Values, nil
	}
	return CollapseSequence(seq), nil
}

// evalPathFunctionStep evaluates a NodeFunction step in path context.
// In JSONata, when a function is called as a path step (e.g. arr.λ($x,$y){...}(6)),
// the path element is PREPENDED as the first argument to the lambda. For builtins,
// the path element is passed as focus so functions can use it as a fallback when
// fewer arguments are provided (e.g., str.$contains("x") → $contains uses focus
// as the string to search in).
func evalPathFunctionStep(step *parser.Node, item any, env *Environment) (any, error) {
	// Resolve the function.
	var fn any
	if step.Procedure != nil {
		var err error
		fn, err = Eval(step.Procedure, item, env)
		if err != nil {
			return nil, err
		}
	}
	// Evaluate the declared arguments.
	args := make([]any, 0, len(step.Arguments))
	for _, argNode := range step.Arguments {
		if argNode.Type == parser.NodePlaceholder {
			args = append(args, nil)
			continue
		}
		val, err := Eval(argNode, item, env)
		if err != nil {
			return nil, err
		}
		args = append(args, val)
	}
	// For user-defined lambdas, prepend the path element as the first argument
	// only when there are fewer explicit args than lambda parameters.
	// (If args already fill all params, the path element is only available as $.)
	if lam, isLambda := fn.(*Lambda); isLambda && len(args) < len(lam.Params) {
		args = append([]any{item}, args...)
	}
	return callFunction(fn, args, item, env)
}

// descendantLookup recursively collects all values at all depths from maps and arrays.
// It returns a *Sequence (never collapses to []any) so that appendToSequence can
// properly flatten the results when building the descendant step sequence.
//
// For arrays, individual elements are added (not the array as a whole), matching
// JSONata semantics where ** traverses into arrays and exposes each element for
// field lookup in subsequent path steps.
func descendantLookup(input any) *Sequence {
	seq := CreateSequence()
	if IsMap(input) {
		MapRange(input, func(_ string, val any) bool {
			if arr, ok := val.([]any); ok {
				for _, item := range arr {
					appendToSequence(seq, item)
					appendToSequence(seq, descendantLookup(item))
				}
			} else {
				appendToSequence(seq, val)
				appendToSequence(seq, descendantLookup(val))
			}
			return true
		})
	} else if arr, ok := input.([]any); ok {
		for _, item := range arr {
			appendToSequence(seq, item)
			appendToSequence(seq, descendantLookup(item))
		}
	}
	return seq
}
