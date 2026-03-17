package gnata

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
	"github.com/tidwall/gjson"
)

func isJSONArray(r *gjson.Result) bool {
	return r.Type == gjson.JSON && r.Raw != "" && r.Raw[0] == '['
}

func isJSONObject(r *gjson.Result) bool {
	return r.Type == gjson.JSON && r.Raw != "" && r.Raw[0] == '{'
}

// collectNumbers extracts all numeric values from a JSON array result.
// Returns (numbers, true) if all elements are numbers, or (nil, false)
// if any non-number element is found.
func collectNumbers(r *gjson.Result) ([]float64, bool) {
	arr := r.Array()
	nums := make([]float64, 0, len(arr))
	for _, elem := range arr {
		if elem.Type != gjson.Number {
			return nil, false
		}
		nums = append(nums, elem.Float())
	}
	return nums, true
}

// funcFastHandler evaluates a fast-path function against a resolved gjson.Result.
// Returns (result, handled, error).
type funcFastHandler func(r *gjson.Result, f *parser.FuncFastPath) (any, bool, error)

// funcFastHandlers maps each FuncFastKind to its handler. Using a dispatch map
// instead of a giant switch keeps per-handler complexity low and avoids exhaustive
// lint violations on the FuncFastKind enum (new kinds that aren't ready for fast-path
// simply fall through to full evaluation).
// Note: FuncFastRound is intentionally absent — it requires banker's rounding
// which the full evaluator handles correctly.
var funcFastHandlers = map[parser.FuncFastKind]funcFastHandler{
	parser.FuncFastExists:    evalFuncExists,
	parser.FuncFastContains:  evalFuncContains,
	parser.FuncFastString:    evalFuncString,
	parser.FuncFastBoolean:   evalFuncBoolean,
	parser.FuncFastNumber:    evalFuncNumber,
	parser.FuncFastKeys:      evalFuncKeys,
	parser.FuncFastDistinct:  evalFuncDistinct,
	parser.FuncFastNot:       evalFuncNot,
	parser.FuncFastLowercase: evalFuncLowercase,
	parser.FuncFastUppercase: evalFuncUppercase,
	parser.FuncFastTrim:      evalFuncTrim,
	parser.FuncFastLength:    evalFuncLength,
	parser.FuncFastType:      evalFuncType,
	parser.FuncFastAbs:       evalFuncAbs,
	parser.FuncFastFloor:     evalFuncFloor,
	parser.FuncFastCeil:      evalFuncCeil,
	parser.FuncFastSqrt:      evalFuncSqrt,
	parser.FuncFastCount:     evalFuncCount,
	parser.FuncFastReverse:   evalFuncReverse,
	parser.FuncFastSum:       evalFuncSum,
	parser.FuncFastMax:       evalFuncMax,
	parser.FuncFastMin:       evalFuncMin,
	parser.FuncFastAverage:   evalFuncAverage,
}

func evalFunc(f *parser.FuncFastPath, data json.RawMessage, mapData map[string]json.RawMessage) (result any, handled bool, err error) {
	r := resolveGjsonPath(data, mapData, f.Path)
	if !r.Exists() {
		// Fall through to full evaluator — gjson doesn't auto-map through
		// arrays, so the path might still resolve via the AST walker.
		return nil, false, nil
	}
	if h, ok := funcFastHandlers[f.Kind]; ok {
		return h(&r, f)
	}
	return nil, false, nil
}

func evalFuncExists(_ *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	return true, true, nil
}

func evalFuncContains(r *gjson.Result, f *parser.FuncFastPath) (result any, handled bool, err error) {
	//nolint:exhaustive // only handle types relevant to this fast path
	switch r.Type {
	case gjson.String:
		return strings.Contains(r.Str, f.StrArg), true, nil
	case gjson.JSON:
		if isJSONArray(r) {
			found := false
			r.ForEach(func(_, elem gjson.Result) bool {
				if elem.Type == gjson.String && strings.Contains(elem.Str, f.StrArg) {
					found = true
					return false
				}
				return true
			})
			return found, true, nil
		}
		return nil, false, nil
	default:
		return nil, false, nil
	}
}

func evalFuncString(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	switch r.Type {
	case gjson.String:
		return r.Str, true, nil
	case gjson.Number:
		return evaluator.FormatNumber(json.Number(r.Raw)), true, nil
	case gjson.True:
		return "true", true, nil
	case gjson.False:
		return "false", true, nil
	case gjson.Null:
		return parser.NullJSON, true, nil
	case gjson.JSON:
		return nil, false, nil
	default:
		return nil, false, nil
	}
}

func evalFuncBoolean(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	switch r.Type {
	case gjson.True:
		return true, true, nil
	case gjson.False:
		return false, true, nil
	case gjson.Null:
		return false, true, nil
	case gjson.String:
		return r.Str != "", true, nil
	case gjson.Number:
		return r.Float() != 0, true, nil
	case gjson.JSON:
		return nil, false, nil
	default:
		return nil, false, nil
	}
}

func evalFuncNumber(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	//nolint:exhaustive // only handle types relevant to this fast path
	switch r.Type {
	case gjson.Number:
		return r.Float(), true, nil
	case gjson.String:
		v, parseErr := strconv.ParseFloat(r.Str, 64)
		if parseErr != nil || math.IsInf(v, 0) || math.IsNaN(v) {
			// Fall through to full evaluator on parse failure or non-finite value.
			return nil, false, nil //nolint:nilerr // intentional: signal fallback, not a real error
		}
		return v, true, nil
	case gjson.True:
		return float64(1), true, nil
	case gjson.False:
		return float64(0), true, nil
	default:
		return nil, false, nil
	}
}

func evalFuncKeys(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if isJSONObject(r) {
		var keys []any
		r.ForEach(func(key, _ gjson.Result) bool {
			keys = append(keys, key.String())
			return true
		})
		switch len(keys) {
		case 0:
			return nil, true, nil
		case 1:
			return keys[0], true, nil
		default:
			return keys, true, nil
		}
	}
	return nil, false, nil
}

func evalFuncDistinct(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if isJSONArray(r) {
		seen := map[string]struct{}{}
		out := make([]any, 0)
		hasComplex := false
		inputLen := 0
		r.ForEach(func(_, elem gjson.Result) bool {
			inputLen++
			var key string
			//nolint:exhaustive // only handle scalar types; complex types fall through
			switch elem.Type {
			case gjson.Number:
				key = strconv.FormatFloat(elem.Float(), 'f', -1, 64)
			case gjson.String:
				key = "s:" + elem.Str
			case gjson.True:
				key = "b:true"
			case gjson.False:
				key = "b:false"
			case gjson.Null:
				key = parser.NullJSON
			default:
				hasComplex = true
				return false
			}
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				out = append(out, gjsonValueToAny(&elem))
			}
			return true
		})
		if hasComplex {
			return nil, false, nil
		}
		// Singleton unwrap: mirrors *Sequence + CollapseSequence in the full
		// evaluator path. inputLen > 1 corresponds to the len(arr) <= 1
		// early-return guard in fnDistinct that skips Sequence wrapping
		// when no dedup was needed.
		if len(out) == 1 && inputLen > 1 {
			return out[0], true, nil
		}
		return out, true, nil
	}
	return nil, false, nil
}

func evalFuncNot(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	switch r.Type {
	case gjson.True:
		return false, true, nil
	case gjson.False:
		return true, true, nil
	case gjson.Null:
		return true, true, nil
	case gjson.String:
		return r.Str == "", true, nil
	case gjson.Number:
		return r.Float() == 0, true, nil
	case gjson.JSON:
		return nil, false, nil
	default:
		return nil, false, nil
	}
}

func evalFuncLowercase(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if r.Type == gjson.String {
		return strings.ToLower(r.Str), true, nil
	}
	return nil, false, nil
}

func evalFuncUppercase(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if r.Type == gjson.String {
		return strings.ToUpper(r.Str), true, nil
	}
	return nil, false, nil
}

func evalFuncTrim(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if r.Type == gjson.String {
		return strings.Join(strings.Fields(r.Str), " "), true, nil
	}
	return nil, false, nil
}

func evalFuncLength(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if r.Type == gjson.String {
		return float64(utf8.RuneCountInString(r.Str)), true, nil
	}
	return nil, false, nil
}

func evalFuncType(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	switch r.Type {
	case gjson.String:
		return "string", true, nil
	case gjson.Number:
		return "number", true, nil
	case gjson.True, gjson.False:
		return "boolean", true, nil
	case gjson.Null:
		return parser.NullJSON, true, nil
	case gjson.JSON:
		if r.Raw != "" {
			switch r.Raw[0] {
			case '[':
				return "array", true, nil
			case '{':
				return "object", true, nil
			}
		}
		return nil, false, nil
	default:
		return nil, false, nil
	}
}

func evalFuncAbs(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if r.Type == gjson.Number {
		return math.Abs(r.Float()), true, nil
	}
	return nil, false, nil
}

func evalFuncFloor(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if r.Type == gjson.Number {
		return math.Floor(r.Float()), true, nil
	}
	return nil, false, nil
}

func evalFuncCeil(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if r.Type == gjson.Number {
		return math.Ceil(r.Float()), true, nil
	}
	return nil, false, nil
}

func evalFuncSqrt(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if r.Type == gjson.Number {
		v := r.Float()
		if v < 0 {
			return nil, false, nil
		}
		return math.Sqrt(v), true, nil
	}
	return nil, false, nil
}

func evalFuncCount(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if isJSONArray(r) {
		count := 0
		r.ForEach(func(_, _ gjson.Result) bool {
			count++
			return true
		})
		return float64(count), true, nil
	}
	return float64(1), true, nil
}

func evalFuncReverse(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if isJSONArray(r) {
		elems := make([]any, 0)
		r.ForEach(func(_, elem gjson.Result) bool {
			elems = append(elems, gjsonValueToAny(&elem))
			return true
		})
		slices.Reverse(elems)
		return elems, true, nil
	}
	return nil, false, nil
}

func evalFuncSum(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if isJSONArray(r) {
		nums, ok := collectNumbers(r)
		if !ok {
			return nil, false, nil
		}
		sum := 0.0
		for _, n := range nums {
			sum += n
		}
		return sum, true, nil
	}
	return nil, false, nil
}

func evalFuncMax(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if isJSONArray(r) {
		nums, ok := collectNumbers(r)
		if !ok {
			return nil, false, nil
		}
		if len(nums) == 0 {
			return nil, true, nil
		}
		return slices.Max(nums), true, nil
	}
	return nil, false, nil
}

func evalFuncMin(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if isJSONArray(r) {
		nums, ok := collectNumbers(r)
		if !ok {
			return nil, false, nil
		}
		if len(nums) == 0 {
			return nil, true, nil
		}
		return slices.Min(nums), true, nil
	}
	return nil, false, nil
}

func evalFuncAverage(r *gjson.Result, _ *parser.FuncFastPath) (result any, handled bool, err error) {
	if isJSONArray(r) {
		nums, ok := collectNumbers(r)
		if !ok || len(nums) == 0 {
			return nil, false, nil
		}
		sum := 0.0
		for _, n := range nums {
			sum += n
		}
		return sum / float64(len(nums)), true, nil
	}
	return nil, false, nil
}
