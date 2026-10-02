package gnata_test

import (
	"context"
	"encoding/json"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/recolabs/gnata"
)

const boolFastTestData = `{
	"event": {"action": "file.download", "count": 3, "enabled": true, "note": "", "tags": ["a", "b"]},
	"actor": {"email": "alice@example.com", "role": "admin", "flags": [""], "disabled": false},
	"records": [
		{"type": "login", "user": {"email": "bob@example.com", "groups": ["ops"]}},
		{"type": "logout"}
	],
	"missing_parent": null
}`

func decodeBoolFastFixture(t *testing.T) (raw json.RawMessage, mapData map[string]json.RawMessage, decoded any) {
	t.Helper()
	raw = json.RawMessage(boolFastTestData)
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	mapData = map[string]json.RawMessage{}
	for key, value := range decoded.(map[string]any) {
		mapData[key] = mustMarshal(t, value)
	}
	return raw, mapData, decoded
}

func assertBytesAndMapMatchEval(
	t *testing.T, expr *gnata.Expression, raw json.RawMessage, mapData map[string]json.RawMessage, decoded any,
) {
	t.Helper()
	ctx := context.Background()
	want, wantErr := expr.Eval(ctx, decoded)
	gotBytes, bytesErr := expr.EvalBytes(ctx, raw)
	gotMap, mapErr := expr.EvalMap(ctx, mapData)
	if (wantErr == nil) != (bytesErr == nil) || (wantErr == nil) != (mapErr == nil) {
		t.Fatalf("error mismatch: Eval=%v EvalBytes=%v EvalMap=%v", wantErr, bytesErr, mapErr)
	}
	if !reflect.DeepEqual(gotBytes, want) {
		t.Fatalf("EvalBytes = %#v, want %#v (from Eval)", gotBytes, want)
	}
	if !reflect.DeepEqual(gotMap, want) {
		t.Fatalf("EvalMap = %#v, want %#v (from Eval)", gotMap, want)
	}
}

func TestBooleanFastPath_Classification(t *testing.T) {
	testCases := []struct {
		expr string
		want bool
	}{
		{`event.action = "file.download" and actor.role = "admin"`, true},
		{`event.action = "a" or event.action = "b" or event.action = "c"`, true},
		{`$exists(actor.email) and actor.email != ""`, true},
		{`($exists(actor.email) and actor.email != null and actor.email != "")`, true},
		{`$not(actor.role = "admin")`, true},
		{`$not($exists(actor.email) and actor.disabled = true)`, true},
		{`actor.disabled or $contains(actor.email, "@example.com")`, true},
		{`$exists(records.user.email) and records.type = "login"`, true},
		{`$not(actor.role)`, false},
		{`actor.role = "admin"`, false},
		{`event.count > 1 and actor.role = "admin"`, false},
		{`records[type = "login"].user.email = "x" or actor.role = "admin"`, false},
		{`event.action in ["a", "b"] and actor.role = "admin"`, false},
		{`$lowercase($string(actor.role)) = "admin" and $exists(actor.email)`, false},
	}
	for _, tC := range testCases {
		t.Run(tC.expr, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tC.expr, err)
			}
			if got := expr.IsBooleanFastPath(); got != tC.want {
				t.Fatalf("IsBooleanFastPath(%q) = %v, want %v", tC.expr, got, tC.want)
			}
		})
	}
}

func TestBooleanFastPath_MatchesEval(t *testing.T) {
	testCases := []struct {
		desc string
		expr string
	}{
		{desc: "and of two true comparisons", expr: `event.action = "file.download" and actor.role = "admin"`},
		{desc: "and short-circuits on a false left side", expr: `event.action = "other" and actor.role = "admin"`},
		{desc: "or chain with the match last", expr: `event.action = "a" or event.action = "b" or event.action = "file.download"`},
		{desc: "or chain with no match", expr: `event.action = "a" or event.action = "b"`},
		{desc: "exists guard and non-empty check", expr: `$exists(actor.email) and actor.email != ""`},
		{desc: "exists guard on a missing field", expr: `$exists(actor.phone) and actor.phone != ""`},
		{desc: "parenthesized guard chain", expr: `($exists(actor.email) and actor.email != null and actor.email != "")`},
		{desc: "not of a comparison", expr: `$not(actor.role = "admin")`},
		{desc: "not of a composition", expr: `$not($exists(actor.email) and actor.disabled = true)`},
		{desc: "pure-path leaf cast to boolean", expr: `actor.disabled or event.enabled`},
		{desc: "empty-string pure-path leaf is false", expr: `event.note or actor.disabled`},
		{desc: "array pure-path leaf is truthy", expr: `event.tags and actor.role = "admin"`},
		{desc: "contains leaf", expr: `actor.disabled or $contains(actor.email, "@example.com")`},
		{desc: "leaves crossing an array", expr: `$exists(records.user.email) and records.type = "login"`},
		{desc: "one-element array leaf behind an array", expr: `$exists(records.user.groups) and records.user.groups != "ops"`},
		{desc: "one-element empty-string array leaf", expr: `$exists(actor.flags) and actor.flags != ""`},
		{desc: "null leaf", expr: `missing_parent = null and actor.role = "admin"`},
		{desc: "nested and inside or", expr: `(event.action = "x" and actor.role = "admin") or (event.count = 3 and $exists(actor.email))`},
	}

	raw, mapData, decoded := decodeBoolFastFixture(t)
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tC.expr, err)
			}
			if !expr.IsBooleanFastPath() {
				t.Fatalf("expected %q to compile as a boolean fast path", tC.expr)
			}
			assertBytesAndMapMatchEval(t, expr, raw, mapData, decoded)
		})
	}
}

func TestBooleanFastPath_FallsBackWhenALeafCannotBeAnswered(t *testing.T) {
	testCases := []struct {
		desc string
		expr string
	}{
		{desc: "pure-path leaf that is undefined", expr: `actor.missing or actor.role = "admin"`},
		{desc: "not of an undefined pure path inside and", expr: `actor.role = "admin" and $not(actor.missing and actor.disabled)`},
		{desc: "contains on a number raises the full evaluator's error", expr: `actor.role = "admin" and $contains(event.count, "3")`},
		{desc: "contains on a number is never reached after a false and", expr: `actor.role = "other" and $contains(event.count, "3")`},
	}

	raw, mapData, decoded := decodeBoolFastFixture(t)
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tC.expr, err)
			}
			assertBytesAndMapMatchEval(t, expr, raw, mapData, decoded)
		})
	}
}

func TestBooleanFastPath_CustomFunctionShadowsBuiltin(t *testing.T) {
	env := gnata.NewCustomEnv(map[string]gnata.CustomFunc{
		"exists": func(_ []any, _ any) (any, error) { return false, nil },
	})
	expr, err := gnata.Compile(`$exists(actor.email) and actor.role = "admin"`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := expr.EvalBytesWithCustomFuncs(context.Background(), json.RawMessage(boolFastTestData), env)
	if err != nil {
		t.Fatal(err)
	}
	if got != false {
		t.Fatalf("EvalBytesWithCustomFuncs = %#v, want false from the custom $exists", got)
	}
}

func TestStreamEvaluator_BooleanFastPath_MatchesEval(t *testing.T) {
	exprStrs := []string{
		`event.action = "file.download"`,
		`$exists(actor.email) and actor.email != ""`,
		`$not(actor.role = "admin") or event.count = 3`,
		`(event.action = "x" and actor.role = "admin") or $exists(records.user.email)`,
	}
	hook := &fastPathCounter{}
	se := gnata.NewStreamEvaluator(nil, gnata.WithMetricsHook(hook))
	indices := make([]int, 0, len(exprStrs))
	for _, s := range exprStrs {
		idx, err := se.Compile(s)
		if err != nil {
			t.Fatalf("Compile(%q): %v", s, err)
		}
		indices = append(indices, idx)
	}

	raw, mapData, decoded := decodeBoolFastFixture(t)
	ctx := context.Background()
	gotMany, err := se.EvalMany(ctx, raw, "bool-schema", indices)
	if err != nil {
		t.Fatal(err)
	}
	gotMap, err := se.EvalMap(ctx, mapData, "bool-schema-map", indices)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range exprStrs {
		expr, err := gnata.Compile(s)
		if err != nil {
			t.Fatal(err)
		}
		want, err := expr.Eval(ctx, decoded)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotMany[i], want) || !reflect.DeepEqual(gotMap[i], want) {
			t.Fatalf("%q: EvalMany = %#v, EvalMap = %#v, want %#v", s, gotMany[i], gotMap[i], want)
		}
	}
	if hook.slow != 0 {
		t.Fatalf("expected every expression on the fast path, got %d full evaluations", hook.slow)
	}
}

func TestBooleanFastPath_Differential(t *testing.T) {
	exprStrs := []string{
		`a.b = "x" and c = "y"`,
		`a.b = "x" or a.b = "z" or c != "y"`,
		`$exists(a.b) and a.b != ""`,
		`$exists(a.b) and a.b != null and a.b != ""`,
		`$not(a.b = "x")`,
		`$not($exists(a.b) and c = true)`,
		`(a.b = 1 or c = false) and $exists(d.e)`,
		`a.b != "x" and $not(c = "y") and d.e = null`,
		`$exists(d.e) or (a.b = "x" and c != "x")`,
	}
	literals := []any{"x", "y", "z", "", nil, true, false, 1.0, 0.0}
	r := rand.New(rand.NewSource(1))
	randomValue := func() any {
		pick := func() any { return literals[r.Intn(len(literals))] }
		switch r.Intn(8) {
		case 0:
			return []any{}
		case 1:
			return []any{pick()}
		case 2:
			return []any{pick(), pick()}
		case 3:
			return map[string]any{"k": pick()}
		default:
			return pick()
		}
	}
	setPath := func(root map[string]any, path string) {
		steps := strings.Split(path, ".")
		cur := root
		for i, step := range steps {
			if i == len(steps)-1 {
				cur[step] = randomValue()
				return
			}
			next := map[string]any{}
			if r.Intn(3) == 0 {
				cur[step] = []any{next, map[string]any{"other": 1}}
			} else {
				cur[step] = next
			}
			cur = next
		}
	}

	ctx := context.Background()
	for _, s := range exprStrs {
		expr, err := gnata.Compile(s)
		if err != nil {
			t.Fatalf("Compile(%q): %v", s, err)
		}
		if !expr.IsBooleanFastPath() {
			t.Fatalf("expected %q to compile as a boolean fast path", s)
		}
		for range 2000 {
			doc := map[string]any{}
			for _, p := range []string{"a.b", "c", "d.e"} {
				if r.Intn(4) != 0 {
					setPath(doc, p)
				}
			}
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			var decoded any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			want, wantErr := expr.Eval(ctx, decoded)
			got, gotErr := expr.EvalBytes(ctx, raw)
			if (wantErr == nil) != (gotErr == nil) || !reflect.DeepEqual(got, want) {
				t.Fatalf("%q on %s:\n  EvalBytes = %#v (err %v)\n  Eval      = %#v (err %v)", s, raw, got, gotErr, want, wantErr)
			}
		}
	}
}

func TestStreamEvaluator_BooleanFastPath_SkippedWhenBatchDecodes(t *testing.T) {
	exprStrs := []string{
		`$exists(actor.email) and actor.email != ""`,
		`records[type = "login"].user.email = "bob@example.com"`,
	}
	hook := &fastPathCounter{}
	se := gnata.NewStreamEvaluator(nil, gnata.WithMetricsHook(hook))
	indices := make([]int, 0, len(exprStrs))
	for _, s := range exprStrs {
		idx, err := se.Compile(s)
		if err != nil {
			t.Fatalf("Compile(%q): %v", s, err)
		}
		indices = append(indices, idx)
	}

	_, mapData, _ := decodeBoolFastFixture(t)
	got, err := se.EvalMap(context.Background(), mapData, "bool-mixed-schema", indices)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != true || got[1] != true {
		t.Fatalf("EvalMap = %#v, want [true true]", got)
	}
	if hook.fast != 0 || hook.slow != 2 {
		t.Fatalf("expected both expressions on the full evaluator, got fast=%d slow=%d", hook.fast, hook.slow)
	}
}

func TestStreamEvaluator_BooleanFastPath_CustomFunctionShadowsBuiltin(t *testing.T) {
	data := json.RawMessage(`{"a":1,"b":2}`)
	custom := func(_ []any, _ any) (any, error) { return "CUSTOM", nil }
	testCases := []struct {
		desc     string
		custom   string
		expr     string
		wantFast int
		wantSlow int
	}{
		{desc: "shadowed $not", custom: "not", expr: `$not(a = 1)`, wantSlow: 1},
		{desc: "shadowed $not inside and", custom: "not", expr: `$not(a = 1) and b = 2`, wantSlow: 1},
		{desc: "shadowed $exists leaf", custom: "exists", expr: `$exists(a) or b = 3`, wantSlow: 1},
		{desc: "unrelated custom function keeps the fast path", custom: "myFunc", expr: `$not(a = 1) and $exists(b)`, wantFast: 1},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			funcs := map[string]gnata.CustomFunc{tC.custom: custom}
			counter := &fastPathCounter{}
			se := gnata.NewStreamEvaluator(nil, gnata.WithMetricsHook(counter), gnata.WithCustomFunctions(funcs))
			idx, err := se.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tC.expr, err)
			}
			got, err := se.EvalMany(context.Background(), data, "shadow-bool-schema", []int{idx})
			if err != nil {
				t.Fatal(err)
			}

			expr, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatal(err)
			}
			var decoded any
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			want, err := expr.EvalWithCustomFuncs(context.Background(), decoded, gnata.NewCustomEnv(funcs))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got[0], want) {
				t.Fatalf("EvalMany(%q) = %#v, want %#v (from the full evaluator)", tC.expr, got[0], want)
			}
			if counter.fast != tC.wantFast || counter.slow != tC.wantSlow {
				t.Fatalf("fast=%d slow=%d, want fast=%d slow=%d", counter.fast, counter.slow, tC.wantFast, tC.wantSlow)
			}
		})
	}
}
