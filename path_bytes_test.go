package gnata_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/recolabs/gnata"
)

const pathBytesTestData = `{
	"Account": {
		"Name": "Firefly",
		"Order": [
			{"OrderID": "order103", "Product": [
				{"SKU": "0406654608", "UnitPrice": 68.45},
				{"SKU": "040657863", "UnitPrice": 107.99}
			]},
			{"OrderID": "order104", "Product": [
				{"SKU": "0406654608", "UnitPrice": 68.45},
				{"SKU": "0406654603", "UnitPrice": 21.67}
			]},
			{"OrderID": "order105", "Product": [], "Note": null},
			{"OrderID": "order106"}
		]
	}
}`

// evalBytesMatchesEvalCase runs one EvalBytes-vs-Eval differential check,
// shared by the pure-path and aggregate-fast test suites below (they differ
// only in which fast-path classifier they assert and the fixture used).
func evalBytesMatchesEvalCase(t *testing.T, expr string, rawData json.RawMessage, decoded any, wantFastPath func(*gnata.Expression) bool) {
	t.Helper()
	compiled, err := gnata.Compile(expr)
	if err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
	if !wantFastPath(compiled) {
		t.Fatalf("Compile(%q): expected the relevant fast-path classifier to be true", expr)
	}

	wantResult, wantErr := compiled.Eval(context.Background(), decoded)
	gotResult, gotErr := compiled.EvalBytes(context.Background(), rawData)

	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("EvalBytes err = %v, Eval err = %v", gotErr, wantErr)
	}
	want := gnata.NormalizeValue(wantResult)
	got := gnata.NormalizeValue(gotResult)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EvalBytes(%q) = %#v, want %#v (from Eval)", expr, got, want)
	}
}

func TestEvalBytes_ArrayAutoMap_MatchesEval(t *testing.T) {
	testCases := []struct {
		desc string
		expr string
	}{
		{desc: "two array boundaries to scalar leaf", expr: "Account.Order.Product.SKU"},
		{desc: "two array boundaries to numeric leaf", expr: "Account.Order.Product.UnitPrice"},
		{desc: "single array boundary", expr: "Account.Order.OrderID"},
		{desc: "field missing on some elements", expr: "Account.Order.Note"},
		{desc: "pure path with no arrays", expr: "Account.Name"},
	}

	rawData := json.RawMessage(pathBytesTestData)
	var decoded any
	if err := json.Unmarshal(rawData, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			evalBytesMatchesEvalCase(t, tC.expr, rawData, decoded, (*gnata.Expression).IsFastPath)
		})
	}
}

func TestEvalBytes_ArrayAutoMap_NoFullDecode(t *testing.T) {
	expr, err := gnata.Compile("Account.Order.Product.SKU")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	rawData := json.RawMessage(pathBytesTestData)

	allocs := testing.AllocsPerRun(20, func() {
		if _, err := expr.EvalBytes(context.Background(), rawData); err != nil {
			t.Fatal(err)
		}
	})
	// The full-decode fallback (json.Decoder into *OrderedMap for every
	// object, plus the AST walk) allocates well over 40 times per call for
	// this fixture. The array-aware walker should stay far below that.
	const maxAllocs = 30
	if allocs > maxAllocs {
		t.Fatalf("EvalBytes allocated %.1f times per call, want <= %d (full decode likely still happening)", allocs, maxAllocs)
	}
}

func TestEvalBytes_AggregateFast_MatchesEval(t *testing.T) {
	testCases := []struct {
		desc string
		expr string
	}{
		{desc: "sum across two array boundaries", expr: "$sum(Account.Order.Product.UnitPrice)"},
		{desc: "count across two array boundaries", expr: "$count(Account.Order.Product.UnitPrice)"},
		{desc: "max across two array boundaries", expr: "$max(Account.Order.Product.UnitPrice)"},
		{desc: "min across two array boundaries", expr: "$min(Account.Order.Product.UnitPrice)"},
		{desc: "average across two array boundaries", expr: "$average(Account.Order.Product.UnitPrice)"},
	}

	rawData := json.RawMessage(pathBytesTestData)
	var decoded any
	if err := json.Unmarshal(rawData, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			evalBytesMatchesEvalCase(t, tC.expr, rawData, decoded, (*gnata.Expression).IsFuncFastPath)
		})
	}
}

// TestEvalBytes_ArrayAutoMap_LiteralKeyWithSpecialChars guards against the
// walker treating a field name as a gjson path expression instead of a
// literal key. gjson.Result.Get interprets '.', '*', '?', '#', '|', '!',
// brackets, and backslash as path syntax — a field literally named "a.b"
// sitting alongside a nested {"a":{"b":...}} would resolve to the wrong
// value (the nested one) if the walker ever called r.Get(step) with the raw
// field name instead of doing a literal-key lookup.
func TestEvalBytes_ArrayAutoMap_LiteralKeyWithSpecialChars(t *testing.T) {
	rawData := json.RawMessage(`{"Items":[{"a.b": 99, "a": {"b": 1}}]}`)
	var decoded any
	if err := json.Unmarshal(rawData, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	expr := `Items."a.b"`

	e, err := gnata.Compile(expr)
	if err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
	if !e.IsFastPath() {
		t.Fatalf("Compile(%q): expected IsFastPath() == true so this exercises the walker", expr)
	}

	want, wantErr := e.Eval(context.Background(), decoded)
	got, gotErr := e.EvalBytes(context.Background(), rawData)
	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("EvalBytes err = %v, Eval err = %v", gotErr, wantErr)
	}
	// gnata.DeepEqual (JSONata equality), not reflect.DeepEqual: EvalBytes
	// represents numbers as json.Number, Eval on already-decoded plain Go
	// values sees float64 — both mean the same JSONata number.
	if !gnata.DeepEqual(got, want) {
		t.Fatalf("EvalBytes(%q) = %#v, want %#v (from Eval) — walker likely misread the literal key as a gjson path", expr, got, want)
	}
}

func TestEvalBytes_ArrayAutoMap_FallsBackOnScalarStep(t *testing.T) {
	// "Name" is a scalar, not an object or array — a further step past it
	// must fall back to full evaluation and still report "undefined", not
	// an incorrect fast-path value.
	expr, err := gnata.Compile("Account.Name.Missing")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	rawData := json.RawMessage(pathBytesTestData)
	got, err := expr.EvalBytes(context.Background(), rawData)
	if err != nil {
		t.Fatalf("EvalBytes: %v", err)
	}
	if got != nil {
		t.Fatalf("EvalBytes(%q) = %#v, want nil (undefined)", "Account.Name.Missing", got)
	}
}
