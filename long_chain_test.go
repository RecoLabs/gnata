// js/wasm runs under Node, whose call stack cannot hold the parser's
// recursion over these chains.

//go:build !js

package gnata_test

import (
	"strings"
	"testing"
)

// longChainCases guard against compile or evaluation time growing
// quadratically with the length of a path or of a chain of sorts,
// subscripts or operators.
var longChainCases = []exprCase{
	{expr: `a` + strings.Repeat(`.a`, 20_000), data: `{"a":{"a":1}}`, want: undefined},
	{expr: `a` + strings.Repeat(`[0]`, 20_000), data: `{"a":[{"c":2},{"c":1}]}`, want: `{"c":2}`},
	{expr: `a[]` + strings.Repeat(`[0]`, 20_000), data: `{"a":[{"c":2},{"c":1}]}`, want: `[{"c":2}]`},
	{expr: `a` + strings.Repeat(`[%.c]`, 20_000) + `.x`, data: `{"a":{"x":1},"c":true}`, want: `1`},
	{expr: `x.*` + strings.Repeat(`[%.c]`, 20_000), data: `{"x":{"a":1,"c":true}}`, want: `[1,true]`},
	{expr: `$count(a.((b)` + strings.Repeat(`[%.c]`, 20_000) + `))`, data: `{"a":{"b":1,"c":true}}`, want: `1`},
	{expr: `a.(%.x` + strings.Repeat(`+%.x`, 20_000) + `)`, data: `{"a":{"x":1},"x":1}`, want: `20001`},
	{
		expr: `a.(` + strings.Repeat(`%.x ? `, 20_000) + `1` + strings.Repeat(` : 0`, 20_000) + `)`,
		data: `{"a":{"x":1},"x":1}`, want: `1`,
	},
	{expr: `a` + strings.Repeat(`.$^(c)`, 20_000), data: `{"a":[{"c":2},{"c":1}]}`, want: `[{"c":1},{"c":2}]`},
	{expr: `a` + strings.Repeat(`^($)`, 20_000), data: `{"a":[3,1,2]}`, want: `[1,2,3]`},
	{expr: `a#$j` + strings.Repeat(`^($)[0]`, 20_000) + `.$j`, data: `{"a":[3,1,2]}`, want: `1`},
	{expr: `a#$j` + strings.Repeat(`^($)`, 20_000) + `.$j`, data: `{"a":[3,1,2]}`, code: "T2008"},
}

func TestLongChains(t *testing.T) {
	runExprCases(t, longChainCases)
}
