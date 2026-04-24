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
	{"string", fnString},
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
	{"formatNumber", fnFormatNumber},
	{"formatBase", fnFormatBase},
	{"formatInteger", fnFormatInteger},
	{"parseInteger", fnParseInteger},
	// ── Numeric ───────────────────────────────────────────────────────────────
	{"number", fnNumber},
	{"abs", fnAbs},
	{"floor", fnFloor},
	{"ceil", fnCeil},
	{"round", fnRound},
	{"power", fnPower},
	{"sqrt", fnSqrt},
	{"random", fnRandom},
	{"sum", fnSum},
	{"max", fnMax},
	{"min", fnMin},
	{"average", fnAverage},
	// ── Array ─────────────────────────────────────────────────────────────────
	{"count", fnCount},
	{"append", fnAppend},
	{"reverse", fnReverse},
	{"shuffle", fnShuffle},
	{"distinct", fnDistinct},
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
	{"now", fnNow},
	{"millis", fnMillis},
	{"fromMillis", fnFromMillis},
	{"toMillis", fnToMillis},
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
