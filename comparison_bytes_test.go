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
