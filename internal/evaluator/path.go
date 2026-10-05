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
// tracking: the step or any subscript it is predicated on carries a #$var or
// @$var binding, or any step/sub-expression references NodeParent (%).
func pathHasTupleStep(steps []*parser.Node) bool {
	for _, step := range steps {
		if stepHasBinding(step) {
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

// evalPathSimple is step-by-step path evaluation for paths with no index,
// focus or parent (%) bindings.
func evalPathSimple(node *parser.Node, input any, env *Environment) (any, error) {
	result, singleConsArray, err := walkPathSimple(node, input, env)
	if err != nil {
		return nil, err
	}
	return pathResult(result, node.KeepSingletonArray, singleConsArray), nil
}

// walkPathSimple evaluates a path's steps, leaving a sequence result
// uncollapsed. singleConsArray reports that the result is an array
// constructed from a single context.
func walkPathSimple(node *parser.Node, input any, env *Environment) (result any, singleConsArray bool, _ error) {
	result = input
	prevWasMapper, prevWasCons := false, false
	for i, step := range node.Steps {
		if i > 0 && result == nil {
			return nil, false, nil
		}
		if seq, ok := result.(*Sequence); ok {
			result = sequenceContexts(seq)
			if i > 0 && result == nil {
				return nil, false, nil
			}
		}

		singleContext := !prevWasMapper || singleConsArray
		if arr, ok := result.([]any); ok && len(arr) == 1 {
			singleContext = true
		}
		mapped := prevWasMapper && !singleConsArray
		stepInput, lone := result, false
		var err error
		switch {
		case singleConsArray:
			result, err = evalConsArrayStep(step, result, env, node.KeepSingletonArray)
		case i > 0 && step.Type == parser.NodeVariable:
			result, lone, err = evalVariableStep(step, result, env, prevWasCons, i == len(node.Steps)-1)
		default:
			result, lone, err = evalPathStepLone(step, result, env, prevWasMapper, node.KeepSingletonArray)
		}
		if err != nil {
			return nil, false, err
		}
		if err := checkPathStep(stepInput, result, singleContext, lone, i == len(node.Steps)-1, env); err != nil {
			return nil, false, err
		}
		// An array constructed from a single context, as in a.[b, c], is one
		// context item for the next step rather than a sequence to map over.
		// A $ step returns each item unchanged, so that state carries over it.
		if i == 0 || step.Type != parser.NodeVariable || step.Value != "" {
			prevWasCons = i > 0 && step.Type == parser.NodeUnary && step.Value == "["
			singleConsArray = prevWasCons && singleContext
			// Mapping over a one-item array nests the array for []; unnest
			// it here so later steps see it whole and it is wrapped once.
			if singleConsArray && mapped && node.KeepSingletonArray {
				result = unnestSingleton(result)
			}
		}
		if prevWasMapper && !singleConsArray && isNothingFound(step, result) {
			return nil, false, nil
		}
		_, isArr := result.([]any)
		_, isSeq := result.(*Sequence)
		prevWasMapper = isArr || isSeq
	}

	return result, singleConsArray, nil
}

// pathResult is a path's value: a sequence collapses as the path ends, as
// in jsonata-js, unless [] keeps it an array.
func pathResult(result any, keepSingleton, singleConsArray bool) any {
	if seq, ok := result.(*Sequence); ok {
		if keepSingleton {
			return CollapseToSlice(seq)
		}
		return CollapseSequence(seq)
	}
	if keepSingleton {
		return keepSingletonArray(result, singleConsArray)
	}
	return result
}

// sequenceContexts returns the contexts a step's sequence result holds for
// the next step, as in jsonata-js, where a filter's result is a sequence
// whose items are the contexts: a lone array item stays one context, and
// any other sequence collapses.
func sequenceContexts(seq *Sequence) any {
	if len(seq.Values) == 1 {
		if _, isArr := AsArray(seq.Values[0]); isArr {
			return CollapseToSlice(seq)
		}
	}
	return CollapseSequence(seq)
}

// checkPathStep applies the sequence guardrail to a path step's result. As
// in jsonata-js, a last step returns the result of a lone context as is
// rather than building a sequence. That context is either the step's only
// one, or the only one of several that yields a value (lone).
func checkPathStep(stepInput, result any, singleContext, lone, lastStep bool, env *Environment) error {
	if _, isArr := stepInput.([]any); lastStep && (!isArr || singleContext || lone) {
		return nil
	}
	return checkSequenceLength(result, env)
}

// checkSequenceLength applies the sequence guardrail to a built sequence. A
// constructed array (ConsArray) is one value of its step, as in jsonata-js,
// so it does not count.
func checkSequenceLength(result any, env *Environment) error {
	switch v := result.(type) {
	case []any:
		return env.CheckSequence(len(v))
	case *Sequence:
		return env.CheckSequence(len(v.Values))
	}
	return nil
}

// isNothingFound reports whether a step mapped over a sequence produced an
// empty array that means "nothing found" (undefined). After a direct access
// (not a mapper), the empty array is a genuine field value (e.g.
// obj.emptyList) and must be preserved so that $exists sees it as defined.
// For field-lookup steps (NodeName/NodeString), evalName already
// distinguishes "nothing found" (returns nil) from "field exists with empty
// array value" (returns []any{}), so those step types are skipped.
func isNothingFound(step *parser.Node, result any) bool {
	arr, ok := AsArray(result)
	return ok && len(arr) == 0 && step.Type != parser.NodeName && step.Type != parser.NodeString
}

// unnestSingleton returns the array inside a one-item array of arrays.
func unnestSingleton(v any) any {
	if arr, ok := AsArray(v); ok && len(arr) == 1 {
		if inner, ok := AsArray(arr[0]); ok {
			return inner
		}
	}
	return v
}

// keepSingletonArray applies a path's [] suffix: the result is always an
// array, and an array constructed from a single context stays one item.
func keepSingletonArray(result any, singleConsArray bool) any {
	if arr, ok := AsArray(result); ok && singleConsArray {
		return []any{arr}
	}
	switch v := result.(type) {
	case []any:
		return v
	case ConsArray:
		return []any(v)
	case nil:
		return nil
	}
	return []any{result}
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
	case step.Type == parser.NodeWildcard:
		return consArrayWildcard(input, env)
	}
	return evalPathStep(step, input, env, false, keepSingleton)
}

// consArrayWildcard applies a * step to a constructed array taken as one
// context item: its values are the array's elements, with arrays flattened.
func consArrayWildcard(input any, env *Environment) (any, error) {
	arr, _ := AsArray(input)
	values := make([]any, 0, len(arr))
	for _, item := range arr {
		if nested, ok := item.([]any); ok {
			values = append(values, nested...)
		} else {
			values = append(values, item)
		}
	}
	if err := env.CheckSequence(len(values)); err != nil {
		return nil, err
	}
	switch len(values) {
	case 0:
		return nil, nil
	case 1:
		return values[0], nil
	}
	return values, nil
}

// consArrayGroup applies a {...} step to a constructed array taken as one
// context item, grouping its elements by key like ${...} does.
func consArrayGroup(step *parser.Node, input any, env *Environment) (any, error) {
	items, _ := AsArray(input)
	return groupItems(objectPairs(step.LHS), items, env)
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
	tupleStart := firstBindingStep(node.Steps)

	// finalGroup accumulates the first step-level Group expression encountered.
	// Step-level groups are applied after all contexts have been collected.
	var finalGroup *parser.GroupExpr

	for stepIdx, step := range node.Steps {
		// Some branches below build ctxs without expandTupleStep.
		if err := checkTuples(ctxs); err != nil {
			return nil, err
		}
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
			if step.Index != "" && stepIdx == tupleStart && !nodeHasIndexBinding(step.Left) {
				for k := range sorted {
					sorted[k].env = NewChildEnvironment(sorted[k].env)
					sorted[k].env.Bind(step.Index, float64(k))
				}
			}
			ctxs = sorted
			continue
		}

		// % steps, with any predicates such as %[0], navigate up the parent
		// chain; the predicates then filter the whole parent stream.
		if base, stages := splitTupleStages(evalStep); base.Type == parser.NodeParent {
			stream, err := parentTuples(base, ctxs)
			if err != nil {
				return nil, err
			}
			if ctxs, err = applyTupleStages(stages, stream); err != nil {
				return nil, err
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
					tupleCtxs, err = expandPathTuple(steps, []pathCtx{ctx})
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
				break
			}
			continue
		}

		// When the step is a Block containing a single Path expression,
		// expand the inner path in tuple mode to preserve parent bindings
		// for the % operator (e.g., Account.(Order.Product).{%.OrderID}).
		if steps := blockPathSteps(evalStep); evalStep.Type == parser.NodeBlock && steps != nil {
			for _, ctx := range ctxs {
				expanded, err := expandPathTuple(steps, []pathCtx{ctx})
				if err != nil {
					return nil, err
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

		if base, stages := splitTupleStages(evalStep); stepIdx >= tupleStart && len(stages) > 0 && isPlainTupleBase(base) {
			var err error
			if ctxs, err = evalTupleStages(base, stages, ctxs, node.KeepSingletonArray); err != nil {
				return nil, err
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
			break
		}
	}

	if err := checkTuples(ctxs); err != nil {
		return nil, err
	}

	// Determine which group expression to apply (step-level or path-level).
	grp := finalGroup
	if node.Group != nil {
		grp = node.Group
	}
	if grp != nil {
		return evalTupleGroup(grp, ctxs, env)
	}

	// Collect final values.
	seq := CreateSequence()
	for _, ctx := range ctxs {
		appendToSequence(seq, ctx.value)
	}
	// [] keeps the stream as an array, so a lone array value stays nested.
	if node.KeepSingletonArray && len(seq.Values) > 0 {
		return CollapseToSlice(seq), nil
	}
	return CollapseSequence(seq), nil
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
	if err := checkTuples(ctxs); err != nil {
		return nil, err
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

	prefixCtxs, err := expandPathTuple(steps[:len(steps)-1], []pathCtx{{value: input, env: env}})
	if err != nil {
		return nil, err
	}
	return expandTupleStep(steps[len(steps)-1], prefixCtxs, false)
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
	case ConsArray:
		for j, elem := range rv {
			elem = nilAsNull(elem)
			*nextCtxs = append(*nextCtxs, pathCtx{value: ctxValue(elem), env: bindAt(j, elem)})
		}
	case []any:
		for j, elem := range rv {
			elem = nilAsNull(elem)
			*nextCtxs = append(*nextCtxs, pathCtx{value: ctxValue(elem), env: bindAt(j, elem)})
		}
	case *Sequence:
		collapsed := sequenceContexts(rv)
		if collapsed == nil {
			return
		}
		if arr, ok := AsArray(collapsed); ok {
			for j, elem := range arr {
				elem = nilAsNull(elem)
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
	case ConsArray:
		for j, elem := range rv {
			elem = nilAsNull(elem)
			*nextCtxs = append(*nextCtxs, pathCtx{value: elem, env: bindAt(j, elem)})
		}
	case []any:
		for j, elem := range rv {
			elem = nilAsNull(elem)
			*nextCtxs = append(*nextCtxs, pathCtx{value: elem, env: bindAt(j, elem)})
		}
	case *Sequence:
		collapsed := sequenceContexts(rv)
		if collapsed == nil {
			return
		}
		if arr, ok := AsArray(collapsed); ok {
			for j, elem := range arr {
				elem = nilAsNull(elem)
				*nextCtxs = append(*nextCtxs, pathCtx{value: elem, env: bindAt(j, elem)})
			}
		} else {
			*nextCtxs = append(*nextCtxs, pathCtx{value: collapsed, env: bindAt(0, collapsed)})
		}
	default:
		*nextCtxs = append(*nextCtxs, pathCtx{value: result, env: bindAt(0, result)})
	}
}

// nilAsNull returns an item of data as a value: a nil item is a JSON null
// in data decoded with encoding/json, not undefined.
func nilAsNull(v any) any {
	if v == nil {
		return Null
	}
	return v
}

// expandPathTuple runs a mini tuple walk over the given path steps, starting
// from the given ctxs. It returns the resulting (value, env) pairs, preserving
// #$var bindings and parent context. This is factored out so that evalTupleSort
// can evaluate Sort.Left in tuple mode rather than calling Eval (which would
// discard per-element environments).
func expandPathTuple(steps []*parser.Node, ctxs []pathCtx) ([]pathCtx, error) {
	for _, step := range steps {
		var err error
		if ctxs, err = expandTupleStep(step, ctxs, false); err != nil || len(ctxs) == 0 {
			return nil, err
		}
	}
	return ctxs, nil
}

// expandTupleStep evaluates step against every tuple and flattens the
// results into the next tuple stream, which counts against the sequence
// guardrail.
func expandTupleStep(step *parser.Node, ctxs []pathCtx, keepSingleton bool) ([]pathCtx, error) {
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
	return next, checkTuples(next)
}

// evalTupleGroup evaluates a group expression against a tuple context list.
//
// JSONata group-by semantics: records are grouped by key, then the value
// expression is evaluated once per group with the context set to the array
// of all group members (or a single value when the group has one member).
// This allows aggregate functions like $join or $sum to operate on the
// full group rather than individual records.
func evalTupleGroup(group *parser.GroupExpr, ctxs []pathCtx, pathEnv *Environment) (any, error) {
	result := NewOrderedMap()

	for _, pair := range group.Pairs {
		// Phase 1: group ctxs by key.
		type groupEntry struct {
			values []any
			envs   []*Environment
			size   appendCount
		}
		var keyOrder []string
		groups := map[string]*groupEntry{}

		for _, ctx := range ctxs {
			keyVal, err := Eval(pair[0], ctx.value, ctx.env)
			if err != nil {
				return nil, err
			}
			if keyVal == nil {
				continue
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
			if err := g.size.add(ctx.value, ctx.env); err != nil {
				return nil, err
			}
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
				var err error
				if groupEnv, err = mergeGroupEnvs(g.envs, pathEnv); err != nil {
					return nil, err
				}
			}
			val, err := Eval(pair[1], groupCtx, groupEnv)
			if err != nil {
				return nil, err
			}
			if seq, ok := val.(*Sequence); ok {
				val = CollapseSequence(seq)
			}
			if val == nil {
				continue
			}
			result.Set(key, val)
		}
	}

	return result, nil
}

// mergeGroupEnvs creates a merged environment for a group of records: each
// variable the records' tuples bind below pathEnv, the environment the path
// was evaluated in, holds their values appended as jsonata-js does.
func mergeGroupEnvs(envs []*Environment, pathEnv *Environment) (*Environment, error) {
	if len(envs) == 0 {
		return nil, nil
	}
	if len(envs) == 1 {
		return envs[0], nil
	}
	// Find the common ancestor to use as parent of the merged env.
	merged := NewChildEnvironment(envs[0].Parent())
	merged.decimalPrecision = envs[0].decimalPrecision

	varNames := map[string]struct{}{}
	for _, env := range envs {
		for e := env; e != nil && e != pathEnv; e = e.Parent() {
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
			// jsonata-js appends each tuple's value with $append.
			var appended []any
			for _, v := range vals {
				appended = append(appended, AppendItems(v)...)
				if err := merged.CheckSequence(len(appended)); err != nil {
					return nil, err
				}
			}
			merged.Bind(name, appended)
		}
	}
	return merged, nil
}

// evalPathStep is evalPathStepLone without its lone result.
func evalPathStep(
	step *parser.Node, input any, env *Environment, prevWasMapper, keepSingletonArray bool,
) (any, error) {
	result, _, err := evalPathStepLone(step, input, env, prevWasMapper, keepSingletonArray)
	return result, err
}

// evalPathStepLone evaluates a single path step against input.
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
// lone reports that exactly one context yielded a value and that the value
// is an array. As a path's last step, jsonata-js returns it as is rather
// than building a sequence.
func evalPathStepLone(
	step *parser.Node, input any, env *Environment, prevWasMapper, keepSingletonArray bool,
) (result any, lone bool, _ error) {
	// Steps that already handle array inputs natively (field lookup, wildcard,
	// descendant, variable, literals, sort) are delegated directly.
	switch step.Type {
	case parser.NodeNumber:
		// S0213: a numeric literal is not a valid path step (use [n] subscript notation instead).
		return nil, false, &JSONataError{Code: "S0213", Token: step.Value, Message: "invalid step in path: numeric literal is not a field name"}
	case parser.NodeName:
		// The path checks each step's result against the sequence
		// guardrail, so a field step skips evalBoundedName's check.
		if step.Group == nil {
			return evalNameLone(step, input)
		}
		return noLone(Eval(step, input, env))
	case parser.NodeWildcard:
		// A step maps * over its contexts, except a first step over the
		// root input array, which jsonata-js wraps as one context.
		if arr, ok := input.([]any); ok && step.Group == nil && (prevWasMapper || !isRootArray(arr, env)) {
			return noLone(evalWildcardStep(arr, env))
		}
		return noLone(Eval(step, input, env))
	case parser.NodeSort: // Sort steps must be applied to the full accumulated input, not mapped per-element.
		return noLone(evalSortStep(step, input, env))
	case parser.NodeVariable, parser.NodeString, parser.NodeValue:
		return noLone(Eval(step, input, env))
	case parser.NodeBlock:
		// A block not preceded by a mapping step (prevWasMapper=false) should receive
		// the full input so that $^(age) inside can sort the whole array.
		// A block preceded by a mapper is handled by per-element fallthrough below.
		if !prevWasMapper {
			return noLone(Eval(step, input, env))
		}
	case parser.NodeDescendant:
		return noLone(evalPathStepDescendant(input, env))
	case parser.NodeBinary:
		// Subscript steps map per-element when preceded by a mapping step (prevWasMapper=true).
		// When NOT preceded by a mapper, apply the subscript to the whole collected array.
		if step.Value == "[" && step.Left != nil && !prevWasMapper {
			return noLone(evalSubscriptStep(step, input, env))
		}
	}

	// Array constructor steps ([...]) that are NOT preceded by a mapping step
	// are literal expressions that should be evaluated once (e.g. [1,2,3].$).
	// When preceded by a mapper (prevWasMapper=true), they are mapped per-element.
	if step.Type == parser.NodeUnary && step.Value == "[" && !prevWasMapper {
		return noLone(Eval(step, input, env))
	}

	arr, ok := pathStepArray(input)
	if !ok {
		if step.Type == parser.NodeFunction {
			return noLone(evalPathFunctionStep(step, input, env))
		}
		return noLone(Eval(step, input, env))
	}

	return mapPathStep(step, arr, env, keepSingletonArray)
}

// mapPathStep evaluates step against each item of arr, flattening the results
// into one sequence.
func mapPathStep(step *parser.Node, arr []any, env *Environment, keepSingletonArray bool) (result any, lone bool, _ error) {
	// isGroupStep is true for .[...] — the array-constructor group step.
	// Group steps produce one array per input element that must NOT be flattened;
	// each per-element result is kept as a nested array in the output sequence.
	isGroupStep, seq, evalItem := step.Type == parser.NodeUnary && step.Value == "[", CreateSequence(), Eval
	switch {
	case step.Type == parser.NodeFunction:
		evalItem = evalPathFunctionStep
	case isSubscript(step):
		evalItem = evalSubscriptStep
	}
	contributed := 0
	for _, item := range arr {
		val, err := evalItem(step, item, env)
		if err != nil {
			return nil, false, err
		}
		if seq, ok := val.(*Sequence); ok && len(seq.Values) == 0 {
			// jsonata-js evaluates an empty sequence result to undefined.
			continue
		}
		if val == nil {
			continue
		}
		contributed++
		_, lone = AsArray(val)
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
		return nil, false, nil
	}
	if isGroupStep && keepSingletonArray {
		// With [] (keepSingletonArray), prevent singleton collapse so that a
		// path like $.[v,e][] with 1 input element returns [[v,e]] not [v,e].
		return seq.Values, false, nil
	}
	return CollapseSequence(seq), lone && contributed == 1, nil
}

func noLone(value any, err error) (result any, lone bool, _ error) {
	return value, false, err
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

// stepHasBinding reports whether a step, or any subscript it is predicated
// on, carries a #$var or @$var binding, as in X#$i[p1][p2].
func stepHasBinding(step *parser.Node) bool {
	for n := step; n != nil; n = n.Left {
		if n.Index != "" || n.Focus != "" {
			return true
		}
		if n.Type != parser.NodeBinary || n.Value != "[" {
			return false
		}
	}
	return false
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
// evalTupleStages can expand per context. Groups keep their dedicated
// handling, and so does a base referencing % unless it carries an @$v
// binding, which only evalTupleStages binds.
func isPlainTupleBase(base *parser.Node) bool {
	switch base.Type {
	case parser.NodeName, parser.NodeString, parser.NodeWildcard, parser.NodeDescendant,
		parser.NodeBlock, parser.NodeFunction, parser.NodeVariable, parser.NodeUnary:
		return base.Group == nil && (base.Focus != "" || !nodeHasParentRef(base))
	}
	return false
}

// evalTupleStages expands base for every context, then applies each stage
// to the whole resulting tuple stream rather than per context, as
// jsonata-js does once a path is a tuple stream: Product[0]#$i keeps only
// the first product overall, and a stage #$i numbers the filtered stream.
func evalTupleStages(base *parser.Node, stages []tupleStage, ctxs []pathCtx, keepSingleton bool) ([]pathCtx, error) {
	stream, err := expandTupleStep(base, ctxs, keepSingleton)
	if err != nil {
		return nil, err
	}
	return applyTupleStages(stages, stream)
}

// checkTuples applies the sequence guardrail to a tuple stream; every tuple
// env shares the evaluation's limit.
func checkTuples(stream []pathCtx) error {
	if len(stream) == 0 {
		return nil
	}
	return stream[0].env.CheckSequence(len(stream))
}

// applyTupleStages filters a tuple stream by each stage in turn, binding a
// stage's #$var to the positions of the filtered stream.
func applyTupleStages(stages []tupleStage, stream []pathCtx) ([]pathCtx, error) {
	for _, stage := range stages {
		var err error
		if stream, err = filterTupleStream(stage.predicate, stream); err != nil {
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
func filterTupleStream(predicate *parser.Node, stream []pathCtx) ([]pathCtx, error) {
	var kept []pathCtx
	for pos, ctx := range stream {
		if err := ctx.env.Err(); err != nil {
			return nil, err
		}
		res, err := Eval(predicate, ctx.value, ctx.env)
		if err != nil {
			return nil, err
		}
		matches, positional, err := positionMatches(res, pos, len(stream))
		if err != nil {
			return nil, err
		}
		if !positional && ToBoolean(res) {
			matches = 1
		}
		if err := ctx.env.CheckSequence(len(kept) + matches); err != nil {
			return nil, err
		}
		for range matches {
			kept = append(kept, ctx)
		}
	}
	return kept, nil
}

// positionMatches counts how many of the positions a predicate result
// selects equal pos; positional is false unless the result is a number or a
// non-empty array of numbers.
func positionMatches(res any, pos, length int) (matches int, positional bool, _ error) {
	indices, isArr := res.([]any)
	if !isArr {
		indices = []any{res}
	} else if len(indices) == 0 {
		return 0, false, nil
	}
	resolved, ok, err := resolveIndices(indices, length)
	if err != nil || !ok {
		return 0, false, err
	}
	for _, i := range resolved {
		if i == pos {
			matches++
		}
	}
	return matches, true, nil
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
// Array values are flattened one level, as jsonata-js does, except an array
// built by the preceding array-constructor step: $ returns that item itself,
// which stays one value (a.[$].$ is [[1],[2]]). A last step with a single
// array result returns that array unchanged.
func evalVariableStep(step *parser.Node, input any, env *Environment, consItems, lastStep bool) (result any, lone bool, _ error) {
	items, ok := input.([]any)
	if !ok {
		return noLone(Eval(step, input, env))
	}
	results := make([]any, 0, len(items))
	for _, item := range items {
		val, err := Eval(step, item, env)
		if err != nil {
			return nil, false, err
		}
		if val != nil {
			results = append(results, val)
		}
	}
	if len(results) == 1 && lastStep {
		if arr, ok := results[0].([]any); ok {
			return arr, true, nil
		}
	}
	keepItems := consItems && step.Value == ""
	seq := CreateSequence()
	for _, val := range results {
		if arr, ok := val.([]any); ok && !keepItems {
			seq.Values = append(seq.Values, arr...)
			continue
		}
		appendToSequence(seq, val)
	}
	if len(seq.Values) == 0 {
		return nil, false, nil
	}
	return CollapseSequence(seq), false, nil
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

// evalPathStepDescendant evaluates a ** path step. It includes the input
// itself, so that a later step can match at the current level too.
func evalPathStepDescendant(input any, env *Environment) (any, error) {
	seq := descendantLookup(input)
	if err := env.CheckSequence(len(seq.Values)); err != nil {
		return nil, err
	}
	if len(seq.Values) == 0 {
		return nil, nil
	}
	return seq, nil
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

// descendantLookup collects input and every value nested in it, as the **
// operator does in jsonata-js: arrays are walked into but are not values
// themselves.
func descendantLookup(input any) *Sequence {
	seq := CreateSequence()
	if input != nil {
		appendDescendants(input, seq)
	}
	return seq
}

func appendDescendants(v any, seq *Sequence) {
	if arr, ok := AsArray(v); ok {
		for _, item := range arr {
			appendDescendants(item, seq)
		}
		return
	}
	v = nilAsNull(v)
	appendToSequence(seq, v)
	if IsMap(v) {
		MapRange(v, func(_ string, val any) bool {
			appendDescendants(val, seq)
			return true
		})
	}
}
