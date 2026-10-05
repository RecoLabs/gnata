package evaluator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"
)

// OrderedMap is a map that preserves insertion order for JSON serialization.
// Go has no stdlib ordered map; this is the minimal implementation needed so
// that objects created by JSONata expressions serialize keys in definition order.
// Input data from json.Unmarshal remains plain map[string]any — the helper
// functions (MapGet, MapKeys, etc.) bridge both types at call sites.
type OrderedMap struct {
	keys []string
	data map[string]any
	// frozen marks maps the evaluator decoded from its own input for the
	// duration of one evaluation call (DecodeInput, DecodeRawMap); evaluation
	// never mutates them. Only frozen maps cache their normalized form (see
	// CachedNormalizedView). Maps from the public DecodeJSON are never
	// frozen, since callers may keep and mutate them.
	frozen bool
	norm   atomic.Pointer[map[string]any]
}

func NewOrderedMap() *OrderedMap {
	return &OrderedMap{data: make(map[string]any)}
}

func NewOrderedMapWithCapacity(n int) *OrderedMap {
	return &OrderedMap{
		keys: make([]string, 0, n),
		data: make(map[string]any, n),
	}
}

func (m *OrderedMap) Set(key string, val any) {
	if m.frozen {
		m.frozen = false
		m.norm.Store(nil)
	}
	if _, exists := m.data[key]; !exists {
		m.keys = append(m.keys, key)
	}
	m.data[key] = val
}

func (m *OrderedMap) Get(key string) (any, bool) {
	v, ok := m.data[key]
	return v, ok
}

func (m *OrderedMap) Has(key string) bool {
	_, ok := m.data[key]
	return ok
}

func (m *OrderedMap) Delete(key string) {
	if m.frozen {
		m.frozen = false
		m.norm.Store(nil)
	}
	if _, ok := m.data[key]; !ok {
		return
	}
	delete(m.data, key)
	m.keys = slices.DeleteFunc(m.keys, func(k string) bool { return k == key })
}

// CachedNormalizedView returns the normalized view of m cached by
// CacheNormalizedView, if any, and whether m can cache one: only frozen maps
// do, so repeated custom-function calls over the same input object do not
// rebuild it. The view is shared and must be treated as read-only. These are
// functions rather than methods so they stay out of the public API of the
// gnata.OrderedMap alias.
func CachedNormalizedView(m *OrderedMap) (view map[string]any, cacheable bool) {
	if !m.frozen {
		return nil, false
	}
	if p := m.norm.Load(); p != nil {
		return *p, true
	}
	return nil, true
}

// CacheNormalizedView caches view, complete, as the normalized view of m if
// m is frozen.
func CacheNormalizedView(m *OrderedMap, view map[string]any) {
	if m.frozen {
		m.norm.Store(&view)
	}
}

func (m *OrderedMap) freeze() { m.frozen = true }

func (m *OrderedMap) Keys() []string        { return m.keys }
func (m *OrderedMap) Len() int              { return len(m.keys) }
func (m *OrderedMap) ToMap() map[string]any { return m.data }

// Range calls fn for each entry in insertion order.
func (m *OrderedMap) Range(fn func(key string, val any) bool) {
	for _, k := range m.keys {
		if !fn(k, m.data[k]) {
			break
		}
	}
}

// MarshalJSON implements json.Marshaler, preserving insertion order.
// json.MarshalIndent calls this then re-indents, so no separate indent method needed.
func (m *OrderedMap) MarshalJSON() ([]byte, error) {
	return AppendJSON(make([]byte, 0, 256), m)
}

// marshalNoHTMLEscape serializes v to JSON without escaping &, <, >.
func marshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// UnmarshalJSON implements json.Unmarshaler, preserving key order.
func (m *OrderedMap) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return &json.UnmarshalTypeError{Value: "non-object", Type: nil}
	}
	m.keys = nil
	m.data = make(map[string]any)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key := keyTok.(string)
		var val any
		if err := dec.Decode(&val); err != nil {
			return err
		}
		m.Set(key, val)
	}
	// Consume closing '}'
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

// DecodeJSON decodes a JSON value, using *OrderedMap for objects to preserve
// key insertion order. Arrays, strings, numbers, booleans, and null are
// decoded normally. This should be used instead of json.Unmarshal when key
// order matters (which is always the case for JSONata evaluation).
//
// The result belongs to the caller, who may keep and mutate it.
func DecodeJSON(b json.RawMessage) (any, error) {
	return decodeJSON(b, false)
}

// DecodeInput decodes input the evaluator parses for a single evaluation call.
// Its objects are frozen so custom functions can share their normalized views
// (see CachedNormalizedView); it must not be used for values handed to
// callers.
func DecodeInput(b json.RawMessage) (any, error) {
	return decodeJSON(b, true)
}

func decodeJSON(b json.RawMessage, freeze bool) (any, error) {
	if v, ok := fastDecodeJSON(b, freeze); ok {
		return v, nil
	}
	v, err := legacyDecodeJSON(b)
	if err == nil && freeze {
		freezeTree(v)
	}
	return v, err
}

// freezeTree freezes every object in a decoded value. It walks with an
// explicit stack, as the legacy decoder can return a value nested deeper than
// maxDecodeDepth.
func freezeTree(v any) {
	stack := []any{v}
	for len(stack) > 0 {
		v, stack = stack[len(stack)-1], stack[:len(stack)-1]
		switch t := v.(type) {
		case *OrderedMap:
			for _, k := range t.keys {
				stack = appendFreezable(stack, t.data[k])
			}
			t.freeze()
		case []any:
			for _, e := range t {
				stack = appendFreezable(stack, e)
			}
		}
	}
}

// appendFreezable appends v to stack if it is an object or an array.
func appendFreezable(stack []any, v any) []any {
	switch v.(type) {
	case *OrderedMap, []any:
		return append(stack, v)
	}
	return stack
}

// legacyDecodeJSON is the encoding/json token decoder. It remains the
// reference for error reporting: fastDecodeJSON defers to it on any input it
// does not accept, including input nested deeper than maxDecodeDepth, so it
// keeps its open objects and arrays on an explicit stack rather than
// recursing, which could overflow the goroutine stack.
func legacyDecodeJSON(b json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var stack []decodeFrame
	for {
		tok, err := dec.Token()
		if err != nil {
			return failedDecode(stack), err
		}
		var v any
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, decodeFrame{object: NewOrderedMap()})
				continue
			case '[':
				stack = append(stack, decodeFrame{array: []any{}})
				continue
			}
			// The decoder only returns a closing delimiter for an open frame.
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top.object != nil {
				v = top.object
			} else {
				v = top.array
			}
		case nil:
			v = Null
		default:
			v = tok
		}
		if len(stack) == 0 {
			return v, nil
		}
		top := &stack[len(stack)-1]
		switch {
		case top.object == nil:
			top.array = append(top.array, v)
		case top.hasKey:
			top.object.Set(top.key, v)
			top.hasKey = false
		default:
			// The decoder only returns a string where an object expects a key.
			top.key, top.hasKey = v.(string), true
		}
	}
}

// failedDecode is the value legacyDecodeJSON returns with an error: the
// typed nil of the outermost object or array it had opened, or nil.
func failedDecode(stack []decodeFrame) any {
	switch {
	case len(stack) == 0:
		return nil
	case stack[0].object != nil:
		return (*OrderedMap)(nil)
	}
	return []any(nil)
}

// decodeFrame is an object or array legacyDecodeJSON has opened, with the
// key read for an object's next value.
type decodeFrame struct {
	object *OrderedMap
	array  []any
	key    string
	hasKey bool
}

// DecodeRawMap converts a map of field names to raw JSON values into an
// *OrderedMap by decoding each value individually. Objects in the values
// preserve key insertion order, consistent with DecodeJSON. Like DecodeInput,
// it is for evaluator input scoped to one evaluation call, so the result is
// frozen.
func DecodeRawMap(m map[string]json.RawMessage) (*OrderedMap, error) {
	om := NewOrderedMapWithCapacity(len(m))
	for _, key := range slices.Sorted(maps.Keys(m)) {
		val, err := DecodeInput(m[key])
		if err != nil {
			return nil, fmt.Errorf("decode key %q: %w", key, err)
		}
		om.Set(key, val)
	}
	om.freeze()
	return om, nil
}

// ── Helpers for dual map[string]any / *OrderedMap handling ───────────────────

func MapGet(obj any, key string) (any, bool) {
	switch m := obj.(type) {
	case *OrderedMap:
		return m.Get(key)
	case map[string]any:
		v, ok := m[key]
		return v, ok
	}
	return nil, false
}

func MapKeys(obj any) []string {
	switch m := obj.(type) {
	case *OrderedMap:
		return m.Keys()
	case map[string]any:
		return slices.Sorted(maps.Keys(m))
	}
	return nil
}

func MapLen(obj any) int {
	switch m := obj.(type) {
	case *OrderedMap:
		return m.Len()
	case map[string]any:
		return len(m)
	}
	return 0
}

func IsMap(obj any) bool {
	switch obj.(type) {
	case *OrderedMap, map[string]any:
		return true
	}
	return false
}

func MapRange(obj any, fn func(key string, val any) bool) {
	switch m := obj.(type) {
	case *OrderedMap:
		m.Range(fn)
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(m)) {
			if !fn(k, m[k]) {
				break
			}
		}
	}
}
