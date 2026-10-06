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
		// A sort step, with any predicates, whose Left contains #$var or @$var
		// bindings needs tuple mode so they survive through the sort into
		// subsequent steps.
		if sortLeftHasBinding(step) {
			return true
		}
		// Any step that references % (NodeParent) requires parent-chain tracking.
		if nodeHasParentRef(step) {
			return true
		}
	}
	return false
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

// evalPathSimple is step-by-step path evaluation for paths with no index,
// focus or parent (%) bindings.
func evalPathSimple(node *parser.Node, input any, env *Environment) (any, error) {
	result := input
	prevWasMapper := false
	for i, step := range node.Steps {
		if i > 0 && result == nil {
			return nil, nil
		}
		if seq, ok := result.(*Sequence); ok {
			result = flattenKept(CollapseSequence(seq))
			if i > 0 && result == nil {
				return nil, nil
			}
		}

		var err error
		// A constructed array, as in a.[b, c], is one context item for the
		// step rather than a sequence to map over.
		switch _, consItem := result.(ConsArray); {
		case consItem:
			result, err = evalConsArrayStep(step, result, env, node.KeepSingletonArray)
		case i > 0 && step.Type == parser.NodeVariable:
			result, err = evalVariableStep(step, result, env, i == len(node.Steps)-1)
		default:
			result, err = evalPathStep(step, result, env, prevWasMapper, node.KeepSingletonArray, i == len(node.Steps)-1)
		}
		if err != nil {
			return nil, err
		}
		result = flattenKept(spreadSortIndexStage(step, result))
		// Mapping over a one-item array nests a constructed array for [];
		// unnest it so later steps see it whole and it is wrapped once.
		if prevWasMapper && node.KeepSingletonArray {
			result = unnestCons(result)
		}
		if _, consItem := result.(ConsArray); !consItem && prevWasMapper && isNothingFound(step, result) {
			return nil, nil
		}
		_, isArr := result.([]any)
		_, isSeq := result.(*Sequence)
		prevWasMapper = isArr || isSeq
	}

	if seq, ok := result.(*Sequence); ok {
		result = CollapseSequence(seq)
	}
	if node.KeepSingletonArray {
		return keepSingletonArray(result), nil
	}
	return result, nil
}

// isNothingFound reports whether a step mapped over a sequence produced an
// empty array that means "nothing found" (undefined). After a direct access
// (not a mapper), the empty array is a genuine field value (e.g.
// obj.emptyList) and must be preserved so that $exists sees it as defined.
// For field-lookup steps (NodeName/NodeString), evalName already
// distinguishes "nothing found" (returns nil) from "field exists with empty
// array value" (returns []any{}), and * returns nil for nothing found and []
// only for flattened empty arrays, so those step types are skipped.
func isNothingFound(step *parser.Node, result any) bool {
	arr, ok := AsArray(result)
	return ok && len(arr) == 0 && step.Type != parser.NodeName && step.Type != parser.NodeString && step.Type != parser.NodeWildcard
}

// unnestCons returns the constructed array inside a one-item array.
func unnestCons(v any) any {
	if arr, ok := v.([]any); ok && len(arr) == 1 {
		if cons, ok := arr[0].(ConsArray); ok {
			return cons
		}
	}
	return v
}

// flattenKept flattens a KeptArray or RawSequence as a path step flattens
// any sequence, returning its item (o.(b[]) is 5), or undefined for an
// empty RawSequence.
func flattenKept(v any) any {
	switch a := v.(type) {
	case KeptArray:
		return a[0]
	case RawSequence:
		return settleRaw(a, false)
	}
	return v
}

// keepSingletonArray applies a path's [] suffix: a single value, including a
// constructed array, becomes a KeptArray, while an array result stays as is.
func keepSingletonArray(result any) any {
	switch v := result.(type) {
	case []any, KeptArray:
		return v
	case nil:
		return nil
	}
	return KeptArray{result}
}

// evalConsArrayStep evaluates a step whose input is an array constructed
// from a single context, as in a.[b, c].$string(): the whole array is one
// context item rather than a sequence to map over.
func evalConsArrayStep(step *parser.Node, input any, env *Environment, keepSingleton bool) (any, error) {
	switch {
	case step.Type == parser.NodeFunction:
		return evalPathFunctionStep(step, input, env)
	case step.Type == parser.NodeUnary && step.Value == "{":
		return consArrayGroup(step, input, env)
	}
	return evalPathStep(step, input, env, false, keepSingleton, false)
}

// consArrayGroup applies a {...} step to a constructed array taken as one
// context item, grouping its elements by key like ${...} does.
func consArrayGroup(step *parser.Node, input any, env *Environment) (any, error) {
	items, _ := AsArray(input)
	return groupItems(objectPairs(step.LHS), items, env)
}

// evalPathTuple evaluates a path that contains #$var or @$var bindings or %
// references by walking it as a tuple stream (see walkPathTuple), then
// applies its group or collects the tuples' values.
func evalPathTuple(node *parser.Node, input any, env *Environment) (any, error) {
	ctxs, finalGroup, rawContext, err := walkPathTuple(node, []pathCtx{{value: input, env: env}}, env, streamOwn)
	if err != nil {
		return nil, err
	}

	// Determine which group expression to apply (step-level or path-level).
	grp := finalGroup
	if node.Group != nil {
		grp = node.Group
	}
	if grp != nil {
		if rawContext != nil {
			ctxs = slices.Clone(ctxs)
			for i, ctx := range ctxs {
				ctxs[i].value, ctxs[i].env = rawContext(ctx)
			}
		}
		return evalTupleGroup(grp, ctxs)
	}

	// Collect final values.
	seq := CreateSequence()
	for _, ctx := range ctxs {
		appendToSequence(seq, ctx.value)
	}
	result := CollapseSequence(seq)

	if !node.KeepSingletonArray {
		return result, nil
	}
	// A tuple stream holds a constructed array's items, as in jsonata-js:
	// o#$i.[b,c][] is [5,6].
	if cons, ok := result.(ConsArray); ok {
		return []any(cons), nil
	}
	return keepSingletonArray(result), nil
}

// streamPos says where a walked path sits relative to the tuple stream.
type streamPos int

const (
	streamOwn     streamPos = iota // the path's own binding step starts it, if any
	streamPending                  // it starts at or after the path, as for a sort's Left
	streamRunning                  // it started before the path
)

// walkPathTuple runs a path's steps over a tuple stream of (value, env)
// contexts, so a #$var or @$var bound at one step stays visible in later
// steps. An index binds the position in the step's output, as in jsonata-js.
//
// It returns the resulting contexts and the first step-level group (as in
// Product{key:val}), stripped from its step so the caller applies it with the
// per-tuple envs. rawContext is non-nil when the last step was a sort that
// jsonata-js leaves as a plain array of tuples (see rawTupleContext): a group
// then sees raw tuple objects.
func walkPathTuple( //nolint:gocyclo,funlen // dispatch
	node *parser.Node, ctxs []pathCtx, env *Environment, state streamPos,
) (_ []pathCtx, _ *parser.GroupExpr, rawContext tupleContext, _ error) {
	tupleStart := firstBindingStep(node.Steps)
	if state == streamRunning {
		tupleStart = -1
	}
	// %-only paths have no binding step; they keep splitting constructed arrays.
	hasBindingStep := tupleStart < len(node.Steps) || state == streamPending

	// finalGroup accumulates the first step-level Group expression encountered.
	// Step-level groups are applied after all contexts have been collected.
	var finalGroup *parser.GroupExpr

	for stepIdx, step := range node.Steps {
		if err := env.Err(); err != nil {
			return nil, nil, nil, err
		}
		var nextCtxs []pathCtx
		rawContext = nil
		// jsonata-js evaluates the steps before the first binding as plain
		// steps; the binding step starts the tuple stream.
		beforeStream := hasBindingStep && stepIdx < tupleStart
		startsStream := hasBindingStep && stepIdx == tupleStart
		inStream := hasBindingStep && stepIdx > tupleStart

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
		// Predicates on the sort, as in ^(x)[0]#$i, then filter the sorted stream.
		// Before the tuple stream starts, a sort with predicates is a plain
		// step, evaluated below like any other.
		sortStep, stages := splitTupleStages(step)
		if sortStep.Type == parser.NodeSort && (len(stages) == 0 || startsStream || inStream) {
			leftState := streamOwn
			switch {
			case inStream:
				leftState = streamRunning
			case beforeStream || startsStream:
				leftState = streamPending
			}
			raw := rawTupleContext(node.Steps[:stepIdx+1], env)
			sorted, err := evalTupleSort(sortStep, ctxs, env, leftState, raw)
			if err != nil {
				return nil, nil, nil, err
			}
			if startsStream && !sortStep.Tuple {
				bindSortIndex(sortStep, sorted)
			}
			// jsonata-js sorts an existing tuple stream into a plain array of
			// tuples, so its predicates see each raw tuple object rather than
			// its value and bindings.
			var contextOf tupleContext
			if inStream || sortStep.Tuple {
				contextOf = raw
			}
			rawContext = contextOf
			if ctxs, err = applyTupleStages(stages, sorted, contextOf); err != nil {
				return nil, nil, nil, err
			}
			continue
		}

		// % steps, with any predicates such as %[0], navigate up the parent
		// chain; the predicates then filter the whole parent stream.
		if base, stages := splitTupleStages(evalStep); base.Type == parser.NodeParent {
			stream, err := parentTuples(base, ctxs)
			if err != nil {
				return nil, nil, nil, err
			}
			if ctxs, err = applyTupleStages(stages, stream, nil); err != nil {
				return nil, nil, nil, err
			}
			if len(ctxs) == 0 {
				break
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
				if steps := blockPathSteps(block); steps != nil {
					var err error
					tupleCtxs, err = expandPathTuple(steps, []pathCtx{ctx}, false)
					if err != nil {
						return nil, nil, nil, err
					}
				} else {
					blockResult, err := Eval(block, ctx.value, ctx.env)
					if err != nil {
						return nil, nil, nil, err
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
						return nil, nil, nil, err
					}
					if ToBoolean(predResult) {
						nextCtxs = append(nextCtxs, tctx)
					}
				}
			}
			ctxs = nextCtxs
			if len(ctxs) == 0 {
				break
			}
			continue
		}

		// When the step is a Block containing a single Path expression,
		// expand the inner path in tuple mode to preserve parent bindings
		// for the % operator (e.g., Account.(Order.Product).{%.OrderID}).
		if steps := blockPathSteps(evalStep); evalStep.Type == parser.NodeBlock && steps != nil {
			for _, ctx := range ctxs {
				expanded, err := expandPathTuple(steps, []pathCtx{ctx}, false)
				if err != nil {
					return nil, nil, nil, err
				}
				if evalStep.Index != "" {
					for k := range expanded {
						expanded[k].env.Bind(evalStep.Index, float64(k))
					}
				}
				nextCtxs = append(nextCtxs, expanded...)
			}
			ctxs = nextCtxs
			if len(ctxs) == 0 {
				break
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
				return nil, nil, nil, err
			}
			if postFilterIndex != "" {
				for k := range nextCtxs {
					nextCtxs[k].env.Bind(postFilterIndex, float64(k))
				}
			}
			ctxs = nextCtxs
			if len(ctxs) == 0 {
				break
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
				return nil, nil, nil, err
			}

			// Apply the outer subscript to the collected tuples.
			outerExpr := evalStep.Right
			if len(nextCtxs) > 0 {
				outerResult, err := Eval(outerExpr, nextCtxs[0].value, nextCtxs[0].env)
				if err != nil {
					return nil, nil, nil, err
				}
				if idx, ok := ToFloat64(outerResult); ok {
					i := ToIntClamped(idx)
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
				break
			}
			continue
		}

		if base, stages := splitTupleStages(evalStep); (startsStream || inStream) && len(stages) > 0 && isPlainTupleBase(base) {
			var err error
			if ctxs, err = evalTupleStages(base, stages, ctxs, node.KeepSingletonArray); err != nil {
				return nil, nil, nil, err
			}
			if len(ctxs) == 0 {
				break
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

			result, err := evalTupleContextStep(evalStep, val, ctx.env, node.KeepSingletonArray, beforeStream)
			if err != nil {
				return nil, nil, nil, err
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
			break
		}
	}

	if err := env.Err(); err != nil {
		return nil, nil, nil, err
	}
	return ctxs, finalGroup, rawContext, nil
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
			result, err := evalPathStep(step, val, ctx.env, false, keepSingleton, false)
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
		result, err := evalPathStep(lastStep, val, ctx.env, false, false, false)
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
// directly by their own values. state places the sort relative to the tuple
// stream; before it starts, Left's constructed arrays are sorted as single
// items.
//
// A Left that is itself a sort, as in a#$j^(x)^(y), sorts first. When a
// stream already existed before that sort (inStream, or bindings in its
// Left), jsonata-js leaves a plain array of tuples, so its predicates and
// this sort's terms see raw tuple objects (see rawTupleContext).
func evalTupleSort(step *parser.Node, ctxs []pathCtx, env *Environment, state streamPos, raw tupleContext) ([]pathCtx, error) {
	inStream := state == streamRunning
	// Determine if we need to navigate via step.Left before sorting.
	needsNavigation := step.Left != nil &&
		step.Left.Type != parser.NodeVariable // bare NodeVar "" means sort-in-place

	var leftSort *parser.Node
	var leftStages []tupleStage
	if needsNavigation {
		left := step.Left
		if parser.IsBoundSortPath(left) {
			left = left.Steps[0]
		}
		leftSort, leftStages = splitTupleStages(left)
	}
	var termContext tupleContext
	if leftSort != nil && leftSort.Type == parser.NodeSort {
		inner, err := evalTupleSort(leftSort, ctxs, env, state, raw)
		if err != nil {
			return nil, err
		}
		if !inStream && !leftSort.Tuple {
			bindSortIndex(leftSort, inner)
		}
		if inStream || leftSort.Tuple {
			termContext = raw
		}
		if ctxs, err = applyTupleStages(leftStages, inner, termContext); err != nil {
			return nil, err
		}
	} else if needsNavigation {
		// Expand: navigate via step.Left for each ctx, collect all (child, childEnv) tuples.
		// A Left path runs as a tuple walk so that index variables survive into the
		// sorted output; a plain Eval would discard the per-element environments.
		var expanded []pathCtx
		switch {
		case step.Left.Type == parser.NodePath && len(step.Left.Steps) > 0 && step.Left.Group == nil && !pathHasStepGroup(step.Left):
			var err error
			if expanded, _, _, err = walkPathTuple(step.Left, ctxs, env, state); err != nil {
				return nil, err
			}
		case step.Left.Type == parser.NodePath && len(step.Left.Steps) > 0:
			var err error
			expanded, err = expandPathTuple(step.Left.Steps, ctxs, state == streamPending)
			if err != nil {
				return nil, err
			}
		default:
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
		aVal, aEnv, bVal, bEnv := a.value, a.env, b.value, b.env
		if termContext != nil {
			aVal, aEnv = termContext(a)
			bVal, bEnv = termContext(b)
		}
		return compareSortTerms(step.Terms, aVal, bVal, aEnv, bEnv)
	}); err != nil {
		return nil, err
	}
	return sorted, nil
}

// bindSortIndex binds a sort step's #$var to the sorted positions, for a sort
// that starts the tuple stream.
func bindSortIndex(step *parser.Node, sorted []pathCtx) {
	if step.Index == "" {
		return
	}
	for k := range sorted {
		sorted[k].env = NewChildEnvironment(sorted[k].env)
		sorted[k].env.Bind(step.Index, float64(k))
	}
}

// pathHasStepGroup reports whether any step of path carries a group.
func pathHasStepGroup(path *parser.Node) bool {
	return slices.ContainsFunc(path.Steps, func(step *parser.Node) bool { return step.Group != nil })
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
		leftResult, err := evalPathStep(leftNode, val, ctx.env, false, false, false)
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

	if seq, ok := result.(*Sequence); ok {
		if result = CollapseSequence(seq); result == nil {
			return
		}
	}
	arr, ok := AsArray(result)
	if !ok {
		*nextCtxs = append(*nextCtxs, pathCtx{value: ctxValue(result), env: bindAt(0, result)})
		return
	}
	for j, elem := range arr {
		*nextCtxs = append(*nextCtxs, pathCtx{value: ctxValue(elem), env: bindAt(j, elem)})
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
	if seq, ok := result.(*Sequence); ok {
		if result = CollapseSequence(seq); result == nil {
			return
		}
	}
	arr, ok := AsArray(result)
	if !ok {
		*nextCtxs = append(*nextCtxs, pathCtx{value: result, env: bindAt(0, result)})
		return
	}
	for j, elem := range arr {
		*nextCtxs = append(*nextCtxs, pathCtx{value: elem, env: bindAt(j, elem)})
	}
}

// expandPathTuple runs a mini tuple walk over the given path steps, starting
// from the given ctxs. It returns the resulting (value, env) pairs, preserving
// #$var bindings and parent context. Block and subscript steps use it, and so
// does a sort whose Left path has a group, which walkPathTuple would leave to
// its caller.
//
// With beforeStream, the walk starts outside a tuple stream: until a step
// binds a variable, steps run as plain steps (see evalTupleContextStep).
func expandPathTuple(steps []*parser.Node, ctxs []pathCtx, beforeStream bool) ([]pathCtx, error) {
	for _, step := range steps {
		beforeStream = beforeStream && !stepHasBinding(step)
		var next []pathCtx
		for _, ctx := range ctxs {
			val := ctx.value
			if seq, ok := val.(*Sequence); ok {
				val = CollapseSequence(seq)
			}
			if val == nil {
				continue
			}
			result, err := evalTupleContextStep(step, val, ctx.env, false, beforeStream)
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
				groupCtx = groupContext(g.values)
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
	merged.decimalPrecision = envs[0].decimalPrecision

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
				if !DeepEqualPrec(v, vals[0], merged.DecimalPrecision()) {
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
//
// lastStep, when true, returns a lone plain-array item result unflattened,
// as jsonata-js evaluateStep does: x.(x ? [1]) is [1].
func evalPathStep(
	step *parser.Node, input any, env *Environment, prevWasMapper, keepSingletonArray, lastStep bool,
) (any, error) {
	// Steps that already handle array inputs natively (field lookup, wildcard,
	// descendant, variable, literals, sort) are delegated directly.
	switch step.Type {
	case parser.NodeNumber:
		// S0213: a numeric literal is not a valid path step (use [n] subscript notation instead).
		return nil, &JSONataError{Code: "S0213", Token: step.Value, Message: "invalid step in path: numeric literal is not a field name"}
	case parser.NodeWildcard:
		return evalPathStepWildcard(step, input, env)
	case parser.NodeName,
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
		return evalPathStepDescendant(input, env)
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

	arr, ok := pathStepArray(input)
	if !ok {
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
	var lone any // the last defined item result
	defined := 0
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
		defined++
		lone = val
		appendFlattened(seq, val)
	}
	// As jsonata-js evaluateStep does, a last step returns a lone item
	// result that is a plain array as is; a sequence would be flattened.
	if plain, ok := lone.([]any); ok && lastStep && defined == 1 {
		return plain, nil
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

// appendFlattened appends a step's result for one item to seq, flattening
// an array or sequence, but not a constructed array, as jsonata-js
// evaluateStep does.
func appendFlattened(seq *Sequence, val any) {
	switch v := val.(type) {
	case []any, KeptArray, RawSequence:
		arr, _ := AsArray(v)
		seq.Values = append(seq.Values, arr...)
	default:
		appendToSequence(seq, val)
	}
}

// tupleStage is one predicate of a step, with the #$var bound after it.
type tupleStage struct {
	predicate *parser.Node
	index     string
}

// firstBindingStep returns the index of the first step carrying a #$var or
// @$var binding, or len(steps) when there is none. From that step on,
// jsonata-js evaluates the path as a tuple stream.
func firstBindingStep(steps []*parser.Node) int {
	if i := slices.IndexFunc(steps, stepHasBinding); i >= 0 {
		return i
	}
	return len(steps)
}

// stepHasBinding reports whether a step, or the base of its predicates,
// carries a #$var or @$var binding, or is a sort whose Left does.
func stepHasBinding(step *parser.Node) bool {
	if sortLeftHasBinding(step) {
		return true
	}
	for n := step; n != nil; n = n.Left {
		if n.Index != "" || n.Focus != "" {
			return true
		}
		if n.Type != parser.NodeBinary || n.Value != "[" {
			break
		}
	}
	return false
}

// sortLeftHasBinding reports whether step is a sort, with any predicates,
// whose Left carries a #$var or @$var binding.
func sortLeftHasBinding(step *parser.Node) bool {
	base, _ := splitTupleStages(step)
	return base.Type == parser.NodeSort && base.Tuple
}

// splitTupleStages splits a step such as X[p1]#$i[p2] into its base step X
// and its predicate stages in source order.
func splitTupleStages(step *parser.Node) (*parser.Node, []tupleStage) {
	var stages []tupleStage
	for step.Type == parser.NodeBinary && step.Value == "[" && step.Left != nil && step.Right != nil {
		stages = append(stages, tupleStage{predicate: step.Right, index: step.Index})
		step = step.Left
	}
	slices.Reverse(stages)
	return step, stages
}

// isPlainTupleBase reports whether a predicated step's base is one that
// evalTupleStages can expand per context; joins and % keep their dedicated
// handling.
func isPlainTupleBase(base *parser.Node) bool {
	switch base.Type {
	case parser.NodeName, parser.NodeString, parser.NodeWildcard, parser.NodeDescendant,
		parser.NodeBlock, parser.NodeFunction, parser.NodeVariable, parser.NodeUnary:
		return base.Focus == "" && base.Group == nil && !nodeHasParentRef(base)
	}
	return false
}

// evalTupleStages expands base for every context, then applies each stage
// to the whole resulting tuple stream rather than per context, as
// jsonata-js does once a path is a tuple stream: Product[0]#$i keeps only
// the first product overall, and a stage #$i numbers the filtered stream.
func evalTupleStages(base *parser.Node, stages []tupleStage, ctxs []pathCtx, keepSingleton bool) ([]pathCtx, error) {
	var stream []pathCtx
	for _, ctx := range ctxs {
		val := ctx.value
		if seq, ok := val.(*Sequence); ok {
			val = CollapseSequence(seq)
		}
		if val == nil {
			continue
		}
		result, err := evalTupleContextStep(base, val, ctx.env, keepSingleton, false)
		if err != nil {
			return nil, err
		}
		if result != nil {
			appendTupleResults(base, result, ctx.value, ctx.env, &stream)
		}
	}
	return applyTupleStages(stages, stream, nil)
}

// evalTupleContextStep evaluates a step against one tuple's context value.
// A constructed array is one context item, as in the simple path. With
// beforeStream the step runs before the tuple stream starts, where jsonata-js
// evaluates it as a plain step: a constructed array it yields stays one
// context, unless a sort's [n] predicate picked it (see classifySortStep).
func evalTupleContextStep(step *parser.Node, val any, env *Environment, keepSingleton, beforeStream bool) (any, error) {
	var result any
	var err error
	switch _, cons := val.(ConsArray); {
	case cons:
		result, err = evalConsArrayStep(step, val, env, keepSingleton)
	case step.Type == parser.NodeWildcard:
		result, err = evalWildcard(step, val, env)
	default:
		result, err = evalPathStep(step, val, env, false, keepSingleton, false)
	}
	if err != nil {
		return nil, err
	}
	if !beforeStream {
		return result, nil
	}
	result = spreadSortIndexStage(step, result)
	if cons, ok := result.(ConsArray); ok {
		return []any{cons}, nil
	}
	return result, nil
}

// tupleContext returns the context value and environment a stage predicate
// is evaluated against for a tuple; nil means the tuple's own value and env.
type tupleContext func(ctx pathCtx) (any, *Environment)

// rawTupleContext evaluates predicates against a jsonata-js tuple object:
// {"@": value} plus the bindings made along steps, in env rather than the
// tuple's env. The binding names are found on first use, since most sorts
// never need them.
func rawTupleContext(steps []*parser.Node, env *Environment) tupleContext {
	var names []string
	found := false
	return func(ctx pathCtx) (any, *Environment) {
		if !found {
			names, found = bindingNames(steps, env), true
		}
		tuple := NewOrderedMap()
		tuple.Set("@", ctx.value)
		for _, name := range names {
			if val, ok := ctx.env.Lookup(name); ok {
				tuple.Set(name, val)
			}
		}
		return tuple, env
	}
}

// bindingNames returns the #$var and @$var names bound along nodes' steps and
// Left chains, in order. Bindings inside a predicate stay local to it, so
// predicates (Right) are not searched, nor are sorts whose Left binds
// nothing. The walk stops once env's deadline passes; the caller's next
// deadline check then fails the evaluation.
func bindingNames(nodes []*parser.Node, env *Environment) []string {
	var names []string
	visits, stopped := 0, false
	var walk func(n *parser.Node)
	walk = func(n *parser.Node) {
		if n == nil || stopped {
			return
		}
		visits++
		if visits%cancelCheckInterval == 0 && env.Err() != nil {
			stopped = true
			return
		}
		if n.Type != parser.NodeSort || n.Tuple {
			walk(n.Left)
		}
		for _, step := range n.Steps {
			walk(step)
		}
		// jsonata-js adds a step's focus to the tuple before its index.
		for _, name := range []string{n.Focus, n.Index} {
			if name != "" && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return names
}

// applyTupleStages filters a tuple stream by each stage in turn, binding a
// stage's #$var to the positions of the filtered stream.
func applyTupleStages(stages []tupleStage, stream []pathCtx, contextOf tupleContext) ([]pathCtx, error) {
	for _, stage := range stages {
		var err error
		if stream, err = filterTupleStream(stage.predicate, stream, contextOf); err != nil {
			return nil, err
		}
		if stage.index != "" {
			for k := range stream {
				stream[k].env.Bind(stage.index, float64(k))
			}
		}
	}
	return stream, nil
}

// parentTuples evaluates a % step for every tuple: the parent value bound by
// appendTupleResults becomes the context, with the parent's env so chained
// %.% keeps walking up. A %@$v step instead keeps the context and binds the
// parent to $v, and %#$i binds the parent's position, which is always 0.
func parentTuples(step *parser.Node, ctxs []pathCtx) ([]pathCtx, error) {
	next := make([]pathCtx, 0, len(ctxs))
	for _, ctx := range ctxs {
		// LookupWithEnv finds the env that directly holds %%, whose parent is
		// the parent level even when evalBlock or other scope creators added
		// intermediate envs that still see %% through the chain.
		parentVal, bindingEnv, ok := ctx.env.LookupWithEnv(parentKey)
		if !ok || parentVal == nil {
			return nil, &JSONataError{Code: "S0217", Message: "% operator used outside of a valid path context"}
		}
		parentEnv := bindingEnv.Parent()
		// Join steps don't change the context level, so consecutive join
		// bindings with the same parent value are one navigation depth.
		if _, isJoin := bindingEnv.LookupDirect(parentJoinFlag); isJoin {
			for parentEnv != nil {
				if _, joinToo := parentEnv.LookupDirect(parentJoinFlag); !joinToo {
					break
				}
				pv, pe, has := parentEnv.LookupWithEnv(parentKey)
				if !has || pv != parentVal {
					break
				}
				parentEnv = pe.Parent()
			}
		}
		if parentEnv == nil {
			parentEnv = bindingEnv
		}
		tuple := pathCtx{value: parentVal, env: parentEnv}
		if step.Focus != "" {
			tuple = ctx
		}
		if step.Focus != "" || step.Index != "" {
			tuple.env = NewChildEnvironment(tuple.env)
		}
		if step.Focus != "" {
			tuple.env.Bind(step.Focus, parentVal)
		}
		if step.Index != "" {
			tuple.env.Bind(step.Index, float64(0))
		}
		next = append(next, tuple)
	}
	return next, nil
}

// filterTupleStream keeps the tuples whose predicate is truthy, or whose
// position matches the predicate when it yields a number or an array of
// numbers (negative positions count from the end).
func filterTupleStream(predicate *parser.Node, stream []pathCtx, contextOf tupleContext) ([]pathCtx, error) {
	var kept []pathCtx
	for pos, ctx := range stream {
		if err := ctx.env.Err(); err != nil {
			return nil, err
		}
		value, env := ctx.value, ctx.env
		if contextOf != nil {
			value, env = contextOf(ctx)
		}
		res, err := Eval(predicate, value, env)
		if err != nil {
			return nil, err
		}
		matches, err := filterMatches(res, pos, len(stream), len(kept), ctx.env)
		if err != nil {
			return nil, err
		}
		for range matches {
			kept = append(kept, ctx)
		}
	}
	return kept, nil
}

// filterMatches returns how many times a filter keeps the item at pos for
// predicate result res, as jsonata-js evaluateFilter does: once per
// position a number or array of numbers selects, or else once if res is
// truthy. kept is the number of items already kept, checked against the
// sequence limit when repeated positions grow the result past length.
func filterMatches(res any, pos, length, kept int, env *Environment) (int, error) {
	if b, ok := res.(bool); ok {
		if b {
			return 1, nil
		}
		return 0, nil
	}
	matches, positional, err := positionMatches(res, pos, length)
	if err != nil {
		return 0, err
	}
	if !positional && ToBoolean(res) {
		matches = 1
	}
	if kept+matches > length {
		if err := env.CheckSequence(kept + matches); err != nil {
			return 0, err
		}
	}
	return matches, nil
}

// positionMatches counts how many of the positions a predicate result
// selects equal pos; positional is false unless the result is a number or a
// non-empty array of numbers.
func positionMatches(res any, pos, length int) (matches int, positional bool, _ error) {
	resolved, positional, err := selectedPositions(res, length)
	for _, i := range resolved {
		if i == pos {
			matches++
		}
	}
	return matches, positional, err
}

// selectedPositions resolves the positions a predicate result selects (see
// positionMatches) with subscriptIndex.
func selectedPositions(res any, length int) (resolved []int, positional bool, _ error) {
	indices, isArr := AsArray(res)
	if !isArr {
		indices = []any{res}
	} else if len(indices) == 0 {
		return nil, false, nil
	}
	resolved, ok, err := resolveIndices(indices, length)
	if err != nil || !ok {
		return nil, false, err
	}
	return resolved, true, nil
}

// blockPathSteps returns the path steps of a block holding a single path,
// treating a lone field name as a one-step path, or nil otherwise. A block
// with an @$var focus binding keeps the generic handling that binds it.
func blockPathSteps(block *parser.Node) []*parser.Node {
	if len(block.Expressions) != 1 || block.Focus != "" {
		return nil
	}
	switch expr := block.Expressions[0]; expr.Type {
	case parser.NodePath:
		return expr.Steps
	case parser.NodeName:
		return []*parser.Node{expr}
	}
	return nil
}

// evalVariableStep evaluates a variable step that is not the first step of
// its path once per context item, so a.$v yields one $v per item of a and
// an empty a yields nothing. Only a leading variable makes a path absolute.
//
// Array values are flattened one level, as jsonata-js does, except a
// constructed array: $ returns that item itself, which stays one value
// (a.[$].$ is [[1],[2]]). A last step with a single array result returns
// that array unchanged.
func evalVariableStep(step *parser.Node, input any, env *Environment, lastStep bool) (any, error) {
	items, ok := input.([]any)
	if !ok {
		return Eval(step, input, env)
	}
	results := make([]any, 0, len(items))
	for _, item := range items {
		val, err := Eval(step, item, env)
		if err != nil {
			return nil, err
		}
		if val != nil {
			results = append(results, val)
		}
	}
	if len(results) == 1 && lastStep {
		if arr, ok := results[0].([]any); ok {
			return arr, nil
		}
	}
	seq := CreateSequence()
	for _, val := range results {
		appendFlattened(seq, val)
	}
	if len(seq.Values) == 0 {
		return nil, nil
	}
	return CollapseSequence(seq), nil
}

// pathStepArray returns the array to map a step over. A ConsArray is
// deliberately excluded: it is one constructed value, not a sequence of
// several context values, so any step receiving it as input evaluates
// once against the whole array (matching jsonata-js, where a cons-flagged
// array is never re-expanded into the outer per-element iteration).
func pathStepArray(input any) ([]any, bool) {
	arr, ok := input.([]any)
	return arr, ok
}

// evalPathStepDescendant evaluates a ** path step (see collector.descendants).
func evalPathStepDescendant(input any, env *Environment) (any, error) {
	c := collector{env: env}
	c.descendants(input)
	if c.err != nil || len(c.values) == 0 {
		return nil, c.err
	}
	return &Sequence{Values: c.values}, nil
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
	result, err := callFunction(fn, args, item, env)
	if err != nil {
		return nil, err
	}
	return CollapseAndKeep(result, step.KeepArray), nil
}
