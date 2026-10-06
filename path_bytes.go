package gnata

import (
	"encoding/json"

	"github.com/tidwall/gjson"
)

// walkPureSteps resolves a chain of pure field-name path steps against a
// gjson document, auto-mapping through arrays at every step exactly like the
// full evaluator does, without ever unmarshaling the document. Returns
// (value, false) when the walker cannot represent the result (a step landed
// on a scalar, or the field is genuinely absent) — the caller should fall
// back to full evaluation in that case. With useNumber set, numbers are returned
// as json.Number.
//
// Mirrors evalName's []any case in internal/evaluator/eval_helpers.go on
// decoded values — keep the two in sync; see the note on evalName.
func walkPureSteps(steps []string, root *gjson.Result, useNumber bool) (any, bool) {
	var cur any = *root
	for _, step := range steps {
		next, ok := stepValue(step, cur)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return finalizeStepValue(cur, useNumber), true
}

func stepValue(step string, cur any) (any, bool) {
	switch v := cur.(type) {
	case gjson.Result:
		return stepSingle(step, &v)
	case []gjson.Result:
		return stepArray(step, v)
	default:
		return nil, false
	}
}

func stepSingle(step string, r *gjson.Result) (any, bool) {
	if r.IsArray() {
		return stepArray(step, r.Array())
	}
	return objectField(step, r)
}

// objectField returns the value of field step in r, if r is an object.
func objectField(step string, r *gjson.Result) (any, bool) {
	if !r.IsObject() {
		return nil, false
	}
	// A literal-key scan via ForEach, not r.Get(step): Get treats its
	// argument as a full gjson path expression, where '.', '*', '?',
	// '#', '|', '!', brackets, and backslash are syntactically
	// significant. A field name containing any of those (e.g. "a.b")
	// would otherwise be silently misinterpreted as a nested/wildcard
	// path instead of the literal key JSONata means. ForEach with an
	// early exit avoids building the full key/value map just to read
	// one entry.
	var val gjson.Result
	found := false
	r.ForEach(func(key, value gjson.Result) bool {
		if key.Str == step {
			val, found = value, true
			return false
		}
		return true
	})
	if !found {
		return nil, false
	}
	return val, true
}

// stepArray applies a field-lookup step across every element of an array,
// flattening one level of nested-array results into the output — matching
// JSONata's array auto-mapping semantics.
func stepArray(step string, arr []gjson.Result) (any, bool) {
	frame := newStepFrame(arr)
	for frame.index < len(frame.items) {
		item := &frame.items[frame.index]
		frame.index++
		if item.IsArray() {
			return stepNested(step, &frame, item.Array())
		}
		frame.add(objectField(step, item))
	}
	return frame.result()
}

// stepNested continues stepArray from frame once an item of it is the array
// nested, keeping the frames of enclosing arrays on an explicit stack rather
// than recursing, so deep nesting cannot overflow the goroutine stack.
func stepNested(step string, frame *stepFrame, nested []gjson.Result) (any, bool) {
	var buf [4]stepFrame
	stack := append(buf[:0], *frame, newStepFrame(nested))
	for {
		top := &stack[len(stack)-1]
		if top.index == len(top.items) {
			val, ok := top.result()
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return val, ok
			}
			stack[len(stack)-1].add(val, ok)
			continue
		}
		item := &top.items[top.index]
		top.index++
		if item.IsArray() {
			stack = append(stack, newStepFrame(item.Array()))
			continue
		}
		top.add(objectField(step, item))
	}
}

// stepFrame is an array stepArray is mapping a field lookup over: its
// items, the index of the next one, and the values found so far.
type stepFrame struct {
	items []gjson.Result
	index int
	flat  []gjson.Result
	found bool
}

func newStepFrame(items []gjson.Result) stepFrame {
	return stepFrame{items: items, flat: make([]gjson.Result, 0, len(items))}
}

// add adds the lookup's value for one item to the frame.
func (f *stepFrame) add(val any, ok bool) {
	if !ok {
		return
	}
	f.found = true
	switch inner := val.(type) {
	case gjson.Result:
		if inner.IsArray() {
			f.flat = append(f.flat, inner.Array()...)
		} else {
			f.flat = append(f.flat, inner)
		}
	case []gjson.Result:
		f.flat = append(f.flat, inner...)
	}
}

// result is the lookup's value over the frame's array.
func (f *stepFrame) result() (any, bool) {
	switch {
	case len(f.flat) == 0 && f.found:
		return []gjson.Result{}, true
	case len(f.flat) == 0:
		return nil, false
	case len(f.flat) == 1:
		return f.flat[0], true
	default:
		return f.flat, true
	}
}

func finalizeStepValue(cur any, useNumber bool) any {
	switch v := cur.(type) {
	case gjson.Result:
		return gjsonValue(&v, useNumber)
	case []gjson.Result:
		out := make([]any, len(v))
		for i := range v {
			out[i] = gjsonValue(&v[i], useNumber)
		}
		return out
	default:
		return nil
	}
}

// walkPureStepsBytes resolves steps against raw JSON bytes. Returns
// (value, false) when the caller should fall back to full evaluation.
func walkPureStepsBytes(steps []string, data []byte, useNumber bool) (any, bool) {
	root := gjson.ParseBytes(data)
	if !root.Exists() {
		return nil, false
	}
	return walkPureSteps(steps, &root, useNumber)
}

// walkPureStepsMapBytes resolves steps against a map of top-level field names
// to raw JSON values (EvalMap's input shape). The first step is an O(1) map
// lookup by field name; remaining steps walk gjson.Result the same way as
// walkPureStepsBytes. Returns (value, false) when the caller should fall
// back to full evaluation.
func walkPureStepsMapBytes(steps []string, mapData map[string]json.RawMessage, useNumber bool) (any, bool) {
	root, rest, ok := firstStepFromMap(steps, mapData)
	if !ok {
		return nil, false
	}
	return walkPureSteps(rest, &root, useNumber)
}

// walkPureStepsValues resolves steps against raw JSON bytes and returns the
// leaf values as a []gjson.Result, for callers (aggregate and any-match fast
// paths) that need to reduce over the raw values themselves rather than a
// finalized Go value. A single scalar result is returned as a one-element
// slice; an array-typed result is exploded into its elements. ok is false
// when the walker cannot represent the result.
func walkPureStepsValues(steps []string, data []byte) (values []gjson.Result, ok bool) {
	root := gjson.ParseBytes(data)
	if !root.Exists() {
		return nil, false
	}
	return walkPureStepsValuesFrom(steps, &root)
}

// walkPureStepsMapValues is walkPureStepsValues for EvalMap's input shape.
func walkPureStepsMapValues(steps []string, mapData map[string]json.RawMessage) (values []gjson.Result, ok bool) {
	root, rest, firstOK := firstStepFromMap(steps, mapData)
	if !firstOK {
		return nil, false
	}
	return walkPureStepsValuesFrom(rest, &root)
}

// firstStepFromMap resolves the first path step via an O(1) map lookup,
// returning the parsed remainder as a gjson.Result root plus the remaining
// steps to walk from there.
func firstStepFromMap(steps []string, mapData map[string]json.RawMessage) (root gjson.Result, rest []string, ok bool) {
	if len(steps) == 0 || mapData == nil {
		return gjson.Result{}, nil, false
	}
	raw, exists := mapData[steps[0]]
	if !exists {
		return gjson.Result{}, nil, false
	}
	root = gjson.ParseBytes(raw)
	if !root.Exists() {
		return gjson.Result{}, nil, false
	}
	return root, steps[1:], true
}

func walkPureStepsValuesFrom(steps []string, root *gjson.Result) (values []gjson.Result, ok bool) {
	cur, ok := walkPureStepsResolvedFrom(steps, root)
	if !ok {
		return nil, false
	}
	switch v := cur.(type) {
	case []gjson.Result:
		return v, true
	case gjson.Result:
		if v.IsArray() {
			return v.Array(), true
		}
		return []gjson.Result{v}, true
	default:
		return nil, false
	}
}

func walkPureStepsResolved(steps []string, data json.RawMessage, mapData map[string]json.RawMessage) (any, bool) {
	switch {
	case data != nil:
		root := gjson.ParseBytes(data)
		if !root.Exists() {
			return nil, false
		}
		return walkPureStepsResolvedFrom(steps, &root)
	case mapData != nil:
		root, rest, ok := firstStepFromMap(steps, mapData)
		if !ok {
			return nil, false
		}
		return walkPureStepsResolvedFrom(rest, &root)
	default:
		return nil, false
	}
}

func walkPureStepsResolvedFrom(steps []string, root *gjson.Result) (any, bool) {
	var cur any = *root
	for _, step := range steps {
		next, ok := stepValue(step, cur)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}
