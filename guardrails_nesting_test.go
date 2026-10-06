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
	sortsBySort := func(depth int) string {
		return fmt.Sprintf(`($d := $reduce([1..%d], function($a,$i){[[$a], $sort]}, [2,1]); $count($sort($d, $sort)))`, depth)
	}
	sortsByPartial := func(depth int) string {
		return fmt.Sprintf(`($p := $sort(?, ?); $d := $reduce([1..%d], function($a,$i){[[$a], $p]}, [2,1]); $count($sort($d, $p)))`, depth)
	}
	deepBodies := func(depth int) string {
		body := strings.Repeat("$string(", depth) + "$f($n-1)" + strings.Repeat(")", depth)
		return `($f := function($n){$n = 0 ? 0 : ` + body + `}; $f(99))`
	}
	transformChain := func(n, depth int) string {
		return fmt.Sprintf(`$reduce([1..%d], function($f,$i){| $ | ({"a":`, n) + strings.Repeat(`{"a":`, depth) + "$f($)" +
			strings.Repeat("}", depth+1) + `; {}) |}, | $ | {} |)({"k":1})`
	}
	nested := func(depth int) string { return strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth) }
	const tooNested = "U1001: stack overflow error: nested calls exceeded the nesting budget of 5000"
	runNestingCases(t, []nestingCase{
		{desc: "partial applications within nesting limit", expr: partials(4_999), want: float64(5)},
		{desc: "partial applications exceed nesting limit", expr: partials(5_001), code: tooNested},
		{desc: "compositions within nesting limit", expr: compositions(4_999), want: "5"},
		{desc: "compositions exceed nesting limit", expr: compositions(5_001), code: tooNested},
		{desc: "transforms within nesting limit", expr: transforms(1_000), want: map[string]any{"k": float64(1)}},
		{desc: "transforms exceed nesting limit", expr: transforms(3_000_000), code: tooNested},
		{desc: "transforms with shallow clauses within nesting limit", expr: transformChain(200, 60), want: map[string]any{"k": float64(1)}},
		{desc: "transforms with shallow clauses exceed nesting limit", expr: transformChain(4_999, 60), code: tooNested},
		{desc: "deep transforms exceed nesting limit", expr: deepTransforms(1_000, 3_000), code: tooNested},
		{desc: "builtin callbacks within nesting limit", expr: sortsBySort(4_999), want: float64(2)},
		{desc: "builtin callbacks exceed nesting limit", expr: sortsBySort(1_000_000), code: tooNested},
		{desc: "builtin callbacks through partials within nesting limit", expr: sortsByPartial(4_999), want: float64(2)},
		{desc: "builtin callbacks through partials exceed nesting limit", expr: sortsByPartial(5_001), code: tooNested},
		{desc: "lambdas with shallow bodies bounded by the call depth alone", expr: deepBodies(60), want: "0"},
		{desc: "lambdas with moderately deep bodies within nesting limit", expr: deepBodies(150), want: "0"},
		{desc: "lambdas with deep bodies exceed nesting limit", expr: deepBodies(400), code: tooNested},
		{
			desc: "deep lambda bodies in tail position release the budget",
			expr: `($f := function($n){` + strings.Repeat("($n > -1 ? ", 300) + "($n = 0 ? 0 : $f($n-1))" +
				strings.Repeat(" : 0)", 300) + `}; $f(99))`,
			want: float64(0),
		},
		{
			desc: "recursion through a transform with a moderately deep clause",
			expr: `($f := function($n){$n = 0 ? 0 : ({} ~> | $ | {"a": ` + strings.Repeat("[", 100) + "$f($n-1)" +
				strings.Repeat("]", 100) + `} |)}; $count($f(98)))`,
			want: float64(1),
		},
		{desc: "recursion through a transform within default stack", expr: recursiveTransform(99), want: float64(2)},
		{desc: "deep transforms in a group exceed nesting limit", expr: groupedTransforms(10_000, 3_000), code: tooNested},
		{
			desc: "inner transform charged when called, not with the outer one",
			expr: `($f := function($n){$n = 0 ? {} : ({"a":1} ~> | $ | ($ ~> | $ | {"b": $f($n-1), "c": ` + nested(110) +
				`} |) |)}; $count($keys($f(99))))`,
			want: float64(3),
		},
		{
			desc: "uncalled lambda body not charged",
			expr: `($f := function($n){$n = 0 ? {} : ({"a":1} ~> | $ | {"b": $f($n-1), "g": function(){` + nested(600) +
				`}} |)}; $count($keys($f(99))))`,
			want: float64(3),
		},
	})
}

// Lambdas, and wrappers that call only lambdas, are bounded by WithStack's
// call depth, not by the nesting budget.
func TestWithStackThroughFunctionValues(t *testing.T) {
	withStack := []gnata.Option{gnata.WithStack(20_000)}
	runNestingCases(t, []nestingCase{
		{
			desc: "WithStack bounds recursion through $map",
			expr: `($f := function($n){$n = 0 ? 0 : $map([$n], function($x){$f($x - 1)})[0] + 1}; $f(6000))`,
			opts: withStack, want: float64(6000),
		},
		{
			desc: "WithStack bounds recursion through a function parameter",
			expr: `($f := function($n, $g){$n = 0 ? 0 : $g($n - 1, $g) + 1}; $f(6000, $f))`,
			opts: withStack, want: float64(6000),
		},
		{
			desc: "WithStack bounds recursion through a partial application",
			expr: `($f := function($n){$n = 0 ? 0 : $p($n - 1) + 1}; $p := $f(?); $p(6000))`,
			opts: withStack, want: float64(6000),
		},
		{
			desc: "WithStack bounds recursion through a partial application argument",
			expr: `($f := function($n, $g){$n = 0 ? 0 : $g($n - 1, $g) + 1}; $f(6000, $f(?, ?)))`,
			opts: withStack, want: float64(6000),
		},
		{
			desc: "WithStack bounds recursion through a composition",
			expr: `($f := function($n){$n = 0 ? 0 : $c($n - 1) + 1}; $c := $f ~> function($x){$x}; $c(6000))`,
			opts: withStack, want: float64(6000),
		},
		{
			desc: "WithStack bounds recursion with moderately deep bodies",
			expr: `($f := function($n){$n = 0 ? 0 : ` + strings.Repeat("$string(", 80) + "$f($n-1)" + strings.Repeat(")", 80) + `}; $f(500))`,
			opts: withStack, want: "0",
		},
		{
			desc: "endless recursion through $map",
			expr: `($f := function($n){$map([$n], function($x){$f($x)})}; $f(1))`,
			opts: withStack, code: "D1011",
		},
	})
}

type nestingCase struct {
	desc string
	expr string
	opts []gnata.Option
	want any
	code string
}

func runNestingCases(t *testing.T, testCases []nestingCase) {
	t.Helper()
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr, tC.opts...)
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
