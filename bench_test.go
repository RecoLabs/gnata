package gnata_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// wideArraysData builds a document with two nested array boundaries
// (Order -> Product), large enough that a regression back to full-document
// decoding shows up clearly in both timing and alloc count — the small
// benchData fixture above is too tiny for that delta to be obvious.
func wideArraysData(orders, products int) []byte {
	var sb strings.Builder
	sb.WriteString(`{"Account":{"Order":[`)
	for i := range orders {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"OrderID":"o%d","Product":[`, i)
		for j := range products {
			if j > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"SKU":"sku-%d-%d","UnitPrice":%d.5}`, i, j, j)
		}
		sb.WriteString("]}")
	}
	sb.WriteString("]}}")
	return []byte(sb.String())
}

var wideArraysExprs = []string{
	"Account.Order.Product.SKU",
	"$sum(Account.Order.Product.UnitPrice)",
	`Account.Order.Product.SKU = "sku-19-4"`,
	`$exists(Account.Order.Product.SKU)`,
	`$contains(Account.Order.Product.SKU, "sku-19-4")`,
}

// BenchmarkEvalBytes_WideArrays exercises EvalBytes on expressions whose path
// crosses two array boundaries before reaching the target field. These stay
// on the gjson fast path (no full-document decode) as of the walker added in
// path_bytes.go; regressing back to a decode fallback here should show up as
// a large jump in both ns/op and allocs/op.
func BenchmarkEvalBytes_WideArrays(b *testing.B) {
	rawData := json.RawMessage(wideArraysData(20, 5))
	for _, exprStr := range wideArraysExprs {
		expr, err := gnata.Compile(exprStr)
		if err != nil {
			b.Fatalf("compile %q: %v", exprStr, err)
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
