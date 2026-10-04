//go:build !tinygo

// Differential tests against encoding/json. They exercise encoding/json's
// error paths, which panic internally and cannot run where recover is
// unavailable (TinyGo on WebAssembly).

package evaluator

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"strconv"
	"testing"
)

// legacyOrderedMapJSON is the previous OrderedMap.MarshalJSON: every key and
// value encoded independently through encoding/json.
func legacyOrderedMapJSON(m *OrderedMap) ([]byte, error) {
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
		vb, err := marshalNoHTMLEscape(legacyWrap(m.data[k]))
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

type legacyMap struct{ m *OrderedMap }

func (l legacyMap) MarshalJSON() ([]byte, error) { return legacyOrderedMapJSON(l.m) }

func legacyWrap(v any) any {
	switch t := v.(type) {
	case *OrderedMap:
		return legacyMap{t}
	case []any:
		if t == nil {
			return []any(nil)
		}
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = legacyWrap(e)
		}
		return out
	case map[string]any:
		if t == nil {
			return map[string]any(nil)
		}
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = legacyWrap(e)
		}
		return out
	}
	return v
}

var trickyStrings = []string{
	"", "plain", `quote"d`, `back\slash`, "tab\tnl\ncr\r", "\x00\x01\x1f", "<html>&amp;",
	"héllo", "\u65e5\u672c\u8a9e", "emoji 😀", "\u2028line\u2029sep", "bad\xffutf8", "\x7f", "a/b",
}

var trickyFloats = []float64{
	0, math.Copysign(0, -1), 1, -1, 0.1, 1e-6, 9.99e-7, 1e-7, 1e20, 1e21, 123456789.125,
	-3.5e-10, math.MaxFloat64, math.SmallestNonzeroFloat64, 42, 1.0 / 3,
}

var trickyNumbers = []json.Number{"0", "-0", "12", "1.5", "1e10", "1E+2", "-0.0e-1", "007", "1.", ".5", "1e", "abc", ""}

func randValue(r *rand.Rand, depth int) any {
	k := r.IntN(10)
	if depth > 3 && k >= 7 {
		k = r.IntN(7)
	}
	switch k {
	case 0:
		return nil
	case 1:
		return Null
	case 2:
		return r.IntN(2) == 0
	case 3:
		return trickyStrings[r.IntN(len(trickyStrings))]
	case 4:
		return trickyFloats[r.IntN(len(trickyFloats))]
	case 5:
		return trickyNumbers[r.IntN(len(trickyNumbers))]
	case 6:
		return r.Float64() * math.Pow(10, float64(r.IntN(40)-20))
	case 7:
		m := NewOrderedMap()
		for range r.IntN(5) {
			m.Set(trickyStrings[r.IntN(len(trickyStrings))], randValue(r, depth+1))
		}
		return m
	case 8:
		n := r.IntN(4)
		if n == 0 && r.IntN(2) == 0 {
			return []any(nil)
		}
		a := make([]any, n)
		for i := range a {
			a[i] = randValue(r, depth+1)
		}
		return a
	default:
		m := map[string]any{}
		for range r.IntN(4) {
			m[trickyStrings[r.IntN(len(trickyStrings))]] = randValue(r, depth+1)
		}
		return m
	}
}

type marshalerVal struct{ s string }

func (m marshalerVal) MarshalJSON() ([]byte, error) {
	return []byte(" { \"m\" : " + strconv.Quote(m.s) + " } "), nil
}

type failingMarshaler struct{}

func (failingMarshaler) MarshalJSON() ([]byte, error) { return nil, errors.New("boom") }

// randScalar covers every non-container type AppendJSON encodes directly.
func randScalar(r *rand.Rand) any {
	switch r.IntN(16) {
	case 0:
		return math.Float64frombits(r.Uint64())
	case 1:
		return float32(math.Float32frombits(r.Uint32()))
	case 2:
		return r.Int()
	case 3:
		return int8(r.Int())
	case 4:
		return int16(r.Int())
	case 5:
		return int32(r.Int())
	case 6:
		return r.Int64()
	case 7:
		return uint(r.Uint64())
	case 8:
		return uint8(r.Uint32())
	case 9:
		return uint16(r.Uint32())
	case 10:
		return r.Uint32()
	case 11:
		return r.Uint64()
	case 12:
		b := make([]byte, r.IntN(12))
		for i := range b {
			b[i] = byte(r.IntN(256))
		}
		return string(b)
	case 13:
		return []string{trickyStrings[r.IntN(len(trickyStrings))], "x"}
	case 14:
		return map[string]string{"b": trickyStrings[r.IntN(len(trickyStrings))], "a": "1"}
	default:
		return marshalerVal{trickyStrings[r.IntN(len(trickyStrings))]}
	}
}

func encodingJSON(v any) ([]byte, error) { return marshalNoHTMLEscape(v) }

func TestAppendJSONMatchesEncodingJSON(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	const randomValues = 300000
	values := make([]any, 0, randomValues+11)
	values = append(values,
		failingMarshaler{},
		BuiltinFunction(func([]any, any) (any, error) { return nil, nil }),
		[]any{"x", BuiltinFunction(func([]any, any) (any, error) { return nil, nil })},
		make(chan int), complex(1, 2),
		(*OrderedMap)(nil), []string(nil), map[string]string(nil),
		float32(1e-7), float32(1e21), float32(3.4028235e38),
	)
	for range randomValues {
		values = append(values, randScalar(r))
	}
	for i, v := range values {
		got, gotErr := AppendJSON(nil, v)
		want, wantErr := encodingJSON(v)
		if (gotErr != nil) != (wantErr != nil) {
			t.Fatalf("value %d (%T %v): err mismatch got=%v want=%v", i, v, v, gotErr, wantErr)
		}
		if gotErr != nil {
			// A MarshalerError names the receiver type differently across
			// encoding/json implementations (v1 vs json/v2-backed), so only the
			// error's presence is compared for it.
			var me *json.MarshalerError
			if !errors.As(gotErr, &me) && gotErr.Error() != wantErr.Error() {
				t.Fatalf("value %d (%T): error text got=%q want=%q", i, v, gotErr, wantErr)
			}
			continue
		}
		if !bytes.Equal(got, canonicalInvalidUTF8(want)) {
			t.Fatalf("value %d (%T %v):\n got=%s\nwant=%s", i, v, v, got, want)
		}
	}
}

// canonicalInvalidUTF8 rewrites the v1 encoder's \ufffd escape for invalid
// UTF-8 to the raw U+FFFD that AppendJSON (like json/v2) emits, so the test
// passes with either encoding/json backend.
func canonicalInvalidUTF8(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte(`\ufffd`), []byte("\ufffd"))
}

func TestAppendJSONMatchesLegacy(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 200000 {
		root := NewOrderedMap()
		for range r.IntN(6) {
			root.Set(trickyStrings[r.IntN(len(trickyStrings))], randValue(r, 0))
		}
		got, gotErr := root.MarshalJSON()
		want, wantErr := legacyOrderedMapJSON(root)
		if (gotErr != nil) != (wantErr != nil) {
			t.Fatalf("case %d: err mismatch got=%v want=%v", i, gotErr, wantErr)
		}
		if gotErr == nil && !bytes.Equal(got, canonicalInvalidUTF8(want)) {
			t.Fatalf("case %d:\n got=%s\nwant=%s", i, got, want)
		}
		// Through json.Marshal, as callers use it.
		got2, err2 := json.Marshal(root)
		want2, werr2 := json.Marshal(legacyMap{root})
		if (err2 != nil) != (werr2 != nil) || (err2 == nil && !bytes.Equal(got2, canonicalInvalidUTF8(want2))) {
			t.Fatalf("case %d json.Marshal mismatch:\n got=%s\nwant=%s (%v/%v)", i, got2, want2, err2, werr2)
		}
	}
}
