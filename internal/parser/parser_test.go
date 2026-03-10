package parser_test

import (
	"testing"

	"github.com/recolabs/gnata/internal/parser"
)

func mustParse(t *testing.T, src string) *parser.Node {
	t.Helper()
	p := parser.NewParser(src)
	node, err := p.Parse()
	if err != nil {
		t.Fatalf("parse(%q) error: %v", src, err)
	}
	node, err = parser.ProcessAST(node)
	if err != nil {
		t.Fatalf("processAST(%q) error: %v", src, err)
	}
	return node
}

func TestParser(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		wantType  string
		wantValue string
	}{
		{"simple name", "Account", "name", "Account"},
		{"dot path", "Account.Name", "path", ""},
		{"number", "42", "number", ""},
		{"string", `"hello"`, "string", "hello"},
		{"true", "true", "value", "true"},
		{"false", "false", "value", "false"},
		{"null", "null", "value", "null"},
		{"variable", "$x", "variable", "x"},
		{"wildcard", "*", "wildcard", "*"},
		{"descendant", "**", "descendant", "**"},
		{"array ctor", "[1,2,3]", "unary", "["},
		{"obj ctor", `{"a": 1}`, "unary", "{"},
		{"addition", "1+2", parser.NodeBinary, "+"},
		{"subtraction", "5-3", parser.NodeBinary, "-"},
		{"multiplication", "2*3", parser.NodeBinary, "*"},
		{"division", "10/2", parser.NodeBinary, "/"},
		{"modulo", "10%3", parser.NodeBinary, "%"},
		{"concat", `"a"&"b"`, parser.NodeBinary, "&"},
		{"comparison eq", `x = "y"`, parser.NodeBinary, "="},
		{"comparison ne", "a != b", parser.NodeBinary, "!="},
		{"comparison lt", "a < b", parser.NodeBinary, "<"},
		{"comparison gt", "a > b", parser.NodeBinary, ">"},
		{"comparison le", "a <= b", parser.NodeBinary, "<="},
		{"comparison ge", "a >= b", parser.NodeBinary, ">="},
		{"and", "a and b", parser.NodeBinary, "and"},
		{"or", "a or b", parser.NodeBinary, "or"},
		{"in", `"x" in arr`, parser.NodeBinary, "in"},
		{"assign", "$x := 1", "bind", ""},
		{"range", "1..10", parser.NodeBinary, ".."},
		{"chain", "expr ~> $fn", parser.NodeBinary, "~>"},
		{"coalesce", "a ?? b", parser.NodeBinary, "??"},
		{"function call", "$sum(items)", "function", "sum"},
		{"nested path", "a.b.c", "path", ""},
		{"paren unwrap", "(42)", "block", ""},
		{"unary minus", "-5", "number", ""},
		{"empty array", "[]", "unary", "["},
		{"empty object", "{}", "unary", "{"},
		{"variable root", "$", "variable", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := mustParse(t, tt.src)
			if node.Type != tt.wantType {
				t.Errorf("type: want %q, got %q", tt.wantType, node.Type)
			}
			if tt.wantValue != "" && node.Value != tt.wantValue {
				t.Errorf("value: want %q, got %q", tt.wantValue, node.Value)
			}
		})
	}
}

func TestPathSteps(t *testing.T) {
	node := mustParse(t, "a.b.c")
	if node.Type != "path" {
		t.Fatalf("expected path, got %q", node.Type)
	}
	if len(node.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(node.Steps))
	}
	wantNames := []string{"a", "b", "c"}
	for i, step := range node.Steps {
		if step.Type != "name" {
			t.Errorf("step[%d]: expected name, got %q", i, step.Type)
		}
		if step.Value != wantNames[i] {
			t.Errorf("step[%d]: expected %q, got %q", i, wantNames[i], step.Value)
		}
	}
}

func TestFunctionCall(t *testing.T) {
	node := mustParse(t, "$sum(1, 2, 3)")
	if node.Type != "function" {
		t.Fatalf("expected function, got %q", node.Type)
	}
	if node.Procedure == nil {
		t.Fatal("procedure is nil")
	}
	if node.Procedure.Value != "sum" {
		t.Errorf("procedure value: want %q, got %q", "sum", node.Procedure.Value)
	}
	if len(node.Arguments) != 3 {
		t.Errorf("expected 3 arguments, got %d", len(node.Arguments))
	}
}

func TestLambda(t *testing.T) {
	node := mustParse(t, "function($x, $y) { $x + $y }")
	if node.Type != "lambda" {
		t.Fatalf("expected lambda, got %q", node.Type)
	}
	if len(node.Arguments) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(node.Arguments))
	}
	if node.Arguments[0].Value != "x" {
		t.Errorf("param 0: want %q, got %q", "x", node.Arguments[0].Value)
	}
	if node.Arguments[1].Value != "y" {
		t.Errorf("param 1: want %q, got %q", "y", node.Arguments[1].Value)
	}
	if node.Body == nil {
		t.Fatal("body is nil")
	}
	const expectedPlus = "+"
	if node.Body.Type != parser.NodeBinary || node.Body.Value != expectedPlus {
		t.Errorf("body: expected binary +, got type=%q value=%q", node.Body.Type, node.Body.Value)
	}
}

func TestConditional(t *testing.T) {
	node := mustParse(t, "a ? b : c")
	if node.Type != "condition" {
		t.Fatalf("expected condition, got %q", node.Type)
	}
	if node.Condition == nil || node.Then == nil || node.Else == nil {
		t.Fatal("condition/then/else must not be nil")
	}
	if node.Condition.Value != "a" {
		t.Errorf("condition: want a, got %q", node.Condition.Value)
	}
	if node.Then.Value != "b" {
		t.Errorf("then: want b, got %q", node.Then.Value)
	}
	if node.Else.Value != "c" {
		t.Errorf("else: want c, got %q", node.Else.Value)
	}
}

func TestBlock(t *testing.T) {
	node := mustParse(t, "($a := 1; $b := 2; $a + $b)")
	if node.Type != "block" {
		t.Fatalf("expected block, got %q", node.Type)
	}
	if len(node.Expressions) != 3 {
		t.Errorf("expected 3 block expressions, got %d", len(node.Expressions))
	}
}

func TestArraySubscript(t *testing.T) {
	node := mustParse(t, "items[0]")
	if node.Type != parser.NodeBinary || node.Value != "[" {
		t.Fatalf("expected binary[, got type=%q value=%q", node.Type, node.Value)
	}
	if node.Left.Value != "items" {
		t.Errorf("left: want items, got %q", node.Left.Value)
	}
}

func TestKeepArray(t *testing.T) {
	node := mustParse(t, "items[]")
	if node.Type != "name" {
		t.Fatalf("expected name node with KeepArray, got %q", node.Type)
	}
	if !node.KeepArray {
		t.Error("expected KeepArray=true")
	}
}

func TestSortExpr(t *testing.T) {
	node := mustParse(t, `items^(price, >discount)`)
	if node.Type != "sort" {
		t.Fatalf("expected sort, got %q", node.Type)
	}
	if len(node.Terms) != 2 {
		t.Fatalf("expected 2 sort terms, got %d", len(node.Terms))
	}
	if node.Terms[0].Descending {
		t.Error("term[0] should be ascending")
	}
	if !node.Terms[1].Descending {
		t.Error("term[1] should be descending")
	}
}

func TestOperatorPrecedence(t *testing.T) {
	// 1 + 2 * 3 should parse as 1 + (2 * 3).
	node := mustParse(t, "1 + 2 * 3")
	if node.Type != parser.NodeBinary || node.Value != "+" {
		t.Fatalf("expected +, got %q %q", node.Type, node.Value)
	}
	right := node.Right
	if right.Type != parser.NodeBinary || right.Value != "*" {
		t.Errorf("right should be *, got %q %q", right.Type, right.Value)
	}
}

func TestAndOrPrecedence(t *testing.T) {
	// a or b and c should parse as a or (b and c).
	node := mustParse(t, "a or b and c")
	if node.Type != parser.NodeBinary || node.Value != "or" {
		t.Fatalf("expected or, got %q %q", node.Type, node.Value)
	}
	right := node.Right
	if right.Type != parser.NodeBinary || right.Value != "and" {
		t.Errorf("right should be 'and', got %q %q", right.Type, right.Value)
	}
}

func TestPartialApplication(t *testing.T) {
	node := mustParse(t, "$add(?, 2)")
	if node.Type != "partial" {
		t.Fatalf("expected partial, got %q", node.Type)
	}
	if len(node.Arguments) != 2 {
		t.Errorf("expected 2 arguments, got %d", len(node.Arguments))
	}
	if node.Arguments[0].Type != "operator" || node.Arguments[0].Value != "?" {
		t.Errorf("first arg should be placeholder, got %q %q", node.Arguments[0].Type, node.Arguments[0].Value)
	}
}

func TestRegex(t *testing.T) {
	node := mustParse(t, "/[a-z]+/i")
	if node.Type != "regex" {
		t.Fatalf("expected regex, got %q", node.Type)
	}
	// Value should contain pattern (and flags).
	if node.Value == "" {
		t.Error("regex value should not be empty")
	}
}

func TestObjectConstructor(t *testing.T) {
	node := mustParse(t, `{"a": 1, "b": 2}`)
	if node.Type != "unary" || node.Value != "{" {
		t.Fatalf("expected unary {, got %q %q", node.Type, node.Value)
	}
	// LHS has flat [k,v,k,v] pairs.
	if len(node.LHS) != 4 {
		t.Errorf("expected 4 LHS entries (2 pairs), got %d", len(node.LHS))
	}
}
