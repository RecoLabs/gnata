package gnata_test

import (
	"context"
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
