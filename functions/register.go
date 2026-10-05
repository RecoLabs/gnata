// Package functions implements the JSONata 2.x standard library.
package functions

import (
	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
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
	{"zip", fnZip},
	// ── Object ────────────────────────────────────────────────────────────────
	{"values", fnValues},
	{"merge", fnMerge},
	{"error", fnError},
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

func newSignedBuiltin(fn func([]any, any) (any, error), sig string) *evaluator.SignedBuiltin {
	parsed, _ := parser.ParseSig(sig)
	return &evaluator.SignedBuiltin{Fn: fn, Sig: sig, ParsedSig: parsed}
}

// RegisterAll binds every JSONata built-in function into env.
// evalFn must call evaluator.ApplyFunction (supplied by gnata.go).
func RegisterAll(env *evaluator.Environment, evalFn EvalFn) {
	for _, b := range builtinFuncs {
		env.Bind(b.name, evaluator.BuiltinFunction(b.fn))
	}
	for _, b := range decimalFuncs {
		env.Bind(b.name, withDecimal(b.fn, b.dec))
	}
	env.Bind("append", evaluator.EnvAwareBuiltin(fnAppend))
	env.Bind("flatten", evaluator.EnvAwareBuiltin(fnFlatten))
	env.Bind("keys", evaluator.EnvAwareBuiltin(fnKeys))
	env.Bind("spread", evaluator.EnvAwareBuiltin(fnSpread))
	env.Bind("lookup", evaluator.EnvAwareBuiltin(fnLookup))
	env.Bind("now", evaluator.EnvAwareBuiltin(fnNow))
	env.Bind("millis", evaluator.EnvAwareBuiltin(fnMillis))
	env.Bind("toMillis", evaluator.EnvAwareBuiltin(fnToMillis))
	env.Bind("uppercase", newSignedBuiltin(fnUppercase, "s-:s"))
	env.Bind("lowercase", newSignedBuiltin(fnLowercase, "s-:s"))
	env.Bind("match", makeFnMatch(evalFn))
	env.Bind("replace", makeFnReplace(evalFn))
	env.Bind("eval", makeFnEval())
	env.Bind("sort", makeFnSort(evalFn))
	env.Bind("sift", makeFnSift(evalFn))
	env.Bind("each", makeFnEach(evalFn))
	env.Bind("map", makeFnMap(evalFn))
	env.Bind("filter", makeFnFilter(evalFn))
	env.Bind("single", makeFnSingle(evalFn))
	env.Bind("reduce", makeFnReduce(evalFn))
}
