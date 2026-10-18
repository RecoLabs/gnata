package evaluator

import (
	"testing"

	"github.com/recolabs/gnata/internal/parser"
)

// TestMayInjectAgreesWithInject checks MayInject against Inject for every
// combination of argument types, so the static check can't drift from the
// matcher it mirrors.
func TestMayInjectAgreesWithInject(t *testing.T) {
	values := map[byte]any{
		's': "x", 'n': 1.0, 'b': true, 'l': Null, 'a': []any{}, 'o': NewOrderedMap(),
		'f': &Lambda{}, 'm': nil,
	}
	signatures := []string{
		"x-b?:s", "s-nn?:s", "s-(sf):b", "s-(sf)(sf)n?:s", "s-s?:n", "n-s?s?:s",
		"(oa)-:o", "s-:s", "a:n", "af:a", "o-f?:o", "x-:b", "s?s?:s", "(sao)-s?:a",
	}
	for _, signature := range signatures {
		specs, err := parser.ParseSig(signature)
		if err != nil {
			t.Fatalf("ParseSig(%q): %v", signature, err)
		}
		sig := compileSignature(specs)
		for nArgs := range 4 {
			forEachSymbols(nArgs, func(symbols []byte) {
				args := make([]any, len(symbols))
				sets := make([]uint8, len(symbols))
				for i, symbol := range symbols {
					args[i] = values[symbol]
					sets[i] = symbolBit(symbol)
				}
				injected, err := sig.Inject(args, &OrderedMap{})
				want := err != nil || len(injected) != len(args)
				if got := sig.MayInject(sets); got != want {
					t.Fatalf("%s with %q: MayInject = %v, Inject injected = %v", signature, symbols, got, want)
				}
			})
		}
	}
}

func TestMayInjectUnknownArgument(t *testing.T) {
	specs, err := parser.ParseSig("s-nn?:s")
	if err != nil {
		t.Fatal(err)
	}
	sig := compileSignature(specs)
	// $substring(x, 1): a number x leaves the string to the context.
	if !sig.MayInject([]uint8{ArgSymbols(0), ArgSymbols('n')}) {
		t.Fatal("MayInject = false for an argument that may be a number")
	}
	if sig.MayInject([]uint8{ArgSymbols('s'), ArgSymbols('n')}) {
		t.Fatal("MayInject = true for a string literal in the context's place")
	}
}

func forEachSymbols(n int, fn func([]byte)) {
	symbols := make([]byte, n)
	var walk func(int)
	walk = func(i int) {
		if i == n {
			fn(symbols)
			return
		}
		for j := range len(sigSymbols) {
			symbols[i] = sigSymbols[j]
			walk(i + 1)
		}
	}
	walk(0)
}
