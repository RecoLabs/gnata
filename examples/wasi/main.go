// Package main shows gnata in a WebAssembly (WASI) program built with TinyGo:
//
//	GOEXPERIMENT=nojsonv2 tinygo build -target=wasip1 -scheduler=none -opt=1 -no-debug -o wasi.wasm ./examples/wasi
//	wasmtime wasi.wasm
//
// It prints with the println builtin rather than fmt.Print*: under TinyGo,
// writing through os.Stdout pulls in timers that require a goroutine
// scheduler, which -scheduler=none rules out. gnata itself starts no
// goroutines. The program also builds and runs with the standard toolchain.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/recolabs/gnata"
)

func main() {
	lines, err := run()
	if err != nil {
		println(err.Error())
		return
	}
	for _, line := range lines {
		println(line)
	}
}

func run() ([]string, error) {
	se := gnata.NewStreamEvaluator(nil,
		gnata.WithCustomFunctions(map[string]gnata.CustomFunc{
			"upper": func(args []any, _ any) (any, error) {
				s, _ := args[0].(string)
				return strings.ToUpper(s), nil
			},
		}),
	)

	exprs := []string{
		`user.plan = "enterprise"`,
		`$sum(orders.amount)`,
		`$upper(user.name)`,
		`{"id": user.id, "orders": $count(orders)}`,
	}
	indices := make([]int, 0, len(exprs))
	for _, e := range exprs {
		idx, err := se.Compile(e)
		if err != nil {
			return nil, fmt.Errorf("compile error: %w", err)
		}
		indices = append(indices, idx)
	}

	event := json.RawMessage(`{"user": {"id": "u1", "name": "ada", "plan": "enterprise"}, "orders": [{"amount": 700}, {"amount": 450}]}`)
	results, err := se.EvalMany(context.Background(), event, "user-order-v1", indices)
	if err != nil {
		return nil, fmt.Errorf("eval error: %w", err)
	}
	lines := make([]string, 0, len(results))
	for i, r := range results {
		out := fmt.Sprint(r)
		if obj, ok := r.(*gnata.OrderedMap); ok {
			b, err := obj.MarshalJSON()
			if err != nil {
				return nil, fmt.Errorf("marshal error: %w", err)
			}
			out = string(b)
		}
		lines = append(lines, exprs[i]+" -> "+out)
	}
	return lines, nil
}
