package gnata_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/recolabs/gnata"
)

func TestGuardrailsDefaultUnaffected(t *testing.T) {
	const factorial = "$factorial := function($n){$n = 0 ? 1 : $n * $factorial($n - 1)}"
	testCases := []struct {
		desc string
		expr string
		want any
		code string
	}{
		{desc: "factorial 99 within default stack", expr: "(" + factorial + "; $factorial(99))", want: 9.33262154439441e+155},
		{desc: "factorial 100 exceeds default stack", expr: "(" + factorial + "; $factorial(100))", code: "U1001"},
		{desc: "range at hard cap", expr: "1..10000001", code: "D2014"},
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

func TestEvalKeepsGuardrailCodes(t *testing.T) {
	const factorial = "$factorial := function($n){$n = 0 ? 1 : $n * $factorial($n - 1)}"
	testCases := []struct {
		desc string
		expr string
		code string
	}{
		{desc: "stack overflow", expr: `$eval("(` + factorial + `; $factorial(100))")`, code: "U1001"},
		{desc: "nested eval error wrapped once", expr: `$eval("$eval('1 + \"a\"')")`, code: "D3121: $eval: T2002"},
		{desc: "ordinary error wrapped", expr: `$eval("1 + 'a'")`, code: "D3121"},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if _, err := e.Eval(context.Background(), nil); err == nil || !strings.HasPrefix(err.Error(), tC.code) {
				t.Fatalf("expected error starting with %s, got %v", tC.code, err)
			}
		})
	}
}

func TestWithStack(t *testing.T) {
	e, err := gnata.Compile(
		"($factorial := function($n){$n = 0 ? 1 : $n * $factorial($n - 1)}; $factorial(20))",
		gnata.WithStack(5),
	)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = e.Eval(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "D1011") {
		t.Fatalf("expected D1011, got %v", err)
	}
}

// TestWithStack_BuiltinTailCalls checks that a lambda recursing through a
// higher-order built-in in tail position is held to the stack limit.
func TestWithStack_BuiltinTailCalls(t *testing.T) {
	for _, expr := range []string{
		`($f := function($n){ $map([$n+1], $f) }; $f(0))`,
		`($f := function($n){ $filter([$n+1], $f) }; $f(0))`,
		`($f := function($n){ $reduce([$n, $n+1], function($a, $b){ $f($b) }) }; $f(0))`,
		`($f := function($n){ $sort([$n, $n+1], function($a, $b){ $f($b) }) }; $f(0))`,
		`($f := function($n){ $each({"k": $n+1}, $f) }; $f(0))`,
		`($f := function($n){ $single([$n+1], $f) }; $f(0))`,
		`($f := function($n){ ($map([$n+1], $f)) }; $f(0))`,
	} {
		t.Run(expr, func(t *testing.T) {
			e, err := gnata.Compile(expr, gnata.WithStack(50))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if _, err := e.Eval(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "D1011") {
				t.Fatalf("expected D1011, got %v", err)
			}
		})
	}
}

func TestWithTimeout(t *testing.T) {
	e, err := gnata.Compile("1..10000000#$i[$i % 2 = 0]", gnata.WithTimeout(1*time.Nanosecond))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = e.Eval(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "D1012") {
		t.Fatalf("expected D1012, got %v", err)
	}
}

// TestWithTimeout_SlowCallsBoundOverrun checks that the deadline is enforced
// at function-call granularity: with a slow custom function evaluated many
// times, evaluation must stop within a few calls of the deadline, not after
// the 128-node polling interval.
func TestWithTimeout_SlowCallsBoundOverrun(t *testing.T) {
	const callCost = 5 * time.Millisecond
	env := gnata.NewCustomEnv(map[string]gnata.CustomFunc{
		"slow": func(args []any, _ any) (any, error) {
			time.Sleep(callCost)
			return args[0], nil
		},
	})
	e, err := gnata.Compile("$map([1..1000], function($v) { $slow($v) })", gnata.WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	start := time.Now()
	_, err = e.EvalWithCustomFuncs(context.Background(), nil, env)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "D1012") {
		t.Fatalf("expected D1012, got %v", err)
	}
	if limit := 20*time.Millisecond + 10*callCost; elapsed > limit {
		t.Fatalf("evaluation ran %v past start, want at most %v (deadline overrun not bounded per call)", elapsed, limit)
	}
}

func TestWithTimeout_ParentCancellationPreserved(t *testing.T) {
	e, err := gnata.Compile("1+1", gnata.WithTimeout(time.Minute))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = e.Eval(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestWithSequence(t *testing.T) {
	wideObject := map[string]any{}
	for i := range 20 {
		wideObject[fmt.Sprintf("k%d", i)] = i
	}
	testCases := []struct {
		desc string
		expr string
		data any
	}{
		{desc: "range exceeds sequence guardrail", expr: "1..100"},
		{desc: "append exceeds sequence guardrail", expr: "$append([1,2,3,4,5,6], [7,8,9,10,11,12])"},
		{desc: "map exceeds sequence guardrail", expr: "$map([1,2,3,4,5,6,7,8,9,10,11,12], function($x){$x})"},
		{desc: "filter exceeds sequence guardrail", expr: "$filter([1,2,3,4,5,6,7,8,9,10,11,12], function($x){true})"},
		{
			desc: "each exceeds sequence guardrail",
			expr: "$each({'a':1,'b':2,'c':3,'d':4,'e':5,'f':6,'g':7,'h':8,'i':9,'j':10,'k':11}, function($v,$k){$v})",
		},
		{desc: "wildcard exceeds sequence guardrail", expr: "*", data: wideObject},
		{desc: "descendant exceeds sequence guardrail", expr: "**", data: wideObject},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr, gnata.WithSequence(10))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			_, err = e.Eval(context.Background(), tC.data)
			if err == nil || !strings.Contains(err.Error(), "D2015") {
				t.Fatalf("expected D2015, got %v", err)
			}
		})
	}
}

// A tuple filter whose predicate repeats each tuple's own position keeps it
// once per repeat, so 200 tuples × 200 repeats grows past the guardrail while
// every intermediate sequence stays under it. Filtering input data larger
// than the guardrail is not growth and stays allowed.
func TestWithSequence_RepeatedTuplePositions(t *testing.T) {
	items := make([]any, 200)
	for i := range items {
		items[i] = map[string]any{"b": float64(i)}
	}
	e, err := gnata.Compile("$count(a.b#$i[($v := $; $map([1..200], function($x){$v}))])", gnata.WithSequence(1000))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = e.Eval(context.Background(), map[string]any{"a": items})
	if err == nil || !strings.Contains(err.Error(), "D2015") {
		t.Fatalf("expected D2015, got %v", err)
	}
	e, err = gnata.Compile("$count(a.b#$i[true])", gnata.WithSequence(100))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got, err := e.Eval(context.Background(), map[string]any{"a": items}); err != nil || got != 200.0 {
		t.Fatalf("filtering input larger than the guardrail: got %v, %v", got, err)
	}
}

func TestWithSequence_UnderLimitUnaffected(t *testing.T) {
	e, err := gnata.Compile("1..5", gnata.WithSequence(10))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := e.Eval(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !gnata.DeepEqual(got, []any{1.0, 2.0, 3.0, 4.0, 5.0}) {
		t.Fatalf("got %v", got)
	}
}
