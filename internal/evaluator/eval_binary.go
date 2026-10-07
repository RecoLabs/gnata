package evaluator

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/recolabs/gnata/internal/decimal"
	"github.com/recolabs/gnata/internal/parser"
)

func evalBinary(node *parser.Node, input any, env *Environment) (any, error) { //nolint:gocyclo,funlen // dispatch
	switch node.Value {
	case "and":
		left, err := Eval(node.Left, input, env)
		if err != nil {
			return nil, err
		}
		truthy, err := ToBooleanEnv(left, env)
		if err != nil {
			return nil, err
		}
		if !truthy {
			return false, nil
		}
		right, err := Eval(node.Right, input, env)
		if err != nil {
			return nil, err
		}
		return ToBooleanEnv(right, env)

	case "or":
		left, err := Eval(node.Left, input, env)
		if err != nil {
			return nil, err
		}
		truthy, err := ToBooleanEnv(left, env)
		if err != nil {
			return nil, err
		}
		if truthy {
			return true, nil
		}
		right, err := Eval(node.Right, input, env)
		if err != nil {
			return nil, err
		}
		return ToBooleanEnv(right, env)

	case "?:":
		// Elvis / default: return left if ToBoolean(left) is true, else right.
		left, result, err := evalDefaultLeft(node, input, env)
		if err != nil {
			return nil, err
		}
		truthy, err := ToBooleanEnv(left, env)
		if err != nil {
			return nil, err
		}
		if !truthy {
			return evalSettled(node.Right, input, env, false)
		}
		return result, nil

	case "??":
		// Null-coalescing: return left if not null/undefined, else right.
		left, result, err := evalDefaultLeft(node, input, env)
		if err != nil {
			return nil, err
		}
		if left == nil {
			return evalSettled(node.Right, input, env, false)
		}
		return result, nil

	case "~>":
		// Chain/pipe: pass left as the first argument to the right-hand function.
		// When the right is a function call with existing args (e.g. $map(fn)),
		// prepend left to those args so arr ~> $map(fn) becomes $map(arr, fn).
		result, _, err := evalShapedChain(node, input, env)
		return result, err

	case "[":
		// Subscript / filter: left[right]
		return evalSubscript(node, input, env)
	}

	// For most binary operators, evaluate both sides first.
	left, err := Eval(node.Left, input, env)
	if err != nil {
		return nil, err
	}
	right, err := Eval(node.Right, input, env)
	if err != nil {
		return nil, err
	}

	switch node.Value {
	case "+", "-", "*", "/", "%", "**":
		op := node.Value

		if lf, ok := left.(float64); ok && env.DecimalPrecision() == 0 {
			if rf, ok2 := right.(float64); ok2 {
				return evalArithFloat64(lf, rf, op)
			}
		}

		if left != nil {
			if _, ok := ToFloat64(left); !ok {
				return nil, &JSONataError{Code: "T2001", Message: fmt.Sprintf("the left operand of the %q operator must evaluate to a number", op)}
			}
		}
		if right != nil {
			if _, ok := ToFloat64(right); !ok {
				return nil, &JSONataError{Code: "T2002", Message: fmt.Sprintf("the right operand of the %q operator must evaluate to a number", op)}
			}
		}
		if left == nil || right == nil {
			return nil, nil
		}
		if prec := env.DecimalPrecision(); prec > 0 {
			res, err := DecimalArith(left, right, op, prec)
			switch {
			case err == nil:
				return res, nil
			case errors.Is(err, decimal.ErrOverflow):
				return nil, &JSONataError{Code: "D1001", Message: "Number out of range"}
			}
		}
		l, _ := ToFloat64(left)
		r, _ := ToFloat64(right)
		return evalArithFloat64(l, r, op)

	case "&":
		ls, err := stringifyValue(left, env)
		if err != nil {
			return nil, err
		}
		rs, err := stringifyValue(right, env)
		if err != nil {
			return nil, err
		}
		return ls + rs, nil

	case "=", "!=":
		if left == nil || right == nil {
			return false, nil
		}
		equal, err := DeepEqualEnv(left, right, env)
		if err != nil {
			return nil, err
		}
		return equal == (node.Value == "="), nil

	case "<", "<=", ">", ">=":
		if prec := env.DecimalPrecision(); prec > 0 {
			if c, ok := decimalOrder(left, right, prec); ok {
				return applyCmpOp(c, node.Value), nil
			}
		}
		return compareValues(left, right, node.Value)

	case "in":
		return containsValue(right, left, env)

	case "..":
		return evalRange(left, right, env)

	default:
		return nil, fmt.Errorf("unknown binary operator: %s", node.Value)
	}
}

// evalDefaultLeft evaluates the left operand of ?: or ??, returning its
// value and the operator's result when it picks that operand. In a lambda's
// tail position jsonata-js returns a call there uncollapsed, as it does a
// tail call (see markTailPosition).
func evalDefaultLeft(node *parser.Node, input any, env *Environment) (left, result any, err error) {
	if node.Thunk && node.Left.Type == parser.NodeFunction {
		result, err = evalFunctionSequence(node.Left, input, env)
		return CollapseAndKeep(result, false), result, err
	}
	left, err = Eval(node.Left, input, env)
	return left, settleRaw(left, false), err
}

// evalSubscript evaluates a subscript chain such as a[[0]][0]. As in
// jsonata-js, each stage filters the sequence the previous one selected,
// and only the chain's result collapses: a lone item stands alone.
//
// jsonata-js evaluates a field with predicates as a one-step path, which
// maps over an array of contexts other than the root array it wraps as one:
// q.(a[0]) over {"q":[[{"a":[1,2]},{"a":[3]}]]} is [1,3].
func evalSubscript(node *parser.Node, input any, env *Environment) (any, error) {
	if subscriptsName(node) && isContextArray(input, env) {
		result, err := evalPathStep(node, input, env, true, false, true)
		if err != nil {
			return nil, err
		}
		keep := parser.ChainKeepsArray(node)
		if result = CollapseAndKeep(result, keep); !keep {
			return result, nil
		}
		return keepSingletonArray(result), nil
	}
	return evalSubscriptValue(node, input, env)
}

// subscriptsName reports whether node is a chain of subscripts of a field.
func subscriptsName(node *parser.Node) bool {
	base := subscriptBase(node)
	return base != nil && base.Type == parser.NodeName
}

// subscriptBase returns the node a chain of subscripts, node itself when it
// is none, applies to, or nil when that has a group-by.
func subscriptBase(node *parser.Node) *parser.Node {
	for isSubscript(node) {
		node = node.Left
	}
	if node == nil || node.Group != nil {
		return nil
	}
	return node
}

// filtersStep reports whether node is a subscript chain that jsonata-js
// evaluates as stages of a path step even alone or as a path's first step,
// passing its selection on as a sequence: the subscripts of a field, or of
// a sort, which jsonata-js always makes a path step. There any other base,
// as in $x[p] or $f()[p], is filtered as an expression, whose selection
// collapses as the expression returns it. On any later step of a path, as
// in $.(n)[p], a subscript is a stage whatever its base.
func filtersStep(node *parser.Node) bool {
	if !isSubscript(node) || node.Group != nil {
		return false
	}
	base := subscriptBase(node)
	return base != nil && (base.Type == parser.NodeName || base.Type == parser.NodeSort)
}

// evalSubscriptValue evaluates a subscript chain against input as one
// context, as a path step does.
func evalSubscriptValue(node *parser.Node, input any, env *Environment) (any, error) {
	result, err := evalSubscriptStage(node, input, env)
	if seq, ok := result.(*Sequence); ok && err == nil {
		return CollapseSequence(seq), nil
	}
	return result, err
}

// evalSubscriptStage evaluates one stage of a subscript chain, leaving a
// filter's selection a sequence, whose items the next stage, a sort or the
// next path step take as they are; [] still keeps it an array.
func evalSubscriptStage(node *parser.Node, input any, env *Environment) (any, error) {
	result, _, err := subscriptStage(node, input, env)
	return result, err
}

// subscriptStage is evalSubscriptStage, also reporting whether the stage kept
// an undefined base as its one undefined item. jsonata-js filters an undefined
// base as [undefined], so a predicate still runs once and its errors surface,
// and the next stage filters it again if it was kept.
func subscriptStage(node *parser.Node, input any, env *Environment) (result any, keptMissing bool, _ error) {
	// keepArray: [] applies to this subscript or to a node in its left chain,
	// as through a sort step.
	keepArray := node.KeepArray || parser.ChainKeepsArray(node.Left)
	// A subscripted block a % reaches into, outside a path's tuple stream,
	// is filtered as its tuples, seeing their bindings, as in jsonata-js;
	// the result is the kept tuples' values, where jsonata-js leaks the
	// tuples themselves unless a path step merges them.
	if base := parser.StepBase(node); base.Type == parser.NodeBlock && base.TupleResult {
		_, stages := splitTupleStages(node)
		stream, err := blockTuples(base, stages, []pathCtx{{value: input, env: env}})
		if err != nil {
			return nil, false, err
		}
		seq := CreateSequence()
		for _, ctx := range stream {
			appendToSequence(seq, ctx.value)
		}
		return CollapseAndKeep(seq, keepArray), false, nil
	}
	items, missing, err := evalSubscriptLeft(node, input, env)
	switch {
	case err != nil:
		return nil, false, err
	case missing:
		kept, err := keepsMissing(node.Right, env)
		return nil, kept, err
	case len(items) == 0:
		return nil, false, nil
	case node.Right.Type == parser.NodeNumber:
		result, err = pickItem(node.Right, items, keepArray, env)
		return result, false, err
	}
	filtered, err := filterByPredicate(node.Right, items, env)
	if err == nil {
		err = env.CheckSequence(len(filtered.Values))
	}
	switch {
	case err != nil:
		return nil, false, err
	case keepArray:
		return CollapseAndKeep(filtered, true), false, nil
	case len(filtered.Values) == 0:
		return nil, false, nil
	}
	return filtered, false, nil
}

// keepsMissing evaluates predicate against an undefined item, reporting
// whether it keeps the item; a number literal never selects one.
func keepsMissing(predicate *parser.Node, env *Environment) (bool, error) {
	if predicate.Type == parser.NodeNumber {
		return false, nil
	}
	res, err := Eval(predicate, nil, NewChildEnvironment(env))
	if err != nil {
		return false, err
	}
	matches, err := filterMatches(res, 0, 1, 0, env)
	return matches > 0, err
}

// pickItem applies a number-literal subscript. As in jsonata-js, an array
// item it picks is the whole result, collapsed like a returned sequence.
func pickItem(index *parser.Node, items []any, keepArray bool, env *Environment) (any, error) {
	i, _, err := subscriptIndex(evalNumber(index, env), len(items))
	if err != nil || i < 0 || i >= len(items) {
		return nil, err
	}
	item := items[i]
	if _, isArr := AsArray(item); isArr {
		return settleRaw(item, keepArray), nil
	}
	item = NilAsNull(item)
	if keepArray {
		return KeptArray{item}, nil
	}
	return item, nil
}

// filterByPredicate evaluates predicate against each item and keeps it as
// filterMatches says.
func filterByPredicate(predicate *parser.Node, items []any, env *Environment) (*Sequence, error) {
	seq := CreateSequence()
	filterEnv := NewChildEnvironment(env)
	if contextFree(predicate) {
		res, err := Eval(predicate, items[0], filterEnv)
		if err != nil {
			return nil, err
		}
		return filterConstant(res, items, env)
	}
	for i, item := range items {
		if err := env.Err(); err != nil {
			return nil, err
		}
		item = NilAsNull(item)
		res, err := Eval(predicate, item, filterEnv)
		if err != nil {
			return nil, err
		}
		matches, err := filterMatches(res, i, len(items), len(seq.Values), env)
		if err != nil {
			return nil, err
		}
		for range matches {
			seq.Values = append(seq.Values, item)
		}
	}
	return seq, nil
}

// filterConstant applies a predicate result that is the same for every
// item, selecting the same items filterByPredicate would.
func filterConstant(res any, items []any, env *Environment) (*Sequence, error) {
	resolved, positional, err := selectedPositions(res, len(items))
	switch {
	case err != nil:
		return nil, err
	case !positional:
		truthy, err := ToBooleanEnv(res, env)
		if err != nil {
			return nil, err
		}
		if truthy {
			seq := &Sequence{Values: slices.Clone(items)}
			for i, item := range seq.Values {
				seq.Values[i] = NilAsNull(item)
			}
			return seq, nil
		}
		return CreateSequence(), nil
	}
	if len(resolved) > len(items) {
		if err := env.CheckSequence(len(resolved)); err != nil {
			return nil, err
		}
	}
	slices.Sort(resolved)
	seq := CreateSequence()
	for _, i := range resolved {
		if i >= 0 && i < len(items) {
			seq.Values = append(seq.Values, NilAsNull(items[i]))
		}
	}
	return seq, nil
}

// contextFree reports whether predicate does not read its item, so it has
// the same value for every item: filterByPredicate then evaluates it once,
// and a computed index such as $a[$i] takes O(1).
func contextFree(predicate *parser.Node) bool {
	if predicate == nil {
		return true
	}
	if predicate.Group != nil || predicate.Focus != "" || predicate.Index != "" {
		return false
	}
	switch predicate.Type {
	case parser.NodeNumber, parser.NodeString, parser.NodeValue:
		return true
	case parser.NodeVariable:
		return predicate.Value != ""
	case parser.NodeBinary:
		return contextFree(predicate.Left) && contextFree(predicate.Right)
	case parser.NodeCondition:
		return contextFree(predicate.Condition) && contextFree(predicate.Then) &&
			contextFree(predicate.Else)
	case parser.NodeUnary:
		switch predicate.Value {
		case "-":
			return contextFree(predicate.Expression)
		case "[":
			return !slices.ContainsFunc(predicate.Expressions, func(e *parser.Node) bool { return !contextFree(e) })
		}
	}
	return false
}

// evalSubscriptLeft evaluates the left side of a subscript and normalizes
// the result to a slice; missing reports that the stage filters an undefined
// base rather than an empty one.
func evalSubscriptLeft(node *parser.Node, input any, env *Environment) (items []any, missing bool, err error) {
	var left any
	missing = true
	switch {
	case node.Left.Type == parser.NodeFunction && node.PathStage:
		// A [] keeps the call's one-item sequence before its stages run.
		if left, err = evalFunctionSequence(node.Left, input, env); err == nil {
			left = CollapseAndKeep(left, parser.ChainKeepsArray(node))
		}
	case node.Left.Type == parser.NodeFunction:
		left, err = evalFunctionSequence(node.Left, input, env)
	case isSubscript(node.Left):
		left, missing, err = subscriptStage(node.Left, input, env)
	case node.Left.Type == parser.NodeSort && node.Left.Group == nil:
		left, err = evalSortStep(node.Left, input, env)
	case node.Left.Type == parser.NodeName && !node.Left.KeepArray && node.Left.Group == nil:
		// The field is looked up in the subscript's one context, an array
		// context included, as jsonata-js evaluates a predicated step.
		left, err = evalLookup(node.Left, input, env)
	default:
		left, err = Eval(node.Left, input, env)
	}
	if err != nil || left == nil {
		return nil, missing, err
	}
	switch v := left.(type) {
	case ConsArray:
		// A sort of one constructed array returns it as one item (see classifySortStep).
		if isSort, indexStage := classifySortStep(node.Left); isSort && !indexStage {
			items = []any{v}
		} else {
			items = []any(v)
		}
	case *Sequence:
		if len(v.Values) == 0 {
			return nil, false, nil
		}
		items = v.Values
	default:
		var isArr bool
		if items, isArr = AsArray(left); !isArr {
			items = []any{left}
		}
	}
	return items, false, nil
}

// resolveIndices resolves every value of an index array with subscriptIndex.
// ok is false when any value is not a number; every value is still checked,
// so an infinite index raises D1001 wherever it sits, as in jsonata-js.
func resolveIndices(values []any, length int) (indices []int, ok bool, err error) {
	indices = make([]int, 0, len(values))
	ok = true
	for _, v := range values {
		i, numeric, indexErr := subscriptIndex(v, length)
		if indexErr != nil {
			return nil, false, indexErr
		}
		ok = ok && numeric
		indices = append(indices, i)
	}
	if !ok {
		return nil, false, nil
	}
	return indices, true, nil
}

// subscriptIndex resolves a numeric subscript value to a position in a
// sequence of `length` items: rounded down, and counted from the end when
// negative. ok is false for non-numbers and NaN, which filter as booleans;
// ±Inf raises D1001, as in jsonata-js.
func subscriptIndex(v any, length int) (i int, ok bool, _ error) {
	f, isNum := ToFloat64(v)
	switch {
	case !isNum || math.IsNaN(f):
		return 0, false, nil
	case math.IsInf(f, 0):
		return 0, false, &JSONataError{Code: "D1001", Message: "subscript index is out of range"}
	}
	if i = ToIntClamped(math.Floor(f)); i < 0 {
		i += length
	}
	return i, true, nil
}

// evalArithFloat64 performs arithmetic on two float64 values.
// Extracted so the fast-path (both operands already float64) can bypass
// ToFloat64 conversion overhead entirely.
func evalArithFloat64(l, r float64, op string) (any, error) {
	var result float64
	switch op {
	case "+":
		result = l + r
	case "-":
		result = l - r
	case "*":
		result = l * r
	case "/":
		result = l / r
		return result, nil // let Inf propagate without error
	case "%":
		if r == 0 {
			return nil, &JSONataError{Code: "D3001", Message: "modulo by zero"}
		}
		result = math.Mod(l, r)
	case "**":
		result = math.Pow(l, r)
	}
	if math.IsInf(result, 0) || math.IsNaN(result) {
		return nil, &JSONataError{Code: "D1001", Message: fmt.Sprintf("Number out of range: %g", result)}
	}
	return result, nil
}

func isSubscript(n *parser.Node) bool {
	return n != nil && n.Type == parser.NodeBinary && n.Value == "["
}
