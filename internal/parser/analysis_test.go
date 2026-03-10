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
