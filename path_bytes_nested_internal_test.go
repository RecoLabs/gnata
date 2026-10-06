//go:build !tinygo

package gnata

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"runtime/debug"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// randomNestedJSON returns a random JSON value rich in nested arrays and in
// objects with the keys stepNestedArray looks up.
func randomNestedJSON(r *rand.Rand, depth int) string {
	switch n := r.IntN(10); {
	case depth > 6 || n < 3:
		return []string{`1`, `-2.5e3`, `"s"`, `"a\"]\\"`, `true`, `null`, `[]`, `{}`, `"\u00e9[{"`}[r.IntN(9)]
	case n < 7:
		items := make([]string, r.IntN(4))
		for i := range items {
			items[i] = randomNestedJSON(r, depth+1)
		}
		return "[" + strings.Join(items, ", ") + "]"
	default:
		fields := make([]string, r.IntN(4))
		for i := range fields {
			fields[i] = `"` + []string{"a", "b", "a.b", "a"}[r.IntN(4)] + `": ` + randomNestedJSON(r, depth+1)
		}
		return "{" + strings.Join(fields, ",") + "}"
	}
}

func TestValidJSON(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	mutations := []string{"", ",", "]", "}", "[", "{", `"`, ":", "\\", "x", " ", "\x01", "\xff", "01", "-", "1."}
	for range 200_000 {
		s := randomNestedJSON(r, 0)
		if !validJSON(s) {
			t.Fatalf("validJSON rejects valid JSON %q", s)
		}
		if i := r.IntN(len(s) + 1); r.IntN(2) == 0 {
			s = s[:i] + mutations[r.IntN(len(mutations))] + s[i:]
		} else if i < len(s) {
			s = s[:i] + s[i+1:]
		}
		// validJSON also rejects invalid UTF-8, which json.Valid accepts.
		if want := json.Valid([]byte(s)) && utf8.ValidString(s); validJSON(s) != want {
			t.Fatalf("validJSON(%q) = %v, want %v", s, !want, want)
		}
	}
}

// splitStep is the field lookup splitting each nested array with gjson, as
// stepArray did before stepNestedArray.
func splitStep(step string, r *gjson.Result) (any, bool) {
	if !r.IsArray() {
		return stepSingle(step, r)
	}
	return stepArrayItems(step, r.Array(), false)
}

// stepNestedArray gives what splitting each nested array with gjson gives.
func TestStepNestedArrayMatchesSplitting(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for range 50_000 {
		raw := "[" + randomNestedJSON(r, 0) + "," + randomNestedJSON(r, 0) + "]"
		root := gjson.Parse(raw)
		for _, step := range []string{"a", "b", "a.b", "c"} {
			want, wantOK := splitStep(step, &root)
			got, gotOK := stepNestedArray(step, raw)
			if gotOK != wantOK || !sameStepValue(got, want) {
				t.Fatalf("%s over %s: got %v %v, want %v %v", step, raw, got, gotOK, want, wantOK)
			}
		}
	}
}

func sameStepValue(a, b any) bool {
	switch x := a.(type) {
	case gjson.Result:
		y, ok := b.(gjson.Result)
		return ok && x.Type == y.Type && x.Raw == y.Raw
	case []gjson.Result:
		y, ok := b.([]gjson.Result)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i].Type != y[i].Type || x[i].Raw != y[i].Raw {
				return false
			}
		}
		return true
	}
	return a == nil && b == nil
}

// A field path over arrays nested 200,000 deep walks the raw JSON once,
// without recursing per level: under a 2 MB stack limit, splitting each
// nested array recursively would overflow.
func TestDeepNestedArrayPathBytes(t *testing.T) {
	prev := debug.SetMaxStack(2 << 20)
	t.Cleanup(func() { debug.SetMaxStack(prev) })
	const depth = 200_000
	data := strings.Repeat("[", depth) + `{"a":1}` + strings.Repeat("]", depth)
	got, ok := walkPureStepsBytes([]string{"a"}, []byte(data), false)
	if !ok || fmt.Sprint(got) != "1" {
		t.Fatalf("got %v %v, want 1", got, ok)
	}
}
