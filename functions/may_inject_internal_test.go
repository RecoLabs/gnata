package functions

import (
	"errors"
	"slices"
	"testing"

	"github.com/recolabs/gnata/internal/evaluator"
)

// TestMayInjectAgreesWithEveryBuiltin checks Signature.MayInject against
// what each registered builtin's call actually does with its arguments,
// through Validate or Inject as the evaluator chooses, for every combination
// of argument types.
func TestMayInjectAgreesWithEveryBuiltin(t *testing.T) {
	env := evaluator.NewEnvironment()
	RegisterAll(env, evaluator.ApplyFunction)
	values := map[byte]any{
		's': "x", 'n': 1.0, 'b': true, 'l': evaluator.Null, 'a': []any{}, 'o': evaluator.NewOrderedMap(),
		'f': &evaluator.Lambda{}, 'm': nil,
	}
	symbols := []byte("asnblfom")
	for name := range jsSpecs {
		fn, ok := env.LookupDirect(name)
		builtin, signed := fn.(*evaluator.SignedBuiltin)
		if !ok || !signed || builtin.Signature == nil {
			continue
		}
		for nArgs := range 4 {
			for combination := range pow(len(symbols), nArgs) {
				args := make([]any, nArgs)
				sets := make([]uint8, nArgs)
				for i := range nArgs {
					symbol := symbols[combination%len(symbols)]
					combination /= len(symbols)
					args[i] = values[symbol]
					sets[i] = evaluator.ArgSymbols(symbol)
				}
				focus := evaluator.NewOrderedMap()
				var out []any
				var err error
				if builtin.Validated {
					out, err = builtin.Signature.Validate(args, focus)
				} else {
					out, err = builtin.Signature.Inject(args, focus)
				}
				var jsonataErr *evaluator.JSONataError
				injected := slices.ContainsFunc(out, func(arg any) bool { return arg == any(focus) }) ||
					errors.As(err, &jsonataErr) && jsonataErr.Code == "T0411"
				if got := builtin.Signature.MayInject(sets); injected && !got {
					t.Fatalf("$%s with %v: the context is injected but MayInject = false", name, args)
				}
			}
		}
	}
}

func pow(base, exp int) int {
	result := 1
	for range exp {
		result *= base
	}
	return result
}
