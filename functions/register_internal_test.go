package functions

import (
	"testing"

	"github.com/recolabs/gnata/internal/evaluator"
)

// floatOnlyNumeric lists the numeric builtins that deliberately stay float64
// under WithDecimalPrecision.
var floatOnlyNumeric = map[string]string{
	"count":         "a count is a small integer",
	"random":        "a random fraction has no exact decimal value to keep",
	"formatInteger": "integers beyond 2^53 are out of scope",
	"parseInteger":  "integers beyond 2^53 are out of scope",
	"millis":        "millisecond timestamps fit in float64 exactly",
	"toMillis":      "millisecond timestamps fit in float64 exactly",
	"fromMillis":    "millisecond timestamps fit in float64 exactly",
}

// nonNumeric lists the builtins in builtinFuncs that neither compute nor
// compare numbers, so decimal precision does not apply to them.
var nonNumeric = []string{
	"length", "substring", "substringBefore", "substringAfter", "trim", "pad", "contains", "split", "join",
	"encodeUrl", "encodeUrlComponent", "decodeUrl", "decodeUrlComponent", "reverse", "shuffle", "flatten",
	"zip", "keys", "values", "spread", "merge", "error", "boolean", "not", "exists", "assert",
	"type", "now", "base64encode", "base64decode",
}

// A new builtin must be classified for WithDecimalPrecision: given a decimal
// variant in decimalFuncs, or listed above as float-only or non-numeric.
func TestBuiltinsClassifiedForDecimalPrecision(t *testing.T) {
	decimal := make(map[string]bool, len(decimalFuncs))
	for _, b := range decimalFuncs {
		decimal[b.name] = true
	}
	classified := make(map[string]bool, len(nonNumeric))
	for _, name := range nonNumeric {
		classified[name] = true
	}
	for name := range floatOnlyNumeric {
		classified[name] = true
	}
	for _, b := range builtinFuncs {
		switch {
		case decimal[b.name]:
			t.Errorf("$%s is in both builtinFuncs and decimalFuncs", b.name)
		case !classified[b.name]:
			t.Errorf("$%s is not classified for decimal precision: add a decimal variant or list it here", b.name)
		}
	}
}

// Every builtin in jsSpecs must be registered, with its context signature
// and validation, so a typo cannot silently drop either.
func TestJSSpecsAreRegistered(t *testing.T) {
	env := evaluator.NewEnvironment()
	RegisterAll(env, evaluator.ApplyFunction)
	for name, spec := range jsSpecs {
		fn, _ := env.Lookup(name)
		sb, ok := fn.(*evaluator.SignedBuiltin)
		if !ok {
			t.Fatalf("$%s is not registered: %T", name, fn)
		}
		if (spec.sig != "") != (sb.Context != nil) {
			t.Fatalf("$%s: context signature %q, bound context %v", name, spec.sig, sb.Context)
		}
		if spec.validate != (sb.ParsedSig != nil) {
			t.Fatalf("$%s: validated is %v, want %v", name, sb.ParsedSig != nil, spec.validate)
		}
	}
}
