// Package gnata implements the JSONata 2.x query and transformation language for Go.
//
// Quick start:
//
//	expr, err := gnata.Compile(`Account.Order.Product.Price`)
//	result, err := expr.Eval(data)
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
	// cmpFast covers simple path-vs-literal comparisons (e.g. `a.b = "x"`).
	// Non-nil when the expression qualifies; nil otherwise.
	cmpFast *parser.ComparisonFastPath
	// funcFast covers built-in function calls on a pure path (e.g. `$exists(a.b)`).
	// Non-nil when the expression qualifies; nil otherwise.
	funcFast *parser.FuncFastPath
}

// Compile parses a JSONata expression string and returns an Expression.
// The returned Expression is goroutine-safe and should be reused across calls.
func Compile(expr string) (*Expression, error) {
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
	return &Expression{
		src:      expr,
		ast:      ast,
		fastPath: fp.IsFastPath,
		paths:    fp.GJSONPaths,
		cmpFast:  fp.CmpFast,
		funcFast: fp.FuncFast,
	}, nil
}

// CustomFunc is a user-defined function that can be registered with gnata.
// It receives evaluated arguments and the current context value (focus).
type CustomFunc func(args []any, focus any) (any, error)

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
	env := evaluator.NewChildEnvironment(parent)
	env.ResetCallCounter()
	env.SetContext(ctx)
	env.Bind("$", data)
	for k, v := range vars {
		env.Bind(k, v)
	}
	result, err = evaluator.Eval(e.ast, data, env)
	if err != nil {
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

// EvalBytes evaluates the expression against raw JSON bytes.
//
//   - Pure-path fast path: zero-copy GJSON extraction (e.g. "Account.Name").
//   - Comparison fast path: single gjson scan for path-vs-literal comparisons.
//   - Complex expressions: json.Unmarshal + full AST evaluation.
func (e *Expression) EvalBytes(ctx context.Context, data json.RawMessage) (result any, err error) {
	defer recoverEvalPanic(&err)
	if e.fastPath && len(e.paths) == 1 {
		if res := gjson.GetBytes(data, e.paths[0]); res.Exists() {
			return gjsonValueToAny(&res), nil
		}
		// gjson couldn't resolve the path — fall through to the full evaluator
		// which handles auto-mapping through arrays correctly.
	}
	if e.cmpFast != nil {
		if res, handled, evalErr := evalComparison(e.cmpFast, data, nil); handled || evalErr != nil {
			return res, evalErr
		}
	}
	if e.funcFast != nil {
		if res, handled, evalErr := evalFunc(e.funcFast, data, nil); handled || evalErr != nil {
			return res, evalErr
		}
	}
	v, err := evaluator.DecodeJSON(data)
	if err != nil {
		return nil, err
	}
	return e.Eval(ctx, v)
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
	if !lhs.Exists() {
		// gjson couldn't resolve the path. This could be because the path is
		// truly undefined OR because an intermediate element is a JSON array
		// (gjson doesn't auto-map through arrays, but JSONata does).
		// Fall back to the full evaluator which handles both cases correctly.
		return nil, false, nil
	}

	if lhs.Type == gjson.JSON {
		raw := lhs.Raw
		if raw != "" && raw[0] == '[' {
			// JSON array: JSONata auto-maps comparisons element-wise.
			// For null checks we can safely short-circuit (arrays are never null).
			if c.RHSKind == parser.RHSKindNull {
				return c.Op == "!=", true, nil
			}
			// For all other comparisons, fall back to the full evaluator.
			return nil, false, nil
		}
		// JSON object: never equal to any primitive literal.
		return c.Op == "!=", true, nil
	}

	var match bool
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
		return nil, false, nil
	}
	if c.Op == "!=" {
		match = !match
	}
	return match, true, nil
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
