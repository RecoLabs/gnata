package evaluator

import (
	"fmt"
	"math"
	"sort"

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

		if lf, ok := left.(float64); ok {
			if rf, ok2 := right.(float64); ok2 {
				return evalArithFloat64(lf, rf, op)
			}
		}

		if left != nil {
			if _, ok := ToFloat64(left); !ok {
				code := "T2001"
				if IsNull(left) {
					code = "T2002"
				}
				return nil, &JSONataError{Code: code, Message: fmt.Sprintf("the left operand of the %q operator must evaluate to a number", op)}
			}
		}
		if left == nil {
			return nil, nil
		}
		if right != nil {
			if _, ok := ToFloat64(right); !ok {
				code := "T2001"
				if IsNull(right) {
					code = "T2002"
				}
				return nil, &JSONataError{Code: code, Message: fmt.Sprintf("the right operand of the %q operator must evaluate to a number", op)}
			}
		}
		if right == nil {
			return nil, nil
		}
		l, _ := ToFloat64(left)
		r, _ := ToFloat64(right)
		return evalArithFloat64(l, r, op)

	case "&":
		ls, err := stringifyValue(left)
		if err != nil {
			return nil, err
		}
		rs, err := stringifyValue(right)
		if err != nil {
			return nil, err
		}
		return ls + rs, nil

	case "=":
		if left == nil || right == nil {
			return false, nil
		}
		return DeepEqual(left, right), nil

	case "!=":
		if left == nil || right == nil {
			return false, nil
		}
		return !DeepEqual(left, right), nil

	case "<":
		return compareValues(left, right, "<")

	case "<=":
		return compareValues(left, right, "<=")

	case ">":
		return compareValues(left, right, ">")

	case ">=":
		return compareValues(left, right, ">=")

	case "in":
		return containsValue(right, left), nil

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
		if arr, ok := v.([]any); ok {
			return arr
		}
		return []any{v}
	}

	if idx, ok := ToFloat64(rightVal); ok {
		i := int(idx)
		if i < 0 {
			i = len(items) + i
		}
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
	if result, ok := selectByIndices(rightVal, items); ok {
		return result, nil
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
	ctx := env.Context()
	for i, item := range items {
		if err := ctx.Err(); err != nil {
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
	return CollapseSequence(seq), nil
}

// evalSubscriptLeft evaluates the left side of a subscript and normalizes
// the result to a slice. Returns (left, items, err); left==nil means no match.
func evalSubscriptLeft(node *parser.Node, input any, env *Environment) (left any, items []any, _ error) {
	if node.Left != nil && node.Left.Type == parser.NodeDescendant {
		descSeq := CreateSequence()
		appendToSequence(descSeq, input)
		appendToSequence(descSeq, descendantLookup(input))
		left = CollapseSequence(descSeq)
	} else {
		var err error
		if left, err = Eval(node.Left, input, env); err != nil {
			return nil, nil, err
		}
	}
	if left == nil {
		return nil, nil, nil
	}
	switch v := left.(type) {
	case []any:
		items = v
	case *Sequence:
		collapsed := CollapseSequence(v)
		if collapsed == nil {
			return nil, nil, nil
		}
		if arr, ok := collapsed.([]any); ok {
			items = arr
		} else {
			items = []any{collapsed}
		}
	default:
		items = []any{left}
	}
	return left, items, nil
}

// selectByIndices handles array-of-indices subscript: when rightVal is []any
// of all-numeric values, it selects the corresponding elements from items.
// Returns (result, true) if handled, or (nil, false) to fall through to predicate filter.
func selectByIndices(rightVal any, items []any) (any, bool) {
	indexArr, ok := rightVal.([]any)
	if !ok {
		return nil, false
	}
	indices := make([]int, 0, len(indexArr))
	for _, idxVal := range indexArr {
		idx, ok := ToFloat64(idxVal)
		if !ok {
			return nil, false // non-numeric → fall through to predicate filter
		}
		i := int(idx)
		if i < 0 {
			i = len(items) + i
		}
		indices = append(indices, i)
	}
	sort.Ints(indices)
	result := make([]any, 0, len(indices))
	for _, i := range indices {
		if i >= 0 && i < len(items) {
			result = append(result, items[i])
		}
	}
	if len(result) == 0 {
		return nil, true
	}
	return result, true
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
