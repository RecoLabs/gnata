// Command streaming shows Tier 3 evaluation: batching several compiled
// expressions per event through StreamEvaluator, with a MetricsHook wired in
// for cache-hit/miss and per-evaluation telemetry.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/recolabs/gnata"
)

type logHook struct{}

func (logHook) OnEval(exprIndex int, fastPath bool, duration time.Duration, err error) {
	fmt.Printf("eval expr=%d fastPath=%v duration=%v err=%v\n", exprIndex, fastPath, duration, err)
}
func (logHook) OnCacheHit(schemaKey string)  { fmt.Printf("cache hit schema=%s\n", schemaKey) }
func (logHook) OnCacheMiss(schemaKey string) { fmt.Printf("cache miss schema=%s\n", schemaKey) }
func (logHook) OnEviction()                  { fmt.Println("cache eviction") }

func main() {
	se := gnata.NewStreamEvaluator(nil,
		gnata.WithPoolSize(10),
		gnata.WithMaxCachedSchemas(1000),
		gnata.WithMetricsHook(logHook{}),
	)

	rules := []string{
		`user.plan = "enterprise"`,
		`$sum(orders.amount) > 1000`,
	}
	indices := make([]int, len(rules))
	for i, rule := range rules {
		idx, err := se.Compile(rule)
		if err != nil {
			log.Fatal(err)
		}
		indices[i] = idx
	}

	event := json.RawMessage(`{
		"user": {"plan": "enterprise"},
		"orders": [{"amount": 600}, {"amount": 500}]
	}`)

	ctx := context.Background()
	results, err := se.EvalMany(ctx, event, "user-order-v1", indices)
	if err != nil {
		log.Fatal(err)
	}
	for i, result := range results {
		fmt.Printf("rule %q -> %v\n", rules[i], result)
	}

	stats := se.Stats()
	fmt.Printf("cache stats: hits=%d misses=%d entries=%d\n", stats.Hits, stats.Misses, stats.Entries)
}
