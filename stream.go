package gnata

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
)

// StreamEvaluator manages compiled expressions for high-throughput streaming evaluation.
// It evaluates multiple expressions against each event with per-schema GroupPlan caching.
//
// Expressions are stored in an atomically swapped slice so reads in EvalMany are fully
// lock-free. Writes (Add / Compile) take a mutex and publish a new copy-on-write slice.
// Goroutine-safe for concurrent EvalMany calls and concurrent Add / Compile calls.
type StreamEvaluator struct {
	exprs     atomic.Pointer[[]*Expression] // copy-on-write; loads are lock-free
	mu        sync.Mutex                    // serialises Add / Compile writes
	cache     *BoundedCache
	metrics   MetricsHook // nil = no overhead
	customEnv *evaluator.Environment
}

// MetricsHook receives evaluation telemetry from StreamEvaluator.
// Implementations must be safe for concurrent use. All methods are optional:
// a nil MetricsHook is never called.
type MetricsHook interface {
	// OnEval is called after each expression evaluation with timing and path info.
	OnEval(exprIndex int, fastPath bool, duration time.Duration, err error)
	// OnCacheHit is called when a GroupPlan is found in the schema cache.
	OnCacheHit(schemaKey string)
	// OnCacheMiss is called when a GroupPlan must be built for a schema.
	OnCacheMiss(schemaKey string)
	// OnEviction is called when a GroupPlan is evicted from the schema cache
	// due to capacity overflow.
	OnEviction()
}

// StreamOption configures a StreamEvaluator.
type StreamOption func(*streamConfig)

type streamConfig struct {
	poolSize    int
	maxSchemas  int
	metrics     MetricsHook
	customFuncs map[string]CustomFunc
}

// WithPoolSize pre-warms the evaluation context pool with n entries.
func WithPoolSize(prewarm int) StreamOption {
	return func(c *streamConfig) { c.poolSize = prewarm }
}

// WithMaxCachedSchemas sets the maximum number of GroupPlans held in the
// BoundedCache (default: 10000).
func WithMaxCachedSchemas(n int) StreamOption {
	return func(c *streamConfig) { c.maxSchemas = n }
}

// WithMetricsHook attaches a MetricsHook for evaluation telemetry.
// Pass nil to disable (default). The hook must be safe for concurrent use.
func WithMetricsHook(hook MetricsHook) StreamOption {
	return func(c *streamConfig) { c.metrics = hook }
}

// WithCustomFunctions registers user-defined functions that extend the
// standard JSONata library. Functions are bound once at construction time,
// so there is zero per-evaluation overhead. The map keys are function names
// (without the leading $).
func WithCustomFunctions(fns map[string]CustomFunc) StreamOption {
	return func(c *streamConfig) { c.customFuncs = fns }
}

// StreamStats holds cache statistics returned by Stats().
type StreamStats struct {
	Hits      int64
	Misses    int64
	Entries   int64
	Evictions int64
}

// NewStreamEvaluator creates a streaming evaluator over the given compiled expressions.
// The initial expressions slice is copied; callers may pass nil or an empty slice and
// populate the evaluator later via Add or Compile.
func NewStreamEvaluator(expressions []*Expression, opts ...StreamOption) *StreamEvaluator {
	cfg := &streamConfig{poolSize: 0, maxSchemas: 10000}
	for _, opt := range opts {
		opt(cfg)
	}
	var customEnv *evaluator.Environment
	if len(cfg.customFuncs) > 0 {
		customEnv = newEnv(cfg.customFuncs)
	}
	se := &StreamEvaluator{
		cache:     NewBoundedCache(cfg.maxSchemas),
		metrics:   cfg.metrics,
		customEnv: customEnv,
	}
	snap := make([]*Expression, len(expressions))
	copy(snap, expressions)
	se.exprs.Store(&snap)
	return se
}

// Add appends a pre-compiled Expression to the evaluator and returns its stable index.
//
// The index is guaranteed to be stable: once assigned it will not change even as more
// expressions are added. Pass this index to EvalMany / EvalOne.
//
// Add is safe to call concurrently with EvalMany. It invalidates the schema plan cache
// so that subsequent EvalMany calls rebuild GroupPlans with correct fast-path metadata
// for the new expression.
func (se *StreamEvaluator) Add(expr *Expression) int {
	se.mu.Lock()
	defer se.mu.Unlock()

	old := *se.exprs.Load()
	newExprs := make([]*Expression, len(old)+1)
	copy(newExprs, old)
	idx := len(old)
	newExprs[idx] = expr
	se.exprs.Store(&newExprs)

	// Invalidate cached plans so new EvalMany calls rebuild them with the
	// new expression's fast-path metadata included.
	se.cache.Invalidate()
	return idx
}

// Compile compiles a JSONata expression string, appends it to the evaluator, and
// returns its stable index. Equivalent to calling gnata.Compile followed by Add.
func (se *StreamEvaluator) Compile(src string) (int, error) {
	expr, err := Compile(src)
	if err != nil {
		return -1, err
	}
	return se.Add(expr), nil
}

// Len returns the number of compiled expressions currently registered.
func (se *StreamEvaluator) Len() int {
	return len(*se.exprs.Load())
}

// Replace swaps the expression at the given index in-place. The index must
// have been returned by a previous Add or Compile call. Replace invalidates
// the schema plan cache so that subsequent EvalMany calls rebuild GroupPlans
// with the new expression's fast-path metadata.
//
// Safe to call concurrently with EvalMany.
func (se *StreamEvaluator) Replace(idx int, expr *Expression) error {
	se.mu.Lock()
	defer se.mu.Unlock()

	old := *se.exprs.Load()
	if idx < 0 || idx >= len(old) {
		return fmt.Errorf("gnata: expression index %d out of range [0, %d)", idx, len(old))
	}
	newExprs := make([]*Expression, len(old))
	copy(newExprs, old)
	newExprs[idx] = expr
	se.exprs.Store(&newExprs)
	se.cache.Invalidate()
	return nil
}

// Remove marks the expression at the given index as removed. Removed indices
// return nil from EvalMany/EvalOne. The index is NOT reused — subsequent Add
// calls still append to the end. Remove invalidates the schema plan cache.
//
// Safe to call concurrently with EvalMany.
func (se *StreamEvaluator) Remove(idx int) error {
	se.mu.Lock()
	defer se.mu.Unlock()

	old := *se.exprs.Load()
	if idx < 0 || idx >= len(old) {
		return fmt.Errorf("gnata: expression index %d out of range [0, %d)", idx, len(old))
	}
	newExprs := make([]*Expression, len(old))
	copy(newExprs, old)
	newExprs[idx] = nil
	se.exprs.Store(&newExprs)
	se.cache.Invalidate()
	return nil
}

// Reset removes all expressions and clears the cache. After Reset, the
// evaluator is empty and new expressions can be added via Add/Compile.
// Previously assigned indices are no longer valid.
//
// Safe to call concurrently with EvalMany (in-flight evaluations use the
// old expression snapshot).
func (se *StreamEvaluator) Reset() {
	se.mu.Lock()
	defer se.mu.Unlock()

	empty := make([]*Expression, 0)
	se.exprs.Store(&empty)
	se.cache.Invalidate()
}

// EvalMany evaluates the specified expressions against raw JSON bytes.
//   - data: raw JSON bytes (not pre-parsed).
//   - schemaKey: external key identifying the event schema. On first encounter, builds
//     and caches a GroupPlan. Subsequent calls are lock-free. Pass "" to disable caching.
//   - exprIndices: which compiled expressions to evaluate.
//
// Returns results[i] = evaluation of expressions[exprIndices[i]], or nil for undefined.
func (se *StreamEvaluator) EvalMany(
	ctx context.Context, data json.RawMessage, schemaKey string, exprIndices []int,
) ([]any, error) {
	return se.evalInternal(ctx, data, nil, nil, schemaKey, exprIndices)
}

// EvalMap evaluates the specified expressions against a map of raw JSON values.
//   - data: map of field names to raw JSON-encoded values (decoded individually).
//   - schemaKey: external key identifying the event schema. On first encounter, builds
//     and caches a GroupPlan. Subsequent calls are lock-free. Pass "" to disable caching.
//   - exprIndices: which compiled expressions to evaluate.
//
// Returns results[i] = evaluation of expressions[exprIndices[i]], or nil for undefined.
func (se *StreamEvaluator) EvalMap(
	ctx context.Context, data map[string]json.RawMessage, schemaKey string, exprIndices []int,
) ([]any, error) {
	return se.evalInternal(ctx, nil, nil, data, schemaKey, exprIndices)
}

func (se *StreamEvaluator) evalInternal(
	ctx context.Context, data json.RawMessage, preparsed any, mapData map[string]json.RawMessage, schemaKey string, exprIndices []int,
) (results []any, err error) {
	defer recoverEvalPanic(&err)
	if len(exprIndices) == 0 {
		return nil, nil
	}

	// Load the current expression slice once; lock-free.
	expressions := *se.exprs.Load()

	var plan *GroupPlan
	if schemaKey != "" {
		cacheKey := planCacheKey(schemaKey, exprIndices)
		var ok bool
		plan, ok = se.cache.Get(cacheKey)
		if !ok {
			plan = buildPlan(expressions, exprIndices)
			evicted := se.cache.Set(cacheKey, plan)
			if se.metrics != nil {
				se.metrics.OnCacheMiss(schemaKey)
				if evicted {
					se.metrics.OnEviction()
				}
			}
		} else if se.metrics != nil {
			se.metrics.OnCacheHit(schemaKey)
		}
	} else {
		plan = buildPlan(expressions, exprIndices)
	}

	results = make([]any, len(exprIndices))
	batch := evalBatch{se: se, plan: plan, data: data, mapData: mapData, parsed: preparsed, parseAttempted: preparsed != nil}
	for i, idx := range exprIndices {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if idx < 0 || idx >= len(expressions) {
			continue
		}
		expr := expressions[idx]
		if expr == nil {
			continue
		}
		result, err := batch.evalSingleExpr(ctx, i, idx, expr)
		if err != nil {
			return nil, err
		}
		results[i] = result
	}
	return results, nil
}

// evalBatch holds per-batch mutable state for EvalMany. The lazily-parsed JSON
// value is shared across all expressions in the batch — safe for read-only
// expressions (paths, comparisons, functions) which cover all current callers.
type evalBatch struct {
	se             *StreamEvaluator
	plan           *GroupPlan
	data           json.RawMessage
	mapData        map[string]json.RawMessage
	parsed         any
	parsedErr      error
	parseAttempted bool
}

// evalSingleExpr evaluates one expression, trying fast paths first (pure path,
// comparison, function) and falling back to full AST evaluation.
func (b *evalBatch) evalSingleExpr(ctx context.Context, i, idx int, expr *Expression) (any, error) {
	var start time.Time
	if b.se.metrics != nil {
		start = time.Now()
	}

	if result, done, err := b.tryFastPaths(i, idx, start); done || err != nil {
		return result, err
	}

	return b.fullEval(ctx, idx, expr, start)
}

// tryFastPaths attempts pure-path, comparison, and function fast paths in order.
// Returns (result, true, nil) on success, (nil, false, nil) to signal fallback,
// or (nil, true, err) on error.
func (b *evalBatch) tryFastPaths(i, idx int, start time.Time) (result any, done bool, err error) {
	if b.plan != nil && i < len(b.plan.ExprFastPath) && b.plan.ExprFastPath[i] && b.plan.FastPaths[i] != "" {
		r := resolveGjsonPath(b.data, b.mapData, b.plan.FastPaths[i])
		if r.Exists() {
			if b.se.metrics != nil {
				b.se.metrics.OnEval(idx, true, time.Since(start), nil)
			}
			return gjsonValueToAny(&r), true, nil
		}
	}

	if b.plan != nil && i < len(b.plan.CmpFast) && b.plan.CmpFast[i] != nil {
		if result, handled, err := evalComparison(b.plan.CmpFast[i], b.data, b.mapData); err != nil {
			if b.se.metrics != nil {
				b.se.metrics.OnEval(idx, true, time.Since(start), err)
			}
			return nil, true, err
		} else if handled {
			if b.se.metrics != nil {
				b.se.metrics.OnEval(idx, true, time.Since(start), nil)
			}
			return result, true, nil
		}
	}

	if b.plan != nil && i < len(b.plan.FuncFast) && b.plan.FuncFast[i] != nil {
		if result, handled, err := evalFunc(b.plan.FuncFast[i], b.data, b.mapData); err != nil {
			if b.se.metrics != nil {
				b.se.metrics.OnEval(idx, true, time.Since(start), err)
			}
			return nil, true, err
		} else if handled {
			if b.se.metrics != nil {
				b.se.metrics.OnEval(idx, true, time.Since(start), nil)
			}
			return result, true, nil
		}
	}

	return nil, false, nil
}

// fullEval performs full AST evaluation, lazily parsing JSON on first use.
func (b *evalBatch) fullEval(ctx context.Context, idx int, expr *Expression, start time.Time) (any, error) {
	if !b.parseAttempted {
		b.parseAttempted = true
		if len(b.mapData) > 0 {
			b.parsed, b.parsedErr = evaluator.DecodeRawMap(b.mapData)
		} else if len(b.data) > 0 {
			b.parsed, b.parsedErr = evaluator.DecodeJSON(b.data)
		}
	}
	if b.parsedErr != nil {
		return nil, b.parsedErr
	}
	var result any
	var err error
	if b.se.customEnv != nil {
		result, err = expr.EvalWithCustomFuncs(ctx, b.parsed, b.se.customEnv)
	} else {
		result, err = expr.Eval(ctx, b.parsed)
	}
	if b.se.metrics != nil {
		b.se.metrics.OnEval(idx, false, time.Since(start), err)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// EvalOne evaluates a single expression against raw JSON bytes with schema caching.
func (se *StreamEvaluator) EvalOne(ctx context.Context, data json.RawMessage, schemaKey string, exprIndex int) (any, error) {
	results, err := se.EvalMany(ctx, data, schemaKey, []int{exprIndex})
	if err != nil || results == nil {
		return nil, err
	}
	return results[0], nil
}

// Stats returns cache statistics.
func (se *StreamEvaluator) Stats() StreamStats {
	return se.cache.Stats()
}

// planCacheKey builds a composite cache key from schemaKey and exprIndices.
// This ensures that plans built for different index sets don't collide when
// sharing the same schemaKey.
func planCacheKey(schemaKey string, exprIndices []int) string {
	b := make([]byte, 0, len(schemaKey)+1+len(exprIndices)*4)
	b = append(b, schemaKey...)
	b = append(b, '|')
	for i, idx := range exprIndices {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendInt(b, int64(idx), 10)
	}
	return string(b)
}

// buildPlan constructs a GroupPlan for the given expression indices.
func buildPlan(expressions []*Expression, exprIndices []int) *GroupPlan {
	plan := &GroupPlan{
		FastPaths:    make([]string, len(exprIndices)),
		ExprFastPath: make([]bool, len(exprIndices)),
		CmpFast:      make([]*parser.ComparisonFastPath, len(exprIndices)),
		FuncFast:     make([]*parser.FuncFastPath, len(exprIndices)),
	}
	hasPure, hasCmp, hasFunc := false, false, false
	for i, idx := range exprIndices {
		if idx < 0 || idx >= len(expressions) {
			continue
		}
		expr := expressions[idx]
		if expr == nil {
			continue
		}
		switch {
		case expr.fastPath && len(expr.paths) == 1:
			plan.ExprFastPath[i] = true
			plan.FastPaths[i] = expr.paths[0]
			hasPure = true
		case expr.cmpFast != nil:
			plan.CmpFast[i] = expr.cmpFast
			hasCmp = true
		case expr.funcFast != nil:
			plan.FuncFast[i] = expr.funcFast
			hasFunc = true
		}
	}
	if !hasPure {
		plan.FastPaths = nil
		plan.ExprFastPath = nil
	}
	if !hasCmp {
		plan.CmpFast = nil
	}
	if !hasFunc {
		plan.FuncFast = nil
	}
	return plan
}
