package gnata_test

import (
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
