// Command customfunc shows registering a domain-specific JSONata function and
// calling it from a standalone expression.
package main

import (
	"context"
	"crypto/md5" //nolint:gosec // demo, not security-sensitive
	"fmt"
	"log"

	"github.com/recolabs/gnata"
)

func main() {
	customFuncs := map[string]gnata.CustomFunc{
		"md5": func(args []any, focus any) (any, error) {
			if len(args) == 0 || args[0] == nil {
				return nil, nil
			}
			h := md5.Sum(fmt.Append(nil, args[0])) //nolint:gosec // demo mirrors the README's custom-function example; not used for security
			return fmt.Sprintf("%x", h), nil
		},
	}

	env := gnata.NewCustomEnv(customFuncs)

	expr, err := gnata.Compile(`$md5(payload.email)`)
	if err != nil {
		log.Fatal(err)
	}

	data := map[string]any{
		"payload": map[string]any{"email": "admin@example.com"},
	}

	result, err := expr.EvalWithCustomFuncs(context.Background(), data, env)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
