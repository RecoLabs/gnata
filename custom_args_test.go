package gnata_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/recolabs/gnata"
)

const customArgsPayload = `{"user": {"name": "ada", "roles": [{"id": 1}, {"id": 2}]}}`

// newArgsEvaluator compiles two expressions that pass the same payload object
// to a custom function, recording the map each call received.
func newArgsEvaluator(t *testing.T, seen *[]map[string]any, opts ...gnata.StreamOption) (se *gnata.StreamEvaluator, idx []int) {
	t.Helper()
	record := func(args []any, _ any) (any, error) {
		m, ok := args[0].(map[string]any)
		if !ok {
			t.Fatalf("custom function got %T, want map[string]any", args[0])
		}
		*seen = append(*seen, m)
		return m["name"], nil
	}
	opts = append([]gnata.StreamOption{gnata.WithCustomFunctions(map[string]gnata.CustomFunc{"record": record})}, opts...)
	se = gnata.NewStreamEvaluator(nil, opts...)
	exprs := []string{`$record(user) & "-a"`, `$record(user) & "-b"`}
	idx = make([]int, 0, len(exprs))
	for _, src := range exprs {
		i, err := se.Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		idx = append(idx, i)
	}
	return se, idx
}

func evalArgs(t *testing.T, se *gnata.StreamEvaluator, idx []int) []any {
	t.Helper()
	res, err := se.EvalMany(context.Background(), json.RawMessage(customArgsPayload), "args", idx)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCustomFuncArgs_DefaultGetsFreshCopies(t *testing.T) {
	var seen []map[string]any
	se, idx := newArgsEvaluator(t, &seen)
	got := evalArgs(t, se, idx)
	if want := []any{"ada-a", "ada-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("results = %v, want %v", got, want)
	}
	if len(seen) != 2 {
		t.Fatalf("custom function called %d times, want 2", len(seen))
	}
	if reflect.ValueOf(seen[0]).Pointer() == reflect.ValueOf(seen[1]).Pointer() {
		t.Fatal("default mode shared the same map between calls")
	}
	seen[0]["name"] = "mutated"
	if seen[1]["name"] != "ada" {
		t.Fatal("mutating one call's argument leaked into another call")
	}
}

func TestCustomFuncArgs_ReadOnlySharesCachedView(t *testing.T) {
	var seen []map[string]any
	se, idx := newArgsEvaluator(t, &seen, gnata.WithReadOnlyCustomFuncArgs())
	got := evalArgs(t, se, idx)
	if want := []any{"ada-a", "ada-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("results = %v, want %v", got, want)
	}
	if len(seen) != 2 {
		t.Fatalf("custom function called %d times, want 2", len(seen))
	}
	if reflect.ValueOf(seen[0]).Pointer() != reflect.ValueOf(seen[1]).Pointer() {
		t.Fatal("read-only mode did not reuse the cached map")
	}
	want := map[string]any{"name": "ada", "roles": []any{map[string]any{"id": json.Number("1")}, map[string]any{"id": json.Number("2")}}}
	if !reflect.DeepEqual(seen[0], want) {
		t.Fatalf("normalized argument = %#v, want %#v", seen[0], want)
	}
}

func TestCustomFuncArgs_CacheIsPerPayload(t *testing.T) {
	var seen []map[string]any
	se, idx := newArgsEvaluator(t, &seen, gnata.WithReadOnlyCustomFuncArgs())
	evalArgs(t, se, idx)
	evalArgs(t, se, idx)
	if len(seen) != 4 {
		t.Fatalf("custom function called %d times, want 4", len(seen))
	}
	if reflect.ValueOf(seen[0]).Pointer() == reflect.ValueOf(seen[2]).Pointer() {
		t.Fatal("cached view leaked across separate evaluations")
	}
}

// TestCustomFuncArgs_CallerDecodedValuesAreNotCached checks that values the
// caller decodes and keeps (DecodeJSON + EvalPreparsed) never carry a cached
// view, so mutations between evaluations are always seen.
func TestCustomFuncArgs_CallerDecodedValuesAreNotCached(t *testing.T) {
	var seen []map[string]any
	se, idx := newArgsEvaluator(t, &seen, gnata.WithReadOnlyCustomFuncArgs())
	doc, err := gnata.DecodeJSON(json.RawMessage(customArgsPayload))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := se.EvalPreparsed(context.Background(), doc, "args-preparsed", idx[:1]); err != nil {
		t.Fatal(err)
	}
	user, _ := doc.(*gnata.OrderedMap).Get("user")
	user.(*gnata.OrderedMap).Set("name", "grace")
	res, err := se.EvalPreparsed(context.Background(), doc, "args-preparsed", idx[:1])
	if err != nil {
		t.Fatal(err)
	}
	if res[0] != "grace-a" {
		t.Fatalf("result after mutation = %v, want grace-a (stale cached argument)", res[0])
	}
}

// TestCustomFuncArgs_ConstructedArraysArePlainSlices checks that arrays built
// by [...] path steps or kept by [] reach a custom function as []any at any
// depth.
func TestCustomFuncArgs_ConstructedArraysArePlainSlices(t *testing.T) {
	isSlice := func(args []any, _ any) (any, error) {
		outer, ok := args[0].([]any)
		if !ok {
			return false, nil
		}
		for _, elem := range outer {
			if _, ok := elem.([]any); !ok {
				return false, nil
			}
		}
		return true, nil
	}
	se := gnata.NewStreamEvaluator(nil, gnata.WithCustomFunctions(map[string]gnata.CustomFunc{"isSlice": isSlice}))
	for _, expr := range []string{
		`$isSlice(a.[b, c])`,
		`$isSlice($map(a, function($v){$v.b[]}))`,
	} {
		t.Run(expr, func(t *testing.T) {
			idx, err := se.Compile(expr)
			if err != nil {
				t.Fatal(err)
			}
			res, err := se.EvalMany(context.Background(), json.RawMessage(`{"a":[{"b":1,"c":2},{"b":3,"c":4}]}`), "cons", []int{idx})
			if err != nil {
				t.Fatal(err)
			}
			if res[0] != true {
				t.Fatalf("%s = %v, want true", expr, res[0])
			}
		})
	}
}
