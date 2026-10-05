package evaluator

import (
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
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
		if isCallable(v) {
			return "", nil
		}
		prepared, err := JSONValue(v, prec)
		if err != nil {
			return "", err
		}
		b, err := AppendJSON(nil, prepared)
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

// FormatFloat converts a float64 to the string jsonata-js's $string gives:
// JavaScript's Number.toString() of roundJS(n), which uses decimal notation
// from 1e-6 up to 1e21 and scientific notation outside it.
func FormatFloat(n float64) string {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return "null"
	}
	b, _ := appendJSONFloat(nil, roundJS(n), 64) // only NaN and Inf fail
	return string(b)
}

// roundJS rounds n as jsonata-js does before turning a number into JSON: a
// non-integer to 15 significant digits (Number(n.toPrecision(15))), and -0
// to 0, which JSON.stringify writes as 0.
func roundJS(n float64) float64 {
	if n == 0 {
		return 0
	}
	if n == math.Trunc(n) || math.IsInf(n, 0) || math.IsNaN(n) {
		return n
	}
	abs := math.Abs(n)
	if isPrecision15Tie(abs) {
		// toPrecision rounds an exact tie away from zero, where strconv
		// rounds it to even; one ulp up breaks the tie that way.
		abs = math.Nextafter(abs, math.Inf(1))
	}
	// A finite non-integer's 15-digit form always parses.
	rounded, _ := strconv.ParseFloat(strconv.FormatFloat(abs, 'g', 15, 64), 64)
	return math.Copysign(rounded, n)
}

// fractionBits returns how many binary digits f has after the point.
func fractionBits(f float64) int {
	frac, exp := math.Frexp(f) // f = frac × 2^exp, 0.5 ≤ frac < 1
	mantissa := uint64(frac * (1 << 53))
	return 53 - exp - bits.TrailingZeros64(mantissa)
}

// isPrecision15Tie reports whether abs is exactly halfway between two
// 15-significant-digit decimals: its 16th digit is 5 and every later one 0.
// A float64 that is not an exact tie differs from one by the 18th digit, so
// 25 digits are enough to tell.
func isPrecision15Tie(abs float64) bool {
	// A tie has 16 significant digits. A non-integer with k binary digits
	// after the point has at least as many decimal digits as 5^k, so k ≤ 22;
	// this skips the slow 25-digit format for almost every float.
	if fractionBits(abs) > 22 {
		return false
	}
	s := strconv.FormatFloat(abs, 'e', 24, 64)
	digits := s[:1] + s[2:26]
	return digits[15] == '5' && strings.Trim(digits[16:], "0") == ""
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
	case []any:
		for _, item := range v {
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
		return input, nil
	}
	val, found := env.Lookup(node.Value)
	if !found {
		return nil, nil
	}
	return val, nil
}

// evalName's array-mapping semantics (flatten one level per step, track
// whether the field was ever found to distinguish "undefined" from "found
// as an empty array", singleton-collapse a single match) are mirrored by
// path_bytes.go's walkPureSteps/stepArray for the gjson-based fast path.
// Keep the two in sync: a change here needs the matching change there.
func evalName(node *parser.Node, input any, _ *Environment) (any, error) {
	switch v := input.(type) {
	case *OrderedMap:
		val, ok := v.Get(node.Value)
		if !ok {
			return nil, nil
		}
		if val == nil {
			return Null, nil
		}
		return val, nil
	case map[string]any:
		val, ok := v[node.Value]
		if !ok {
			return nil, nil
		}
		if val == nil {
			return Null, nil
		}
		return val, nil
	case []any:
		// JSONata maps field lookups across arrays.
		// Per the JSONata spec, array results from each field lookup are
		// flattened into the result sequence (not nested).
		seq := CreateSequence()
		fieldFound := false
		for _, item := range v {
			val, err := evalName(node, item, nil)
			if err != nil {
				return nil, err
			}
			if val == nil {
				continue
			}
			fieldFound = true
			// Flatten plain []any results from navigating through arrays.
			// This matches JSONata's automatic flattening semantics.
			switch inner := val.(type) {
			case []any:
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
	case ConsArray:
		return evalName(node, []any(v), nil)
	case *Sequence:
		return evalName(node, CollapseSequence(v), nil)
	default:
		return nil, nil
	}
}

func evalWildcard(_ *parser.Node, input any, env *Environment) (any, error) {
	if IsMap(input) {
		if MapLen(input) == 0 {
			return nil, nil
		}
		seq := CreateSequence()
		MapRange(input, func(_ string, val any) bool {
			if arr, ok := val.([]any); ok {
				seq.Values = append(seq.Values, arr...)
			} else {
				seq.Values = append(seq.Values, val)
			}
			return true
		})
		if len(seq.Values) == 0 {
			return nil, nil
		}
		if err := env.CheckSequence(len(seq.Values)); err != nil {
			return nil, err
		}
		if len(seq.Values) == 1 {
			return seq.Values[0], nil
		}
		return CollapseSequence(seq), nil
	}
	switch v := input.(type) {
	case []any:
		seq := CreateSequence()
		for _, item := range v {
			if IsMap(item) {
				val, err := evalWildcard(nil, item, env)
				if err != nil {
					return nil, err
				}
				if val != nil {
					appendToSequence(seq, val)
				}
			} else if item != nil {
				seq.Values = append(seq.Values, item)
			}
		}
		if err := env.CheckSequence(len(seq.Values)); err != nil {
			return nil, err
		}
		return CollapseSequence(seq), nil
	case ConsArray:
		return evalWildcard(nil, []any(v), env)
	default:
		return nil, nil
	}
}
