package evaluator

import (
	"encoding/json"
	"math"
	"slices"

	"github.com/recolabs/gnata/internal/parser"
)

// Null is the singleton JSONata null value.
var Null any = jsonNullType{}

// JSONNull is a sentinel type that represents JSON null explicitly,
// distinguishing it from Go nil (which represents JSONata undefined).
type jsonNullType struct{}

func (jsonNullType) MarshalJSON() ([]byte, error) { return []byte(parser.NullJSON), nil }

// IsNull reports whether v is the JSON null sentinel.
func IsNull(v any) bool {
	_, ok := v.(jsonNullType)
	return ok
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

// IsSequence reports whether v is a *Sequence.
func IsSequence(v any) bool {
	_, ok := v.(*Sequence)
	return ok
}

// CollapseSequence applies JSONata singleton-collapsing rules:
//   - len 0 → nil (undefined)
//   - len 1 → elem[0] unless KeepSingleton
//   - len > 1 → []any(seq.Values)
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
		return slices.Clone(s.Values)
	}
}

// IsArray returns true for []any values (not *Sequence).
func IsArray(v any) bool {
	_, ok := v.([]any)
	return ok
}

// CollapseToSlice returns the sequence values as a plain []any slice.
func CollapseToSlice(s *Sequence) []any {
	return slices.Clone(s.Values)
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

// CheckNumeric validates that v is a finite numeric value. Returns a D1001 error for Inf/NaN.
func CheckNumeric(v any) error {
	switch n := v.(type) {
	case float64:
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return &JSONataError{Code: "D1001", Value: v}
		}
	case json.Number:
		f, err := n.Float64()
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return &JSONataError{Code: "D1001", Value: v}
		}
	}
	return nil
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
		return val != 0
	case json.Number:
		f, err := val.Float64()
		return err == nil && f != 0
	case *OrderedMap:
		return val.Len() > 0
	case map[string]any:
		return len(val) > 0
	case []any:
		switch len(val) {
		case 0:
			return false
		case 1:
			return ToBoolean(val[0])
		default:
			return slices.ContainsFunc(val, ToBoolean)
		}
	case *Sequence:
		return ToBoolean(CollapseSequence(val))
	}
	return false
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
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !DeepEqual(av[i], bv[i]) {
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
			if !exists || !DeepEqual(va, vb) {
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
			if !exists || !DeepEqual(va, vb) {
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
