package gnata_test

import (
	"context"
	"encoding/json"
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
		{desc: "wildcard exceeds sequence guardrail", expr: "*", data: wideObject},
		{desc: "descendant exceeds sequence guardrail", expr: "**", data: wideObject},
		{desc: "field step over a sequence", expr: "a.b", data: data},
		{desc: "field step through a stored array", expr: "x.a.b", data: data},
		{desc: "field step over a root array", expr: "b", data: items},
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
		{desc: "filter under the guardrail", expr: "a[b<5].b", want: []any{0.0, 1.0, 2.0, 3.0, 4.0}},
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
