package gnata_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	c.runDecoded(t, decodeOrdered)
}

// runDecoded runs the case with data decoded by decode.
func (c exprCase) runDecoded(t *testing.T, decode func(data string) (any, error)) {
	t.Helper()
	got, err := evalWith(c.expr, c.data, decode)
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
	{expr: `$each()`, code: "D3006"},
	{expr: `$each(5, function($v){$v})`, code: "T0410"},
	{expr: `$sift()`, code: "D3006"},
	{expr: `$sift(null, function($v){$v})`, code: "T0410"},
	{expr: `$sift(5, function($v){$v})`, code: "T0410"},
	{expr: `$sift({"a":1}, function($v){$error("x")})`, code: "D3137"},
	{expr: `$merge(5)`, code: "T0412"},
	{expr: `$merge([1])`, code: "T0412"},
	{expr: `$error("a","b")`, code: "T0410"},
	{expr: `$lookup({"a":1})`, code: "D3006"},
	{expr: `$lookup({"a":1}, 5)`, code: "T0410"},
	{expr: `$boolean()`, code: "D3006"},
	{expr: `$not()`, code: "D3006"},
	{expr: `$number("zz")`, code: "D3030"},
	{expr: `$number(a)`, data: `{"a":1e999}`, code: "D3030"},
	{expr: `$round()`, code: "T0410"},
	{expr: `$sum(1,2)`, code: "T0410"},
	{expr: `$sum("a")`, code: "T0412"},
	{expr: `$max("a")`, code: "T0412"},
	{expr: `$min()`, code: "T0410"},
	{expr: `$min("a")`, code: "T0412"},
	{expr: `$average()`, code: "T0410"},
	{expr: `$average("a")`, code: "T0412"},
	{expr: `$sqrt(-1)`, code: "D3060"},
	{expr: `$string(1, true, 3)`, code: "T0410"},
	{expr: `$string(1, $string)`, code: "D3011"},
	{expr: `$string(1, 5)`, code: "T0410"},
	{expr: `$string(1/0)`, code: "D3001"},
	{expr: `$substring("abc")`, code: "D3006"},
	{expr: `$substring("abc", 1, 1, 1)`, code: "T0410"},
	{expr: `$substring(5, 1)`, code: "T0410"},
	{expr: `$substring("abc", "x")`, code: "T0410"},
	{expr: `$substring("abc", 1, "x")`, code: "T0410"},
	{expr: `$substringBefore("a-b", "-", 1)`, code: "T0410"},
	{expr: `$substringBefore()`, code: "T0411"},
	{expr: `$uppercase(5)`, code: "T0410"},
	{expr: `$lowercase(5)`, code: "T0410"},
	{expr: `$trim(5)`, code: "T0410"},
	{expr: `$pad("a")`, code: "D3006"},
	{expr: `$pad(5, 3)`, code: "T0410"},
	{expr: `$pad("a", "x")`, code: "T0410"},
	{expr: `$pad("a", 1e9)`, code: "D3010"},
	{expr: `$pad("a", 3, 5)`, code: "T0410"},
	{expr: `$contains("a")`, code: "D3006"},
	{expr: `$contains(5, "b")`, code: "T0410"},
	{expr: `$contains("abc", 5)`, code: "T0410"},
	{expr: `$contains("abc", /a{2,1}/)`, code: "D3137"},
	{expr: `$split()`, code: "D3006"},
	{expr: `$split(5, ",")`, code: "T0410"},
	{expr: `$split("a")`, code: "D3006"},
	{expr: `$split("a,b", ",", "x")`, code: "T0410"},
	{expr: `$split("a,b", ",", -1)`, code: "D3020"},
	{expr: `$split("a,b", /a{2,1}/)`, code: "D3137"},
	{expr: `$split("a,b", $string)`, code: "T1010"},
	{expr: `$split("a,b", 5)`, code: "T0410"},
	{expr: `$match()`, code: "D3006"},
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
	{expr: `$replace("abc", "b")`, code: "T0410"},
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
	{expr: `$formatBase()`, code: "D3006"},
	{expr: `$formatBase("x")`, code: "T0410"},
	{expr: `$formatInteger(1)`, code: "D3006"},
	{expr: `$formatInteger("x", "1")`, code: "T0410"},
	{expr: `$formatInteger(1, 5)`, code: "T0410"},
	{expr: `$formatInteger(1e300, "1")`, code: "D3137"},
	{expr: `$formatInteger(5, "1١")`, code: "D3131"},
	{expr: `$formatInteger(5, "١#1")`, code: "D3131"},
	{expr: `$formatInteger(1234, "0١")`, code: "D3131"},
	{expr: `$parseInteger("1")`, code: "D3006"},
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
