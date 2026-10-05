package gnata_test

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/recolabs/gnata"
)

// fastPathFunctions are the builtins the function fast path can answer
// straight from raw bytes; each is applied to every parityValues entry.
var fastPathFunctions = []string{
	`$string(v)`, `$boolean(v)`, `$number(v)`, `$not(v)`,
	`$lowercase(v)`, `$uppercase(v)`, `$trim(v)`, `$length(v)`, `$type(v)`,
	`$abs(v)`, `$floor(v)`, `$ceil(v)`, `$sqrt(v)`,
	`$count(v)`, `$reverse(v)`, `$sum(v)`, `$max(v)`, `$min(v)`, `$average(v)`,
	`$exists(v)`, `$keys(v)`, `$distinct(v)`, `$contains(v, "b")`,
	`$count(v.k)`, `$sum(v.k)`, `$exists(v.k)`, `$contains(v.k, "b")`, `$string(v.k)`,
}

var parityValues = []string{
	`{}`,
	`{"v":" Ab  c "}`,
	`{"v":"12"}`,
	`{"v":"1e999"}`,
	`{"v":""}`,
	`{"v":3.5}`,
	`{"v":-4}`,
	`{"v":0}`,
	`{"v":true}`,
	`{"v":false}`,
	`{"v":null}`,
	`{"v":{}}`,
	`{"v":{"k":"abc"}}`,
	`{"v":{"k":1,"j":2}}`,
	`{"v":[]}`,
	`{"v":[1,2,2]}`,
	`{"v":["a","b","a"]}`,
	`{"v":[{"k":1},{"k":2}]}`,
	`{"v":[{"k":"abc"},{"k":"x"}]}`,
	`{"v":[[1],[2,3]]}`,
	`{"v":[1,"a"]}`,
}

var errorCode = regexp.MustCompile(`[A-Z]\d{4}`)

// TestFuncFastPathParity checks that the byte-level function fast path never
// disagrees with full AST evaluation, whether it answers or falls back.
func TestFuncFastPathParity(t *testing.T) {
	for _, src := range fastPathFunctions {
		expr, err := gnata.Compile(src)
		if err != nil {
			t.Fatalf("Compile(%s): %v", src, err)
		}
		for _, data := range parityValues {
			t.Run(src+" "+data, func(t *testing.T) {
				decoded, err := gnata.DecodeJSON(json.RawMessage(data))
				if err != nil {
					t.Fatalf("DecodeJSON: %v", err)
				}
				want := parityOutcome(t, func() (any, error) { return expr.Eval(context.Background(), decoded) })
				gotBytes := parityOutcome(t, func() (any, error) { return expr.EvalBytes(context.Background(), json.RawMessage(data)) })
				if gotBytes != want {
					t.Fatalf("EvalBytes = %s, full evaluation = %s", gotBytes, want)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal([]byte(data), &fields); err != nil {
					t.Fatalf("Unmarshal: %v", err)
				}
				gotMap := parityOutcome(t, func() (any, error) { return expr.EvalMap(context.Background(), fields) })
				if gotMap != want {
					t.Fatalf("EvalMap = %s, full evaluation = %s", gotMap, want)
				}
			})
		}
	}
}

func parityOutcome(t *testing.T, eval func() (any, error)) string {
	t.Helper()
	got, err := eval()
	if err != nil {
		if code := errorCode.FindString(err.Error()); code != "" {
			return "error " + code
		}
		return "error " + err.Error()
	}
	return render(t, got)
}
