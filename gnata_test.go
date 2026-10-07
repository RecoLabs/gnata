package gnata_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/recolabs/gnata"
)

func TestCompile(t *testing.T) {
	expr, err := gnata.Compile("Account.name")
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}
	if expr == nil {
		t.Fatal("expected non-nil expression")
	}
}

func TestCompile_EscapedParenInRegex(t *testing.T) {
	expr := `$contains(x, /(-foo|\bbar\b)\s*\(?\s*x\.y\s+-?eq\s+"z"/)`
	if _, err := gnata.Compile(expr); err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
}

func TestOrderedMap_TypeAssertFromEval(t *testing.T) {
	compiled, err := gnata.Compile(`{"a": 1, "b": 2}`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	result, err := compiled.Eval(context.Background(), nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	om, ok := result.(*gnata.OrderedMap)
	if !ok {
		t.Fatalf("Eval result type %T, want *gnata.OrderedMap", result)
	}
	if got, _ := om.Get("a"); got != float64(1) {
		t.Fatalf("Get(a) = %v, want 1", got)
	}
	normalized, ok := gnata.NormalizeValue(result).(map[string]any)
	if !ok {
		t.Fatalf("NormalizeValue type %T, want map[string]any", gnata.NormalizeValue(result))
	}
	if normalized["b"] != float64(2) {
		t.Fatalf("normalized[b] = %v, want 2", normalized["b"])
	}
}

func TestQuotedPathArrayIndexSelect(t *testing.T) {
	data := map[string]any{
		"testData": []any{
			map[string]any{"value": "output"},
		},
	}
	tests := []struct {
		name string
		expr string
		want any
	}{
		{name: "unquoted", expr: `testData[0].value`, want: "output"},
		{name: "quoted", expr: `"testData"[0]."value"`, want: "output"},
		{name: "mixed", expr: `testData[0]."value"`, want: "output"},
		{name: "rooted quoted", expr: `$."testData"[0]."value"`, want: "output"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := gnata.Compile(tc.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.expr, err)
			}
			got, err := compiled.Eval(context.Background(), data)
			if err != nil {
				t.Fatalf("Eval(%q): %v", tc.expr, err)
			}
			if !gnata.DeepEqual(got, tc.want) {
				t.Errorf("Eval(%q) = %#v (%T), want %#v (%T)", tc.expr, got, got, tc.want, tc.want)
			}
		})
	}
}

func TestJSONNull_TypeAssertFromEval(t *testing.T) {
	compiled, err := gnata.Compile(`{"a": null}`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	result, err := compiled.Eval(context.Background(), nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	om, ok := result.(*gnata.OrderedMap)
	if !ok {
		t.Fatalf("Eval result type %T, want *gnata.OrderedMap", result)
	}
	val, exists := om.Get("a")
	if !exists {
		t.Fatal("missing key a")
	}
	if !gnata.IsNull(val) {
		t.Fatalf("IsNull(%v) = false, want true", val)
	}
	if _, ok := val.(gnata.JSONNull); !ok {
		t.Fatalf("value type %T, want gnata.JSONNull", val)
	}
	if val != gnata.Null {
		t.Fatalf("value %v != gnata.Null", val)
	}
}

// TestRegexAtAPIEdges checks that a regex is its {"pattern", "flags"} map
// in results and custom function arguments, that a custom function can return
// that map as a regex, and that input data of the same shape stays an object.
func TestRegexAtAPIEdges(t *testing.T) {
	regexMap := map[string]any{"pattern": "b", "flags": "g"}
	env := gnata.NewCustomEnvironment(map[string]gnata.CustomFunc{
		"identity": func(args []any, _ any) (any, error) { return args[0], nil },
		"coalesce": func(args []any, _ any) (any, error) {
			for _, arg := range args {
				if arg != nil {
					return arg, nil
				}
			}
			return nil, nil
		},
		"second": func(args []any, _ any) (any, error) { return args[0].([]any)[1], nil },
		"regex": func(args []any, _ any) (any, error) {
			return map[string]any{"pattern": args[0], "flags": "g"}, nil
		},
		"copy": func(args []any, _ any) (any, error) {
			m := args[0].(map[string]any)
			return map[string]any{"pattern": m["pattern"], "flags": m["flags"]}, nil
		},
		"isMap": func(args []any, _ any) (any, error) {
			_, ok := args[0].(map[string]any)
			return ok, nil
		},
	})
	testCases := []struct {
		desc string
		expr string
		data any
		want any
		code string
	}{
		{desc: "result", expr: `/b/`, want: regexMap},
		{desc: "custom function argument", expr: `$isMap(/b/)`, want: true},
		{desc: "custom function round trip", expr: `$identity(/b/)("abc").start`, want: float64(1)},
		{desc: "pass-through of one argument", expr: `$coalesce(nothing, /b/)("abc").start`, want: float64(1)},
		{desc: "pass-through of a nested regex", expr: `$type($second([1, /b/]))`, want: "function"},
		{desc: "built map is an object", expr: `$type($regex("b"))`, want: "object"},
		{desc: "built map is not a regex", expr: `$match("abc", $regex("b"))`, code: "T0410"},
		{desc: "copy of a regex map is an object", expr: `$type($copy(/b/))`, want: "object"},
		{desc: "Go map input", expr: `$type(r)`, data: map[string]any{"r": regexMap}, want: "object"},
		{desc: "Go map input passed through", expr: `$type($identity(r))`, data: map[string]any{"r": regexMap}, want: "object"},
		{
			desc: "Go map input passed through $coalesce", expr: `$type($coalesce(nothing, r))`,
			data: map[string]any{"r": regexMap}, want: "object",
		},
		{desc: "nested result", expr: `{"r": /b/}`, want: map[string]any{"r": regexMap}},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			compiled, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tC.expr, err)
			}
			got, err := compiled.EvalWithCustomEnvironmentAndVars(context.Background(), tC.data, env, nil)
			if tC.code != "" {
				if err == nil || !strings.Contains(err.Error(), tC.code) {
					t.Fatalf("Eval(%q): got %v, want %s", tC.expr, err, tC.code)
				}
				return
			}
			if err != nil {
				t.Fatalf("Eval(%q): %v", tC.expr, err)
			}
			if got = gnata.NormalizeValue(got); !reflect.DeepEqual(got, tC.want) {
				t.Fatalf("Eval(%q) = %#v, want %#v", tC.expr, got, tC.want)
			}
		})
	}
	compiled, err := gnata.Compile(`r("abc")`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := compiled.Eval(context.Background(), map[string]any{"r": regexMap}); err == nil || !strings.Contains(err.Error(), "T1006") {
		t.Fatalf("calling a Go map with a regex's keys: got %v, want T1006", err)
	}
}

func TestEvalWithCustomEnvironmentAndVars(t *testing.T) {
	env := gnata.NewCustomEnvironment(map[string]gnata.CustomFunc{
		"greet": func(args []any, _ any) (any, error) {
			return "hello " + args[0].(string), nil
		},
	})
	compiled, err := gnata.Compile(`$greet($who)`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := compiled.EvalWithCustomEnvironmentAndVars(
		context.Background(), nil, env, map[string]any{"who": "world"},
	)
	if err != nil {
		t.Fatalf("EvalWithCustomEnvironmentAndVars: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("got %v, want %q", got, "hello world")
	}
}

func TestEvalBytesWithCustomFuncs(t *testing.T) {
	env := gnata.NewCustomEnv(map[string]gnata.CustomFunc{
		"double": func(args []any, _ any) (any, error) {
			n, err := args[0].(json.Number).Float64()
			if err != nil {
				return nil, err
			}
			return n * 2, nil
		},
	})
	compiled, err := gnata.Compile(`$double(value)`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := compiled.EvalBytesWithCustomFuncs(context.Background(), []byte(`{"value": 21}`), env)
	if err != nil {
		t.Fatalf("EvalBytesWithCustomFuncs: %v", err)
	}
	if got != float64(42) {
		t.Fatalf("got %v, want 42", got)
	}
}

func TestEvalBytesWithCustomEnvironmentAndVars(t *testing.T) {
	env := gnata.NewCustomEnvironment(map[string]gnata.CustomFunc{
		"greet": func(args []any, _ any) (any, error) {
			return "hello " + args[0].(string), nil
		},
	})
	compiled, err := gnata.Compile(`$greet($who)`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := compiled.EvalBytesWithCustomEnvironmentAndVars(
		context.Background(), []byte(`{}`), env, map[string]any{"who": "world"},
	)
	if err != nil {
		t.Fatalf("EvalBytesWithCustomEnvironmentAndVars: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("got %v, want %q", got, "hello world")
	}
}

// TestEvalBytesWithCustomFuncs_OverridesShadowedBuiltin verifies that a
// custom function registered under a name that collides with a fast-path
// builtin (here "sum") actually runs instead of the builtin gjson-based
// implementation. The function fast path dispatches purely by source-text
// name with no reference to the caller's environment, so EvalBytesWithCustomFuncs
// must skip that tier entirely — otherwise the override would be silently
// ignored whenever the fast path fires.
func TestEvalBytesWithCustomFuncs_OverridesShadowedBuiltin(t *testing.T) {
	env := gnata.NewCustomEnv(map[string]gnata.CustomFunc{
		"sum": func(_ []any, _ any) (any, error) {
			return "overridden", nil
		},
	})
	compiled, err := gnata.Compile(`$sum(nums)`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !compiled.IsFuncFastPath() {
		t.Fatal("expected IsFuncFastPath() == true so this test actually exercises the shadowing risk")
	}
	got, err := compiled.EvalBytesWithCustomFuncs(context.Background(), []byte(`{"nums": [1, 2, 3]}`), env)
	if err != nil {
		t.Fatalf("EvalBytesWithCustomFuncs: %v", err)
	}
	if got != "overridden" {
		t.Fatalf("got %v, want %q (custom function override was ignored, builtin $sum ran instead)", got, "overridden")
	}
}

// TestEvalBytesWithCustomFuncs_FastPathUnaffected checks that a pure-path
// fast-path expression still resolves via gjson (not the custom env / decode
// fallback) when evaluated through the new bytes+custom-env API — the fast
// path never references custom functions, so it must behave identically
// regardless of which environment the caller supplies.
func TestEvalBytesWithCustomFuncs_FastPathUnaffected(t *testing.T) {
	env := gnata.NewCustomEnv(nil)
	compiled, err := gnata.Compile(`Account.Order.Product.SKU`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !compiled.IsFastPath() {
		t.Fatal("expected IsFastPath() == true")
	}
	data := []byte(`{"Account":{"Order":[{"Product":[{"SKU":"a"},{"SKU":"b"}]}]}}`)
	got, err := compiled.EvalBytesWithCustomFuncs(context.Background(), data, env)
	if err != nil {
		t.Fatalf("EvalBytesWithCustomFuncs: %v", err)
	}
	want := []any{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestDeepEqual(t *testing.T) {
	tests := []struct {
		a, b any
		want bool
	}{
		{nil, nil, true},
		{nil, 1.0, false},
		{1.0, 1.0, true},
		{1.0, 2.0, false},
		{"hello", "hello", true},
		{"hello", "world", false},
		{true, true, true},
		{true, false, false},
		{[]any{1.0, 2.0}, []any{1.0, 2.0}, true},
		{[]any{1.0}, []any{1.0, 2.0}, false},
		{map[string]any{"a": 1.0}, map[string]any{"a": 1.0}, true},
		{map[string]any{"a": 1.0}, map[string]any{"a": 2.0}, false},
	}
	for _, tt := range tests {
		got := gnata.DeepEqual(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("DeepEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// The transform operator copies its input, as $clone does, keeping Go values
// and number precision, and never modifies the caller's data, even when the
// expression rebinds $clone to return its argument.
func TestTransformCopiesInput(t *testing.T) {
	const big = json.Number("123456789012345678901234.5")
	newData := func() map[string]any {
		return map[string]any{"i": 5, "s": []string{"x"}, "d": big, "n": nil}
	}
	testCases := []struct {
		desc string
		expr string
		opts []gnata.Option
		want any
	}{
		{
			desc: "keeps Go values",
			expr: `$ ~> |$|{"z":1}|`,
			want: map[string]any{"i": 5, "s": []string{"x"}, "d": big, "n": nil, "z": float64(1)},
		},
		{
			desc: "keeps decimal precision",
			expr: `$ ~> |$|{"z":1}|`,
			opts: []gnata.Option{gnata.WithDecimalPrecision(30)},
			want: map[string]any{"i": 5, "s": []string{"x"}, "d": big, "n": nil, "z": json.Number("1")},
		},
		{
			desc: "copies around a rebound $clone",
			expr: `($clone := function($x){$x}; $ ~> |$|{"z":1}, ["i"]|)`,
			want: map[string]any{"s": []string{"x"}, "d": big, "n": nil, "z": float64(1)},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			expr, err := gnata.Compile(tC.expr, tC.opts...)
			if err != nil {
				t.Fatal(err)
			}
			data := newData()
			got, err := expr.Eval(context.Background(), data)
			if err != nil {
				t.Fatal(err)
			}
			if got := gnata.NormalizeValue(got); !reflect.DeepEqual(got, tC.want) {
				t.Fatalf("got %#v, want %#v", got, tC.want)
			}
			if !reflect.DeepEqual(data, newData()) {
				t.Fatalf("input changed to %#v", data)
			}
		})
	}
}

// Go input can contain itself, through an object or an array, which no
// expression can build. Every walk over it finishes, with or without a
// deadline: a walk that can fail reports U1001 instead of overflowing the
// Go stack or running forever, and one that cannot stops descending where
// the value contains itself.
func TestSelfContainingInput(t *testing.T) {
	customEnv := gnata.NewCustomEnvironment(map[string]gnata.CustomFunc{
		"f": func([]any, any) (any, error) { return "called", nil },
	})
	testCases := slices.Concat(selfContainingObjectCases(), selfContainingArrayCases(), selfContainingTwiceCases())
	for _, tC := range testCases {
		for _, opts := range [][]gnata.Option{nil, {gnata.WithTimeout(time.Minute)}} {
			t.Run(fmt.Sprintf("%s over %s, %d options", tC.expr, tC.desc, len(opts)), func(t *testing.T) {
				expr, err := gnata.Compile(tC.expr, opts...)
				if err != nil {
					t.Fatal(err)
				}
				data, vars := tC.data(), map[string]any(nil)
				if tC.asVar {
					data, vars = nil, map[string]any{"v": data}
				}
				result, err := evalWithin(t, 10*time.Second, func() (any, error) {
					return expr.EvalWithCustomEnvironmentAndVars(context.Background(), data, customEnv, vars)
				})
				switch {
				case tC.wantErr != "":
					if err == nil || !strings.Contains(err.Error(), tC.wantErr) {
						t.Fatalf("got error %v, want %s", err, tC.wantErr)
					}
				case err != nil:
					t.Fatalf("unexpected error: %v", err)
				case tC.want != "":
					if b, err := json.Marshal(result); err != nil || string(b) != tC.want {
						t.Fatalf("got %s (%v), want %s", b, err, tC.want)
					}
				}
			})
		}
	}
	for desc, data := range map[string]func() any{
		"object": selfObject, "array": selfArray, "array twice": selfArrayTwice,
	} {
		t.Run("encoding a result holding "+desc, func(t *testing.T) {
			expr, err := gnata.Compile(`{"x": $}`)
			if err != nil {
				t.Fatal(err)
			}
			result, err := expr.Eval(context.Background(), data())
			if err != nil {
				t.Fatal(err)
			}
			_, err = evalWithin(t, 10*time.Second, func() (any, error) { return json.Marshal(result) })
			if err == nil || !strings.Contains(err.Error(), "U1001") {
				t.Fatalf("got error %v, want U1001", err)
			}
		})
	}
	t.Run("encoding a wide array that contains itself", func(t *testing.T) {
		data := make([]any, 10001)
		for i := range 10000 {
			data[i] = float64(i)
		}
		data[10000] = data
		expr, err := gnata.Compile(`{"x": $}`)
		if err != nil {
			t.Fatal(err)
		}
		result, err := expr.Eval(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err = json.Marshal(result)
		runtime.ReadMemStats(&after)
		if err == nil || !strings.Contains(err.Error(), "U1001") {
			t.Fatalf("got error %v, want U1001", err)
		}
		// Encoding to the cycle check's depth would write about 50MB.
		if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 16<<20 {
			t.Fatalf("allocated %d bytes before failing", alloc)
		}
	})
	t.Run("NormalizeValue over array twice", func(t *testing.T) {
		data := selfArrayTwice()
		if _, err := evalWithin(t, 10*time.Second, func() (any, error) { return gnata.NormalizeValue(data), nil }); err != nil {
			t.Fatal(err)
		}
	})
}

// selfContainingCase is an expression TestSelfContainingInput evaluates
// over Go input that contains itself.
type selfContainingCase struct {
	desc string
	expr string
	data func() any
	// asVar binds the data to $v rather than passing it as the input.
	asVar bool
	// want is the result as JSON; it is not checked when empty, for a
	// result that holds the data.
	want    string
	wantErr string
}

func selfObject() any {
	data := map[string]any{"a": 1.0}
	data["s"] = data
	return data
}

func selfArray() any {
	data := []any{nil}
	data[0] = data
	return data
}

// selfArrayTwice holds itself twice, so a walk that does not stop where it
// re-enters itself visits 2^1000 paths before the cycle check catches it.
func selfArrayTwice() any {
	data := []any{nil, nil}
	data[0], data[1] = data, data
	return data
}

// holdingSelf is an object holding the value self returns.
func holdingSelf(self func() any) func() any {
	return func() any { return map[string]any{"a": map[string]any{"x": 1.0}, "self": self()} }
}

func selfContainingObjectCases() []selfContainingCase {
	object := selfObject
	return []selfContainingCase{
		{desc: "object", expr: `$string($)`, data: object, wantErr: "U1001"},
		{desc: "object", expr: `"" & $`, data: object, wantErr: "U1001"},
		{desc: "object", expr: `$clone($)`, data: object, wantErr: "U1001"},
		{desc: "object", expr: `$f($)`, data: object, want: `"called"`},
		{desc: "object", expr: `$ ~> $f`, data: object, want: `"called"`},
		{desc: "object", expr: `{"a":{"b":1}} ~> |a|{"x": $v}|`, data: object, asVar: true},
		{desc: "object", expr: `$.a ~> |$|{"y": $$.self}|`, data: holdingSelf(object)},
		{desc: "object", expr: `$.a ~> |$|{"y": [$$.self]}|`, data: holdingSelf(object)},
		{desc: "object", expr: `$ = $`, data: object, want: `true`},
		{desc: "object", expr: `$ = $.s`, data: object, want: `true`},
		{desc: "object", expr: `$ in [$]`, data: object, want: `true`},
		{desc: "object", expr: `$boolean($)`, data: object, want: `true`},
		{desc: "object", expr: `$ ? 1 : 2`, data: object, want: `1`},
		{desc: "object", expr: `$.**`, data: object, wantErr: "U1001"},
		{desc: "object", expr: `$count($.**)`, data: object, wantErr: "U1001"},
		{desc: "object", expr: `$.*`, data: object},
		{desc: "object", expr: `$flatten($)`, data: object},
		{desc: "object", expr: `$lookup($, "a")`, data: object, want: `1`},
		{desc: "object", expr: `$keys($)`, data: object, want: `["a","s"]`},
	}
}

func selfContainingArrayCases() []selfContainingCase {
	array := selfArray
	pair := func() any {
		data := []any{1.0, nil}
		data[1] = data
		return data
	}
	return []selfContainingCase{
		{desc: "array", expr: `$string($)`, data: array, wantErr: "U1001"},
		{desc: "array", expr: `"" & $`, data: array, wantErr: "U1001"},
		{desc: "array", expr: `$clone($)`, data: array, wantErr: "U1001"},
		{desc: "array", expr: `$`, data: array},
		{desc: "array", expr: `$f($)`, data: array, want: `"called"`},
		{desc: "array", expr: `$ ~> $f`, data: array, want: `"called"`},
		{desc: "array", expr: `{"a":{"b":1}} ~> |a|{"x": $v}|`, data: array, asVar: true},
		{desc: "array", expr: `$.a ~> |$|{"y": $$.self}|`, data: holdingSelf(array)},
		{desc: "array", expr: `$.a ~> |$|{"y": [$$.self]}|`, data: holdingSelf(array)},
		{desc: "array holding 1", expr: `$.a`, data: pair, want: `null`},
		{desc: "array", expr: `$ = $`, data: array, want: `true`},
		{desc: "array", expr: `$ = $.s`, data: array, want: `false`},
		{desc: "array", expr: `$ in [$]`, data: array, want: `true`},
		{desc: "array", expr: `$boolean($)`, data: array, want: `false`},
		{desc: "array", expr: `$ ? 1 : 2`, data: array, want: `2`},
		{desc: "array", expr: `$.**`, data: array, wantErr: "U1001"},
		{desc: "array", expr: `$count($.**)`, data: array, wantErr: "U1001"},
		{desc: "array", expr: `$.*`, data: array, wantErr: "U1001: cannot search"},
		{desc: "array", expr: `$flatten($)`, data: array, wantErr: "U1001: cannot flatten"},
		{desc: "array", expr: `$lookup($, "a")`, data: array, wantErr: "U1001: cannot look up"},
		{desc: "array", expr: `$spread($)`, data: array, wantErr: "U1001: cannot spread"},
		{desc: "array", expr: `$keys($)`, data: array, want: `null`},
	}
}

func selfContainingTwiceCases() []selfContainingCase {
	twice := selfArrayTwice
	twiceWithX := func() any {
		data := []any{map[string]any{"x": 1.0}, nil, nil}
		data[1], data[2] = data, data
		return data
	}
	return []selfContainingCase{
		{desc: "array twice holding x", expr: `$exists($.x)`, data: twiceWithX, want: `true`},
		{desc: "array twice", expr: `$.a`, data: twice, want: `null`},
		{desc: "array twice", expr: `$.x`, data: twice, want: `null`},
		{desc: "array twice", expr: `$exists($.x)`, data: twice, want: `false`},
		{desc: "array twice", expr: `$boolean($)`, data: twice, want: `false`},
		{desc: "array twice", expr: `$ ? 1 : 2`, data: twice, want: `2`},
		{desc: "array twice", expr: `$ = $.a`, data: twice, want: `false`},
		{desc: "array twice", expr: `$f($)`, data: twice, want: `"called"`},
	}
}

// evalWithin returns eval's outcome, failing the test when it runs longer
// than limit.
func evalWithin(t *testing.T, limit time.Duration, eval func() (any, error)) (any, error) {
	t.Helper()
	type outcome struct {
		result any
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := eval()
		done <- outcome{result, err}
	}()
	select {
	case got := <-done:
		return got.result, got.err
	case <-time.After(limit):
		t.Fatal("evaluation did not finish")
		return nil, nil
	}
}

// Arrays nested by $reduce share their items, so the value is small but
// copying, stringifying or flattening it walks 2^40 leaves; the walk checks
// the deadline.
func TestWithTimeout_SharedValueWalk(t *testing.T) {
	for _, fn := range []string{"$string($v)", "$clone($v)", `"" & $v`, `{"a": $v}.*`, "$flatten($v)"} {
		t.Run(fn, func(t *testing.T) {
			e, err := gnata.Compile(
				`($v := $reduce([1..40], function($a, $i){[$a, $a]}, [1]); `+fn+`)`,
				gnata.WithTimeout(50*time.Millisecond),
			)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			start := time.Now()
			if _, err := e.Eval(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "D1012") {
				t.Fatalf("expected D1012, got %v", err)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("timeout took %v to fire", elapsed)
			}
		})
	}
}
