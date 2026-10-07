package gnata_test

import "testing"

// loneArrayCases pin how jsonata-js 2.2.2 evaluateStep returns a field
// step's result: a last step whose contexts yield one array value returns
// that array as is, and a nested array context's lookup is one sequence,
// flattened at any depth and collapsed once (kept with []).
var loneArrayCases = []exprCase{
	{expr: `w1.c[]`, data: `{"w1":[{"c":[[1]]}]}`, want: `[[1]]`},
	{expr: `w1.c`, data: `{"w1":[{"c":[[1]]}]}`, want: `[[1]]`},
	{expr: `w1[].c`, data: `{"w1":[{"c":[[1]]}]}`, want: `[[1]]`},
	{expr: `w1.c`, data: `{"w1":[{"c":[1]}]}`, want: `[1]`},
	{expr: `w1.c[]`, data: `{"w1":[{"c":[1]}]}`, want: `[1]`},
	{expr: `$type(w1.c)`, data: `{"w1":[{"c":[1]}]}`, want: `"array"`},
	{expr: `w1.c`, data: `{"w1":[{"c":[1]},{"c":null}]}`, want: `[1,null]`},
	{expr: `w1.c`, data: `{"w1":[[{"c":[[1]]}]]}`, want: `[1]`},
	{expr: `w1.c[]`, data: `{"w1":[[{"c":[[1]]}]]}`, want: `[[1]]`},
	{expr: `w1.(c)`, data: `{"w1":[[{"c":[[1]]}]]}`, want: `[[1]]`},
	{expr: `w1#$i.c`, data: `{"w1":[[{"c":[[1]]}]]}`, want: `1`},
	{expr: `w1#$i.c[]`, data: `{"w1":[[{"c":[[1]]}]]}`, want: `[[1]]`},
	{expr: `w1.c`, data: `{"w1":[[{"c":[[1]]}],[{"c":[[2]]}]]}`, want: `[1,2]`},
	{expr: `w1.c[]`, data: `{"w1":[[{"c":[[1]]}],[{"c":[[2]]}]]}`, want: `[[1],[2]]`},
	{expr: `w1#$i.c`, data: `{"w1":[[{"c":[[1]]}],[{"c":[[2]]}]]}`, want: `[1,2]`},
	{expr: `a.c`, data: `{"a":[[[{"c":[[1]]}],[{"c":[[2]]}]]]}`, want: `[[1],[2]]`},
	{expr: `c`, data: `[[{"c":[[1]]}]]`, want: `[1]`},
	{expr: `a.b`, data: `{"a":[{"b":["z"]},{"other":1}]}`, want: `["z"]`},
	{expr: `a.b = "z"`, data: `{"a":[{"b":["z"]},{"other":1}]}`, want: `false`},
	{expr: `a.b = ["z"]`, data: `{"a":[{"b":["z"]},{"other":1}]}`, want: `true`},
	{expr: `$count(a.b)`, data: `{"a":[{"b":["z"]},{"other":1}]}`, want: `1`},
	{expr: `**.a`, data: `{"v":{"a":[1]}}`, want: `[1]`},
	{expr: `**.a[]`, data: `{"v":{"a":[1]}}`, want: `[1]`},
	{expr: `**.a`, data: `{"v":{"a":[[1]]}}`, want: `[[1]]`},
	{expr: `**.a`, data: `{"v":{"a":[1]},"w":{"a":2}}`, want: `[1,2]`},
	{expr: `$count(**.a)`, data: `{"v":{"a":[1]}}`, want: `1`},
	{expr: `v.**.a`, data: `{"v":{"a":[1]}}`, want: `[1]`},
}

// callKeepArrayCases pin [] on a builtin call: it keeps a one-item result
// sequence as an array, but leaves a value that is not a sequence as is.
var callKeepArrayCases = []exprCase{
	{expr: `$count(n)[]`, data: `{"n":[1,2]}`, want: `2`},
	{expr: `$sum(n)[]`, data: `{"n":[1,2]}`, want: `3`},
	{expr: `$exists(n)[]`, data: `{"n":[1,2]}`, want: `true`},
	{expr: `$string(n)[]`, data: `{"n":[1,2]}`, want: `"[1,2]"`},
	{expr: `$count(n)[0][]`, data: `{"n":[1,2]}`, want: `[2]`},
	{expr: `$count(n)[true][]`, data: `{"n":[1,2]}`, want: `[2]`},
	{expr: `$count(n)#$i[]`, data: `{"n":[1,2]}`, want: `[2]`},
	{expr: `n.$count($)[]`, data: `{"n":[1,2]}`, want: `[1,1]`},
	{expr: `$lookup(o,"x")[]`, data: `{"o":[{"x":1}]}`, want: `[1]`},
	{expr: `$single([1])[]`, want: `1`},
	{expr: `$map([1],function($v){$v})[]`, want: `[1]`},
	{expr: `$filter([1],function($v){true})[]`, want: `[1]`},
}

// groupedCases pin a group on an expression the gjson fast paths would
// otherwise answer: a call, a comparison, its literal, and/or and $not.
var groupedCases = []exprCase{
	{expr: `$count(n){"k":$}`, data: `{"n":[1,2],"a":1,"b":true}`, want: `{"k":2}`},
	{expr: `(a = 1){"k":$}`, data: `{"n":[1,2],"a":1,"b":true}`, want: `{"k":true}`},
	{expr: `a = 1{"k":$}`, data: `{"n":[1,2],"a":1,"b":true}`, want: `false`},
	{expr: `a != 1{"k":$}`, data: `{"n":[1,2],"a":1,"b":true}`, want: `true`},
	{expr: `true{"k":1}`, data: `{"n":[1,2],"a":1,"b":true}`, want: `{"k":1}`},
	{expr: `(a and b){"k":$}`, data: `{"n":[1,2],"a":1,"b":true}`, want: `{"k":true}`},
	{expr: `(a or b){"k":$}`, data: `{"n":[1,2],"a":1,"b":true}`, want: `{"k":true}`},
	{expr: `(a = 1 and b){"k":$}`, data: `{"n":[1,2],"a":1,"b":true}`, want: `{"k":true}`},
	{expr: `b and (a and b){"k":$}`, data: `{"n":[1,2],"a":1,"b":true}`, want: `true`},
	{expr: `$not(b){"k":$}`, data: `{"n":[1,2],"a":1,"b":true}`, want: `{"k":false}`},
	{expr: `$contains(s, "x"{"k":1})`, data: `{"s":"x"}`, code: "T0410"},
}

// tupleStageCases pin [] after a tuple stage's [n]: it filters the tuple
// stream, so it picks one tuple's value, which [] leaves as is.
var tupleStageCases = []exprCase{
	{expr: `w.$filter(a,function($v){$v>1})#$i[0][]`, data: `{"w":[{"a":[1,2,3],"x":[5,6]},{"a":[4],"x":7}]}`, want: `2`},
	{expr: `w.$map(a,function($v){$v*2})#$i[0][]`, data: `{"w":[{"a":[1,2,3],"x":[5,6]},{"a":[4],"x":7}]}`, want: `2`},
	{expr: `w.x#$i[0][]`, data: `{"w":[{"a":[1,2,3],"x":[5,6]},{"a":[4],"x":7}]}`, want: `5`},
	{expr: `w.x#$i[1][]`, data: `{"w":[{"a":[1,2,3],"x":[5,6]},{"a":[4],"x":7}]}`, want: `6`},
	{expr: `w.x#$i[0][].$i`, data: `{"w":[{"a":[1,2,3],"x":[5,6]},{"a":[4],"x":7}]}`, want: `0`},
}

// missingBaseCases pin how jsonata-js filters an undefined base as one
// undefined item: a predicate other than a number literal still runs once,
// so its errors surface, and the item is kept for the next stage when the
// predicate is truthy or selects position 0.
var missingBaseCases = []exprCase{
	{expr: `q[$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, code: `D3137`},
	{expr: `$x[$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, code: `D3137`},
	{expr: `z.q[$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, code: `D3137`},
	{expr: `q[$error("x")][]`, data: `{"z":1,"a":[],"b":[1]}`, code: `D3137`},
	{expr: `$count(q[$error("x")])`, data: `{"z":1,"a":[],"b":[1]}`, code: `D3137`},
	{expr: `q[[0]][$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, code: `D3137`},
	{expr: `q[true][$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, code: `D3137`},
	{expr: `q["a"][$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, code: `D3137`},
	{expr: `$map(q, function($v){1})[$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, code: `D3137`},
	{expr: `z[$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, code: `D3137`},
	{expr: `q[0][$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `q[-1][$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `q[false][$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `q[[1]][$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `q.r[$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `a[$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `a[true][$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `b[false][$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `q#$i[$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `q@$v[$error("x")]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `q^($error("x"))`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `q[true]`, data: `{"z":1,"a":[],"b":[1]}`, want: undefined},
	{expr: `$count(q[true])`, data: `{"z":1,"a":[],"b":[1]}`, want: `0`},
	{expr: `$exists(q[true])`, data: `{"z":1,"a":[],"b":[1]}`, want: `false`},
}

// keptMissingCases pin how a filter's kept undefined item flows on: the
// next step takes it as an undefined context, which a call, a constructor
// or a block still evaluates.
var keptMissingCases = []exprCase{
	{expr: `w[[0]].$count($)#$j[]`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `0`},
	{expr: `w[[0]].$count($)`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `0`},
	{expr: `q[true].$count($)`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `0`},
	{expr: `q[true].$exists($)`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `false`},
	{expr: `q[true].{"k":1}`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `{"k":1}`},
	{expr: `q[true].(1)`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `1`},
	{expr: `q[true].a`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: undefined},
	{expr: `q[0].$count($)`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: undefined},
	{expr: `q[false].$count($)`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: undefined},
	{expr: `z.q[true].$count($)`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `0`},
	{expr: `q[true].$count($)[]`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `[0]`},
	{expr: `$count(q[true].$count($))`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `1`},
	{expr: `q[true][true].$count($)`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `0`},
	{expr: `q[true].$count($)#$j.$string($j)`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `"0"`},
	{expr: `q[true].$count($)[0]#$j`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `0`},
	{expr: `q[true].$$.w2#$j.a`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `[1,2]`},
	{expr: `w2.q[true].$count($)#$j`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `[0,0]`},
	{expr: `q[true].$count($)@$v.$v`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: `0`},
	{expr: `q[true].$count($)@$v`, data: `{"z":1,"w2":[{"a":1},{"a":2}]}`, want: undefined},
}

// containsCases pin $contains on a path through an array: a lone value that
// is neither a string nor an array fails as T0410, as in jsonata-js, on every
// API. Searching an array is gnata's extension, where jsonata-js raises T0410.
var containsCases = []exprCase{
	{expr: `$contains(w.a,"s")`, data: `{"w":[{"a":true}]}`, code: `T0410`},
	{expr: `$contains(w.a,"s")`, data: `{"w":[{"a":null}]}`, code: `T0410`},
	{expr: `$contains(w.a,"s")`, data: `{"w":[{"a":{"c":"s"}},{"x":1}]}`, code: `T0410`},
	{expr: `$contains(w.a.c,"s")`, data: `{"w":["s",{"a":{"c":{},"w":true},"x":1}]}`, code: `T0410`},
	{expr: `$contains(w.a,"s")`, data: `{"w":[[{"a":"s"}]]}`, want: `true`},
	{expr: `$contains(w.a,"s")`, data: `{"w":[{"a":"x"},{"o":1}]}`, want: `false`},
	{expr: `$contains(w.a,"s")`, data: `{"w":[{"a":["s"]},{"o":1}]}`, want: `true`},
	{expr: `$contains(w.a,"s")`, data: `{"w":[{"a":[["s"]]}]}`, want: `false`},
	{expr: `$contains(w.a,"s")`, data: `{"w":[{"a":1},{"a":"s"}]}`, want: `true`},
}

// nullItemCases pin a null item of an array in a path, filter, sort, group,
// membership test or higher-order function: with data decoded by
// encoding/json the item is a nil, which is a JSON null rather than undefined.
var nullItemCases = []exprCase{ //nolint:dupl // distinct cases sharing the exprCase layout
	{expr: `a.$`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `[4,null]`},
	{expr: `a.($)`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `[4,null]`},
	{expr: `a.$[]`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `[4,null]`},
	{expr: `w.a.$`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `[null,2]`},
	{expr: `a.$type($)`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `["number","null"]`},
	{expr: `a.[$]`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `[[4],[null]]`},
	{expr: `a.{"v":$}`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `[{"v":4},{"v":null}]`},
	{expr: `a#$i.$`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `[4,null]`},
	{expr: `$join(a.$string($))`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `"4null"`},
	{expr: `a[$=null]`, data: `{"a":[null,1]}`, want: `null`},
	{expr: `$count(a[$=null])`, data: `{"a":[null,1]}`, want: `1`},
	{expr: `a[$=null][]`, data: `{"a":[null,1]}`, want: `[null]`},
	{expr: `a[$=null]`, data: `{"a":null}`, want: `null`},
	{expr: `a[[1]]`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `null`},
	{expr: `a[true]`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `[4,null]`},
	{expr: `w.a[$=null]`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `null`},
	{expr: `a.$[$=null]`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `null`},
	{expr: `**[$=null]`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `[null,null]`},
	{expr: `**.a`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `[4,null,null,2]`},
	{expr: `a{$string($):1}`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, want: `{"4":1,"null":1}`},
	{expr: `null in a`, data: `{"a":[4,null]}`, want: `true`},
	{expr: `$map(a,function($v){$v})`, data: `{"a":[4,null]}`, want: `[4,null]`},
	{expr: `$filter(a,function($v){$v=null})`, data: `{"a":[4,null]}`, want: `null`},
	{expr: `$reduce(a,function($p,$c){$c})`, data: `{"a":[4,null]}`, want: `null`},
	{expr: `$single(a,function($v){$v=null})`, data: `{"a":[4,null]}`, want: `null`},
	{expr: `a^($)`, data: `{"a":[4,null],"w":[{"a":[null,2]}]}`, code: `T2008`},
}

// wildcardGroupCases pin a group on a wildcard over input that has no
// fields: the group still runs once and builds an object, as in jsonata-js.
var wildcardGroupCases = []exprCase{
	{expr: `*{"k":$}`, data: `1`, want: `{}`},
	{expr: `*{"k":$}`, data: `[]`, want: `{}`},
	{expr: `*{"k":$}`, data: `"s"`, want: `{}`},
	{expr: `*{"k":$}`, data: `{}`, want: `{}`},
	{expr: `*{"k":$}`, data: `{"a":[]}`, want: `{}`},
	{expr: `*{"k":1}`, data: `1`, want: `{"k":1}`},
	{expr: `*{"k":$}`, data: `{"a":1,"b":2}`, want: `{"k":[1,2]}`},
}

// boundKeepArrayCases pin [] after a #$var binding: jsonata-js keeps the
// array only when the binding is on the path's first step, drops it on a
// later step, and still yields undefined for an empty tuple stream.
var boundKeepArrayCases = []exprCase{
	{expr: `x[0]#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `[1]`},
	{expr: `x#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `[1,2]`},
	{expr: `x[0]#$i[0][]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `[1]`},
	{expr: `x#$i[].$`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `[1,2]`},
	{expr: `q#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `[3]`},
	{expr: `w[0]#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `[{"x":[1,2]}]`},
	{expr: `w.x[0]#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `1`},
	{expr: `w.x[1]#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `2`},
	{expr: `w.x[0]#$i[][0]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `1`},
	{expr: `w.x[0]#$i[][0][]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `1`},
	{expr: `w.x[0]#$i[].$i`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `0`},
	{expr: `w#$i.x[0]#$j[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `1`},
	{expr: `w.x[][0]#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `[1]`},
	{expr: `w.x[0][]#$i`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `[1]`},
	{expr: `w.x[0][1]#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: undefined},
	{expr: `z#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: undefined},
	{expr: `q2#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: undefined},
	{expr: `x[$=9]#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: undefined},
	{expr: `w.x[$=9]#$i[]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: undefined},
	{expr: `x#$i[$i>5][]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: undefined},
	{expr: `$count(x[$=9]#$i[])`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `0`},
	{expr: `[x[$=9]#$i[]]`, data: `{"w":{"x":[1,2]},"x":[1,2],"q":3,"q2":[]}`, want: `[]`},
}

// loneArrayContextCases pin a step over several contexts whose results
// flatten to one array: jsonata-js flattens them once, so the next step
// takes that array as one context, and a last step keeps a lone context's
// array, even an empty one. A predicate yields a sequence for every
// context, so one that empties some contexts still flattens the lone array.
var loneArrayContextCases = []exprCase{
	{expr: `w1.c.$`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `[1]`},
	{expr: `w1.c.($)`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `[1]`},
	{expr: `$.w1.c.$`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `[1]`},
	{expr: `w1.c.$.$`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `1`},
	{expr: `w1.c.$[0]`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `1`},
	{expr: `w1.c.$count($)`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `1`},
	{expr: `w1.c^($).$`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `[1]`},
	{expr: `w1.c#$i.$`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `1`},
	{expr: `**.a.$`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `[1]`},
	{expr: `*.a.$`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `[1]`},
	{expr: `**.a[0]`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `1`},
	{expr: `v.**.a[0]`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `1`},
	{expr: `v.a[0]`, data: `{"w1":[{"c":[[1]]}],"v":{"a":[[1]]}}`, want: `[1]`},
	{expr: `w4.c.$`, data: `{"w4":[[{"c":[[1]]}]]}`, want: `1`},
	{expr: `w.(a).$`, data: `{"w":[{"a":[[1]]},{"b":2}]}`, want: `[1]`},
	{expr: `w.(a).$.$`, data: `{"w":[{"a":[[1]]},{"b":2}]}`, want: `1`},
	{expr: `w.(a)^($).$`, data: `{"w":[{"a":[[1]]},{"b":2}]}`, want: `[1]`},
	{expr: `w.a[0]`, data: `{"w":[{"a":[[1]]},{"b":1}],"u":[{"a":[[1]]}],"t":[{"a":[[1]]},{"a":[]}]}`, want: `1`},
	{expr: `w.(a)[0]`, data: `{"w":[{"a":[[1]]},{"b":1}],"u":[{"a":[[1]]}],"t":[{"a":[[1]]},{"a":[]}]}`, want: `1`},
	{expr: `$.w.a[0]`, data: `{"w":[{"a":[[1]]},{"b":1}],"u":[{"a":[[1]]}],"t":[{"a":[[1]]},{"a":[]}]}`, want: `1`},
	{expr: `w.a[0][]`, data: `{"w":[{"a":[[1]]},{"b":1}],"u":[{"a":[[1]]}],"t":[{"a":[[1]]},{"a":[]}]}`, want: `[1]`},
	{expr: `u.a[0]`, data: `{"w":[{"a":[[1]]},{"b":1}],"u":[{"a":[[1]]}],"t":[{"a":[[1]]},{"a":[]}]}`, want: `[1]`},
	{expr: `t.a[0]`, data: `{"w":[{"a":[[1]]},{"b":1}],"u":[{"a":[[1]]}],"t":[{"a":[[1]]},{"a":[]}]}`, want: `1`},
	{expr: `w.a.$`, data: `{"w":{"a":[[]]},"e":[[]],"o":{"a":[1]}}`, want: `[]`},
	{expr: `w.a.($)`, data: `{"w":{"a":[[]]},"e":[[]],"o":{"a":[1]}}`, want: `[]`},
	{expr: `e.$`, data: `{"w":{"a":[[]]},"e":[[]],"o":{"a":[1]}}`, want: `[]`},
	{expr: `o.([])`, data: `{"w":{"a":[[]]},"e":[[]],"o":{"a":[1]}}`, want: `[]`},
	{expr: `o.($x := []; $x)`, data: `{"w":{"a":[[]]},"e":[[]],"o":{"a":[1]}}`, want: `[]`},
	{expr: `($x := []; o.$x)`, data: `{"w":{"a":[[]]},"e":[[]],"o":{"a":[1]}}`, want: `[]`},
	{expr: `o.(a[$>5])`, data: `{"w":{"a":[[]]},"e":[[]],"o":{"a":[1]}}`, want: undefined},
	{expr: `o.$map(a,function($v){[]})[0]`, data: `{"w":{"a":[[]]},"e":[[]],"o":{"a":[1]}}`, want: undefined},
	{expr: `w.c[0]`, data: `{"w":[{"c":[[]]}],"z":[1,2]}`, want: `[]`},
	{expr: `w.(c)[0]`, data: `{"w":[{"c":[[]]}],"z":[1,2]}`, want: `[]`},
	{expr: `w.c[$count($)=0]`, data: `{"w":[{"c":[[]]}],"z":[1,2]}`, want: `[]`},
	{expr: `z.([])`, data: `{"w":[{"c":[[]]}],"z":[1,2]}`, want: undefined},
	{expr: `z.$[$>5]`, data: `{"w":[{"c":[[]]}],"z":[1,2]}`, want: undefined},
	{expr: `z.([])[0]`, data: `{"w":[{"c":[[]]}],"z":[1,2]}`, want: undefined},
}

// rootWildcardCases pin * leading a path over a one-item root array: as
// jsonata-js wraps the root array as one context, * yields its item, which
// the next step takes as its context.
var rootWildcardCases = []exprCase{
	{expr: `*.$`, data: `[1]`, want: `1`},
	{expr: `*.(1)`, data: `[1]`, want: `1`},
	{expr: `*.$string()`, data: `[1]`, want: `"1"`},
	{expr: `*.{"k":$}`, data: `[1]`, want: `{"k":1}`},
	{expr: `*.[$]`, data: `[1]`, want: `[1]`},
	{expr: `*.a`, data: `[1]`, want: undefined},
	{expr: `$.*.$`, data: `[1]`, want: undefined},
	{expr: `*.$`, data: `[{"a":1}]`, want: `{"a":1}`},
	{expr: `*.a`, data: `[{"a":1}]`, want: `1`},
	{expr: `*.*`, data: `[{"a":1}]`, want: `1`},
	{expr: `*[0].$`, data: `[{"a":1}]`, want: `{"a":1}`},
	{expr: `$.*.$`, data: `[{"a":1}]`, want: `1`},
	{expr: `*.$`, data: `[[{"a":1}]]`, want: `{"a":1}`},
	{expr: `*.a`, data: `[{"a":[1]}]`, want: `[1]`},
	{expr: `*.$`, data: `[1,2]`, want: `[1,2]`},
	{expr: `*.$`, data: `[[]]`, want: undefined},
	{expr: `b.*.$`, data: `{"b":[{"x":1}]}`, want: `1`},
}

// emptyFieldCases pin a field holding an empty array: a lone context's own
// value is that array, so $exists sees it, but a lookup over an array
// context, or several contexts' empty arrays, flattens to an empty
// sequence, which jsonata-js collapses to undefined.
var emptyFieldCases = []exprCase{
	{expr: `a`, data: `{"a":[]}`, want: `[]`},
	{expr: `$exists(a)`, data: `{"a":[]}`, want: `true`},
	{expr: `a[]`, data: `{"a":[]}`, want: `[]`},
	{expr: `x.a`, data: `{"x":{"a":[]}}`, want: `[]`},
	{expr: `$exists(x.a)`, data: `{"x":{"a":[]}}`, want: `true`},
	{expr: `x.a`, data: `{"x":[{"a":[]}]}`, want: `[]`},
	{expr: `$exists(x.a)`, data: `{"x":[{"a":[]}]}`, want: `true`},
	{expr: `x.a[]`, data: `{"x":[{"a":[]}]}`, want: `[]`},
	{expr: `x.a`, data: `{"x":[{"a":[]},{"b":1}]}`, want: `[]`},
	{expr: `x.y.a`, data: `{"x":{"y":[{"a":[]}]}}`, want: `[]`},
	{expr: `x.a`, data: `{"x":[{"a":[]},{"a":[]}]}`, want: undefined},
	{expr: `$exists(x.a)`, data: `{"x":[{"a":[]},{"a":[]}]}`, want: `false`},
	{expr: `x.a[]`, data: `{"x":[{"a":[]},{"a":[]}]}`, want: undefined},
	{expr: `$count(x.a)`, data: `{"x":[{"a":[]},{"a":[]}]}`, want: `0`},
	{expr: `$type(x.a)`, data: `{"x":[{"a":[]},{"a":[]}]}`, want: undefined},
	{expr: `x.(a)`, data: `{"x":[{"a":[]},{"a":[]}]}`, want: undefined},
	{expr: `x."a"`, data: `{"x":[{"a":[]},{"a":[]}]}`, want: undefined},
	{expr: `w.a`, data: `{"w":[["s",[{"a":[]}]]]}`, want: undefined},
	{expr: `$exists(w.a)`, data: `{"w":[["s",[{"a":[]}]]]}`, want: `false`},
	{expr: `x.a`, data: `{"x":[[{"a":[]}]]}`, want: undefined},
	{expr: `$exists(x.a)`, data: `{"x":[[{"a":[]}]]}`, want: `false`},
	{expr: `y.a`, data: `{"y":[[{"a":[]},{"a":1}]]}`, want: `1`},
	{expr: `a`, data: `[{"a":[]}]`, want: undefined},
	{expr: `$exists(a)`, data: `[{"a":[]}]`, want: `false`},
	{expr: `$.a`, data: `[{"a":[]}]`, want: `[]`},
	{expr: `a`, data: `[{"a":[]},{"a":[]}]`, want: undefined},
}

// sortOneArrayCases pin a sort over a filter that keeps one array item:
// the sorted sequence collapses to that array as the expression returns
// it, while a later stage or step still sees the array as one item.
var sortOneArrayCases = []exprCase{ //nolint:dupl // distinct cases sharing the exprCase layout
	{expr: `n[$count($)>1]^($)`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `$count(n[$count($)>1]^($))`, data: `{"n":[[1,2],[3]]}`, want: `2`},
	{expr: `n[$count($)>1]^(-$)`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `$reverse(n)[$count($)>1]^($)`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `$append(n,[])[$count($)>1]^($)`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `$filter(n,function($v){$count($v)>1})^($)`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `($x:=n; $x[$count($)>1]^($))`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `($x := n[$count($)>1]^($); $count($x))`, data: `{"n":[[1,2],[3]]}`, want: `2`},
	{expr: `(n[$count($)>1]^($))[0]`, data: `{"n":[[1,2],[3]]}`, want: `1`},
	{expr: `n[$count($)>1]^($) = [1,2]`, data: `{"n":[[1,2],[3]]}`, want: `true`},
	{expr: `n[$count($)>1]^($) ~> $count`, data: `{"n":[[1,2],[3]]}`, want: `2`},
	{expr: `$string(n[$count($)>1]^($))`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `$append(n[$count($)>1]^($), 5)`, data: `{"n":[[1,2],[3]]}`, want: `[1,2,5]`},
	{expr: `$reverse(n[$count($)>1]^($))`, data: `{"n":[[1,2],[3]]}`, want: `[2,1]`},
	{expr: `[n[$count($)>1]^($)]`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `[[n[$count($)>1]^($)]]`, data: `{"n":[[1,2],[3]]}`, want: `[[1,2]]`},
	{expr: `n[$count($)>1]^($)^($)`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `n[$count($)>1]^($)[]`, data: `{"n":[[1,2],[3]]}`, want: `[[1,2]]`},
	{expr: `$count(n[$count($)>1]^($)[])`, data: `{"n":[[1,2],[3]]}`, want: `1`},
	{expr: `n[$count($)>1]^($)[0]`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `n[$count($)>1]^($)[0][0]`, data: `{"n":[[1,2],[3]]}`, want: `1`},
	{expr: `n[$count($)>1]^($).$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `n[$count($)>1]^($){"k":$count($)}`, data: `{"n":[[1,2],[3]]}`, want: `{"k":2}`},
	{expr: `$.n[$count($)>1]^($)[0]`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `$.n[$count($)>1]^($).$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `o.n[$count($)>1]^($)[0]`, data: `{"o":{"n":[[1,2],[3]]}}`, want: `[1,2]`},
}

// filterBaseCases pin a filter's selection by its base. On a field or a
// sort, or on any step after a path's first, it is a stage of that step
// and passes its selection on as a sequence; on a variable, call,
// constructor or block alone or first in a path it is a predicate of an
// expression, whose one-item selection collapses as the expression returns
// it, before a later path step, sort or #$var binding sees it.
var filterBaseCases = []exprCase{
	{expr: `$.(n)[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `$.$lookup($,"n")[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `a.(n)[$count($)>1].$string()`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `"[1,2]"`},
	{expr: `a.$lookup($,"n")[$count($)>1].$string()`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `"[1,2]"`},
	{expr: `a.[n][$count($)>1].$string()`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `"[1,2]"`},
	{expr: `$.(n)[$count($)>1].$#$i.$i`, data: `{"n":[[1,2],[3]]}`, want: `[0,1]`},
	{expr: `$.(n)[$count($)>1]#$i.$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `[[1,2]][true].$string()`, want: `["1","2"]`},
	{expr: `[[1,2]][[0]].$string()`, want: `["1","2"]`},
	{expr: `[[1,2],[3]][$count($)>1].$string()`, want: `["1","2"]`},
	{expr: `($x:=n; $x[$count($)>1].$[0])`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `($x:=n; $x[$count($)>1].$string())`, data: `{"n":[[1,2],[3]]}`, want: `["1","2"]`},
	{expr: `($x:=n; $x[[0]].$string())`, data: `{"n":[[1,2],[3]]}`, want: `["1","2"]`},
	{expr: `($x:=n; $x[$count($)>1][[0]].$string())`, data: `{"n":[[1,2],[3]]}`, want: `["1","2"]`},
	{expr: `($x:=n; $x[$count($)>1][0].$string())`, data: `{"n":[[1,2],[3]]}`, want: `["1","2"]`},
	{expr: `($x:=n; $x[$count($)>1][0])`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `($x:=n; $x[$count($)>1][].$string())`, data: `{"n":[[1,2],[3]]}`, want: `["[1,2]"]`},
	{expr: `$filter(n, function($v){true})[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `["1","2"]`},
	{expr: `$reverse(n)[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `["1","2"]`},
	{expr: `$reverse(n)[[0]].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"3"`},
	{expr: `$reverse(n)[$count($)>1][].$string()`, data: `{"n":[[1,2],[3]]}`, want: `["[1,2]"]`},
	{expr: `(n)[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `["1","2"]`},
	{expr: `($x:=n; $x[$count($)>1]^($)[0])`, data: `{"n":[[1,2],[3]]}`, want: `1`},
	{expr: `($x:=n; $x[[0]]^($)[0])`, data: `{"n":[[1,2],[3]]}`, want: `1`},
	{expr: `$reverse(n)[$count($)>1]^($)[0]`, data: `{"n":[[1,2],[3]]}`, want: `1`},
	{expr: `$filter(n,function($v){true})[$count($)>1]^($)[]`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `($x:=n; $x[-1]{"k":$}^($))`, data: `{"n":[[1,2],[3]]}`, want: `{"k":3}`},
	{expr: `($x:=n; $x[$count($)>1]{"k":$}^($))`, data: `{"n":[[1,2],[3]]}`, want: `{"k":[1,2]}`},
	{expr: `($x:=n; $x[0][]#$i)`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `($x:=n; $x[0][]#$i[0])`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `($x:=n; $x[0][]#$i[])`, data: `{"n":[[1,2],[3]]}`, want: `[[1,2]]`},
	{expr: `($x:=n; $x[0][]#$i.$string())`, data: `{"n":[[1,2],[3]]}`, want: `["[1,2]"]`},
	{expr: `($x:=n; $x[$count($)>1][]#$i)`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `$reverse(n)[0][]#$i`, data: `{"n":[[1,2],[3]]}`, want: `[3]`},
	{expr: `$reverse(n)[]#$i[0]`, data: `{"n":[[1,2],[3]]}`, want: `[3]`},
	{expr: `$reverse(n)#$i[0][]`, data: `{"n":[[1,2],[3]]}`, want: `[[3]]`},
	{expr: `($x:=[5]; $x[0][]#$i)`, data: `{}`, want: `5`},
	{expr: `$count(a)[]#$i`, data: `{"a":[1,2,3]}`, want: `3`},
	{expr: `$count(a)#$i[]`, data: `{"a":[1,2,3]}`, want: `[3]`},
	{expr: `*[0][]#$i`, data: `{"a":[1,2]}`, want: `1`},
	{expr: `n[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `n[$count($)>1].$[0]`, data: `{"n":[[1,2],[3]]}`, want: `1`},
	{expr: `n[$count($)>1][].$string()`, data: `{"n":[[1,2],[3]]}`, want: `["[1,2]"]`},
	{expr: `n[0][].$string()`, data: `{"n":[[1,2],[3]]}`, want: `["1","2"]`},
	{expr: `n[0][]#$i`, data: `{"n":[[1,2],[3]]}`, want: `[[1,2]]`},
	{expr: `n[$count($)>1][]#$i`, data: `{"n":[[1,2],[3]]}`, want: `[[1,2]]`},
	{expr: `n[-1]{"k":$}^($)`, data: `{"n":[[1,2],[3]]}`, want: `{"k":3}`},
	{expr: `a[0][]#$i`, data: `{"a":[1,2]}`, want: `[1]`},
	{expr: `q.{"k":n[$count($)>1]}`, data: `{"q":[[{"n":[[1,2],[3]]}]]}`, want: `{"k":[1,2]}`},
	{expr: `q.{"k":n[$count($)>1][]}`, data: `{"q":[[{"n":[[1,2],[3]]}]]}`, want: `{"k":[[1,2]]}`},
	{expr: `q.(n[$count($)>1] = [1,2])`, data: `{"q":[[{"n":[[1,2],[3]]}]]}`, want: `true`},
	{expr: `q.(n[$count($)>1]).$string()`, data: `{"q":[[{"n":[[1,2],[3]]}]]}`, want: `["1","2"]`},
	{expr: `q.(n[$count($)>1][]).$string()`, data: `{"q":[[{"n":[[1,2],[3]]}]]}`, want: `"[1,2]"`},
	{expr: `q.(n[$count($)>1])[0]`, data: `{"q":[[{"n":[[1,2],[3]]}]]}`, want: `1`},
	{expr: `q.[n[$count($)>1]]`, data: `{"q":[[{"n":[[1,2],[3]]}]]}`, want: `[1,2]`},
	{expr: `q.(n[[0]]).$string()`, data: `{"q":[[{"n":[[1,2],[3]]}]]}`, want: `["1","2"]`},
	{expr: `q.$string(n[$count($)>1])`, data: `{"q":[[{"n":[[1,2],[3]]}]]}`, want: `"[1,2]"`},
	{expr: `n^($[0])[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `n^($[0])[$count($)>1].$[0]`, data: `{"n":[[1,2],[3]]}`, want: `1`},
	{expr: `n^($[0])[$count($)>1]^($).$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `n^($[0])[$count($)>1]^($)[0]`, data: `{"n":[[1,2],[3]]}`, want: `[1,2]`},
	{expr: `n^(-$[0])[[0]].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[3]"`},
	{expr: `n^($[0])[[1]].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[3]"`},
	{expr: `n^($[0])[$count($)>1][[0]].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `n^($[0])^($[0])[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `$.n^($[0])[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `$reverse(n)^($[0])[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `(n)^($[0])[$count($)>1].$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `a.n^($[0])[[0]].$string()`, data: `{"a":[{"n":[[1,2],[3]]}]}`, want: `"[1,2]"`},
	{expr: `a.n^($[0])[$count($)>1].$string()`, data: `{"a":[{"n":[[1,2],[3]]}]}`, want: `"[1,2]"`},
}

// tupleSelectionCases pin a field filter's selection where a tuple stream
// takes it: its lone array item stays one tuple, before a sort or a later
// #$var step alike, and a path starting at a constructor runs without
// input.
var tupleSelectionCases = []exprCase{
	{expr: `n[$count($)>1]^($)#$i.$i`, data: `{"n":[[1,2],[3]]}`, want: `0`},
	{expr: `n[$count($)>1]^($)#$i.$string()`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `n[[0,1]]^($[0])#$i.$i`, data: `{"n":[[1,2],[3]]}`, want: `[0,1]`},
	{expr: `n[0]^($)#$i.$i`, data: `{"n":[[1,2],[3]]}`, want: `[0,1]`},
	{expr: `a.n[$count($)>1]^($)#$i.$i`, data: `{"a":[{"n":[[1,2],[3]]}]}`, want: `0`},
	{expr: `$.n[$count($)>1]^($)#$i.$i`, data: `{"n":[[1,2],[3]]}`, want: `0`},
	{expr: `a.n[$count($)>1]^($)[0]`, data: `{"a":[{"n":[[1,2],[3]]}]}`, want: `[1,2]`},
	{expr: `a.n[$count($)>1]^($)[]`, data: `{"a":[{"n":[[1,2],[3]]}]}`, want: `[[1,2]]`},
	{expr: `a.n[$count($)>1]^($).$string()`, data: `{"a":[{"n":[[1,2],[3]]}]}`, want: `"[1,2]"`},
	{expr: `a.n[$count($)>1][]`, data: `{"a":[{"n":[[1,2],[3]]}]}`, want: `[[1,2]]`},
	{expr: `n[$count($)>1].$#$i.$i`, data: `{"n":[[1,2],[3]]}`, want: `[0,1]`},
	{expr: `a.n[$count($)>1].$#$i.$i`, data: `{"a":[{"n":[[1,2],[3]]}]}`, want: `[0,1]`},
	{expr: `n[$count($)>1].$string()#$i`, data: `{"n":[[1,2],[3]]}`, want: `"[1,2]"`},
	{expr: `n[[0,1]].$#$i.$i`, data: `{"n":[[1,2],[3]]}`, want: `[0,1,0]`},
	{expr: `n[0].$#$i.$i`, data: `{"n":[[1,2],[3]]}`, want: `[0,0]`},
	{expr: `n[$count($)>5].$#$i.$i`, data: `{"n":[[1,2],[3]]}`, want: undefined},
	{expr: `[[1,2]][0][]#$i`, want: `[1,2]`},
	{expr: `[[1,2]][0]#$i`, want: `[1,2]`},
	{expr: `[[1,2]][[0]]#$i`, want: `[1,2]`},
	{expr: `([[1,2]])[0]#$i`, want: `[1,2]`},
	{expr: `$x[0]#$i`, want: undefined},
	{expr: `[[1,2]][$i>=0]#$i`, want: undefined},
	{expr: `"a"[0]#$i`, want: `"a"`},
}

// sortGroupLoneArrayCases pin that a sort or group of a path sees the
// step's sequence before it collapses: one context yields an array, its
// sibling an empty array, so the sequence holds one item, that array.
var sortGroupLoneArrayCases = []exprCase{
	{expr: `m.b^($)[0]`, data: `{"m":[{"b":[[1,2]]},{"b":[]}]}`, want: `[1,2]`},
	{expr: `m.b^($)[]`, data: `{"m":[{"b":[[1,2]]},{"b":[]}]}`, want: `[[1,2]]`},
	{expr: `m.b^($).$string()`, data: `{"m":[{"b":[[1,2]]},{"b":[]}]}`, want: `"[1,2]"`},
	{expr: `m.(b)^($)[0]`, data: `{"m":[{"b":[[1,2]]},{"b":[]}]}`, want: `[1,2]`},
	{expr: `m.$.b^($)[0]`, data: `{"m":[{"b":[[1,2]]},{"b":[]}]}`, want: `[1,2]`},
	{expr: `m.$lookup($,"b")^($)[0]`, data: `{"m":[{"b":[[1,2]]},{"b":[]}]}`, want: `[1,2]`},
	{expr: `m.b{"k":$count($)}`, data: `{"m":[{"b":[[1,2]]},{"b":[]}]}`, want: `{"k":2}`},
	{expr: `m.b^($)`, data: `{"m":[{"b":[[1,2]]},{"b":[]}]}`, want: `[1,2]`},
	{expr: `m.b`, data: `{"m":[{"b":[[1,2]]},{"b":[]}]}`, want: `[1,2]`},
}

// keptStageCases pin that a path's [] keeps the lone array a filter stage
// of its last step selected as one item, on a path's first step or any
// later one, whatever the step's base.
var keptStageCases = []exprCase{
	{expr: `n[$count($)>1][]`, data: `{"n":[[1,2],[3]]}`, want: `[[1,2]]`},
	{expr: `$.n[$count($)>1][]`, data: `{"n":[[1,2],[3]]}`, want: `[[1,2]]`},
	{expr: `$.n[][$count($)>1]`, data: `{"n":[[1,2],[3]]}`, want: `[[1,2]]`},
	{expr: `$.(n)[$count($)>1][]`, data: `{"n":[[1,2],[3]]}`, want: `[[1,2]]`},
	{expr: `a.n[$count($)>1][]`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[[1,2]]`},
	{expr: `a.b.n[$count($)>1][]`, data: `{"a":{"b":{"n":[[1,2],[3]]}}}`, want: `[[1,2]]`},
	{expr: `a.[n][$count($)>1][]`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[[1,2]]`},
	{expr: `a.$lookup($,"n")[$count($)>1][]`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[[1,2]]`},
	{expr: `a.n[$count($)>1][$count($)>1][]`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[[1,2]]`},
	{expr: `a.n[[0]][]`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[[1,2]]`},
	{expr: `a.n[0][]`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[1,2]`},
	{expr: `a.n[$count($)>0][]`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[[1,2],[3]]`},
	{expr: `a.n[$count($)>1][].$`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[1,2]`},
	{expr: `a.(n[$count($)>1][])`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[1,2]`},
	{expr: `$count(a.n[$count($)>1][])`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `1`},
	{expr: `$string(a.n[$count($)>1][])`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `"[[1,2]]"`},
	{expr: `a.n[$count($)>1][]^($count($))`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[[1,2]]`},
	{expr: `a.n^($count($))[$count($)>1][]`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[[1,2]]`},
	{expr: `a.n[$count($)>1][]#$i.$i`, data: `{"a":{"n":[[1,2],[3]]}}`, want: `[0]`},
	{expr: `s.n[$count($)>1][]`, data: `{"s":[{"n":[[1,2],[3]]}]}`, want: `[[1,2]]`},
}

// regexDepthCases pin regex literals jsonata-js closes by its one bracket
// depth count, which counts brackets in classes and of any kind.
var regexDepthCases = []exprCase{
	{expr: `$match("a{}", /(a{)}/).match`, want: `"a{}"`},
	{expr: `$match("a{}", /({[)])/).match`, want: undefined},
	{expr: `$match("{)", /({[)])/).match`, want: `"{)"`},
	{expr: `$match("a{}", /({[}])/).match`, want: `"{}"`},
	{expr: `$match("a{}", /([)]{)/).match`, want: undefined},
	{expr: `$match(")" & "{", /([)]{)/).match`, want: `"){"`},
}

// assertCases pin $assert's arguments validated against its signature
// <bs?:x>: an undefined condition or message matches and fails, any other
// non-boolean condition or non-string message raises T0410.
var assertCases = []exprCase{
	{expr: `$assert(true)`, want: undefined},
	{expr: `$assert(true, "m")`, want: undefined},
	{expr: `$assert(true, 5)`, code: "T0410"},
	{expr: `$assert(false)`, code: "D3141"},
	{expr: `$assert(false, "m")`, code: "D3141: m"},
	{expr: `$assert(false, 5)`, code: "T0410"},
	{expr: `$assert(false, nothing)`, code: "D3141"},
	{expr: `$assert(nothing)`, code: "D3141"},
	{expr: `$assert(nothing, "m")`, code: "D3141: m"},
	{expr: `$assert(nothing, 5)`, code: "T0410"},
	{expr: `$assert(nothing, nothing)`, code: "D3141"},
	{expr: `$assert(1)`, code: "T0410"},
	{expr: `$assert(null)`, code: "T0410"},
	{expr: `$assert("x", "m")`, code: "T0410"},
	{expr: `$assert([true])`, code: "T0410"},
	{expr: `[true].$assert()`, code: "T0410"},
	{expr: `$assert()`, code: "T0410"},
	{expr: `$assert(true, "a", "b")`, code: "T0410"},
}

// argumentCountCases pin that a builtin without a context parameter ('-')
// raises T0410 for too few or too many arguments, whatever the context,
// while an undefined argument still counts as one.
var argumentCountCases = []exprCase{
	{expr: `$.n[$count($)>1].$count()`, data: `{"n":[[1,2],[3]]}`, code: "T0410"},
	{expr: `n[$count($)>1].$count()`, data: `{"n":[[1,2],[3]]}`, code: "T0410"},
	{expr: `n[$count($)>1].($count())`, data: `{"n":[[1,2],[3]]}`, code: "T0410"},
	{expr: `n.$count()`, data: `{"n":[[1,2],[3]]}`, code: "T0410"},
	{expr: `$count()`, data: `{"n":[[1,2],[3]]}`, code: "T0410"},
	{expr: `$count()`, code: "T0410"},
	{expr: `[1,2].$count()`, code: "T0410"},
	{expr: `$count(1,2)`, code: "T0410"},
	{expr: `$count(nothing)`, want: `0`},
	{expr: `n[$count($)>1].$count($)`, data: `{"n":[[1,2],[3]]}`, want: `2`},
	{expr: `$map([[1],[2,3]], $count)`, want: `[1,2]`},
	{expr: `$count(?)([1,2])`, want: `2`},
	{expr: `($f := $count; $f())`, code: "T0410"},
	{expr: `[1,2].$reverse()`, code: "T0410"},
	{expr: `$reverse([1],2)`, code: "T0410"},
	{expr: `[1,2].$sort()`, code: "T0410"},
	{expr: `[1,2].$distinct()`, code: "T0410"},
	{expr: `[1,2].$shuffle()`, code: "T0410"},
	{expr: `$type()`, code: "T0410"},
	{expr: `$type(nothing)`, want: `undefined`},
	{expr: `$type([1],2)`, code: "T0410"},
	{expr: `$append(nothing)`, code: "T0410"},
	{expr: `$append([1],2,3)`, code: "T0410"},
	{expr: `$append(nothing,1)`, want: `1`},
	{expr: `$single()`, code: "T0410"},
	{expr: `$single([1],$exists,3)`, code: "T0410"},
	{expr: `$reduce([1])`, code: "T0410"},
	{expr: `$random(1)`, code: "T0410"},
	{expr: `$millis(1)`, code: "T0410"},
	{expr: `$now(1,2,3)`, code: "T0410"},
	{expr: `$contains()`, code: "T0410"},
	{expr: `s.$contains("a")`, data: `{"s":"ab"}`, want: `true`},
}

// functionEqualityCases pin that a function value equals only itself,
// alone or inside an array or object, whether or not the containers
// compared are one.
var functionEqualityCases = []exprCase{
	{expr: `($a:=[$string]; $a=$a)`, want: `true`},
	{expr: `($a:=[$string]; $a = [$a[0]])`, want: `true`},
	{expr: `($a:=[$string]; $a != [$a[0]])`, want: `false`},
	{expr: `($f:=function(){1}; [$f] = [$f])`, want: `true`},
	{expr: `($f:=function(){1}; $f = $f)`, want: `true`},
	{expr: `($f:=function(){1}; {"a":$f} = {"a":$f})`, want: `true`},
	{expr: `$string = $string`, want: `true`},
	{expr: `$string != $string`, want: `false`},
	{expr: `{"a":$string} = {"a":$string}`, want: `true`},
	{expr: `$string = $number`, want: `false`},
	{expr: `function(){1} = function(){1}`, want: `false`},
	{expr: `($p:=$substring(?,1); [$p]=[$p])`, want: `true`},
	{expr: `($t:=|$|{}|; [$t]=[$t])`, want: `true`},
	{expr: `|$|{}| = |$|{}|`, want: `false`},
	{expr: `($r:=/a/; [$r]=[$r])`, want: `true`},
	{expr: `/a/ = /a/`, want: `false`},
	{expr: `$replace("aa", /a/, function($m){ $string([$m.next] = [$m.next]) })`, want: `"truetrue"`},
	{expr: `($f:=function(){1}; $f in [$f])`, want: `true`},
	{expr: `$string in [$string]`, want: `true`},
	{expr: `($f:=function(){1}; $count($distinct([$f,$f])))`, want: `1`},
	{expr: `$count($distinct([$sum,$string,$sum]))`, want: `2`},
}

// cloneFunctionCases pin that $clone and a transform's copy go through
// JSON, where a function, a regex included, becomes "".
var cloneFunctionCases = []exprCase{
	{expr: `$clone({"r": /a/, "f": $sum})`, want: `{"f":"","r":""}`},
	{expr: `{"r": /a/, "f": $sum} ~> |$|{}|`, want: `{"f":"","r":""}`},
	{expr: `{"a": {"r": /a/}} ~> |a|{"x": 1}|`, want: `{"a":{"r":"","x":1}}`},
}

// TestJSONataJSParity runs cases whose expectations come from jsonata-js
// 2.2.2 through every evaluation API, fast paths included.
func TestJSONataJSParity(t *testing.T) {
	t.Run("lone array step results", func(t *testing.T) { runExprCasesAllAPIs(t, loneArrayCases) })
	t.Run("[] on a call", func(t *testing.T) { runExprCasesAllAPIs(t, callKeepArrayCases) })
	t.Run("grouped fast-path expressions", func(t *testing.T) { runExprCasesAllAPIs(t, groupedCases) })
	t.Run("[] after a tuple stage", func(t *testing.T) { runExprCasesAllAPIs(t, tupleStageCases) })
	t.Run("predicate on a missing base", func(t *testing.T) { runExprCasesAllAPIs(t, missingBaseCases) })
	t.Run("kept missing item as a context", func(t *testing.T) { runExprCasesAllAPIs(t, keptMissingCases) })
	t.Run("$contains through an array", func(t *testing.T) { runExprCasesAllAPIs(t, containsCases) })
	t.Run("null items of Go data", func(t *testing.T) { runExprCasesAllAPIs(t, nullItemCases) })
	t.Run("group on a wildcard over no fields", func(t *testing.T) { runExprCasesAllAPIs(t, wildcardGroupCases) })
	t.Run("[] after a #$var binding", func(t *testing.T) { runExprCasesAllAPIs(t, boundKeepArrayCases) })
	t.Run("lone array over several contexts", func(t *testing.T) { runExprCasesAllAPIs(t, loneArrayContextCases) })
	t.Run("* over a one-item root array", func(t *testing.T) { runExprCasesAllAPIs(t, rootWildcardCases) })
	t.Run("field holding an empty array", func(t *testing.T) { runExprCasesAllAPIs(t, emptyFieldCases) })
	t.Run("sort of one array item", func(t *testing.T) { runExprCasesAllAPIs(t, sortOneArrayCases) })
	t.Run("filter selection by base", func(t *testing.T) { runExprCasesAllAPIs(t, filterBaseCases) })
	t.Run("filter selection in a tuple stream", func(t *testing.T) { runExprCasesAllAPIs(t, tupleSelectionCases) })
	t.Run("sort or group of a lone array", func(t *testing.T) { runExprCasesAllAPIs(t, sortGroupLoneArrayCases) })
	t.Run("[] on a lone filtered array", func(t *testing.T) { runExprCasesAllAPIs(t, keptStageCases) })
	t.Run("regex closed by bracket depth", func(t *testing.T) { runExprCasesAllAPIs(t, regexDepthCases) })
	t.Run("$assert arguments", func(t *testing.T) { runExprCasesAllAPIs(t, assertCases) })
	t.Run("argument count of a builtin", func(t *testing.T) { runExprCasesAllAPIs(t, argumentCountCases) })
	t.Run("function equality", func(t *testing.T) { runExprCasesAllAPIs(t, functionEqualityCases) })
	t.Run("functions in a copy", func(t *testing.T) { runExprCasesAllAPIs(t, cloneFunctionCases) })
}
