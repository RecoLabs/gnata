package gnata_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/recolabs/gnata"
)

// checkEvalMapPruned runs a conformance case with an object input through
// StreamEvaluator.EvalMap and Expression.EvalMap, which decode only the
// top-level keys the expression reads, and requires what evaluating the whole
// decoded input gives: the same result, or an error from both. runTestCase
// calls it for every case.
func checkEvalMapPruned(t *testing.T, src string, expr *gnata.Expression, input json.RawMessage, bindings map[string]any) {
	t.Helper()
	var mapData map[string]json.RawMessage
	if len(bindings) > 0 || json.Unmarshal(input, &mapData) != nil || mapData == nil {
		return
	}
	// EvalMap decodes a map's keys in sorted order, as json.Marshal writes
	// them; decode the whole input the same way.
	sorted, err := json.Marshal(mapData)
	if err != nil {
		t.Fatal(err)
	}
	whole, err := gnata.DecodeJSON(sorted)
	if err != nil {
		t.Fatal(err)
	}
	want, wantErr := expr.Eval(context.Background(), whole)
	streamed, streamErr := gnata.NewStreamEvaluator([]*gnata.Expression{expr}).
		EvalMap(context.Background(), mapData, "", []int{0})
	var streamedResult any
	if streamErr == nil {
		streamedResult = streamed[0]
	}
	single, singleErr := expr.EvalMap(context.Background(), mapData)
	for _, got := range []struct {
		via    string
		result any
		err    error
	}{{"StreamEvaluator.EvalMap", streamedResult, streamErr}, {"Expression.EvalMap", single, singleErr}} {
		if (wantErr != nil) != (got.err != nil) || wantErr == nil && !gnata.DeepEqual(want, got.result) {
			t.Errorf("%s on a pruned input differs from the whole input:\n  expr: %s\n  want: %v, %v\n  got:  %v, %v",
				got.via, src, want, wantErr, got.result, got.err)
		}
	}
}

// TestExpressionCopy checks an Expression can still be copied (go vet's
// copylocks rejects copying a struct holding an atomic value) and that the
// copy evaluates like the original.
func TestExpressionCopy(t *testing.T) {
	expr, err := gnata.Compile(`$contains($string(order.note), "h")`)
	if err != nil {
		t.Fatal(err)
	}
	mapData := map[string]json.RawMessage{"order": json.RawMessage(`{"note":"hi"}`), "history": json.RawMessage(`[1]`)}
	copied := *expr
	for _, e := range []*gnata.Expression{expr, &copied} {
		got, err := e.EvalMap(context.Background(), mapData)
		if err != nil || got != true {
			t.Fatalf("got %v, %v", got, err)
		}
	}
}

// TestEvalMapDecodesOnlyReadKeys checks that a key no expression reads is
// never decoded: a malformed value there fails only expressions that may
// read the whole input.
func TestEvalMapDecodesOnlyReadKeys(t *testing.T) {
	mapData := map[string]json.RawMessage{
		"order":   json.RawMessage(`{"note":"hi","status":"shipped"}`),
		"history": json.RawMessage(`[{"broken":`),
	}
	tests := []struct {
		name    string
		expr    string
		want    any
		wantErr bool
	}{
		{name: "named key", expr: `$contains($string(order.note), "h")`, want: true},
		{name: "named key in a block", expr: `($s := order.status; $s = "shipped" ? "yes" : "no")`, want: "yes"},
		{name: "whole input", expr: `$count($keys($)) > 0`, wantErr: true},
		{name: "wildcard", expr: `$count(*) > 0`, wantErr: true},
		{name: "context of a call", expr: `$length($string()) > 0`, wantErr: true},
		{name: "unread key named", expr: `$count(history) > 0`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := gnata.Compile(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			got, err := gnata.NewStreamEvaluator([]*gnata.Expression{expr}).
				EvalMap(context.Background(), mapData, "", []int{0})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && !gnata.DeepEqual(got[0], tc.want) {
				t.Fatalf("got %v, want %v", got[0], tc.want)
			}
		})
	}
}

// TestEvalMapBatchDecodesUnion checks that a batch decodes the keys every
// one of its expressions reads, not only the first one's.
func TestEvalMapBatchDecodesUnion(t *testing.T) {
	mapData := map[string]json.RawMessage{
		"a":      json.RawMessage(`{"x":"1"}`),
		"b":      json.RawMessage(`{"y":"2"}`),
		"unused": json.RawMessage(`[{"broken":`),
	}
	first, err := gnata.Compile(`$string(a.x) & "!"`)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gnata.Compile(`$string(b.y) & "?"`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := gnata.NewStreamEvaluator([]*gnata.Expression{first, second}).
		EvalMap(context.Background(), mapData, "", []int{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "1!" || got[1] != "2?" {
		t.Fatalf("got %v", got)
	}
}

// BenchmarkEvalMapLargeUnreadKey evaluates a full-path expression against a
// map holding a large value it never reads beside the two small ones it does.
func BenchmarkEvalMapLargeUnreadKey(b *testing.B) {
	entries := make([]string, 0, 200)
	for i := range 200 {
		text := strings.Repeat("lorem ipsum ", 250) + fmt.Sprint(i)
		entries = append(entries, fmt.Sprintf(`{"id":%d,"lines":[{"kind":"text","text":%q}]}`, i, text))
	}
	mapData := map[string]json.RawMessage{
		"history":  json.RawMessage("[" + strings.Join(entries, ",") + "]"),
		"order":    json.RawMessage(`{"note":"leave at the door","status":"shipped"}`),
		"customer": json.RawMessage(`{"region":"EU","is_new":false}`),
	}
	expr, err := gnata.Compile(
		`$contains($lowercase($string(order.note)), "door") and customer.region in ["EU","UK"]`,
	)
	if err != nil {
		b.Fatal(err)
	}
	stream := gnata.NewStreamEvaluator([]*gnata.Expression{expr})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := stream.EvalMap(context.Background(), mapData, "", []int{0}); err != nil {
			b.Fatal(err)
		}
	}
}

// TestEvalMapPrunedConcurrent evaluates the same expressions from many
// goroutines at once, through a stream and a bare Expression, so the race
// detector covers the per-expression key cache.
func TestEvalMapPrunedConcurrent(t *testing.T) {
	mapData := map[string]json.RawMessage{
		"order":    json.RawMessage(`{"note":"hello","status":"shipped"}`),
		"customer": json.RawMessage(`{"region":"EU"}`),
		"history":  json.RawMessage(`[{"id":1}]`),
	}
	sources := []string{
		`$contains($string(order.note), "ell") and customer.region = "EU"`,
		`$count($keys($))`,
		`order.status & "/" & $string(customer.region)`,
	}
	want := []any{true, float64(3), "shipped/EU"}
	stream := gnata.NewStreamEvaluator(nil)
	exprs := make([]*gnata.Expression, len(sources))
	indices := make([]int, len(sources))
	for i, src := range sources {
		expr, err := gnata.Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		exprs[i], indices[i] = expr, stream.Add(expr)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for range 32 {
		wg.Go(func() {
			for range 50 {
				got, err := stream.EvalMap(context.Background(), mapData, "", indices)
				if err != nil {
					errs <- err
					return
				}
				for i := range want {
					if !gnata.DeepEqual(got[i], want[i]) {
						errs <- fmt.Errorf("stream %q: got %v, want %v", sources[i], got[i], want[i])
						return
					}
					single, err := exprs[i].EvalMap(context.Background(), mapData)
					if err != nil || !gnata.DeepEqual(single, want[i]) {
						errs <- fmt.Errorf("expression %q: got %v (%w), want %v", sources[i], single, err, want[i])
						return
					}
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
