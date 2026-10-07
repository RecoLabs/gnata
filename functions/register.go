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
type EvalFn func(fn any, args []any, env *evaluator.Environment) (any, error)

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
	{"zip", fnZip},
	// ── Object ────────────────────────────────────────────────────────────────
	{"values", fnValues},
	{"merge", fnMerge},
	{"error", fnError},
	// ── Boolean ───────────────────────────────────────────────────────────────
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
}

// jsSpec is how jsonata-js declares a builtin, as bound in
// https://github.com/jsonata-js/jsonata/blob/v2.2.2/src/jsonata.js
type jsSpec struct {
	// arity is the number of parameters the jsonata-js implementation
	// declares, which is how many arguments a HOF passes it as a callback.
	arity int
	// sig is the jsonata-js signature, empty only for a gnata extension
	// jsonata-js lacks. It validates the arguments, as jsonata-js's
	// validateArguments does, and fills a parameter that defaults to the
	// context value ('-').
	sig string
	// lenient checks only the number of arguments and fills the context,
	// leaving their types to the builtin, which checks them itself or, as
	// a gnata extension, accepts more than jsonata-js.
	lenient bool
}

// jsSpecs declares every registered builtin.
var jsSpecs = map[string]jsSpec{
	"sum":                {arity: 1, sig: "a<n>:n"},
	"count":              {arity: 1, sig: "a:n"},
	"max":                {arity: 1, sig: "a<n>:n"},
	"min":                {arity: 1, sig: "a<n>:n"},
	"average":            {arity: 1, sig: "a<n>:n"},
	"string":             {arity: 1, sig: "x-b?:s"},
	"substring":          {arity: 3, sig: "s-nn?:s"},
	"substringBefore":    {arity: 2, sig: "s-s:s"},
	"substringAfter":     {arity: 2, sig: "s-s:s"},
	"lowercase":          {arity: 1, sig: "s-:s"},
	"uppercase":          {arity: 1, sig: "s-:s"},
	"length":             {arity: 1, sig: "s-:n"},
	"trim":               {arity: 1, sig: "s-:s"},
	"pad":                {arity: 3, sig: "s-ns?:s"},
	"match":              {arity: 3, sig: "s-f<s:o>n?:a<o>"},
	"contains":           {arity: 2, sig: "s-(sf):b", lenient: true}, // also searches arrays
	"replace":            {arity: 4, sig: "s-(sf)(sf)n?:s"},
	"split":              {arity: 3, sig: "s-(sf)n?:a<s>"},
	"join":               {arity: 2, sig: "a<s>s?:s"},
	"formatNumber":       {arity: 3, sig: "n-so?:s"},
	"formatBase":         {arity: 2, sig: "n-n?:s"},
	"formatInteger":      {arity: 2, sig: "n-s:s"},
	"parseInteger":       {arity: 2, sig: "s-s:n"},
	"number":             {arity: 1, sig: "(nsb)-:n"},
	"floor":              {arity: 1, sig: "n-:n"},
	"ceil":               {arity: 1, sig: "n-:n"},
	"round":              {arity: 2, sig: "n-n?:n"},
	"abs":                {arity: 1, sig: "n-:n"},
	"sqrt":               {arity: 1, sig: "n-:n"},
	"power":              {arity: 2, sig: "n-n:n"},
	"random":             {arity: 0, sig: ":n"},
	"boolean":            {arity: 1, sig: "x-:b"},
	"not":                {arity: 1, sig: "x-:b"},
	"map":                {arity: 2, sig: "af"},
	"zip":                {arity: 0, sig: "a+"},
	"filter":             {arity: 2, sig: "af"},
	"single":             {arity: 2, sig: "af?"},
	"reduce":             {arity: 3, sig: "afj?:j"},
	"sift":               {arity: 2, sig: "o-f?:o"},
	"keys":               {arity: 1, sig: "x-:a<s>"},
	"lookup":             {arity: 2, sig: "x-s:x"},
	"append":             {arity: 2, sig: "xx:a"},
	"exists":             {arity: 1, sig: "x:b"},
	"spread":             {arity: 1, sig: "x-:a<o>"},
	"merge":              {arity: 1, sig: "a<o>:o"},
	"reverse":            {arity: 1, sig: "a:a"},
	"each":               {arity: 2, sig: "o-f:a"},
	"error":              {arity: 1, sig: "s?:x"},
	"assert":             {arity: 2, sig: "bs?:x"},
	"type":               {arity: 1, sig: "x:s"},
	"sort":               {arity: 2, sig: "af?:a"},
	"shuffle":            {arity: 1, sig: "a:a"},
	"distinct":           {arity: 1, sig: "x:x"},
	"base64encode":       {arity: 1, sig: "s-:s"},
	"base64decode":       {arity: 1, sig: "s-:s"},
	"encodeUrlComponent": {arity: 1, sig: "s-:s"},
	"encodeUrl":          {arity: 1, sig: "s-:s"},
	"decodeUrlComponent": {arity: 1, sig: "s-:s"},
	"decodeUrl":          {arity: 1, sig: "s-:s"},
	"eval":               {arity: 2, sig: "sx?:x"},
	"toMillis":           {arity: 2, sig: "s-s?:n"},
	"fromMillis":         {arity: 3, sig: "n-s?s?:s"},
	"now":                {arity: 2, sig: "s?s?:s"},
	"clone":              {arity: 1, sig: "(oa)-:o"},
	"millis":             {arity: 0, sig: ":n"},
	"values":             {arity: 1}, // gnata extension
	"flatten":            {arity: 1}, // gnata extension
}

// bind binds fn into env with its jsonata-js signature and arity.
func bind(env *evaluator.Environment, name string, fn evaluator.EnvAwareBuiltin) {
	env.Bind(name, newBuiltin(name, fn))
}

// newBuiltin returns fn with its jsonata-js signature and arity.
func newBuiltin(name string, fn evaluator.EnvAwareBuiltin) *evaluator.SignedBuiltin {
	spec, declared := jsSpecs[name]
	if !declared {
		panic(fmt.Sprintf("$%s is not declared in jsSpecs", name))
	}
	sb, err := evaluator.NewSignedBuiltin(name, fn, spec.sig, spec.arity, !spec.lenient)
	if err != nil {
		panic(fmt.Sprintf("$%s: %v", name, err))
	}
	return sb
}

// bindPlain is bind for a builtin that does not need the environment.
func bindPlain(env *evaluator.Environment, name string, fn func([]any, any) (any, error)) {
	sb := newBuiltin(name, func(args []any, focus any, _ *evaluator.Environment) (any, error) {
		return fn(args, focus)
	})
	sb.Plain = fn
	env.Bind(name, sb)
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
	bind(env, "distinct", fnDistinct)
	bind(env, "string", fnString)
	bind(env, "boolean", fnBoolean)
	bind(env, "not", fnNot)
	bind(env, "append", fnAppend)
	bind(env, "now", fnNow)
	bind(env, "millis", fnMillis)
	bind(env, "toMillis", fnToMillis)
	bind(env, "lookup", fnLookup)
	bind(env, "keys", fnKeys)
	bind(env, "spread", fnSpread)
	bind(env, "flatten", fnFlatten)
	bindPlain(env, "uppercase", fnUppercase)
	bindPlain(env, "lowercase", fnLowercase)
	bind(env, "match", makeFnMatch(evalFn))
	bind(env, "replace", makeFnReplace(evalFn))
	bind(env, "eval", makeFnEval())
	bind(env, "clone", fnClone)
	bind(env, "sort", makeFnSort(evalFn))
	bind(env, "sift", makeFnSift(evalFn))
	bind(env, "each", makeFnEach(evalFn))
	bind(env, "map", makeFnMap(evalFn))
	bind(env, "filter", makeFnFilter(evalFn))
	bind(env, "single", makeFnSingle(evalFn))
	bind(env, "reduce", makeFnReduce(evalFn))
}
