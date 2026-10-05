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

// jsSpec is how jsonata-js declares a builtin, as bound in
// https://github.com/jsonata-js/jsonata/blob/v2.2.2/src/jsonata.js
type jsSpec struct {
	// arity is the number of parameters the jsonata-js implementation
	// declares, which is how many arguments a HOF passes it as a callback.
	arity int
	// sig is the jsonata-js signature when a parameter defaults to the
	// context value ('-').
	sig string
	// validate checks the arguments against sig, as jsonata-js's
	// validateArguments does.
	validate bool
}

// jsSpecs declares every registered builtin.
var jsSpecs = map[string]jsSpec{
	"sum":                {arity: 1},
	"count":              {arity: 1},
	"max":                {arity: 1},
	"min":                {arity: 1},
	"average":            {arity: 1},
	"string":             {arity: 1, sig: "x-b?:s"},
	"substring":          {arity: 3, sig: "s-nn?:s"},
	"substringBefore":    {arity: 2, sig: "s-s:s"},
	"substringAfter":     {arity: 2, sig: "s-s:s"},
	"lowercase":          {arity: 1, sig: "s-:s", validate: true},
	"uppercase":          {arity: 1, sig: "s-:s", validate: true},
	"length":             {arity: 1, sig: "s-:n"},
	"trim":               {arity: 1, sig: "s-:s"},
	"pad":                {arity: 3, sig: "s-ns?:s"},
	"match":              {arity: 3, sig: "s-f<s:o>n?:a<o>"},
	"contains":           {arity: 2, sig: "s-(sf):b"},
	"replace":            {arity: 4, sig: "s-(sf)(sf)n?:s"},
	"split":              {arity: 3, sig: "s-(sf)n?:a<s>"},
	"join":               {arity: 2},
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
	"random":             {arity: 0},
	"boolean":            {arity: 1, sig: "x-:b"},
	"not":                {arity: 1, sig: "x-:b"},
	"map":                {arity: 2},
	"zip":                {arity: 0},
	"filter":             {arity: 2},
	"single":             {arity: 2},
	"reduce":             {arity: 3},
	"sift":               {arity: 2, sig: "o-f?:o"},
	"keys":               {arity: 1, sig: "x-:a<s>"},
	"lookup":             {arity: 2, sig: "x-s:x"},
	"append":             {arity: 2},
	"exists":             {arity: 1},
	"spread":             {arity: 1, sig: "x-:a<o>"},
	"merge":              {arity: 1},
	"reverse":            {arity: 1},
	"each":               {arity: 2, sig: "o-f:a"},
	"error":              {arity: 1},
	"assert":             {arity: 2},
	"type":               {arity: 1},
	"sort":               {arity: 2},
	"shuffle":            {arity: 1},
	"distinct":           {arity: 1},
	"base64encode":       {arity: 1, sig: "s-:s"},
	"base64decode":       {arity: 1, sig: "s-:s"},
	"encodeUrlComponent": {arity: 1, sig: "s-:s"},
	"encodeUrl":          {arity: 1, sig: "s-:s"},
	"decodeUrlComponent": {arity: 1, sig: "s-:s"},
	"decodeUrl":          {arity: 1, sig: "s-:s"},
	"eval":               {arity: 2},
	"toMillis":           {arity: 2, sig: "s-s?:n"},
	"fromMillis":         {arity: 3, sig: "n-s?s?:s"},
	"now":                {arity: 2},
	"millis":             {arity: 0},
	"values":             {arity: 1}, // gnata extension
	"flatten":            {arity: 1}, // gnata extension
}

// bind binds fn into env with its jsonata-js signature and arity.
func bind(env *evaluator.Environment, name string, fn evaluator.EnvAwareBuiltin) {
	spec, declared := jsSpecs[name]
	if !declared {
		panic(fmt.Sprintf("$%s is not declared in jsSpecs", name))
	}
	sb, err := evaluator.NewSignedBuiltin(name, fn, spec.sig, spec.arity, spec.validate)
	if err != nil {
		panic(fmt.Sprintf("$%s: %v", name, err))
	}
	env.Bind(name, sb)
}

// bindPlain is bind for a builtin that does not need the environment.
func bindPlain(env *evaluator.Environment, name string, fn func([]any, any) (any, error)) {
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
