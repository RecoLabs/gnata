// Command evalbytes shows Tier 2 evaluation: evaluating directly against raw
// JSON bytes via the zero-copy GJSON fast path, with no intermediate
// unmarshal for fast-path-eligible expressions.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/recolabs/gnata"
)

func main() {
	expr, err := gnata.Compile(`user.email = "admin@example.com"`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("comparison fast path:", expr.IsComparisonFastPath()) // true

	rawJSON := json.RawMessage(`{"user": {"email": "admin@example.com"}}`)
	result, err := expr.EvalBytes(context.Background(), rawJSON)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result) // true
}
