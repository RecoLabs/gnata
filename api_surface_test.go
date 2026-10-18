package gnata_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/recolabs/gnata"
	"github.com/recolabs/gnata/internal/evaluator"
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

func TestConsArrayIsExported(t *testing.T) {
	var v any = evaluator.ConsArray{1.0, 2.0}
	cons, ok := v.(gnata.ConsArray)
	if !ok || len(cons) != 2 {
		t.Fatalf("an evaluator ConsArray does not match gnata.ConsArray: %T", v)
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

// TestTransformKeepsGoMaps checks that a transform over Go map input returns
// Go maps, with internal array types stripped from them, and leaves the
// input unchanged.
func TestTransformKeepsGoMaps(t *testing.T) {
	input := func() map[string]any {
		return map[string]any{"o": map[string]any{"b": 5.0}, "p": map[string]any{"c": map[string]any{"d": 1.0}}}
	}
	testCases := []struct {
		expr string
		want any
	}{
		{expr: `$ ~> |o|{"z": b[]}|`, want: map[string]any{"o": map[string]any{"b": 5.0, "z": []any{5.0}}, "p": input()["p"]}},
		{expr: `{"k": p} ~> |k|{"z": 1}|`, want: map[string]any{"k": map[string]any{"c": map[string]any{"d": 1.0}, "z": 1.0}}},
		{expr: `({"x": p.c[]} ~> |x|{"z": 1}|; p)`, want: input()["p"]},
		{expr: `($ ~> |o|{"z": 1}|) ~> |p|{"w": 1}|`, want: map[string]any{
			"o": map[string]any{"b": 5.0, "z": 1.0}, "p": map[string]any{"c": map[string]any{"d": 1.0}, "w": 1.0},
		}},
		{expr: `$ ~> |p.c|{"self": $}|`, want: map[string]any{
			"o": input()["o"], "p": map[string]any{"c": map[string]any{"d": 1.0, "self": map[string]any{"d": 1.0}}},
		}},
		{expr: `($ ~> |$$.p.c|{"self": $$.p}|; $$)`, want: input()},
	}
	for _, tC := range testCases {
		t.Run(tC.expr, func(t *testing.T) {
			compiled, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatal(err)
			}
			data := input()
			got, err := compiled.Eval(context.Background(), data)
			if err != nil {
				t.Fatal(err)
			}
			if om, ok := got.(*gnata.OrderedMap); ok {
				got = om.ToMap()
			}
			if !reflect.DeepEqual(got, tC.want) {
				t.Fatalf("got %#v, want %#v", got, tC.want)
			}
			if !reflect.DeepEqual(data, input()) {
				t.Fatalf("input changed to %#v", data)
			}
		})
	}
}

// TestStripSharedResults checks that results sharing subtrees, also through
// a transform's update, reach the caller without a walk or copy per path:
// here 2^40 paths lead to the kept array.
func TestStripSharedResults(t *testing.T) {
	for _, expr := range []string{
		`$reduce([1..40], function($acc, $i){{"a": $acc, "b": [$acc]}}, {"x": [1][]})`,
		`$reduce([1..40], function($acc, $i){[[$acc], [$acc]]}, [1][])`,
		`{} ~> |$|{"a": $reduce([1..40], function($acc, $i){{"a": $acc, "b": $acc}}, {"x": [1][]})}|`,
	} {
		t.Run(expr, func(t *testing.T) {
			compiled, err := gnata.Compile(expr)
			if err != nil {
				t.Fatal(err)
			}
			got, err := compiled.Eval(context.Background(), nil)
			if err != nil || got == nil {
				t.Fatalf("got %v, %v", got, err)
			}
			// Follow one path down: walking every path would take 2^40 steps.
			for got != nil {
				if typ := fmt.Sprintf("%T", got); strings.HasPrefix(typ, "evaluator.") {
					t.Fatalf("result holds internal type %s", typ)
				}
				switch v := got.(type) {
				case *gnata.OrderedMap:
					got, _ = v.Get("a")
				case []any:
					got = v[0]
				default:
					got = nil
				}
			}
		})
	}
}

// TestTransformNeverBuildsCycles checks that transforms whose update values
// lead back to the target, also through a variable the pattern or an
// earlier target's update bound, return results that encode as JSON.
func TestTransformNeverBuildsCycles(t *testing.T) {
	for _, tC := range []struct {
		expr string
		code string // the error expected instead of a result
	}{
		{expr: `$ ~> |nested.x|{"z": $}|`},
		{expr: `$ ~> |[$, nested]|{"w": $g, "z": $g := $}|`},
		{expr: `$ ~> |[$g := $, nested][1]|{"w": $g}|`},
		// Cloning a function makes it "", so calling it fails, as in jsonata-js.
		{expr: `($t := |$|{"k": $g := $.f()}, ["f"]|; $ ~> |[$, nested]|{"w": $g, "z": $t({"f": function(){$}})}|)`, code: "T1006"},
		{expr: `$ ~> |**|{"w": [$g], "z": $g := $}|`},
		{expr: `$ ~> |**|{"w": {"v": $g}, "z": $g := $}|`},
		{expr: `$ ~> |**|{"s": $exists($f) ? $f() : 0, "k": $f := function(){$}}| ~> |**|{}, ["k"]|`},
	} {
		t.Run(tC.expr, func(t *testing.T) {
			compiled, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatal(err)
			}
			got, err := compiled.EvalBytes(context.Background(), json.RawMessage(`{"nested":{"x":{"y":3}}}`))
			if tC.code != "" {
				if err == nil || !strings.Contains(err.Error(), tC.code) {
					t.Fatalf("got %v, %v; want error %s", got, err, tC.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := json.Marshal(got); err != nil {
				t.Fatalf("encoding the result: %v", err)
			}
		})
	}
}

// referencedPathsInput is the input TestReferencedPaths prunes to each
// expression's paths, to check that its result doesn't change.
var referencedPathsInput = func() any {
	input, err := gnata.DecodeJSON(json.RawMessage(`{
		"a": [{"b": 1, "c": 2, "k": "x", "v": 3}, {"b": 2, "c": "q", "x": 1}],
		"b": "hello", "c": {"d": 1, "secret": "s"}, "d": 5, "s": "str", "n": 2,
		"items": [{"price": 2, "name": "p"}, {"price": 1, "name": "q"}],
		"x": {"y": [1, 2], "z": [{"w": 2, "n": 1}, {"w": 1}], "n": 3, "w": 1, "secret": "S"}, "y": {"q": 1},
		"status": "active", "name": "N", "secret": "S",
		"Account": {"Order": [{"Price": 2}, {"Price": 3}]},
		"arr": [{"n": 1}, {"n": 2}], "obj": {"p": 1, "q": -1}
	}`))
	if err != nil {
		panic(err)
	}
	return input
}()

type referencedPathsCase struct {
	expr string
	want [][]string
	// partial is the expected !complete.
	partial bool
}

func referencedPathsCases() []referencedPathsCase {
	return []referencedPathsCase{
		{expr: `a`, want: [][]string{{"a"}}},
		{expr: `a.b.c = "x"`, want: [][]string{{"a", "b", "c"}}},
		{expr: `"a.b"`, want: nil},
		{expr: "`first name`.value", want: [][]string{{"first name", "value"}}},
		{expr: "`a\x00b` = 1 and a.b = 2", want: [][]string{{"a\x00b"}, {"a", "b"}}},
		{expr: `$exists(a) and a.b != ""`, want: [][]string{{"a"}, {"a", "b"}}},
		{expr: `$contains(a.b, "x") or $lowercase(c) = "y"`, want: [][]string{{"a", "b"}, {"c"}}},
		{expr: `a[b = 1].c`, want: [][]string{{"a", "b"}, {"a", "c"}}},
		{expr: `a[0].b`, want: [][]string{{"a", "b"}}},
		{expr: `(a)[b = 1].x`, want: [][]string{{"a", "b"}, {"a", "x"}}},
		{expr: `a.(b + c)`, want: [][]string{{"a"}, {"a", "b"}, {"a", "c"}}},
		{expr: `a.*.c`, want: [][]string{{"a"}}},
		{expr: `a.**.b`, want: [][]string{{"a"}}},
		{expr: `a.b ? c : d.e`, want: [][]string{{"a", "b"}, {"c"}, {"d", "e"}}},
		{expr: `a.b.%.c`, want: [][]string{{"a", "b"}, {"a", "c"}}},
		{expr: `a.b[%.c = 1]`, want: [][]string{{"a", "b"}, {"a", "c"}}},
		{expr: `{"k": a.b, "n": [c, d]}`, want: [][]string{{"a", "b"}, {"c"}, {"d"}}},
		{expr: `a.b = 1 and a.b = 2`, want: [][]string{{"a", "b"}}},

		{expr: `$$.a.b`, want: [][]string{{"a", "b"}}},
		{expr: `$.a`, want: [][]string{{"a"}}},
		{expr: `$[status = "active"].name`, want: [][]string{{"status"}, {"name"}}},
		{expr: `$$[status = "active"].name`, want: [][]string{{"status"}, {"name"}}},
		{expr: `a.$$.b`, want: [][]string{{"a"}, {"b"}}},
		{expr: `a.$.b`, want: [][]string{{"a", "b"}}},
		{expr: `($x := $; $x.secret)`, want: [][]string{{"secret"}}},
		{expr: `($x := a.b; $x.c)`, want: [][]string{{"a", "b", "c"}}},
		{expr: `$x.b`, partial: true},

		{expr: `$map([1, 2], function($v){ b })`, want: [][]string{{"b"}}},
		{expr: `($f := function(){ c }; $f())`, want: [][]string{{"c"}}},
		{expr: `$map(a, function($v){ $v.b })`, want: [][]string{{"a"}}, partial: true},
		{expr: `$map(a, function($v){ $$.c })`, want: [][]string{{"a"}, {"c"}}},

		{expr: `*`, partial: true},
		{expr: `**.Price`, partial: true},
		{expr: `$`, partial: true},
		{expr: `$lookup($, "c")`, partial: true},
		{expr: `$keys($)`, partial: true},
		{expr: `$string()`, partial: true},
		{expr: `$exists($now())`},
		{expr: `a.$string()`, want: [][]string{{"a"}}},
		{expr: `a.$substring(0, 2)`, want: [][]string{{"a"}}},
		{expr: `$substring(a, 0, 2)`, want: [][]string{{"a"}}},
		{expr: `$myCustom(a)`, want: [][]string{{"a"}}, partial: true},
		{expr: `a ~> $string()`, want: [][]string{{"a"}}},

		{expr: `a ~> |b|{"c": d}|`, want: [][]string{{"a"}, {"a", "b"}, {"a", "b", "d"}}},
		{expr: `a ~> |b|{"c": d}| ~> $string()`, want: [][]string{{"a"}, {"a", "b"}, {"a", "b", "d"}}},
		{
			expr:    `$ ~> |Account.Order|{"Total": Price * 2}|`,
			want:    [][]string{{"Account", "Order"}, {"Account", "Order", "Price"}},
			partial: true,
		},
		{expr: `( $t := |b|{"c": d}|; a ~> $t )`, want: [][]string{{"a"}}},

		{expr: `a^(b, >c)`, want: [][]string{{"a", "b"}, {"a", "c"}, {"a"}}},
		{expr: `items^(price).name`, want: [][]string{{"items", "price"}, {"items", "name"}}},
		{expr: `a{b: $sum(c)}`, want: [][]string{{"a", "b"}, {"a", "c"}, {"a"}}},
		{expr: `a[b = 1]{k: v}`, want: [][]string{{"a", "b"}, {"a", "k"}, {"a", "v"}, {"a"}}},

		{expr: `x.y@$p.z.w`, want: [][]string{{"x", "y"}, {"x", "z", "w"}}},
		{expr: `x.y@$p.$p.n`, want: [][]string{{"x", "y"}, {"x"}, {"x", "y", "n"}}},
		{expr: `x.y@$p.z^(w)`, want: [][]string{{"x", "y"}, {"x", "z", "w"}, {"x", "z"}}},
		{expr: `x.y@$p.z{k: v}`, want: [][]string{{"x", "y"}, {"x", "z", "k"}, {"x", "z", "v"}, {"x", "z"}}},
		{expr: `x.y@$p.z ~> |q|{"r": s}|`, want: [][]string{{"x", "y"}, {"x", "z"}, {"x", "z", "q"}, {"x", "z", "q", "s"}}},
		{expr: `a#$i[$i < 2].b`, want: [][]string{{"a", "b"}}},
		{expr: `a@$p[$p.x = 1].b`, want: [][]string{{"a"}, {"a", "x"}, {"b"}}},
		{expr: `$string(a)`, want: [][]string{{"a"}}},
		{expr: `$substring(0, 2)`, partial: true},

		{expr: `a.{"k": 1}`, want: [][]string{{"a"}}},
		{expr: `a.[1]`, want: [][]string{{"a"}}},
		{expr: `a.$string("x")`, want: [][]string{{"a"}}},
		{expr: `($f := function(){ 1 }; a.$f())`, want: [][]string{{"a"}}},
		{expr: `$x := c`, want: [][]string{{"c"}}},
		{expr: `$string($x := s)`, want: [][]string{{"s"}}},
		{expr: `{"k": $x := a}`, want: [][]string{{"a"}}},
		{expr: `($x := a; $f := function(){ $x }; $x := c; $f())`, partial: true},
		{expr: `($p := c; x.y@$p.z; $p.d)`, want: [][]string{{"x", "y"}, {"x", "z"}}, partial: true},
		{expr: `(d ? $x := a : 1; $x.b)`, want: [][]string{{"d"}, {"a"}}, partial: true},
		{expr: `x.y@$p.z.$p`, want: [][]string{{"x", "y"}, {"x", "z"}}},
		{expr: `b.$eval("$$.secret")`, want: [][]string{{"b"}}, partial: true},
		{expr: `b.$substring($$.n, 1)`, want: [][]string{{"b"}, {"n"}}},
		{expr: `obj.$sift(function($v){ $v > 0 })`, want: [][]string{{"obj"}}, partial: true},
		{expr: `obj.$sift(function(){ true })`, want: [][]string{{"obj"}}},
		{expr: "b.$split(/l/, 1)", want: [][]string{{"b"}}},
		{expr: `arr@$p.%.n`, want: [][]string{{"arr"}}, partial: true},
		{expr: `a.*.%.%.b`, want: [][]string{{"a"}}, partial: true},
		{expr: `x.z.%@$t.%.secret`, want: [][]string{{"x", "z"}, {"x"}, {"x", "secret"}}, partial: true},
		{expr: `x.z.w.%@$t.%.secret`, want: [][]string{{"x", "z", "w"}, {"x", "z"}, {"x", "z", "secret"}}, partial: true},
		{expr: `c.($f := function(){ $string() }; $f())`, want: [][]string{{"c"}}},
		{expr: `c.($f := function(){ $string() }; obj.$f())`, want: [][]string{{"c"}, {"c", "obj"}}},
		{expr: `"secret" ~> $lookup()`, partial: true},
		{expr: `c.("secret" ~> $lookup())`, want: [][]string{{"c"}}},
		{expr: strings.Repeat("a.", 400) + "b", want: [][]string{append(slices.Repeat([]string{"a"}, 400), "b")}},
	}
}

// referencedPathsCallerContextCases call a lambda that reads its caller's
// context in ways other than by its bound name.
func referencedPathsCallerContextCases() []referencedPathsCase {
	return []referencedPathsCase{
		{expr: `y.($f := function(){ $lookup("secret") }; $g := [$f]; $$.x[$g[0]() = "S"].w)`, want: [][]string{{"y"}, {"x"}, {"x", "w"}}},
		{expr: `y.($h := function(){ λ(){ $lookup("secret") } }; $$.x[$h()() = "S"].w)`, want: [][]string{{"y"}, {"x"}, {"x", "w"}}},
		{expr: `y.($f := function(){ (function(){ $lookup("secret") })() }; $$.x[$f() = "S"].w)`, want: [][]string{{"y"}, {"x"}, {"x", "w"}}},
		{
			expr: `y.($a := function(){ $sum() }; $sum := function(){ $lookup("secret") }; $$.x[$a() = "S"].w)`,
			want: [][]string{{"y"}, {"x"}, {"x", "w"}},
		},
	}
}

func TestReferencedPaths(t *testing.T) {
	testCases := append(referencedPathsCases(), referencedPathsCallerContextCases()...)
	for _, tC := range testCases {
		t.Run(tC.expr, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			got, complete := expr.ReferencedPaths()
			if !reflect.DeepEqual(got, tC.want) || complete == tC.partial {
				t.Fatalf("ReferencedPaths = %v, %v; want %v, %v", got, complete, tC.want, !tC.partial)
			}
			if want, err := expr.Eval(context.Background(), referencedPathsInput); err == nil {
				checkReferencedPaths(t, tC.expr, expr, referencedPathsInput, nil, want)
			}
		})
	}
}

func TestReferencedPaths_SharesPrefixes(t *testing.T) {
	src := strings.Repeat("a.", 4000) + "(" + strings.Repeat("b;", 2000) + "b)"
	expr, err := gnata.Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	paths, complete := expr.ReferencedPaths()
	if !complete || len(paths) != 2 {
		t.Fatalf("ReferencedPaths = %d paths, %v; want the prefix and the prefix with b, complete", len(paths), complete)
	}
}

func TestReferencedPaths_CapsSize(t *testing.T) {
	fields := make([]string, 70000)
	for i := range fields {
		fields[i] = fmt.Sprintf("f%d", i)
	}
	testCases := []struct {
		desc string
		src  string
	}{
		{desc: "more distinct fields than the path cap", src: "[" + strings.Join(fields, ",") + "]"},
		{desc: "more field names than the element cap", src: strings.Repeat("a.(", 3000) + "b" + strings.Repeat(")", 3000)},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			expr, err := gnata.Compile(tC.src)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			paths, complete := expr.ReferencedPaths()
			if complete {
				t.Fatal("complete = true for an expression past the size cap")
			}
			names := 0
			for _, path := range paths {
				names += len(path)
			}
			if names > 1<<16 {
				t.Fatalf("kept %d field names, want at most %d", names, 1<<16)
			}
		})
	}
}

func TestReferencedPathsIn_CustomFunctionReplacingBuiltin(t *testing.T) {
	expr, err := gnata.Compile(`$uppercase("x")`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, complete := expr.ReferencedPaths(); !complete {
		t.Fatal("the builtin $uppercase of a literal reads no input, want complete")
	}
	env := gnata.NewCustomEnvironment(map[string]gnata.CustomFunc{
		"uppercase": func(_ []any, focus any) (any, error) { return focus, nil },
	})
	if _, complete := expr.ReferencedPathsIn(env); complete {
		t.Fatal("a custom $uppercase is given the whole input, want incomplete")
	}
	if _, complete := expr.ReferencedPathsIn(nil); !complete {
		t.Fatal("a nil environment is the builtins, want complete")
	}
}
