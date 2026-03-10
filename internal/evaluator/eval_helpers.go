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

func stringifyValue(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	switch val := v.(type) {
	case string:
		return val, nil
	case json.Number:
		return FormatNumber(val), nil
	case float64:
		return FormatFloat(val), nil
	case bool:
		if val {
			return "true", nil
		}
		return "false", nil
	default:
		b, err := marshalNoHTMLEscape(v)
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
			switch op {
			case "<":
				return ls < rs, nil
			case "<=":
				return ls <= rs, nil
			case ">":
				return ls > rs, nil
			case ">=":
				return ls >= rs, nil
			}
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

func compareOrder(a, b any) (int, error) {
	if a == nil && b == nil {
		return 0, nil
	}
	if a == nil {
		return 1, nil
	}
	if b == nil {
		return -1, nil
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

func containsValue(arr, elem any) bool {
	if arr == nil {
		return false
	}
	switch v := arr.(type) {
	case []any:
		for _, item := range v {
			if DeepEqual(item, elem) {
				return true
			}
		}
	case *Sequence:
		for _, item := range v.Values {
			if DeepEqual(item, elem) {
				return true
			}
		}
	default:
		return DeepEqual(arr, elem)
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
	case *Sequence:
		return evalName(node, CollapseSequence(v), nil)
	default:
		return nil, nil
	}
}

func evalWildcard(_ *parser.Node, input any, _ *Environment) (any, error) {
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
				val, err := evalWildcard(nil, item, nil)
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
		return CollapseSequence(seq), nil
	default:
		return nil, nil
	}
}
