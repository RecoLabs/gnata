package gnata_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/recolabs/gnata"
	"github.com/recolabs/gnata/internal/parser"
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

// nestingShapes build an expression whose nesting grows by one level per n.
var nestingShapes = map[string]func(n int) string{
	"parens":    func(n int) string { return strings.Repeat("(", n) + "1" + strings.Repeat(")", n) },
	"arrays":    func(n int) string { return strings.Repeat("[", n) + "1" + strings.Repeat("]", n) },
	"objects":   func(n int) string { return strings.Repeat(`{"a":`, n) + "1" + strings.Repeat("}", n) },
	"negation":  func(n int) string { return strings.Repeat("-", n) + "1" },
	"chain":     func(n int) string { return "1" + strings.Repeat("+1", n) },
	"path":      func(n int) string { return "a" + strings.Repeat(".a", n) },
	"predicate": func(n int) string { return "a" + strings.Repeat("[true]", n) },
	"condition": func(n int) string { return strings.Repeat("true ? ", n) + "1" },
	"binding":   func(n int) string { return strings.Repeat("$x := ", n) + "1" },
	"calls":     func(n int) string { return strings.Repeat("$string(", n) + "1" + strings.Repeat(")", n) },
}

func TestNestingDepthLimit(t *testing.T) {
	for name, build := range nestingShapes {
		t.Run(name, func(t *testing.T) {
			e, err := gnata.Compile(build(parser.MaxDepth / 2))
			if err != nil {
				t.Fatalf("compile at half the limit: %v", err)
			}
			if _, err := e.Eval(context.Background(), nil); err != nil {
				t.Fatalf("eval at half the limit: %v", err)
			}
			if _, err := gnata.Compile(build(parser.MaxDepth + 2)); err == nil || !strings.Contains(err.Error(), "S0218") {
				t.Fatalf("compile past the limit: want S0218, got %v", err)
			}
		})
	}
	t.Run("exact limit", func(t *testing.T) {
		// The innermost 1 is one level, and each pair of parentheses adds one.
		if _, err := gnata.Compile(nestingShapes["parens"](parser.MaxDepth - 1)); err != nil {
			t.Fatalf("at the limit: %v", err)
		}
		if _, err := gnata.Compile(nestingShapes["parens"](parser.MaxDepth)); err == nil || !strings.Contains(err.Error(), "S0218") {
			t.Fatalf("one past the limit: want S0218, got %v", err)
		}
	})
	t.Run("eval", func(t *testing.T) {
		e, err := gnata.Compile(`$eval($)`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Eval(context.Background(), nestingShapes["parens"](parser.MaxDepth))
		if err == nil || !strings.Contains(err.Error(), "D3120") || !strings.Contains(err.Error(), "S0218") {
			t.Fatalf("want D3120 wrapping S0218, got %v", err)
		}
	})
}

func TestRecursionNestingLimit(t *testing.T) {
	// Each call of $f re-enters a body nested depth levels deep.
	recurse := func(depth, n int) string {
		body := strings.Repeat(`{"a":`, depth) + `($n = 0 ? 0 : $f($n - 1))` + strings.Repeat("}", depth)
		return fmt.Sprintf("($f := function($n){%s}; $f(%d))", body, n)
	}
	// compose wraps $string n times with step, using tail calls.
	compose := func(n int, step string) string {
		return fmt.Sprintf("($build := function($n, $f){ $n = 0 ? $f : $build($n - 1, %s) }; $build(%d, $string)(1))", step, n)
	}
	testCases := []struct {
		desc string
		expr string
		opts []gnata.Option
		code string
	}{
		{desc: "within the nesting budget", expr: recurse(20, 90)},
		{desc: "past the nesting budget", expr: recurse(5000, 50), code: "U1001"},
		{
			desc: "past the budget with a raised call limit", expr: recurse(5000, 500),
			opts: []gnata.Option{gnata.WithStack(100_000)}, code: "U1001",
		},
		{desc: "transform calling itself", expr: `($t := |$|{"x": $ ~> $t}|; {"a": 1} ~> $t)`, code: "U1001"},
		{desc: "short composition", expr: compose(50, "$f ~> $string")},
		{desc: "composition built in a loop", expr: compose(200_000, "$f ~> $string"), code: "U1001"},
		{desc: "partial applications built in a loop", expr: compose(200_000, "$f(?)"), code: "U1001"},
		{
			desc: "recursion through a transform per level",
			expr: `($f := function($n){ $n = 0 ? 0 : ({"a": 1} ~> |$|{"b": $f($n - 1)}|).b + 1 }; $f(90))`,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr, tC.opts...)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			_, err = e.Eval(context.Background(), nil)
			if tC.code == "" {
				if err != nil {
					t.Fatalf("eval: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tC.code) {
				t.Fatalf("want %s, got %v", tC.code, err)
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

// A $lookup over arrays that share nested arrays visits 2^40 items here, so
// the walk itself must stop at the deadline.
func TestWithTimeout_LookupNestedArrays(t *testing.T) {
	e, err := gnata.Compile(
		`$lookup($reduce([1..40], function($acc, $i){[[$acc],[$acc]]}, [{"b":1}]), "a")`,
		gnata.WithTimeout(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = e.Eval(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "D1012") {
		t.Fatalf("expected D1012, got %v", err)
	}
}

// TestWithTimeout_NestedTupleBlocks checks the deadline holds while blocks
// a % reaches into copy their tuples' bindings, which nested blocks with
// leading expressions repeat at every level.
func TestWithTimeout_NestedTupleBlocks(t *testing.T) {
	var indexes strings.Builder
	for k := range 100 {
		fmt.Fprintf(&indexes, ".$#$i%d", k)
	}
	expr := `$count(a.(` + strings.Repeat(`1; (`, 100) + `items` + indexes.String() + `.v` + strings.Repeat(`)`, 100) + `).%)`
	e, err := gnata.Compile(expr, gnata.WithTimeout(100*time.Millisecond))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	items := make([]any, 10_000)
	for i := range items {
		items[i] = map[string]any{"v": i}
	}
	start := time.Now()
	_, err = e.Eval(context.Background(), map[string]any{"a": map[string]any{"items": items}})
	if err == nil || !strings.Contains(err.Error(), "D1012") {
		t.Fatalf("expected D1012, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout took %v to fire", elapsed)
	}
}

// Arrays nested by $reduce share their items, so the value is small but
// $keys, $lookup, $spread, $flatten and * walk 2^30 leaves; the walk checks
// the deadline.
func TestWithTimeout_NestedArrayWalk(t *testing.T) {
	for _, fn := range []string{"$keys($v)", `$lookup($v, "k")`, "$spread($v)", "$flatten($v)", `{"x": $v}.*`} {
		t.Run(fn, func(t *testing.T) {
			expr := "($v := $reduce([1..30], function($a, $x){[$a, $a]}, [{\"k\": 1}]); " + fn + ")"
			e, err := gnata.Compile(expr, gnata.WithTimeout(50*time.Millisecond))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			start := time.Now()
			_, err = e.Eval(context.Background(), nil)
			if err == nil || !strings.Contains(err.Error(), "D1012") {
				t.Fatalf("expected D1012, got %v", err)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("walk ran %v past the deadline", elapsed)
			}
		})
	}
}

// Each $g doubles the paths through $d, which shares its arrays, so the
// lookups and the custom function's argument walk 2^34 paths unless they
// memoize. Over [[1]] they find nothing, so memoized they finish at once;
// over [[{"x":1}]] the lookup finds 2^34 values, and the argument walk gives
// each of the 2^34 objects a map of its own, so both must stop at the
// deadline.
func TestWithTimeout_SharedArrayDAG(t *testing.T) {
	customEnv := gnata.NewCustomEnvironment(map[string]gnata.CustomFunc{
		"f": func([]any, any) (any, error) { return "called", nil },
	})
	testCases := []struct {
		leaf    string
		expr    string
		timeout time.Duration
		want    any
		wantErr string
	}{
		{leaf: `[[1]]`, expr: `$d.x`, timeout: 500 * time.Millisecond},
		{leaf: `[[1]]`, expr: `$exists($d.x)`, timeout: 500 * time.Millisecond, want: false},
		{leaf: `[[1]]`, expr: `$f($d)`, timeout: 500 * time.Millisecond, want: "called"},
		{leaf: `[[1]]`, expr: `$boolean($d)`, timeout: 500 * time.Millisecond, want: true},
		{leaf: `[[{"x":1}]]`, expr: `$d.x`, timeout: 50 * time.Millisecond, wantErr: "D1012"},
		{leaf: `[[{"x":1}]]`, expr: `$exists($d.x)`, timeout: 50 * time.Millisecond, wantErr: "D1012"},
		{leaf: `[[{"x":1}]]`, expr: `$f($d)`, timeout: 50 * time.Millisecond, wantErr: "D1012"},
	}
	for _, tC := range testCases {
		t.Run(tC.expr+" over "+tC.leaf, func(t *testing.T) {
			expr := "($d := " + tC.leaf + "; $g := function($x){$map([1,2], function(){$x})}; " +
				strings.Repeat("$d := $g($d); ", 34) + tC.expr + ")"
			e, err := gnata.Compile(expr, gnata.WithTimeout(tC.timeout))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, err := evalWithin(t, 5*time.Second, func() (any, error) {
				return e.EvalWithCustomEnvironmentAndVars(context.Background(), nil, customEnv, nil)
			})
			switch {
			case tC.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tC.wantErr) {
					t.Fatalf("expected %s, got %v", tC.wantErr, err)
				}
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			case got != tC.want:
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}

// $a40 and $b40 are two expression-built arrays, each sharing its arrays
// along 2^40 paths and equal to the other, so comparing them walks every
// path and must stop at the deadline.
func TestWithTimeout_EqualSharedArrayDAGs(t *testing.T) {
	var dags strings.Builder
	for _, name := range []string{"$a", "$b"} {
		dags.WriteString(name + "0 := [[false]]; ")
		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&dags, "%s%d := [[%s%d], [%s%d]]; ", name, i, name, i-1, name, i-1)
		}
	}
	for _, compare := range []string{`$a40 = $b40`, `$a40 != $b40`, `$a40 in [[$b40]]`, `$distinct([$a40, $b40])`} {
		t.Run(compare, func(t *testing.T) {
			e, err := gnata.Compile("("+dags.String()+compare+")", gnata.WithTimeout(50*time.Millisecond))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			_, err = evalWithin(t, 5*time.Second, func() (any, error) { return e.Eval(context.Background(), nil) })
			if err == nil || !strings.Contains(err.Error(), "D1012") {
				t.Fatalf("expected D1012, got %v", err)
			}
		})
	}
}

// $distinct over many small objects makes a quadratic run of comparisons,
// each too short to poll the deadline alone.
func TestWithTimeout_DistinctManySmallObjects(t *testing.T) {
	e, err := gnata.Compile(`$count($distinct([1..20000].{"a":$}))`, gnata.WithTimeout(200*time.Millisecond))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = evalWithin(t, 3*time.Second, func() (any, error) { return e.Eval(context.Background(), nil) })
	if err == nil || !strings.Contains(err.Error(), "D1012") {
		t.Fatalf("expected D1012, got %v", err)
	}
}

// A lookup over $d, which shares its arrays along 2^40 paths, finds a value
// on each path; with no deadline WithSequence must stop it as the values
// arrive rather than once all have been found.
func TestWithSequence_SharedArrayDAG(t *testing.T) {
	customEnv := gnata.NewCustomEnvironment(map[string]gnata.CustomFunc{
		"f": func([]any, any) (any, error) { return "called", nil },
	})
	for _, lookup := range []string{`$d.x.y`, `$f($d.x)`, `$d.x[y=1]`} {
		t.Run(lookup, func(t *testing.T) {
			expr := `($d := [[{"x":{"y":1}}]]; $g := function($x){$map([1,2], function(){$x})}; ` +
				strings.Repeat("$d := $g($d); ", 40) + lookup + ")"
			e, err := gnata.Compile(expr, gnata.WithSequence(1000))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			_, err = evalWithin(t, 5*time.Second, func() (any, error) {
				return e.EvalWithCustomEnvironmentAndVars(context.Background(), nil, customEnv, nil)
			})
			if err == nil || !strings.Contains(err.Error(), "D2015") {
				t.Fatalf("expected D2015, got %v", err)
			}
		})
	}
}

// A Go value whose arrays each hold the next one twice has 2^40 paths;
// NormalizeValue copies each array once and shares the copies as the value
// shares the arrays.
func TestNormalizeValue_SharedArrays(t *testing.T) {
	data := any([]any{1.0})
	for range 40 {
		data = []any{data, data}
	}
	got, err := evalWithin(t, 5*time.Second, func() (any, error) { return gnata.NormalizeValue(data), nil })
	if err != nil {
		t.Fatal(err)
	}
	for range 40 {
		arr, ok := got.([]any)
		if !ok || len(arr) != 2 {
			t.Fatalf("got %#v, want a pair", got)
		}
		got = arr[0]
	}
	if want := []any{1.0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got leaf %#v, want %#v", got, want)
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

// twentyItems returns 20 objects {"b": i}, twice the guardrail used by the
// WithSequence tests.
func twentyItems() []any {
	items := make([]any, 20)
	for i := range items {
		items[i] = map[string]any{"b": float64(i)}
	}
	return items
}

// Once * appends an array value its result is a plain array, so jsonata-js
// counts the values after it only as part of a later appended array.
const (
	arrayThenValues     = `{"v":{"a":[1],"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9,"j":10,"k":11,"l":12}}`
	valuesThenArray     = `{"v":{"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9,"j":10,"k":11,"l":12,"a":[1]}}`
	rootArrayThenValues = `[[1],2,3,4,5,6,7,8,9,10,11,12]`
)

func mustDecodeJSON(t *testing.T, data string) any {
	t.Helper()
	v, err := gnata.DecodeJSON(json.RawMessage(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func TestWithSequence(t *testing.T) {
	wideObject := map[string]any{}
	for i := range 20 {
		wideObject[fmt.Sprintf("k%d", i)] = i
	}
	items := twentyItems()
	oneItemArrays := make([]any, 12)
	for i := range oneItemArrays {
		oneItemArrays[i] = []any{float64(i)}
	}
	data := map[string]any{
		"a":  items,
		"x":  map[string]any{"a": items},
		"m":  []any{map[string]any{"b": items}, map[string]any{"c": 1.0}},
		"oo": []any{[]any{map[string]any{"a": items}}},
		"qq": []any{[]any{map[string]any{"a": items}, map[string]any{"a": items}}},
		"w":  oneItemArrays,
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
		{desc: "lookup exceeds sequence guardrail", expr: `$lookup([{"a":[1,2,3,4,5,6]},{"a":[7,8,9,10,11]}], "a")`},
		{desc: "wildcard exceeds sequence guardrail", expr: "*", data: wideObject},
		{desc: "descendant exceeds sequence guardrail", expr: "**", data: wideObject},
		{desc: "field step over a sequence", expr: "a.b", data: data},
		{desc: "field step through a stored array", expr: "x.a.b", data: data},
		{desc: "field step over a root array", expr: "b", data: items},
		{
			desc: "index of a stored array beside an empty array", expr: "me.b[0]",
			data: map[string]any{"me": []any{map[string]any{"b": []any{numbersArray()}}, map[string]any{"b": []any{}}}},
		},
		{desc: "variable step", expr: "a.$", data: data},
		{desc: "block step", expr: "a.(b)", data: data},
		{desc: "function step", expr: "a.$string(b)", data: data},
		{desc: "tuple expansion", expr: "a#$i.b", data: data},
		{desc: "stages after a binding", expr: "a#$i[true][true]", data: data},
		{desc: "stream of a first-step binding", expr: "a#$i[$i<3]", data: data},
		{desc: "stream of a binding after a filter", expr: "a[b<3]#$i", data: data},
		{desc: "join stream", expr: "a@$v[$v.b<3]", data: data},
		{desc: "filter", expr: "x.a[true]", data: data},
		{desc: "sort", expr: "x.a^(b)", data: data},
		{desc: "binding with nothing after it", expr: "a#$i", data: data},
		{desc: "array constructor", expr: "[a]", data: data},
		{desc: "growing array constructor", expr: "$reduce([1..5], function($acc, $x){[$acc,$acc]}, [1])"},
		{desc: "group-by", expr: `a{"k": b}`, data: data},
		{desc: "$keys", expr: "$keys($)", data: wideObject},
		{desc: "$spread", expr: "$spread($)", data: wideObject},
		{desc: "$lookup", expr: `$lookup(a, "b")`, data: data},
		{desc: "$match", expr: `$match("aaaaaaaaaaaa", /a/)`},
		{desc: "group variables", expr: `x.a@$e{"k": $e}`, data: data},
		{desc: "$eval of an array context", expr: `$eval("b", a)`, data: data},
		// A lone context's value passes through a last step only when it is a
		// stored array; a lookup over an array context builds a sequence.
		{desc: "last field step over a nested array context", expr: "$count(oo.a)", data: data},
		{desc: "subscripted field over a nested array context", expr: "$count(oo.a[0])", data: data},
		{desc: "predicated step over many contexts", expr: "$count(w.(a[0]))", data: data},
		{desc: "predicated field over an array of contexts", expr: "$count(qq.(a[true]))", data: data},
		{desc: "wildcard appending an array to its values", expr: "$count(v.*)", data: mustDecodeJSON(t, valuesThenArray)},
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
// every intermediate sequence stays under it.
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
}

// The gjson fast paths would otherwise walk the array outside the guardrail.
func TestWithSequence_EvalBytes(t *testing.T) {
	e, err := gnata.Compile("a.b", gnata.WithSequence(10))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	data, err := json.Marshal(map[string]any{"a": twentyItems()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := e.EvalBytes(context.Background(), data); err == nil || !strings.Contains(err.Error(), "D2015") {
		t.Fatalf("expected D2015, got %v", err)
	}
}

// The gjson fast paths keep their single lookup, which never crosses an
// array; a path crossing one falls back to the evaluator, which bounds it.
func TestWithSequence_FastPaths(t *testing.T) {
	keys := make([]string, 20)
	for i := range keys {
		keys[i] = fmt.Sprintf(`"k%d":%d`, i, i)
	}
	data := []byte(`{"x":{"a":` + twentyItemsJSON() + `,"n":"s"},"a":` + twentyItemsJSON() + `,"o":{` + strings.Join(keys, ",") + `}}`)
	testCases := []struct {
		desc string
		expr string
		want any
		code string
	}{
		{desc: "path", expr: "x.n", want: "s"},
		{desc: "comparison", expr: `x.n = "s"`, want: true},
		{desc: "aggregate of a stored array", expr: "$count(x.a)", want: 20.0},
		{desc: "boolean", expr: "x.n and true", want: true},
		{desc: "path crossing an array", expr: "a.b", code: "D2015"},
		{desc: "comparison crossing an array", expr: "a.b = 1", code: "D2015"},
		{desc: "aggregate crossing an array", expr: "$count(a.b)", code: "D2015"},
		{desc: "existence crossing an array", expr: "$exists(a.b)", code: "D2015"},
		{desc: "boolean crossing an array", expr: "a.b and true", code: "D2015"},
		{desc: "binding", expr: "$count(x.a#$i)", code: "D2015"},
		{desc: "keys, whose result is bounded", expr: "$keys(o)", code: "D2015"},
		{desc: "boolean of keys", expr: "x.n and $keys(o)", code: "D2015"},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr, gnata.WithSequence(10))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, err := e.EvalBytes(context.Background(), data)
			if tC.code != "" {
				if err == nil || !strings.Contains(err.Error(), tC.code) {
					t.Fatalf("expected %s, got %v, %v", tC.code, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("eval bytes: %v", err)
			}
			if !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
	e, err := gnata.Compile("x.n", gnata.WithSequence(10))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if paths := e.RequiredPaths(); len(paths) != 1 {
		t.Fatalf("RequiredPaths() = %v, want the fast path's one path", paths)
	}
}

func numbersArray() []any {
	numbers := make([]any, 20)
	for i := range numbers {
		numbers[i] = float64(i)
	}
	return numbers
}

func twentyItemsJSON() string {
	b, err := json.Marshal(twentyItems())
	if err != nil {
		panic(err)
	}
	return string(b)
}

// A last step against a single context returns its value without building a
// sequence, so a stored array longer than the guardrail passes through.
func TestWithSequence_Allowed(t *testing.T) {
	items := twentyItems()
	data := map[string]any{
		"a":  items,
		"x":  map[string]any{"a": items},
		"o":  []any{map[string]any{"a": items}},
		"m":  []any{map[string]any{"b": items}, map[string]any{"c": 1.0}},
		"n":  []any{map[string]any{"b": items}, []any{map[string]any{"c": 1.0}}},
		"p":  []any{[]any{map[string]any{"b": items}}, map[string]any{"c": 1.0}},
		"oo": []any{[]any{map[string]any{"a": items}}},
		"mm": []any{map[string]any{"b": []any{numbersArray()}}},
		// One context yields a stored array, its sibling an empty array:
		// the step's sequence holds one item, the array.
		"me": []any{map[string]any{"b": []any{numbersArray()}}, map[string]any{"b": []any{}}},
	}
	rootStoredThenEmpty := []any{
		map[string]any{"b": map[string]any{"c": []any{numbersArray()}}},
		map[string]any{"b": map[string]any{"c": []any{}}},
	}
	// group-by iterates jsonata-js's wrapper of a root array, so its values
	// see the array's items as contexts.
	rootArray := []any{map[string]any{"a": items}, map[string]any{"c": 1.0}}
	a1 := map[string]any{"a": 1.0}
	testCases := []struct {
		desc string
		expr string
		data any // the shared data when nil
		want any
	}{
		{desc: "range under the guardrail", expr: "1..5", want: []any{1.0, 2.0, 3.0, 4.0, 5.0}},
		{desc: "stored array as a field", expr: "$count(a)", want: 20.0},
		{desc: "stored array as a last step", expr: "$count(x.a)", want: 20.0},
		{desc: "last step against a one-item array", expr: "$count(o.a)", want: 20.0},
		{desc: "last field step with one matching context", expr: "$count(m.b)", want: 20.0},
		{desc: "last field step beside a nested array without the field", expr: "$count(n.b)", want: 20.0},
		{desc: "last block step with one context yielding", expr: "$count(m.(b))", want: 20.0},
		{desc: "last function step with one context yielding", expr: `$count(m.$lookup($, "b"))`, want: 20.0},
		{desc: "last block step with an array context", expr: "$count(p.(b))", want: 20.0},
		{desc: "group-by over a root array", expr: `{"k": $count(a)}`, data: rootArray, want: map[string]any{"k": 20.0}},
		{desc: "$eval of the current context in a group", expr: `{"k": $count($eval("a"))}`, data: rootArray, want: map[string]any{"k": 20.0}},
		{desc: "wildcard step over a root array", expr: "*.a", data: []any{a1, map[string]any{"a": 2.0}}, want: []any{1.0, 2.0}},
		{desc: "lone wildcard over a nested root array", expr: `{"k": *}`, data: []any{[]any{a1}}, want: map[string]any{"k": []any{a1}}},
		{desc: "wildcard values after an array", expr: "$count(v.*)", data: mustDecodeJSON(t, arrayThenValues), want: 12.0},
		{desc: "wildcard over a root array after an array", expr: "$count(*)", data: mustDecodeJSON(t, rootArrayThenValues), want: 12.0},
		{desc: "subscript of a stored array", expr: "x.a[0].b", want: 0.0},
		{desc: "subscript inside a block over a nested array", expr: "$count(oo.(a[0]))", want: 1.0},
		{desc: "stored array of arrays as a last step", expr: "$exists(mm.b)", want: true},
		{desc: "filter under the guardrail", expr: "a[b<5].b", want: []any{0.0, 1.0, 2.0, 3.0, 4.0}},
		{desc: "field step beside an empty array", expr: "me.b", want: numbersArray()},
		{desc: "field step after $ beside an empty array", expr: "me.$.b", want: numbersArray()},
		{desc: "quoted field step beside an empty array", expr: "me.`b`", want: numbersArray()},
		{desc: "block step beside an empty array", expr: "me.(b)", want: numbersArray()},
		{desc: "predicated step beside an empty array", expr: "me.b[true]", want: numbersArray()},
		{desc: "count of a step beside an empty array", expr: "$count(me.b)", want: 20.0},
		{desc: "sort of a step beside an empty array", expr: "me.b^($)", want: numbersArray()},
		{desc: "sort's lone array item", expr: "me.b^($)[0]", want: numbersArray()},
		{desc: "group of a step beside an empty array", expr: `me.b{"k":$}`, want: map[string]any{"k": numbersArray()}},
		{desc: "group of a block step beside an empty array", expr: `me.(b){"k":$count($)}`, want: map[string]any{"k": 20.0}},
		{desc: "root array step beside an empty array", expr: "b.c", data: rootStoredThenEmpty, want: numbersArray()},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr, gnata.WithSequence(10))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			input := tC.data
			if input == nil {
				input = data
			}
			got, err := e.Eval(context.Background(), input)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}
