package evaluator

import (
	"github.com/recolabs/gnata/internal/parser"
)

// deepClone copies the objects and arrays of v. It fills the copy's nested
// ones from an explicit stack of slots rather than by recursion, so a deeply
// nested value cannot overflow the goroutine stack.
func deepClone(v any) any {
	var buf [8]cloneSlot
	out, pending := cloneValue(v, buf[:0])
	for len(pending) > 0 {
		slot := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		var val any
		val, pending = cloneValue(slot.src, pending)
		slot.fill(val)
	}
	return out
}

// cloneSlot is a place in a copied object or array still to be filled with
// the copy of src, itself an object or an array.
type cloneSlot struct {
	src     any
	array   []any
	index   int
	ordered *OrderedMap
	plain   map[string]any
	key     string
}

func (s *cloneSlot) fill(v any) {
	switch {
	case s.ordered != nil:
		s.ordered.data[s.key] = v
	case s.plain != nil:
		s.plain[s.key] = v
	default:
		s.array[s.index] = v
	}
}

// cloneValue copies v. Of a copied object or array it sets the values that
// are neither, and appends slots for the rest to pending.
func cloneValue(v any, pending []cloneSlot) (any, []cloneSlot) {
	switch val := v.(type) {
	case *OrderedMap:
		m := NewOrderedMapWithCapacity(val.Len())
		for _, k := range val.keys {
			item := val.data[k]
			if isClonable(item) {
				pending = append(pending, cloneSlot{src: item, ordered: m, key: k})
			}
			m.Set(k, item)
		}
		return m, pending
	case map[string]any:
		m := make(map[string]any, len(val))
		for k, item := range val {
			if isClonable(item) {
				pending = append(pending, cloneSlot{src: item, plain: m, key: k})
			}
			m[k] = item
		}
		return m, pending
	case []any:
		s := make([]any, len(val))
		for i, item := range val {
			if isClonable(item) {
				pending = append(pending, cloneSlot{src: item, array: s, index: i})
			}
			s[i] = item
		}
		return s, pending
	}
	return v, pending
}

func isClonable(v any) bool {
	switch v.(type) {
	case *OrderedMap, map[string]any, []any:
		return true
	}
	return false
}

func evalTransform(node *parser.Node, _ any, env *Environment) (any, error) {
	return BuiltinFunction(func(args []any, focus any) (any, error) {
		var doc any
		if len(args) > 0 {
			doc = args[0]
		} else {
			doc = focus
		}
		return applyTransform(node, doc, env)
	}), nil
}

var (
	errTransformUpdate = JSONataError{
		Code: "T2011", Message: "the insert/update clause of the transform expression must evaluate to an object",
	}
	errTransformDelete = JSONataError{
		Code: "T2012", Message: "the delete clause of the transform expression must evaluate to an array of strings",
	}
)

func applyTransform(node *parser.Node, input any, env *Environment) (any, error) {
	if input == nil {
		return nil, nil
	}
	if !IsMap(input) && !IsArray(input) {
		return nil, &JSONataError{Code: "T0410", Message: "the transform expression must be applied to an object or an array"}
	}
	cloned := deepClone(input)

	matched, err := Eval(node.Pattern, cloned, env)
	if err != nil {
		return nil, err
	}

	var targets []any
	switch m := matched.(type) {
	case *OrderedMap:
		targets = []any{m}
	case map[string]any:
		targets = []any{m}
	case []any:
		for _, item := range m {
			if IsMap(item) {
				targets = append(targets, item)
			}
		}
	}

	if len(targets) == 0 && matched != nil {
		// Pattern matched a non-object value: validate update/delete types
		// but don't mutate (original jsonata-js behavior for non-object patterns).
		return cloned, validateTransformClauses(node, cloned, env)
	}

	for _, target := range targets {
		if err := applyTransformTarget(node, target, env); err != nil {
			return nil, err
		}
	}
	return cloned, nil
}

// validateTransformClauses evaluates update/delete clauses for type-checking only,
// without mutating the target. Used when the pattern matches a non-object value.
func validateTransformClauses(node *parser.Node, target any, env *Environment) error {
	if node.Update != nil {
		if updateVal, err := Eval(node.Update, target, env); err != nil {
			return err
		} else if updateVal != nil && !IsNull(updateVal) && !IsMap(updateVal) {
			return &errTransformUpdate
		}
	}
	if node.Delete != nil {
		if deleteVal, err := Eval(node.Delete, target, env); err != nil {
			return err
		} else if deleteVal != nil && !IsNull(deleteVal) {
			switch deleteVal.(type) {
			case []any, string:
			default:
				return &errTransformDelete
			}
		}
	}
	return nil
}

func applyTransformTarget(node *parser.Node, target any, env *Environment) error {
	if node.Update != nil {
		if updateVal, err := Eval(node.Update, target, env); err != nil {
			return err
		} else if updateVal != nil && !IsNull(updateVal) {
			if !IsMap(updateVal) {
				return &errTransformUpdate
			}
			transformMerge(target, updateVal)
		}
	}
	if node.Delete != nil {
		if deleteVal, err := Eval(node.Delete, target, env); err != nil {
			return err
		} else if deleteVal != nil && !IsNull(deleteVal) {
			switch dv := deleteVal.(type) {
			case []any:
				for _, f := range dv {
					if s, ok := f.(string); ok {
						transformDelete(target, s)
					}
				}
			case string:
				transformDelete(target, dv)
			default:
				return &errTransformDelete
			}
		}
	}
	return nil
}

func transformMerge(target, update any) {
	MapRange(update, func(k string, v any) bool {
		switch t := target.(type) {
		case *OrderedMap:
			t.Set(k, v)
		case map[string]any:
			t[k] = v
		}
		return true
	})
}

func transformDelete(target any, key string) {
	switch t := target.(type) {
	case *OrderedMap:
		t.Delete(key)
	case map[string]any:
		delete(t, key)
	}
}
