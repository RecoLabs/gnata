//go:build !tinygo

package evaluator

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func sameDecoded(a, b any) bool {
	switch x := a.(type) {
	case *OrderedMap:
		y, ok := b.(*OrderedMap)
		if !ok || len(x.keys) != len(y.keys) {
			return false
		}
		for i, k := range x.keys {
			if y.keys[i] != k || !sameDecoded(x.data[k], y.data[k]) {
				return false
			}
		}
		return len(x.data) == len(y.data)
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) || (x == nil) != (y == nil) {
			return false
		}
		for i := range x {
			if !sameDecoded(x[i], y[i]) {
				return false
			}
		}
		return true
	default:
		return fmt.Sprintf("%T|%v", a, a) == fmt.Sprintf("%T|%v", b, b)
	}
}

func checkDecode(t *testing.T, name string, in []byte) {
	t.Helper()
	fast, ok := fastDecodeJSON(in, false)
	legacy, err := legacyDecodeJSON(in)
	if ok && err != nil {
		t.Fatalf("%s: fast accepted input legacy rejects (%v): %q", name, err, truncate(in))
	}
	if ok && !sameDecoded(fast, legacy) {
		t.Fatalf("%s: decoded values differ for %q", name, truncate(in))
	}
	got, gotErr := DecodeJSON(in)
	if (gotErr != nil) != (err != nil) || (err == nil && !sameDecoded(got, legacy)) {
		t.Fatalf("%s: DecodeJSON differs from legacy: %v vs %v", name, gotErr, err)
	}
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}
	return string(b)
}

func TestFastDecodeMatchesLegacy_Datasets(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "datasets", "*.json"))
	if len(files) == 0 {
		t.Fatal("no testdata/datasets fixtures found")
	}
	r := rand.New(rand.NewPCG(7, 9))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		checkDecode(t, filepath.Base(f), b)
		for range 20 {
			m := append([]byte(nil), b...)
			switch r.IntN(4) {
			case 0:
				if len(m) > 0 {
					m[r.IntN(len(m))] = byte(r.IntN(256))
				}
			case 1:
				m = m[:r.IntN(len(m)+1)]
			case 2:
				m = append(m, []byte(`x}]"1 , {`)[r.IntN(9):]...)
			case 3:
				if len(m) > 1 {
					i := r.IntN(len(m) - 1)
					m = append(m[:i], m[i+1:]...)
				}
			}
			checkDecode(t, filepath.Base(f)+"/mut", m)
		}
	}
	t.Logf("checked %d dataset files", len(files))
}

var decodeSnippets = []string{
	`""`, `"a"`, `"\"\\\/\b\f\n\r\t"`, "\"A\u00e9\u4e2d\"", `"😀"`, `"\ud83d"`, `"\ud83dx"`,
	`"\ud83dA"`, `"\ude00"`, `"\ud83d\uzzzz"`, "\"bad\xffutf\"", "\"\xe2\x80\xa8\"", "\"tab\there\"",
	`"\x"`, `"\u12"`, `0`, `-0`, `12`, `1.5`, `1e10`, `1E+2`, `-0.0e-1`, `007`, `1.`, `.5`, `1e`, `-`,
	`true`, `false`, `null`, `tru`, `nul`, `[]`, `{}`, `[1,2,]`, `{"a":1,}`, `{"a" 1}`, `{"a":1 "b":2}`,
	`{"a":1,"a":2,"b":3,"a":4}`, `[[],[{}],{"x":[null]}]`, `  {"k": "v"}  `, `{"k":"v"} trailing`, `123abc`,
	`[1] [2]`, `"unterminated`, `{"a":`, `[`, ``, `   `, "{\"\u00e9\":\"\u00fc\",\"\u65e5\":\"\u672c\"}", "{\"a\":\"x\x01\"}",
}

func TestFastDecodeMatchesLegacy_Snippets(t *testing.T) {
	for _, s := range decodeSnippets {
		checkDecode(t, "snippet", []byte(s))
	}
	r := rand.New(rand.NewPCG(3, 4))
	for range 300000 {
		var sb strings.Builder
		n := 1 + r.IntN(4)
		for range n {
			sb.WriteString(decodeSnippets[r.IntN(len(decodeSnippets))])
			if r.IntN(3) == 0 {
				sb.WriteString([]string{",", ":", " ", "[", "]", "{", "}", `"k":`}[r.IntN(8)])
			}
		}
		checkDecode(t, "gen", []byte(sb.String()))
		// Valid structured documents.
		v := randValue(r, 0)
		if b, err := json.Marshal(legacyWrap(v)); err == nil {
			checkDecode(t, "valid", b)
		}
	}
}

func TestFastDecodeDeepNesting(t *testing.T) {
	for _, n := range []int{100, 9999, 10000, 10001, 20000} {
		checkDecode(t, fmt.Sprintf("depth%d", n), []byte(strings.Repeat("[", n)+strings.Repeat("]", n)))
	}
}

func TestDecodedStringsDoNotAliasInput(t *testing.T) {
	in := []byte(`{"key": "value", "list": ["a", "b"], "n": 12}`)
	v, err := DecodeJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	for i := range in {
		in[i] = 'X'
	}
	m := v.(*OrderedMap)
	if got, _ := m.Get("key"); got != "value" {
		t.Fatalf("decoded value changed with the input buffer: %q", got)
	}
	if m.Keys()[0] != "key" {
		t.Fatalf("decoded key changed with the input buffer: %q", m.Keys()[0])
	}
	list, _ := m.Get("list")
	if list.([]any)[1] != "b" {
		t.Fatalf("decoded array element changed with the input buffer: %v", list)
	}
	if n, _ := m.Get("n"); n != json.Number("12") {
		t.Fatalf("decoded number changed with the input buffer: %v", n)
	}
}

// The legacy decoder can return a value nested deeper than maxDecodeDepth,
// which freezeTree walks without recursing: under a 16 MB stack limit it
// freezes 200,000 nested objects.
func TestFreezeTreeDeepNesting(t *testing.T) {
	prev := debug.SetMaxStack(16 << 20)
	t.Cleanup(func() { debug.SetMaxStack(prev) })
	const depth = 200_000
	var v any = 1.0
	for range depth {
		m := NewOrderedMap()
		m.Set("a", []any{v})
		v = m
	}
	freezeTree(v)
	for n := range depth {
		m := v.(*OrderedMap)
		if !m.frozen {
			t.Fatalf("object at depth %d is not frozen", depth-n)
		}
		a, _ := m.Get("a")
		v = a.([]any)[0]
	}
}

func TestOnlyEvaluatorInputIsFrozen(t *testing.T) {
	doc := []byte(`{"a": {"b": [{"c": 1}]}}`)
	isFrozen := func(v any) bool { return v.(*OrderedMap).frozen }
	nested := func(v any) any {
		a, _ := v.(*OrderedMap).Get("a")
		b, _ := a.(*OrderedMap).Get("b")
		return b.([]any)[0]
	}
	pub, err := DecodeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	if isFrozen(pub) || isFrozen(nested(pub)) {
		t.Fatal("DecodeJSON result must not be frozen")
	}
	in, err := DecodeInput(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !isFrozen(in) || !isFrozen(nested(in)) {
		t.Fatal("DecodeInput result must be frozen throughout")
	}
	raw, err := DecodeRawMap(map[string]json.RawMessage{"a": json.RawMessage(`{"b": [{"c": 1}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !isFrozen(raw) || !isFrozen(nested(raw)) {
		t.Fatal("DecodeRawMap result must be frozen, including its root")
	}
	// The legacy fallback path must freeze too.
	legacy, err := DecodeInput([]byte(`{"a": {"b": [{"c": 1}]}} trailing`))
	if err != nil {
		t.Fatal(err)
	}
	if !isFrozen(legacy) || !isFrozen(nested(legacy)) {
		t.Fatal("legacy-decoded DecodeInput result must be frozen throughout")
	}
}
