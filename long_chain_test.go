// js/wasm runs under Node, whose call stack cannot hold the parser's
// recursion over these chains.

//go:build !js

package gnata_test

import (
	"strings"
	"testing"
)

// longChainCases guard against compile or evaluation time growing
// quadratically with the length of a chain of sorts or subscripts.
var longChainCases = []exprCase{
	{expr: `a` + strings.Repeat(`[0]`, 20_000), data: `{"a":[{"c":2},{"c":1}]}`, want: `{"c":2}`},
	{expr: `a[]` + strings.Repeat(`[0]`, 20_000), data: `{"a":[{"c":2},{"c":1}]}`, want: `[{"c":2}]`},
	{expr: `a` + strings.Repeat(`[%.c]`, 20_000) + `.x`, data: `{"a":{"x":1},"c":true}`, want: `1`},
	{expr: `a` + strings.Repeat(`.$^(c)`, 20_000), data: `{"a":[{"c":2},{"c":1}]}`, want: `[{"c":1},{"c":2}]`},
	{expr: `a` + strings.Repeat(`^($)`, 20_000), data: `{"a":[3,1,2]}`, want: `[1,2,3]`},
	{expr: `a#$j` + strings.Repeat(`^($)[0]`, 20_000) + `.$j`, data: `{"a":[3,1,2]}`, want: `1`},
	{expr: `a#$j` + strings.Repeat(`^($)`, 20_000) + `.$j`, data: `{"a":[3,1,2]}`, code: "T2008"},
}

func TestLongChains(t *testing.T) {
	runExprCases(t, longChainCases)
}
