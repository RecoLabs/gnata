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
	if r.Exists() {
		if h, ok := funcFastHandlers[f.Kind]; ok {
			return h(&r, f)
		}
		return nil, false, nil
	}
	// A single dotted-path lookup doesn't auto-map through arrays. Walk the
	// path step-by-step so an array anywhere in the chain still avoids a
	// full document decode, for the kinds whose semantics over a resolved
	// sequence are unambiguous: $exists just needs definedness, the numeric
	// aggregates and $contains already auto-map over a sequence in the
	// full evaluator (see their non-walked handlers above). The remaining
	// kinds (string/boolean coercions, $keys, $distinct, etc.) are left to
	// the full evaluator: their behavior over a multi-element sequence isn't
	// a simple per-element reduction, so walking them here would risk a
	// result that silently disagrees with the AST.
	if len(f.PathSteps) == 0 {
		return nil, false, nil
	}
	if f.Kind == parser.FuncFastExists {
		exists := pathExistsWalked(f, data, mapData)
		return exists, true, nil
	}
	values, ok := walkedValues(f, data, mapData)
	if !ok {
		return nil, false, nil
	}
	//nolint:exhaustive // only the aggregate and contains kinds are handled; other kinds fall through to the (nil, false, nil) fallback below
	switch f.Kind {
	case parser.FuncFastSum, parser.FuncFastCount, parser.FuncFastMax, parser.FuncFastMin, parser.FuncFastAverage:
		if aggResult, aggOK := aggregateFastValues(f.Kind, values); aggOK {
			return aggResult, true, nil
		}
	case parser.FuncFastContains:
		return containsAnyValue(values, f.StrArg), true, nil
	}
	return nil, false, nil
}

// walkedValues resolves f's primary-argument path across array boundaries,
// against whichever of data / mapData the caller has available.
func walkedValues(f *parser.FuncFastPath, data json.RawMessage, mapData map[string]json.RawMessage) ([]gjson.Result, bool) {
	if data != nil {
		return walkPureStepsValues(f.PathSteps, data)
	}
	if mapData != nil {
		return walkPureStepsMapValues(f.PathSteps, mapData)
	}
	return nil, false
}

// pathExistsWalked reports whether f's path resolves to a defined value once
// array boundaries are accounted for. The walker's "cannot resolve" outcome
// is exactly JSONata's undefined for a pure field-name chain, so this needs
// no full-evaluator fallback in either direction.
func pathExistsWalked(f *parser.FuncFastPath, data json.RawMessage, mapData map[string]json.RawMessage) bool {
	_, ok := walkedValues(f, data, mapData)
	return ok
}

// containsAnyValue reports whether any string element of values contains
// substr, matching $contains's existing any-match semantics over a sequence.
func containsAnyValue(values []gjson.Result, substr string) bool {
	for _, v := range values {
		if v.Type == gjson.String && strings.Contains(v.Str, substr) {
			return true
		}
	}
	return false
}

// aggregateFastValues computes a numeric aggregate fast path over an
// already-resolved slice of gjson values. Returns (nil, false) when the kind
// isn't a supported aggregate or an element isn't numeric.
func aggregateFastValues(kind parser.FuncFastKind, values []gjson.Result) (any, bool) {
	if kind == parser.FuncFastCount {
		return float64(len(values)), true
	}
	nums, ok := collectNumbersFrom(values)
	if !ok {
		return nil, false
	}
	//nolint:exhaustive // Count is already handled above; only the remaining numeric kinds reach here
	switch kind {
	case parser.FuncFastSum:
		var sum float64
		for _, n := range nums {
			sum += n
		}
		return sum, true
	case parser.FuncFastAverage:
		if len(nums) == 0 {
			return nil, false
		}
		var sum float64
		for _, n := range nums {
			sum += n
		}
		return sum / float64(len(nums)), true
	case parser.FuncFastMax:
		if len(nums) == 0 {
			return nil, true
		}
		return slices.Max(nums), true
	case parser.FuncFastMin:
		if len(nums) == 0 {
			return nil, true
		}
		return slices.Min(nums), true
	default:
		return nil, false
	}
}

// collectNumbersFrom extracts numeric values from an already-collected slice
// of gjson results. Returns (nil, false) if any element isn't a number.
func collectNumbersFrom(values []gjson.Result) ([]float64, bool) {
	nums := make([]float64, 0, len(values))
	for _, v := range values {
		if v.Type != gjson.Number {
			return nil, false
		}
		nums = append(nums, v.Float())
	}
	return nums, true
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
