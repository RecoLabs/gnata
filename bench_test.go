package gnata_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/recolabs/gnata"
)

const (
	benchData = `{
		"Account": {
			"Name": "Firefly",
			"Order": [
				{"OrderID": "order103", "Product": [
					{"SKU": "0406654608", "Description": "Bowler Hat", "UnitPrice": 68.45, "Quantity": 2, "Discount": 0.1},
					{"SKU": "040657863",  "Description": "Cloak",      "UnitPrice": 107.99, "Quantity": 1, "Discount": 0.2}
				]},
				{"OrderID": "order104", "Product": [
					{"SKU": "0406654608", "Description": "Bowler Hat", "UnitPrice": 68.45, "Quantity": 4, "Discount": 0.1},
					{"SKU": "0406654603", "Description": "Trilby",     "UnitPrice": 21.67, "Quantity": 1, "Discount": 0.0}
				]}
			]
		}
	}`
)

var benchExprs = []string{
	"Account.Name",
	"Account.Order.Product.SKU",
	"Account.Order.Product[UnitPrice > 50].SKU",
	"$sum(Account.Order.Product.(UnitPrice * Quantity * (1 - Discount)))",
}

func BenchmarkCompile(b *testing.B) {
	for _, expr := range benchExprs {
		b.Run(expr, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_, err := gnata.Compile(expr)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkEval(b *testing.B) {
	var data any
	if err := json.Unmarshal(json.RawMessage(benchData), &data); err != nil {
		b.Fatal(err)
	}
	for _, exprStr := range benchExprs {
		expr, err := gnata.Compile(exprStr)
		if err != nil {
			b.Logf("skip %q: %v", exprStr, err)
			continue
		}
		b.Run(exprStr, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := expr.Eval(context.Background(), data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkEvalBytes(b *testing.B) {
	rawData := json.RawMessage(benchData)
	for _, exprStr := range benchExprs {
		expr, err := gnata.Compile(exprStr)
		if err != nil {
			b.Logf("skip %q: %v", exprStr, err)
			continue
		}
		b.Run(exprStr, func(b *testing.B) {
			b.SetBytes(int64(len(rawData)))
			b.ReportAllocs()
			for range b.N {
				if _, err := expr.EvalBytes(context.Background(), rawData); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkStreamEvaluator(b *testing.B) {
	exprs := make([]*gnata.Expression, 0, len(benchExprs))
	indices := make([]int, 0, len(benchExprs))
	for _, exprStr := range benchExprs {
		e, err := gnata.Compile(exprStr)
		if err != nil {
			b.Logf("skip %q: %v", exprStr, err)
			continue
		}
		indices = append(indices, len(exprs))
		exprs = append(exprs, e)
	}
	se := gnata.NewStreamEvaluator(exprs)
	rawData := json.RawMessage(benchData)

	b.ResetTimer()
	b.SetBytes(int64(len(rawData)))
	b.ReportAllocs()
	for range b.N {
		if _, err := se.EvalMany(context.Background(), rawData, "bench-schema", indices); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(se.Stats().Hits), "cache-hits")
}
