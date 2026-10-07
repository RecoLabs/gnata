// js/wasm runs under Node, whose call stack cannot hold the parser's
// recursion over these chains.

//go:build !js

package gnata_test

import (
	"strings"
	"testing"
)

// chainLength stays under the parser's nesting limit of 10,000 levels,
// where each chained operator is a level; a chain repeating two operators
// has half as many links.
const chainLength = 9_000

// longChainCases guard against compile or evaluation time growing
// quadratically with the length of a path or of a chain of sorts,
// subscripts or operators.
var longChainCases = []exprCase{
	{expr: `a` + strings.Repeat(`.a`, chainLength), data: `{"a":{"a":1}}`, want: undefined},
	{expr: `a` + strings.Repeat(`[0]`, chainLength), data: `{"a":[{"c":2},{"c":1}]}`, want: `{"c":2}`},
	{expr: `a[]` + strings.Repeat(`[0]`, chainLength), data: `{"a":[{"c":2},{"c":1}]}`, want: `[{"c":2}]`},
	{expr: `a` + strings.Repeat(`[%.c]`, chainLength) + `.x`, data: `{"a":{"x":1},"c":true}`, want: `1`},
	{expr: `x.*` + strings.Repeat(`[%.c]`, chainLength), data: `{"x":{"a":1,"c":true}}`, want: `[1,true]`},
	{expr: `$count(a.((b)` + strings.Repeat(`[%.c]`, chainLength) + `))`, data: `{"a":{"b":1,"c":true}}`, want: `1`},
	{expr: `a.(%.x` + strings.Repeat(`+%.x`, chainLength) + `)`, data: `{"a":{"x":1},"x":1}`, want: `9001`},
	{
		expr: `a.(` + strings.Repeat(`%.x ? `, chainLength) + `1` + strings.Repeat(` : 0`, chainLength) + `)`,
		data: `{"a":{"x":1},"x":1}`, want: `1`,
	},
	{expr: `a` + strings.Repeat(`.$^(c)`, chainLength/2), data: `{"a":[{"c":2},{"c":1}]}`, want: `[{"c":1},{"c":2}]`},
	{expr: `a` + strings.Repeat(`^($)`, chainLength), data: `{"a":[3,1,2]}`, want: `[1,2,3]`},
	{expr: `a#$j` + strings.Repeat(`^($)[0]`, chainLength/2) + `.$j`, data: `{"a":[3,1,2]}`, want: `1`},
	{expr: `a#$j` + strings.Repeat(`^($)`, chainLength) + `.$j`, data: `{"a":[3,1,2]}`, code: "T2008"},
	{expr: `a` + strings.Repeat(`.a`, 10_000), code: "S0218"},
	{expr: `a` + strings.Repeat(`[0]`, 10_000), code: "S0218"},
}

func TestLongChains(t *testing.T) {
	runExprCases(t, longChainCases)
}
