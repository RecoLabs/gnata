//go:build !tinygo

package gnata_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/recolabs/gnata"
)

// Chains near the nesting limit need tens of MB of Go stack, more than the
// fixed stacks the TinyGo test run uses.
func TestNestedFunctionValueLimit(t *testing.T) {
	partials := func(n int) string {
		return fmt.Sprintf("($b := function($f,$n){($n = 0) ? $f : $b($f(?), $n-1)}; $b(function($x){$x}, %d)(5))", n)
	}
	compositions := func(n int) string {
		return fmt.Sprintf("($b := function($f,$n){($n = 0) ? $f : $b($f ~> $string, $n-1)}; $b($string, %d)(5))", n)
	}
	transforms := func(n int) string {
		return fmt.Sprintf(`$reduce([1..%d], function($f,$i){| $ | ($f($); {}) |}, | $ | {} |)({"k":1})`, n)
	}
	deepTransforms := func(n, depth int) string {
		return fmt.Sprintf(`$reduce([1..%d], function($f,$i){| $ | (%s$f($)%s; {}) |}, | $ | {} |)({"k":1})`,
			n, strings.Repeat("(", depth), strings.Repeat(")", depth))
	}
	recursiveTransform := func(n int) string {
		return fmt.Sprintf(`($f := function($n){$n = 0 ? {} : ({"a":1} ~> | $ | {"b": $f($n-1)} |)}; $count($keys($f(%d))))`, n)
	}
	groupedTransforms := func(n, depth int) string {
		return fmt.Sprintf(`$reduce([1..%d], function($f,$i){$lookup(({"a":1}){"k": | $ | (%s$f($)%s; {}) |}, "k")}, | $ | {} |)({"k":1})`,
			n, strings.Repeat("(", depth), strings.Repeat(")", depth))
	}
	nested := func(depth int) string { return strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth) }
	const tooNested = "U1001: stack overflow error: function values nested more than 50000 deep"
	testCases := []struct {
		desc string
		expr string
		want any
		code string
	}{
		{desc: "partial applications within nesting limit", expr: partials(49_999), want: float64(5)},
		{desc: "partial applications exceed nesting limit", expr: partials(50_001), code: tooNested},
		{desc: "compositions within nesting limit", expr: compositions(49_999), want: "5"},
		{desc: "compositions exceed nesting limit", expr: compositions(50_001), code: tooNested},
		{desc: "transforms within nesting limit", expr: transforms(10_000), want: map[string]any{"k": float64(1)}},
		{desc: "transforms exceed nesting limit", expr: transforms(3_000_000), code: tooNested},
		{desc: "deep transforms exceed nesting limit", expr: deepTransforms(1_000, 3_000), code: tooNested},
		{desc: "recursion through a transform within default stack", expr: recursiveTransform(99), want: float64(2)},
		{desc: "deep transforms in a group exceed nesting limit", expr: groupedTransforms(10_000, 3_000), code: tooNested},
		{
			desc: "inner transform charged when called, not with the outer one",
			expr: `($f := function($n){$n = 0 ? {} : ({"a":1} ~> | $ | ($ ~> | $ | {"b": $f($n-1), "c": ` + nested(250) +
				`} |) |)}; $count($keys($f(99))))`,
			want: float64(3),
		},
		{
			desc: "uncalled lambda body not charged",
			expr: `($f := function($n){$n = 0 ? {} : ({"a":1} ~> | $ | {"b": $f($n-1), "g": function(){` + nested(600) +
				`}} |)}; $count($keys($f(99))))`,
			want: float64(3),
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, err := e.Eval(context.Background(), nil)
			if tC.code != "" {
				if err == nil || !strings.Contains(err.Error(), tC.code) {
					t.Fatalf("expected error code %s, got result=%v err=%v", tC.code, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("want %v, got %v", tC.want, got)
			}
		})
	}
}
