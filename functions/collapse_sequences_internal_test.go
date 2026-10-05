//go:build !tinygo

package functions

import (
	"runtime/debug"
	"testing"

	"github.com/recolabs/gnata/internal/evaluator"
)

// $string collapses a sequence nested in lone sequences 200,000 deep without
// recursing, under a 16 MB stack limit.
func TestStringOfNestedSequences(t *testing.T) {
	prev := debug.SetMaxStack(16 << 20)
	t.Cleanup(func() { debug.SetMaxStack(prev) })
	var v any = []any{1.0, 2.0}
	for range 200_000 {
		seq := evaluator.CreateSequence()
		seq.Values = append(seq.Values, v)
		v = seq
	}
	for _, prettify := range []bool{false, true} {
		got, err := valueToString(v, prettify, 0)
		if err != nil {
			t.Fatalf("valueToString: %v", err)
		}
		if want := map[bool]string{false: "[1,2]", true: "[\n  1,\n  2\n]"}[prettify]; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if got, want := sanitizeForJSON([]any{v}, 0), []any{[]any{1.0, 2.0}}; !evaluator.DeepEqual(got, want) {
		t.Fatalf("sanitizeForJSON got %v, want %v", got, want)
	}
}
