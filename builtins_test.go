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
	got, err := evalDecoded(c.expr, c.data)
	if c.code != "" {
		if err == nil || !strings.Contains(err.Error(), c.code) {
			t.Fatalf("%s: want error %s, got %v (err %v)", c.expr, c.code, render(t, got), err)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", c.expr, err)
	}
	if rendered := render(t, got); rendered != c.want {
		t.Fatalf("%s:\n got: %s\nwant: %s", c.expr, rendered, c.want)
	}
}

func runExprCases(t *testing.T, cases []exprCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.expr, c.run)
	}
}

func evalDecoded(expr, data string) (any, error) {
	e, err := gnata.Compile(expr)
	if err != nil {
		return nil, err
	}
	var input any
	if data != "" {
		if input, err = gnata.DecodeJSON(json.RawMessage(data)); err != nil {
			return nil, err
		}
	}
	return e.Eval(context.Background(), input)
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
	{expr: `$sort(function($a,$b){$a>$b})`, want: undefined},
	{expr: `$sort()`, want: undefined},
	{expr: `$distinct(a.b)`, data: `{"a":[{"b":[1,1,2]},{"b":2}]}`, want: `[1,2]`},
	{expr: `$distinct([[1],[1],[2],null,null])`, want: `[[1],[2],null]`},
	{expr: `$flatten([[1,[2,[3]]]], 1)`, want: `[1,[2,[3]]]`},
	{expr: `$zip([1,2],[3])`, want: `[[1,3]]`},
	{expr: `$zip()`, want: `[]`},
	{expr: `$zip([1,2], undefined)`, want: `[]`},
	{expr: `$reverse(a.b)`, data: `{"a":[{"b":1},{"b":2}]}`, want: `[2,1]`},
	{expr: `$shuffle([1])`, want: `[1]`},
	{expr: `$map([1,2], function($v,$i,$a){$v + $i + $count($a)})`, want: `[3,5]`},
	{expr: `$filter([1,2,3], function($v){$v>1})`, want: `[2,3]`},
	{expr: `[1,2,3] ~> $filter(function($v){$v>1})`, want: `[2,3]`},
	{expr: `$single()`, want: undefined},
	{expr: `$single(5)`, want: `5`},
	{expr: `$reduce([], function($a,$b){$a+$b})`, want: undefined},
	{expr: `$reduce([], function($a,$b){$a+$b}, 7)`, want: `7`},
	{expr: `[1,2,3] ~> $reduce(function($a,$b){$a+$b})`, want: `6`},
	{expr: `$sift({"a":1,"b":2}, function($v,$k,$o){$v>1 and $k="b" and $exists($o.a)})`, want: `{"b":2}`},
	{expr: `{"a":1,"b":2} ~> $sift(function($v){$v>1})`, want: `{"b":2}`},
	{expr: `$merge([])`, want: `{}`},
	{expr: `$merge()`, want: undefined},
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
	// Code point positions, unlike jsonata-js (README known difference #12).
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

func TestBuiltinResults(t *testing.T) {
	t.Run("array and object", func(t *testing.T) { runExprCases(t, arrayAndObjectBuiltinCases) })
	t.Run("numeric", func(t *testing.T) { runExprCases(t, numericBuiltinCases) })
	t.Run("string", func(t *testing.T) { runExprCases(t, stringBuiltinCases) })
}

var builtinArgumentErrorCases = []exprCase{
	{expr: `$append([1])`, code: "D3006"},
	{expr: `$sort([3,1,2], function($a,$b){$error("boom")})`, code: "D3137"},
	{expr: `$sort([1,"a"])`, code: "D3070"},
	{expr: `$flatten([1,[2]], "x")`, code: "T0410"},
	{expr: `$map()`, code: "D3006"},
	{expr: `$filter()`, code: "D3006"},
	{expr: `$filter([1,2,3], function($v){$error("x")})`, code: "D3137"},
	{expr: `$single([1,2,3], function($v){$v>1})`, code: "D3138"},
	{expr: `$single([1,2,3], function($v){$v>5})`, code: "D3139"},
	{expr: `$single([1,2,3], function($v){$error("x")})`, code: "D3137"},
	{expr: `$reduce()`, code: "D3006"},
	{expr: `$reduce([1,2], function($a,$b){$error("x")})`, code: "D3137"},
	{expr: `$assert()`, code: "T0410"},
	{expr: `$assert(true, "a", "b")`, code: "T0410"},
	{expr: `$assert(false)`, code: "D3141"},
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
	{expr: `$eval()`, code: "D3006"},
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
