package gnata_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/recolabs/gnata"
)

func TestCompile(t *testing.T) {
	expr, err := gnata.Compile("Account.name")
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}
	if expr == nil {
		t.Fatal("expected non-nil expression")
	}
}

func TestCompile_EscapedParenInRegex(t *testing.T) {
	expr := `$contains(x, /(-foo|\bbar\b)\s*\(?\s*x\.y\s+-?eq\s+"z"/)`
	if _, err := gnata.Compile(expr); err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
}

func TestOrderedMap_TypeAssertFromEval(t *testing.T) {
	compiled, err := gnata.Compile(`{"a": 1, "b": 2}`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	result, err := compiled.Eval(context.Background(), nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	om, ok := result.(*gnata.OrderedMap)
	if !ok {
		t.Fatalf("Eval result type %T, want *gnata.OrderedMap", result)
	}
	if got, _ := om.Get("a"); got != float64(1) {
		t.Fatalf("Get(a) = %v, want 1", got)
	}
	normalized, ok := gnata.NormalizeValue(result).(map[string]any)
	if !ok {
		t.Fatalf("NormalizeValue type %T, want map[string]any", gnata.NormalizeValue(result))
	}
	if normalized["b"] != float64(2) {
		t.Fatalf("normalized[b] = %v, want 2", normalized["b"])
	}
}

func TestQuotedPathArrayIndexSelect(t *testing.T) {
	data := map[string]any{
		"testData": []any{
			map[string]any{"value": "output"},
		},
	}
	tests := []struct {
		name string
		expr string
		want any
	}{
		{name: "unquoted", expr: `testData[0].value`, want: "output"},
		{name: "quoted", expr: `"testData"[0]."value"`, want: "output"},
		{name: "mixed", expr: `testData[0]."value"`, want: "output"},
		{name: "rooted quoted", expr: `$."testData"[0]."value"`, want: "output"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := gnata.Compile(tc.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.expr, err)
			}
			got, err := compiled.Eval(context.Background(), data)
			if err != nil {
				t.Fatalf("Eval(%q): %v", tc.expr, err)
			}
			if !gnata.DeepEqual(got, tc.want) {
				t.Errorf("Eval(%q) = %#v (%T), want %#v (%T)", tc.expr, got, got, tc.want, tc.want)
			}
		})
	}
}

func TestJSONNull_TypeAssertFromEval(t *testing.T) {
	compiled, err := gnata.Compile(`{"a": null}`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	result, err := compiled.Eval(context.Background(), nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	om, ok := result.(*gnata.OrderedMap)
	if !ok {
		t.Fatalf("Eval result type %T, want *gnata.OrderedMap", result)
	}
	val, exists := om.Get("a")
	if !exists {
		t.Fatal("missing key a")
	}
	if !gnata.IsNull(val) {
		t.Fatalf("IsNull(%v) = false, want true", val)
	}
	if _, ok := val.(gnata.JSONNull); !ok {
		t.Fatalf("value type %T, want gnata.JSONNull", val)
	}
	if val != gnata.Null {
		t.Fatalf("value %v != gnata.Null", val)
	}
}

func TestEvalWithCustomEnvironmentAndVars(t *testing.T) {
	env := gnata.NewCustomEnvironment(map[string]gnata.CustomFunc{
		"greet": func(args []any, _ any) (any, error) {
			return "hello " + args[0].(string), nil
		},
	})
	compiled, err := gnata.Compile(`$greet($who)`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := compiled.EvalWithCustomEnvironmentAndVars(
		context.Background(), nil, env, map[string]any{"who": "world"},
	)
	if err != nil {
		t.Fatalf("EvalWithCustomEnvironmentAndVars: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("got %v, want %q", got, "hello world")
	}
}

func TestEvalBytesWithCustomFuncs(t *testing.T) {
	env := gnata.NewCustomEnv(map[string]gnata.CustomFunc{
		"double": func(args []any, _ any) (any, error) {
			n, err := args[0].(json.Number).Float64()
			if err != nil {
				return nil, err
			}
			return n * 2, nil
		},
	})
	compiled, err := gnata.Compile(`$double(value)`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := compiled.EvalBytesWithCustomFuncs(context.Background(), []byte(`{"value": 21}`), env)
	if err != nil {
		t.Fatalf("EvalBytesWithCustomFuncs: %v", err)
	}
	if got != float64(42) {
		t.Fatalf("got %v, want 42", got)
	}
}

func TestEvalBytesWithCustomEnvironmentAndVars(t *testing.T) {
	env := gnata.NewCustomEnvironment(map[string]gnata.CustomFunc{
		"greet": func(args []any, _ any) (any, error) {
			return "hello " + args[0].(string), nil
		},
	})
	compiled, err := gnata.Compile(`$greet($who)`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := compiled.EvalBytesWithCustomEnvironmentAndVars(
		context.Background(), []byte(`{}`), env, map[string]any{"who": "world"},
	)
	if err != nil {
		t.Fatalf("EvalBytesWithCustomEnvironmentAndVars: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("got %v, want %q", got, "hello world")
	}
}

// TestEvalBytesWithCustomFuncs_OverridesShadowedBuiltin verifies that a
// custom function registered under a name that collides with a fast-path
// builtin (here "sum") actually runs instead of the builtin gjson-based
// implementation. The function fast path dispatches purely by source-text
// name with no reference to the caller's environment, so EvalBytesWithCustomFuncs
// must skip that tier entirely — otherwise the override would be silently
// ignored whenever the fast path fires.
func TestEvalBytesWithCustomFuncs_OverridesShadowedBuiltin(t *testing.T) {
	env := gnata.NewCustomEnv(map[string]gnata.CustomFunc{
		"sum": func(_ []any, _ any) (any, error) {
			return "overridden", nil
		},
	})
	compiled, err := gnata.Compile(`$sum(nums)`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !compiled.IsFuncFastPath() {
		t.Fatal("expected IsFuncFastPath() == true so this test actually exercises the shadowing risk")
	}
	got, err := compiled.EvalBytesWithCustomFuncs(context.Background(), []byte(`{"nums": [1, 2, 3]}`), env)
	if err != nil {
		t.Fatalf("EvalBytesWithCustomFuncs: %v", err)
	}
	if got != "overridden" {
		t.Fatalf("got %v, want %q (custom function override was ignored, builtin $sum ran instead)", got, "overridden")
	}
}

// TestEvalBytesWithCustomFuncs_FastPathUnaffected checks that a pure-path
// fast-path expression still resolves via gjson (not the custom env / decode
// fallback) when evaluated through the new bytes+custom-env API — the fast
// path never references custom functions, so it must behave identically
// regardless of which environment the caller supplies.
func TestEvalBytesWithCustomFuncs_FastPathUnaffected(t *testing.T) {
	env := gnata.NewCustomEnv(nil)
	compiled, err := gnata.Compile(`Account.Order.Product.SKU`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !compiled.IsFastPath() {
		t.Fatal("expected IsFastPath() == true")
	}
	data := []byte(`{"Account":{"Order":[{"Product":[{"SKU":"a"},{"SKU":"b"}]}]}}`)
	got, err := compiled.EvalBytesWithCustomFuncs(context.Background(), data, env)
	if err != nil {
		t.Fatalf("EvalBytesWithCustomFuncs: %v", err)
	}
	want := []any{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestDeepEqual(t *testing.T) {
	tests := []struct {
		a, b any
		want bool
	}{
		{nil, nil, true},
		{nil, 1.0, false},
		{1.0, 1.0, true},
		{1.0, 2.0, false},
		{"hello", "hello", true},
		{"hello", "world", false},
		{true, true, true},
		{true, false, false},
		{[]any{1.0, 2.0}, []any{1.0, 2.0}, true},
		{[]any{1.0}, []any{1.0, 2.0}, false},
		{map[string]any{"a": 1.0}, map[string]any{"a": 1.0}, true},
		{map[string]any{"a": 1.0}, map[string]any{"a": 2.0}, false},
	}
	for _, tt := range tests {
		got := gnata.DeepEqual(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("DeepEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
