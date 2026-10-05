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
		if !ToBoolean(left) {
			return false, nil
		}
		right, err := Eval(node.Right, input, env)
		if err != nil {
			return nil, err
		}
		return ToBoolean(right), nil

	case "or":
		left, err := Eval(node.Left, input, env)
		if err != nil {
			return nil, err
		}
		if ToBoolean(left) {
			return true, nil
		}
		right, err := Eval(node.Right, input, env)
		if err != nil {
			return nil, err
		}
		return ToBoolean(right), nil

	case "?:":
		// Elvis / default: return left if ToBoolean(left) is true, else right.
		left, err := Eval(node.Left, input, env)
		if err != nil {
			return nil, err
		}
		if ToBoolean(left) {
			return left, nil
		}
		return Eval(node.Right, input, env)

	case "??":
		// Null-coalescing: return left if not null/undefined, else right.
		left, err := Eval(node.Left, input, env)
		if err != nil {
			return nil, err
		}
		if left != nil {
			return left, nil
		}
		return Eval(node.Right, input, env)

	case "~>":
		// Chain/pipe: pass left as the first argument to the right-hand function.
		// When the right is a function call with existing args (e.g. $map(fn)),
		// prepend left to those args so arr ~> $map(fn) becomes $map(arr, fn).
		left, err := Eval(node.Left, input, env)
		if err != nil {
			return nil, err
		}
		return evalChain(node.Right, left, input, env)

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
		ls, err := stringifyValue(left, env.DecimalPrecision())
		if err != nil {
			return nil, err
		}
		rs, err := stringifyValue(right, env.DecimalPrecision())
		if err != nil {
			return nil, err
		}
		return ls + rs, nil

	case "=", "!=":
		if left == nil || right == nil {
			return false, nil
		}
		return DeepEqualPrec(left, right, env.DecimalPrecision()) == (node.Value == "="), nil

	case "<", "<=", ">", ">=":
		if prec := env.DecimalPrecision(); prec > 0 {
			if c, ok := decimalOrder(left, right, prec); ok {
				return applyCmpOp(c, node.Value), nil
			}
		}
		return compareValues(left, right, node.Value)

	case "in":
		return containsValue(right, left, env.DecimalPrecision()), nil

	case "..":
		return evalRange(left, right, env)

	default:
		return nil, fmt.Errorf("unknown binary operator: %s", node.Value)
	}
}

func evalSubscript(node *parser.Node, input any, env *Environment) (any, error) {
	// When Left is a Block containing a single path expression and the
	// predicate references % (parent), evaluate the inner path in tuple mode
	// so each item retains its parent context for the % operator.
	if node.Left != nil && node.Left.Type == parser.NodeBlock &&
		nodeHasParentRef(node.Right) &&
		len(node.Left.Expressions) == 1 &&
		node.Left.Expressions[0].Type == parser.NodePath {
		return evalSubscriptBlockParent(node, input, env)
	}

	left, items, err := evalSubscriptLeft(node, input, env)
	if err != nil || left == nil {
		return nil, err
	}

	// keepArray is true when the [] operator was applied to this subscript (or any
	// node in the left chain), forcing the result to be returned as an array even
	// if singular. We walk the left chain to propagate KeepArray through sort steps.
	keepArray := node.KeepArray || parser.ChainKeepsArray(node.Left)

	if len(items) == 0 {
		if keepArray {
			return []any{}, nil
		}
		return nil, nil
	}

	wrapResult := func(v any) any {
		if !keepArray {
			return v
		}
		if v == nil {
			return []any{}
		}
		if _, ok := AsArray(v); ok {
			return v
		}
		return []any{v}
	}

	if node.Right.Type == parser.NodeNumber {
		item, err := pickItem(node.Right, items, env)
		if err != nil || item == nil {
			return nil, err
		}
		return wrapResult(item), nil
	}
	indexVar := ""
	if node.Left != nil {
		indexVar = node.Left.Index
	}
	filtered, err := filterByPredicate(node.Right, items, input, indexVar, env)
	if err != nil {
		return nil, err
	}
	return wrapResult(CollapseSequence(filtered)), nil
}

// pickItem applies a number-literal subscript, which jsonata-js resolves
// without evaluating it per item.
func pickItem(index *parser.Node, items []any, env *Environment) (any, error) {
	i, _, err := subscriptIndex(evalNumber(index, env), len(items))
	if err != nil || i < 0 || i >= len(items) {
		return nil, err
	}
	if item := items[i]; item != nil {
		return item, nil
	}
	return Null, nil
}

// filterByPredicate evaluates predicate against each item and keeps it as
// filterMatches says. It binds %% → parent (for the % operator) and
// optionally indexVar to the item's position.
func filterByPredicate(predicate *parser.Node, items []any, parent any, indexVar string, env *Environment) (*Sequence, error) {
	seq := CreateSequence()
	filterEnv := NewChildEnvironment(env)
	filterEnv.Bind(parentKey, parent)
	if contextFree(predicate, indexVar) {
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
		if indexVar != "" {
			filterEnv.Bind(indexVar, float64(i))
		}
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
		if ToBoolean(res) {
			return &Sequence{Values: slices.Clone(items)}, nil
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
			seq.Values = append(seq.Values, items[i])
		}
	}
	return seq, nil
}

// contextFree reports whether predicate reads neither its item nor its
// position, so it has the same value for every item: filterByPredicate then
// evaluates it once, and a computed index such as $a[$i] takes O(1).
func contextFree(predicate *parser.Node, indexVar string) bool {
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
		return predicate.Value != "" && predicate.Value != indexVar
	case parser.NodeBinary:
		return contextFree(predicate.Left, indexVar) && contextFree(predicate.Right, indexVar)
	case parser.NodeCondition:
		return contextFree(predicate.Condition, indexVar) && contextFree(predicate.Then, indexVar) &&
			contextFree(predicate.Else, indexVar)
	case parser.NodeUnary:
		switch predicate.Value {
		case "-":
			return contextFree(predicate.Expression, indexVar)
		case "[":
			return !slices.ContainsFunc(predicate.Expressions, func(e *parser.Node) bool { return !contextFree(e, indexVar) })
		}
	}
	return false
}

// evalSubscriptLeft evaluates the left side of a subscript and normalizes
// the result to a slice. Returns (left, items, err); left==nil means no match.
func evalSubscriptLeft(node *parser.Node, input any, env *Environment) (left any, items []any, _ error) {
	left, err := Eval(node.Left, input, env)
	if err != nil {
		return nil, nil, err
	}
	if left == nil {
		return nil, nil, nil
	}
	switch v := left.(type) {
	case ConsArray:
		// A sort of one constructed array returns it as one item (see classifySortStep).
		if isSort, indexStage := classifySortStep(node.Left); isSort && !indexStage {
			items = []any{v}
		} else {
			items = []any(v)
		}
	case []any:
		items = v
	case *Sequence:
		collapsed := CollapseSequence(v)
		if collapsed == nil {
			return nil, nil, nil
		}
		if arr, ok := AsArray(collapsed); ok {
			items = arr
		} else {
			items = []any{collapsed}
		}
	default:
		items = []any{left}
	}
	return left, items, nil
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

func evalSubscriptBlockParent(node *parser.Node, input any, env *Environment) (any, error) {
	innerPath := node.Left.Expressions[0]
	tupleCtxs, err := expandPathTuple(innerPath.Steps, []pathCtx{{value: input, env: env}}, false)
	if err != nil {
		return nil, err
	}
	if len(tupleCtxs) == 0 {
		return nil, nil
	}

	keepArray := node.KeepArray || parser.ChainKeepsArray(node.Left)
	seq := CreateSequence()
	for _, tctx := range tupleCtxs {
		predResult, err := Eval(node.Right, tctx.value, tctx.env)
		if err != nil {
			return nil, err
		}
		if ToBoolean(predResult) {
			seq.Values = append(seq.Values, tctx.value)
		}
	}
	result := CollapseSequence(seq)
	if !keepArray {
		return result, nil
	}
	if result == nil {
		return []any{}, nil
	}
	if arr, ok := result.([]any); ok {
		return arr, nil
	}
	return []any{result}, nil
}
