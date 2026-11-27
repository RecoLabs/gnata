package gnata_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/recolabs/gnata"
)

type pruneCase struct {
	Expr     string          `json:"expr"`
	Data     json.RawMessage `json:"data"`
	Dataset  *string         `json:"dataset"`
	Bindings map[string]any  `json:"bindings"`
}

func loadPruneCases(t *testing.T, file string) []pruneCase {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 0 && raw[0] == '[' {
		var cases []pruneCase
		if err := json.Unmarshal(raw, &cases); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		return cases
	}
	var single pruneCase
	if err := json.Unmarshal(raw, &single); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return []pruneCase{single}
}

// TestEvalMapPrunedParity runs every conformance-suite expression whose
// input is an object through EvalMap, which decodes only the top-level keys
// the expression reads, and requires what evaluating the whole decoded input
// gives.
func TestEvalMapPrunedParity(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "groups", "*", "*.json"))
	if err != nil || len(files) == 0 {
		t.Skip("conformance suite not found")
	}
	var compared, pruned int
	for _, file := range files {
		for _, tc := range loadPruneCases(t, file) {
			if tc.Expr == "" || len(tc.Bindings) > 0 {
				continue
			}
			input := tc.Data
			if tc.Dataset != nil {
				if input, err = os.ReadFile(filepath.Join("testdata", "datasets", *tc.Dataset+".json")); err != nil {
					continue
				}
			}
			var mapData map[string]json.RawMessage
			if json.Unmarshal(input, &mapData) != nil || mapData == nil {
				continue
			}
			expr, err := gnata.Compile(tc.Expr)
			if err != nil {
				continue
			}
			// EvalMap decodes a map's keys in sorted order, as json.Marshal
			// writes them; decode the whole input the same way.
			sorted, err := json.Marshal(mapData)
			if err != nil {
				t.Fatal(err)
			}
			whole, err := gnata.DecodeJSON(sorted)
			if err != nil {
				t.Fatal(err)
			}
			want, wantErr := expr.Eval(context.Background(), whole)
			got, gotErr := gnata.NewStreamEvaluator([]*gnata.Expression{expr}).
				EvalMap(context.Background(), mapData, "", []int{0})

			single, singleErr := expr.EvalMap(context.Background(), mapData)

			name := strings.TrimPrefix(file, filepath.Join("testdata", "groups")+string(filepath.Separator))
			if (wantErr != nil) != (singleErr != nil) || wantErr == nil && !gnata.DeepEqual(want, single) {
				t.Errorf("%s %q: Expression.EvalMap: whole=%v (%v) pruned=%v (%v)", name, tc.Expr, want, wantErr, single, singleErr)
			}
			if (wantErr != nil) != (gotErr != nil) {
				t.Errorf("%s %q: error mismatch: whole=%v pruned=%v", name, tc.Expr, wantErr, gotErr)
				continue
			}
			if wantErr == nil && !gnata.DeepEqual(want, got[0]) {
				t.Errorf("%s %q: whole=%v pruned=%v", name, tc.Expr, want, got[0])
			}
			compared++
			if paths, complete := expr.ReferencedPaths(); complete && len(rootKeys(paths)) < len(mapData) {
				pruned++
			}
		}
	}
	t.Logf("compared %d expressions, %d evaluated on a pruned input", compared, pruned)
	if pruned == 0 {
		t.Fatal("no expression exercised pruning")
	}
}

func rootKeys(paths [][]string) map[string]bool {
	keys := map[string]bool{}
	for _, path := range paths {
		if len(path) > 0 {
			keys[path[0]] = true
		}
	}
	return keys
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
