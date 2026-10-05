package evaluator

import (
	"bytes"
	"encoding/json"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/recolabs/gnata/internal/parser"
)

// AppendJSON appends the JSON encoding of v to b, producing the same bytes as
// encoding/json with HTML escaping disabled. Invalid UTF-8 is replaced by a
// raw U+FFFD character, as the json/v2-backed encoding/json (the Go 1.25+
// default) does; the original v1 encoder writes the escape \ufffd instead.
// Both decode to the same string, and gnata emits the same bytes on every
// toolchain, including TinyGo.
//
// Every value the evaluator produces is encoded here without encoding/json.
// encoding/json's encoder reports errors by panicking and recovering
// internally, which aborts the program on runtimes where recover is
// unavailable (TinyGo on WebAssembly), so it is used only as a last resort for
// foreign types such as structs.
func AppendJSON(b []byte, v any) ([]byte, error) { //nolint:gocyclo,funlen // type dispatch
	switch val := v.(type) {
	case nil:
		return append(b, "null"...), nil
	case JSONNull:
		return append(b, parser.NullJSON...), nil
	case bool:
		return strconv.AppendBool(b, val), nil
	case string:
		return appendJSONString(b, val), nil
	case float64:
		return appendJSONFloat(b, val, 64)
	case float32:
		return appendJSONFloat(b, float64(val), 32)
	case int:
		return strconv.AppendInt(b, int64(val), 10), nil
	case int8:
		return strconv.AppendInt(b, int64(val), 10), nil
	case int16:
		return strconv.AppendInt(b, int64(val), 10), nil
	case int32:
		return strconv.AppendInt(b, int64(val), 10), nil
	case int64:
		return strconv.AppendInt(b, val, 10), nil
	case uint:
		return strconv.AppendUint(b, uint64(val), 10), nil
	case uint8:
		return strconv.AppendUint(b, uint64(val), 10), nil
	case uint16:
		return strconv.AppendUint(b, uint64(val), 10), nil
	case uint32:
		return strconv.AppendUint(b, uint64(val), 10), nil
	case uint64:
		return strconv.AppendUint(b, val, 10), nil
	case json.Number:
		return appendJSONNumber(b, val)
	case *OrderedMap:
		if val == nil {
			return append(b, "null"...), nil
		}
		b = append(b, '{')
		for i, k := range val.keys {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendJSONString(b, k)
			b = append(b, ':')
			var err error
			if b, err = AppendJSON(b, val.data[k]); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	case []any:
		if val == nil {
			return append(b, "null"...), nil
		}
		b = append(b, '[')
		for i, e := range val {
			if i > 0 {
				b = append(b, ',')
			}
			var err error
			if b, err = AppendJSON(b, e); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	case []string:
		if val == nil {
			return append(b, "null"...), nil
		}
		b = append(b, '[')
		for i, s := range val {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendJSONString(b, s)
		}
		return append(b, ']'), nil
	case map[string]any:
		if val == nil {
			return append(b, "null"...), nil
		}
		b = append(b, '{')
		for i, k := range slices.Sorted(maps.Keys(val)) {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendJSONString(b, k)
			b = append(b, ':')
			var err error
			if b, err = AppendJSON(b, val[k]); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	case map[string]string:
		if val == nil {
			return append(b, "null"...), nil
		}
		b = append(b, '{')
		for i, k := range slices.Sorted(maps.Keys(val)) {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendJSONString(b, k)
			b = append(b, ':')
			b = appendJSONString(b, val[k])
		}
		return append(b, '}'), nil
	case json.Marshaler:
		if rv := reflect.ValueOf(val); rv.Kind() == reflect.Pointer && rv.IsNil() {
			return append(b, "null"...), nil
		}
		raw, err := val.MarshalJSON()
		if err != nil {
			return nil, &json.MarshalerError{Type: reflect.TypeOf(val), Err: err}
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			return nil, &json.MarshalerError{Type: reflect.TypeOf(val), Err: err}
		}
		return append(b, buf.Bytes()...), nil
	}
	if t := reflect.TypeOf(v); unsupportedJSONKind(t.Kind()) {
		// Same error encoding/json returns, without its panic-based path.
		return nil, &json.UnsupportedTypeError{Type: t}
	}
	enc, err := marshalNoHTMLEscape(v)
	if err != nil {
		return nil, err
	}
	return append(b, enc...), nil
}

// unsupportedJSONKind reports kinds encoding/json can never encode.
func unsupportedJSONKind(k reflect.Kind) bool {
	return k == reflect.Func || k == reflect.Chan || k == reflect.Complex64 ||
		k == reflect.Complex128 || k == reflect.UnsafePointer
}

// appendJSONFloat formats f exactly like encoding/json's float encoder.
func appendJSONFloat(b []byte, f float64, bits int) ([]byte, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, &json.UnsupportedValueError{Value: reflect.ValueOf(f), Str: strconv.FormatFloat(f, 'g', -1, bits)}
	}
	format := byte('f')
	if abs := math.Abs(f); abs != 0 {
		if bits == 64 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
			format = 'e'
		}
	}
	b = strconv.AppendFloat(b, f, format, -1, bits)
	if format == 'e' {
		// Clean up e-09 to e-9.
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return b, nil
}

func appendJSONNumber(b []byte, n json.Number) ([]byte, error) {
	if n == "" {
		return append(b, '0'), nil
	}
	if !isPlainJSONNumber(string(n)) {
		return nil, invalidNumberError(n)
	}
	return append(b, n...), nil
}

type jsonNumberError struct{ lit json.Number }

func (e *jsonNumberError) Error() string {
	return "json: invalid number literal " + strconv.Quote(string(e.lit))
}

func invalidNumberError(n json.Number) error { return &jsonNumberError{n} }

const hexDigits = "0123456789abcdef"

// appendJSONString quotes s like encoding/json with HTML escaping disabled:
// invalid UTF-8 becomes U+FFFD and U+2028/U+2029 are escaped.
func appendJSONString(b []byte, s string) []byte {
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); {
		if c := s[i]; c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' {
				i++
				continue
			}
			b = append(b, s[start:i]...)
			switch c {
			case '"', '\\':
				b = append(b, '\\', c)
			case '\b':
				b = append(b, '\\', 'b')
			case '\f':
				b = append(b, '\\', 'f')
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b = append(b, s[start:i]...)
			b = append(b, `�`...)
			i += size
			start = i
			continue
		}
		if r == ' ' || r == ' ' {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', '2', '0', '2', hexDigits[r&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	b = append(b, s[start:]...)
	return append(b, '"')
}

// JSONValue prepares v for JSON as jsonata-js's $string does: function
// values become "", and numbers are laid out in decimal under precision prec
// or rounded by roundJS otherwise. Objects become *OrderedMap, keeping the
// insertion order of an *OrderedMap.
func JSONValue(v any, prec int) (any, error) {
	return jsonValue(v, prec, &valuePath{what: "stringify"})
}

func jsonValue(v any, prec int, path *valuePath) (any, error) {
	if IsNull(v) {
		return nil, nil
	}
	switch val := v.(type) {
	case *Sequence:
		return jsonValue(CollapseSequence(val), prec, path)
	case *OrderedMap:
		if err := path.enter(val); err != nil {
			return nil, err
		}
		defer path.leave(val)
		out := NewOrderedMapWithCapacity(val.Len())
		var err error
		val.Range(func(k string, v any) bool {
			var s any
			if s, err = jsonValue(v, prec, path); err != nil {
				return false
			}
			out.Set(k, s)
			return true
		})
		return out, err
	case map[string]any:
		if err := path.enter(val); err != nil {
			return nil, err
		}
		defer path.leave(val)
		out := NewOrderedMapWithCapacity(len(val))
		for _, k := range MapKeys(val) {
			s, err := jsonValue(val[k], prec, path)
			if err != nil {
				return nil, err
			}
			out.Set(k, s)
		}
		return out, nil
	case []any:
		return jsonArray(val, prec, path)
	case ConsArray:
		return jsonArray(val, prec, path)
	case json.Number:
		if s, ok := FormatDecimal(val, prec); ok {
			return json.Number(s), nil
		}
		return v, nil
	case float64:
		if s, ok := FormatDecimal(val, prec); ok {
			return json.Number(s), nil
		}
		return roundJS(val), nil
	default:
		if isCallable(v) {
			return "", nil
		}
		return v, nil
	}
}

func jsonArray(arr []any, prec int, path *valuePath) ([]any, error) {
	if err := path.enter(nil); err != nil {
		return nil, err
	}
	defer path.leave(nil)
	out := make([]any, 0, len(arr))
	for _, v := range arr {
		s, err := jsonValue(v, prec, path)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
