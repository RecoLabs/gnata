package evaluator

import (
	"encoding/json"
	"math"
	"slices"

	"github.com/recolabs/gnata/internal/parser"
)

// Null is the singleton JSONata null value.
var Null any = JSONNull{}

// JSONNull is a sentinel type that represents JSON null explicitly,
// distinguishing it from Go nil (which represents JSONata undefined).
type JSONNull struct{}

func (JSONNull) MarshalJSON() ([]byte, error) { return []byte(parser.NullJSON), nil }

// IsNull reports whether v is the JSON null sentinel.
func IsNull(v any) bool {
	_, ok := v.(JSONNull)
	return ok
}

// ConsArray is a JSONata constructed array (`[...]` used as a path step).
// It is indexable like []any, but it is one value in a path: a later step
// does not auto-map into its elements the way it maps a result sequence.
type ConsArray []any

// AsArray returns the slice behind a []any or ConsArray.
func AsArray(v any) ([]any, bool) {
	switch a := v.(type) {
	case []any:
		return a, true
	case ConsArray:
		return []any(a), true
	}
	return nil, false
}

// StripCons converts ConsArray values to []any, recursively. Used at the
// public Eval boundary so callers see ordinary slices.
func StripCons(v any) any {
	switch a := v.(type) {
	case ConsArray:
		return stripConsSlice([]any(a))
	case []any:
		return stripConsSliceIfNeeded(a)
	default:
		return v
	}
}

func stripConsSlice(s []any) []any {
	out := make([]any, len(s))
	for i, e := range s {
		out[i] = StripCons(e)
	}
	return out
}

func stripConsSliceIfNeeded(s []any) []any {
	if slices.ContainsFunc(s, containsCons) {
		return stripConsSlice(s)
	}
	return s
}

func containsCons(v any) bool {
	switch a := v.(type) {
	case ConsArray:
		return true
	case []any:
		return slices.ContainsFunc(a, containsCons)
	}
	return false
}

// Sequence is the core multi-value container used throughout evaluation.
// It represents an ordered collection of values that may be collapsed to a
// single value or remain as a sequence depending on context.
type Sequence struct {
	Values        []any
	KeepSingleton bool // do NOT unwrap single-element sequences
	ConsArray     bool // explicitly constructed via [...]; prevents flattening
	OuterWrapper  bool // input was a JSON array; treated as a single document
	TupleStream   bool // contains tuple objects {"@": value, varName: value}
}

// CreateSequence creates a Sequence optionally pre-populated with one value.
func CreateSequence(items ...any) *Sequence {
	s := &Sequence{Values: make([]any, 0, len(items)+4)}
	s.Values = append(s.Values, items...)
	return s
}

// CollapseSequence applies JSONata singleton-collapsing rules:
//   - len 0 → nil (undefined)
//   - len 1 → elem[0] unless KeepSingleton is set
//   - len > 1 → []any(seq.Values) — ownership transfer; callers must not mutate
func CollapseSequence(s *Sequence) any {
	switch len(s.Values) {
	case 0:
		return nil
	case 1:
		if s.KeepSingleton {
			return []any{s.Values[0]}
		}
		return s.Values[0]
	default:
		return slices.Clip(s.Values)
	}
}

// CollapseAndKeep normalizes a function call result for callers that need
// KeepArray (the [] suffix) support. Builtins returning *Sequence rely on
// CollapseSequence; when keepArray is true, singletons are preserved as
// one-element arrays instead of being unwrapped.
func CollapseAndKeep(result any, keepArray bool) any {
	if seq, ok := result.(*Sequence); ok {
		if keepArray {
			s := *seq
			s.KeepSingleton = true
			seq = &s
		}
		result = CollapseSequence(seq)
	}
	if keepArray {
		switch result.(type) {
		case []any:
			return result
		case nil:
			return nil
		default:
			return []any{result}
		}
	}
	return result
}

// IsArray returns true for []any and ConsArray values (not *Sequence).
func IsArray(v any) bool {
	_, ok := AsArray(v)
	return ok
}

// CollapseToSlice returns the sequence values as a plain []any slice.
// Ownership transfer; callers must not mutate the returned slice.
func CollapseToSlice(s *Sequence) []any {
	return slices.Clip(s.Values)
}

// AppendItems returns the items $append adds for v, as jsonata-js fn.append
// does: an array's or sequence's items, nothing for undefined, or v itself.
func AppendItems(v any) []any {
	if v == nil {
		return []any{}
	}
	if arr, ok := AsArray(v); ok {
		return arr
	}
	if seq, ok := v.(*Sequence); ok {
		return CollapseToSlice(seq)
	}
	return []any{v}
}

// EachLeaf calls visit, in order, for every item of arr that is not an
// array, descending up to depth levels (-1 for all) into nested arrays. It
// walks iteratively so that deep nesting cannot overflow the stack, and
// checks the evaluation's deadline as it goes, as nested arrays can share
// items.
func EachLeaf(arr []any, depth int, env *Environment, visit func(item any) error) error {
	type frame struct {
		items       []any
		next, depth int
	}
	stack := []frame{{items: arr, depth: depth}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next == len(top.items) {
			stack = stack[:len(stack)-1]
			continue
		}
		item := top.items[top.next]
		top.next++
		if err := env.Err(); err != nil {
			return err
		}
		if nested, ok := AsArray(item); ok && top.depth != 0 {
			stack = append(stack, frame{items: nested, depth: max(top.depth-1, -1)})
			continue
		}
		if err := visit(item); err != nil {
			return err
		}
	}
	return nil
}

// ToFloat64 converts a numeric value to float64, handling both float64 and json.Number.
func ToFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// IsNumeric returns true for finite numeric values (float64 or json.Number).
func IsNumeric(v any) bool {
	switch n := v.(type) {
	case float64:
		return !math.IsInf(n, 0) && !math.IsNaN(n)
	case json.Number:
		_, err := n.Float64()
		return err == nil
	}
	return false
}

// ToBoolean implements JSONata boolean casting rules.
func ToBoolean(v any) bool {
	if v == nil || IsNull(v) {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val != ""
	case float64:
		return val != 0 && !math.IsNaN(val)
	case json.Number:
		f, err := val.Float64()
		return err == nil && f != 0
	case *OrderedMap:
		return val.Len() > 0
	case map[string]any:
		return len(val) > 0
	case []any:
		return arrayToBoolean(val)
	case ConsArray:
		return arrayToBoolean(val)
	case *Sequence:
		return ToBoolean(CollapseSequence(val))
	}
	return false
}

func arrayToBoolean(val []any) bool {
	switch len(val) {
	case 0:
		return false
	case 1:
		return ToBoolean(val[0])
	default:
		return slices.ContainsFunc(val, ToBoolean)
	}
}

func normalizeNumber(v any) any {
	if n, ok := v.(json.Number); ok {
		f, err := n.Float64()
		if err != nil {
			return v
		}
		return f
	}
	return v
}

// DeepEqual implements JSONata structural equality.
func DeepEqual(a, b any) bool {
	return DeepEqualPrec(a, b, 0)
}

// DeepEqualPrec is DeepEqual comparing numbers in decimal to prec significant
// digits, or in float64 when prec is 0.
func DeepEqualPrec(a, b any, prec int) bool { //nolint:gocyclo // type-switch fast path adds branches but not real complexity
	if prec > 0 {
		if equal, ok := decimalEqual(a, b, prec); ok {
			return equal
		}
	}
	switch av := a.(type) {
	case float64:
		if bv, ok := b.(float64); ok {
			return av == bv
		}
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	}

	a, b = normalizeNumber(a), normalizeNumber(b)
	if a == nil || b == nil || IsNull(a) || IsNull(b) {
		return a == nil && b == nil || IsNull(a) && IsNull(b)
	}
	switch av := a.(type) {
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case []any, ConsArray:
		bv, ok := AsArray(b)
		avSlice, _ := AsArray(av)
		if !ok || len(avSlice) != len(bv) {
			return false
		}
		for i := range avSlice {
			if !DeepEqualPrec(avSlice[i], bv[i], prec) {
				return false
			}
		}
		return true
	case map[string]any:
		if !IsMap(b) || MapLen(b) != len(av) {
			return false
		}
		for k, va := range av {
			vb, exists := MapGet(b, k)
			if !exists || !DeepEqualPrec(va, vb, prec) {
				return false
			}
		}
		return true
	case *OrderedMap:
		if !IsMap(b) || MapLen(b) != av.Len() {
			return false
		}
		equal := true
		av.Range(func(k string, va any) bool {
			vb, exists := MapGet(b, k)
			if !exists || !DeepEqualPrec(va, vb, prec) {
				equal = false
				return false
			}
			return true
		})
		return equal
	}
	return false
}

// JSONataError is the structured error type used throughout evaluation.
// Code matches the JSONata spec error codes (S0xxx, T0xxx, T1xxx, T2xxx, D1xxx, D2xxx, D3xxx).
type JSONataError struct {
	Code    string
	Token   string
	Value   any
	Message string
}

func (e *JSONataError) Error() string {
	if e.Message != "" && e.Code != "" {
		return e.Code + ": " + e.Message
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// ToIntClamped truncates a JSONata number to int, saturating outside the int32
// range. A plain int() of an out-of-range float is implementation-defined, and
// int is only 32 bits on some WebAssembly targets, so indexes, limits and
// widths beyond ±2^31 (all of which already mean "unbounded") are clamped.
func ToIntClamped(f float64) int {
	switch {
	case math.IsNaN(f):
		return 0
	case f >= math.MaxInt32:
		return math.MaxInt32
	case f <= math.MinInt32:
		return math.MinInt32
	}
	return int(f)
}
