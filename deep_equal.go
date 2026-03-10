package gnata

import (
	"encoding/json"

	"github.com/recolabs/gnata/internal/evaluator"
)

// deepEqualInternal is the external-facing deep equality that normalizes the
// JSONata null sentinel to Go nil. This lets callers compare evaluator output
// against json.Unmarshal'd expectations (where JSON null becomes Go nil).
// The internal evaluator.DeepEqual remains strict (null ≠ undefined).
func deepEqualInternal(a, b any) bool {
	a = normalizeNull(a)
	b = normalizeNull(b)
	return deepEqNorm(a, b)
}

func normalizeNull(v any) any {
	if evaluator.IsNull(v) {
		return nil
	}
	if n, ok := v.(json.Number); ok {
		f, err := n.Float64()
		if err == nil {
			return f
		}
	}
	return v
}

func deepEqNorm(a, b any) bool {
	a = normalizeNull(a)
	b = normalizeNull(b)
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
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
			if !deepEqNorm(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		if !evaluator.IsMap(b) || evaluator.MapLen(b) != len(av) {
			return false
		}
		for k, va := range av {
			vb, exists := evaluator.MapGet(b, k)
			if !exists || !deepEqNorm(normalizeNull(va), normalizeNull(vb)) {
				return false
			}
		}
		return true
	case *evaluator.OrderedMap:
		if !evaluator.IsMap(b) || evaluator.MapLen(b) != av.Len() {
			return false
		}
		equal := true
		av.Range(func(k string, va any) bool {
			vb, exists := evaluator.MapGet(b, k)
			if !exists || !deepEqNorm(normalizeNull(va), normalizeNull(vb)) {
				equal = false
				return false
			}
			return true
		})
		return equal
	default:
		return evaluator.DeepEqual(a, b)
	}
}
