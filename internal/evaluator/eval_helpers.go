package evaluator

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/recolabs/gnata/internal/parser"
)

func appendToSequence(seq *Sequence, v any) {
	if v == nil {
		return
	}
	switch val := v.(type) {
	case *Sequence:
		for _, item := range val.Values {
			appendToSequence(seq, item)
		}
	default:
		seq.Values = append(seq.Values, v)
	}
}

func stringifyValue(v any, prec int) (string, error) {
	if v == nil {
		return "", nil
	}
	switch val := v.(type) {
	case string:
		return val, nil
	case json.Number:
		if s, ok := FormatDecimal(val, prec); ok {
			return s, nil
		}
		return FormatNumber(val), nil
	case float64:
		if s, ok := FormatDecimal(val, prec); ok {
			return s, nil
		}
		return FormatFloat(val), nil
	case bool:
		if val {
			return "true", nil
		}
		return "false", nil
	default:
		b, err := AppendJSON(nil, v)
		if err != nil {
			return "", fmt.Errorf("cannot stringify value: %w", err)
		}
		return string(b), nil
	}
}

// FormatNumber converts a json.Number to its canonical string form,
// normalizing scientific notation to match JavaScript's Number.toString().
// Only converts to float64 when the raw string contains scientific notation
// (e/E); plain integers and decimals are returned verbatim to preserve
// precision for values beyond 2^53.
func FormatNumber(n json.Number) string {
	s := n.String()
	if !strings.ContainsAny(s, "eE") {
		return s
	}
	f, err := n.Float64()
	if err != nil {
		return s
	}
	return FormatFloat(f)
}

// FormatFloat converts a float64 to its canonical string form matching
// JavaScript's Number.toString() behavior. Numbers between 1e-7 and 1e21
// use decimal notation; numbers outside that range use scientific notation.
func FormatFloat(n float64) string {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return "null"
	}
	s := strconv.FormatFloat(n, 'g', 15, 64)
	abs := math.Abs(n)
	if abs != 0 && (abs < 5e-7 || abs >= 1e21) {
		sci := strconv.FormatFloat(n, 'e', -1, 64)
		return cleanExponent(sci)
	}
	if strings.ContainsRune(s, 'e') || strings.ContainsRune(s, 'E') {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return s
}

func cleanExponent(s string) string {
	mantissa, exp, ok := strings.Cut(s, "e")
	if !ok {
		return s
	}
	sign := ""
	if exp != "" && (exp[0] == '+' || exp[0] == '-') {
		sign = string(exp[0])
		exp = exp[1:]
	}
	if exp = strings.TrimLeft(exp, "0"); exp == "" {
		exp = "0"
	}
	return mantissa + "e" + sign + exp
}

// applyCmpOp applies an ordering operator to the result c of a three-way
// comparison (-1, 0 or +1).
func applyCmpOp(c int, op string) bool {
	switch op {
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	}
	return false
}

func compareValues(left, right any, op string) (any, error) {
	_, leftIsNum := ToFloat64(left)
	_, leftIsStr := left.(string)
	if left != nil && !leftIsNum && !leftIsStr {
		return nil, &JSONataError{Code: "T2010", Message: fmt.Sprintf("the operands of the %q operator must be numbers or strings", op)}
	}
	if left == nil || right == nil {
		return nil, nil
	}
	if ln, lok := ToFloat64(left); lok {
		if rn, rok := ToFloat64(right); rok {
			switch op {
			case "<":
				return ln < rn, nil
			case "<=":
				return ln <= rn, nil
			case ">":
				return ln > rn, nil
			case ">=":
				return ln >= rn, nil
			}
		}
		if _, isStr := right.(string); isStr {
			return nil, &JSONataError{
				Code:    "T2009",
				Message: fmt.Sprintf("the operands of the %q operator must be both numbers or both strings", op),
			}
		}
		return nil, &JSONataError{Code: "T2010", Message: fmt.Sprintf("the operands of the %q operator must be numbers or strings", op)}
	}
	if ls, lok := left.(string); lok {
		if rs, rok := right.(string); rok {
			return applyCmpOp(strings.Compare(ls, rs), op), nil
		}
		if _, isNum := ToFloat64(right); isNum {
			return nil, &JSONataError{
				Code:    "T2009",
				Message: fmt.Sprintf("the operands of the %q operator must be both numbers or both strings", op),
			}
		}
		return nil, &JSONataError{Code: "T2010", Message: fmt.Sprintf("the operands of the %q operator must be numbers or strings", op)}
	}
	return nil, &JSONataError{Code: "T2010", Message: fmt.Sprintf("the operands of the %q operator must be numbers or strings", op)}
}

func compareOrder(a, b any, prec int) (int, error) {
	if a == nil && b == nil {
		return 0, nil
	}
	if a == nil {
		return 1, nil
	}
	if b == nil {
		return -1, nil
	}
	if prec > 0 {
		if c, ok := DecimalCmp(a, b, prec); ok {
			return c, nil
		}
	}
	an, aNum := ToFloat64(a)
	bn, bNum := ToFloat64(b)
	if aNum && bNum {
		if an < bn {
			return -1, nil
		} else if an > bn {
			return 1, nil
		}
		return 0, nil
	}
	as, aStr := a.(string)
	bs, bStr := b.(string)
	if aStr && bStr {
		if as < bs {
			return -1, nil
		} else if as > bs {
			return 1, nil
		}
		return 0, nil
	}
	if (aNum && bStr) || (aStr && bNum) {
		return 0, &JSONataError{Code: "T2007", Message: "cannot compare string and number values"}
	}
	return 0, &JSONataError{Code: "T2008", Message: fmt.Sprintf("cannot compare values of type %T and %T", a, b)}
}

func containsValue(arr, elem any, prec int) bool {
	if arr == nil {
		return false
	}
	switch v := arr.(type) {
	case []any, ConsArray, KeptArray, RawSequence:
		items, _ := AsArray(v)
		for _, item := range items {
			if DeepEqualPrec(item, elem, prec) {
				return true
			}
		}
	case *Sequence:
		for _, item := range v.Values {
			if DeepEqualPrec(item, elem, prec) {
				return true
			}
		}
	default:
		return DeepEqualPrec(arr, elem, prec)
	}
	return false
}

func evalValue(node *parser.Node) (any, error) {
	switch node.Value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case parser.NullJSON:
		return Null, nil
	default:
		return nil, nil
	}
}

func evalVariable(node *parser.Node, input any, env *Environment) (any, error) {
	if node.Value == "" {
		return settleRaw(input, node.KeepArray), nil
	}
	val, found := env.Lookup(node.Value)
	if !found {
		return nil, nil
	}
	return settleRaw(val, node.KeepArray), nil
}

// evalName's array-mapping semantics (flatten one level per step, track
// whether the field was ever found to distinguish "undefined" from "found
// as an empty array", singleton-collapse a single match) are mirrored by
// path_bytes.go's walkPureSteps/stepArray for the gjson-based fast path.
// Keep the two in sync: a change here needs the matching change there
// (internal array types never occur in decoded input, so their handling is
// evaluator-only).
func evalName(node *parser.Node, input any, _ *Environment) (any, error) {
	switch v := input.(type) {
	case *OrderedMap:
		val, ok := v.Get(node.Value)
		if !ok {
			return nil, nil
		}
		return fieldValue(val), nil
	case map[string]any:
		val, ok := v[node.Value]
		if !ok {
			return nil, nil
		}
		return fieldValue(val), nil
	case []any:
		// JSONata maps field lookups across arrays.
		// Per the JSONata spec, array results from each field lookup are
		// flattened into the result sequence (not nested).
		seq := CreateSequence()
		fieldFound := false
		for _, item := range v {
			val, err := lookupItem(node, item)
			if err != nil {
				return nil, err
			}
			if val == nil {
				continue
			}
			fieldFound = true
			// Flatten array results from navigating through arrays, except a
			// constructed array, as jsonata-js evaluateStep does.
			switch val.(type) {
			case []any, KeptArray, RawSequence:
				inner, _ := AsArray(val)
				for _, sv := range inner {
					if sv == nil {
						sv = Null
					}
					seq.Values = append(seq.Values, sv)
				}
			default:
				appendToSequence(seq, val)
			}
		}
		if len(seq.Values) == 0 {
			if fieldFound {
				// At least one element had this field defined (e.g. as an
				// empty array []). Return empty array rather than nil so
				// downstream $exists sees the field as present.
				return []any{}, nil
			}
			return nil, nil
		}
		if len(seq.Values) == 1 {
			return seq.Values[0], nil
		}
		return CollapseSequence(seq), nil
	case ConsArray, KeptArray, RawSequence:
		arr, _ := AsArray(v)
		return evalName(node, arr, nil)
	case *Sequence:
		return evalName(node, CollapseSequence(v), nil)
	default:
		return nil, nil
	}
}

// lookupItem looks a name up in one item of an array: an object's field
// value as stored, so the caller can flatten a kept array or raw sequence
// it holds, or the name's result for any other item.
func lookupItem(node *parser.Node, item any) (any, error) {
	if !IsMap(item) {
		return evalName(node, item, nil)
	}
	val, ok := MapGet(item, node.Value)
	switch v := val.(type) {
	case nil:
		if ok {
			return Null, nil
		}
	case RawSequence:
		if len(v) == 0 {
			return nil, nil
		}
	}
	return val, nil
}

// fieldValue returns a field's value as a name step yields it. jsonata-js
// evaluates a name as a one-step path, which flattens a kept array or raw
// sequence the field holds: {"k": o.b[]}.k is 5.
func fieldValue(val any) any {
	if val == nil {
		return Null
	}
	return flattenKept(val)
}

// evalWildcard evaluates * against one context item (see wildcardItem).
func evalWildcard(_ *parser.Node, input any, env *Environment) (any, error) {
	c := collector{env: env}
	c.wildcardItem(input)
	return c.wildcardResult()
}

// evalPathStepWildcard maps a * step over a sequence of context items. A root
// path's first step sees the root array as one item, as jsonata-js wraps it.
func evalPathStepWildcard(step *parser.Node, input any, env *Environment) (any, error) {
	items, ok := input.([]any)
	if !ok || len(items) == 1 || step.RootContext && isRootInput(items, env) {
		if ok && len(items) == 1 {
			input = items[0]
		}
		return evalWildcard(step, input, env)
	}
	// As jsonata-js evaluateStep does, a lone item result that is a plain
	// array (see collector.wildcardResult) is returned as is; otherwise the
	// item results are flattened into one sequence.
	c := collector{env: env}
	defined, lonePlain := 0, false
	for _, item := range items {
		start := len(c.values)
		c.fromArray = false
		c.wildcardItem(item)
		if c.err != nil {
			return nil, c.err
		}
		if len(c.values) > start || c.fromArray {
			defined++
			lonePlain = c.fromArray
		}
	}
	c.fromArray = defined == 1 && lonePlain
	return c.wildcardResult()
}

// unwrapRoot returns a child of env in which no array is the root input:
// what a step evaluates once against the root array jsonata-js wraps sees
// that array as unwrapped.
func unwrapRoot(env *Environment) *Environment {
	child := NewChildEnvironment(env)
	child.SetRootInput(nil)
	return child
}

// isRootInput reports whether items is the root input (see SetRootInput).
func isRootInput(items []any, env *Environment) bool {
	root, ok := env.Lookup(rootInputKey)
	if !ok {
		return false
	}
	rootItems, ok := root.([]any)
	return ok && len(rootItems) == len(items) && (len(items) == 0 || &rootItems[0] == &items[0])
}

// collector gathers the values * and ** yield. It enforces WithSequence as
// values arrive and checks for cancellation as it walks, so a large or
// deeply shared structure cannot run unbounded before the limits apply.
type collector struct {
	env       *Environment
	values    []any
	visits    int
	fromArray bool // some value came from flattening an array member
	err       error
}

// cancelCheckInterval is how many visited values pass between cancellation
// checks; checking every value would dominate a small walk.
const cancelCheckInterval = 1024

func (c *collector) visit() bool {
	if c.err != nil {
		return false
	}
	c.visits++
	if c.visits%cancelCheckInterval == 0 {
		c.err = c.env.Err()
	}
	return c.err == nil
}

func (c *collector) add(v any) {
	c.values = append(c.values, v)
	c.err = c.env.CheckSequence(len(c.values))
}

// wildcardItem collects what * yields for one item, as jsonata-js
// evaluateWildcard does: an object's values or an array's elements, with
// arrays among them flattened at every depth. A scalar yields nothing.
func (c *collector) wildcardItem(item any) {
	if arr, ok := AsArray(item); ok {
		for _, elem := range arr {
			c.flatten(elem)
		}
		return
	}
	if IsMap(item) {
		MapRange(item, func(_ string, val any) bool {
			c.flatten(val)
			return c.err == nil
		})
	}
}

func (c *collector) flatten(v any) {
	if !c.visit() {
		return
	}
	arr, ok := AsArray(v)
	if !ok {
		c.add(v)
		return
	}
	c.fromArray = true
	for _, elem := range arr {
		c.flatten(elem)
	}
}

// wildcardResult returns the collected values. jsonata-js appends flattened
// array members with fn.append, which yields a plain array that no longer
// collapses to a single value or undefined.
func (c *collector) wildcardResult() (any, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.fromArray {
		if c.values == nil {
			return []any{}, nil
		}
		return c.values, nil
	}
	return CollapseSequence(&Sequence{Values: c.values}), nil
}

// descendants collects v and every value nested in it, as jsonata-js
// recurseDescendants does. Arrays are transparent: they contribute their
// members but are never values themselves, so a later step does not match
// both an array and its elements.
func (c *collector) descendants(v any) {
	if !c.visit() {
		return
	}
	if seq, ok := v.(*Sequence); ok {
		v = seq.Values
	}
	if arr, ok := AsArray(v); ok {
		for _, elem := range arr {
			c.descendants(elem)
		}
		return
	}
	if v == nil {
		return
	}
	c.add(v)
	if IsMap(v) {
		MapRange(v, func(_ string, val any) bool {
			c.descendants(val)
			return c.err == nil
		})
	}
}
