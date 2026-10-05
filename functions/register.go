// Package functions implements the JSONata 2.x standard library.
package functions

import (
	"fmt"

	"github.com/recolabs/gnata/internal/evaluator"
)

// EvalFn is a callback used by higher-order functions to invoke a lambda or
// builtin function value without creating an import cycle. The env parameter
// carries the per-evaluation call counter, ensuring concurrent Eval calls
// don't share stack-depth state.
type EvalFn func(fn any, args []any, focus any, env *evaluator.Environment) (any, error)

// builtinFuncs lists all plain BuiltinFunction registrations (name → func).
var builtinFuncs = []struct {
	name string
	fn   func([]any, any) (any, error)
}{
	// ── String ────────────────────────────────────────────────────────────────
	{"length", fnLength},
	{"substring", fnSubstring},
	{"substringBefore", fnSubstringBefore},
	{"substringAfter", fnSubstringAfter},
	{"trim", fnTrim},
	{"pad", fnPad},
	{"contains", fnContains},
	{"split", fnSplit},
	{"join", fnJoin},
	{"base64encode", fnBase64Encode},
	{"base64decode", fnBase64Decode},
	{"encodeUrl", fnEncodeURL},
	{"encodeUrlComponent", fnEncodeURLComponent},
	{"decodeUrl", fnDecodeURL},
	{"decodeUrlComponent", fnDecodeURLComponent},
	{"formatInteger", fnFormatInteger},
	{"parseInteger", fnParseInteger},
	// ── Numeric ───────────────────────────────────────────────────────────────
	{"random", fnRandom},
	// ── Array ─────────────────────────────────────────────────────────────────
	{"count", fnCount},
	{"reverse", fnReverse},
	{"shuffle", fnShuffle},
	{"flatten", fnFlatten},
	{"zip", fnZip},
	// ── Object ────────────────────────────────────────────────────────────────
	{"keys", fnKeys},
	{"values", fnValues},
	{"spread", fnSpread},
	{"merge", fnMerge},
	{"error", fnError},
	{"lookup", fnLookup},
	// ── Boolean ───────────────────────────────────────────────────────────────
	{"boolean", fnBoolean},
	{"not", fnNot},
	{"exists", fnExists},
	// ── Misc ──────────────────────────────────────────────────────────────────
	{"assert", fnAssert},
	{"type", fnTypeOf},
	// ── Date / Time ───────────────────────────────────────────────────────────
	{"fromMillis", fnFromMillis},
}

// decimalFuncs lists numeric builtins with a decimal variant used under WithDecimalPrecision.
var decimalFuncs = []struct {
	name string
	fn   func([]any, any) (any, error)
	dec  decimalFn
}{
	{"number", fnNumber, decNumber},
	{"string", fnString, decString},
	{"abs", fnAbs, decAbs},
	{"floor", fnFloor, decFloor},
	{"ceil", fnCeil, decCeil},
	{"round", fnRound, decRound},
	{"sum", fnSum, decSum},
	{"max", fnMax, decMax},
	{"min", fnMin, decMin},
	{"average", fnAverage, decAverage},
	{"formatNumber", fnFormatNumber, decFormatNumber},
	{"formatBase", fnFormatBase, decFormatBase},
	{"power", fnPower, decPower},
	{"sqrt", fnSqrt, decSqrt},
	{"distinct", fnDistinct, decDistinct},
}

// contextSigs holds the jsonata-js signatures of the builtins with a
// parameter that defaults to the context value ('-'), as bound in
// https://github.com/jsonata-js/jsonata/blob/v2.1.0/src/jsonata.js
var contextSigs = map[string]string{
	"string":             "x-b?:s",
	"substring":          "s-nn?:s",
	"substringBefore":    "s-s:s",
	"substringAfter":     "s-s:s",
	"lowercase":          "s-:s",
	"uppercase":          "s-:s",
	"length":             "s-:n",
	"trim":               "s-:s",
	"pad":                "s-ns?:s",
	"match":              "s-f<s:o>n?:a<o>",
	"contains":           "s-(sf):b",
	"replace":            "s-(sf)(sf)n?:s",
	"split":              "s-(sf)n?:a<s>",
	"formatNumber":       "n-so?:s",
	"formatBase":         "n-n?:s",
	"formatInteger":      "n-s:s",
	"parseInteger":       "s-s:n",
	"number":             "(nsb)-:n",
	"floor":              "n-:n",
	"ceil":               "n-:n",
	"round":              "n-n?:n",
	"abs":                "n-:n",
	"sqrt":               "n-:n",
	"power":              "n-n:n",
	"boolean":            "x-:b",
	"not":                "x-:b",
	"sift":               "o-f?:o",
	"keys":               "x-:a<s>",
	"lookup":             "x-s:x",
	"spread":             "x-:a<o>",
	"each":               "o-f:a",
	"base64encode":       "s-:s",
	"base64decode":       "s-:s",
	"encodeUrlComponent": "s-:s",
	"encodeUrl":          "s-:s",
	"decodeUrlComponent": "s-:s",
	"decodeUrl":          "s-:s",
	"toMillis":           "s-s?:n",
	"fromMillis":         "n-s?s?:s",
}

// validatedBuiltins are checked against their signature at direct call sites.
var validatedBuiltins = map[string]bool{"uppercase": true, "lowercase": true}

// bind binds fn into env, wrapped with its signature when it has one.
func bind(env *evaluator.Environment, name string, fn evaluator.EnvAwareBuiltin) {
	sig, signed := contextSigs[name]
	if !signed {
		env.Bind(name, fn)
		return
	}
	sb, err := evaluator.NewSignedBuiltin(fn, sig, validatedBuiltins[name])
	if err != nil {
		panic(fmt.Sprintf("$%s: %v", name, err))
	}
	env.Bind(name, sb)
}

// bindPlain is bind for a builtin that does not need the environment.
func bindPlain(env *evaluator.Environment, name string, fn func([]any, any) (any, error)) {
	if _, signed := contextSigs[name]; !signed {
		env.Bind(name, evaluator.BuiltinFunction(fn))
		return
	}
	bind(env, name, func(args []any, focus any, _ *evaluator.Environment) (any, error) {
		return fn(args, focus)
	})
}

// RegisterAll binds every JSONata built-in function into env.
// evalFn must call evaluator.ApplyFunction (supplied by gnata.go).
func RegisterAll(env *evaluator.Environment, evalFn EvalFn) {
	for _, b := range builtinFuncs {
		bindPlain(env, b.name, b.fn)
	}
	for _, b := range decimalFuncs {
		bind(env, b.name, withDecimal(b.fn, b.dec))
	}
	bind(env, "append", fnAppend)
	bind(env, "now", fnNow)
	bind(env, "millis", fnMillis)
	bind(env, "toMillis", fnToMillis)
	bindPlain(env, "uppercase", fnUppercase)
	bindPlain(env, "lowercase", fnLowercase)
	bind(env, "match", makeFnMatch(evalFn))
	bind(env, "replace", makeFnReplace(evalFn))
	bind(env, "eval", makeFnEval())
	bind(env, "sort", makeFnSort(evalFn))
	bind(env, "sift", makeFnSift(evalFn))
	bind(env, "each", makeFnEach(evalFn))
	bind(env, "map", makeFnMap(evalFn))
	bind(env, "filter", makeFnFilter(evalFn))
	bind(env, "single", makeFnSingle(evalFn))
	bind(env, "reduce", makeFnReduce(evalFn))
}
