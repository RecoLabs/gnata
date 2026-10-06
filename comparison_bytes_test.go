package gnata_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/recolabs/gnata"
)

const comparisonBytesTestData = `{
	"Account": {
		"Order": [
			{"OrderID": "order103", "Product": [
				{"SKU": "a"},
				{"SKU": "b"}
			]},
			{"OrderID": "order104", "Product": [
				{"SKU": "c"}
			]},
			{"OrderID": "order105", "Product": []}
		]
	}
}`

func TestEvalBytes_ComparisonAcrossArrays_MatchesEval(t *testing.T) {
	testCases := []struct {
		desc string
		expr string
	}{
		{desc: "equals a value present in the sequence", expr: `Account.Order.Product.SKU = "a"`},
		{desc: "not-equals a value present in the sequence", expr: `Account.Order.Product.SKU != "a"`},
		{desc: "equals a value absent from the sequence", expr: `Account.Order.Product.SKU = "zzz"`},
		{desc: "not-equals a value absent from the sequence", expr: `Account.Order.Product.SKU != "zzz"`},
		{desc: "equals against a genuinely undefined path", expr: `Account.Order.Missing.SKU = "a"`},
		{desc: "not-equals against a genuinely undefined path", expr: `Account.Order.Missing.SKU != "a"`},
		{desc: "single array boundary equals", expr: `Account.Order.OrderID = "order103"`},
		{desc: "single array boundary not-equals", expr: `Account.Order.OrderID != "order103"`},
		{desc: "no arrays at all", expr: `Account.Order[0].OrderID = "order103"`},
	}

	rawData := json.RawMessage(comparisonBytesTestData)
	var decoded any
	if err := json.Unmarshal(rawData, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tC.expr, err)
			}

			wantResult, wantErr := expr.Eval(context.Background(), decoded)
			gotResult, gotErr := expr.EvalBytes(context.Background(), rawData)

			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("EvalBytes err = %v, Eval err = %v", gotErr, wantErr)
			}
			if !reflect.DeepEqual(gotResult, wantResult) {
				t.Fatalf("EvalBytes(%q) = %#v, want %#v (from Eval)", tC.expr, gotResult, wantResult)
			}
		})
	}
}

func TestEvalBytes_ExistsContainsAcrossArrays_MatchesEval(t *testing.T) {
	testCases := []struct {
		desc string
		expr string
	}{
		{desc: "exists across two array boundaries, present", expr: `$exists(Account.Order.Product.SKU)`},
		{desc: "exists across two array boundaries, absent", expr: `$exists(Account.Order.Missing.SKU)`},
		{desc: "contains across two array boundaries, match", expr: `$contains(Account.Order.Product.SKU, "a")`},
		{desc: "contains across two array boundaries, no match", expr: `$contains(Account.Order.Product.SKU, "zzz")`},
	}

	rawData := json.RawMessage(comparisonBytesTestData)
	var decoded any
	if err := json.Unmarshal(rawData, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tC.expr, err)
			}

			wantResult, wantErr := expr.Eval(context.Background(), decoded)
			gotResult, gotErr := expr.EvalBytes(context.Background(), rawData)

			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("EvalBytes err = %v, Eval err = %v", gotErr, wantErr)
			}
			if !reflect.DeepEqual(gotResult, wantResult) {
				t.Fatalf("EvalBytes(%q) = %#v, want %#v (from Eval)", tC.expr, gotResult, wantResult)
			}
		})
	}
}

// TestEvalBytes_KeepArray_MatchesEval checks that the EvalBytes fast paths
// leave a [] suffix to the evaluator.
func TestEvalBytes_KeepArray_MatchesEval(t *testing.T) {
	testCases := []struct {
		desc string
		expr string
	}{
		{desc: "kept field", expr: `Account[]`},
		{desc: "builtin of a kept field", expr: `$type(Account[])`},
		{desc: "kept builtin result", expr: `$keys(Account)[]`},
	}

	rawData := json.RawMessage(comparisonBytesTestData)
	var decoded any
	if err := json.Unmarshal(rawData, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	noFastPath := func(e *gnata.Expression) bool { return !e.IsFastPath() && !e.IsFuncFastPath() }
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			evalBytesMatchesEvalCase(t, tC.expr, rawData, decoded, noFastPath)
		})
	}
}

// TestEvalBytes_DistinctKeepsArray checks that the $distinct fast path, which
// handles only a path that crosses no array, keeps its plain array an array.
func TestEvalBytes_DistinctKeepsArray(t *testing.T) {
	rawData := json.RawMessage(`{"m":["x","x"],"o":{"m":["x","x","y"]}}`)
	var decoded any
	if err := json.Unmarshal(rawData, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	for _, expr := range []string{`$distinct(m)`, `$distinct(o.m)`} {
		t.Run(expr, func(t *testing.T) {
			evalBytesMatchesEvalCase(t, expr, rawData, decoded, (*gnata.Expression).IsFuncFastPath)
		})
	}
}

func TestEvalMap_ArrayAutoMap_MatchesEval(t *testing.T) {
	testCases := []struct {
		desc string
		expr string
	}{
		{desc: "pure path across two array boundaries", expr: "Account.Order.Product.SKU"},
		{desc: "sum-shaped aggregate across two array boundaries", expr: "$count(Account.Order.Product.SKU)"},
		{desc: "comparison across two array boundaries", expr: `Account.Order.Product.SKU = "a"`},
		{desc: "exists across two array boundaries", expr: `$exists(Account.Order.Product.SKU)`},
		{desc: "contains across two array boundaries", expr: `$contains(Account.Order.Product.SKU, "a")`},
	}

	var decoded any
	if err := json.Unmarshal([]byte(comparisonBytesTestData), &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	accountValue := decoded.(map[string]any)["Account"]
	mapData := map[string]json.RawMessage{
		"Account": mustMarshal(t, accountValue),
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tC.expr, err)
			}

			wantResult, wantErr := expr.Eval(context.Background(), decoded)
			gotResult, gotErr := expr.EvalMap(context.Background(), mapData)

			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("EvalMap err = %v, Eval err = %v", gotErr, wantErr)
			}
			want := gnata.NormalizeValue(wantResult)
			got := gnata.NormalizeValue(gotResult)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("EvalMap(%q) = %#v, want %#v (from Eval)", tC.expr, got, want)
			}
		})
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

const loneArrayLeafTestData = `{
	"Records": [
		{"Actor": {"Roles": ["admin"], "Flags": [""], "Groups": [], "Tags": ["a", "b"]}},
		{"Other": 1}
	],
	"Nested": [
		{"Items": [{"Codes": ["x"]}]}
	]
}`

func TestEvalBytes_ComparisonLoneArrayAcrossArrays_MatchesEval(t *testing.T) {
	testCases := []struct {
		desc string
		expr string
	}{
		{desc: "one-element array equals its element", expr: `Records.Actor.Roles = "admin"`},
		{desc: "one-element array not-equals its element", expr: `Records.Actor.Roles != "admin"`},
		{desc: "one-element empty-string array equals empty string", expr: `Records.Actor.Flags = ""`},
		{desc: "one-element empty-string array not-equals empty string", expr: `Records.Actor.Flags != ""`},
		{desc: "empty array equals literal", expr: `Records.Actor.Groups = "admin"`},
		{desc: "two-element array not-equals literal", expr: `Records.Actor.Tags != "a"`},
		{desc: "one-element array behind two array boundaries", expr: `Nested.Items.Codes = "x"`},
		{desc: "one-element array compared to null", expr: `Records.Actor.Roles != null`},
	}

	rawData := json.RawMessage(loneArrayLeafTestData)
	var decoded any
	if err := json.Unmarshal(rawData, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	mapData := map[string]json.RawMessage{}
	for key, value := range decoded.(map[string]any) {
		mapData[key] = mustMarshal(t, value)
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tC.expr, err)
			}
			if !expr.IsComparisonFastPath() {
				t.Fatalf("expected %q to compile as a comparison fast path", tC.expr)
			}

			wantResult, wantErr := expr.Eval(context.Background(), decoded)
			if wantErr != nil {
				t.Fatalf("Eval(%q): %v", tC.expr, wantErr)
			}

			gotBytes, err := expr.EvalBytes(context.Background(), rawData)
			if err != nil {
				t.Fatalf("EvalBytes(%q): %v", tC.expr, err)
			}
			if !reflect.DeepEqual(gotBytes, wantResult) {
				t.Fatalf("EvalBytes(%q) = %#v, want %#v (from Eval)", tC.expr, gotBytes, wantResult)
			}

			gotMap, err := expr.EvalMap(context.Background(), mapData)
			if err != nil {
				t.Fatalf("EvalMap(%q): %v", tC.expr, err)
			}
			if !reflect.DeepEqual(gotMap, wantResult) {
				t.Fatalf("EvalMap(%q) = %#v, want %#v (from Eval)", tC.expr, gotMap, wantResult)
			}
		})
	}
}
