package gnata_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/recolabs/gnata"
)

func TestStreamEvaluator_Options(t *testing.T) {
	hook := new(testMetricsHook)
	se := gnata.NewStreamEvaluator(nil,
		gnata.WithPoolSize(4),
		gnata.WithMaxCachedSchemas(1),
		gnata.WithMetricsHook(hook),
	)
	idx, err := se.Compile(`data.action`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, schema := range []string{"s1", "s2", "s1"} {
		got, err := se.EvalOne(context.Background(), json.RawMessage(streamTestData), schema, idx)
		if err != nil {
			t.Fatalf("EvalOne(%s): %v", schema, err)
		}
		if got != "grant-access" {
			t.Fatalf("EvalOne(%s) = %v, want grant-access", schema, got)
		}
	}
	if hook.misses != 3 {
		t.Fatalf("misses = %d, want 3 (capacity 1 evicts s1 before it is reused)", hook.misses)
	}
	if hook.evictions != 2 {
		t.Fatalf("evictions = %d, want 2", hook.evictions)
	}
	if stats := se.Stats(); stats.Evictions != 2 || stats.Entries != 1 {
		t.Fatalf("Stats = %+v, want 2 evictions and 1 entry", stats)
	}
}

func TestStreamEvaluator_Errors(t *testing.T) {
	se := gnata.NewStreamEvaluator(nil)
	if _, err := se.Compile(`(`); err == nil {
		t.Fatal("Compile of invalid expression succeeded")
	}
	if se.Len() != 0 {
		t.Fatalf("Len after failed Compile = %d, want 0", se.Len())
	}
	expr, err := gnata.Compile(`1`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, idx := range []int{-1, 0} {
		if err := se.Replace(idx, expr); err == nil {
			t.Fatalf("Replace(%d) on empty evaluator succeeded", idx)
		}
		if err := se.Remove(idx); err == nil {
			t.Fatalf("Remove(%d) on empty evaluator succeeded", idx)
		}
	}

	hook := new(erroringMetricsHook)
	se = gnata.NewStreamEvaluator(nil, gnata.WithMetricsHook(hook))
	idx, err := se.Compile(`$error("boom")`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := se.EvalMany(context.Background(), json.RawMessage(streamTestData), "", []int{idx}); err == nil ||
		!strings.Contains(err.Error(), "D3137") {
		t.Fatalf("EvalMany error = %v, want D3137", err)
	}
	if hook.errs != 1 {
		t.Fatalf("OnEval saw %d errors, want 1", hook.errs)
	}
}

type erroringMetricsHook struct {
	testMetricsHook
	errs int
}

func (h *erroringMetricsHook) OnEval(_ int, _ bool, _ time.Duration, err error) {
	if err != nil {
		h.errs++
	}
}

func TestBoundedCache_GetSet(t *testing.T) {
	cache := gnata.NewBoundedCache(0)
	if _, ok := cache.Get("k"); ok {
		t.Fatal("Get on empty cache hit")
	}
	plan := new(gnata.GroupPlan)
	if evicted := cache.Set("k", plan); evicted {
		t.Fatal("first Set evicted")
	}
	if got, ok := cache.Get("k"); !ok || got != plan {
		t.Fatalf("Get(k) = %p, %v; want %p, true", got, ok, plan)
	}
	if evicted := cache.Set("k2", plan); !evicted {
		t.Fatal("Set beyond clamped capacity 1 did not evict")
	}
	if _, ok := cache.Get("k"); ok {
		t.Fatal("evicted key still present")
	}
}

func TestEvalBytesWithVars(t *testing.T) {
	testCases := []struct {
		desc string
		expr string
		data string
		want string
	}{
		{desc: "pure path fast path", expr: `a.b`, data: `{"a":{"b":1}}`, want: `1`},
		{desc: "function fast path", expr: `$exists(a)`, data: `{"a":1}`, want: `true`},
		{desc: "full evaluation with vars", expr: `a.b + $offset`, data: `{"a":{"b":1}}`, want: `11`},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			got, err := expr.EvalBytesWithVars(context.Background(), json.RawMessage(tC.data), map[string]any{"offset": 10.0})
			if err != nil {
				t.Fatalf("EvalBytesWithVars: %v", err)
			}
			if rendered := render(t, got); rendered != tC.want {
				t.Fatalf("got %s, want %s", rendered, tC.want)
			}
		})
	}

	expr, err := gnata.Compile(`a + $offset`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := expr.EvalBytesWithVars(context.Background(), json.RawMessage(`{`), nil); err == nil {
		t.Fatal("EvalBytesWithVars on malformed JSON succeeded")
	}
}

func TestRequiredPaths(t *testing.T) {
	expr, err := gnata.Compile(`a.b`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if paths := expr.RequiredPaths(); !slices.Equal(paths, []string{"a.b"}) {
		t.Fatalf("RequiredPaths = %v, want [a.b]", paths)
	}
	expr, err = gnata.Compile(`$sum(a.b) + $count(c)`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if paths := expr.RequiredPaths(); len(paths) != 0 {
		t.Fatalf("RequiredPaths of non-fast-path expression = %v, want none", paths)
	}
}

func TestOrderedMapConstructionAndUnmarshal(t *testing.T) {
	for _, om := range []*gnata.OrderedMap{gnata.NewOrderedMap(), gnata.NewOrderedMapWithCapacity(2)} {
		om.Set("z", 1.0)
		om.Set("a", 2.0)
		if keys := om.Keys(); !slices.Equal(keys, []string{"z", "a"}) {
			t.Fatalf("Keys = %v, want [z a]", keys)
		}
	}

	om := gnata.NewOrderedMap()
	if err := json.Unmarshal([]byte(`{"z":1,"a":{"n":true}}`), om); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if keys := om.Keys(); !slices.Equal(keys, []string{"z", "a"}) {
		t.Fatalf("Keys after Unmarshal = %v, want [z a]", keys)
	}
	for _, bad := range []string{`[1]`, `{"a":}`, `{"a":1`} {
		if err := json.Unmarshal([]byte(bad), gnata.NewOrderedMap()); err == nil {
			t.Fatalf("Unmarshal(%s) succeeded", bad)
		}
	}
}

func TestObjectFunctionsOnGoMaps(t *testing.T) {
	vars := map[string]any{"m": map[string]any{"b": 2.0, "a": 1.0}}
	testCases := []struct {
		expr string
		want string
	}{
		{expr: `$keys($m)`, want: `["a","b"]`},
		{expr: `$values($m)`, want: `[1,2]`},
	}
	for _, tC := range testCases {
		t.Run(tC.expr, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			got, err := expr.EvalWithVars(context.Background(), nil, vars)
			if err != nil {
				t.Fatalf("EvalWithVars: %v", err)
			}
			if rendered := render(t, got); rendered != tC.want {
				t.Fatalf("got %s, want %s", rendered, tC.want)
			}
		})
	}
}

func TestCustomFunctionPanicIsRecovered(t *testing.T) {
	testCases := []struct {
		desc  string
		value any
		want  string
	}{
		{desc: "error value", value: errors.New("custom failure"), want: "custom failure"},
		{desc: "non-error value", value: "plain", want: "gnata: unexpected panic: plain"},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			env := gnata.NewCustomEnv(map[string]gnata.CustomFunc{
				"explode": func(_ []any, _ any) (any, error) { panic(tC.value) },
			})
			expr, err := gnata.Compile(`$explode()`)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			_, err = expr.EvalWithCustomFuncs(context.Background(), nil, env)
			if err == nil || !strings.Contains(err.Error(), tC.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tC.want)
			}
		})
	}
}

// TestEvalResultHasNoInternalArrays checks that results, including values
// inside objects the expression built, reach the caller as plain slices.
func TestEvalResultHasNoInternalArrays(t *testing.T) {
	for _, expr := range []string{
		`{"k": o.b[]}`,
		`{"k": o.[b, c]}`,
		`{"k": [{"j": o.b[]}]}`,
		`$map([{"a":1}], $keys)`,
		`{"k": $map([{"a":1}], $keys)}`,
		`$ ~> |o|{"z": b[]}|`,
	} {
		t.Run(expr, func(t *testing.T) {
			compiled, err := gnata.Compile(expr)
			if err != nil {
				t.Fatal(err)
			}
			got, err := compiled.Eval(context.Background(), map[string]any{"o": map[string]any{"b": 5.0, "c": 6.0}})
			if err != nil {
				t.Fatal(err)
			}
			assertNoInternalArrays(t, got)
		})
	}
}

func assertNoInternalArrays(t *testing.T, v any) {
	t.Helper()
	switch val := v.(type) {
	case []any:
		for _, elem := range val {
			assertNoInternalArrays(t, elem)
		}
	case *gnata.OrderedMap:
		for _, k := range val.Keys() {
			elem, _ := val.Get(k)
			assertNoInternalArrays(t, elem)
		}
	case map[string]any:
		for _, elem := range val {
			assertNoInternalArrays(t, elem)
		}
	default:
		// The internal array types are unexported from gnata, so match any
		// type of the evaluator package rather than a list that can go stale.
		if typ := fmt.Sprintf("%T", v); strings.HasPrefix(typ, "evaluator.") {
			t.Fatalf("result holds internal type %s", typ)
		}
	}
}
