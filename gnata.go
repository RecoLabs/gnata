// Package gnata implements the JSONata 2.x query and transformation language for Go.
//
// Quick start:
//
//	expr, err := gnata.Compile(`Account.Order.Product.Price`)
//	result, err := expr.Eval(context.Background(), data)
//
// For high-throughput streaming workloads, use StreamEvaluator which provides
// lock-free schema-keyed plan caching and batched expression evaluation.
package gnata

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/recolabs/gnata/functions"
	"github.com/recolabs/gnata/internal/decimal"
	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
	"github.com/tidwall/gjson"
)

// Expression is a compiled, reusable, goroutine-safe JSONata expression.
type Expression struct {
	src string
	ast *parser.Node
	// fastPath and paths cover pure-path expressions (e.g. "Account.Name").
	fastPath bool
	paths    []string
	// pathSteps holds the un-escaped field names making up paths[0], used to
	// walk the path step-by-step (auto-mapping through arrays) when the
	// single dotted-path gjson lookup below can't resolve it directly.
	pathSteps []string
	// cmpFast covers simple path-vs-literal comparisons (e.g. `a.b = "x"`).
	// Non-nil when the expression qualifies; nil otherwise.
	cmpFast *parser.ComparisonFastPath
	// funcFast covers built-in function calls on a pure path (e.g. `$exists(a.b)`).
	// Non-nil when the expression qualifies; nil otherwise.
	funcFast *parser.FuncFastPath
	boolFast *parser.BoolFastPath
	// options holds the settings passed to Compile; nil when Compile was called
	// without options (default, unlimited float64 behavior).
	options *compileOptions
}

// compileOptions holds the settings configured via Option: the resource
// limits of the jsonata-js 2.2 guardrails API (stack / timeout / sequence) and
// the decimal precision.
type compileOptions struct {
	stack            int
	timeout          time.Duration
	sequence         int
	decimalPrecision int
}

// Option configures a compiled Expression: a resource guardrail (WithStack,
// WithTimeout, WithSequence) or decimal arithmetic (WithDecimalPrecision).
type Option func(*compileOptions)

// WithStack limits the maximum lambda recursion depth. Exceeding it returns
// error D1011. Without this option, gnata still enforces its built-in limit
// of 100 (error U1001) — WithStack only changes the limit and the resulting
// error code, matching jsonata-js's `stack` guardrail.
func WithStack(n int) Option {
	return func(o *compileOptions) { o.stack = n }
}

// WithTimeout limits total evaluation time. Exceeding it returns error
// D1012. Without this option, evaluation is bounded only by the ctx passed
// to Eval, matching jsonata-js's `timeout` guardrail.
//
// The deadline is checked on every function call and periodically between
// expression nodes, without a timer goroutine, so it also applies inside a
// synchronous call on single-threaded WebAssembly hosts. A single builtin or
// custom function call is not interrupted: evaluation can exceed the timeout
// by at most the duration of the call in progress.
func WithTimeout(d time.Duration) Option {
	return func(o *compileOptions) { o.timeout = d }
}

// WithSequence limits the length of sequences built during evaluation: the
// range operator (..), $append, $map, $filter, $each, wildcard (*), and
// descendant (**). Exceeding it returns error D2015, matching jsonata-js's
// `sequence` guardrail. Without this option, only the built-in 10,000,000
// element hard caps (D2014 / D3010) apply.
func WithSequence(n int) Option {
	return func(o *compileOptions) { o.sequence = n }
}

// WithDecimalPrecision enables decimal arithmetic and comparison of numbers,
// rounded half to even to digits significant digits (e.g. 78 for uint256).
// digits must be between 17 and 100; 0 leaves decimal arithmetic off, the
// default.
//
// Numbers computed in decimal are returned as json.Number; others keep their
// type, so results and the arguments of custom functions may be float64 or
// json.Number. A float64 counts as its shortest decimal form (0.1 as 0.1), so
// for input numbers to keep more digits than float64 holds, pass raw JSON to
// EvalBytes, EvalMap or a StreamEvaluator, or decode with
// json.Decoder.UseNumber for Eval.
// Magnitudes are limited to the range of float64, with the same errors.
// Exponentiation with a fractional power is the costliest operation, about a
// millisecond at 100 digits.
func WithDecimalPrecision(digits int) Option {
	return func(o *compileOptions) { o.decimalPrecision = digits }
}

// Compile parses a JSONata expression string and returns an Expression.
// The returned Expression is goroutine-safe and should be reused across calls.
func Compile(expr string, opts ...Option) (*Expression, error) {
	p := parser.NewParser(expr)
	ast, err := p.Parse()
	if err != nil {
		return nil, err
	}
	ast, err = parser.ProcessAST(ast)
	if err != nil {
		return nil, err
	}
	fp := parser.AnalyzeFastPath(ast)
	var o *compileOptions
	if len(opts) > 0 {
		o = &compileOptions{}
		for _, opt := range opts {
			opt(o)
		}
		if o.decimalPrecision != 0 && (o.decimalPrecision < decimal.MinPrecision || o.decimalPrecision > decimal.MaxPrecision) {
			return nil, fmt.Errorf("gnata: WithDecimalPrecision %d must be between %d and %d digits",
				o.decimalPrecision, decimal.MinPrecision, decimal.MaxPrecision)
		}
		if o.decimalPrecision > 0 {
			fp.DecimalSafe()
		}
	}
	return &Expression{
		src:       expr,
		ast:       ast,
		fastPath:  fp.IsFastPath,
		paths:     fp.GJSONPaths,
		pathSteps: fp.PathSteps,
		cmpFast:   fp.CmpFast,
		funcFast:  fp.FuncFast,
		boolFast:  fp.BoolFast,
		options:   o,
	}, nil
}

// CustomFunc is a user-defined function that can be registered with gnata.
// It receives evaluated arguments and the current context value (focus).
// Numbers arrive as float64 or json.Number (always so for numbers computed
// under WithDecimalPrecision); evaluator-style helpers such as a type switch
// on both handle either.
type CustomFunc func(args []any, focus any) (any, error)

// CustomEnvironment is a reusable root environment containing standard library
// functions and caller-provided custom functions.
type CustomEnvironment struct {
	env *evaluator.Environment
}

// NewCustomEnvironment pre-builds a reusable environment for a stable set of
// custom functions. Each evaluation creates a child environment for variables.
func NewCustomEnvironment(customFuncs map[string]CustomFunc) *CustomEnvironment {
	return &CustomEnvironment{env: newEnv(customFuncs, false)}
}

// builtinEnv is a shared root environment with all standard library functions.
// Created once at init; each eval creates a thin child env for per-call bindings.
var builtinEnv *evaluator.Environment

func init() {
	builtinEnv = newEnv(nil, false)
}

// newEnv creates a root environment with all standard library functions
// and optional custom functions registered. With sharedArgs, custom functions
// receive cached, shared normalized views of decoded input objects (see
// WithReadOnlyCustomFuncArgs) instead of a fresh copy per call.
func newEnv(customFuncs map[string]CustomFunc, sharedArgs bool) *evaluator.Environment {
	env := evaluator.NewEnvironment()
	functions.RegisterAll(env, evaluator.ApplyFunction)
	for name, fn := range customFuncs {
		wrapped := wrapCustomFunc(fn, sharedArgs)
		env.Bind(name, evaluator.BuiltinFunction(wrapped))
	}
	return env
}

// wrapCustomFunc wraps a user-provided custom function to normalize
// internal evaluator types (OrderedMap, Null sentinel) into standard
// Go types (map[string]any, nil) before the function sees them.
func wrapCustomFunc(fn CustomFunc, sharedArgs bool) CustomFunc {
	return func(args []any, focus any) (any, error) {
		for i, a := range args {
			args[i] = normalizeValue(a, sharedArgs)
		}
		return fn(args, normalizeValue(focus, sharedArgs))
	}
}

// NormalizeValue converts internal evaluator types to standard Go types.
// OrderedMap becomes map[string]any, the null sentinel becomes nil,
// and slices are recursively normalized only when they contain internal types.
// Scalar values and slices of pure scalars pass through without allocation:
// maps in the result are always freshly allocated and may be modified, but
// slices may be the caller's own and must not be modified in place.
func NormalizeValue(v any) any {
	return normalizeValue(v, false)
}

// normalizeValue implements NormalizeValue. With shared, objects decoded from
// input JSON return a normalized map cached on the object and shared by every
// caller, which must therefore not modify it.
func normalizeValue(v any, shared bool) any {
	if v == nil {
		return nil
	}
	if evaluator.IsNull(v) {
		return nil
	}
	switch val := v.(type) {
	case *evaluator.Sequence:
		return normalizeValue(evaluator.CollapseSequence(val), shared)
	case *evaluator.OrderedMap:
		if shared {
			return evaluator.NormalizedView(val, normalizeOrderedMapShared)
		}
		return normalizeOrderedMap(val, false)
	case []any:
		return normalizeSlice(val, shared)
	case evaluator.ConsArray:
		return normalizeSlice([]any(val), shared)
	}
	return v
}

func normalizeOrderedMapShared(om *evaluator.OrderedMap) map[string]any {
	return normalizeOrderedMap(om, true)
}

func normalizeOrderedMap(om *evaluator.OrderedMap, shared bool) map[string]any {
	m := om.ToMap()
	out := make(map[string]any, len(m))
	for k, mv := range m {
		out[k] = normalizeValue(mv, shared)
	}
	return out
}

// normalizeSlice only allocates a copy when at least one element
// needs conversion (OrderedMap, null sentinel, or nested slice).
func normalizeSlice(s []any, shared bool) any {
	needsCopy := slices.ContainsFunc(s, needsNormalize)
	if !needsCopy {
		return s
	}
	out := make([]any, len(s))
	for i, elem := range s {
		out[i] = normalizeValue(elem, shared)
	}
	return out
}

func needsNormalize(v any) bool {
	if v == nil {
		return false
	}
	switch v.(type) {
	case *evaluator.OrderedMap:
		return true
	case *evaluator.Sequence:
		return true
	case []any, evaluator.ConsArray:
		return true
	}
	return evaluator.IsNull(v)
}

func recoverEvalPanic(errp *error) { //nolint:gocritic // ptrToRefParam: must mutate caller's error via pointer
	if r := recover(); r != nil {
		switch v := r.(type) {
		case *evaluator.JSONataError:
			*errp = v
		case error:
			*errp = v
		default:
			*errp = fmt.Errorf("gnata: unexpected panic: %v", r)
		}
	}
}

// evalCore is the shared evaluation logic for all Eval variants.
func (e *Expression) evalCore(ctx context.Context, data any, parent *evaluator.Environment, vars map[string]any) (result any, err error) {
	defer recoverEvalPanic(&err)
	env := evaluator.NewChildEnvironment(parent)
	env.ResetCallCounter()
	env.SetContext(ctx)
	if e.options != nil {
		if e.options.timeout > 0 {
			env.SetDeadline(time.Now().Add(e.options.timeout))
		}
		if e.options.stack > 0 {
			env.SetMaxStackDepth(e.options.stack)
		}
		if e.options.sequence > 0 {
			env.SetMaxSequence(e.options.sequence)
		}
		if e.options.decimalPrecision > 0 {
			env.SetDecimalPrecision(e.options.decimalPrecision)
		}
	}
	env.Bind("$", data)
	for k, v := range vars {
		env.Bind(k, v)
	}
	result, err = evaluator.Eval(e.ast, data, env)
	if err != nil {
		if env.DeadlineExceeded() {
			return nil, &evaluator.JSONataError{Code: "D1012", Message: fmt.Sprintf("Evaluation timeout after %s", e.options.timeout)}
		}
		return nil, err
	}
	if seq, ok := result.(*evaluator.Sequence); ok {
		result = evaluator.CollapseSequence(seq)
	}
	return evaluator.StripCons(result), nil
}

// Eval evaluates the expression against pre-parsed Go data (map[string]any, []any, scalar, nil).
// Returns (nil, nil) for undefined results.
func (e *Expression) Eval(ctx context.Context, data any) (result any, err error) {
	return e.evalCore(ctx, data, builtinEnv, nil)
}

// tryFastPathBytes attempts the pure-path (including the array-crossing
// walker) and comparison gjson tiers, against either raw bytes or a
// pre-destructured map (exactly one of data / mapData should be non-nil,
// mirroring resolveGjsonPath). Neither tier can ever name a function, so
// this is safe to share across every EvalBytes*/EvalMap variant regardless
// of which vars or custom environment the caller passed — including callers
// whose custom environment shadows a builtin name. handled is false when
// neither tier could resolve the expression.
func (e *Expression) tryFastPathBytes(data json.RawMessage, mapData map[string]json.RawMessage) (result any, handled bool, err error) {
	if e.fastPath && len(e.paths) == 1 {
		if v, ok := resolvePurePath(e.paths[0], e.pathSteps, data, mapData, e.decimalPrecision() > 0); ok {
			return v, true, nil
		}
		// The walker couldn't resolve the path either — fall through to the
		// full evaluator.
	}
	if e.cmpFast != nil {
		if res, ok, evalErr := evalComparison(e.cmpFast, data, mapData); ok || evalErr != nil {
			return res, true, evalErr
		}
	}
	return nil, false, nil
}

// tryFuncFastBytes attempts the built-in-function fast path, against either
// raw bytes or a pre-destructured map. Unlike tryFastPathBytes, this
// dispatches purely on the function's source-text name (parser.FuncFastKind),
// with no reference to which environment the caller actually passed — so it
// is only safe for callers that always evaluate against the standard
// builtinEnv (EvalBytes, EvalBytesWithVars, EvalMap). A caller-supplied
// custom environment can register a function under a name that collides
// with a fast-path-eligible builtin (e.g. "sum", "exists", "contains");
// calling this tier for such a caller would silently run the builtin
// instead of the caller's override. EvalBytesWithCustomFuncs and
// EvalBytesWithCustomEnvironmentAndVars must not call this — they fall
// straight through to the full evaluator for function-fast expressions,
// which correctly consults the caller's environment.
func (e *Expression) tryFuncFastBytes(data json.RawMessage, mapData map[string]json.RawMessage) (result any, handled bool, err error) {
	if e.funcFast != nil {
		if res, ok, evalErr := evalFunc(e.funcFast, data, mapData); ok || evalErr != nil {
			return res, true, evalErr
		}
	}
	if e.boolFast != nil {
		if res, ok, evalErr := evalBool(e.boolFast, data, mapData); ok || evalErr != nil {
			return res, true, evalErr
		}
	}
	return nil, false, nil
}

// EvalBytes evaluates the expression against raw JSON bytes.
//
//   - Pure-path fast path: zero-copy GJSON extraction (e.g. "Account.Name").
//     When the path crosses one or more arrays (e.g. "Account.Order.Product.Price"),
//     a step-by-step gjson walk auto-maps through them without decoding the document.
//   - Comparison fast path: single gjson scan for path-vs-literal comparisons.
//   - Complex expressions: json.Unmarshal + full AST evaluation.
func (e *Expression) EvalBytes(ctx context.Context, data json.RawMessage) (result any, err error) {
	defer recoverEvalPanic(&err)
	if res, handled, fastErr := e.tryFastPathBytes(data, nil); handled || fastErr != nil {
		return res, fastErr
	}
	if res, handled, fastErr := e.tryFuncFastBytes(data, nil); handled || fastErr != nil {
		return res, fastErr
	}
	v, err := evaluator.DecodeInput(data)
	if err != nil {
		return nil, err
	}
	return e.Eval(ctx, v)
}

// EvalMap evaluates the expression against a map of field names to raw JSON values.
// This enables O(1) top-level key lookup with gjson fast paths for nested access,
// making it ideal for pre-destructured data (e.g. database columns, form fields).
func (e *Expression) EvalMap(ctx context.Context, data map[string]json.RawMessage) (result any, err error) {
	defer recoverEvalPanic(&err)
	if res, handled, fastErr := e.tryFastPathBytes(nil, data); handled || fastErr != nil {
		return res, fastErr
	}
	if res, handled, fastErr := e.tryFuncFastBytes(nil, data); handled || fastErr != nil {
		return res, fastErr
	}
	v, err := evaluator.DecodeRawMap(data)
	if err != nil {
		return nil, err
	}
	return e.Eval(ctx, v)
}

// EvalBytesWithVars evaluates the expression against raw JSON bytes with extra
// variable bindings. Combines the gjson fast-path cascade from EvalBytes with
// the variable support from EvalWithVars.
func (e *Expression) EvalBytesWithVars(ctx context.Context, data json.RawMessage, vars map[string]any) (result any, err error) {
	defer recoverEvalPanic(&err)
	if res, handled, fastErr := e.tryFastPathBytes(data, nil); handled || fastErr != nil {
		return res, fastErr
	}
	if res, handled, fastErr := e.tryFuncFastBytes(data, nil); handled || fastErr != nil {
		return res, fastErr
	}
	v, err := evaluator.DecodeInput(data)
	if err != nil {
		return nil, err
	}
	return e.evalCore(ctx, v, builtinEnv, vars)
}

// EvalBytesWithCustomFuncs evaluates raw JSON bytes using a custom
// environment. The env parameter should be created via NewCustomEnv.
// Combines the pure-path/comparison gjson tiers from EvalBytes with the
// custom environment support from EvalWithCustomFuncs. Deliberately skips
// the function fast path (see tryFuncFastBytes): a caller-supplied
// environment can register a function under a name that shadows a
// fast-path-eligible builtin, and only the full evaluator consults env for
// function resolution.
func (e *Expression) EvalBytesWithCustomFuncs(
	ctx context.Context, data json.RawMessage, env *evaluator.Environment,
) (result any, err error) {
	defer recoverEvalPanic(&err)
	if res, handled, fastErr := e.tryFastPathBytes(data, nil); handled || fastErr != nil {
		return res, fastErr
	}
	v, err := evaluator.DecodeInput(data)
	if err != nil {
		return nil, err
	}
	return e.evalCore(ctx, v, env, nil)
}

// EvalBytesWithCustomEnvironmentAndVars evaluates raw JSON bytes with a
// pre-built custom environment and per-call variable bindings. Construct the
// environment once via NewCustomEnvironment and reuse it across evaluations,
// same as EvalWithCustomEnvironmentAndVars. Combines the pure-path/comparison
// gjson tiers from EvalBytes with that decoded-input API's custom-function
// and $-variable support. Deliberately skips the function fast path — see
// EvalBytesWithCustomFuncs and tryFuncFastBytes for why.
func (e *Expression) EvalBytesWithCustomEnvironmentAndVars(
	ctx context.Context,
	data json.RawMessage,
	customEnv *CustomEnvironment,
	vars map[string]any,
) (result any, err error) {
	defer recoverEvalPanic(&err)
	if res, handled, fastErr := e.tryFastPathBytes(data, nil); handled || fastErr != nil {
		return res, fastErr
	}
	v, err := evaluator.DecodeInput(data)
	if err != nil {
		return nil, err
	}
	parent := builtinEnv
	if customEnv != nil {
		parent = customEnv.env
	}
	return e.evalCore(ctx, v, parent, vars)
}

// resolveGjsonPath resolves a gjson path from either raw bytes or a pre-decoded map.
// When data is available (EvalMany), it delegates to gjson.GetBytes on the full blob.
// When mapData is available (EvalMap), it does an O(1) map lookup for the top-level
// key, then uses gjson on the nested value bytes — skipping sibling keys entirely.
// Paths containing gjson special characters fall through to return an empty result,
// letting the caller fall back to full AST evaluation.
func resolveGjsonPath(data json.RawMessage, mapData map[string]json.RawMessage, path string) gjson.Result {
	if data != nil {
		return gjson.GetBytes(data, path)
	}
	if mapData == nil {
		return gjson.Result{}
	}
	if strings.ContainsAny(path, `\#*?@`) {
		return gjson.Result{}
	}
	key, rest, hasDot := strings.Cut(path, ".")
	raw, ok := mapData[key]
	if !ok {
		return gjson.Result{}
	}
	if !hasDot {
		return gjson.ParseBytes(raw)
	}
	return gjson.GetBytes(raw, rest)
}

// resolvePurePath resolves a pure-path fast path against raw bytes or a
// pre-decoded map, walking through arrays when a direct gjson lookup misses.
// ok is false when the walker cannot resolve the path. With useNumber, numbers
// are returned as json.Number.
func resolvePurePath(
	path string, steps []string, data json.RawMessage, mapData map[string]json.RawMessage, useNumber bool,
) (any, bool) {
	if res := resolveGjsonPath(data, mapData, path); res.Exists() {
		return gjsonValue(&res, useNumber), true
	}
	switch {
	case data != nil:
		return walkPureStepsBytes(steps, data, useNumber)
	case mapData != nil:
		return walkPureStepsMapBytes(steps, mapData, useNumber)
	default:
		return nil, false
	}
}

// evalComparison evaluates a pre-compiled comparison fast path against raw JSON
// bytes or a pre-decoded map. Returns (result, true, nil) on success. Returns
// (nil, false, nil) when the expression cannot safely short-circuit (e.g. the
// LHS is a JSON array that requires auto-mapping), signalling the caller to
// fall back to full evaluation.
//

func evalComparison(
	c *parser.ComparisonFastPath, data json.RawMessage, mapData map[string]json.RawMessage,
) (result any, handled bool, err error) {
	lhs := resolveGjsonPath(data, mapData, c.LHSPath)
	if lhs.Exists() {
		match, ok := matchComparison(&lhs, c)
		return match, ok, nil
	}
	// gjson couldn't resolve the path. This could be because the path is
	// truly undefined OR because an intermediate element is a JSON array
	// (gjson doesn't auto-map through arrays, but JSONata does). Walk the
	// path step-by-step so the array case still avoids a full document decode.
	if len(c.LHSPathSteps) == 0 {
		return nil, false, nil
	}
	resolved, ok := walkPureStepsResolved(c.LHSPathSteps, data, mapData)
	if !ok {
		return nil, false, nil
	}
	switch v := resolved.(type) {
	case gjson.Result:
		match, matchOK := matchComparison(&v, c)
		return match, matchOK, nil
	case []gjson.Result:
		if len(v) != 1 {
			// A resolved sequence — whether flattened to more than one element,
			// or to zero elements from a field present as an empty array — is
			// never structurally equal to a scalar literal.
			return c.Op == "!=", true, nil
		}
		match, matchOK := matchComparison(&v[0], c)
		return match, matchOK, nil
	default:
		return nil, false, nil
	}
}

// matchComparison evaluates a single resolved gjson value against a
// pre-compiled comparison literal. JSON arrays and objects are never equal
// to a primitive literal regardless of the literal's type; JSONata's
// equality operator does structural, not any-element, comparison.
// ok is false only for an unrecognized RHSKind, which never occurs today
// since the parser only ever produces the four known kinds.
func matchComparison(lhs *gjson.Result, c *parser.ComparisonFastPath) (match, ok bool) {
	if lhs.Type == gjson.JSON {
		return c.Op == "!=", true
	}
	switch c.RHSKind {
	case parser.RHSKindString:
		match = lhs.Type == gjson.String && lhs.String() == c.RHSString
	case parser.RHSKindNumber:
		if lhs.Type != gjson.Number {
			break
		}
		if isCanonicalInteger(lhs.Raw) && isCanonicalInteger(c.RHSNumberStr) {
			match = lhs.Raw == c.RHSNumberStr
		} else {
			match = lhs.Float() == c.RHSNumber
		}
	case parser.RHSKindBool:
		if c.RHSBool {
			match = lhs.Type == gjson.True
		} else {
			match = lhs.Type == gjson.False
		}
	case parser.RHSKindNull:
		match = lhs.Type == gjson.Null
	default:
		return false, false
	}
	if c.Op == "!=" {
		match = !match
	}
	return match, true
}

func (e *Expression) decimalPrecision() int {
	if e.options == nil {
		return 0
	}
	return e.options.decimalPrecision
}

// fastValue converts a pure-path result, keeping numbers as json.Number under
// decimal precision.
func (e *Expression) fastValue(r *gjson.Result) any {
	return gjsonValue(r, e.decimalPrecision() > 0)
}

// gjsonValue is gjsonValueToAny, except that with useNumber (decimal precision)
// numbers stay json.Number so they keep full precision, as in the full evaluator.
func gjsonValue(r *gjson.Result, useNumber bool) any {
	if r.Type == gjson.Number && useNumber {
		return json.Number(r.Raw)
	}
	return gjsonValueToAny(r)
}

// gjsonValueToAny converts a gjson.Result to a native Go value.
func gjsonValueToAny(r *gjson.Result) any {
	switch r.Type {
	case gjson.Null:
		return evaluator.Null
	case gjson.True:
		return true
	case gjson.False:
		return false
	case gjson.Number:
		if raw := r.Raw; isCanonicalInteger(raw) {
			return json.Number(raw)
		}
		return r.Float()
	case gjson.String:
		return r.String()
	case gjson.JSON:
		if v, err := evaluator.DecodeInput(json.RawMessage(r.Raw)); err == nil {
			return v
		}
		return nil
	}
	return nil
}

// EvalWithCustomFuncs evaluates against pre-parsed data using a custom environment.
// The env parameter should be created via NewCustomEnv.
func (e *Expression) EvalWithCustomFuncs(ctx context.Context, data any, env *evaluator.Environment) (result any, err error) {
	return e.evalCore(ctx, data, env, nil)
}

// EvalWithCustomEnvironmentAndVars evaluates with a pre-built custom
// environment and per-call variable bindings. Construct the environment once
// via NewCustomEnvironment and reuse it across calls — rebuilding the
// environment per evaluation re-registers the entire standard library and is
// significantly more expensive than the per-call variable bind below.
func (e *Expression) EvalWithCustomEnvironmentAndVars(
	ctx context.Context,
	data any,
	customEnv *CustomEnvironment,
	vars map[string]any,
) (result any, err error) {
	if customEnv == nil {
		return e.evalCore(ctx, data, builtinEnv, vars)
	}
	return e.evalCore(ctx, data, customEnv.env, vars)
}

// NewCustomEnv creates a root environment with all standard library functions
// plus the provided custom functions. The returned environment is goroutine-safe
// for concurrent reads and should be reused across evaluations.
func NewCustomEnv(customFuncs map[string]CustomFunc) *evaluator.Environment {
	return newEnv(customFuncs, false)
}

// EvalWithVars evaluates the expression with extra variable bindings.
func (e *Expression) EvalWithVars(ctx context.Context, data any, vars map[string]any) (result any, err error) {
	return e.evalCore(ctx, data, builtinEnv, vars)
}

// IsFastPath reports whether this expression uses the zero-copy GJSON pure-path fast path.
func (e *Expression) IsFastPath() bool {
	return e.fastPath
}

// IsFuncFastPath reports whether this expression uses the function fast path
// (a built-in function applied to a pure path, evaluated via gjson).
func (e *Expression) IsFuncFastPath() bool {
	return e.funcFast != nil
}

// IsBooleanFastPath reports whether this expression uses the boolean fast path
// (and / or / $not over fast-path leaves, evaluated via gjson).
func (e *Expression) IsBooleanFastPath() bool {
	return e.boolFast != nil
}

// IsComparisonFastPath reports whether this expression uses the comparison fast path
// (a pure path compared to a literal, evaluated via gjson).
func (e *Expression) IsComparisonFastPath() bool {
	return e.cmpFast != nil
}

// RequiredPaths returns the GJSON paths this expression needs (fast-path only).
func (e *Expression) RequiredPaths() []string {
	return e.paths
}

// DeepEqual reports whether two JSONata values are structurally equal.
// This is the same equality used by the = and != operators.
func DeepEqual(a, b any) bool {
	// Delegates to the internal evaluator implementation.
	// Imported here so callers don't need to reference internal packages.
	return deepEqualInternal(a, b)
}

// IsNull reports whether v is the JSONata null sentinel value.
// The evaluator distinguishes JSON null (IsNull returns true) from
// JSONata undefined (Go nil). Use this when serializing evaluator output.
func IsNull(v any) bool {
	return evaluator.IsNull(v)
}

type JSONNull = evaluator.JSONNull

var Null = evaluator.Null

type OrderedMap = evaluator.OrderedMap

func NewOrderedMap() *OrderedMap {
	return evaluator.NewOrderedMap()
}

func NewOrderedMapWithCapacity(n int) *OrderedMap {
	return evaluator.NewOrderedMapWithCapacity(n)
}

// DecodeJSON decodes a JSON value using OrderedMap for objects, preserving
// key insertion order. Use this instead of json.Unmarshal when key order
// matters (which is always the case for JSONata evaluation).
func DecodeJSON(b json.RawMessage) (any, error) {
	return evaluator.DecodeJSON(b)
}

// isCanonicalInteger checks if a raw JSON number string represents a
// canonical integer (no decimal point, no exponent, no leading zeros except "0").
// Returns false for "-0" (not canonical; should normalize to "0") and for
// numbers with 21+ digits (JavaScript uses scientific notation for |v| >= 1e21).
func isCanonicalInteger(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '-' {
		start = 1
		if len(s) == 1 {
			return false
		}
	}
	digits := len(s) - start
	if s[start] == '0' {
		if digits == 1 && start == 1 {
			return false // "-0" is not canonical
		}
		return digits == 1
	}
	if digits > 20 {
		return false // JS uses scientific notation for |v| >= 1e21
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
