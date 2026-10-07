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

// fnString is $string, laying out numbers, including those inside arrays
// and objects, in decimal to env's precision, or through float64 when
// decimal precision is off.
func fnString(args []any, _ any, env *evaluator.Environment) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil // undefined → undefined
	}
	arg := args[0]
	if len(args) > 2 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$string: takes at most 2 arguments"}
	}
	prettify := false
	if len(args) >= 2 && args[1] != nil {
		switch v := args[1].(type) {
		case bool:
			prettify = v
		default:
			if evaluator.IsFunction(v) {
				return nil, &evaluator.JSONataError{Code: "D3011", Message: "$string: second argument cannot be a function"}
			}
			return nil, &evaluator.JSONataError{Code: "T0410", Message: fmt.Sprintf("$string: second argument must be a boolean, got %T", v)}
		}
	}
	return valueToString(arg, prettify, env)
}

func valueToString(v any, prettify bool, env *evaluator.Environment) (string, error) {
	v = evaluator.CollapseSequences(v)
	prec := env.DecimalPrecision()
	if evaluator.IsNull(v) {
		return parser.NullJSON, nil
	}
	if evaluator.IsFunction(v) {
		return "", nil // functions serialize as empty string in JSONata
	}
	switch val := v.(type) {
	case string:
		return val, nil
	case json.Number:
		if s, ok := evaluator.FormatDecimal(val, prec); ok {
			return s, nil
		}
		return evaluator.FormatNumber(val), nil
	case float64:
		if s, ok := evaluator.FormatDecimal(val, prec); ok {
			return s, nil
		}
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
	default:
		prepared, err := evaluator.JSONValue(v, env)
		if err != nil {
			return "", err
		}
		out, err := evaluator.AppendJSON(nil, prepared)
		if err != nil {
			return "", &evaluator.JSONataError{Code: "D1001", Message: "Number out of range"}
		}
		if prettify {
			var buf bytes.Buffer
			if err := json.Indent(&buf, out, "", "  "); err != nil {
				return "", &evaluator.JSONataError{Code: "D1001", Message: "Number out of range"}
			}
			return buf.String(), nil
		}
		return string(out), nil
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
	var startF, lengthF float64
	hasStart := args[1] != nil
	hasLength := len(args) >= 3 && args[2] != nil
	if hasStart {
		var ok bool
		if startF, ok = evaluator.ToFloat64(args[1]); !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$substring: argument 2 must be a number"}
		}
	}
	if hasLength {
		var ok bool
		if lengthF, ok = evaluator.ToFloat64(args[2]); !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$substring: argument 3 must be a number"}
		}
	}
	if !hasStart {
		// jsonata-js slices from an undefined start, so there is no end
		// position for a length to count from.
		if hasLength {
			return "", nil
		}
		return s, nil
	}

	runes := []rune(s)
	n := len(runes)
	// Bounded to the string length so start+length cannot overflow int.
	start := min(max(evaluator.ToIntClamped(startF), -n-1), n+1)

	if start < 0 {
		start = max(n+start, 0)
	}
	if start >= n {
		return "", nil
	}

	if !hasLength {
		return string(runes[start:]), nil
	}
	length := min(max(evaluator.ToIntClamped(lengthF), -1), n+1)
	if length < 0 {
		return "", nil
	}
	return string(runes[start:min(start+length, n)]), nil
}

// ── $substringBefore / $substringAfter ────────────────────────────────────────

// substringCutFunc selects which part of strings.Cut to return.
type substringCutFunc func(before, after string) string

func fnSubstringCut(name string, cutFn substringCutFunc) func([]any, any) (any, error) {
	return func(args []any, _ any) (any, error) {
		if len(args) > 2 {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: name + ": too many arguments"}
		}
		if len(args) == 0 || args[0] == nil {
			return nil, nil
		}
		s, isString := args[0].(string)
		if !isString {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: name + ": argument 1 must be a string"}
		}
		// jsonata-js searches for an undefined separator as "undefined".
		sep := "undefined"
		if len(args) > 1 && args[1] != nil {
			if sep, isString = args[1].(string); !isString {
				return nil, &evaluator.JSONataError{Code: "T0410", Message: name + ": argument 2 must be a string"}
			}
		}
		if before, after, ok := strings.Cut(s, sep); ok {
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

func fnUppercase(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$uppercase: argument must be a string"}
	}
	return strings.ToUpper(s), nil
}

func fnLowercase(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$lowercase: argument must be a string"}
	}
	return strings.ToLower(s), nil
}

func fnTrim(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
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
	if args[1] == nil {
		return s, nil
	}
	widthF, widthOk := evaluator.ToFloat64(args[1])
	if !widthOk {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$pad: argument 2 must be a number"}
	}
	width := evaluator.ToIntClamped(widthF)

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
	// Direct calls arrive with the context already filled. A single argument
	// that does not match the signature, such as $contains(5), is reported
	// as argument 2 so the code is T0410, as in jsonata-js.
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

	if args[1] == nil {
		return nil, nil
	}

	switch p := args[1].(type) {
	case string:
		return strings.Contains(s, p), nil
	case *evaluator.RegexLiteral:
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

	limitArg, err := optionalLimit(args, 2, "$split", "D3020")
	if err != nil {
		return nil, err
	}

	var parts []string
	switch p := args[1].(type) {
	case string:
		// jsonata-js splits on a string with String.prototype.split, which
		// truncates a fractional limit.
		if limit := evaluator.ToIntClamped(limitArg); limit >= 0 {
			parts = strings.SplitN(s, p, limit+1)
			if len(parts) > limit {
				parts = parts[:limit]
			}
		} else {
			parts = strings.Split(s, p)
		}
	case *evaluator.RegexLiteral:
		re, err := compileRegex(p)
		if err != nil {
			return nil, err
		}
		if parts, err = splitRegex(re, s, matchCountLimit(limitArg)); err != nil {
			return nil, err
		}
	default:
		if evaluator.IsFunction(args[1]) {
			return nil, &evaluator.JSONataError{Code: "T1010", Message: "$split: second argument must be a string or regex"}
		}
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$split: second argument must be a string or regex"}
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
	arr := evaluator.AppendItems(args[0])

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
