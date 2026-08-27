// Command basic shows Tier 1 evaluation: compiling a JSONata expression once
// and running it against a pre-decoded Go value.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/recolabs/gnata"
)

func main() {
	expr, err := gnata.Compile(`Account.Order.Product[UnitPrice > 50].SKU`)
	if err != nil {
		log.Fatal(err)
	}

	data := map[string]any{
		"Account": map[string]any{
			"Order": []any{
				map[string]any{"Product": map[string]any{"SKU": "0406654608", "UnitPrice": 68.45}},
				map[string]any{"Product": map[string]any{"SKU": "040657863", "UnitPrice": 21.67}},
			},
		},
	}

	result, err := expr.Eval(context.Background(), data)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result) // 0406654608
}
