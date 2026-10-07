package gnata_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/recolabs/gnata"
)

// undefined is how render and the want columns spell a JSONata undefined
// result, keeping it distinct from JSON null.
const undefined = "undefined"

// pairsJSON holds constructed-array inputs: a.[b, c] builds one array per
// item of a, and o.[b, c] builds one from a single context.
const pairsJSON = `{"a":[{"b":1,"c":2},{"b":3,"c":4}],"o":{"b":5,"c":6}}`

// stageJSON gives each item of w arrays for a call in a later path step.
const stageJSON = `{"w":[{"a":[[1,2],[3]]},{"a":[[1,9]]}]}`

// boundJSON holds one item with a one-item nested array c and a one-item
// array m, for [] written before or after #$i and @$x.
const boundJSON = `{"w1":[{"c":[[1,2]],"m":[3]}]}`

// exprCase evaluates expr against data, a JSON document ("" for no input).
// want is the canonical JSON of the result (see render); code, when set,
// is the error code evaluation or compilation must fail with instead.
type exprCase struct {
	expr string
	data string
	want string
	code string
}

func (c exprCase) run(t *testing.T) {
	t.Helper()
	c.runDecoded(t, decodeOrdered)
}

// runDecoded runs the case with data decoded by decode.
func (c exprCase) runDecoded(t *testing.T, decode func(data string) (any, error)) {
	t.Helper()
	got, err := evalWith(c.expr, c.data, decode)
	c.check(t, "Eval", got, err)
}

func runExprCases(t *testing.T, cases []exprCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.expr, c.run)
	}
}

// runExprCasesAllAPIs runs each case through Eval, on data decoded by
// DecodeJSON and by encoding/json, and through the APIs that take raw JSON
// and may answer from a fast path: EvalBytes, EvalMap (for object data) and
// StreamEvaluator.EvalOne.
func runExprCasesAllAPIs(t *testing.T, cases []exprCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			c.runDecoded(t, decodeOrdered)
			c.runDecoded(t, decodeGoMaps)
			c.runRaw(t)
		})
	}
}

// runRaw runs the case through the APIs that take raw JSON.
func (c exprCase) runRaw(t *testing.T) {
	t.Helper()
	e, err := gnata.Compile(c.expr)
	if err != nil {
		c.check(t, "Compile", nil, err)
		return
	}
	ctx := context.Background()
	data := c.data
	if data == "" {
		data = "null"
	}
	got, err := e.EvalBytes(ctx, json.RawMessage(data))
	c.check(t, "EvalBytes", got, err)
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(data), &fields) == nil && fields != nil {
		got, err = e.EvalMap(ctx, fields)
		c.check(t, "EvalMap", got, err)
	}
	se := gnata.NewStreamEvaluator([]*gnata.Expression{e})
	got, err = se.EvalOne(ctx, json.RawMessage(data), "", 0)
	c.check(t, "StreamEvaluator.EvalOne", got, err)
}

func (c exprCase) check(t *testing.T, api string, got any, err error) {
	t.Helper()
	if c.code != "" {
		if err == nil || !strings.Contains(err.Error(), c.code) {
			t.Fatalf("%s %s: want error %s, got %v (err %v)", api, c.expr, c.code, render(t, got), err)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s %s: unexpected error: %v", api, c.expr, err)
	}
	if rendered := render(t, got); rendered != c.want {
		t.Fatalf("%s %s:\n got: %s\nwant: %s", api, c.expr, rendered, c.want)
	}
}

func evalWith(expr, data string, decode func(data string) (any, error)) (any, error) {
	e, err := gnata.Compile(expr)
	if err != nil {
		return nil, err
	}
	var input any
	if data != "" {
		if input, err = decode(data); err != nil {
			return nil, err
		}
	}
	return e.Eval(context.Background(), input)
}

// decodeOrdered decodes objects as *OrderedMap, as gnata.DecodeJSON does.
func decodeOrdered(data string) (any, error) {
	return gnata.DecodeJSON(json.RawMessage(data))
}

// decodeGoMaps decodes objects as map[string]any, as encoding/json does.
func decodeGoMaps(data string) (any, error) {
	var v any
	err := json.Unmarshal([]byte(data), &v)
	return v, err
}

// render serializes an evaluation result as compact JSON with sorted object
// keys and unescaped HTML characters, or undefined for a nil result.
func render(t *testing.T, v any) string {
	t.Helper()
	if v == nil {
		return undefined
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(gnata.NormalizeValue(v)); err != nil {
		t.Fatalf("render %v: %v", v, err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

var arrayAndObjectBuiltinCases = []exprCase{
	{expr: `$count([1,2] ~> $append(3))`, want: `3`},
	{expr: `$sort([3,1,2], function($a,$b){$a>$b})`, want: `[1,2,3]`},
	{expr: `[3,1,2] ~> $sort(function($a,$b){$a>$b})`, want: `[1,2,3]`},
	{expr: `$type($sort(function($a,$b){$a>$b})[0])`, want: `"function"`},
	{expr: `$count($sort(function($a,$b){$a>$b}))`, want: `1`},
	{expr: `$sort()`, code: "T0410"},
	{expr: `$distinct(a.b)`, data: `{"a":[{"b":[1,1,2]},{"b":2}]}`, want: `[1,2]`},
	{expr: `$distinct([[1],[1],[2],null,null])`, want: `[[1],[2],null]`},
	{expr: `$distinct([[1,2],[1,2]])[0]`, want: `[1,2]`},
	{expr: `$distinct(o.[b,b])[0]`, data: `{"o":{"b":5}}`, want: `5`},
	{expr: `$count($distinct(o.[b,b]))`, data: `{"o":{"b":5}}`, want: `1`},
	{expr: `o.[b,b] ~> $distinct()`, data: `{"o":{"b":5}}`, want: `[5]`},
	{expr: `$flatten([[1,[2,[3]]]], 1)`, want: `[1,[2,[3]]]`},
	{expr: `$zip([1,2],[3])`, want: `[[1,3]]`},
	{expr: `$zip()`, code: "T0410"},
	{expr: `$zip([1,2], undefined)`, want: `[]`},
	{expr: `$reverse(a.b)`, data: `{"a":[{"b":1},{"b":2}]}`, want: `[2,1]`},
	{expr: `$shuffle([1])`, want: `[1]`},
	{expr: `$map([1,2], function($v,$i,$a){$v + $i + $count($a)})`, want: `[3,5]`},
	{expr: `$filter([1,2,3], function($v){$v>1})`, want: `[2,3]`},
	{expr: `[1,2,3] ~> $filter(function($v){$v>1})`, want: `[2,3]`},
	{expr: `$filter([1,2,3], function($v){$v>2})`, want: `3`},
	{expr: `$filter([[1,2],[3]], function($v){$v[0]=1})[0]`, want: `[1,2]`},
	{expr: `$filter([[1,2],[3]], function($v){$v[0]=1})[1]`, want: undefined},
	{expr: `$filter([[1,2],[3]], function($v){$v[0]=1})[-1]`, want: `[1,2]`},
	{expr: `$filter([[1,2],[3]], function($v){$v[0]=1})[$=1]`, want: undefined},
	{expr: `$count($filter([[1,2],[3]], function($v){$v[0]=1}))`, want: `2`},
	{expr: `($x := $filter([[1,2],[3]], function($v){$v[0]=1}); $x[0])`, want: `1`},
	{expr: `$filter([[[1,2]]], function($v){true})[0]`, want: `[[1,2]]`},
	{expr: `$filter([[]], function($v){true})[0]`, want: `[]`},
	{expr: `$filter(["a",""], $boolean)`, want: `"a"`},
	{expr: `$filter([[1,2],[3]], function($v){$v[0]=1}){$string($): $count($)}`, want: `{"[1,2]":2}`},
	{expr: `$filter([["x","y"]], function($v){true}){$: 1}`, code: "T1003"},
	{expr: `$each(q, function($v){$v})[0]`, data: `{"q":{"k":["x","y"]}}`, want: `["x","y"]`},
	{expr: `$each(q, function($v){$v}){$: 1}`, data: `{"q":{"k":["x","y"]}}`, code: "T1003"},
	{expr: `q.$each(function($v){$v})[0]`, data: `{"q":{"k":["x","y"]}}`, want: `"x"`},
	{expr: `a.($each(function($v){$v})[0])`, data: `{"a":[{"k":["x"]},{"k":["y","z"]}]}`, want: `["x","y","z"]`},
	{expr: `$map([[[1,2],[3]]], $filter(?, function($v){$v[0]=1}))`, want: `[[1,2]]`},
	{expr: `$map([[[1,2]],[[3]]], $filter(?, function($v){true}))`, want: `[[[1,2]],[[3]]]`},
	{expr: `$each({"a":[[1,2]]}, $filter(?, function($v){true}))`, want: `[[1,2]]`},
	{expr: `$map([{"a":1},{"b":1}], $keys)`, want: `[["a"],["b"]]`},
	{expr: `$map([[1,1],[2,2]], $distinct)`, want: `[[1],[2]]`},
	{expr: `$map([{"a":1},{"b":1}], $keys)[0]`, want: `"a"`},
	{expr: `$map($map([{"a":1},{"b":1}], $keys), function($v){$v})[0]`, want: `"a"`},
	{expr: `($m := $map([{"a":1},{"b":1}], $keys); $m[0])`, want: `"a"`},
	{expr: `x.$distinct(a.b)[0]`, data: `{"x":{"a":[{"b":[[1,2]]},{"b":[[1,2]]}]}}`, want: `1`},
	{expr: `$distinct(a.b)#$i[0]`, data: `{"a":[{"b":[[1,2]]},{"b":[[1,2]]}]}`, want: `1`},
	{expr: `$distinct(a.b)[0]#$i`, data: `{"a":[{"b":[[1,2]]},{"b":[[1,2]]}]}`, want: `1`},
	{expr: `w[0].$filter(a, function($v){$v[0]=1})[0][]`, data: stageJSON, want: `[1,2]`},
	{expr: `w[0].$filter(a, function($v){$v[0]=1})[1][]`, data: stageJSON, want: undefined},
	{expr: `w.$filter(a, function($v){$v[0]=1})[0][]`, data: stageJSON, want: `[1,2,1,9]`},
	{expr: `w.$filter(a, function($v){$v[0]=1})[0]`, data: stageJSON, want: `[1,1]`},
	{expr: `w[0].$filter(a, function($v){$v[0]=1})[]`, data: stageJSON, want: `[[1,2]]`},
	{expr: `$count(w[0].$filter(a, function($v){$v[0]=1})[])`, data: stageJSON, want: `1`},
	{expr: `w[0].$filter(a, function($v){$v[0]=1})[].$string()`, data: stageJSON, want: `["[1,2]"]`},
	{expr: `w[[0]].$filter(a, function($v){$v[0]=1})[]`, data: stageJSON, want: `[[1,2]]`},
	{expr: `w[[0]].$each(function($v){$v})[]`, data: stageJSON, want: `[[[1,2],[3]]]`},
	{expr: `w[[0]].(a)[]`, data: stageJSON, want: `[[1,2],[3]]`},
	{expr: `w[[0]].$filter(a, function($v){$v[0]=1}).$string()[]`, data: stageJSON, want: `["1","2"]`},
	{expr: `w[[0]].$filter(a, function($v){$v[0]=1}).$sum($)[]`, data: stageJSON, want: `[1,2]`},
	{expr: `w[[0]].$each(function($v){$v}).$string()[]`, data: stageJSON, want: `["[1,2]","[3]"]`},
	{expr: `w.$filter(a, function($v){$v[0]=1})`, data: stageJSON, want: `[1,2,1,9]`},
	{expr: `w#$j.$filter(a, function($v){$v[0]=1})`, data: stageJSON, want: `[1,2,1,9]`},
	{expr: `w[[0]].(a)[]#$i`, data: stageJSON, want: `[[1,2],[3]]`},
	{expr: `w[0].$filter(a, function($v){$v[0]=1})[]@$z`, data: stageJSON, want: `[{"a":[[1,2],[3]]}]`},
	{expr: `w.$filter(a, function($v){$v[0]=1})[]#$i`, data: stageJSON, want: `[[1,2],[1,9]]`},
	{expr: `w1.(c)[]#$i`, data: `{"w1":[{"c":[[1,2]]}]}`, want: `[[1,2]]`},
	{expr: `w1#$j.c[]`, data: `{"w1":[{"c":[[1,2]]}]}`, want: `[[1,2]]`},
	{expr: `w1.c#$i[]`, data: `{"w1":[{"c":[[1,2]]}]}`, want: `[1,2]`},
	{expr: `w1.(c)#$i[]`, data: `{"w1":[{"c":[[1,2]]}]}`, want: `[1,2]`},
	{expr: `w1.c#$i[0][]`, data: `{"w1":[{"c":[[1,2]]}]}`, want: `[1,2]`},
	{expr: `w1.c#$i[$i=0][]`, data: `{"w1":[{"c":[[1,2]]}]}`, want: `[1,2]`},
	{expr: `$count(w1.(c)#$i[])`, data: `{"w1":[{"c":[[1,2]]}]}`, want: `2`},
	{expr: `w1#$j.(c)#$i[]`, data: `{"w1":[{"c":[[1,2]]}]}`, want: `[1,2]`},
	{expr: `z.($)#$i[]`, data: `{"z":[[[1,2]]]}`, want: `[1,2]`},
	{expr: `w.$filter(a, function($v){$v[0]=1})#$i[]`, data: stageJSON, want: `[1,2,1,9]`},
	{expr: `w.$filter(a, function($v){$v[0]=1})[]#$i[]`, data: stageJSON, want: `[[1,2],[1,9]]`},
	{expr: `w1.c[]#$i[]`, data: boundJSON, want: `[[1,2]]`},
	{expr: `w1.m[]#$i[]`, data: boundJSON, want: `[3]`},
	{expr: `u.c[]#$i[]`, data: `{"u":[{"c":5}]}`, want: `[5]`},
	{expr: `w1.c#$i@$x[]`, data: boundJSON, want: `[{"c":[[1,2]],"m":[3]}]`},
	{expr: `w1.c@$x#$i[]`, data: boundJSON, want: `{"c":[[1,2]],"m":[3]}`},
	{expr: `w1.m@$x[]#$i[]`, data: boundJSON, want: `[{"c":[[1,2]],"m":[3]}]`},
	{expr: `($f := function(){$filter([[1,2],[3]], function($v){$v[0]=1})}; $f()[0])`, want: `[1,2]`},
	{expr: `$filter(o.[b,c], function($v){$v>5})`, data: pairsJSON, want: `6`},
	{expr: `$filter(a.[b,c], function($v){$v[0]=3})[0]`, data: pairsJSON, want: `[3,4]`},
	{expr: `$sum(o.[b,c])`, data: pairsJSON, want: `11`},
	{expr: `$type(o.[b,c])`, data: pairsJSON, want: `"array"`},
	{expr: `$map(a.[b,c], $sum)`, data: pairsJSON, want: `[3,7]`},
	{expr: `$append(o.[b,c], nothing).$string()`, data: pairsJSON, want: `"[5,6]"`},
	{expr: `$distinct(o.[b]).$string()`, data: pairsJSON, want: `"[5]"`},
	{expr: `$sort(o.[b]).$string()`, data: pairsJSON, want: `"[5]"`},
	{expr: `$reverse(o.[b]).$string()`, data: pairsJSON, want: `"[5]"`},
	{expr: `$shuffle(o.[b]).$string()`, data: pairsJSON, want: `"[5]"`},
	{expr: `$sort(o.[b])[]`, data: pairsJSON, want: `[5]`},
	{expr: `$count(**)`, data: `{"a":{"b":1}}`, want: `3`},
	{expr: `($f := function($x)<a>{$x}; $f(o.[b,c]).$string())`, data: pairsJSON, want: `"[5,6]"`},
	{expr: `($f := function($x)<a<n>:n>{$x.$sum($)}; $f(o.[b,c]))`, data: pairsJSON, want: `11`},
	{expr: `$single()`, code: "T0410"},
	{expr: `$single(5)`, want: `5`},
	{expr: `$reduce([], function($a,$b){$a+$b})`, want: undefined},
	{expr: `$reduce([], function($a,$b){$a+$b}, 7)`, want: `7`},
	{expr: `[1,2,3] ~> $reduce(function($a,$b){$a+$b})`, want: `6`},
	{expr: `$sift({"a":1,"b":2}, function($v,$k,$o){$v>1 and $k="b" and $exists($o.a)})`, want: `{"b":2}`},
	{expr: `{"a":1,"b":2} ~> $sift(function($v){$v>1})`, want: `{"b":2}`},
	{expr: `$merge([])`, want: `{}`},
	{expr: `$merge()`, code: "T0410"},
	{expr: `$lookup(undefined, "a")`, want: undefined},
	{expr: `$lookup([{"a":1},{"b":2}], "a")`, want: `1`},
	{expr: `$lookup([{"b":1}], "a")`, want: undefined},
	{expr: `$lookup([{"a":1},{"a":2}], "a")`, want: `[1,2]`},
	{expr: `$values({"a":1,"b":[2]})`, want: `[1,[2]]`},
	{expr: `$values([{"a":1},5,{"b":2}])`, want: `[1,2]`},
	{expr: `$values([5])`, want: undefined},
	{expr: `$values(5)`, want: undefined},
	{expr: `$values()`, want: undefined},
	{expr: `$keys(5)`, want: undefined},
	{expr: `$keys()`, want: undefined},
	{expr: `$spread([{"a":1,"b":2}, 3])`, want: `[{"a":1},{"b":2},3]`},
	{expr: `$spread([])`, want: undefined},
	{expr: `$map([[1,2]], function($v){$v})[0]`, want: `[1,2]`},
	{expr: `$map([[1,2]], function($v){$v})[]`, want: `[[1,2]]`},
	{expr: `$map([{"a":1}], $keys)`, want: `["a"]`},
	{expr: `$map(a, $keys)[]`, data: pairsJSON, want: `[["b","c"],["b","c"]]`},
	{expr: `$map(a, function($v){$v.b[]})`, data: pairsJSON, want: `[[1],[3]]`},
	{expr: `$keys({"a":1})[]`, want: `["a"]`},
	{expr: `$lookup([{"b":1}], "b")[]`, want: `[1]`},
	{expr: `$lookup({"a":1}, "a")[]`, want: `1`},
	{expr: `$sum([5])[]`, want: `5`},
	{expr: `$string(1)[]`, want: `"1"`},
	{expr: `$filter([[1,2],[3]], function($v){$v[0]=1})`, want: `[1,2]`},
	{expr: `$filter([[1,2],[3]], function($v){$v[0]=1})[[0]]`, want: `[1,2]`},
	{expr: `$filter([[1,2],[3]], function($v){$v[0]=1})[true][]`, want: `[[1,2]]`},
	{expr: `$filter([[1,2],[3]], function($v){$v[0]=1})[0][]`, want: `[1,2]`},
	{expr: `$map([{"a":1},{"b":2}], $keys)[0]`, want: `"a"`},
	{expr: `$map([{"a":1}], $keys)[0]`, want: `"a"`},
	{expr: `$map([{"a":1}], $keys)[]`, want: `[["a"]]`},
	{expr: `$map([{"a":1},{"b":2}], $keys)[$ = "a"]`, want: `["a"]`},
	{expr: `($m := $map([{"a":1}], $keys); $m)`, want: `"a"`},
	{expr: `($m := $map([{"a":1}], $keys); $m[])`, want: `["a"]`},
	{expr: `($map([{"a":1}], $keys))`, want: `"a"`},
	{expr: `{"k": $map([{"a":1}], $keys)}`, want: `{"k":["a"]}`},
	{expr: `{"k": $map([{"a":1}], $keys)}.k`, want: `"a"`},
	{expr: `$each({"a":{"x":1},"b":{"y":1}}, $keys)[0]`, want: `"x"`},
	{expr: `$map([{"a":1},{"b":2}], function($o){$keys($o)})`, want: `[["a"],["b"]]`},
	{expr: `($x := 1; $map([1], function($v){$keys({"a":1})}))`, want: `"a"`},
	{expr: `$map([1], function($v){$x := $keys({"a":1})})`, want: `"a"`},
	{expr: `($f := function(){$keys({"a":1})[]}; $f())`, want: `"a"`},
	{expr: `($f := function(){$keys({"a":1})}; $f()[])`, want: `["a"]`},
	{expr: `a.$map($, $keys)`, data: pairsJSON, want: `["b","c","b","c"]`},
	{expr: `$lookup([{"b":[1,2]},{"b":3}], "b")`, want: `[1,2,3]`},
	{expr: `$lookup([{"b":[1,2]}], "b")[0]`, want: `1`},
	{expr: `$lookup([[{"b":1}],{"b":2}], "b")`, want: `[1,2]`},
	{expr: `$lookup({"a":null}, "a")`, want: `null`},
	{expr: `$lookup([{"a":null}], "a")`, want: `null`},
	{expr: `q.$lookup(x, "k")`, data: `{"q":[{"x":[{"k":[1,2]}]},{"x":[{"k":[3]}]}]}`, want: `[1,2,3]`},
	{expr: `$sort([3,1], function($a,$b){[0]})`, want: `[1,3]`},
	{expr: `$sort([3,1], function($a,$b){[]})`, want: `[1,3]`},
	{expr: `$sort([3,1], function($a,$b){{}})`, want: `[1,3]`},
	{expr: `$sort([3,1], function($a,$b){$string})`, want: `[1,3]`},
	{expr: `$sort([3,1], function($a,$b){0})`, want: `[3,1]`},
	{expr: `$sort([3,1], function($a,$b){""})`, want: `[3,1]`},
	{expr: `$distinct([5,5])`, want: `[5]`},
	{expr: `$distinct(m)`, data: `{"m":[5,5]}`, want: `[5]`},
	{expr: `$distinct(q.k)`, data: `{"q":[{"k":[5,5]}]}`, want: `[5]`},
	{expr: `$distinct(a.b)`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `($x := a.b; $distinct($x))`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `$distinct(m[$ > 0])`, data: `{"m":[5,5]}`, want: `5`},
	{expr: `$distinct(nn[0])`, data: `{"nn":[[5,5]]}`, want: `[5]`},
	{expr: `$distinct(m^($))`, data: `{"m":[5,5]}`, want: `[5]`},
	{expr: `$distinct((a.b))`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `$distinct($map([5,5], function($v){$v}))`, want: `5`},
	{expr: `$distinct(o.[b,b])`, data: `{"o":{"b":5}}`, want: `[5]`},
	{expr: `$distinct(o.([b,b]))`, data: `{"o":{"b":5}}`, want: `[5]`},
	{expr: `$distinct(n.[b,b])`, data: `{"n":[[{"b":1}]]}`, want: `[1]`},
	{expr: `$distinct(a.[b,b])`, data: `{"a":[{"b":1},{"b":3}]}`, want: `[[1,1],[3,3]]`},
	{expr: `$distinct(o.[b,b])^($)`, data: `{"o":{"b":5}}`, want: `5`},
	{expr: `($x := [5,5]; $distinct($x))`, want: `[5]`},
	{expr: `($x := m; $distinct($x))`, data: `{"m":[5,5]}`, want: `[5]`},
	{expr: `($x := a.b; $distinct($x))`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `($x := $append([5],[5]); $distinct($x))`, want: `[5]`},
	{expr: `($x := m; ($y := $x; $distinct($y)))`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `[5]`},
	{expr: `($x := a.b; ($y := $x; $distinct($y)))`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `5`},
	{expr: `($x := m; ($x := a.b; $distinct($x)))`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `5`},
	{expr: `($x := a.b; $x := m; $distinct($x))`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `[5]`},
	{expr: `($x := m; $x := a.b; $distinct($x))`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `5`},
	{expr: `($a := 1; $b := 2; $x := m; $distinct($x))`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `[5]`},
	{expr: `($a := 1; $b := 2; $x := m; $x := a.b; $distinct($x))`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `5`},
	{expr: `($x := m; (function(){$distinct($x)})())`, data: `{"m":[5,5]}`, want: `[5]`},
	{expr: `(function($x){$distinct($x)})([5,5])`, want: `[5]`},
	{expr: `(function($x){$distinct($x)})(a.b)`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `(function($x){$distinct(a.b)})(m)`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `5`},
	{expr: `(function($x){$distinct(m)})(a.b)`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `[5]`},
	{expr: `($f := function($x){$distinct($x)}; $f(a.b) & $string($f(m)))`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `"5[5]"`},
	{expr: `(function($a, $x){$distinct($x)})(1, [5,5])`, want: `[5]`},
	{expr: `(function($x)<a:a>{$distinct($x)})([5,5])`, want: `[5]`},
	{expr: `($f := function($x, $n){$n = 0 ? $distinct($x) : $f($x, $n - 1)}; $f([5,5], 3))`, want: `[5]`},
	{expr: `($f := function($x, $n){$n = 0 ? $distinct($x) : $f($x, $n - 1)}; $f(a.b, 3))`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `(function($x){$type($distinct($x))})([5,5])`, want: `"array"`},
	{expr: `[5,5] ~> $distinct()`, want: `[5]`},
	{expr: `a.b ~> $distinct()`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `[5,5] ~> $distinct`, want: `[5]`},
	{expr: `a.b ~> $distinct`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `[5,5] ~> $distinct() ~> $distinct()`, want: `[5]`},
	{expr: `a.b ~> $append([]) ~> $distinct()`, data: `{"a":[{"b":5},{"b":5}]}`, want: `[5]`},
	{expr: `[5,5] ~> function($x){$distinct($x)}`, want: `[5]`},
	{expr: `a.b ~> function($x){$distinct($x)}`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `$distinct(true ? m : a.b)`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `[5]`},
	{expr: `$distinct(false ? m : a.b)`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `5`},
	{expr: `$distinct(true ? m)`, data: `{"m":[5,5]}`, want: `[5]`},
	{expr: `$distinct(m ?: a.b)`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `[5]`},
	{expr: `$distinct(a.b ?: m)`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `5`},
	{expr: `$distinct(m ?? a.b)`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `[5]`},
	{expr: `$distinct(z ?? m)`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `[5]`},
	{expr: `$distinct(z ?? a.b)`, data: `{"a":[{"b":5},{"b":5}],"m":[5,5]}`, want: `5`},
	{expr: `$distinct($x := m)`, data: `{"m":[5,5]}`, want: `[5]`},
	{expr: `$distinct($x := a.b)`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `$distinct(nn[1-1])`, data: `{"nn":[[5,5]]}`, want: `[5]`},
	{expr: `($i := 0; $distinct(nn[$i]))`, data: `{"nn":[[5,5]]}`, want: `[5]`},
	{expr: `$distinct(nn[true])`, data: `{"nn":[[5,5]]}`, want: `[5]`},
	{expr: `$distinct(m[[0,1]])`, data: `{"m":[5,5]}`, want: `5`},
	{expr: `$distinct(m[])`, data: `{"m":[5,5]}`, want: `[5]`},
	{expr: `$distinct(o.m[])`, data: `{"o":{"m":[5,5]}}`, want: `[5]`},
	{expr: `$distinct(a.b[])`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `$distinct([5,5][])`, want: `[5]`},
	{expr: `$distinct(nn[0][])`, data: `{"nn":[[5,5]]}`, want: `[5]`},
	{expr: `($x := [5,5]; $distinct($x[]))`, want: `[5]`},
	{expr: `$distinct(o.[m][])`, data: `{"o":{"m":[5,5]}}`, want: `[[5,5]]`},
	{expr: `$distinct(o.([m])[])`, data: `{"o":{"m":[5,5]}}`, want: `[5]`},
	{expr: `$distinct(q.(k))`, data: `{"q":[{"k":[5,5]}]}`, want: `[5]`},
	{expr: `$distinct(q.(k))`, data: `{"q":[{"k":5},{"k":5}]}`, want: `5`},
	{expr: `$distinct(o.*)`, data: `{"o":{"m":[5,5]}}`, want: `[5]`},
	{expr: `$distinct(o.*)`, data: `{"o":{"y":5,"x":[],"z":5}}`, want: `[5]`},
	{expr: `$distinct(o.*)`, data: `{"o":{"y":5,"z":5}}`, want: `5`},
	{expr: `$distinct(nn.$)`, data: `{"nn":[[5,5]]}`, want: `[5]`},
	{expr: `$distinct(nn.($))`, data: `{"nn":[[5,5]]}`, want: `[5]`},
	{expr: `$distinct(m.$)`, data: `{"m":[5,5]}`, want: `5`},
	{expr: `nn.$distinct($)`, data: `{"nn":[[5,5]]}`, want: `[5]`},
	{expr: `$distinct($)`, data: `[5,5]`, want: `[5]`},
	{expr: `$distinct($$)`, data: `[5,5]`, want: `[5]`},
	{expr: `($f := function($x){$distinct($x)}; $g := function($h){$h([5,5])}; $g($f))`, want: `[5]`},
	{expr: `($f := function($x){$distinct($x)}; $g := function($h){$type($h([5,5]))}; $g($f))`, want: `"array"`},
	{expr: `($f := function($x){$distinct($x)}; $g := function($h){$h(a.b)}; $g($f))`, data: `{"a":[{"b":5},{"b":5}]}`, want: `5`},
	{expr: `$map([1], function($i){$filter([[1,2],[3]], function($v){$v[0]=1})})`, want: `[[1,2]]`},
	{expr: `$map([1,2], function($i){$filter([[1,2],[3]], function($v){$v[0]=1})})`, want: `[[[1,2]],[[1,2]]]`},
	{expr: `$keys([{"a":1},[{"b":2}],{"a":3,"c":1}])`, want: `["a","b","c"]`},
	{expr: `$keys([[{"a":1}]])`, want: `"a"`},
	{expr: `$spread([{"a":1,"b":2},3,[{"c":4}]])`, want: `[{"a":1},{"b":2},3,{"c":4}]`},
	{expr: `$spread([[]])`, want: `[]`},
	{expr: `$spread([{"a":1}])`, want: `[{"a":1}]`},
	{expr: `$count($keys($reduce([1..1000], function($a,$x){[[$a]]}, [{"k":1}])))`, want: `1`},
	{expr: `$count($spread($reduce([1..1000], function($a,$x){[[$a]]}, [{"k":1}])))`, want: `1`},
	{expr: `$lookup($reduce([1..1000], function($a,$x){[[$a]]}, [{"k":1}]), "k")`, want: `1`},
}

var numericBuiltinCases = []exprCase{
	{expr: `$number("0x1F")`, want: `31`},
	{expr: `$round(2.5)`, want: `2`},
	{expr: `$round(-2.5)`, want: `-2`},
	{expr: `$round(2.5001)`, want: `3`},
	{expr: `$round(1.2351, 2)`, want: `1.24`},
	{expr: `$round(-1.5, 3)`, want: `-1.5`},
	{expr: `$round(12345, -2)`, want: `12300`},
	{expr: `$average([])`, want: undefined},
	{expr: `$type($random())`, want: `"number"`},
	{expr: `$abs(a.b)`, data: `{"a":{"b":-3}}`, want: `3`},
}

var stringBuiltinCases = []exprCase{
	{expr: `$decodeUrl("a%20b%26c")`, want: `"a b%26c"`},
	{expr: `$decodeUrl("%3B%2F%3F%3A%40%26%3D%2B%24%2C%23")`, want: `"%3B%2F%3F%3A%40%26%3D%2B%24%2C%23"`},
	{expr: `$decodeUrl("%3b%2f")`, want: `"%3b%2f"`},
	{expr: `$decodeUrlComponent("%3B%2F%3F%3A%40%26%3D%2B%24%2C%23")`, want: `";/?:@&=+$,#"`},
	{expr: `$decodeUrl("%E2%82%AC")`, want: `"€"`},
	{expr: `$decodeUrl("%EF%BF%BD")`, want: "\"\uFFFD\""},
	{expr: `$decodeUrlComponent("a%EF%BF%BDb")`, want: "\"a\uFFFDb\""},
	{expr: `$decodeUrlComponent("%E2%82%AC%F0%9F%98%80")`, want: `"€😀"`},
	{expr: `$decodeUrl("a+b")`, want: `"a+b"`},
	{expr: `$decodeUrl("é%C3%A9")`, want: `"éé"`},
	{expr: `$decodeUrl("%25")`, want: `"%"`},
	{expr: `$decodeUrl("%2541")`, want: `"%41"`},
	{expr: `$decodeUrlComponent("")`, want: `""`},
	{expr: `$decodeUrl(nothing)`, want: undefined},
	// $string and & lay out numbers as jsonata-js does: a non-integer is
	// rounded to 15 significant digits (an exact tie away from zero), -0 is
	// 0, and below 1e-6 or from 1e21 numbers use exponent notation.
	{expr: `$string(5e-7)`, want: `"5e-7"`},
	{expr: `$string(9e-7)`, want: `"9e-7"`},
	{expr: `$string(1e-6)`, want: `"0.000001"`},
	{expr: `$string(1.5e-7)`, want: `"1.5e-7"`},
	{expr: `$string(1.23456789012345678e-7)`, want: `"1.23456789012346e-7"`},
	{expr: `$string(0.1+0.2)`, want: `"0.3"`},
	{expr: `$string([0.1+0.2])`, want: `"[0.3]"`},
	{expr: `$string({"b":0.1+0.2})`, want: `"{\"b\":0.3}"`},
	{expr: `$string(1e21)`, want: `"1e+21"`},
	{expr: `$string(1.5e21)`, want: `"1.5e+21"`},
	{expr: `$string(123456789012345678)`, want: `"123456789012345680"`},
	{expr: `$string(1/3)`, want: `"0.333333333333333"`},
	{expr: `$string([1/3])`, want: `"[0.333333333333333]"`},
	{expr: `"" & (0.1+0.2)`, want: `"0.3"`},
	{expr: `"" & 1.23456789012345678e-7`, want: `"1.23456789012346e-7"`},
	{expr: `"" & [1/3]`, want: `"[0.333333333333333]"`},
	{expr: `$string(-0)`, want: `"0"`},
	{expr: `$string(100)`, want: `"100"`},
	{expr: `$string(1e300*1.5)`, want: `"1.5e+300"`},
	{expr: `$string([1.23456789012345678e-7])`, want: `"[1.23456789012346e-7]"`},
	{expr: `$string(12345678.123456789)`, want: `"12345678.1234568"`},
	{expr: `$string(123456789012344.5)`, want: `"123456789012345"`},
	{expr: `$string([123456789012344.5])`, want: `"[123456789012345]"`},
	{expr: `$string(12345678901234.25)`, want: `"12345678901234.3"`},
	{expr: `$string(-12345678901234.25)`, want: `"-12345678901234.3"`},
	{expr: `$string(0.5)`, want: `"0.5"`},
	{expr: `$string(1.25)`, want: `"1.25"`},
	{expr: `$string(2.675)`, want: `"2.675"`},
	// Arrays built by a path step are laid out the same way.
	{expr: `$string(a.[b*0.1])`, data: `{"a":[{"b":3}]}`, want: `"[0.3]"`},
	{expr: `"" & a.[b*0.1]`, data: `{"a":[{"b":3}]}`, want: `"[0.3]"`},
	{expr: `$string([1..2].[0.1+0.2])`, want: `"[[0.3],[0.3]]"`},
	{expr: `$string({"a":[1..2].[1/3]})`, want: `"{\"a\":[[0.333333333333333],[0.333333333333333]]}"`},
	{expr: `$string([1..2].[$string])`, want: `"[[\"\"],[\"\"]]"`},
	{expr: `"" & [1..2].[$string]`, want: `"[[\"\"],[\"\"]]"`},
	{expr: `"" & [1, $string]`, want: `"[1,\"\"]"`},
	{expr: `"x" & $uppercase`, want: `"x"`},
	// A builtin returning its function argument returns the function
	// itself, where jsonata-js returns its closure (README known
	// difference #18); passed to a lambda, it is a closure in both.
	{expr: `($f := function(){1}; $append($f, [])[0] = $f)`, want: `true`},
	{expr: `($f := function(){1}; $f in $append($f, []))`, want: `true`},
	{expr: `($id := function($x){$x}; $f := function(){1}; $id($f) = $f)`, want: `false`},
	// A transform update holding its target stores a copy of it, so the
	// result stays a finite tree (README known difference #19).
	{expr: `($x := {"a":1} ~> |$|{"s":$}|; $string($x))`, want: `"{\"a\":1,\"s\":{\"a\":1}}"`},
	{expr: `($x := {"a":1} ~> |$|{"s":$}|; "" & $x)`, want: `"{\"a\":1,\"s\":{\"a\":1}}"`},
	{expr: `($x := {"a":1} ~> |$|($o:=$;{"s":[1].[$o]})|; $string($x))`, want: `"{\"a\":1,\"s\":[{\"a\":1}]}"`},
	{expr: `$string()`, want: undefined},
	{expr: `$string({"a":[1,{"b":2}]}, true)`, want: `"{\n  \"a\": [\n    1,\n    {\n      \"b\": 2\n    }\n  ]\n}"`},
	{expr: `$string([$string, 1])`, want: `"[\"\",1]"`},
	{expr: `$string(a.b)`, data: `{"a":[{"b":1},{"b":2}]}`, want: `"[1,2]"`},
	{expr: `"x" ~> $string()`, want: `"x"`},
	{expr: `$string ~> $string()`, want: `""`},
	{expr: `$substring("abc", 5)`, want: `""`},
	{expr: `$substring("abc", 1, -1)`, want: `""`},
	{expr: `"a-b" ~> $substringBefore("-")`, want: `"a"`},
	{expr: `"Ab" ~> $lowercase()`, want: `"ab"`},
	{expr: `$lowercase(undefined)`, want: undefined},
	{expr: `$uppercase(undefined)`, want: undefined},
	{expr: `"abc" ~> $contains("b")`, want: `true`},
	{expr: `$contains(["x","abc"], "b")`, want: `true`},
	{expr: `$contains([1, "x"], "b")`, want: `false`},
	{expr: `$contains("abc", undefined)`, want: undefined},
	{expr: `$split("a1b2c", /[0-9]/, 1)`, want: `["a"]`},
	{expr: `$match(undefined, /a/)`, want: undefined},
	{expr: `$match("ab", /(x)?b/)`, want: `{"groups":[""],"index":1,"match":"b"}`},
	{expr: `$replace("abc", /(x)?b/, function($m){ "[" & $m.groups[0] & "]" })`, want: `"a[]c"`},
	{expr: `$match("abc", /(x)?b/).groups[0] = null`, want: `false`},
	{expr: `$keys($match("abc", /b/))`, want: `["match","index","groups"]`},
	// Code point positions, unlike jsonata-js (README known difference #13).
	{expr: `$match("😀ab", /a/).index`, want: `1`},
	{expr: `$substring("😀ab", $match("😀ab", /a/).index)`, want: `"ab"`},
	{expr: `$replace("abc", /b/, function($m){ $join($keys($m), ",") })`, want: `"amatch,start,end,groups,nextc"`},
	{expr: `$replace("aba", /a/, function($m){ $string($m.next().start) }, 1)`, want: `"2ba"`},
	{expr: `$replace("abab", /b/, function($m){ $type($m.next) })`, want: `"afunctionafunction"`},
	{expr: `$match("aab", /a/, 1.5)`, want: `[{"groups":[],"index":0,"match":"a"},{"groups":[],"index":1,"match":"a"}]`},
	{expr: `$count($match("aaaa", /a/, 2.5))`, want: `3`},
	{expr: `$match("a1b2", /\d/, 0.5)`, want: `{"groups":[],"index":1,"match":"1"}`},
	{expr: `$match("", /x*/)`, want: `{"groups":[],"index":0,"match":""}`},
	{expr: `$match("ab ab", /\bab/)`, want: `[{"groups":[],"index":0,"match":"ab"},{"groups":[],"index":3,"match":"ab"}]`},
	{expr: `$replace("aaaa", /a/, "b", 2.5)`, want: `"bbba"`},
	{expr: `$replace("aaaa", "a", "b", 1.5)`, want: `"bbaa"`},
	{expr: `$replace("", /x*/, "-")`, want: `"-"`},
	{expr: `$split("a1b2c3", /\d/, 2.5)`, want: `["a","b","c"]`},
	{expr: `$split("a,b,c", ",", 1.5)`, want: `["a"]`},
	{expr: `$split("a,b,", /,/)`, want: `["a","b",""]`},
	{expr: `$replace("abc", /(x)?b/, function($m){ $string($m.groups) })`, want: `"a[\"\"]c"`},
	{expr: `$match("abc", function($s){ {"match":"a","start":0,"groups":[]} })`, want: `{"groups":[],"index":0,"match":"a"}`},
	{
		expr: `$match("abc", function($s){ {"match":"a","start":0,"groups":[], "next": function(){ {"match":"b","start":1,"groups":[]} }} })`,
		want: `[{"groups":[],"index":0,"match":"a"},{"groups":[],"index":1,"match":"b"}]`,
	},
	{
		expr: `$match("abc", function($s){ {"match":"a","start":0,"groups":[], "next": function(){ {"match":"b","start":1,"groups":[]} }} }, 1)`,
		want: `{"groups":[],"index":0,"match":"a"}`,
	},
	{expr: `$match("abc", function($s){ 5 })`, code: "T1010"},
	{expr: `$match("abc", function($s){ {} })`, code: "T1010"},
	{expr: `$match("abc", function($s){ false })`, want: undefined},
	{expr: `$replace("abc", "b", function($m){ "[" & $m.match & "]" })`, want: `"a[b]c"`},
	{expr: `$replace("abab", /b/, function($m){ "y" }, 1)`, want: `"ayab"`},
	{expr: `$replace("abcdefghijk", /(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)(k)/, "$12")`, want: `"a2"`},
	{expr: `$replace("abc", /(a)/, "$9x")`, want: `"xbc"`},
	{expr: `$replace("abc", /a/, "$$")`, want: `"$bc"`},
	{expr: `"abc" ~> /b(x)?/`, want: `{"end":2,"groups":[""],"match":"b","start":1}`},
	{expr: `5 ~> /b/`, want: undefined},
	{expr: `"abc" ~> /z/`, want: undefined},
	{expr: `/b(x)?/("abc")`, want: `{"end":2,"groups":[""],"match":"b","start":1}`},
	{expr: `/z/("abc")`, want: undefined},
	{expr: `/b/()`, want: undefined},
	{expr: `/5/(5).start`, want: `0`},
	{expr: `/n/(null).start`, want: `0`},
	{expr: `/u/().start`, want: `0`},
	{expr: `/d/(nothing).start`, want: `2`},
	{expr: `(nothing ~> /d/).start`, want: `2`},
	{expr: `/t/(true).start`, want: `0`},
	{expr: `/s/(false).start`, want: `3`},
	{expr: `/5/(0.5).start`, want: `2`},
	{expr: `/e/(1e21).match`, want: `"e"`},
	{expr: `/\+/(1e300).start`, want: `2`},
	{expr: `/e/(0.0000001).start`, want: `1`},
	{expr: `/\d+/(123.0).match`, want: `"123"`},
	{expr: `/-/(-0)`, want: undefined},
	{expr: `/,/([1,2]).start`, want: `1`},
	{expr: `/2/([1,2], 1).start`, want: `2`},
	{expr: `/,,/([1,null,2]).start`, want: `1`},
	{expr: `/1/([[1,[2]],null]).start`, want: `0`},
	{expr: `/,/([[],[]]).start`, want: `0`},
	{expr: `/./([])`, want: undefined},
	{expr: `/,/(c).start`, data: `{"c":[true,"x",null]}`, want: `4`},
	{expr: `/O/(a).start`, data: `{"a":{}}`, want: `8`},
	{expr: `/O/([a]).start`, data: `{"a":{}}`, want: `8`},
	{expr: `(n ~> /5/).start`, data: `{"n":5}`, want: `0`},
	{expr: `([1,2] ~> /,/).start`, want: `1`},
	{expr: `$map([1,2], /\d/).start`, want: `0`},
	{expr: `/b/("abc", 2)`, want: undefined},
	{expr: `/b/("abcb", 2).start`, want: `3`},
	{expr: `/b/("abc", 1.9).start`, want: `1`},
	{expr: `/b/("abc", "x").start`, want: `1`},
	{expr: `/a/("aba", "0x2").start`, want: `2`},
	{expr: `/a/("aba", "inf").start`, want: `0`},
	{expr: `/a/("aba", ["2"]).start`, want: `2`},
	{expr: `/a/("aba", [true]).start`, want: `0`},
	{expr: `/a/("aba", []).start`, want: `0`},
	{expr: `/a/("aba", [[]]).start`, want: `0`},
	{expr: `/a/("aba", 2.7).start`, want: `2`},
	{expr: `/a/("aba", "Infinity")`, want: undefined},
	{expr: `/a/("aba", " +1e400 ")`, want: undefined},
	{expr: `/a/("aba", "-Infinity").start`, want: `0`},
	{expr: `/a/("aba", "0xFFFFFFFFFFFFFFFFFFFF")`, want: undefined},
	{expr: `/a/("aba", "0x1p400").start`, want: `0`},
	{expr: `/a/("aba", "++1e400").start`, want: `0`},
	{expr: `/a/("aba", "\uFEFF2").start`, want: `2`},
	{expr: `/a/("aba", "\u20282").start`, want: `2`},
	{expr: `/a/("aba", "\u00852").start`, want: `0`},
	{expr: `/a/("aba", "1_0").start`, want: `0`},
	{expr: `/a/("aba", n)`, data: `{"n": 1e400}`, want: undefined},
	{expr: `/a/("aba", $.["2"]).start`, data: `{}`, want: `2`},
	{expr: `/b/($.["abc"]).start`, data: `{}`, want: `1`},
	{expr: `($.["abc"] ~> /b/).start`, data: `{}`, want: `1`},
	{expr: `/x*/("ab", 2).start`, want: `2`},
	{expr: `/x*/("ab", 3)`, want: undefined},
	{expr: `/^b/("bb", 1)`, want: undefined},
	{expr: `/\Ab/("ab", 1)`, want: undefined},
	{expr: `/\bb/("ab b", 1).start`, want: `3`},
	{expr: `/^b/m("a\nb", 1).start`, want: `2`},
	{expr: `/\w+\b/("ab", 1).start`, want: `1`},
	{expr: `/\w+\b/("ab cd", 1).match`, want: `"b"`},
	{expr: `/^a|\B/("ab", 1).start`, want: `1`},
	{expr: `/\b\w/("ab cd", 1).start`, want: `3`},
	{expr: `/(\w)\b/("ab cd", 1).groups`, want: `["b"]`},
	{expr: `/\B\w+/("ab cd", 2).start`, want: `4`},
	{expr: `/^|b/("ab", 1).start`, want: `1`},
	{expr: `/\b/("ab", 1).start`, want: `2`},
	{expr: `/(?:\b|x)c/("éxc", 1).match`, want: `"xc"`},
	{expr: `/\bc/("éc", 1).start`, want: `1`},
	{expr: `/a|\Ab/("xb", 1)`, want: undefined},
	{expr: `/\w\b/i("aB cd", 1).match`, want: `"B"`},
	{expr: `/(?:^|,)(\w)/("a,b", 1).match`, want: `",b"`},
	{expr: `/^b/m("ab", 1)`, want: undefined},
	{expr: `/\bx|y/("ay x", 1).match`, want: `"y"`},
	{expr: `/\bb/("ab", 1)`, want: undefined},
	{expr: `/\Bb/("ab", 1).start`, want: `1`},
	{expr: `/b/(["abc"]).start`, want: `1`},
	{expr: `["abc"] ~> /b/`, want: `{"end":2,"groups":[],"match":"b","start":1}`},
	{expr: `($r := /b/; $r("abc").start)`, want: `1`},
	{expr: `/b/(?)("abc").start`, want: `1`},
	{expr: `$map(["ab", "cb"], /b/).start`, want: `[1,1]`},
	{expr: `$map(["ba", "ba"], /b/).start`, want: `0`},
	{expr: `$filter(["ba", "ab", "ba"], /b/)`, want: `["ba","ab"]`},
	{expr: `$sift({"k": "ab", "j": "cd"}, /a/)`, want: `{"k":"ab"}`},
	{expr: `$each({"k": "ab"}, /a/).start`, want: `0`},
	{expr: `$sort(["b", "a", "c"], /a/)`, want: `["b","c","a"]`},
	{expr: `function($f)<f:x>{ $f("ab").start }(/a/)`, want: `0`},
	{expr: `($c := /a/ ~> $count; $c("ab"))`, want: `1`},
	{expr: `$type(/a/)`, want: `"function"`},
	{expr: `$string(/a/)`, want: `""`},
	{expr: `$string({"r": /a/})`, want: `"{\"r\":\"\"}"`},
	{expr: `$keys(/a/)`, want: undefined},
	{expr: `$lookup(/a/, "pattern")`, want: undefined},
	{expr: `$boolean(/a/)`, want: `false`},
	{expr: `/a/ = /a/`, want: `false`},
	{expr: `($r := /a/; $r = $r)`, want: `true`},
	{expr: `/a/ & "x"`, want: `"x"`},
	{expr: `$sum & "x"`, want: `"x"`},
	{expr: `$type(r)`, data: `{"r": {"pattern": "a", "flags": "g"}}`, want: `"object"`},
	{expr: `/a/`, want: `{"flags":"g","pattern":"a"}`},
	{expr: `{"r": /a/}`, want: `{"r":{"flags":"g","pattern":"a"}}`},
	{expr: `$eval("a", {"a": 3})`, want: `3`},
	{expr: `$formatInteger(12, "Ww")`, want: `"Twelve"`},
	{expr: `$formatInteger(1234, "٠١٢")`, want: `"١٢٣٤"`},
	{expr: `$formatInteger(1234, "#")`, code: "D3130"},
	{expr: `$formatInteger(1, "b")`, code: "D3130"},
	{expr: `$formatInteger(28, "A")`, want: `"AB"`},
	{expr: `$formatInteger(1234, "#;o")`, code: "D3130"},
	{expr: `$formatInteger(0, "w")`, want: `"zero"`},
	{expr: `$formatInteger(1000000000000000000, "w")`, want: `"one million trillion"`},
	{expr: `$formatInteger(1.5, "w")`, want: `"one"`},
	{expr: `$formatInteger(22, "1;o")`, want: `"22nd"`},
	{expr: `$formatInteger(13, "1;o")`, want: `"13th"`},
	{expr: `$formatInteger(20, "w;o")`, want: `"twentieth"`},
	{expr: `$formatInteger(21, "Ww;o")`, want: `"Twenty-First"`},
	{expr: `$formatInteger(1000000, "w;o")`, want: `"one millionth"`},
	{expr: `$formatInteger(1e21, "W")`, want: `"ONE BILLION TRILLION"`},
	{expr: `$formatInteger(1e21, "Ww")`, want: `"One Billion Trillion"`},
	{expr: `$formatInteger(1e21, "w;o")`, want: `"one billion trillionth"`},
	{expr: `$formatInteger(1e21, "W;o")`, want: `"ONE BILLION TRILLIONTH"`},
	{expr: `$formatInteger(1e21, "Ww;o")`, want: `"One Billion Trillionth"`},
	{expr: `$formatInteger(-1e21, "w")`, want: `"-one billion trillion"`},
	{expr: `$formatInteger(-1e21, "Ww")`, want: `"-One Billion Trillion"`},
	{expr: `$base64decode("YQ")`, want: `"a"`},
	{expr: `$base64decode("YWI")`, want: `"ab"`},
	{expr: `$base64decode("YQ=")`, want: `"a"`},
	{expr: `$formatNumber(0, "#.#")`, want: `".0"`},
	{expr: `$formatNumber(-0.5, ".00")`, want: `"-.50"`},
	{expr: `$formatNumber(0.4, "#")`, want: `"0"`},
	{expr: `$replace("abcb", /b/, "x", undefined)`, want: `"axcx"`},
	{expr: `$formatInteger(1234567, "#,##0")`, want: `"1,234,567"`},
	{expr: `$parseInteger("hundred", "w")`, want: `100`},
	{expr: `$parseInteger("thousand", "w")`, want: `1000`},
	{expr: `$parseInteger("one million two", "w")`, want: `1000002`},
	{expr: `$parseInteger("١٢", "١")`, want: `12`},
	{expr: `$formatNumber(1234.5, "#,##0.00")`, want: `"1,234.50"`},
	{expr: `$formatNumber(0.5, ".0")`, want: `".5"`},
	{expr: `$formatNumber(12.3456, "0.00##")`, want: `"12.3456"`},
	{expr: `$formatNumber(12, "0.00##")`, want: `"12.00"`},
	{expr: `$formatNumber(12, "0.#")`, want: `"12"`},
	{expr: `$formatNumber(12, "0.#%")`, want: `"1200%"`},
	{expr: `$formatNumber(12, "0.e0")`, want: `"1e1"`},
	{expr: `$formatNumber(1e21, "0.0e0")`, want: `"1.0e21"`},
	{expr: `$formatNumber(0.05, ".0")`, want: `".0"`},
	{expr: `$formatNumber(0.15, ".0")`, want: `".2"`},
	{expr: `$formatNumber(-0.15, "0.0")`, want: `"-0.2"`},
	{expr: `$formatNumber(4.525, "0.00")`, want: `"4.52"`},
	{expr: `$formatNumber(12.345, "0.0#")`, want: `"12.34"`},
	{expr: `$formatNumber(2.5e10, "0e0")`, want: `"2e10"`},
	{expr: `$formatNumber(1.35e-5, "0.0e0")`, want: `"1.4e-5"`},
	{expr: `$formatNumber(5e-324, "0.0e0")`, want: `"4.9e-324"`},
	{expr: `$formatNumber(1234.5, "0.000e0")`, want: `"1.235e3"`},
	{expr: `$formatNumber(265e-4, "0.0e0")`, want: `"2.7e-2"`},
	{expr: `$formatNumber(0.000123456, "#.00e0")`, want: `"0.12e-3"`},
	{expr: `$formatNumber(9.95, "0.0e0")`, want: `"1.0e1"`},
}

// tailAndCollapseCases cover where jsonata-js collapses a built-in's result
// sequence: once, when an expression returns it, which a call in a lambda's
// tail position skips.
var tailAndCollapseCases = []exprCase{
	{expr: `$map([{"a":1}], $keys) ?: 1`, want: `"a"`},
	{expr: `$map([{"a":1}], $keys) ?? 1`, want: `"a"`},
	{expr: `false ?: $map([{"a":1}], $keys)`, want: `"a"`},
	{expr: `(o.b[] ?: 1)`, data: pairsJSON, want: `[5]`},
	{expr: `a.$map([$], $keys)[0]`, data: pairsJSON, want: `["b","b"]`},
	{expr: `a.($map([$], $keys)[0])`, data: pairsJSON, want: `["b","c","b","c"]`},
	{expr: `($f := function(){$string()}; o.$f())`, data: pairsJSON, want: `"{\"b\":5,\"c\":6}"`},
	{expr: `($y := 1; $f := function(){($y := 2; $eval("$y"))}; $f())`, want: `1`},
	{expr: `($f := function(){$keys({"a":1,"b":2}){$: 1}}; $f())`, want: `["a","b"]`},
	{expr: `($f := function($n){ $n > 3 ? $n : $map([$n+1], $f) }; $f(0))`, want: `[[4]]`},
	{expr: `($f := function($n){($r := $n = 0 ? "done" : $f($n-1))}; $f(50))`, want: `"done"`},
	{expr: `($ ~> |o|{"z": b[]}|).o.z`, data: pairsJSON, want: `5`},
	{expr: `$filter(n, function($v){$v[0]=1})[0].$`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `$map([{"a":1,"b":2}], $keys)[1].$`, want: undefined},
	{expr: `o.$filter($$.n, function($v){$v[0]=1})[0]`, data: `{"n":[[1,2],[3]],"o":{}}`, want: `1`},
	{expr: `o.$filter($$.n, function($v){$v[0]=1})[0][]`, data: `{"n":[[1,2],[3]],"o":{}}`, want: `[1,2]`},
	{expr: `$map([1,2], function($v){$keys({"a":1}) ?: 0})`, want: `[["a"],["a"]]`},
	{expr: `($f := function(){$keys({"a":1})[] ?: 0}; $f())`, want: `"a"`},
	{expr: `$map([1], function($v){$zz ?? $keys({"a":1})})`, want: `["a"]`},
	{expr: `($f := function($n){$n > 0 ? ($zz ?? $f($n-1)) : "d"}; $f(300))`, want: `"d"`},
	{expr: `$map([1,2], function($v){$keys(5)})`, want: `[[],[]]`},
	{expr: `$map([1,2], $keys)`, want: `[[],[]]`},
	{expr: `$map([1,2], function($v){$filter([1], function($w){false})})`, want: `[[],[]]`},
	{expr: `{"k": $map([1], function($v){$keys(5)})}`, want: `{"k":[]}`},
	{expr: `$map([1], function($v){$keys(5)})[]`, want: `[[]]`},
	{expr: `$count($map([1,2], function($v){$keys(5)}))`, want: `2`},
	{expr: `($m := $map([1], function($v){$keys(5)}); $m)`, want: undefined},
	{expr: `($m := $map([1], function($v){$keys(5)}); $m[])`, want: undefined},
	{expr: `$exists([{"k": $map([1], $keys)}].k)`, want: `false`},
	{expr: `$reverse($map([1], $keys))`, want: undefined},
	{expr: `$map([1,2], function($v){$append([], [])})`, want: `[[],[]]`},
	{expr: `$map([1,2], function($v){$match("a", /b/)})`, want: `[[],[]]`},
	{expr: `$map([[]], $spread)`, want: `[]`},
	{expr: `$append([], [])`, want: `[]`},
	{expr: `$map([1], $keys)^($)`, want: undefined},
	{expr: `$map($map([1,2], function($v){$match("a", /b/)}), $reverse)[0]`, want: undefined},
}

func TestBuiltinResults(t *testing.T) {
	t.Run("array and object", func(t *testing.T) { runExprCases(t, arrayAndObjectBuiltinCases) })
	t.Run("tail calls and collapse", func(t *testing.T) { runExprCases(t, tailAndCollapseCases) })
	t.Run("numeric", func(t *testing.T) { runExprCases(t, numericBuiltinCases) })
	t.Run("string", func(t *testing.T) { runExprCases(t, stringBuiltinCases) })
}

var builtinArgumentErrorCases = []exprCase{
	{expr: `$append([1])`, code: "T0410"},
	{expr: `$sort([3,1,2], function($a,$b){$error("boom")})`, code: "D3137"},
	{expr: `$sort([1,"a"])`, code: "D3070"},
	{expr: `$flatten([1,[2]], "x")`, code: "T0410"},
	{expr: `$map()`, code: "T0410"},
	{expr: `$filter()`, code: "T0410"},
	{expr: `$filter([1,2,3], function($v){$error("x")})`, code: "D3137"},
	{expr: `$single([1,2,3], function($v){$v>1})`, code: "D3138"},
	{expr: `$single([1,2,3], function($v){$v>5})`, code: "D3139"},
	{expr: `$single([1,2,3], function($v){$error("x")})`, code: "D3137"},
	{expr: `$reduce()`, code: "T0410"},
	{expr: `$reduce([1,2], function($a,$b){$error("x")})`, code: "D3137"},
	{expr: `$assert()`, code: "T0410"},
	{expr: `$assert(true, "a", "b")`, code: "T0410"},
	{expr: `$assert(false)`, code: "D3141"},
	{expr: `$assert(nothing)`, code: "D3141"},
	{expr: `$each({"a":1}, function($v,$k){$error("x")})`, code: "D3137"},
	{expr: `$each()`, code: "T0410: argument 1"},
	{expr: `$each(5, function($v){$v})`, code: "T0410"},
	{expr: `$sift(null, function($v){$v})`, code: "T0410"},
	{expr: `$sift(5, function($v){$v})`, code: "T0410"},
	{expr: `$sift({"a":1}, function($v){$error("x")})`, code: "D3137"},
	{expr: `$merge(5)`, code: "T0412"},
	{expr: `$merge([1])`, code: "T0412"},
	{expr: `$error("a","b")`, code: "T0410"},
	{expr: `$lookup({"a":1})`, code: "T0410: argument 2"},
	{expr: `$lookup({"a":1}, 5)`, code: "T0410"},
	{expr: `$number("zz")`, code: "D3030"},
	{expr: `$number(a)`, data: `{"a":1e999}`, code: "D3030"},
	{expr: `$sum(1,2)`, code: "T0410"},
	{expr: `$sum("a")`, code: "T0412"},
	{expr: `$max("a")`, code: "T0412"},
	{expr: `$min()`, code: "T0410"},
	{expr: `$min("a")`, code: "T0412"},
	{expr: `$average()`, code: "T0410"},
	{expr: `$average("a")`, code: "T0412"},
	{expr: `$sqrt(-1)`, code: "D3060"},
	{expr: `$string(1, true, 3)`, code: "T0410"},
	{expr: `$string(1, $string)`, code: "T0410: argument 2"},
	{expr: `$string(1, 5)`, code: "T0410"},
	{expr: `$string(1/0)`, code: "D3001"},
	{expr: `$substring("abc")`, code: "T0410: argument 2"},
	{expr: `$substring("abc", 1, 1, 1)`, code: "T0410"},
	{expr: `$substring("abc", "x")`, code: "T0410"},
	{expr: `$substring("abc", 1, "x")`, code: "T0410"},
	{expr: `$substringBefore("a-b", "-", 1)`, code: "T0410"},
	{expr: `$substringBefore()`, code: "T0410: argument 1"},
	{expr: `$uppercase(5)`, code: "T0410"},
	{expr: `$lowercase(5)`, code: "T0410"},
	{expr: `$trim(5)`, code: "T0410"},
	{expr: `$pad("a")`, code: "T0410: argument 2"},
	{expr: `$pad(5, 3)`, code: "T0410"},
	{expr: `$pad("a", "x")`, code: "T0410"},
	{expr: `$pad("a", 1e9)`, code: "D3010"},
	{expr: `$pad("a", 3, 5)`, code: "T0410"},
	{expr: `$contains(5, "b")`, code: "T0410"},
	{expr: `$contains("abc", 5)`, code: "T0410"},
	{expr: `$contains("abc", /a{2,1}/)`, code: "D3137"},
	{expr: `$split()`, code: "T0410: argument 1"},
	{expr: `$split(5, ",")`, code: "T0410"},
	{expr: `$split("a,b", ",", "x")`, code: "T0410"},
	{expr: `$split("a,b", ",", -1)`, code: "D3020"},
	{expr: `$split("a,b", /a{2,1}/)`, code: "D3137"},
	{expr: `$split("a,b", $string)`, code: "T1010"},
	{expr: `$match()`, code: "T0410: argument 1"},
	{expr: `$match(5, /a/)`, code: "T0410"},
	{expr: `$match("abc", /a{2,1}/)`, code: "D3137"},
	{expr: `$match("abc", function($s){ {"match":"a","start":0,"groups":[], "next": function(){ $error("n") }} })`, code: "D3137"},
	{expr: `$match("abc", function($s){ $error("m") })`, code: "D3137"},
	{expr: `$match("abc", function($s){ {"match":"a","start":0,"groups":[], "next": function(){ $error("n") }} }, 1)`, code: "D3137"},
	{expr: `$match("aaaa", /a/, -1)`, code: "D3040"},
	{expr: `$match("a", /a/, -0.5)`, code: "D3040"},
	{expr: `$match("abc", /x*/)`, code: "D1004"},
	{expr: `$match("abc", /x*/, 1)`, code: "D1004"},
	{expr: `$match("a,b,", /[^,]*/)`, code: "D1004"},
	{expr: `$split("abc", /x*/)`, code: "D1004"},
	{expr: `$split("a,b", /,/, -0.5)`, code: "D3020"},
	{expr: `$replace(5, "a", "b")`, code: "T0410"},
	{expr: `$replace("abc")`, code: "T0410"},
	{expr: `$replace("abc", "b", "x", "1")`, code: "T0410"},
	{expr: `$replace("abc", "b", "x", "y")`, code: "T0410"},
	{expr: `$replace("abc", "b", "x", -1)`, code: "D3011"},
	{expr: `$replace("abc", "", "x")`, code: "D3010"},
	{expr: `$replace("abc", 5, "x")`, code: "T0410"},
	{expr: `$replace("abc", /a{2,1}/, "x")`, code: "D3137"},
	{expr: `$replace("abc", /b/, function($m){ $error("r") })`, code: "D3137"},
	{expr: `$replace("abc", /b/, function($m){ 5 })`, code: "D3012"},
	{expr: `$replace("abc", /x*/, function($m){ "y" })`, code: "D1004"},
	{expr: `"abc" ~> /a{2,1}/`, code: "D1002"},
	{expr: `/a{2,1}/("abc")`, code: "D1002"},
	{expr: `{"pattern": 5}("abc")`, code: "T1006"},
	{expr: `"abc" ~> {"pattern": 5}`, code: "T2006"},
	{expr: `{"pattern": "a"}("abc")`, code: "T1006"},
	{expr: `"abc" ~> {"pattern": "a"}`, code: "T2006"},
	{expr: `r("abc")`, data: `{"r": {"pattern": "a"}}`, code: "T1006"},
	{expr: `r(?)`, data: `{"r": {"pattern": "a"}}`, code: "T1008"},
	{expr: `"a" ~> $`, data: `{"pattern": "a", "x": 1}`, code: "T2006"},
	{expr: `$match("abc", r)`, data: `{"r": {"pattern": "a"}}`, code: "T0410"},
	{expr: `$replace("abc", r, "x")`, data: `{"r": {"pattern": "a"}}`, code: "T0410"},
	{expr: `$split("abc", r)`, data: `{"r": {"pattern": "a"}}`, code: "T0410"},
	{expr: `$contains("abc", r)`, data: `{"r": {"pattern": "a"}}`, code: "T0410"},
	{expr: `r("abc")`, data: `{"r": {"pattern": "a", "flags": "g"}}`, code: "T1006"},
	{expr: `"abc" ~> r`, data: `{"r": {"pattern": "a", "flags": "g"}}`, code: "T2006"},
	{expr: `$match("abc", r)`, data: `{"r": {"pattern": "a", "flags": "g"}}`, code: "T0410"},
	{expr: `function($f)<j:x>{ 1 }(/a/)`, code: "T0410"},
	{expr: `$eval()`, code: "T0410"},
	{expr: `$eval(5)`, code: "T0410"},
	{expr: `$eval("1 +")`, code: "D3120"},
	{expr: `$eval("$error('e')")`, code: "D3121"},
	{expr: `$eval("1 + 'a'")`, code: "D3121"},
	{expr: `$base64encode(5)`, code: "T0410"},
	{expr: `$base64decode(5)`, code: "T0410"},
	{expr: `$base64decode("!!!")`, code: "D3137"},
	{expr: `$encodeUrl(5)`, code: "T0410"},
	{expr: `$encodeUrlComponent(5)`, code: "T0410"},
	{expr: `$decodeUrl(5)`, code: "T0410"},
	{expr: `$decodeUrlComponent(5)`, code: "T0410"},
	{expr: `$decodeUrl("%")`, code: "D3140"},
	{expr: `$decodeUrl("%2")`, code: "D3140"},
	{expr: `$decodeUrl("%zz")`, code: "D3140"},
	{expr: `$decodeUrl("%FF")`, code: "D3140"},
	{expr: `$decodeUrl("%C0%AF")`, code: "D3140"},
	{expr: `$decodeUrl("%ED%A0%80")`, code: "D3140"},
	{expr: `$decodeUrl("%E2%82")`, code: "D3140"},
	{expr: `$decodeUrl("%E2%82%41")`, code: "D3140"},
	{expr: `$decodeUrl("%F4%90%80%80")`, code: "D3140"},
	{expr: `$decodeUrlComponent("%80")`, code: "D3140"},
	{expr: `$formatBase("x")`, code: "T0410"},
	{expr: `$formatInteger(1)`, code: "T0410: argument 2"},
	{expr: `$formatInteger("x", "1")`, code: "T0410"},
	{expr: `$formatInteger(1, 5)`, code: "T0410"},
	{expr: `$formatInteger(1e300, "1")`, code: "D3137"},
	{expr: `$formatInteger(5, "1١")`, code: "D3131"},
	{expr: `$formatInteger(5, "١#1")`, code: "D3131"},
	{expr: `$formatInteger(1234, "0١")`, code: "D3131"},
	{expr: `$parseInteger(5, "1")`, code: "T0410"},
	{expr: `$parseInteger("1", 5)`, code: "T0410"},
	{expr: `$parseInteger("x", "1")`, code: "D3137"},
	{expr: `$parseInteger("one blah", "w")`, code: "D3137"},
	{expr: `$parseInteger("XQ", "I")`, code: "D3137"},
	{expr: `$parseInteger("a1", "a")`, code: "D3137"},
	{expr: `$formatNumber("x", "#")`, code: "T0410"},
	{expr: `$formatNumber(1, "abc")`, code: "D3086"},
	{expr: `$formatNumber(1, "")`, code: "D3085"},
	{expr: `$formatNumber(1, "e")`, code: "D3093"},
	{expr: `$formatNumber(1, "0e#")`, code: "D3093"},
	{expr: `$formatNumber(1, "0.0e-0")`, code: "D3093"},
	{expr: `$formatNumber(1, "0-0")`, code: "D3086"},
	{expr: `$formatNumber(1, "0-0.")`, code: "D3086"},
	{expr: `$formatNumber(1, "#.#.#")`, code: "D3081"},
}

func TestBuiltinArgumentErrors(t *testing.T) {
	runExprCases(t, builtinArgumentErrorCases)
}

// signatureArgumentCases call builtins with arguments their jsonata-js
// signature rejects; each code names the argument jsonata-js blames.
var signatureArgumentCases = []exprCase{
	{expr: `$sum()`, code: "T0410: argument 1 does not match"},
	{expr: `$sum(["a"])`, code: "T0412: argument 1 must be an array of n"},
	{expr: `$sum(x)`, data: `{"x":["a"]}`, code: "T0412: argument 1 must be an array of n"},
	{expr: `$sum([1], 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$count()`, code: "T0410: argument 1 does not match"},
	{expr: `$count([1], 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$max({"a":1})`, code: "T0412: argument 1 must be an array of n"},
	{expr: `$max()`, code: "T0410: argument 1 does not match"},
	{expr: `$min(["a"])`, code: "T0412: argument 1 must be an array of n"},
	{expr: `$min([1], 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$average([true])`, code: "T0412: argument 1 must be an array of n"},
	{expr: `$average()`, code: "T0410: argument 1 does not match"},
	{expr: `$join([1])`, code: "T0412: argument 1 must be an array of s"},
	{expr: `$join(["a"], 1)`, code: "T0410: argument 2 does not match"},
	{expr: `$join()`, code: "T0410: argument 1 does not match"},
	{expr: `$join("a", ",", 1)`, code: "T0410: argument 3 does not match"},
	{expr: `$random(1)`, code: "T0410: argument 1 does not match"},
	{expr: `$map([1], 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$map([1])`, code: "T0410: argument 2 does not match"},
	{expr: `$map(1, 2, 3)`, code: "T0410: argument 2 does not match"},
	{expr: `$map($string, [1])`, code: "T0410: argument 2 does not match"},
	{expr: `$zip()`, code: "T0410: argument 1 does not match"},
	{expr: `$filter([1], 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$filter([1])`, code: "T0410: argument 2 does not match"},
	{expr: `$filter(1, [1])`, code: "T0410: argument 2 does not match"},
	{expr: `$single([1], 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$single()`, code: "T0410: argument 1 does not match"},
	{expr: `$reduce([1], $string, $string)`, code: "T0410: argument 3 does not match"},
	{expr: `$reduce([1], 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$reduce([1])`, code: "T0410: argument 2 does not match"},
	{expr: `$reduce($string, [1])`, code: "T0410: argument 2 does not match"},
	{expr: `$append([1])`, code: "T0410: argument 2 does not match"},
	{expr: `$append(1, 2, 3)`, code: "T0410: argument 3 does not match"},
	{expr: `$exists()`, code: "T0410: argument 1 does not match"},
	{expr: `$exists(1, 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$merge([1])`, code: "T0412: argument 1 must be an array of o"},
	{expr: `$merge("a")`, code: "T0412: argument 1 must be an array of o"},
	{expr: `$merge()`, code: "T0410: argument 1 does not match"},
	{expr: `$reverse()`, code: "T0410: argument 1 does not match"},
	{expr: `$reverse([1], 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$error(1)`, code: "T0410: argument 1 does not match"},
	{expr: `$error("a", "b")`, code: "T0410: argument 2 does not match"},
	{expr: `$type()`, code: "T0410: argument 1 does not match"},
	{expr: `$type(1, 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$sort([1], 1)`, code: "T0410: argument 2 does not match"},
	{expr: `$sort([1], $string, 1)`, code: "T0410: argument 3 does not match"},
	{expr: `$sort()`, data: `{"a":1}`, code: "T0410: argument 1 does not match"},
	{expr: `{"a":1}.$sort()`, code: "T0410: argument 1 does not match"},
	{expr: `$shuffle()`, code: "T0410: argument 1 does not match"},
	{expr: `$shuffle([1], 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$distinct()`, code: "T0410: argument 1 does not match"},
	{expr: `$distinct(1, 2)`, code: "T0410: argument 2 does not match"},
	{expr: `$eval(1)`, code: "T0410: argument 1 does not match"},
	{expr: `$eval()`, code: "T0410: argument 1 does not match"},
	{expr: `$eval("a", 1, 2)`, code: "T0410: argument 3 does not match"},
	{expr: `$now(1)`, code: "T0410: argument 1 does not match"},
	{expr: `$now("[Y]", 1)`, code: "T0410: argument 2 does not match"},
	{expr: `$now("a", "b", "c")`, code: "T0410: argument 3 does not match"},
	{expr: `$millis(1)`, code: "T0410: argument 1 does not match"},
	{expr: `$map([1], $sort)`, code: "T0410: argument 2 does not match"},
	{expr: `$map(["a"], $sum)`, code: "T0412: argument 1 must be an array of n"},
	{expr: `$zip(1, "a")`, want: `[[1,"a"]]`},
	{expr: `$reduce(1, function($x, $y){$x + $y})`, want: `1`},
	{expr: `$reduce([1, 2], function($x, $y){$x + $y}, nothing)`, want: `3`},
	{expr: `$join(["a", "b"], nothing)`, want: `"ab"`},
	{expr: `$sum(5)`, want: `5`},
	{expr: `$merge({"a":1})`, want: `{"a":1}`},
	{expr: `$sort("b")`, want: `["b"]`},
}

func TestSignatureArgumentErrors(t *testing.T) {
	runExprCasesAllAPIs(t, signatureArgumentCases)
}

// A partial application of a higher-order builtin is not validated, so its
// array argument is not wrapped: jsonata-js loops over the argument's
// length, visiting a string's characters and nothing of a number, object
// or function. Expected values are jsonata-js 2.2.2's.
func TestPartialHigherOrderArguments(t *testing.T) {
	runExprCasesAllAPIs(t, []exprCase{
		{expr: `$map(?)(function($v){$v})`, want: undefined},
		{expr: `$filter(?)(function($v){$v})`, want: undefined},
		{expr: `$map(?)($string)`, want: undefined},
		{expr: `$map(?)(/a/)`, want: undefined},
		{expr: `$map(?)(5)`, want: undefined},
		{expr: `$filter(?)({})`, want: undefined},
		{expr: `$map(?)(nothing)`, want: undefined},
		{expr: `$map(?, function($v){$v})(5)`, want: undefined},
		{expr: `$filter(?, function($v){true})(5)`, want: undefined},
		{expr: `$map(?, function($v){$v})({"a":1})`, want: undefined},
		{expr: `$map(?, function($v){$v})("ab")`, want: `["a","b"]`},
		{expr: `$filter(?, function($v){$v != "b"})("abc")`, want: `["a","c"]`},
		{expr: `$reduce(?, function($a,$b){$a & $b})("abc")`, want: `"abc"`},
		{expr: `$reduce(?, function($a,$b){$a+$b})(5)`, want: undefined},
		{expr: `$single(?, function($v){true})(5)`, code: "D3139"},
		{expr: `$single(?)(5)`, code: "D3139"},
		{expr: `$single(?)([1])`, want: `1`},
		{expr: `$sort(?)([2,1])`, want: `[1,2]`},
		{expr: `$map(?, function($v){$v})([1,2])`, want: `[1,2]`},
		// Direct calls wrap a non-array argument, as jsonata-js's signature does.
		{expr: `$map(5, function($v){$v})`, want: `5`},
		{expr: `$type($map(function($v){$v}, function($v){$v}))`, want: `"function"`},
	})
}

// $power raises D3061 for an undefined exponent, as jsonata-js's
// Math.pow(base, undefined) is NaN; an undefined base stays undefined.
// Expected values are jsonata-js 2.2.2's.
func TestPowerUndefinedArguments(t *testing.T) {
	runExprCasesAllAPIs(t, []exprCase{
		{expr: `$power(2, nothing)`, code: "D3061"},
		{expr: `$power(2, x)`, data: `{"n":2}`, code: "D3061"},
		{expr: `n.$power(nothing)`, data: `{"n":2}`, code: "D3061"},
		{expr: `$power(?, nothing)(2)`, code: "D3061"},
		{expr: `$power(nothing, 2)`, want: undefined},
		{expr: `$power(nothing, nothing)`, want: undefined},
	})
}

// builtinContextCases cover the builtins whose first parameter defaults to
// the context value (the '-' marker in their jsonata-js signatures).
var builtinContextCases = []exprCase{
	// Every context builtin, called as a path step.
	{expr: `n.$string()`, data: `{"n":1.5}`, want: `"1.5"`},
	{expr: `o.$string(true)`, data: `{"o":{"a":1}}`, want: `"true"`},
	{expr: `s.$substring(1)`, data: `{"s":"hello"}`, want: `"ello"`},
	{expr: `s.$substring(1, 2)`, data: `{"s":"hello"}`, want: `"el"`},
	{expr: `s.$substringBefore("l")`, data: `{"s":"hello"}`, want: `"he"`},
	{expr: `s.$substringAfter("l")`, data: `{"s":"hello"}`, want: `"lo"`},
	{expr: `s.$lowercase()`, data: `{"s":"HeLLo"}`, want: `"hello"`},
	{expr: `s.$uppercase()`, data: `{"s":"hello"}`, want: `"HELLO"`},
	{expr: `s.$length()`, data: `{"s":"hello"}`, want: `5`},
	{expr: `s.$trim()`, data: `{"s":" a  b "}`, want: `"a b"`},
	{expr: `s.$pad(4)`, data: `{"s":"ab"}`, want: `"ab  "`},
	{expr: `s.$pad(-4, "*")`, data: `{"s":"ab"}`, want: `"**ab"`},
	{expr: `s.$match(/l/)`, data: `{"s":"hello"}`, want: `[{"groups":[],"index":2,"match":"l"},{"groups":[],"index":3,"match":"l"}]`},
	{expr: `s.$match(/l/, 1)`, data: `{"s":"hello"}`, want: `{"groups":[],"index":2,"match":"l"}`},
	{expr: `s.$contains("ell")`, data: `{"s":"hello"}`, want: `true`},
	{expr: `s.$contains(/z/)`, data: `{"s":"hello"}`, want: `false`},
	{expr: `s.$replace("l", "L")`, data: `{"s":"hello"}`, want: `"heLLo"`},
	{expr: `s.$replace(/l/, "L", 1)`, data: `{"s":"hello"}`, want: `"heLlo"`},
	{expr: `s.$split("l")`, data: `{"s":"hello"}`, want: `["he","","o"]`},
	{expr: `s.$split(/l/, 1)`, data: `{"s":"hello"}`, want: `["he"]`},
	{expr: `n.$formatNumber("#,##0.0")`, data: `{"n":1234.5}`, want: `"1,234.5"`},
	{expr: `n.$formatBase()`, data: `{"n":255}`, want: `"255"`},
	{expr: `n.$formatBase(16)`, data: `{"n":255}`, want: `"16"`},
	{expr: `n.$formatInteger("w")`, data: `{"n":12}`, want: `"twelve"`},
	{expr: `s.$parseInteger("w")`, data: `{"s":"twelve"}`, want: `12`},
	{expr: `s.$number()`, data: `{"s":"12"}`, want: `12`},
	{expr: `b.$number()`, data: `{"b":true}`, want: `1`},
	{expr: `n.$floor()`, data: `{"n":-1.5}`, want: `-2`},
	{expr: `n.$ceil()`, data: `{"n":-1.5}`, want: `-1`},
	{expr: `n.$round()`, data: `{"n":2.5}`, want: `2`},
	{expr: `n.$round(1)`, data: `{"n":2.25}`, want: `1`},
	{expr: `n.$abs()`, data: `{"n":-1.5}`, want: `1.5`},
	{expr: `n.$sqrt()`, data: `{"n":16}`, want: `4`},
	{expr: `n.$power(2)`, data: `{"n":3}`, want: `9`},
	{expr: `s.$boolean()`, data: `{"s":"x"}`, want: `true`},
	{expr: `s.$not()`, data: `{"s":""}`, want: `true`},
	{expr: `o.$sift(function($v){$v > 1})`, data: `{"o":{"a":1,"b":2}}`, want: `{"b":2}`},
	{expr: `o.$keys()`, data: `{"o":{"a":1,"b":2}}`, want: `["a","b"]`},
	{expr: `o.$lookup("b")`, data: `{"o":{"a":1,"b":2}}`, want: `2`},
	{expr: `o.$spread()`, data: `{"o":{"a":1,"b":2}}`, want: `[{"a":1},{"b":2}]`},
	{expr: `o.$each(function($v, $k){$k & $v})`, data: `{"o":{"a":1,"b":2}}`, want: `["a1","b2"]`},
	{expr: `s.$base64encode()`, data: `{"s":"hello"}`, want: `"aGVsbG8="`},
	{expr: `s.$base64decode()`, data: `{"s":"aGVsbG8="}`, want: `"hello"`},
	{expr: `s.$encodeUrlComponent()`, data: `{"s":"a b&c"}`, want: `"a%20b%26c"`},
	{expr: `s.$encodeUrl()`, data: `{"s":"a b&c"}`, want: `"a%20b&c"`},
	{expr: `s.$decodeUrlComponent()`, data: `{"s":"a%20b%26c"}`, want: `"a b&c"`},
	{expr: `s.$decodeUrl()`, data: `{"s":"a%20b"}`, want: `"a b"`},
	{expr: `s.$toMillis()`, data: `{"s":"1970-01-01T00:00:01.000Z"}`, want: `1000`},
	{expr: `n.$fromMillis()`, data: `{"n":1000}`, want: `"1970-01-01T00:00:01.000Z"`},
	{expr: `n.$fromMillis("[Y]-[M01]")`, data: `{"n":0}`, want: `"1970-01"`},
	{expr: `n.$fromMillis("[H01]", "+0100")`, data: `{"n":0}`, want: `"01"`},
	// The context of a whole-input call, a block and a filter.
	{expr: `$keys()`, data: `{"a":1,"b":2}`, want: `["a","b"]`},
	{expr: `$keys()`, data: `[{"a":1},{"b":2}]`, want: `["a","b"]`},
	{expr: `$keys()`, data: `[[{"a":1}],{"b":2}]`, want: `["a","b"]`},
	{expr: `$keys()`, data: `[[{"a":1},[{"c":3}]],{"b":2}]`, want: `["a","c","b"]`},
	{expr: `$keys([[],[[{"z":1,"a":2}]],{"a":3,"y":4}])`, want: `["z","a","y"]`},
	{expr: `$keys([1,"x",[null]])`, want: undefined},
	{expr: `[[{"a":1}],{"b":2}].$keys()`, want: `["a","b"]`},
	// Arrays shared at every level are walked once each, not 2^40 times.
	{expr: sharedArrays(40) + "$keys($a40))", want: `"k"`},
	{expr: `$length()`, data: `"abc"`, want: `3`},
	{expr: `o.$keys().$uppercase()`, data: `{"o":{"a":1,"b":2}}`, want: `["A","B"]`},
	{expr: `o.($keys())`, data: `{"o":{"a":1}}`, want: `"a"`},
	{expr: `a.$length()`, data: `{"a":["a","bb"]}`, want: `[1,2]`},
	{expr: `a[$length() > 1]`, data: `{"a":["a","bb"]}`, want: `"bb"`},
	{expr: `s.{"k": $uppercase()}`, data: `{"s":"ab"}`, want: `{"k":"AB"}`},
	// x ~> f(...) passes x as the first argument; HOF callbacks take no context.
	{expr: `"abc" ~> $substring(1)`, want: `"bc"`},
	{expr: `5 ~> $pad()`, data: `"ab"`, want: `"ab   "`},
	{expr: `"abc" ~> $uppercase("x")`, code: "T0410"},
	{expr: `$map(["a", "b"], $uppercase)`, want: `["A","B"]`},
	// A bare x ~> $f has a null context.
	{expr: `"abc" ~> $uppercase`, want: `"ABC"`},
	{expr: `"k" ~> $lookup`, data: `{"k":1}`, want: undefined},
	{expr: `2 ~> $power`, data: `3`, code: "T0411"},
	{expr: `"a" ~> $contains`, data: `"abc"`, code: "T0411"},
	// With no input the context argument is undefined.
	{expr: `$sift()`, want: undefined},
	{expr: `$boolean()`, want: undefined},
	{expr: `$not()`, want: undefined},
	{expr: `$round()`, want: undefined},
	{expr: `$substring(5, 1)`, want: undefined},
	{expr: `$contains("a")`, want: undefined},
	{expr: `$split("a")`, want: undefined},
	{expr: `$split("a,b", 5)`, want: undefined},
	{expr: `$replace("abc", "b")`, want: undefined},
	{expr: `$formatBase()`, want: undefined},
	{expr: `$parseInteger("1")`, want: undefined},
	{expr: `$fromMillis("x")`, want: undefined},
	{expr: `o.$uppercase()`, data: `{"o":{}}`, code: "T0411"},
	// An undefined argument is still an argument, not a missing one.
	{expr: `x.$formatBase(nothing)`, data: `{"x":5}`, want: undefined},
	{expr: `x.$number(nothing)`, data: `{"x":"5"}`, want: undefined},
	{expr: `x.$fromMillis(nothing)`, data: `{"x":0}`, want: undefined},
	{expr: `x.$string(nothing)`, data: `{"x":"a"}`, want: undefined},
	{expr: `$pad(nothing)`, data: `"ab"`, want: `"ab"`},
	{expr: `$pad(nothing, "x")`, data: `"ab"`, want: `"ab"`},
	{expr: `$pad("ab", nothing)`, want: `"ab"`},
	// A context value of the wrong type raises T0411.
	{expr: `n.$length()`, data: `{"n":1}`, code: "T0411"},
	{expr: `s.$abs()`, data: `{"s":"x"}`, code: "T0411"},
	{expr: `o.$number()`, data: `{"o":{}}`, code: "T0411"},
	{expr: `s.$keys()`, data: `{"s":"x"}`, want: undefined},
	{expr: `n.$each(function($v){$v})`, data: `{"n":1}`, code: "T0411"},
	{expr: `$contains("a")`, data: `["a"]`, code: "T0411"},
	{expr: `$abs()`, data: `{}`, code: "T0411"},
	{expr: `$power(2)`, data: `{}`, code: "T0411"},
	{expr: `s.$fromMillis("x")`, data: `{"s":"a"}`, code: "T0411"},
}

func TestBuiltinContextArgument(t *testing.T) {
	runExprCases(t, builtinContextCases)
}

// functionContextCases cover the context of calls in and to functions, which
// jsonata-js's apply sets: a lambda body sees its definition's context, a
// function argument is applied with a null context and as many arguments as
// it declares, and a tail call takes the context of the call that entered
// the lambda.
var functionContextCases = []exprCase{
	// A lambda body evaluates against its definition's context.
	{expr: `($f := function($x){$keys($)}; a.$f(1))`, data: `{"a":{"b":1}}`, want: `"a"`},
	{expr: `($f := function($x){[$keys()]}; a.$f(1))`, data: `{"a":{"b":1}}`, want: `["a"]`},
	{expr: `a.(function($x){$x})()`, data: `{"a":5}`, want: undefined},
	{expr: `a.(function($x,$y){[$x,$y]})(6)`, data: `{"a":5}`, want: `[6]`},
	{expr: `a.(function(){$})()`, data: `{"a":5}`, want: `5`},
	{expr: `($f := function(){$}; a.$f())`, data: `{"a":5}`, want: `{"a":5}`},
	{expr: `($f := function($x){$}; a.$f(1))`, data: `{"a":5}`, want: `{"a":5}`},
	{expr: `a.uppercase()`, data: `{"a":"x"}`, code: "T1005"},
	// A lambda's '-' parameter takes the call's context.
	{expr: `(function($x)<s->{$uppercase($x)})()`, data: `"ab"`, want: `"AB"`},
	{expr: `FirstName.function($str, $prefix)<s-s>{$prefix & $str}("Hello ")`, data: `{"FirstName":"Fred"}`, want: `"Hello Fred"`},
	{expr: `$map([1], function($x)<s->{$x})`, data: `"zz"`, code: "T0410"},
	{expr: `($f := function($s)<s->{$s}; $map([1], function($v){$f()}))`, data: `"zz"`, code: "T0411"},
	{expr: `($f := function($s)<s->{$s}; $map([1], function($v){[$f()]}))`, data: `"zz"`, want: `["zz"]`},
	{expr: `(function($x,$y)<s-s->{$x&$y})()`, data: `"q"`, want: `"qq"`},
	{expr: `(function($a,$b,$c,$d,$e,$f,$g,$h,$i)<nnnnnnnns->{$i})(1,2,3,4,5,6,7,8)`, data: `"q"`, want: `"q"`},
	{expr: `(function($x)<s+->{$x})()`, data: `"q"`, code: "T0410"},
	{expr: `(function($x)<s+>{$x})()`, code: "T0410"},
	{expr: `(function($x,$y)<n+s>{$y})(1,2,"a")`, want: `2`},
	{expr: `(function($a,$b)<n+s>{[$a,$b]})(1)`, code: "T0410"},
	{expr: `(function($a,$b)<n+n>{$b})(1)`, code: "T0410"},
	// The context fills a '-' parameter as it is, without coercion.
	{expr: `(function($a)<a<n>->{$a})()`, data: `5`, want: `5`},
	{expr: `(function($s)<s->{"x"})()`, want: `"x"`},
	// A tail call in a callback has a null context; other calls have $.
	{expr: `$map([0], function($v){$v ?: $string()})`, data: `"zz"`, want: `"null"`},
	{expr: `$map(["a"], function($v){nothing ?? $string()})`, data: `"zz"`, want: `"null"`},
	{expr: `$map(["a"], function($v){$string()})`, data: `"zz"`, want: `"null"`},
	{expr: `$map(["a"], function($v){$string() & ""})`, data: `"zz"`, want: `"zz"`},
	{expr: `$map(["a"], function($v){($string())})`, data: `"zz"`, want: `"null"`},
	{expr: `$map(["a"], function($v){$v ? $string() : 1})`, data: `"zz"`, want: `"null"`},
	{expr: `$filter([1], function($v){$boolean()})`, data: `"zz"`, want: undefined},
	{expr: `o.$map([1,2], function($v){$string()})`, data: `{"o":{"a":1}}`, want: `["null","null"]`},
	{expr: `$map([{"a":1}], function($v){$v.$keys()})`, want: `"a"`},
	// A callback's body sees the context where the lambda was defined.
	{expr: `o.$map([1,2], function($v){$})`, data: `{"o":{"a":1}}`, want: `[{"a":1},{"a":1}]`},
	{expr: `($f := function($v){$}; o.$map([1], $f))`, data: `{"o":{"a":1}}`, want: `{"o":{"a":1}}`},
	{expr: `o.$map([1], function($v){$map([2], function($w){$})})`, data: `{"o":{"a":1}}`, want: `{"a":1}`},
	{expr: `o.$filter([1,2], function($v){$.a = 1})`, data: `{"o":{"a":1}}`, want: `[1,2]`},
	{expr: `o.$reduce([1,2], function($a,$v){$a + $.a})`, data: `{"o":{"a":10}}`, want: `11`},
	{expr: `o.$sort([2,1], function($a,$b){$.a > 0 and $a > $b})`, data: `{"o":{"a":1}}`, want: `[1,2]`},
	{expr: `$map(["a"], function($v){$uppercase()})`, data: `"zz"`, code: "T0411"},
	{expr: `($f := function(){$uppercase()}; $f())`, data: `"zz"`, want: `"ZZ"`},
	{expr: `($f := function($n){$n > 0 ? $f($n-1) : $string()}; $f(3))`, data: `"zz"`, want: `"zz"`},
	{expr: `($f := function($n){$n > 0 ? $f($n-1) : $string()}; $map([3], $f))`, data: `"zz"`, want: `"null"`},
	{expr: `$map([1], function($v){$string()#$i})`, data: `"zz"`, want: `"zz"`},
	{expr: `$map([1], function($v){$string()@$x})`, data: `"zz"`, want: `"null"`},
	{expr: `$map([1], function($v){$string()[0]})`, data: `"zz"`, want: `"zz"`},
	// A bind is not a tail call, though gnata still trampolines it.
	{expr: `($f := function($n){$n = 0 ? 0 : ($x := $f($n-1))}; $f(300))`, want: `0`},
	{expr: `$map([1], function($v){($x := $string(); $x)})`, data: `"zz"`, want: `"zz"`},
	{expr: `($f := function(){$x := $string()}; $map([1], $f))`, data: `"zz"`, want: `"zz"`},
	{
		expr: `s.($g := function(){$string()}; $f := function(){($x := $g())}; $$.a.$f())`,
		data: `{"s":"outer","a":"entering"}`, want: `"outer"`,
	},
	{expr: `($g := function(){$string()}; $map([1], function($v){($x := $g())}))`, data: `"zz"`, want: `"zz"`},
	{expr: `($g := function($v)<s->{$v}; $map([1], function($v){($x := $g())}))`, data: `"zz"`, want: `"zz"`},
	{expr: `($g := function($s)<x->{$s}; $f := function(){($x := $g())}; a.$f())`, data: `{"a":"A"}`, want: `{"a":"A"}`},
	// A function passed to a lambda is applied with a null context.
	{expr: `($g := function($f){$f()}; $g($string))`, data: `"zz"`, want: `"null"`},
	{expr: `($g := function($f){$f() & ""}; $g($string))`, data: `"zz"`, want: `"null"`},
	{expr: `($g := function($f){a.$f()}; $g($string))`, data: `{"a":"in"}`, want: `"null"`},
	{expr: `($g := function($f){$f()}; $g(function($x)<s->{$x}))`, data: `"zz"`, code: "T0411"},
	{expr: `($loop := function($f, $n){$n = 0 ? 0 : $f($f, $n-1)}; $loop($loop, 300))`, want: `0`},
	{expr: `($g := function($f){$map([1,2], $f)}; $g($power))`, want: `[1,2]`},
	{expr: `($g := function($f){$type($f)}; $g($string))`, want: `"function"`},
	{expr: `($g := function($f){$f}; $g($string)("x"))`, want: `"x"`},
	// A regex argument is not a function.
	{expr: `($g := function($re){$match("abc", $re).match}; $g(/b/))`, want: `"b"`},
	{expr: `($g := function($re){$contains("abc", $re)}; $g(/b/))`, want: `true`},
	{expr: `($g := function($re){$split("abc", $re)}; $g(/b/))`, want: `["a","c"]`},
	// A tail call passes function arguments as they are.
	{expr: `($g := function($f){$f() & ""}; $k := function(){$g($string)}; $k())`, data: `"zz"`, want: `"zz"`},
	{expr: `($g := function($f){$f()}; $k := function(){$g($string)}; $k())`, data: `"zz"`, want: `"zz"`},
	{expr: `($g := function($f){$f() & ""}; $k := function($x){$x ? $g($string) : 0}; $k(1))`, data: `"zz"`, want: `"zz"`},
	// Partial applications, compositions and function arguments wrap them too.
	{expr: `($g := function($f){$f() & ""}; $p := $g(?); $p($string))`, data: `"zz"`, want: `"null"`},
	{expr: `($g := function($f){$f() & ""}; $c := $g ~> $uppercase; $c($string))`, data: `"zz"`, want: `"NULL"`},
	{expr: `($g := function($f){$f() & ""}; $w := function($x){$x}; $h := $w($g); $h($string) & "")`, data: `"zz"`, want: `"null"`},
	{expr: `($h := function($g){ [$g()] }; $map([1], function($v){ $h($uppercase) }))`, data: `"in"`, want: `["IN"]`},
	{expr: `($h := function($g){ [$g()] }; [$h($uppercase)])`, data: `"in"`, code: "T0411"},
	// So does x ~> f(...), but not for the piped value.
	{expr: `($h := function($x,$g){$g()}; "x" ~> $h($uppercase))`, data: `"abc"`, code: "T0411"},
	{expr: `($h := function($x,$g){[$g()]}; "x" ~> $h($uppercase))`, data: `"abc"`, code: "T0411"},
	{expr: `($l := function($x,$g){[$x,$g()]}; "abc" ~> $l($string))`, data: `"zz"`, want: `["abc","null"]`},
	{expr: `($h := function($x,$g){$g()}; "x" ~> $h($string))`, data: `{"a":1}`, want: `"null"`},
	{expr: `($l := function($x,$g){[$x,$g()]}; $string ~> $l("abc"))`, data: `"zz"`, code: "T1006"},
	// A builtin callback takes as many arguments as it declares.
	{expr: `$map([1,2], $power)`, want: `[1,2]`},
	{expr: `$map(["ab","cd"], $substringBefore)`, code: "T0410"},
	{expr: `$map([1], $string)`, want: `"1"`},
	{expr: `$map(["a","b"], $uppercase)`, want: `["A","B"]`},
	{expr: `$reduce([1,2], $sum)`, code: "D3050"},
	{expr: `$reduce([1,2,3], $append)`, want: `[1,2,3]`},
	{expr: `$each({"a":1}, $string)`, want: `"1"`},
	{expr: `$sift({"a":1,"b":2}, function($v){$v>1})`, want: `{"b":2}`},
	{expr: `$filter([0,1,2], $boolean)`, want: `[1,2]`},
	// Partial application and composition.
	{expr: `$reduce([1,2,3], $append(?, ?))`, want: `[1,2,3]`},
	{expr: `($f := function($a,$b){$a+$b}; $reduce([1,2,3], $f(?, ?)))`, want: `6`},
	{expr: `$each({"a":1}, $append(?, ?))`, want: `[1,"a"]`},
	{expr: `$reduce([1,2], $uppercase ~> $lowercase)`, code: "D3050"},
	{expr: `($string ~> $uppercase)()`, want: undefined},
	{expr: `($trim ~> $uppercase)()`, want: undefined},
	{expr: `($p := $substring(?, 1); $p("abc"))`, want: `"bc"`},
	// A partial application of a built-in has no context; a lambda's tail
	// call takes the caller's.
	{expr: `($p := $eval(?); s.$p("$"))`, data: `{"s":"hello"}`, want: undefined},
	{expr: `($p := $eval(?); $p("$"))`, data: `{"s":"hello"}`, want: undefined},
	{expr: `($p := $eval("$", ?); s.$p())`, data: `{"s":"hello"}`, want: undefined},
	{expr: `($p := $eval(?); $q := $p(?); s.$q("$"))`, data: `{"s":"hello"}`, want: undefined},
	{expr: `(s ~> $eval("$", ?))`, data: `{"s":"hello"}`, want: `"hello"`},
	{expr: `$eval(?, ?)("$", 5)`, data: `{"s":"hello"}`, want: `5`},
	{expr: `($p := $contains(?); s.$p("l"))`, data: `{"s":"hello"}`, want: undefined},
	{expr: `($p := $sort(?); [arr].$p())`, data: `{"arr":[3,1,2]}`, want: undefined},
	{expr: `($p := $uppercase(?); s.$p())`, data: `{"s":"hello"}`, want: undefined},
	{expr: `($p := $exists(?); s.$p())`, data: `{"s":"hello"}`, want: `false`},
	{expr: `($c := $eval ~> $string; $p := $c(?); s.$p("$"))`, data: `{"s":"hello"}`, want: `"null"`},
	{expr: `($f := function($x){$eval("$")}; $p := $f(?); s.$p(1))`, data: `{"s":"hello"}`, want: `"hello"`},
	{expr: `($f := function($x){[$eval("$")]}; $p := $f(?); s.$p(1))`, data: `{"s":"hello"}`, want: `[{"s":"hello"}]`},
	{expr: `($f := function($x, $y){$eval("$")}; $p := $f(?, ?); $q := $p(1, ?); s.$q(2))`, data: `{"s":"hello"}`, want: `"hello"`},
	{expr: `($f := function($x, $y){$string()}; $p := $f(?, ?); $q := $p(1, ?); s.$q(2))`, data: `{"s":"hello"}`, want: `"hello"`},
	{expr: `($f := function($x){$}; $p := $f(?); s.$p(1))`, data: `{"s":"hello"}`, want: `{"s":"hello"}`},
	// A partial application of a built-in takes the parameters its
	// jsonata-js implementation declares.
	{expr: `$substring(?, ?, ?, ?)("abc", 1, 1, 9)`, want: `"b"`},
	{expr: `$count(?, ?)([1,2], 5)`, want: `2`},
	{expr: `$append(?)([1])`, want: `[1]`},
	{expr: `$uppercase(?, 1)("ab")`, want: `"AB"`},
	{expr: `$uppercase("x", ?)("ab")`, want: `"X"`},
	{expr: `$reduce(?, function($a,$b){$a+$b})([1,2])`, want: `3`},
	{expr: `$reduce([1,2], function($a,$b){$a+$b}, nothing)`, want: `3`},
	{expr: `$map(["ab","cd"], $substring(?, ?))`, want: `["ab","d"]`},
	{expr: `$zip(?)([1,2])`, want: `[[1],[2]]`},
	{expr: `($trim ~> $uppercase)(" a ")`, want: `"A"`},
	{expr: `$map([1,22], $string ~> $length)`, want: `[1,2]`},
	// $substring with an undefined start.
	{expr: `$substring("abc", nothing)`, want: `"abc"`},
	{expr: `$substring("abc", nothing, 2)`, want: `""`},
	{expr: `$substring("abc", 1, nothing)`, want: `"bc"`},
	{expr: `x.$substring(nothing)`, data: `{"x":"abc"}`, want: `"abc"`},
	{expr: `$substring("abc", nothing, "x")`, code: "T0410"},
	// $clone.
	{expr: `$clone(a.[b,c])`, data: `{"a":{"b":1,"c":2}}`, want: `[1,2]`},
	{expr: `($clone := function($x){{"z":1}}; {"a":1} ~> |$|{}|)`, want: `{"z":1}`},
	{expr: `{"f":$uppercase,"g":1} ~> |$|{}|`, want: `{"f":"","g":1}`},
	{expr: `($clone := 5; {"a":1} ~> |$|{}|)`, code: "T2013"},
	{expr: `$clone({"a":[1,{"b":"c"}],"d":null})`, want: `{"a":[1,{"b":"c"}],"d":null}`},
	{expr: `$clone([1,"x",null,true])`, want: `[1,"x",null,true]`},
	{expr: `{"a":1}.$clone()`, want: `{"a":1}`},
	{expr: `$clone({"f":$uppercase,"a":1})`, want: `{"a":1,"f":""}`},
	{expr: `$clone({"a":nothing})`, want: `{}`},
	{expr: `$clone()`, want: undefined},
	{expr: `$clone("a")`, code: "T0410"},
	{expr: `$clone(null)`, code: "T0410"},
}

func TestFunctionContext(t *testing.T) {
	runExprCases(t, functionContextCases)
}

// signatureCases cover jsonata-js's signature validation: the arguments
// match the signature as its backtracking regex does, are passed by
// position, and an error blames the argument jsonata-js does.
var signatureCases = []exprCase{
	{expr: `(function($a,$b,$c)<s-s?s>{[$a,$b,$c]})("x","y")`, data: `"ctx"`, want: `["x","y"]`},
	{expr: `(function($a,$b,$c)<s?s?s>{{"a":$a,"b":$b,"c":$c}})("x","y")`, want: `{"a":"x","b":"y"}`},
	{expr: `(function($a,$b)<s?s>{{"a":$a,"b":$b}})("x")`, want: `{"a":"x"}`},
	{expr: `(function($a,$b)<n?s>{{"a":$a,"b":$b}})("x")`, want: `{"a":"x"}`},
	{expr: `(function($a,$b)<a?n>{{"a":$a,"b":$b}})(5)`, want: `{"a":5}`},
	{expr: `(function($a,$b,$c)<n?s?n>{{"a":$a,"b":$b,"c":$c}})(1,2)`, want: `{"a":1,"b":2}`},
	{expr: `(function($a)<n:n>{1})(nothing)`, want: `1`},
	{expr: `(function($a,$b)<nn>{[$a,$b]})(nothing, 1)`, want: `[1]`},
	{expr: `(function($a,$b)<a<n>s>{{"a":$a,"b":$b}})(nothing,"x")`, want: `{"b":"x"}`},
	{expr: `(function($a)<f>{1})(nothing)`, code: "T0410: argument 1"},
	{expr: `(function($a)<s>{1})()`, code: "T0410: argument 1"},
	{expr: `(function($a,$b)<ns>{[$a,$b]})("x",1)`, code: "T0410: argument 1"},
	{expr: `(function($a,$b)<ns>{[$a,$b]})(1,1)`, code: "T0410: argument 2"},
	{expr: `(function($a,$b)<ns>{[$a,$b]})(1,"x",3)`, code: "T0410: argument 3"},
	{expr: `(function($a,$b)<ns?>{[$a,$b]})(1,2)`, code: "T0410: argument 2"},
	{expr: `(function($a,$b)<(sn)n>{[$a,$b]})("a")`, code: "T0410: argument 2"},
	{expr: `(function($a,$b)<s?a>{[$a,$b]})(nothing)`, want: `[]`},
	{expr: `(function($a,$b)<s?a>{{"a":$a,"b":$b}})("x")`, want: `{"a":"x","b":[null]}`},
	{expr: `(function($a,$b,$c)<s?a?n>{{"a":$a,"b":$b,"c":$c}})(nothing,1)`, want: `{"b":1}`},
	{expr: `(function($a,$b)<n?-n?>{[$a,$b]})(1)`, data: `5`, want: `[5,1]`},
	{expr: `(function($a,$b)<n??n?>{[$a,$b]})(1)`, data: `5`, want: `[1]`},
	{expr: `(function($a,$b,$c,$d)<x??>{[$a,$b]})({"k":1},"ab")`, code: "T0410: argument 1"},
	{expr: `(function($a,$b)<a<s>?->{[$a,$b]})(1,1)`, code: "T0410: argument 1"},
	{expr: `(function($a,$b)<n+-n+>{[$a,$b]})(1,2,3)`, want: `[1,2]`},
	{expr: `(function($a)<s?->{$a})()`, data: `5`, want: `5`},
	{expr: `(function($a,$b,$c)<n-j-o?->{{"p0":$a,"p1":$b,"p2":$c}})("ab")`, data: `5`, want: `{"p0":5,"p1":"ab","p2":5}`},
	{expr: `(function($a)<s-?>{$a})()`, data: `5`, code: "T0411"},
	{expr: `(function($a)<a<(sn)>>{$a})([1])`, code: "T0412: argument 1"},
	{expr: `(function($a)<a<(sn)>>{$a})(true)`, code: "T0412: argument 1"},
	{expr: `(function($a)<a<(sn)>>{$a})([])`, want: `[]`},
	{expr: `(function($a,$b)<n+?>{[$a,$b]})(1,2)`, want: `[1,2]`},
	{expr: `(function($a)<n+?>{$a})()`, code: "T0410: argument 1"},
	{expr: `(function($a)<a<n>>{$a})(5)`, want: `[5]`},
	{expr: `(function($a)<a<n>>{$a})("x")`, code: "T0412: argument 1"},
	{expr: `(function($a)<a<n>>{$a})([1,"x"])`, code: "T0412: argument 1"},
	{expr: `(function($a)<a<n>>{$a})([[1]])`, code: "T0412: argument 1"},
	{expr: `(function($a)<a<a>>{$a})([[1],2])`, code: "T0412: argument 1"},
	{expr: `(function($a)<a<a<n>>>{$a})([["x"]])`, want: `[["x"]]`},
	{expr: `(function($a)<a<o>>{$a})([{"a":1},null])`, code: "T0412: argument 1"},
	{expr: `(function($a)<a<l>>{$a})([null])`, want: `[null]`},
	{expr: `(function($a,$b)<a<n>+>{[$a,$b]})(1,2)`, code: "T0412: argument 1"},
	{expr: `(function($a,$b)<a+>{[$a,$b]})(1,[2])`, want: `[1,2]`},
	{expr: `(function($a)<(as)>{$a})(5)`, code: "T0410: argument 1"},
	{expr: `(function($a,$b)<s-n>{[$a,$b]})("x","y")`, data: `"c"`, code: "T0410: argument 2"},
	{expr: `(function($a,$b)<s-n>{[$a,$b]})(1)`, data: `5`, code: "T0411"},
	{expr: `(function($a,$b,$c)<s?n-s>{[$a,$b,$c]})("x")`, data: `4`, want: `["x",4]`},
	{expr: `(function($a,$b)<j-n>{[$a,$b]})(5)`, want: `[5]`},
	{expr: `"x" ~> $substring()`, code: "T0410: argument 2"},
	{expr: `$substring("x")`, code: "T0410: argument 2"},
	{expr: `"x" ~> $substring(0)`, want: `"x"`},
	{expr: `$abs(1,2)`, code: "T0410: argument 2"},
	{expr: `$substringBefore("abc", nothing)`, want: `"abc"`},
	{expr: `$substringBefore("xundefinedy", nothing)`, want: `"x"`},
	{expr: `$substringAfter("abc", nothing)`, want: `"abc"`},
	{expr: `$sift(/a/)`, want: undefined},
	// Builtins as higher-order function callbacks, as in jsonata-js.
	{expr: `$filter([""], $boolean)`, want: undefined},
	{expr: `$filter([1,2], $not)`, want: undefined},
	{expr: `$map(["a"], $keys)`, want: `[]`},
	{expr: `$map([], $keys)`, want: undefined},
	{expr: `$each({"a":1,"b":2}, $keys)`, want: `[[],[]]`},
	{expr: `$sift({"a":1}, $keys)`, want: undefined},
	{expr: `$sort(["b","a"], $boolean)`, code: "T0410: argument 2"},
	{expr: `$sift(/a/)`, data: `"str"`, code: "T0411"},
	{expr: `(function($o)<o-f?>{$o})(/a/)`, data: `{"x":1}`, want: `{"x":1}`},
	{expr: `$sift(?)(function($v){true})`, data: `{"a":1}`, want: undefined},
	{expr: `$each(?)(function($v){$v})`, data: `{"a":1}`, want: undefined},
	{expr: `$uppercase(5)`, code: "T0410: argument 1"},
	{expr: `$uppercase()`, data: `5`, code: "T0411"},
	{expr: `$uppercase("a","b")`, code: "T0410: argument 2"},
	{expr: `$lowercase(nothing)`, want: undefined},
	{expr: `$map(["x"], function($v)<n:n>{$v})`, code: "T0410: argument 1"},
	{expr: `($f := function($a,$b)<ns>{$a&$b}; $f(?,"x"))("y")`, want: `"yx"`},
}

func TestFunctionSignatures(t *testing.T) {
	runExprCases(t, signatureCases)
}

// sharedArrays starts a block binding $a0 to $a{levels}, each holding the
// one before it twice.
func sharedArrays(levels int) string {
	var b strings.Builder
	b.WriteString(`($a0 := [[{"k":1}]]; `)
	for i := 1; i <= levels; i++ {
		fmt.Fprintf(&b, "$a%d := [[$a%d],[$a%d]]; ", i, i-1, i-1)
	}
	return b.String()
}

// A regex is a function, so object parameters reject it, while a Go map
// shaped like a regex is data, which they accept. Expectations are from
// jsonata-js 2.2.2.
func TestRegexShapedDataMaps(t *testing.T) {
	data := map[string]any{"a": map[string]any{"pattern": "b", "flags": ""}}
	testCases := []struct {
		expr string
		want any
		code string
	}{
		{expr: `$each(a, function($v,$k){$k})`, want: []any{"flags", "pattern"}},
		{expr: `$count($keys($sift(a, function($v){true})))`, want: float64(2)},
		{expr: `$clone(a).pattern`, want: "b"},
		{expr: `$sift(/a/, function($v){true})`, code: "T0410: argument 2"},
		{expr: `$each(/a/, function($v){$v})`, code: "T0410: argument 2"},
	}
	for _, tC := range testCases {
		t.Run(tC.expr, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, err := e.Eval(context.Background(), data)
			if tC.code != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tC.code) {
					t.Fatalf("got error %v, want %s", err, tC.code)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}
