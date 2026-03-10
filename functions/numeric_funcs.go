package functions

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"

	"github.com/recolabs/gnata/internal/evaluator"
)

// ── $number ───────────────────────────────────────────────────────────────────

func fnNumber(args []any, focus any) (any, error) {
	var arg any
	switch len(args) {
	case 0:
		arg = focus
	case 1:
		arg = args[0]
	default:
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$number: too many arguments"}
	}
	if arg == nil {
		return nil, nil
	}
	if evaluator.IsNull(arg) {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$number: cannot cast null to number"}
	}
	switch v := arg.(type) {
	case float64:
		return v, nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil, &evaluator.JSONataError{Code: "D3030", Message: fmt.Sprintf("$number: unable to cast %q to a number", v.String())}
		}
		return f, nil
	case string:
		s := strings.TrimSpace(v)
		// Support hex (0x/0X), binary (0b/0B), octal (0o/0O) prefixes
		if len(s) >= 2 && s[0] == '0' {
			switch s[1] {
			case 'x', 'X':
				if n, err := strconv.ParseInt(s[2:], 16, 64); err == nil {
					return float64(n), nil
				}
				return nil, &evaluator.JSONataError{Code: "D3030", Message: fmt.Sprintf("$number: unable to cast %q to a number", v)}
			case 'b', 'B':
				if n, err := strconv.ParseInt(s[2:], 2, 64); err == nil {
					return float64(n), nil
				}
				return nil, &evaluator.JSONataError{Code: "D3030", Message: fmt.Sprintf("$number: unable to cast %q to a number", v)}
			case 'o', 'O':
				if n, err := strconv.ParseInt(s[2:], 8, 64); err == nil {
					return float64(n), nil
				}
				return nil, &evaluator.JSONataError{Code: "D3030", Message: fmt.Sprintf("$number: unable to cast %q to a number", v)}
			}
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, &evaluator.JSONataError{Code: "D3030", Message: fmt.Sprintf("$number: unable to cast %q to a number", v)}
		}
		return f, nil
	case bool:
		if v {
			return float64(1), nil
		}
		return float64(0), nil
	case []any:
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$number: cannot cast array to number"}
	case *evaluator.OrderedMap, map[string]any:
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$number: cannot cast object to number"}
	default:
		return nil, &evaluator.JSONataError{Code: "T0410", Message: fmt.Sprintf("$number: unsupported type %T", v)}
	}
}

// ── $abs ──────────────────────────────────────────────────────────────────────

func fnAbs(args []any, _ any) (any, error) {
	n, ok, err := requireNumber(args, "$abs")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return math.Abs(n), nil
}

// ── $floor ────────────────────────────────────────────────────────────────────

func fnFloor(args []any, _ any) (any, error) {
	n, ok, err := requireNumber(args, "$floor")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return math.Floor(n), nil
}

// ── $ceil ─────────────────────────────────────────────────────────────────────

func fnCeil(args []any, _ any) (any, error) {
	n, ok, err := requireNumber(args, "$ceil")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return math.Ceil(n), nil
}

// ── $round ────────────────────────────────────────────────────────────────────

func fnRound(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$round: argument is required"}
	}
	if args[0] == nil {
		return nil, nil
	}
	n, nOk := evaluator.ToFloat64(args[0])
	if !nOk {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$round: argument must be a number"}
	}
	scale := 0
	if len(args) >= 2 && args[1] != nil {
		sf, sfOk := evaluator.ToFloat64(args[1])
		if !sfOk {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$round: scale argument must be a number"}
		}
		scale = int(sf)
	}
	return bankersRound(n, scale), nil
}

// bankersRound rounds n to scale decimal places using "round half to even".
// To avoid floating-point precision artifacts (e.g. 4.525 * 100 = 452.5000000006),
// we work from the shortest decimal string representation of n.
func bankersRound(n float64, scale int) float64 {
	if scale >= 0 {
		return bankersRoundDecimal(n, scale)
	}
	// Negative scale: round to nearest 10^|scale|.
	mult := math.Pow(10, float64(-scale))
	scaled := n / mult
	rounded := bankersRoundDecimal(scaled, 0)
	return rounded * mult
}

// bankersRoundDecimal rounds n to `places` decimal places (places >= 0).
// Uses the shortest decimal string of n to avoid IEEE 754 precision artifacts.
func bankersRoundDecimal(n float64, places int) float64 {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return n
	}
	negative := n < 0
	if negative {
		n = -n
	}
	// Format with enough precision to see all relevant digits without premature rounding.
	// Use -1 (shortest) to get the canonical decimal representation.
	str := strconv.FormatFloat(n, 'f', -1, 64)
	dotIdx := strings.Index(str, ".")
	if dotIdx < 0 {
		// Whole number: pad decimal part.
		str += "."
		dotIdx = len(str) - 1
	}
	decimals := str[dotIdx+1:] // digits after the dot (may be empty)

	// Get the digit at position `places` (0-indexed in decimals).
	var roundDigit int
	if places >= len(decimals) {
		// No digit at that position: no rounding needed, already at or below precision.
		result, _ := strconv.ParseFloat(str, 64)
		if negative {
			result = -result
		}
		return result
	}
	roundDigit = int(decimals[places] - '0')

	// Truncate to `places` decimal digits.
	truncated := str[:dotIdx+1+places]
	if places == 0 {
		truncated = str[:dotIdx]
	}
	base, _ := strconv.ParseFloat(truncated, 64)

	switch {
	case roundDigit < 5:
		// Round down (keep base).
	case roundDigit > 5:
		// Round up.
		base += math.Pow(10, -float64(places))
	default:
		// Exactly 5: check if any digit after roundDigit is non-zero.
		hasRemainder := false
		for i := places + 1; i < len(decimals); i++ {
			if decimals[i] != '0' {
				hasRemainder = true
				break
			}
		}
		if hasRemainder {
			// Not exactly 0.5: round up.
			base += math.Pow(10, -float64(places))
		} else {
			// Exactly 0.5: round to even (look at the last kept digit).
			var lastDigit int
			if places == 0 {
				// Rounding to integer: last kept digit is just before the dot.
				if dotIdx > 0 {
					lastDigit = int(str[dotIdx-1] - '0')
				}
			} else {
				// Last kept digit is at position dotIdx+places in the original string.
				pos := dotIdx + places
				if pos < len(str) {
					lastDigit = int(str[pos] - '0')
				}
			}
			if lastDigit%2 != 0 {
				// Last digit is odd: round up.
				base += math.Pow(10, -float64(places))
			}
			// If even: keep base (round down).
		}
	}
	// Re-format to the correct precision to avoid floating-point trailing error.
	result, _ := strconv.ParseFloat(strconv.FormatFloat(base, 'f', places, 64), 64)
	if negative {
		result = -result
	}
	return result
}

// ── $power ────────────────────────────────────────────────────────────────────

func fnPower(args []any, _ any) (any, error) {
	if len(args) < 2 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$power: requires 2 arguments"}
	}
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	base, ok1 := evaluator.ToFloat64(args[0])
	exp, ok2 := evaluator.ToFloat64(args[1])
	if !ok1 || !ok2 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$power: arguments must be numbers"}
	}
	result := math.Pow(base, exp)
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return nil, &evaluator.JSONataError{Code: "D3061", Message: "$power: result is non-finite"}
	}
	return result, nil
}

// ── $sqrt ─────────────────────────────────────────────────────────────────────

func fnSqrt(args []any, _ any) (any, error) {
	n, ok, err := requireNumber(args, "$sqrt")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	if n < 0 {
		return nil, &evaluator.JSONataError{Code: "D3060", Message: "$sqrt: square root of a negative number"}
	}
	return math.Sqrt(n), nil
}

// ── $random ───────────────────────────────────────────────────────────────────

func fnRandom(_ []any, _ any) (any, error) {
	return rand.Float64(), nil
}

// ── $sum ──────────────────────────────────────────────────────────────────────

func fnSum(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$sum: argument 1 is required"}
	}
	if len(args) > 1 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$sum: expects 1 argument"}
	}
	if args[0] == nil {
		return nil, nil
	}
	arr := toNumberArray(args[0])
	if arr == nil {
		return nil, &evaluator.JSONataError{Code: "T0412", Message: "$sum: argument must be an array of numbers"}
	}
	if len(arr) == 0 {
		return float64(0), nil
	}
	sum := 0.0
	for _, v := range arr {
		n, ok := evaluator.ToFloat64(v)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0412", Message: "$sum: array element is not a number"}
		}
		sum += n
	}
	return sum, nil
}

// ── $max ──────────────────────────────────────────────────────────────────────

func fnMax(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$max: argument 1 is required"}
	}
	if len(args) > 1 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$max: expects 1 argument"}
	}
	if args[0] == nil {
		return nil, nil
	}
	arr := toNumberArray(args[0])
	if arr == nil {
		return nil, &evaluator.JSONataError{Code: "T0412", Message: "$max: argument must be an array of numbers"}
	}
	if len(arr) == 0 {
		return nil, nil
	}
	maxVal := math.Inf(-1)
	for _, v := range arr {
		n, ok := evaluator.ToFloat64(v)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0412", Message: "$max: array element is not a number"}
		}
		if n > maxVal {
			maxVal = n
		}
	}
	return maxVal, nil
}

// ── $min ──────────────────────────────────────────────────────────────────────

func fnMin(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$min: argument 1 is required"}
	}
	if len(args) > 1 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$min: expects 1 argument"}
	}
	if args[0] == nil {
		return nil, nil
	}
	arr := toNumberArray(args[0])
	if arr == nil {
		return nil, &evaluator.JSONataError{Code: "T0412", Message: "$min: argument must be an array of numbers"}
	}
	if len(arr) == 0 {
		return nil, nil
	}
	minVal := math.Inf(1)
	for _, v := range arr {
		n, ok := evaluator.ToFloat64(v)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0412", Message: "$min: array element is not a number"}
		}
		if n < minVal {
			minVal = n
		}
	}
	return minVal, nil
}

// ── $average ──────────────────────────────────────────────────────────────────

func fnAverage(args []any, _ any) (any, error) {
	if len(args) == 0 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$average: argument 1 is required"}
	}
	if len(args) > 1 {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$average: expects 1 argument"}
	}
	if args[0] == nil {
		return nil, nil
	}
	arr := toNumberArray(args[0])
	if arr == nil {
		return nil, &evaluator.JSONataError{Code: "T0412", Message: "$average: argument must be an array of numbers"}
	}
	if len(arr) == 0 {
		return nil, nil
	}
	sum := 0.0
	for _, v := range arr {
		n, ok := evaluator.ToFloat64(v)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0412", Message: "$average: array element is not a number"}
		}
		sum += n
	}
	return sum / float64(len(arr)), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// requireNumber extracts a float64 from the first argument.
// Returns (0, false, nil) if the argument is nil/undefined (caller should propagate nil).
// Returns (0, false, error) for missing or wrong-type arguments.
// Returns (n, true, nil) for a valid number.
func requireNumber(args []any, fn string) (n float64, ok bool, err error) {
	if len(args) == 0 {
		return 0, false, &evaluator.JSONataError{Code: "T0410", Message: fn + ": argument is required"}
	}
	if args[0] == nil {
		return 0, false, nil // propagate undefined
	}
	v, isNum := evaluator.ToFloat64(args[0])
	if !isNum {
		return 0, false, &evaluator.JSONataError{Code: "T0410", Message: fmt.Sprintf("%s: argument must be a number, got %T", fn, args[0])}
	}
	return v, true, nil
}

// toNumberArray normalises a single number or []any into []any.
// Returns nil if the input is neither.
func toNumberArray(v any) []any {
	switch val := v.(type) {
	case []any:
		return val
	case float64:
		return []any{val}
	case json.Number:
		return []any{val}
	case *evaluator.Sequence:
		return evaluator.CollapseToSlice(val)
	default:
		return nil
	}
}
