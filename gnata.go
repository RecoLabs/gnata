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
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/recolabs/gnata/functions"
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
	// guardrails holds optional resource limits set via Compile options.
	// nil when Compile was called without options (default, unlimited behavior).
	guardrails *guardrails
}

// guardrails holds the resource limits configured via Option, matching the
// jsonata-js 2.2 guardrails API (stack / timeout / sequence).
type guardrails struct {
	stack    int
	timeout  time.Duration
	sequence int
}

// errGuardrailTimeout tags the context cause set by WithTimeout, so evalCore
// can distinguish a guardrail timeout (error D1012) from the caller's own
// context cancellation (propagated as-is).
var errGuardrailTimeout = errors.New("gnata: guardrail timeout exceeded")

// Option configures an optional resource guardrail on a compiled Expression.
// See WithStack, WithTimeout, and WithSequence.
type Option func(*guardrails)

// WithStack limits the maximum lambda recursion depth. Exceeding it returns
// error D1011. Without this option, gnata still enforces its built-in limit
// of 100 (error U1001) — WithStack only changes the limit and the resulting
// error code, matching jsonata-js's `stack` guardrail.
func WithStack(n int) Option {
	return func(g *guardrails) { g.stack = n }
}

// WithTimeout limits total evaluation time. Exceeding it returns error
// D1012. Without this option, evaluation is bounded only by the ctx passed
// to Eval, matching jsonata-js's `timeout` guardrail.
func WithTimeout(d time.Duration) Option {
	return func(g *guardrails) { g.timeout = d }
}

// WithSequence limits the length of sequences built during evaluation: the
// range operator (..), $append, $map, $filter, $each, wildcard (*), and
// descendant (**). Exceeding it returns error D2015, matching jsonata-js's
// `sequence` guardrail. Without this option, only the built-in 10,000,000
// element hard caps (D2014 / D3010) apply.
func WithSequence(n int) Option {
	return func(g *guardrails) { g.sequence = n }
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
	var g *guardrails
	if len(opts) > 0 {
		g = &guardrails{}
		for _, opt := range opts {
			opt(g)
		}
	}
	return &Expression{
		src:        expr,
		ast:        ast,
		fastPath:   fp.IsFastPath,
		paths:      fp.GJSONPaths,
		pathSteps:  fp.PathSteps,
		cmpFast:    fp.CmpFast,
		funcFast:   fp.FuncFast,
		guardrails: g,
	}, nil
}

// CustomFunc is a user-defined function that can be registered with gnata.
// It receives evaluated arguments and the current context value (focus).
type CustomFunc func(args []any, focus any) (any, error)

// CustomEnvironment is a reusable root environment containing standard library
// functions and caller-provided custom functions.
type CustomEnvironment struct {
	env *evaluator.Environment
}

// NewCustomEnvironment pre-builds a reusable environment for a stable set of
// custom functions. Each evaluation creates a child environment for variables.
func NewCustomEnvironment(customFuncs map[string]CustomFunc) *CustomEnvironment {
	return &CustomEnvironment{env: newEnv(customFuncs)}
}

// builtinEnv is a shared root environment with all standard library functions.
// Created once at init; each eval creates a thin child env for per-call bindings.
var builtinEnv *evaluator.Environment

func init() {
	builtinEnv = newEnv(nil)
}

// newEnv creates a root environment with all standard library functions
// and optional custom functions registered.
func newEnv(customFuncs map[string]CustomFunc) *evaluator.Environment {
	env := evaluator.NewEnvironment()
	functions.RegisterAll(env, evaluator.ApplyFunction)
	for name, fn := range customFuncs {
		wrapped := wrapCustomFunc(fn)
		env.Bind(name, evaluator.BuiltinFunction(wrapped))
	}
	return env
}

// wrapCustomFunc wraps a user-provided custom function to normalize
// internal evaluator types (OrderedMap, Null sentinel) into standard
// Go types (map[string]any, nil) before the function sees them.
func wrapCustomFunc(fn CustomFunc) CustomFunc {
	return func(args []any, focus any) (any, error) {
		for i, a := range args {
			args[i] = NormalizeValue(a)
		}
		return fn(args, NormalizeValue(focus))
	}
}

// NormalizeValue converts internal evaluator types to standard Go types.
// OrderedMap becomes map[string]any, the null sentinel becomes nil,
// and slices are recursively normalized only when they contain internal types.
// Scalar values and slices of pure scalars pass through without allocation.
func NormalizeValue(v any) any {
	if v == nil {
		return nil
	}
	if evaluator.IsNull(v) {
		return nil
	}
	switch val := v.(type) {
	case *evaluator.Sequence:
		collapsed := evaluator.CollapseSequence(val)
		return NormalizeValue(collapsed)
	case *evaluator.OrderedMap:
		m := val.ToMap()
		out := make(map[string]any, len(m))
		for k, mv := range m {
			out[k] = NormalizeValue(mv)
		}
		return out
	case []any:
		return normalizeSlice(val)
	}
	return v
}

// normalizeSlice only allocates a copy when at least one element
// needs conversion (OrderedMap, null sentinel, or nested slice).
func normalizeSlice(s []any) any {
	needsCopy := slices.ContainsFunc(s, needsNormalize)
	if !needsCopy {
		return s
	}
	out := make([]any, len(s))
	for i, elem := range s {
		out[i] = NormalizeValue(elem)
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
	case []any:
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
	if e.guardrails != nil && e.guardrails.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, e.guardrails.timeout, errGuardrailTimeout)
		defer cancel()
	}
	env := evaluator.NewChildEnvironment(parent)
	env.ResetCallCounter()
	env.SetContext(ctx)
	if e.guardrails != nil {
		if e.guardrails.stack > 0 {
			env.SetMaxStackDepth(e.guardrails.stack)
		}
		if e.guardrails.sequence > 0 {
			env.SetMaxSequence(e.guardrails.sequence)
		}
	}
	env.Bind("$", data)
	for k, v := range vars {
		env.Bind(k, v)
	}
	result, err = evaluator.Eval(e.ast, data, env)
	if err != nil {
		if e.guardrails != nil && e.guardrails.timeout > 0 && errors.Is(context.Cause(ctx), errGuardrailTimeout) {
			return nil, &evaluator.JSONataError{Code: "D1012", Message: fmt.Sprintf("Evaluation timeout after %s", e.guardrails.timeout)}
		}
		return nil, err
	}
	if seq, ok := result.(*evaluator.Sequence); ok {
		result = evaluator.CollapseSequence(seq)
	}
	return result, nil
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
		if res := resolveGjsonPath(data, mapData, e.paths[0]); res.Exists() {
			return gjsonValueToAny(&res), true, nil
		}
		var v any
		var ok bool
		switch {
		case data != nil:
			v, ok = walkPureStepsBytes(e.pathSteps, data)
		case mapData != nil:
			v, ok = walkPureStepsMapBytes(e.pathSteps, mapData)
		}
		if ok {
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
	v, err := evaluator.DecodeJSON(data)
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
	v, err := evaluator.DecodeJSON(data)
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
	v, err := evaluator.DecodeJSON(data)
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
	v, err := evaluator.DecodeJSON(data)
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

// evalComparison evaluates a pre-compiled comparison fast path against raw JSON
// bytes or a pre-decoded map. Returns (result, true, nil) on success. Returns
// (nil, false, nil) when the expression cannot safely short-circuit (e.g. the
// LHS is a JSON array that requires auto-mapping), signalling the caller to
// fall back to full evaluation.
//
//nolint:unparam // err is part of the funcFastHandler contract; always nil for now
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
	var values []gjson.Result
	var ok bool
	switch {
	case data != nil:
		values, ok = walkPureStepsValues(c.LHSPathSteps, data)
	case mapData != nil:
		values, ok = walkPureStepsMapValues(c.LHSPathSteps, mapData)
	}
	if !ok {
		return nil, false, nil
	}
	if len(values) != 1 {
		// A resolved sequence — whether flattened to more than one element,
		// or to zero elements from a field present as an empty array — is
		// never structurally equal to a scalar literal.
		return c.Op == "!=", true, nil
	}
	match, ok := matchComparison(&values[0], c)
	return match, ok, nil
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
		if v, err := evaluator.DecodeJSON(json.RawMessage(r.Raw)); err == nil {
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
	return newEnv(customFuncs)
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
