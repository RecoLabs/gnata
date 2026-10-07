package parser_test

import (
	"testing"

	"github.com/recolabs/gnata/internal/parser"
)

func TestAnalyzeFuncFastPath(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		wantKind parser.FuncFastKind
		wantPath string
		wantStr  string
	}{
		{"$exists single arg", `$exists(a)`, parser.FuncFastExists, "a", ""},
		{"$exists dotted path", `$exists(a.b.c)`, parser.FuncFastExists, "a.b.c", ""},
		{"$contains with literal", `$contains(name, "foo")`, parser.FuncFastContains, "name", "foo"},
		{"$string", `$string(age)`, parser.FuncFastString, "age", ""},
		{"$boolean", `$boolean(flag)`, parser.FuncFastBoolean, "flag", ""},
		{"$number", `$number(val)`, parser.FuncFastNumber, "val", ""},
		{"$lowercase", `$lowercase(name)`, parser.FuncFastLowercase, "name", ""},
		{"$uppercase", `$uppercase(name)`, parser.FuncFastUppercase, "name", ""},
		{"$trim", `$trim(greeting)`, parser.FuncFastTrim, "greeting", ""},
		{"$length", `$length(s)`, parser.FuncFastLength, "s", ""},
		{"$type", `$type(x)`, parser.FuncFastType, "x", ""},
		{"$not", `$not(flag)`, parser.FuncFastNot, "flag", ""},
		{"$abs", `$abs(n)`, parser.FuncFastAbs, "n", ""},
		{"$floor", `$floor(n)`, parser.FuncFastFloor, "n", ""},
		{"$ceil", `$ceil(n)`, parser.FuncFastCeil, "n", ""},
		{"$sqrt", `$sqrt(n)`, parser.FuncFastSqrt, "n", ""},
		{"$count", `$count(arr)`, parser.FuncFastCount, "arr", ""},
		{"$reverse", `$reverse(arr)`, parser.FuncFastReverse, "arr", ""},
		{"$distinct", `$distinct(arr)`, parser.FuncFastDistinct, "arr", ""},
		{"$keys", `$keys(obj)`, parser.FuncFastKeys, "obj", ""},
		{"$sum", `$sum(arr)`, parser.FuncFastSum, "arr", ""},
		{"$max", `$max(arr)`, parser.FuncFastMax, "arr", ""},
		{"$min", `$min(arr)`, parser.FuncFastMin, "arr", ""},
		{"$average", `$average(arr)`, parser.FuncFastAverage, "arr", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := mustParse(t, tc.expr)
			fp := parser.AnalyzeFastPath(node)
			if fp.FuncFast == nil {
				t.Fatalf("expected FuncFast for %q, got nil", tc.expr)
			}
			if fp.FuncFast.Kind != tc.wantKind {
				t.Errorf("kind: got %d, want %d", fp.FuncFast.Kind, tc.wantKind)
			}
			if fp.FuncFast.Path != tc.wantPath {
				t.Errorf("path: got %q, want %q", fp.FuncFast.Path, tc.wantPath)
			}
			if fp.FuncFast.StrArg != tc.wantStr {
				t.Errorf("strArg: got %q, want %q", fp.FuncFast.StrArg, tc.wantStr)
			}
		})
	}
}

func TestAnalyzeFuncFastPath_Rejected(t *testing.T) {
	rejected := []struct {
		name string
		expr string
	}{
		{"$round excluded", `$round(x)`},
		{"nested function arg", `$exists($lowercase(x))`},
		{"non-path arg", `$exists(1 + 2)`},
		{"$contains non-string second arg", `$contains(name, 42)`},
		{"$contains one arg", `$contains(name)`},
		{"multi-arg function", `$substring(name, 0, 3)`},
		{"custom function", `$myFunc(x)`},
		{"variable arg", `$exists($x)`},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			node := mustParse(t, tc.expr)
			fp := parser.AnalyzeFastPath(node)
			if fp.FuncFast != nil {
				t.Errorf("expected FuncFast to be nil for %q, got kind=%d", tc.expr, fp.FuncFast.Kind)
			}
		})
	}
}

func TestDecimalSafeFastPath(t *testing.T) {
	tests := []struct {
		desc                    string
		expr                    string
		wantCmp, wantFn, wantBl bool
	}{
		{desc: "number comparison dropped", expr: `a = 0.3`},
		{desc: "string comparison kept", expr: `a = "x"`, wantCmp: true},
		{desc: "numeric function dropped", expr: `$sum(a)`},
		{desc: "string function kept", expr: `$lowercase(a)`, wantFn: true},
		{desc: "and with number comparison dropped", expr: `a = 0.3 and b`},
		{desc: "or with numeric function dropped", expr: `b or $sum(a)`},
		{desc: "not with number comparison dropped", expr: `$not(a = 1e20)`},
		{desc: "nested number comparison dropped", expr: `b and (c or a != 1)`},
		{desc: "and of safe leaves kept", expr: `$exists(a) and b = "x"`, wantBl: true},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			fp := parser.AnalyzeFastPath(mustParse(t, tc.expr))
			fp.DecimalSafe()
			if got := fp.CmpFast != nil; got != tc.wantCmp {
				t.Errorf("CmpFast kept = %v, want %v", got, tc.wantCmp)
			}
			if got := fp.FuncFast != nil; got != tc.wantFn {
				t.Errorf("FuncFast kept = %v, want %v", got, tc.wantFn)
			}
			if got := fp.BoolFast != nil; got != tc.wantBl {
				t.Errorf("BoolFast kept = %v, want %v", got, tc.wantBl)
			}
		})
	}
}

func TestSequenceLimited(t *testing.T) {
	tests := []struct {
		desc                                                  string
		expr                                                  string
		wantFastPath, wantCmpFast, wantFuncFast, wantBoolFast bool
		wantBoolFuncs                                         int // function leaves the BoolFast keeps
	}{
		{desc: "path kept as a lookup", expr: `a.b`, wantFastPath: true},
		{desc: "comparison kept as a lookup", expr: `a.b = "x"`, wantCmpFast: true},
		{desc: "function kept as a lookup", expr: `$lowercase(a.b)`, wantFuncFast: true},
		{desc: "$keys dropped", expr: `$keys(a.b)`},
		{desc: "and of leaves kept as lookups", expr: `$exists(a.b) and c.d = "x"`, wantBoolFast: true, wantBoolFuncs: 1},
		{desc: "$keys leaf of an and dropped", expr: `$keys(a) and $exists(b)`, wantBoolFast: true, wantBoolFuncs: 1},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			fp := parser.AnalyzeFastPath(mustParse(t, tc.expr))
			fp.SequenceLimited()
			if fp.IsFastPath != tc.wantFastPath {
				t.Errorf("IsFastPath = %v, want %v", fp.IsFastPath, tc.wantFastPath)
			}
			if got := fp.CmpFast != nil; got != tc.wantCmpFast {
				t.Errorf("CmpFast kept = %v, want %v", got, tc.wantCmpFast)
			}
			if got := fp.FuncFast != nil; got != tc.wantFuncFast {
				t.Errorf("FuncFast kept = %v, want %v", got, tc.wantFuncFast)
			}
			if got := fp.BoolFast != nil; got != tc.wantBoolFast {
				t.Errorf("BoolFast kept = %v, want %v", got, tc.wantBoolFast)
			}
			if fp.PathSteps != nil || fp.CmpFast != nil && fp.CmpFast.LHSPathSteps != nil ||
				fp.FuncFast != nil && fp.FuncFast.PathSteps != nil {
				t.Errorf("a step walk is kept: %+v", fp)
			}
			if got := boolFuncLeaves(t, fp.BoolFast); got != tc.wantBoolFuncs {
				t.Errorf("BoolFast keeps %d function leaves, want %d", got, tc.wantBoolFuncs)
			}
		})
	}
}

// boolFuncLeaves counts the function leaves of b, failing the test on any
// leaf that keeps a step walk.
func boolFuncLeaves(t *testing.T, b *parser.BoolFastPath) int {
	t.Helper()
	if b == nil {
		return 0
	}
	if b.PureSteps != nil || b.Cmp != nil && b.Cmp.LHSPathSteps != nil || b.Func != nil && b.Func.PathSteps != nil {
		t.Errorf("a step walk is kept in %+v", b)
	}
	n := boolFuncLeaves(t, b.Left) + boolFuncLeaves(t, b.Right)
	if b.Func != nil {
		n++
	}
	return n
}
