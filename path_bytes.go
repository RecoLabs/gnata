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
// as json.Number. doc reports that root is the whole document, whose array
// jsonata-js wraps as one context.
//
// Mirrors evalName and nameOverContexts in internal/evaluator/eval_helpers.go
// on decoded values — keep the two in sync; see the note on evalName.
func walkPureSteps(steps []string, root *gjson.Result, doc, useNumber bool) (any, bool) {
	cur, ok := walkPureStepsResolvedFrom(steps, root, doc)
	if !ok {
		return nil, false
	}
	return finalizeStepValue(cur, useNumber), true
}

// stepValue applies field step to cur, the previous step's result, or the
// document's root value when doc is set. A step maps over an array of
// contexts (see stepArray), but the document's root array is one context.
func stepValue(step string, cur any, doc, last bool) (any, bool) {
	switch v := cur.(type) {
	case gjson.Result:
		switch {
		case !v.IsArray():
			return objectField(step, &v)
		case doc:
			return stepInArray(step, &v)
		}
		return stepArray(step, v.Array(), last)
	case []gjson.Result:
		return stepArray(step, v, last)
	default:
		return nil, false
	}
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

// stepArray applies a field-lookup step to every context in arr, flattening
// one level of each context's result into the output, as jsonata-js
// evaluateStep does. A nested array context's lookup is one collapsed
// sequence (see stepInArray), and a last step returns a lone context's
// array value as is.
func stepArray(step string, arr []gjson.Result, last bool) (any, bool) {
	frame := rawArrayFrame{flat: make([]gjson.Result, 0, len(arr))}
	defined := 0
	var lone any
	for i := range arr {
		item := &arr[i]
		var val any
		var ok bool
		if item.IsArray() {
			val, ok = stepInArray(step, item)
		} else {
			val, ok = objectField(step, item)
		}
		frame.add(val, ok)
		switch val.(type) {
		case gjson.Result, []gjson.Result:
			defined++
			lone = val
		}
	}
	if r, single := lone.(gjson.Result); last && defined == 1 && single && r.IsArray() {
		return r, true
	}
	return frame.result()
}

// stepInArray applies a field-lookup step to the array r taken as one
// context, as jsonata-js lookup does: the values found in its items and, at
// any depth, in their nested arrays form one sequence, collapsed once. An
// array that is valid JSON is walked once by stepNestedArray; any other is
// split with gjson, and so are the arrays inside it, without validating them
// again.
func stepInArray(step string, r *gjson.Result) (any, bool) {
	if validJSON(r.Raw) {
		return stepNestedArray(step, r.Raw)
	}
	var frame rawArrayFrame
	// The items still to visit of each enclosing array, kept on an explicit
	// stack rather than recursing, so deep nesting cannot overflow the
	// goroutine stack.
	stack := [][]gjson.Result{r.Array()}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		if len(top) == 0 {
			stack = stack[:len(stack)-1]
			continue
		}
		item := top[0]
		stack[len(stack)-1] = top[1:]
		if item.IsArray() {
			stack = append(stack, item.Array())
			continue
		}
		frame.add(objectField(step, &item))
	}
	return frame.result()
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
	return walkPureSteps(steps, &root, true, useNumber)
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
	return walkPureSteps(rest, &root, false, useNumber)
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
	return walkPureStepsValuesFrom(steps, &root, true)
}

// walkPureStepsMapValues is walkPureStepsValues for EvalMap's input shape.
func walkPureStepsMapValues(steps []string, mapData map[string]json.RawMessage) (values []gjson.Result, ok bool) {
	root, rest, firstOK := firstStepFromMap(steps, mapData)
	if !firstOK {
		return nil, false
	}
	return walkPureStepsValuesFrom(rest, &root, false)
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

func walkPureStepsValuesFrom(steps []string, root *gjson.Result, doc bool) (values []gjson.Result, ok bool) {
	cur, ok := walkPureStepsResolvedFrom(steps, root, doc)
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
		return walkPureStepsResolvedFrom(steps, &root, true)
	case mapData != nil:
		root, rest, ok := firstStepFromMap(steps, mapData)
		if !ok {
			return nil, false
		}
		return walkPureStepsResolvedFrom(rest, &root, false)
	default:
		return nil, false
	}
}

func walkPureStepsResolvedFrom(steps []string, root *gjson.Result, doc bool) (any, bool) {
	var cur any = *root
	for i, step := range steps {
		next, ok := stepValue(step, cur, doc && i == 0, i == len(steps)-1)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}
