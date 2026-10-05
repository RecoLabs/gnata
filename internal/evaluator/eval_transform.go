package evaluator

import (
	"reflect"

	"github.com/recolabs/gnata/internal/parser"
)

// maxValueDepth bounds CloneValue's and JSONValue's recursion, so a value
// nested deeper than this is reported instead of overflowing the Go stack.
const maxValueDepth = 100_000

// cycleCheckDepth is the depth past which valuePath records the objects it
// walks, so a value that contains itself (which a transform can build) is
// caught one cycle later instead of copied up to maxValueDepth times;
// ordinary values never reach it.
const cycleCheckDepth = 1000

// valuePath tracks the depth of a walk over a value and, past
// cycleCheckDepth, the objects open on the current path.
type valuePath struct {
	what  string // the walk, for errors: "clone" or "stringify"
	depth int
	open  map[any]struct{}
}

// enter descends into container, reporting U1001 when it is nested too
// deeply or already open on the path.
func (p *valuePath) enter(container any) error {
	p.depth++
	if p.depth > maxValueDepth {
		return &JSONataError{Code: "U1001", Message: "value is nested too deeply to " + p.what}
	}
	if p.depth <= cycleCheckDepth {
		return nil
	}
	key := objectIdentity(container)
	if key == nil {
		return nil
	}
	if _, open := p.open[key]; open {
		return &JSONataError{Code: "U1001", Message: "cannot " + p.what + " a value that contains itself"}
	}
	if p.open == nil {
		p.open = make(map[any]struct{})
	}
	p.open[key] = struct{}{}
	return nil
}

// leave returns from the container enter descended into.
func (p *valuePath) leave(container any) {
	if p.depth > cycleCheckDepth {
		if key := objectIdentity(container); key != nil {
			delete(p.open, key)
		}
	}
	p.depth--
}

// objectIdentity returns a comparable identity for an object, or nil for
// other values. A value can only contain itself through an object, which a
// transform modifies in place.
func objectIdentity(v any) any {
	switch val := v.(type) {
	case *OrderedMap:
		return val
	case map[string]any:
		return reflect.ValueOf(val).UnsafePointer()
	}
	return nil
}

// CloneValue deep-copies v as jsonata-js's $clone does through JSON, where a
// function becomes "". Numbers and other Go values, including nil (which
// is null in Go input), are kept as they are, so no precision is lost.
func CloneValue(v any) (any, error) {
	return cloneValue(v, &valuePath{what: "clone"})
}

func cloneValue(v any, path *valuePath) (any, error) {
	switch val := v.(type) {
	case *OrderedMap:
		if err := path.enter(val); err != nil {
			return nil, err
		}
		defer path.leave(val)
		m := NewOrderedMapWithCapacity(val.Len())
		var err error
		val.Range(func(k string, vv any) bool {
			var c any
			if c, err = cloneValue(vv, path); err != nil {
				return false
			}
			m.Set(k, c)
			return true
		})
		return m, err
	case map[string]any:
		if err := path.enter(val); err != nil {
			return nil, err
		}
		defer path.leave(val)
		m := make(map[string]any, len(val))
		for k, vv := range val {
			c, err := cloneValue(vv, path)
			if err != nil {
				return nil, err
			}
			m[k] = c
		}
		return m, nil
	case []any:
		return cloneArray(val, path)
	case ConsArray:
		return cloneArray(val, path)
	case *Sequence:
		return cloneValue(CollapseSequence(val), path)
	default:
		if isCallable(v) {
			return "", nil
		}
		return v, nil
	}
}

func cloneArray(arr []any, path *valuePath) ([]any, error) {
	if err := path.enter(nil); err != nil {
		return nil, err
	}
	defer path.leave(nil)
	out := make([]any, len(arr))
	for i, v := range arr {
		c, err := cloneValue(v, path)
		if err != nil {
			return nil, err
		}
		out[i] = c
	}
	return out, nil
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
	// jsonata-js copies the input with whatever $clone is bound to. The
	// clauses below modify the copy, so a rebound $clone is given, and its
	// result taken as, a copy gnata owns: input can be shared by concurrent
	// evaluations.
	cloneFn, _ := env.Lookup("clone")
	if !isCallable(cloneFn) {
		return nil, &JSONataError{Code: "T2013", Message: "the transform expression requires $clone to be a function"}
	}
	cloned, err := CloneValue(input)
	if err != nil {
		return nil, err
	}
	if sb, isBuiltin := cloneFn.(*SignedBuiltin); !isBuiltin || sb.Name != "clone" {
		result, err := callFunction(cloneFn, []any{cloned}, Null, env)
		if err != nil {
			return nil, err
		}
		if cloned, err = CloneValue(result); err != nil {
			return nil, err
		}
	}

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
