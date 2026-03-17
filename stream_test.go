package gnata_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/recolabs/gnata"
)

const streamTestData = `{
	"data": {"action": "grant-access", "user_type": 2},
	"metadata": {"is_admin": true}
}`

// TestStreamEvaluator_Compile verifies that Compile() assigns sequential indices
// and that each compiled expression evaluates to the expected result.
func TestStreamEvaluator_Compile(t *testing.T) {
	tests := []struct {
		name        string
		exprs       []string
		wantResults []any
	}{
		{
			name:        "single expression",
			exprs:       []string{`data.action = "grant-access"`},
			wantResults: []any{true},
		},
		{
			name: "sequential index assignment and correct evaluation",
			exprs: []string{
				`data.action = "grant-access"`,
				`data.user_type = 2`,
				`metadata.is_admin = true`,
			},
			wantResults: []any{true, true, true},
		},
		{
			name: "mixed fast-path and complex expressions",
			exprs: []string{
				`data.user_type = 2`, // comparison fast path
				`data.user_type > 1`, // full eval
				`data.user_type`,     // pure-path fast path
			},
			wantResults: []any{true, true, float64(2)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			se := gnata.NewStreamEvaluator(nil)

			indices := make([]int, len(tc.exprs))
			for i, src := range tc.exprs {
				idx, err := se.Compile(src)
				if err != nil {
					t.Fatalf("Compile(%q): %v", src, err)
				}
				if idx != i {
					t.Fatalf("expr %d: want index %d, got %d", i, i, idx)
				}
				indices[i] = idx
			}
			if se.Len() != len(tc.exprs) {
				t.Fatalf("Len: want %d, got %d", len(tc.exprs), se.Len())
			}

			results, err := se.EvalMany(context.Background(), json.RawMessage(streamTestData), "schema", indices)
			if err != nil {
				t.Fatalf("EvalMany: %v", err)
			}
			for i, want := range tc.wantResults {
				if !gnata.DeepEqual(results[i], want) {
					t.Errorf("result[%d]: want %v (%T), got %v (%T)",
						i, want, want, results[i], results[i])
				}
			}
		})
	}
}

// TestStreamEvaluator_Add verifies that Add() accepts pre-compiled Expressions
// and that EvalOne returns the expected result for each expression type.
func TestStreamEvaluator_Add(t *testing.T) {
	tests := []struct {
		name       string
		expr       string
		wantResult any
	}{
		{"comparison fast path - string eq", `data.action = "grant-access"`, true},
		{"comparison fast path - num eq", `data.user_type = 2`, true},
		{"comparison fast path - bool eq", `metadata.is_admin = true`, true},
		{"comparison fast path - string neq", `data.action != "other"`, true},
		{"comparison fast path - num neq", `data.user_type != 99`, true},
		{"pure-path fast path", `data.user_type`, float64(2)},
		{"full eval - gt", `data.user_type > 1`, true},
		{"full eval - and", `data.user_type = 2 and metadata.is_admin = true`, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := gnata.Compile(tc.expr)
			if err != nil {
				t.Fatalf("gnata.Compile(%q): %v", tc.expr, err)
			}

			se := gnata.NewStreamEvaluator(nil)
			idx := se.Add(compiled)
			if idx != 0 {
				t.Fatalf("first Add: want index 0, got %d", idx)
			}

			got, err := se.EvalOne(context.Background(), json.RawMessage(streamTestData), "", idx)
			if err != nil {
				t.Fatalf("EvalOne: %v", err)
			}
			if !gnata.DeepEqual(got, tc.wantResult) {
				t.Errorf("want %v (%T), got %v (%T)", tc.wantResult, tc.wantResult, got, got)
			}
		})
	}
}

// TestStreamEvaluator_CacheInvalidation verifies that adding an expression after
// the cache is warm causes a cache miss on the next EvalMany call, and that the
// newly added expression evaluates correctly.
func TestStreamEvaluator_CacheInvalidation(t *testing.T) {
	tests := []struct {
		name          string
		initialExpr   string
		addedExpr     string
		wantMissCount int64
	}{
		{
			name:          "add after single warmup call",
			initialExpr:   `data.action = "grant-access"`,
			addedExpr:     `data.user_type = 2`,
			wantMissCount: 2, // warmup miss + post-Add miss
		},
		{
			name:          "add complex expression after warmup",
			initialExpr:   `data.user_type = 2`,
			addedExpr:     `data.user_type > 1`,
			wantMissCount: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			se := gnata.NewStreamEvaluator(nil)
			idx0, err := se.Compile(tc.initialExpr)
			if err != nil {
				t.Fatalf("Compile initial: %v", err)
			}

			// Warm the cache.
			if _, err := se.EvalMany(context.Background(), json.RawMessage(streamTestData), "schema", []int{idx0}); err != nil {
				t.Fatalf("warmup EvalMany: %v", err)
			}
			if got := se.Stats().Misses; got != 1 {
				t.Fatalf("after warmup: want 1 miss, got %d", got)
			}

			idx1, err := se.Compile(tc.addedExpr)
			if err != nil {
				t.Fatalf("Compile added: %v", err)
			}

			// Add must have invalidated the cache — expect a miss.
			results, err := se.EvalMany(context.Background(), json.RawMessage(streamTestData), "schema", []int{idx0, idx1})
			if err != nil {
				t.Fatalf("post-Add EvalMany: %v", err)
			}
			for i, got := range results {
				if got != true {
					t.Errorf("result[%d]: want true, got %v", i, got)
				}
			}
			if got := se.Stats().Misses; got != tc.wantMissCount {
				t.Errorf("miss count: want %d, got %d", tc.wantMissCount, got)
			}
		})
	}
}

// TestStreamEvaluator_IndexStability verifies that previously assigned indices
// continue to point to the correct expression after additional expressions are added.
func TestStreamEvaluator_IndexStability(t *testing.T) {
	tests := []struct {
		name       string
		seedExprs  []string
		extraCount int
		wantAll    bool // all seed expressions should evaluate to true with json.RawMessage(streamTestData)
	}{
		{
			name:       "stable after 1 extra add",
			seedExprs:  []string{`data.action = "grant-access"`, `data.user_type = 2`},
			extraCount: 1,
			wantAll:    true,
		},
		{
			name:       "stable after 10 extra adds",
			seedExprs:  []string{`data.action = "grant-access"`, `data.user_type = 2`},
			extraCount: 10,
			wantAll:    true,
		},
		{
			name:       "stable after 100 extra adds",
			seedExprs:  []string{`data.action = "grant-access"`, `data.user_type = 2`},
			extraCount: 100,
			wantAll:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			se := gnata.NewStreamEvaluator(nil)

			seedIndices := make([]int, len(tc.seedExprs))
			for i, src := range tc.seedExprs {
				idx, err := se.Compile(src)
				if err != nil {
					t.Fatalf("Compile seed[%d]: %v", i, err)
				}
				seedIndices[i] = idx
			}

			for i := range tc.extraCount {
				_, _ = se.Compile(fmt.Sprintf(`data.user_type = %d`, i+1000))
			}

			wantLen := len(tc.seedExprs) + tc.extraCount
			if se.Len() != wantLen {
				t.Fatalf("Len: want %d, got %d", wantLen, se.Len())
			}

			results, err := se.EvalMany(context.Background(), json.RawMessage(streamTestData), "", seedIndices)
			if err != nil {
				t.Fatalf("EvalMany: %v", err)
			}
			for i, got := range results {
				if got != tc.wantAll {
					t.Errorf("seed[%d] (index %d): want %v, got %v",
						i, seedIndices[i], tc.wantAll, got)
				}
			}
		})
	}
}

// TestStreamEvaluator_Replace verifies that Replace swaps an expression in-place
// and that subsequent evaluations use the new expression.
func TestStreamEvaluator_Replace(t *testing.T) {
	se := gnata.NewStreamEvaluator(nil)
	idx, err := se.Compile(`data.action`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	got, err := se.EvalOne(context.Background(), json.RawMessage(streamTestData), "", idx)
	if err != nil {
		t.Fatalf("EvalOne before replace: %v", err)
	}
	if got != "grant-access" {
		t.Fatalf("before replace: want grant-access, got %v", got)
	}

	newExpr, _ := gnata.Compile(`data.user_type`)
	if err := se.Replace(idx, newExpr); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got, err = se.EvalOne(context.Background(), json.RawMessage(streamTestData), "", idx)
	if err != nil {
		t.Fatalf("EvalOne after replace: %v", err)
	}
	if !gnata.DeepEqual(got, float64(2)) {
		t.Fatalf("after replace: want 2, got %v", got)
	}
}

// TestStreamEvaluator_Remove verifies that Remove marks an expression as removed
// and that subsequent evaluations return nil for that index.
func TestStreamEvaluator_Remove(t *testing.T) {
	se := gnata.NewStreamEvaluator(nil)
	idx0, _ := se.Compile(`data.action = "grant-access"`)
	idx1, _ := se.Compile(`data.user_type = 2`)

	if err := se.Remove(idx0); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	results, err := se.EvalMany(context.Background(), json.RawMessage(streamTestData), "", []int{idx0, idx1})
	if err != nil {
		t.Fatalf("EvalMany: %v", err)
	}
	if results[0] != nil {
		t.Errorf("removed expr: want nil, got %v", results[0])
	}
	if results[1] != true {
		t.Errorf("kept expr: want true, got %v", results[1])
	}
}

// TestStreamEvaluator_Reset verifies that Reset clears all expressions.
func TestStreamEvaluator_Reset(t *testing.T) {
	se := gnata.NewStreamEvaluator(nil)
	if _, err := se.Compile(`data.action`); err != nil {
		t.Fatal(err)
	}
	if _, err := se.Compile(`data.user_type`); err != nil {
		t.Fatal(err)
	}

	if se.Len() != 2 {
		t.Fatalf("before reset: want Len 2, got %d", se.Len())
	}

	se.Reset()
	if se.Len() != 0 {
		t.Fatalf("after reset: want Len 0, got %d", se.Len())
	}

	idx, err := se.Compile(`metadata.is_admin`)
	if err != nil {
		t.Fatal(err)
	}
	if idx != 0 {
		t.Fatalf("after reset: first Add should get index 0, got %d", idx)
	}
}

// TestStreamEvaluator_MetricsHook verifies that the MetricsHook receives
// OnEval, OnCacheHit, and OnCacheMiss callbacks.
func TestStreamEvaluator_MetricsHook(t *testing.T) {
	hook := &testMetricsHook{}
	se := gnata.NewStreamEvaluator(nil, gnata.WithMetricsHook(hook))
	idx, _ := se.Compile(`data.action = "grant-access"`)

	// First call → cache miss.
	if _, err := se.EvalMany(context.Background(), json.RawMessage(streamTestData), "schema1", []int{idx}); err != nil {
		t.Fatal(err)
	}
	if hook.misses != 1 {
		t.Errorf("want 1 miss, got %d", hook.misses)
	}
	if hook.evals != 1 {
		t.Errorf("want 1 eval, got %d", hook.evals)
	}

	// Second call → cache hit.
	if _, err := se.EvalMany(context.Background(), json.RawMessage(streamTestData), "schema1", []int{idx}); err != nil {
		t.Fatal(err)
	}
	if hook.hits != 1 {
		t.Errorf("want 1 hit, got %d", hook.hits)
	}
	if hook.evals != 2 {
		t.Errorf("want 2 evals, got %d", hook.evals)
	}
	if hook.fastPaths < 1 {
		t.Errorf("want at least 1 fast-path eval, got %d", hook.fastPaths)
	}
}

// testMetricsHook is a test-only stub; not goroutine-safe. The concurrent
// safety tests (TestStreamEvaluator_ConcurrentSafety) do not attach a hook,
// so unsynchronized counters are acceptable here.
type testMetricsHook struct {
	evals     int
	fastPaths int
	hits      int
	misses    int
	evictions int
}

func (h *testMetricsHook) OnEval(_ int, fastPath bool, _ time.Duration, _ error) {
	h.evals++
	if fastPath {
		h.fastPaths++
	}
}
func (h *testMetricsHook) OnCacheHit(_ string)  { h.hits++ }
func (h *testMetricsHook) OnCacheMiss(_ string) { h.misses++ }
func (h *testMetricsHook) OnEviction()          { h.evictions++ }

// TestStreamEvaluator_EvalMap verifies that EvalMap produces identical results
// to EvalMany for the same data and expressions.
func TestStreamEvaluator_EvalMap(t *testing.T) {
	rawData := json.RawMessage(streamTestData)

	var dataMap map[string]json.RawMessage
	if err := json.Unmarshal(rawData, &dataMap); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	tests := []struct {
		name string
		expr string
		want any
	}{
		{"path lookup", `data.action`, "grant-access"},
		{"comparison", `data.user_type = 2`, true},
		{"boolean field", `metadata.is_admin`, true},
		{"greater than", `data.user_type > 1`, true},
		{"nested path", `data.user_type`, float64(2)},
		{"and expression", `data.user_type = 2 and metadata.is_admin = true`, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			se := gnata.NewStreamEvaluator(nil)
			idx, err := se.Compile(tc.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.expr, err)
			}

			evalManyResult, err := se.EvalMany(context.Background(), rawData, "", []int{idx})
			if err != nil {
				t.Fatalf("EvalMany: %v", err)
			}

			evalMapResult, err := se.EvalMap(context.Background(), dataMap, "", []int{idx})
			if err != nil {
				t.Fatalf("EvalMap: %v", err)
			}

			if !gnata.DeepEqual(evalManyResult[0], evalMapResult[0]) {
				t.Errorf("EvalMany=%v (%T), EvalMap=%v (%T)",
					evalManyResult[0], evalManyResult[0],
					evalMapResult[0], evalMapResult[0])
			}
			if !gnata.DeepEqual(evalMapResult[0], tc.want) {
				t.Errorf("want %v (%T), got %v (%T)",
					tc.want, tc.want, evalMapResult[0], evalMapResult[0])
			}
		})
	}
}

// TestStreamEvaluator_EvalMap_MultipleExprs verifies that EvalMap evaluates
// multiple expressions against the same pre-parsed data.
func TestStreamEvaluator_EvalMap_MultipleExprs(t *testing.T) {
	var dataMap map[string]json.RawMessage
	if err := json.Unmarshal(json.RawMessage(streamTestData), &dataMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	se := gnata.NewStreamEvaluator(nil)
	idx0, _ := se.Compile(`data.action`)
	idx1, _ := se.Compile(`data.user_type = 2`)
	idx2, _ := se.Compile(`metadata.is_admin`)

	results, err := se.EvalMap(context.Background(), dataMap, "schema", []int{idx0, idx1, idx2})
	if err != nil {
		t.Fatalf("EvalMap: %v", err)
	}

	if results[0] != "grant-access" {
		t.Errorf("result[0]: want grant-access, got %v", results[0])
	}
	if results[1] != true {
		t.Errorf("result[1]: want true, got %v", results[1])
	}
	if results[2] != true {
		t.Errorf("result[2]: want true, got %v", results[2])
	}
}

// TestStreamEvaluator_EvalMap_NilAndEmpty verifies edge cases.
func TestStreamEvaluator_EvalMap_NilAndEmpty(t *testing.T) {
	se := gnata.NewStreamEvaluator(nil)
	idx, _ := se.Compile(`foo`)

	results, err := se.EvalMap(context.Background(), nil, "", []int{idx})
	if err != nil {
		t.Fatalf("EvalMap(nil): %v", err)
	}
	if results[0] != nil {
		t.Errorf("nil map: want nil result, got %v", results[0])
	}

	results, err = se.EvalMap(context.Background(), map[string]json.RawMessage{}, "", []int{idx})
	if err != nil {
		t.Fatalf("EvalMap(empty): %v", err)
	}
	if results[0] != nil {
		t.Errorf("empty map: want nil result, got %v", results[0])
	}

	results, err = se.EvalMap(context.Background(), map[string]json.RawMessage{}, "", nil)
	if err != nil {
		t.Fatalf("EvalMap(no indices): %v", err)
	}
	if results != nil {
		t.Errorf("no indices: want nil results, got %v", results)
	}
}

// TestStreamEvaluator_EvalMap_FastPaths verifies that EvalMap takes the same
// fast paths (pure path, comparison, function) as EvalMany, using MetricsHook
// to confirm fastPath=true.
func TestStreamEvaluator_EvalMap_FastPaths(t *testing.T) {
	var dataMap map[string]json.RawMessage
	if err := json.Unmarshal(json.RawMessage(streamTestData), &dataMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	tests := []struct {
		name string
		expr string
		want any
	}{
		{"pure path", `data.action`, "grant-access"},
		{"comparison", `data.user_type = 2`, true},
		{"function $exists", `$exists(data.action)`, true},
		{"nested path", `data.user_type`, float64(2)},
		{"missing top-level key", `nonexistent.field`, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hook := &testMetricsHook{}
			se := gnata.NewStreamEvaluator(nil, gnata.WithMetricsHook(hook))
			idx, err := se.Compile(tc.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.expr, err)
			}

			results, err := se.EvalMap(context.Background(), dataMap, "s", []int{idx})
			if err != nil {
				t.Fatalf("EvalMap: %v", err)
			}

			if !gnata.DeepEqual(results[0], tc.want) {
				t.Errorf("want %v (%T), got %v (%T)", tc.want, tc.want, results[0], results[0])
			}

			if tc.want != nil && hook.fastPaths == 0 {
				t.Errorf("expected fast path for %q but MetricsHook reported 0 fast paths", tc.expr)
			}
		})
	}
}

// TestStreamEvaluator_ConcurrentSafety exercises concurrent Add / Compile calls
// interleaved with EvalMany to surface data races. Run with -race.
func TestStreamEvaluator_ConcurrentSafety(t *testing.T) {
	tests := []struct {
		name    string
		writers int
		readers int
		opsEach int
	}{
		{"light load", 2, 2, 25},
		{"heavy load", 4, 4, 50},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			se := gnata.NewStreamEvaluator(nil)
			seed, _ := se.Compile(`data.action = "grant-access"`)

			var wg sync.WaitGroup

			for g := range tc.writers {
				wg.Add(1)
				go func(g int) {
					defer wg.Done()
					for i := range tc.opsEach {
						_, _ = se.Compile(fmt.Sprintf(`data.user_type = %d`, g*1000+i))
					}
				}(g)
			}

			for range tc.readers {
				wg.Go(func() {
					for range tc.opsEach {
						results, err := se.EvalMany(context.Background(), json.RawMessage(streamTestData), "schema", []int{seed})
						if err != nil {
							t.Errorf("EvalMany: %v", err)
							return
						}
						if results[0] != true {
							t.Errorf("seed result: want true, got %v", results[0])
						}
					}
				})
			}

			wg.Wait()

			wantMin := 1 + tc.writers*tc.opsEach
			if se.Len() < wantMin {
				t.Errorf("Len: want >= %d, got %d", wantMin, se.Len())
			}
		})
	}
}
