package evaluator

import (
	"errors"
	"fmt"
	"math"
	"sort"

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

func hasKeepArrayInChain(node *parser.Node) bool {
	for node != nil {
		if node.KeepArray {
			return true
		}
		node = node.Left
	}
	return false
}

// evalSubscript evaluates a subscript chain such as a[[0]][0]. As in
// jsonata-js, each stage filters the sequence the previous one selected,
// and only the chain's result collapses: a lone item stands alone.
func evalSubscript(node *parser.Node, input any, env *Environment) (any, error) {
	result, err := evalSubscriptStage(node, input, env)
	seq, isSeq := result.(*Sequence)
	if err != nil || !isSeq {
		return result, err
	}
	if node.KeepArray || hasKeepArrayInChain(node.Left) {
		return CollapseToSlice(seq), nil
	}
	return CollapseSequence(seq), nil
}

// evalSubscriptStep evaluates a subscript as a path step: its index
// selection stays a sequence, whose items are the next step's contexts.
func evalSubscriptStep(node *parser.Node, input any, env *Environment) (any, error) {
	result, err := evalSubscriptStage(node, input, env)
	if seq, ok := result.(*Sequence); ok && err == nil && (node.KeepArray || hasKeepArrayInChain(node.Left)) {
		return CollapseToSlice(seq), nil
	}
	return result, err
}

// evalSubscriptStage evaluates one stage of a subscript chain, leaving an
// index selection a sequence for the next stage.
func evalSubscriptStage(node *parser.Node, input any, env *Environment) (any, error) {
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
	// A #$i or @$v binding makes the items a tuple stream, which counts
	// against the sequence guardrail as in jsonata-js.
	if stepHasBinding(node) {
		if err := env.CheckSequence(len(items)); err != nil {
			return nil, err
		}
	}

	// keepArray is true when the [] operator was applied to this subscript (or any
	// node in the left chain), forcing the result to be returned as an array even
	// if singular. We walk the left chain to propagate KeepArray through sort steps.
	keepArray := node.KeepArray || hasKeepArrayInChain(node.Left)

	if len(items) == 0 {
		if keepArray {
			return []any{}, nil
		}
		return nil, nil
	}

	// Try numeric indexing: evaluate the right-hand side with a representative
	// context (the first item, if available) to avoid null-context errors.
	rightCtx := items[0]
	rightVal, err := Eval(node.Right, rightCtx, env)
	if err != nil {
		// If the predicate errors with item context, try with original input.
		rightVal, err = Eval(node.Right, input, env)
		if err != nil {
			return nil, err
		}
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
		if _, ok := v.(*Sequence); ok {
			return v
		}
		return []any{v}
	}

	if i, ok, err := subscriptIndex(rightVal, len(items)); err != nil {
		return nil, err
	} else if ok {
		if i < 0 || i >= len(items) {
			return nil, nil
		}
		item := items[i]
		if item == nil {
			item = Null
		}
		return wrapResult(item), nil
	}

	// Array index: when subscript evaluates to an array of all-numeric indices,
	// select multiple elements. Non-numeric arrays fall through to predicate filter.
	if result, ok, err := selectByIndices(rightVal, items, env); err != nil || ok {
		if err != nil || result == nil {
			return nil, err
		}
		return wrapResult(result), nil
	}

	// Predicate filter: keep items where right evaluates to truthy.
	indexVar := ""
	if node.Left != nil {
		indexVar = node.Left.Index
	}
	filtered, err := filterByPredicate(node.Right, items, input, indexVar, env)
	if err != nil {
		return nil, err
	}
	return wrapResult(filtered), nil
}

// filterByPredicate keeps items where predicate evaluates to truthy, binding
// %% → parent (for the % operator) and optionally indexVar to the loop position.
func filterByPredicate(predicate *parser.Node, items []any, parent any, indexVar string, env *Environment) (any, error) {
	seq := CreateSequence()
	filterEnv := NewChildEnvironment(env)
	filterEnv.Bind(parentKey, parent)
	for i, item := range items {
		if err := env.Err(); err != nil {
			return nil, err
		}
		if indexVar != "" {
			filterEnv.Bind(indexVar, float64(i))
		}
		if val, err := Eval(predicate, item, filterEnv); err != nil {
			return nil, err
		} else if ToBoolean(val) {
			seq.Values = append(seq.Values, item)
		}
	}
	if err := env.CheckSequence(len(seq.Values)); err != nil {
		return nil, err
	}
	if len(seq.Values) == 0 {
		return nil, nil
	}
	return seq, nil
}

// evalSubscriptLeft evaluates the left side of a subscript and normalizes
// the result to a slice. Returns (left, items, err); left==nil means no match.
func evalSubscriptLeft(node *parser.Node, input any, env *Environment) (left any, items []any, _ error) {
	switch {
	case node.Left != nil && node.Left.Type == parser.NodeDescendant:
		descSeq := descendantLookup(input)
		if err := env.CheckSequence(len(descSeq.Values)); err != nil {
			return nil, nil, err
		}
		left = CollapseSequence(descSeq)
	case isSubscript(node.Left) || node.Left != nil && node.Left.Type == parser.NodeSort:
		stage, err := evalOperandStage(node.Left, input, env)
		if seq, ok := stage.(*Sequence); ok && err == nil {
			return seq, seq.Values, nil
		}
		if left = stage; err != nil {
			return nil, nil, err
		}
	default:
		var err error
		if left, err = Eval(node.Left, input, env); err != nil {
			return nil, nil, err
		}
	}
	if left == nil {
		return nil, nil, nil
	}
	switch v := left.(type) {
	case ConsArray:
		items = []any(v)
	case []any:
		items = v
	case *Sequence:
		// A filter applies to the sequence's items, as in jsonata-js.
		if len(v.Values) == 0 {
			return nil, nil, nil
		}
		items = v.Values
	default:
		items = []any{left}
	}
	return left, items, nil
}

// selectByIndices handles array-of-indices subscript: when rightVal is []any
// of all-numeric values, it selects the corresponding elements from items.
// ok is false when an index is not a number, so the caller falls through to
// the predicate filter; an infinite index raises D1001.
func selectByIndices(rightVal any, items []any, env *Environment) (selected any, ok bool, err error) {
	indexArr, isArr := rightVal.([]any)
	if !isArr {
		return nil, false, nil
	}
	indices, ok, err := resolveIndices(indexArr, len(items))
	if err != nil || !ok {
		return nil, false, err
	}
	sort.Ints(indices)
	result := make([]any, 0, len(indices))
	for _, i := range indices {
		if i >= 0 && i < len(items) {
			result = append(result, items[i])
		}
	}
	if err := env.CheckSequence(len(result)); err != nil {
		return nil, false, err
	}
	if len(result) == 0 {
		return nil, true, nil
	}
	return &Sequence{Values: result}, true, nil
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
	tupleCtxs, err := expandPathTuple(innerPath.Steps, []pathCtx{{value: input, env: env}})
	if err != nil {
		return nil, err
	}
	if len(tupleCtxs) == 0 {
		return nil, nil
	}

	keepArray := node.KeepArray || hasKeepArrayInChain(node.Left)
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

// evalOperandStage evaluates a subscript's or a group-by's operand, leaving
// the sequence a subscript, sort or path yields uncollapsed: the next stage
// applies to its items, of which a lone array is one.
func evalOperandStage(node *parser.Node, input any, env *Environment) (any, error) {
	switch {
	case isSubscript(node):
		return evalSubscriptStage(node, input, env)
	case node.Type == parser.NodeSort:
		return evalSortStep(node, input, env)
	case node.Type == parser.NodePath && node.Group == nil && !pathHasTupleStep(node.Steps):
		result, singleConsArray, err := walkPathSimple(node, input, env)
		if _, isSeq := result.(*Sequence); isSeq && !node.KeepSingletonArray || err != nil {
			return result, err
		}
		return pathResult(result, node.KeepSingletonArray, singleConsArray), nil
	}
	return Eval(node, input, env)
}

func isSubscript(n *parser.Node) bool {
	return n != nil && n.Type == parser.NodeBinary && n.Value == "["
}
