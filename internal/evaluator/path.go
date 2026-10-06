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
// When any step binds a #$var, @$var or ancestor, it switches to tuple-aware
// evaluation so the binding remains visible in subsequent steps.
func evalPath(node *parser.Node, input any, env *Environment) (any, error) {
	if pathHasTupleStep(node.Steps) {
		return evalPathTuple(node, input, env)
	}
	return evalPathSimple(node, input, env)
}

// pathHasTupleStep reports whether any step binds a #$var, @$var or
// ancestor, so the path runs as a tuple stream (see stepHasBinding).
func pathHasTupleStep(steps []*parser.Node) bool {
	return slices.ContainsFunc(steps, stepHasBinding)
}

// evalPathSimple is step-by-step path evaluation for paths with no index,
// focus or ancestor bindings.
func evalPathSimple(node *parser.Node, input any, env *Environment) (any, error) {
	result, _, err := evalPathSimpleLast(node, input, env)
	return result, err
}

// evalPathSimpleLast is evalPathSimple, also returning the last step's
// input (see argShape).
func evalPathSimpleLast(node *parser.Node, input any, env *Environment) (result, lastIn any, _ error) {
	result = input
	prevWasMapper := false
	for i, step := range node.Steps {
		if i > 0 && result == nil {
			return nil, nil, nil
		}
		if seq, ok := result.(*Sequence); ok {
			result = flattenKept(CollapseSequence(seq))
			if i > 0 && result == nil {
				return nil, nil, nil
			}
		}

		var err error
		lastIn = result
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
			return nil, nil, err
		}
		result = flattenKept(spreadSortIndexStage(step, result))
		// Mapping over a one-item array nests a constructed array for [];
		// unnest it so later steps see it whole and it is wrapped once.
		if prevWasMapper && node.KeepSingletonArray {
			result = unnestCons(result)
		}
		if _, consItem := result.(ConsArray); !consItem && prevWasMapper && isNothingFound(step, result) {
			return nil, nil, nil
		}
		_, isArr := result.([]any)
		_, isSeq := result.(*Sequence)
		prevWasMapper = isArr || isSeq
	}

	if seq, ok := result.(*Sequence); ok {
		result = CollapseSequence(seq)
	}
	if node.KeepSingletonArray {
		return keepSingletonArray(result), lastIn, nil
	}
	return result, lastIn, nil
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

// evalPathTuple evaluates a path that binds #$var, @$var or an ancestor by
// walking it as a tuple stream (see walkPathTuple), then applies its group
// or collects the tuples' values.
func evalPathTuple(node *parser.Node, input any, env *Environment) (any, error) {
	ctxs, rawContext, started, err := walkPathTuple(node, inputTuples(node, input, env), env, streamOwn)
	if err != nil {
		return nil, err
	}

	if grp := node.Group; grp != nil {
		// jsonata-js groups a path that emptied before its tuple stream
		// started, or a stream sorted into raw tuples, as plain items.
		switch {
		case !started:
			return groupItems(groupPairs(grp.Pairs), nil, env)
		case rawContext != nil:
			items := make([]any, len(ctxs))
			for i, ctx := range ctxs {
				items[i], _ = rawContext(ctx)
			}
			return groupItems(groupPairs(grp.Pairs), items, env)
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

// inputTuples returns the tuples a tuple path starts from. As in jsonata-js
// evaluatePath, each item of an array input is a tuple unless the first
// step is a variable, or the input is the root array jsonata-js wraps as
// one item (see markRootContext).
func inputTuples(node *parser.Node, input any, env *Environment) []pathCtx {
	first := parser.StepBase(node.Steps[0])
	items, ok := AsArray(input)
	if !ok || first.Type == parser.NodeVariable || first.RootContext && isRootInput(items, env) {
		return []pathCtx{{value: input, env: env}}
	}
	ctxs := make([]pathCtx, len(items))
	for i, item := range items {
		ctxs[i] = pathCtx{value: item, env: env}
	}
	return ctxs
}

// streamPos says where a walked path sits relative to the tuple stream.
type streamPos int

const (
	streamOwn     streamPos = iota // the path's own binding step starts it, if any
	streamPending                  // it starts at or after the path, as for a sort's Left
	streamRunning                  // it started before the path
)

// walkPathTuple runs a path's steps over a tuple stream of (value, env)
// contexts, so a #$var, @$var or ancestor bound at one step stays visible in
// later steps. An index binds the position in the step's output, as in
// jsonata-js. A step with its own group, as a leading block can have, is
// evaluated whole, group included; the path's group is left to the caller.
//
// It returns the resulting contexts. rawContext is non-nil when the last
// step was a sort that jsonata-js leaves as a plain array of tuples (see
// rawTupleContext): a group then sees raw tuple objects. started reports
// whether the walk reached the step that starts the tuple stream.
func walkPathTuple( //nolint:gocyclo,funlen // dispatch
	node *parser.Node, ctxs []pathCtx, env *Environment, state streamPos,
) (_ []pathCtx, rawContext tupleContext, started bool, _ error) {
	tupleStart := firstBindingStep(node.Steps)
	if state == streamRunning {
		tupleStart = -1
	}
	hasBindingStep := tupleStart < len(node.Steps) || state == streamPending

	started = state == streamRunning
	for stepIdx, step := range node.Steps {
		if err := env.Err(); err != nil {
			return nil, nil, false, err
		}
		var nextCtxs []pathCtx
		rawContext = nil
		// jsonata-js evaluates the steps before the first binding as plain
		// steps; the binding step starts the tuple stream.
		beforeStream := hasBindingStep && stepIdx < tupleStart
		startsStream := hasBindingStep && stepIdx == tupleStart
		inStream := hasBindingStep && stepIdx > tupleStart
		started = started || startsStream

		// A step's own group, which only a leading step can have, applies
		// to the step's result, so the step is evaluated whole per tuple.
		if step.Group != nil {
			var err error
			if ctxs, err = mapTupleStep(step, ctxs, stepIdx == 0, node.KeepSingletonArray, beforeStream); err != nil {
				return nil, nil, false, err
			}
			if len(ctxs) == 0 {
				break
			}
			continue
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
			sorted, sortedRaw, sortStarted, err := evalTupleSort(sortStep, ctxs, env, leftState, raw)
			if err != nil {
				return nil, nil, false, err
			}
			if startsStream {
				started = sortStarted
				if !sortStep.Tuple {
					bindSortIndex(sortStep, sorted)
				}
			}
			// jsonata-js sorts an existing tuple stream of several tuples into
			// a plain array of tuples, so its predicates see each raw tuple
			// object rather than its value and bindings.
			var contextOf tupleContext
			if sortedRaw {
				contextOf = raw
			}
			rawContext = contextOf
			if ctxs, err = applyTupleStages(stages, sorted, contextOf); err != nil {
				return nil, nil, false, err
			}
			continue
		}

		// A block a % reaches into yields its last expression's tuples,
		// which carry the bindings made inside; predicates on the block
		// then filter the whole stream.
		if base, stages := splitTupleStages(step); base.Type == parser.NodeBlock && base.TupleResult {
			var stream []pathCtx
			for _, ctx := range ctxs {
				var err error
				if stream, err = appendBlockTuples(stream, base, ctx); err != nil {
					return nil, nil, false, err
				}
			}
			var err error
			if ctxs, err = applyTupleStages(stages, stream, nil); err != nil {
				return nil, nil, false, err
			}
			if len(ctxs) == 0 {
				break
			}
			continue
		}

		// Subscript step whose Left has a Focus binding (join operator @):
		// e.g., Contact@$c[$c.ssn = $e.SSN]. We evaluate the Left to get
		// elements, bind each to $focus_var, then apply the predicate with
		// access to both the focus variable and previously bound variables.
		if step.Type == parser.NodeBinary && step.Value == "[" &&
			step.Left != nil && isJoinStep(step.Left) {
			predicate := step.Right
			leftNode := step.Left
			focusVar := leftNode.Focus
			indexVar := leftNode.Index
			postFilterIndex := step.Index
			var err error
			if nextCtxs, err = evalJoinFilter(ctxs, nextCtxs, leftNode, predicate, focusVar, indexVar); err != nil {
				return nil, nil, false, err
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
		if step.Type == parser.NodeBinary && step.Value == "[" &&
			step.Left != nil && step.Left.Type == parser.NodeBinary && step.Left.Value == "[" &&
			step.Left.Left != nil && isJoinStep(step.Left.Left) {
			// Process the inner join-filter as if it were a standalone step.
			innerStep := step.Left
			predicate := innerStep.Right
			leftNode := innerStep.Left
			focusVar := leftNode.Focus
			indexVar := leftNode.Index
			var err error
			if nextCtxs, err = evalJoinFilter(ctxs, nextCtxs, leftNode, predicate, focusVar, indexVar); err != nil {
				return nil, nil, false, err
			}

			// Apply the outer subscript to the collected tuples.
			outerExpr := step.Right
			if len(nextCtxs) > 0 {
				outerResult, err := Eval(outerExpr, nextCtxs[0].value, nextCtxs[0].env)
				if err != nil {
					return nil, nil, false, err
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

		if base, stages := splitTupleStages(step); (startsStream || inStream) && len(stages) > 0 && !isJoinStep(base) {
			var err error
			if ctxs, err = evalTupleStages(base, stages, ctxs, node.KeepSingletonArray); err != nil {
				return nil, nil, false, err
			}
			if len(ctxs) == 0 {
				break
			}
			continue
		}

		var err error
		if ctxs, err = mapTupleStep(step, ctxs, stepIdx == 0, node.KeepSingletonArray, beforeStream); err != nil {
			return nil, nil, false, err
		}
		if len(ctxs) == 0 {
			break
		}
	}

	if err := env.Err(); err != nil {
		return nil, nil, false, err
	}
	return ctxs, rawContext, started, nil
}

// mapTupleStep evaluates step against each tuple's value, flattening its
// results into the next tuples (see appendTupleResults). An undefined value
// is skipped except at a path's first step.
func mapTupleStep(step *parser.Node, ctxs []pathCtx, first, keepSingleton, beforeStream bool) ([]pathCtx, error) {
	var next []pathCtx
	for _, ctx := range ctxs {
		val := ctx.value
		if seq, ok := val.(*Sequence); ok {
			val = CollapseSequence(seq)
		}
		if val == nil && !first {
			continue
		}
		result, err := evalTupleContextStep(step, val, ctx.env, keepSingleton, beforeStream)
		if err != nil {
			return nil, err
		}
		if result != nil {
			appendTupleResults(step, result, ctx.value, ctx.env, &next)
		}
	}
	return next, nil
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
func evalTupleSort(
	step *parser.Node, ctxs []pathCtx, env *Environment, state streamPos, raw tupleContext,
) (sorted []pathCtx, rawResult, started bool, _ error) {
	inStream := state == streamRunning
	// A Left of $ sorts a running stream's tuples in place; anywhere else
	// it navigates like any Left, so the input's items are sorted.
	needsNavigation := step.Left != nil &&
		(!inStream || step.Left.Type != parser.NodeVariable || step.Left.Value != "")

	var leftSort *parser.Node
	var leftStages []tupleStage
	if needsNavigation {
		left := step.Left
		if parser.IsStepPath(left) {
			left = left.Steps[0]
		}
		leftSort, leftStages = splitTupleStages(left)
	}
	var termContext tupleContext
	inputRaw := false
	started = true
	if leftSort != nil && leftSort.Type == parser.NodeSort {
		inner, innerRaw, innerStarted, err := evalTupleSort(leftSort, ctxs, env, state, raw)
		if err != nil {
			return nil, false, false, err
		}
		if !inStream && !leftSort.Tuple {
			bindSortIndex(leftSort, inner)
		}
		if innerRaw {
			termContext = raw
		}
		inputRaw, started = innerRaw, innerStarted
		if ctxs, err = applyTupleStages(leftStages, inner, termContext); err != nil {
			return nil, false, false, err
		}
	} else if needsNavigation {
		// Expand: navigate via step.Left for each ctx, collect all (child, childEnv) tuples.
		// A Left path runs as a tuple walk so that index variables survive into the
		// sorted output; a plain Eval would discard the per-element environments.
		var expanded []pathCtx
		switch {
		case step.Left.Type == parser.NodePath && len(step.Left.Steps) > 0:
			var err error
			if expanded, _, started, err = walkPathTuple(step.Left, ctxs, env, state); err != nil {
				return nil, false, false, err
			}
		default:
			for _, ctx := range ctxs {
				result, err := Eval(step.Left, ctx.value, ctx.env)
				if err != nil {
					return nil, false, false, err
				}
				if result == nil {
					continue
				}
				appendTupleResults(step.Left, result, ctx.value, ctx.env, &expanded)
			}
		}
		ctxs = expanded
	}
	// A sort whose Left binds nothing starts any stream itself, which
	// jsonata-js never reaches when that Left yields nothing.
	if !inStream && !step.Tuple {
		started = len(ctxs) > 0
	}
	// jsonata-js returns a stream of at most one tuple as is, still a
	// stream, and sorts a longer one into raw tuples.
	rawResult = inputRaw || (inStream || step.Tuple) && len(ctxs) > 1

	if len(step.Terms) == 0 {
		return ctxs, rawResult, started, nil
	}

	sorted = slices.Clone(ctxs)

	if err := SortItemsErr(sorted, func(a, b pathCtx) (int, error) {
		aVal, aEnv, bVal, bEnv := a.value, a.env, b.value, b.env
		if termContext != nil {
			aVal, aEnv = termContext(a)
			bVal, bEnv = termContext(b)
		}
		return compareSortTerms(step.Terms, aVal, bVal, aEnv, bEnv)
	}); err != nil {
		return nil, false, false, err
	}
	return sorted, rawResult, started, nil
}

// bindSortIndex binds a sort step's #$var to the sorted positions, for a sort
// that starts the tuple stream.
func bindSortIndex(step *parser.Node, sorted []pathCtx) {
	if step.Index == "" {
		return
	}
	for k := range sorted {
		sorted[k].env = NewChildEnvironment(sorted[k].env)
		sorted[k].env.tuple = true
		sorted[k].env.Bind(step.Index, float64(k))
	}
}

// newTupleEnv returns the environment for a tuple a step yields from the
// context value input, binding the step's ancestor slot, if any, to input.
func newTupleEnv(step *parser.Node, input any, env *Environment) *Environment {
	e := NewChildEnvironment(env)
	e.tuple = true
	if step.Ancestor != nil {
		e.Bind(step.Ancestor.Label, input)
	}
	return e
}

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
			childEnv := newTupleEnv(leftNode, ctx.value, ctx.env)
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
// step.Focus to each element (for @$var join bindings), and step's ancestor
// slot, if any, to the context value parentValue.
//
// When step.Focus is set (join operator @), the context VALUE stays at the parent
// level rather than advancing to the result element. This implements lateral-join
// semantics: the variable captures each element, but subsequent path steps continue
// navigating from the parent context.
func appendTupleResults(step *parser.Node, result, parentValue any, parentEnv *Environment, nextCtxs *[]pathCtx) {
	isJoin := step.Focus != ""

	bindAt := func(j int, elem any) *Environment {
		e := newTupleEnv(step, parentValue, parentEnv)
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

// appendBlockTuples appends to stream the tuples a block a % reaches into
// yields for ctx: jsonata-js merges each tuple the block's last expression
// returns into ctx's tuple, while a plain value's items become tuples as
// for any step.
func appendBlockTuples(stream []pathCtx, block *parser.Node, ctx pathCtx) ([]pathCtx, error) {
	val := ctx.value
	if seq, ok := val.(*Sequence); ok {
		val = CollapseSequence(seq)
	}
	if val == nil {
		return stream, nil
	}
	tuples, result, err := tupleResult(block, val, ctx.env)
	if err != nil {
		return nil, err
	}
	if result != nil {
		appendTupleResults(block, result, ctx.value, ctx.env, &stream)
	}
	return append(stream, tuples...), nil
}

// tupleResult evaluates node, a block or path a % reaches into, against
// val. A path yields its tuples (see inputTuples); with a group it yields
// the grouped value instead, as does any expression a % passed over.
func tupleResult(node *parser.Node, val any, env *Environment) (tuples []pathCtx, result any, _ error) {
	switch {
	case node.Type == parser.NodeBlock && node.TupleResult && node.Group == nil:
		last := len(node.Expressions) - 1
		blockEnv := env
		if last > 0 {
			blockEnv = NewChildEnvironment(env)
			for _, expr := range node.Expressions[:last] {
				if _, err := Eval(expr, val, blockEnv); err != nil {
					return nil, nil, err
				}
			}
		}
		tuples, result, err := tupleResult(node.Expressions[last], val, blockEnv)
		if err != nil || tuples == nil {
			return nil, settleRaw(result, node.KeepArray), err
		}
		if blockEnv != env {
			if err := rebaseTuples(tuples, blockEnv, env); err != nil {
				return nil, nil, err
			}
		}
		return tuples, nil, nil
	case node.Type == parser.NodePath && node.TupleResult && node.Group == nil:
		tuples, _, _, err := walkPathTuple(node, inputTuples(node, val, env), env, streamOwn)
		return tuples, nil, err
	}
	result, err := Eval(node, val, env)
	return nil, result, err
}

// rebaseTuples moves the bindings each tuple holds below from onto a child
// of env: the variables a block binds stay inside it, as jsonata-js keeps
// them in the block's frame rather than in its tuples. Every tuple's env
// descends from from. Nested blocks copy the bindings again at each level,
// as jsonata-js does, so the deadline is polled per tuple.
func rebaseTuples(tuples []pathCtx, from, env *Environment) error {
	for i, tuple := range tuples {
		if err := env.Err(); err != nil {
			return err
		}
		var chain []*Environment
		for e := tuple.env; e != from; e = e.Parent() {
			chain = append(chain, e)
		}
		rebased := NewChildEnvironment(env)
		rebased.tuple = true
		for _, e := range slices.Backward(chain) {
			e.Range(func(name string, val any) { rebased.Bind(name, val) })
		}
		tuples[i].env = rebased
	}
	return nil
}

// evalTupleGroup evaluates a group expression against a tuple stream (see
// groupBy): each tuple is keyed in its own environment, and a group's value
// sees its tuples' bindings merged.
func evalTupleGroup(group *parser.GroupExpr, ctxs []pathCtx) (any, error) {
	values := make([]any, len(ctxs))
	envs := make([]*Environment, len(ctxs))
	for i, ctx := range ctxs {
		values[i], envs[i] = ctx.value, ctx.env
	}
	return groupBy(groupPairs(group.Pairs), values, envs, nil)
}

// mergeGroupEnvs creates a merged environment for a group of records, as
// jsonata-js reduceTupleStream does: each variable a record binds holds
// every record's value, folded as fn.append does (see groupContext).
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

	// Collect variable names from tuple-specific envs only (stop at the
	// first env that is not a tuple's: a shared ancestor with built-in
	// bindings).
	varNames := map[string]struct{}{}
	for _, env := range envs {
		for e := env; e != nil && e.tuple; e = e.Parent() {
			e.Range(func(name string, _ any) {
				varNames[name] = struct{}{}
			})
		}
	}

	// For each variable, collect values from each env via Lookup (full chain).
	merged.tuple = true
	for name := range varNames {
		var vals []any
		for _, env := range envs {
			if v, ok := env.Lookup(name); ok {
				vals = append(vals, v)
			}
		}
		merged.Bind(name, groupContext(vals))
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
	// A leading step's own group, as on (a){k: v} or *{k: v}, groups the
	// step's result.
	if step.Group != nil {
		return Eval(step, input, env)
	}
	// Steps that already handle array inputs natively (field lookup, wildcard,
	// descendant, variable, literals, sort) are delegated directly.
	switch step.Type {
	case parser.NodeWildcard:
		return evalPathStepWildcard(step, input, env)
	case parser.NodeName, parser.NodeNumber,
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
// carries a #$var, @$var or ancestor binding, is a block a % reaches into,
// or is a sort whose Left binds any of these.
func stepHasBinding(step *parser.Node) bool {
	if sortLeftHasBinding(step) {
		return true
	}
	for n := step; n != nil; n = n.Left {
		if n.Index != "" || n.Focus != "" || n.Ancestor != nil || n.TupleResult {
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

// isJoinStep reports whether a predicated step's base is a join, binding an
// @$var on a step other than %, which keeps its dedicated handling. Any
// other base's predicates filter the tuple stream (see evalTupleStages);
// a sort's are applied by the sort branch, and a predicated base cannot
// carry a group of its own (S0209).
func isJoinStep(base *parser.Node) bool {
	return base.Focus != "" && base.Type != parser.NodeParent
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
	case step.Type == parser.NodeWildcard && step.Group == nil:
		result, err = evalWildcard(step, val, env)
	case beforeStream:
		result, err = evalPathStep(step, val, env, false, keepSingleton, false)
	default:
		return evalTupleStep(step, val, env)
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

// evalTupleStep evaluates a step of a running tuple stream once against the
// tuple's value, as jsonata-js evaluateTupleStep does: a call on an array
// value sees the whole array, where a plain step would map over its items.
func evalTupleStep(step *parser.Node, val any, env *Environment) (any, error) {
	switch {
	case step.Type == parser.NodeFunction && step.Group == nil:
		return evalPathFunctionStep(step, val, env)
	case step.Type == parser.NodeDescendant:
		return evalPathStepDescendant(val, env)
	}
	return Eval(step, val, env)
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
		// A block a % reaches into carries its last expression's bindings.
		if n.Type == parser.NodeBlock && n.TupleResult {
			walk(n.Expressions[len(n.Expressions)-1])
		}
		// jsonata-js adds a step's focus to the tuple, then its index and
		// ancestor.
		var ancestor string
		if n.Ancestor != nil {
			ancestor = n.Ancestor.Label
		}
		for _, name := range []string{n.Focus, n.Index, ancestor} {
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
