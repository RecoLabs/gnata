package evaluator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// OrderedMap is a map that preserves insertion order for JSON serialization.
// Go has no stdlib ordered map; this is the minimal implementation needed so
// that objects created by JSONata expressions serialize keys in definition order.
// Input data from json.Unmarshal remains plain map[string]any — the helper
// functions (MapGet, MapKeys, etc.) bridge both types at call sites.
type OrderedMap struct {
	keys []string
	data map[string]any
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
	if _, ok := m.data[key]; !ok {
		return
	}
	delete(m.data, key)
	m.keys = slices.DeleteFunc(m.keys, func(k string) bool { return k == key })
}

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
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := marshalNoHTMLEscape(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := marshalNoHTMLEscape(m.data[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
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
func DecodeJSON(b json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return decodeValue(dec)
}

// DecodeRawMap converts a map of field names to raw JSON values into an
// *OrderedMap by decoding each value individually via DecodeJSON. Objects
// in the values preserve key insertion order, consistent with DecodeJSON.
func DecodeRawMap(m map[string]json.RawMessage) (*OrderedMap, error) {
	om := NewOrderedMapWithCapacity(len(m))
	for key, raw := range m {
		val, err := DecodeJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("decode key %q: %w", key, err)
		}
		om.Set(key, val)
	}
	return om, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return decodeObject(dec)
		case '[':
			return decodeArray(dec)
		}
	case json.Number:
		return t, nil
	case string:
		return t, nil
	case bool:
		return t, nil
	case nil:
		return Null, nil
	}
	return tok, nil
}

func decodeObject(dec *json.Decoder) (*OrderedMap, error) {
	m := NewOrderedMap()
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := keyTok.(string)
		val, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}
		m.Set(key, val)
	}
	// Consume closing '}'
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return m, nil
}

func decodeArray(dec *json.Decoder) ([]any, error) {
	var arr []any
	for dec.More() {
		val, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}
		arr = append(arr, val)
	}
	// Consume closing ']'
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if arr == nil {
		arr = []any{}
	}
	return arr, nil
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
		return slices.Collect(maps.Keys(m))
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
		for k, v := range m {
			if !fn(k, v) {
				break
			}
		}
	}
}
