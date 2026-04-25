package functions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
)

// ── $string ──────────────────────────────────────────────────────────────────

func fnString(args []any, focus any) (any, error) {
	if len(args) == 0 {
		if focus == nil {
			return nil, nil
		}
		switch focus.(type) {
		case evaluator.BuiltinFunction, evaluator.EnvAwareBuiltin, *evaluator.Lambda, *evaluator.SignedBuiltin:
			return nil, nil
		}
		return valueToString(focus, false)
	}
	arg := args[0]
	if arg == nil {
		return nil, nil // undefined → undefined
	}
	if len(args) > 2 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$string: takes at most 2 arguments"}
	}
	prettify := false
	if len(args) >= 2 && args[1] != nil {
		switch v := args[1].(type) {
		case bool:
			prettify = v
		case evaluator.BuiltinFunction, evaluator.EnvAwareBuiltin, *evaluator.Lambda, *evaluator.SignedBuiltin:
			return nil, &evaluator.JSONataError{Code: "D3011", Message: "$string: second argument cannot be a function"}
		default:
			return nil, &evaluator.JSONataError{Code: "T0410", Message: fmt.Sprintf("$string: second argument must be a boolean, got %T", v)}
		}
	}
	return valueToString(arg, prettify)
}

func valueToString(v any, prettify bool) (string, error) {
	if evaluator.IsNull(v) {
		return parser.NullJSON, nil
	}
	switch val := v.(type) {
	case string:
		return val, nil
	case json.Number:
		return evaluator.FormatNumber(val), nil
	case float64:
		if math.IsInf(val, 0) || math.IsNaN(val) {
			return "", &evaluator.JSONataError{Code: "D3001", Message: "Number out of range"}
		}
		return evaluator.FormatFloat(val), nil
	case bool:
		if val {
			return "true", nil
		}
		return "false", nil
	case nil:
		return "", nil // undefined → caller returns nil
	case evaluator.BuiltinFunction, evaluator.EnvAwareBuiltin, *evaluator.Lambda, *evaluator.SignedBuiltin:
		return "", nil // functions serialize as empty string in JSONata
	case *evaluator.Sequence:
		return valueToString(evaluator.CollapseSequence(val), prettify)
	default:
		sanitized := sanitizeForJSON(v)
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if prettify {
			enc.SetIndent("", "  ")
		}
		if err := enc.Encode(sanitized); err != nil {
			return "", &evaluator.JSONataError{Code: "D1001", Message: "Number out of range"}
		}
		return strings.TrimRight(buf.String(), "\n"), nil
	}
}

// sanitizeForJSON replaces function values with "" so they can be JSON-marshaled.
// For *OrderedMap, returns a new *OrderedMap preserving insertion order.
func sanitizeForJSON(v any) any {
	if evaluator.IsNull(v) {
		return nil
	}
	switch val := v.(type) {
	case *evaluator.Sequence:
		return sanitizeForJSON(evaluator.CollapseSequence(val))
	case evaluator.BuiltinFunction, evaluator.EnvAwareBuiltin, *evaluator.Lambda, *evaluator.SignedBuiltin:
		return ""
	case *evaluator.OrderedMap:
		out := evaluator.NewOrderedMapWithCapacity(val.Len())
		val.Range(func(k string, v any) bool {
			out.Set(k, sanitizeForJSON(v))
			return true
		})
		return out
	case map[string]any:
		out := evaluator.NewOrderedMapWithCapacity(len(val))
		for _, k := range evaluator.MapKeys(val) {
			out.Set(k, sanitizeForJSON(val[k]))
		}
		return out
	case []any:
		out := make([]any, 0, len(val))
		for _, v := range val {
			out = append(out, sanitizeForJSON(v))
		}
		return out
	default:
		return v
	}
}

// ── $length ───────────────────────────────────────────────────────────────────

func fnLength(args []any, _ any) (any, error) {
	switch len(args) {
	case 0:
		return nil, &evaluator.JSONataError{Code: "T0411", Message: "$length: argument 1 is required"}
	case 1:
		if args[0] == nil {
			return nil, nil // propagate undefined
		}
		if s, ok := args[0].(string); ok {
			return float64(utf8.RuneCountInString(s)), nil
		}
		return nil, &evaluator.JSONataError{Code: "T0410", Message: fmt.Sprintf("$length: argument must be a string, got %T", args[0])}
	default:
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$length: takes exactly 1 argument"}
	}
}

// ── $substring ────────────────────────────────────────────────────────────────

func fnSubstring(args []any, _ any) (any, error) {
	if len(args) < 2 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$substring: requires at least 2 arguments"}
	}
	if len(args) > 3 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$substring: too many arguments"}
	}
	if args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$substring: argument 1 must be a string"}
	}
	startF, startOk := evaluator.ToFloat64(args[1])
	if !startOk {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$substring: argument 2 must be a number"}
	}

	var lengthF float64
	hasLength := len(args) >= 3 && args[2] != nil
	if hasLength {
		var ok2 bool
		lengthF, ok2 = evaluator.ToFloat64(args[2])
		if !ok2 {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$substring: argument 3 must be a number"}
		}
	}

	runes := []rune(s)
	n := len(runes)
	start := int(startF)

	if start < 0 {
		start = max(n+start, 0)
	}
	if start >= n {
		return "", nil
	}

	if !hasLength {
		return string(runes[start:]), nil
	}
	length := int(lengthF)
	if length < 0 {
		return "", nil
	}
	return string(runes[start:min(start+length, n)]), nil
}

// ── $substringBefore / $substringAfter ────────────────────────────────────────

// substringCutFunc selects which part of strings.Cut to return.
type substringCutFunc func(before, after string) string

func fnSubstringCut(name string, cutFn substringCutFunc) func([]any, any) (any, error) {
	return func(args []any, focus any) (any, error) {
		if len(args) > 2 {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: name + ": too many arguments"}
		}
		var str, sep any
		fromContext := false
		switch len(args) {
		case 0:
			return nil, &evaluator.JSONataError{Code: "T0411", Message: name + ": requires 2 arguments"}
		case 1:
			str = focus
			sep = args[0]
			fromContext = true
		default:
			str = args[0]
			sep = args[1]
		}
		if str == nil {
			return nil, nil
		}
		s, ok1 := str.(string)
		if !ok1 {
			code := "T0410"
			if fromContext {
				code = "T0411"
			}
			return nil, &evaluator.JSONataError{Code: code, Message: name + ": argument 1 must be a string"}
		}
		sep2, ok2 := sep.(string)
		if !ok2 {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: name + ": argument 2 must be a string"}
		}
		if before, after, ok := strings.Cut(s, sep2); ok {
			return cutFn(before, after), nil
		}
		return s, nil
	}
}

var (
	fnSubstringBefore = fnSubstringCut("$substringBefore", func(before, _ string) string { return before })
	fnSubstringAfter  = fnSubstringCut("$substringAfter", func(_, after string) string { return after })
)

// ── $uppercase / $lowercase / $trim ──────────────────────────────────────────

func fnUppercase(args []any, focus any) (any, error) {
	var val any
	if len(args) == 0 {
		val = focus
	} else {
		val = args[0]
	}
	if val == nil {
		return nil, nil
	}
	s, ok := val.(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$uppercase: argument must be a string"}
	}
	return strings.ToUpper(s), nil
}

func fnLowercase(args []any, focus any) (any, error) {
	var val any
	if len(args) == 0 {
		val = focus
	} else {
		val = args[0]
	}
	if val == nil {
		return nil, nil
	}
	s, ok := val.(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$lowercase: argument must be a string"}
	}
	return strings.ToLower(s), nil
}

func fnTrim(args []any, focus any) (any, error) {
	var val any
	if len(args) == 0 {
		val = focus
	} else {
		val = args[0]
	}
	if val == nil {
		return nil, nil
	}
	s, ok := val.(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$trim: argument must be a string"}
	}
	return strings.Join(strings.Fields(s), " "), nil
}

// ── $pad ──────────────────────────────────────────────────────────────────────

func fnPad(args []any, _ any) (any, error) {
	if len(args) < 2 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$pad: requires at least 2 arguments"}
	}
	if args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$pad: argument 1 must be a string"}
	}
	widthF, widthOk := evaluator.ToFloat64(args[1])
	if !widthOk {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$pad: argument 2 must be a number"}
	}
	width := int(widthF)

	const maxPadWidth = 10_000
	if width > maxPadWidth || width < -maxPadWidth {
		return nil, &evaluator.JSONataError{Code: "D3010", Message: fmt.Sprintf("$pad: width argument exceeds maximum of %d", maxPadWidth)}
	}

	padStr := " "
	if len(args) >= 3 && args[2] != nil {
		p, ok := args[2].(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$pad: argument 3 must be a string"}
		}
		if p == "" {
			padStr = " "
		} else {
			padStr = p
		}
	}

	runes := []rune(s)
	sLen := len(runes)
	absWidth := max(width, -width)
	if sLen >= absWidth {
		return s, nil
	}
	needed := absWidth - sLen
	padRunes := []rune(padStr)
	padBuf := make([]rune, needed)
	for i := range needed {
		padBuf[i] = padRunes[i%len(padRunes)]
	}
	padding := string(padBuf)
	if width > 0 {
		return s + padding, nil
	}
	return padding + s, nil
}

// ── $contains ─────────────────────────────────────────────────────────────────

func fnContains(args []any, focus any) (any, error) {
	// When called as a path step (e.g., str.$contains("x")), focus holds the
	// path context; prepend it as the first argument so the function receives
	// the string to search in.
	if len(args) == 1 && focus != nil {
		args = []any{focus, args[0]}
	}
	if len(args) < 2 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$contains: requires 2 arguments"}
	}
	if args[0] == nil {
		return nil, nil
	}

	// JSONata auto-maps over arrays: $contains(["a","b","c"], "b") evaluates
	// $contains on each string element (single level only, no recursion into
	// nested arrays). Returns true if any element matches — this matches the
	// predicate-context semantics used by the reference implementation.
	if _, isStr := args[0].(string); !isStr {
		if arr := tryAsArray(args[0]); arr != nil {
			for _, elem := range arr {
				s, ok := elem.(string)
				if !ok {
					continue
				}
				result, err := fnContains([]any{s, args[1]}, nil)
				if err != nil {
					return nil, err
				}
				if b, ok := result.(bool); ok && b {
					return true, nil
				}
			}
			return false, nil
		}
	}

	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$contains: argument 1 must be a string"}
	}

	switch p := args[1].(type) {
	case string:
		return strings.Contains(s, p), nil
	case map[string]any:
		re, err := compileRegex(p)
		if err != nil {
			return nil, err
		}
		matched, matchErr := re.MatchString(s)
		if matchErr != nil {
			return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("regex error: %v", matchErr)}
		}
		return matched, nil
	default:
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$contains: argument 2 must be a string or regex"}
	}
}

// ── $split ────────────────────────────────────────────────────────────────────

func fnSplit(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$split: requires at least 2 arguments"}
	}
	if args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: fmt.Sprintf("$split: argument 1 must be a string, got %T", args[0])}
	}
	if len(args) < 2 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$split: requires at least 2 arguments"}
	}

	limit := -1
	if len(args) >= 3 && args[2] != nil {
		lf, ok := args[2].(float64)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$split: argument 3 must be a number"}
		}
		limit = int(lf)
		if limit < 0 {
			return nil, &evaluator.JSONataError{Code: "D3020", Message: "$split: limit must be a non-negative integer"}
		}
	}

	var parts []string
	switch p := args[1].(type) {
	case string:
		if limit >= 0 {
			parts = strings.SplitN(s, p, limit+1)
			if len(parts) > limit {
				parts = parts[:limit]
			}
		} else {
			parts = strings.Split(s, p)
		}
	case map[string]any:
		re, err := compileRegex(p)
		if err != nil {
			return nil, err
		}
		var splitErr error
		parts, splitErr = splitRegex(re, s, limit)
		if splitErr != nil {
			return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("regex error: %v", splitErr)}
		}
	default:
		switch args[1].(type) {
		case evaluator.BuiltinFunction, evaluator.EnvAwareBuiltin, *evaluator.Lambda, *evaluator.SignedBuiltin:
			return nil, &evaluator.JSONataError{Code: "T1010", Message: "$split: second argument must be a string or regex"}
		default:
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$split: second argument must be a string or regex"}
		}
	}

	result := make([]any, len(parts))
	for i, p := range parts {
		result[i] = p
	}
	return result, nil
}

// ── $join ─────────────────────────────────────────────────────────────────────

func fnJoin(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$join: argument 1 is required"}
	}
	if args[0] == nil {
		return nil, nil
	}
	if s, ok := args[0].(string); ok {
		return s, nil
	}
	arr := wrapArray(args[0])

	sep := ""
	if len(args) >= 2 && args[1] != nil {
		s, ok := args[1].(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$join: argument 2 must be a string"}
		}
		sep = s
	}

	parts := make([]string, 0, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0412", Message: fmt.Sprintf("$join: array element %d must be a string", i)}
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, sep), nil
}
