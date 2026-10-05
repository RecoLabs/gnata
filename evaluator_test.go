package gnata_test

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/recolabs/gnata"
)

func evalExpr(t *testing.T, expr string, data any) any {
	t.Helper()
	e, err := gnata.Compile(expr)
	if err != nil {
		t.Fatalf("compile %q: %v", expr, err)
	}
	result, err := e.Eval(context.Background(), data)
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return result
}

func evalWithVars(t *testing.T, expr string, data any, vars map[string]any) any {
	t.Helper()
	e, err := gnata.Compile(expr)
	if err != nil {
		t.Fatalf("compile %q: %v", expr, err)
	}
	result, err := e.EvalWithVars(context.Background(), data, vars)
	if err != nil {
		t.Fatalf("eval %q with vars: %v", expr, err)
	}
	return result
}

func TestEval(t *testing.T) {
	tests := []struct {
		name string
		expr string
		data any
		want any
	}{
		// Literals
		{"number", "42", nil, float64(42)},
		{"string", `"hello"`, nil, "hello"},
		{"true", "true", nil, true},
		{"false", "false", nil, false},
		{"null", "null", nil, nil},

		// Field access
		{"name lookup", "name", map[string]any{"name": "Alice"}, "Alice"},
		{
			"nested path", "Account.Name",
			map[string]any{"Account": map[string]any{"Name": "Firefly"}},
			"Firefly",
		},

		// Arithmetic
		{"add", "1 + 2", nil, float64(3)},
		{"subtract", "10 - 3", nil, float64(7)},
		{"multiply", "3 * 4", nil, float64(12)},
		{"divide", "10 / 4", nil, float64(2.5)},
		{"modulo", "10 % 3", nil, float64(1)},
		{"power", "2 ** 8", nil, float64(256)},

		// String concatenation
		{"concat", `"hello" & " " & "world"`, nil, "hello world"},

		// Comparison operators
		{"equal true", "1 = 1", nil, true},
		{"not equal true", "1 != 2", nil, true},
		{"less than true", "1 < 2", nil, true},
		{"greater than true", "2 > 1", nil, true},
		{"less or equal", "2 <= 2", nil, true},
		{"greater or equal", "3 >= 2", nil, true},

		// Boolean operators
		{"and true", "true and true", nil, true},
		{"or true", "true or false", nil, true},
		{"and false", "false and true", nil, false},
		{"or false", "false or false", nil, false},

		// Range
		{"range", "1..5", nil, []any{float64(1), float64(2), float64(3), float64(4), float64(5)}},

		// Array constructor
		{"array", "[1, 2, 3]", nil, []any{float64(1), float64(2), float64(3)}},

		// Conditions
		{"condition true branch", "true ? 1 : 2", nil, float64(1)},
		{"condition false branch", "false ? 1 : 2", nil, float64(2)},

		// Field access over arrays
		{
			"name over array", "name",
			[]any{map[string]any{"name": "a"}, map[string]any{"name": "b"}},
			[]any{"a", "b"},
		},

		// Missing field returns nil
		{"missing field", "missing", map[string]any{"name": "Alice"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evalExpr(t, tt.expr, tt.data)
			if !gnata.DeepEqual(got, tt.want) {
				t.Errorf("expr %q: got %v (%T), want %v (%T)",
					tt.expr, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestEvalWithVars(t *testing.T) {
	for _, tC := range []struct {
		desc string
		expr string
		vars map[string]any
		want any
	}{
		{"variable lookup", "$x", map[string]any{"x": "hello"}, "hello"},
		{"variable arithmetic", "$v + 1", map[string]any{"v": float64(5)}, float64(6)},
	} {
		t.Run(tC.desc, func(t *testing.T) {
			if got := evalWithVars(t, tC.expr, nil, tC.vars); !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
	t.Run("variable binding", func(t *testing.T) {
		if got := evalExpr(t, "$x := 42", nil); !gnata.DeepEqual(got, float64(42)) {
			t.Fatalf("got %v, want 42", got)
		}
	})
}

func TestEvalWildcard(t *testing.T) {
	data := map[string]any{"a": float64(1), "b": float64(2)}
	got := evalExpr(t, "*", data)
	// Wildcard returns all values — order is not guaranteed.
	arr, ok := got.([]any)
	if !ok {
		// Single value is also acceptable if the map has one element via unwrap.
		// But we have two elements so it must be an array.
		t.Fatalf("wildcard: got %v (%T), want []any", got, got)
	}
	if len(arr) != 2 {
		t.Errorf("wildcard: got %d elements, want 2", len(arr))
	}
	sum := 0.0
	for _, v := range arr {
		if n, ok := v.(float64); ok {
			sum += n
		}
	}
	if sum != 3.0 {
		t.Errorf("wildcard sum: got %v, want 3.0", sum)
	}
}

func TestEvalLambda(t *testing.T) {
	if got := evalExpr(t, "($f := function($x) { $x * 2 }; $f(5))", nil); !gnata.DeepEqual(got, float64(10)) {
		t.Fatalf("got %v, want 10", got)
	}
}

// TestEvalManyBindings forces a single environment past the point where it
// must switch from small inline storage to a map: each of these binds more
// than a couple of names into one scope (call-site "$" plus several lambda
// params or block variables), which needs to work identically to binding
// just one or two.
func TestEvalManyBindings(t *testing.T) {
	for _, tC := range []struct {
		desc string
		expr string
		want any
	}{
		{"three lambda params", "function($a, $b, $c){$a+$b+$c}(1,2,3)", float64(6)},
		{"five lambda params", "function($a,$b,$c,$d,$e){$a+$b+$c+$d+$e}(1,2,3,4,5)", float64(15)},
		{"four sequential block bindings", "($a:=1; $b:=2; $c:=3; $d:=4; $a+$b+$c+$d)", float64(10)},
		{"rebind an existing name after overflow", "($a:=1; $b:=2; $c:=3; $a:=10; $a+$b+$c)", float64(15)},
	} {
		t.Run(tC.desc, func(t *testing.T) {
			if got := evalExpr(t, tC.expr, nil); !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}

func TestEvalIn(t *testing.T) {
	for _, tC := range []struct {
		desc string
		expr string
		want any
	}{
		{"found", `"a" in ["a", "b", "c"]`, true},
		{"not found", `"z" in ["a", "b", "c"]`, false},
	} {
		t.Run(tC.desc, func(t *testing.T) {
			if got := evalExpr(t, tC.expr, nil); !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}

func TestEvalObjectConstructor(t *testing.T) {
	if got := evalExpr(t, `{"key": "value"}`, nil); !gnata.DeepEqual(got, map[string]any{"key": "value"}) {
		t.Fatalf("got %v, want {key: value}", got)
	}
}

func TestEvalElvis(t *testing.T) {
	for _, tC := range []struct {
		desc string
		expr string
		data any
		want any
	}{
		{"defined", `"hello" ?: "default"`, nil, "hello"},
		{"undefined", `missing ?: "default"`, map[string]any{}, "default"},
	} {
		t.Run(tC.desc, func(t *testing.T) {
			if got := evalExpr(t, tC.expr, tC.data); !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}

func evalJSON(t *testing.T, expr, rawJSON string) any {
	t.Helper()
	var data any
	if err := json.Unmarshal(json.RawMessage(rawJSON), &data); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	e, err := gnata.Compile(expr)
	if err != nil {
		t.Fatalf("compile %q: %v", expr, err)
	}
	result, err := e.Eval(context.Background(), data)
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return result
}

// Regression tests for edge cases found during development.
func TestRegressionJSON(t *testing.T) { //nolint:funlen // TDT data
	//nolint:lll // JSON payloads and JSONata expressions are inherently long single-line strings
	for _, tC := range []struct {
		desc    string
		expr    string
		payload string
		want    any
	}{
		// $contains as method syntax
		{
			desc:    "contains_method_no_match",
			expr:    `$count(payload.value[registeredMethods.$contains("temporaryToken")]) > 0`,
			payload: `{"payload":{"@context":"https://api.example.com/v1/$metadata","value":[{"id":"d47aa714","registeredMethods":["email","phone","pushNotification","oneTimeCode"]}]}}`,
			want:    false,
		},
		{
			desc:    "contains_method_match",
			expr:    `$count(payload.value[registeredMethods.$contains("temporaryToken")]) > 0`,
			payload: `{"payload":{"value":[{"id":"abc","registeredMethods":["temporaryToken","email"]}]}}`,
			want:    true,
		},
		{
			desc:    "contains_method_conditional_access",
			expr:    "payload.value.conditions.applications.includeActions.$contains('register')",
			payload: `{"payload":{"value":[{"conditions":{"applications":{"includeActions":["urn:action:register"]}}}]}}`,
			want:    true,
		},
		{
			desc:    "contains_method_empty_array",
			expr:    "payload.value.conditions.applications.includeActions.$contains('register')",
			payload: `{"payload":{"value":[{"conditions":{"applications":{"includeActions":[]}}}]}}`,
			want:    nil,
		},
		// $join + $map
		{
			desc:    "join_map_single",
			expr:    "$join($map(payload.value, function($v){$v.displayName}), ', ')",
			payload: `{"payload":{"value":[{"displayName":"IOS"}]}}`,
			want:    "IOS",
		},
		{
			desc:    "join_map_multiple",
			expr:    "$join($map(payload.value, function($v){$v.displayName}), ', ')",
			payload: `{"payload":{"value":[{"displayName":"IOS"},{"displayName":"Android"}]}}`,
			want:    "IOS, Android",
		},
		{
			desc:    "join_map_scopes",
			expr:    "($raw := payload.*.App.authConfig.scopes; $values := $append([], $raw.scope ? $raw.scope : $raw); $join($map($values, function($v) { $string($v) }), ', '))",
			payload: `{"payload":{"apps/My_Test_App.app":{"App":{"authConfig":{"scopes":["Api","Web","Full","RefreshToken"]}}}}}`,
			want:    "Api, Web, Full, RefreshToken",
		},
		// Regex literals
		{
			desc:    "regex_in_function_arg",
			expr:    `$exists(payload.value[0].definition[0]) ? (($m := $match(payload.value[0].definition[0], /SessionIdleTimeout":"(\d{2}:\d{2}:\d{2})/); $m ? $m.groups[0] : "Not Configured")) : "Not Configured"`,
			payload: `{"payload":{"value":[{"definition":["{\"TimeoutPolicy\":{\"Policies\":[{\"Id\":\"default\",\"SessionIdleTimeout\":\"01:00:00\"}]}}"]}]}}`,
			want:    "01:00:00",
		},
		{
			desc:    "regex_in_assignment_rhs",
			expr:    `($r := /hello/; $contains("hello world", $r))`,
			payload: "{}",
			want:    true,
		},
		{
			desc:    "regex_in_ternary_then",
			expr:    `true ? $contains("hello", /ell/) : "no"`,
			payload: "{}",
			want:    true,
		},
		{
			desc:    "regex_in_ternary_else",
			expr:    `false ? "yes" : $match("abc", /b/).match`,
			payload: "{}",
			want:    "b",
		},
		// $contains auto-map over $keys
		{
			desc:    "contains_keys_basic",
			expr:    `$contains($keys(settings), "RemoteEndpoint")`,
			payload: `{"settings":{"RemoteEndpoint":{"url":"https://example.com"}}}`,
			want:    true,
		},
		{
			desc:    "contains_keys_nested_lookup",
			expr:    `($remoteKey := $filter($keys(payload), function($k) { $contains($k, 'endpoints/') })[0]; $settings := $lookup(payload, $remoteKey); $contains($keys($settings), "RemoteEndpoint"))`,
			payload: `{"payload":{"endpoints/MySite.endpoint":{"RemoteEndpoint":{"fullName":"MySite","isActive":"true","url":"https://api.example.com"}}}}`,
			want:    true,
		},
		{
			desc:    "contains_keys_no_match",
			expr:    `$contains($keys(settings), "Endpoint")`,
			payload: `{"settings":{"OtherSetting":{"url":"https://example.com"}}}`,
			want:    false,
		},
		{
			desc:    "contains_keys_multi",
			expr:    `$contains($keys(settings), "Endpoint")`,
			payload: `{"settings":{"Foo":1,"RemoteEndpoint":2,"Bar":3}}`,
			want:    true,
		},
		// $map + $filter chain
		{
			desc:    "map_filter_chain_regression",
			expr:    `$count($filter($distinct($map(headers[name="To"].value, function($v) { $match($v, /\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b/).match })), function($v) { false = $contains($v, "example.com") })) > 0`,
			payload: `{"headers":[{"name":"To","value":"\"User\" <user@other.com>, \"admin@example.com\" <admin@example.com>"}]}`,
			want:    true,
		},
		// Empty array filtering
		{
			desc:    "empty_array_filter_with_string_predicate",
			expr:    "$exists(data.groups[$match($lowercase($), /(admin|root)/)])",
			payload: `{"data":{"groups":[]}}`,
			want:    false,
		},
		{
			desc:    "empty_array_filter_simple",
			expr:    `arr[$contains($, "x")]`,
			payload: `{"arr":[]}`,
			want:    nil,
		},
		{
			desc:    "nonempty_array_filter_match",
			expr:    "$exists(data.groups[$match($lowercase($), /(admin|root)/)])",
			payload: `{"data":{"groups":["Admin Users","Regular","Root Access"]}}`,
			want:    true,
		},
		{
			desc:    "nonempty_array_filter_no_match",
			expr:    "$exists(data.groups[$match($lowercase($), /(admin|root)/)])",
			payload: `{"data":{"groups":["Users","Viewers"]}}`,
			want:    false,
		},
		{
			desc:    "empty_array_filter_keepArray",
			expr:    `arr[][$contains($, "x")]`,
			payload: `{"arr":[]}`,
			want:    []any{},
		},
		// $string serialization
		{
			desc:    "string_no_html_escape_ampersand",
			expr:    "$string(urls)",
			payload: `{"urls":["https://example.com?a=1&b=2"]}`,
			want:    `["https://example.com?a=1&b=2"]`,
		},
		{
			desc:    "string_no_html_escape_angle_brackets",
			expr:    "$string(data)",
			payload: `{"data":{"tag":"<div>"}}`,
			want:    `{"tag":"<div>"}`,
		},
		{
			desc:    "string_unicode_escape_decoded",
			expr:    "$string(value)",
			payload: `{"value":["module=Project\u0026action=view"]}`,
			want:    `["module=Project&action=view"]`,
		},
		{
			desc:    "string_scientific_notation_normalized",
			expr:    "$string(score)",
			payload: `{"score":6.312467E-05}`,
			want:    "0.00006312467",
		},
		{
			desc:    "string_small_float_decimal",
			expr:    "$string(val)",
			payload: `{"val":1.23e-4}`,
			want:    "0.000123",
		},
		{
			desc:    "string_very_small_float_scientific",
			expr:    "$string(val)",
			payload: `{"val":1.5e-8}`,
			want:    "1.5e-8",
		},
		{
			desc:    "concat_scientific_notation_normalized",
			expr:    `"score:" & score`,
			payload: `{"score":6.312467E-05}`,
			want:    "score:0.00006312467",
		},
		{
			desc:    "distinct_eq_string_after_dedup",
			expr:    `$distinct(events.name) = "CHANGE_SETTING"`,
			payload: `{"events":[{"name":"CHANGE_SETTING","type":"SETTINGS","parameters":[{"value":"SHARING_CROSS_DOMAIN_OPTIONS"}]},{"name":"CHANGE_SETTING","type":"OTHER"}]}`,
			want:    true,
		},
		{
			desc:    "string_no_html_escape_ordered_map",
			expr:    `$string({"url": urls[0]})`,
			payload: `{"urls":["https://example.com?a=1&b=2"]}`,
			want:    `{"url":"https://example.com?a=1&b=2"}`,
		},
		{
			desc:    "array_field_null_preserved",
			expr:    "$count(items.val)",
			payload: `{"items":[{"val":[1,null,2]}]}`,
			want:    json.Number("3"),
		},
		{
			desc:    "spread_mixed_objects_and_strings",
			expr:    "$spread($)",
			payload: `[{"a":1}, "hello", {"b":2}]`,
			want:    []any{map[string]any{"a": float64(1)}, "hello", map[string]any{"b": float64(2)}},
		},
	} {
		t.Run(tC.desc, func(t *testing.T) {
			if got := evalJSON(t, tC.expr, tC.payload); !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}

// TestRegressionExpr covers regressions that require evalExpr (non-JSON
// input data, literal-only expressions) or direct EvalBytes access.
func TestRegressionExpr(t *testing.T) {
	for _, tC := range []struct {
		desc  string
		expr  string
		input any
		want  any
	}{{
		desc: "spread_preserves_non_object_array_elements",
		expr: `$spread(["hello", "world"])`,
		want: []any{"hello", "world"},
	}, {
		desc: "map_singleton_unwrap_with_match",
		expr: `$map(["foo@bar.com, baz@qux.com"], function($v) { $match($v, /\b\w+@\w+\.\w+\b/).match })`,
		want: []any{"foo@bar.com", "baz@qux.com"},
	}, {
		desc: "map_multi_element_no_unwrap",
		expr: "$map([1, 2, 3], function($v) { $v * 2 })",
		want: []any{float64(2), float64(4), float64(6)},
	}, {
		desc: "distinct_singleton_unwrap_after_dedup",
		expr: "$distinct(items.id)",
		input: map[string]any{"items": []any{
			map[string]any{"id": "uuid1"},
			map[string]any{"id": "uuid1"},
			map[string]any{"id": "uuid1"},
		}},
		want: "uuid1",
	}, {
		desc: "distinct_single_element_input",
		expr: "$distinct([1])",
		want: []any{float64(1)},
	}, {
		desc: "distinct_all_duplicates_literal_unwraps",
		expr: "$distinct([1, 1, 1])",
		want: float64(1),
	}, {
		desc: "distinct_no_unwrap_all_unique",
		expr: "$distinct([1,2,3])",
		want: []any{float64(1), float64(2), float64(3)},
	}, {
		desc: "keys_single_key_singleton_unwrap",
		expr: `$keys({"a": 1})`,
		want: "a",
	}} {
		t.Run(tC.desc, func(t *testing.T) {
			if got := evalExpr(t, tC.expr, tC.input); !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %v (%T), want %v (%T)", got, got, tC.want, tC.want)
			}
		})
	}
}

// TestSingletonUnwrapEdgeCases covers scenarios the JSON suite cannot:
// error expectations and chain operator (~>) paths.
func TestSingletonUnwrapEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		data      string
		want      any
		wantError bool
	}{
		{
			name: "join_nested_each_single_key_errors",
			expr: `$join($each({"x": {"p": "hi", "q": "lo"}},` +
				` function($obj, $name) { $each($obj,` +
				` function($v, $k) { $name & "." & $k & "=" & $v }) })[], ', ')`,
			data:      `{}`,
			wantError: true,
		},
		{
			name: "chain_each_single_key",
			expr: `{"a": 1} ~> $each(function($v, $k) { $k })`,
			data: `{}`,
			want: "a",
		},
		{
			name: "chain_spread_single_key",
			expr: `{"a": 1} ~> $spread()`,
			data: `{}`,
			want: map[string]any{"a": float64(1)},
		},
		{
			name: "chain_bare_spread_single_key",
			expr: `{"a": 1} ~> $spread`,
			data: `{}`,
			want: map[string]any{"a": float64(1)},
		},
		{
			name: "composition_spread_merge",
			expr: `($spread ~> $merge)({"a": 1, "b": 2})`,
			data: `{}`,
			want: map[string]any{"a": float64(1), "b": float64(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var data any
			if err := json.Unmarshal([]byte(tt.data), &data); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			e, err := gnata.Compile(tt.expr)
			if err != nil {
				t.Fatalf("compile %q: %v", tt.expr, err)
			}
			result, err := e.Eval(context.Background(), data)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got %v", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("eval %q: %v", tt.expr, err)
			}
			if tt.want != nil && !gnata.DeepEqual(result, tt.want) {
				got, _ := json.Marshal(result)
				want, _ := json.Marshal(tt.want)
				t.Fatalf("got %s, want %s", got, want)
			}
		})
	}
}

// TestRegressionBytes covers regressions requiring direct EvalBytes
// access (e.g. large integers that exceed float64 precision).
func TestRegressionBytes(t *testing.T) {
	for _, tC := range []struct {
		desc    string
		expr    string
		payload json.RawMessage
		want    any
	}{{
		desc:    "string_large_integer_preserves_precision",
		expr:    "$string(id)",
		payload: json.RawMessage(`{"id":123456789012345678}`),
		want:    "123456789012345678",
	}} {
		t.Run(tC.desc, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if got, err := e.EvalBytes(context.Background(), tC.payload); err != nil {
				t.Fatalf("eval: %v", err)
			} else if !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}

func TestConsArrayPathFilter(t *testing.T) {
	expr := `(tags.[$split($, "=")])[$[0] = "b"].$[1]`
	tests := []struct {
		desc string
		tags []any
		want any
	}{{
		desc: "one match among two tags",
		tags: []any{"a=20", "b=114"},
		want: "114",
	}, {
		desc: "two matches",
		tags: []any{"b=1", "b=2"},
		want: []any{"1", "2"},
	}, {
		desc: "single matching tag",
		tags: []any{"b=114"},
		// Inner path collapses to one cons array; the filter then iterates
		// that array's elements (jsonata-js evaluateFilter), so .$[1] is undefined.
		want: nil,
	}, {
		desc: "no match",
		tags: []any{"a=20", "c=3"},
		want: nil,
	}}
	for _, tC := range tests {
		t.Run(tC.desc, func(t *testing.T) {
			got := evalExpr(t, expr, map[string]any{"tags": tC.tags})
			if !gnata.DeepEqual(got, tC.want) {
				t.Fatalf("got %#v (%T), want %#v (%T)", got, got, tC.want, tC.want)
			}
		})
	}
}

// decimalCase is run with WithDecimalPrecision(78) through Eval, EvalBytes,
// EvalMap and StreamEvaluator. want is the JSON encoding of the result, so json.Number
// precision is checked exactly.
type decimalCase struct {
	desc          string
	expr          string
	payload       string
	want          string
	code          string
	sameInFloat64 bool // also run without decimal precision, expecting the same result
}

const (
	u127 = "170141183460469231731687303715884105728"
	u255 = "57896044618658097711785492504343953926634992332820282019728792003956564819968"
	u256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"
)

func runDecimalCases(t *testing.T, cases []decimalCase) {
	t.Helper()
	for _, tC := range cases {
		t.Run(tC.desc, func(t *testing.T) {
			payload := cmp.Or(tC.payload, "{}")
			dec := json.NewDecoder(strings.NewReader(payload))
			dec.UseNumber()
			var data any
			if err := dec.Decode(&data); err != nil {
				t.Fatalf("decode: %v", err)
			}
			var mapData map[string]json.RawMessage
			if err := json.Unmarshal([]byte(payload), &mapData); err != nil {
				t.Fatalf("decode map: %v", err)
			}
			precs := []int{78}
			if tC.sameInFloat64 {
				precs = append(precs, 0)
			}
			for _, prec := range precs {
				e, err := gnata.Compile(tC.expr, gnata.WithDecimalPrecision(prec))
				if err != nil {
					t.Fatalf("precision %d: compile: %v", prec, err)
				}
				viaEval, evalErr := e.Eval(context.Background(), data)
				viaBytes, bytesErr := e.EvalBytes(context.Background(), json.RawMessage(payload))
				viaMap, mapErr := e.EvalMap(context.Background(), mapData)
				viaStream, streamErr := gnata.NewStreamEvaluator([]*gnata.Expression{e}).EvalOne(context.Background(), json.RawMessage(payload), "k", 0)
				for _, r := range []struct {
					got any
					err error
				}{{viaEval, evalErr}, {viaBytes, bytesErr}, {viaMap, mapErr}, {viaStream, streamErr}} {
					if tC.code != "" {
						if r.err == nil || !strings.Contains(r.err.Error(), tC.code) {
							t.Fatalf("precision %d: expected error %s, got %v (%v)", prec, tC.code, r.got, r.err)
						}
						continue
					}
					if r.err != nil {
						t.Fatalf("precision %d: eval: %v", prec, r.err)
					}
					if b, _ := json.Marshal(r.got); string(b) != tC.want {
						t.Fatalf("precision %d: got %s (%T), want %s", prec, b, r.got, tC.want)
					}
				}
			}
		})
	}
}

func TestWithDecimalPrecisionRange(t *testing.T) {
	tests := []struct {
		digits  int
		wantErr bool
	}{
		{digits: -1, wantErr: true},
		{digits: 0},
		{digits: 16, wantErr: true},
		{digits: 17},
		{digits: 78},
		{digits: 100},
		{digits: 101, wantErr: true},
	}
	for _, tC := range tests {
		_, err := gnata.Compile("1", gnata.WithDecimalPrecision(tC.digits))
		if (err != nil) != tC.wantErr {
			t.Fatalf("precision %d: err = %v, wantErr %v", tC.digits, err, tC.wantErr)
		}
	}
}

// TestDecimalArithmetic covers operators with WithDecimalPrecision.
func TestDecimalArithmetic(t *testing.T) {
	//nolint:lll // large literals
	runDecimalCases(t, []decimalCase{
		{desc: "add_beyond_2^53", expr: "9007199254740993 + 1", want: "9007199254740994"},                                                                                 // float64: 2^53 rounding
		{desc: "add_fields", expr: "a + b", payload: `{"a":9007199254740993,"b":1}`, want: "9007199254740994"},                                                            // float64: 2^53 rounding
		{desc: "add_int64_overflow", expr: "9223372036854775807 + 1", want: "9223372036854775808"},                                                                        // float64: 2^53 rounding
		{desc: "add_decimals", expr: "0.1 + 0.2", want: "0.3"},                                                                                                            // float64: binary fractions inexact
		{desc: "sub_uint256", expr: "a - 1", payload: `{"a":` + u256 + `}`, want: "115792089237316195423570985008687907853269984665640564039457584007913129639934"},       // float64: ~16 digits
		{desc: "mul_to_2^254", expr: "a * a", payload: `{"a":` + u127 + `}`, want: "28948022309329048855892746252171976963317496166410141009864396001978282409984"},       // float64: ~16 digits
		{desc: "mul_rounded", expr: "a * a", payload: `{"a":` + u255 + `}`, want: "3.35195198248564927489350624955146153186984145514809834443089036093044100751839e+153"}, // float64: ~16 digits
		{desc: "div_terminating", expr: "10 / 4", want: "2.5", sameInFloat64: true},
		{desc: "div_rounded", expr: "1 / 3", want: "0." + strings.Repeat("3", 78)},                                            // float64: ~16 digits
		{desc: "div_big_exact", expr: "a / 2", payload: `{"a":` + u127 + `}`, want: "85070591730234615865843651857942052864"}, // float64: ~16 digits
		{desc: "mod_sign_follows_dividend", expr: "-7 % 3", want: "-1", sameInFloat64: true},
		{desc: "mod_decimal", expr: "7.5 % 2", want: "1.5", sameInFloat64: true},
		{desc: "mod_big", expr: "a % 10", payload: `{"a":` + u256 + `}`, want: "5"}, // float64: ~16 digits
		{desc: "mod_by_zero", expr: "a % 0", payload: `{"a":` + u256 + `}`, code: "D3001", sameInFloat64: true},
		{desc: "pow_integer", expr: "2 ** 100", want: "1267650600228229401496703205376"}, // float64: ~16 digits
		{desc: "pow_negative_integer", expr: "2 ** -2", want: "0.25", sameInFloat64: true},
		{desc: "div_counts", expr: "$count(a) / $count(b) = 2 / 3", payload: `{"a":[1,2],"b":[1,2,3]}`, want: "true"},
		{desc: "rem_large_ratio", expr: "1e50 % 7", want: "2"},
		{desc: "overflow_beyond_float64", expr: "a * 2", payload: `{"a":1e308}`, code: "D1001", sameInFloat64: true},
		{desc: "overflow_hidden_by_float64_rounding", expr: "1.0000000000000000001 ** 1e22", code: "D1001"},
		{desc: "power_overflow_hidden_by_float64_rounding", expr: "$power(1.0000000000000000001, 1e25)", code: "D3061"},
		{desc: "sum_overflow_hidden_by_float64_rounding", expr: "$sum([a, 1e289])", payload: `{"a":1.7976931348623158079e308}`, code: "D1001"},
		{desc: "underflow_to_zero", expr: "0.9999999999999999999 ** 1e300", want: "0"},
		{desc: "negate", expr: "-a", payload: `{"a":2.5}`, want: "-2.5", sameInFloat64: true},
		{desc: "range", expr: "[1..3]", want: "[1,2,3]", sameInFloat64: true},
		{desc: "range_beyond_2^53", expr: "[a..a+2]", payload: `{"a":9007199254740993}`, want: "[9007199254740993,9007199254740994,9007199254740995]"},
		{desc: "range_fractional_bound", expr: "[a..a]", payload: `{"a":9007199254740993.5}`, code: "T2003"},
		{desc: "range_fractional_right", expr: "[1..a]", payload: `{"a":2.5}`, code: "T2004", sameInFloat64: true},
		{desc: "pow_fractional", expr: "2 ** 0.5", want: "1.41421356237309504880168872420969807856967187537694807317667973799073247846211"},
		{desc: "pow_rounded", expr: "2 ** 300", want: "2.03703597633448608626844568840937816105146839366593625063614044935438129976334e+90"}, // float64: ~16 digits
		{desc: "pow_overflow", expr: "2 ** 100000", code: "D1001", sameInFloat64: true},
		{desc: "unary_minus", expr: "-a", payload: `{"a":` + u256 + `}`, want: "-" + u256}, // float64: ~16 digits
		{desc: "float_operand", expr: "$count([1,2,3]) * 0.1", want: "0.3"},                // float64: binary fractions inexact
		{desc: "huge_exponent_input_unchanged", expr: "a + 1", payload: `{"a":1e999999}`, code: "T2001", sameInFloat64: true},
		{desc: "huge_digits_input_unchanged", expr: "a + 1", payload: `{"a":` + strings.Repeat("9", 1_000_000) + `}`, code: "T2001", sameInFloat64: true},
		{desc: "add_rounds_half_even", expr: "a * 10 + 5", payload: `{"a":` + u256 + `}`, want: "1.15792089237316195423570985008687907853269984665640564039457584007913129639936e+78"}, // float64: ~16 digits
		{desc: "add_negligible", expr: "1 + 1e-100", want: "1", sameInFloat64: true},
		{desc: "add_beyond_2^256", expr: "a + 2 = a + 1", payload: `{"a":` + u256 + `}`, want: `false`}, // float64: ~16 digits
		{desc: "mul_overflow", expr: "1e308 * 10", code: "D1001", sameInFloat64: true},
		{desc: "div_underflow", expr: "1e-308 / 1e100", want: "0", sameInFloat64: true},
	})
}

// TestDecimalLiterals covers number literals with WithDecimalPrecision.
func TestDecimalLiterals(t *testing.T) {
	runDecimalCases(t, []decimalCase{
		{desc: "negative_literal", expr: "-9007199254740993", want: "-9007199254740993"}, // float64: 2^53 rounding
		{desc: "literal_exponent", expr: "1e3 + 1", want: "1001", sameInFloat64: true},
		{desc: "literal_exponent_string", expr: "$string(1e21)", want: `"1e+21"`, sameInFloat64: true},
		{desc: "literal_negative_zero", expr: "-0", want: "0"},                   // float64: keeps -0
		{desc: "literal_negative_zero_string", expr: "$string(-0)", want: `"0"`}, // float64: keeps -0
		{desc: "literal_trailing_zero", expr: "1.50", want: "1.5", sameInFloat64: true},
		{desc: "literal_underflow", expr: "1e-400", want: "0", sameInFloat64: true},
		{desc: "literal_double_negative", expr: "--1.5", want: "1.5", sameInFloat64: true},
	})
}

// TestDecimalPaths covers numbers read from the input with WithDecimalPrecision.
func TestDecimalPaths(t *testing.T) {
	//nolint:lll // large literals
	runDecimalCases(t, []decimalCase{
		{desc: "path_decimal", expr: "a", payload: `{"a":9007199254740993.5}`, want: `9007199254740993.5`},                                                // float64: 2^53 rounding
		{desc: "path_uint256", expr: "a.b", payload: `{"a":{"b":` + u256 + `}}`, want: u256},                                                              // float64: ~16 digits
		{desc: "path_through_array", expr: "a.b", payload: `{"a":[{"b":9007199254740993.5},{"b":1}]}`, want: `[9007199254740993.5,1]`},                    // float64: 2^53 rounding
		{desc: "path_through_arrays", expr: "a.b.c", payload: `{"a":[{"b":[{"c":0.1},{"c":` + u256 + `}]},{"b":{"c":1}}]}`, want: `[0.1,` + u256 + `,1]`}, // float64: ~16 digits
		{desc: "path_array_of_objects", expr: "a.b", payload: `{"a":[{"b":{"c":` + u256 + `}},{"b":[1.5]}]}`, want: `[{"c":` + u256 + `},1.5]`},           // float64: ~16 digits
		{desc: "path_array_mixed", expr: "a.b", payload: `{"a":[{"b":"x"},{"b":null},{"b":true},{"b":2}]}`, want: `["x",null,true,2]`, sameInFloat64: true},
		{desc: "path_eq_beyond_2^53", expr: "a = 9007199254740992", payload: `{"a":9007199254740993}`, want: `false`}, // float64: 2^53 rounding
		{desc: "path_eq_string", expr: `a = "x"`, payload: `{"a":"x"}`, want: `true`, sameInFloat64: true},
		{desc: "path_distinct", expr: "$distinct(a)", payload: `{"a":[9007199254740993.5,9007199254740993.5]}`, want: `9007199254740993.5`}, // float64: 2^53 rounding
		{desc: "path_string", expr: "$string(a)", payload: `{"a":9007199254740993}`, want: `"9007199254740993"`, sameInFloat64: true},
	})
}

// TestDecimalComparison covers comparison and sorting with WithDecimalPrecision.
func TestDecimalComparison(t *testing.T) {
	//nolint:lll // large literals
	runDecimalCases(t, []decimalCase{
		{desc: "eq_beyond_2^53", expr: "9007199254740993 = 9007199254740992", want: `false`},                      // float64: 2^53 rounding
		{desc: "neq_beyond_2^53", expr: "a != 9007199254740992", payload: `{"a":9007199254740993}`, want: `true`}, // float64: 2^53 rounding
		{desc: "eq_decimal_sum", expr: "0.1 + 0.2 = 0.3", want: `true`},                                           // float64: binary fractions inexact
		{desc: "eq_float_operand", expr: "$count([1,2]) = a", payload: `{"a":2.0}`, want: `true`, sameInFloat64: true},
		{desc: "lt_uint256", expr: "a < " + u256, payload: `{"a":` + u255 + `}`, want: `true`, sameInFloat64: true},
		{desc: "ge_uint256", expr: "a >= " + u256, payload: `{"a":` + u256 + `}`, want: `true`, sameInFloat64: true},
		{desc: "order_by_beyond_2^53", expr: "a^(>$).$string()", payload: `{"a":[9007199254740993,9007199254740995,9007199254740994]}`, want: `["9007199254740995","9007199254740994","9007199254740993"]`, sameInFloat64: true},
		{desc: "sort_beyond_2^53", expr: "$sort(a).$string()", payload: `{"a":[9007199254740995,9007199254740993,9007199254740994]}`, want: `["9007199254740993","9007199254740994","9007199254740995"]`, sameInFloat64: true},
		{desc: "sort_strings", expr: `$sort(["b","a"])`, want: `["a","b"]`, sameInFloat64: true},
		{desc: "sort_mixed", expr: `$sort([1,"a"])`, code: "D3070", sameInFloat64: true},
		{desc: "le_decimal", expr: "1.5 <= 2", want: `true`, sameInFloat64: true},
		{desc: "gt_decimal", expr: "1.5 > 2", want: `false`, sameInFloat64: true},
		{desc: "lt_string", expr: `1.5 < "a"`, code: "T2009", sameInFloat64: true},
	})
}

// TestDecimalFunctions covers the numeric builtins with WithDecimalPrecision.
func TestDecimalFunctions(t *testing.T) {
	//nolint:lll // large literals
	runDecimalCases(t, []decimalCase{
		{desc: "number_uint256", expr: "$number(a)", payload: `{"a":"` + u256 + `"}`, want: u256}, // float64: ~16 digits
		{desc: "number_canonical", expr: "$number(a)", payload: `{"a":1.10}`, want: `1.1`, sameInFloat64: true},
		{desc: "number_hex_uint256", expr: `$number("0x" & $join(["ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff","ff"]))`, want: u256}, // float64: hex beyond int64 rejected
		{desc: "number_hex_too_long", expr: `$number("0x" & a)`, payload: `{"a":"` + strings.Repeat("f", 100_000) + `"}`, code: "D3030", sameInFloat64: true},
		{desc: "number_fraction_rejected", expr: `$number("1/3")`, code: "D3030", sameInFloat64: true},
		{desc: "number_exponent_form", expr: `$number("1e80")`, want: `1e+80`, sameInFloat64: true},
		{desc: "number_overflow", expr: `$number("1e400")`, code: "D3030", sameInFloat64: true},
		{desc: "abs_big", expr: "$abs(a)", payload: `{"a":-` + u256 + `}`, want: u256}, // float64: ~16 digits
		{desc: "floor_negative", expr: "$floor(a)", payload: `{"a":-9007199254740993.5}`, want: `-9007199254740994`, sameInFloat64: true},
		{desc: "ceil_negative", expr: "$ceil(a)", payload: `{"a":-9007199254740993.5}`, want: `-9007199254740993`}, // float64: 2^53 rounding
		{desc: "round_half_even", expr: "$round(2.5) & $round(3.5) & $round(-2.5)", want: `"24-2"`, sameInFloat64: true},
		{desc: "round_places", expr: "$round(a, 2)", payload: `{"a":9007199254740993.125}`, want: `9007199254740993.12`}, // float64: 2^53 rounding
		{desc: "round_negative_places", expr: "$round(a, -2)", payload: `{"a":9007199254740950}`, want: `9007199254741000`, sameInFloat64: true},
		{desc: "round_huge_places_bounded", expr: "$round(1.5, 100000)", want: `1.5`, sameInFloat64: true},
		{desc: "sum_beyond_2^53", expr: "$sum(a)", payload: `{"a":[9007199254740993,1]}`, want: `9007199254740994`},                                                        // float64: 2^53 rounding
		{desc: "sum_decimals", expr: "$sum([0.1, 0.2])", want: `0.3`},                                                                                                      // float64: binary fractions inexact
		{desc: "sum_to_2^256", expr: "$sum(a)", payload: `{"a":[` + u256 + `,1]}`, want: `115792089237316195423570985008687907853269984665640564039457584007913129639936`}, // float64: ~16 digits
		{desc: "average_terminating", expr: "$average(a)", payload: `{"a":[9007199254740993,9007199254740995]}`, want: `9007199254740994`, sameInFloat64: true},
		{desc: "average_rounded", expr: "$average([1, 1, 1.5])", want: `1.1` + strings.Repeat("6", 75) + `7`}, // float64: ~16 digits
		{desc: "max_uint256", expr: "$max(a)", payload: `{"a":[` + u255 + `,` + u256 + `,1]}`, want: u256},    // float64: ~16 digits
		{desc: "min_beyond_2^53", expr: "$min(a)", payload: `{"a":[9007199254740993,9007199254740992]}`, want: `9007199254740992`, sameInFloat64: true},
		{desc: "number_context", expr: "a.$number()", payload: `{"a":"1.50"}`, want: `1.5`, sameInFloat64: true},
		{desc: "number_too_many_args", expr: "$number(1, 2)", code: "T0410", sameInFloat64: true},
		{desc: "number_boolean", expr: "$number(true)", want: `1`, sameInFloat64: true},
		{desc: "number_binary", expr: `$number("0b101")`, want: `5`, sameInFloat64: true},
		{desc: "number_octal", expr: `$number("0o17")`, want: `15`, sameInFloat64: true},
		{desc: "number_hex_invalid", expr: `$number("0xzz")`, code: "D3030", sameInFloat64: true},
		{desc: "abs_no_args", expr: "$abs()", code: "T0410", sameInFloat64: true},
		{desc: "abs_float", expr: `$abs($length("ab"))`, want: `2`, sameInFloat64: true},
		{desc: "round_float", expr: `$round($length("ab"))`, want: `2`, sameInFloat64: true},
		{desc: "round_overflow", expr: "$round(a, -308)", payload: `{"a":9.5e308}`, code: "T0410", sameInFloat64: true},
		{desc: "round_places_string", expr: `$round(1.5, "x")`, code: "T0410", sameInFloat64: true},
		{desc: "sum_no_args", expr: "$sum()", code: "T0410", sameInFloat64: true},
		{desc: "sum_empty", expr: "$sum([])", want: `0`, sameInFloat64: true},
		{desc: "sum_string", expr: `$sum(["1"])`, code: "T0412", sameInFloat64: true},
		{desc: "sum_overflow", expr: "$sum(a)", payload: `{"a":[5e308,5e308]}`, code: "T0412"}, // float64: +Inf, no error
		{desc: "average_empty", expr: "$average([])", want: `null`, sameInFloat64: true},
		{desc: "max_no_args", expr: "$max()", code: "T0410", sameInFloat64: true},
		{desc: "max_empty", expr: "$max([])", want: `null`, sameInFloat64: true},
		{desc: "max_floats", expr: `$max([$length("a"), $length("ab")])`, want: `2`, sameInFloat64: true},
		{desc: "max_string", expr: `$max([1, "a"])`, code: "T0412", sameInFloat64: true},
		{desc: "format_number_big", expr: `$formatNumber(12345678901234567.89, "#,##0.00")`, want: `"12,345,678,901,234,567.89"`},                                                                                            // float64: ~16 digits
		{desc: "format_number_uint256", expr: `$formatNumber(a, "#,##0")`, payload: `{"a":` + u256 + `}`, want: `"115,792,089,237,316,195,423,570,985,008,687,907,853,269,984,665,640,564,039,457,584,007,913,129,639,935"`}, // float64: ~16 digits
		{desc: "format_number_decimal_tie", expr: `$formatNumber(2.675, "0.00")`, want: `"2.68"`},                                                                                                                            // float64: binary fractions inexact
		{desc: "format_number_half_even", expr: `$formatNumber(0.125, "0.00")`, want: `"0.12"`, sameInFloat64: true},
		{desc: "format_number_integer", expr: `$formatNumber(2.5, "0")`, want: `"2"`, sameInFloat64: true},
		{desc: "format_number_negative_picture", expr: `$formatNumber(-1234.5, "#,##0.0;(#,##0.0)")`, want: `"(1,234.5)"`, sameInFloat64: true},
		{desc: "format_number_percent", expr: `$formatNumber(0.1234567890123456789, "0.0000000000000000000%")`, want: `"12.3456789012345678900%"`}, // float64: ~16 digits
		{desc: "format_number_per_mille", expr: `$formatNumber(0.0125, "0.0‰")`, want: `"12.5‰"`, sameInFloat64: true},
		{desc: "format_number_zero_digit", expr: `$formatNumber(1.5, "##٠.٠٠", {"zero-digit": "٠"})`, want: `"١.٥٠"`, sameInFloat64: true},
		{desc: "format_number_exponent", expr: `$formatNumber(1.5, "0.0e0")`, want: `"1.5e0"`, sameInFloat64: true},
		{desc: "format_number_exponent_big", expr: `$formatNumber(12345678901234567.89, "0.0000000000000000000e0")`, want: `"1.2345678901234567890e16"`},                     // float64: ~16 digits
		{desc: "format_number_exponent_uint256", expr: `$formatNumber(a, "0.000000000000000000000e0")`, payload: `{"a":` + u256 + `}`, want: `"1.157920892373161954236e77"`}, // float64: ~16 digits
		{desc: "format_number_exponent_tie", expr: `$formatNumber(1.25, "0.0e0")`, want: `"1.2e0"`},                                                                          // float64: rounds half away from zero
		{desc: "format_number_exponent_carry", expr: `$formatNumber(9.96, "0.0e0")`, want: `"1.0e1"`, sameInFloat64: true},
		{desc: "format_number_exponent_fraction", expr: `$formatNumber(1234, ".00e0")`, want: `".12e4"`, sameInFloat64: true},
		{desc: "format_number_exponent_int_digits", expr: `$formatNumber(12345, "00.0e0")`, want: `"12.3e3"`, sameInFloat64: true},
		{desc: "format_number_exponent_negative", expr: `$formatNumber(-0.00015, "0.0e00")`, want: `"-1.5e-04"`, sameInFloat64: true},
		{desc: "format_number_exponent_tiny", expr: `$formatNumber(1.5e-300, "0.0e0")`, want: `"1.5e-300"`, sameInFloat64: true},
		{desc: "format_number_exponent_zero", expr: `$formatNumber(0, "0.0e0")`, want: `"0.0e0"`, sameInFloat64: true},
		{desc: "format_number_exponent_range", expr: `$substring($formatNumber(1e10, p), 0, 4)`, payload: `{"p":"` + strings.Repeat("0", 309) + `e0"}`, want: `"1000"`, sameInFloat64: true},
		{desc: "format_number_scale_overflow", expr: `$formatNumber(a, "0‰")`, payload: `{"a":9e307}`, want: `"+Inf‰"`, sameInFloat64: true},
		{desc: "format_number_long_picture", expr: `$formatNumber(1.5, p)`, payload: `{"p":"0.` + strings.Repeat("0", 10_001) + `"}`, want: `"1.5` + strings.Repeat("0", 10_000) + `"`, sameInFloat64: true},
		{desc: "format_number_picture_number", expr: `$formatNumber(1.5, 1)`, code: "T0410", sameInFloat64: true},
		{desc: "format_number_no_picture", expr: `$formatNumber(a)`, payload: `{"a":1.5}`, code: "D3006", sameInFloat64: true},
		{desc: "format_number_bad_negative_picture", expr: `$formatNumber(1.5, "0;0.0.0")`, code: "D3081", sameInFloat64: true},
		{desc: "format_number_two_separators", expr: `$formatNumber(1.5, "#;#;#")`, code: "D3080", sameInFloat64: true},
		{desc: "format_base_uint256", expr: `$formatBase(a, 16)`, payload: `{"a":` + u256 + `}`, want: `"` + strings.Repeat("f", 64) + `"`}, // float64: int64 overflow
		{desc: "format_base_beyond_2^53", expr: `$formatBase(9007199254740993, 2)`, want: `"1` + strings.Repeat("0", 52) + `1"`},            // float64: 2^53 rounding
		{desc: "format_base_half_even", expr: `$formatBase(2.5) & $formatBase(-2.5)`, want: `"2-2"`},                                        // float64: rounds half away from zero
		{desc: "format_base_negative", expr: `$formatBase(-255, 16)`, want: `"-ff"`, sameInFloat64: true},
		{desc: "format_base_default", expr: `$formatBase(a)`, payload: `{"a":100}`, want: `"100"`, sameInFloat64: true},
		{desc: "format_base_fraction_base", expr: `$formatBase(255, 16.9)`, want: `"ff"`, sameInFloat64: true},
		{desc: "format_base_bad_base", expr: `$formatBase(1, 37)`, code: "D3100", sameInFloat64: true},
		{desc: "format_base_string_base", expr: `$formatBase(1, "2")`, code: "T0410", sameInFloat64: true},
		{desc: "power_2^100", expr: `$power(2, 100)`, want: `1267650600228229401496703205376`}, // float64: ~16 digits
		{desc: "power_decimal", expr: `$power(1.1, 2)`, want: `1.21`},                          // float64: binary fractions inexact
		{desc: "power_matches_operator", expr: `$power(a, 3) = a ** 3`, payload: `{"a":9007199254740993}`, want: `true`, sameInFloat64: true},
		{desc: "power_negative_exponent", expr: `$power(2, -2)`, want: `0.25`, sameInFloat64: true},
		{desc: "power_fractional_exponent", expr: `$power(4, 0.5)`, want: `2`, sameInFloat64: true},
		{desc: "power_zero_negative", expr: `$power(0, -1)`, code: "D3061", sameInFloat64: true},
		{desc: "power_overflow", expr: `$power(10, 400)`, code: "D3061", sameInFloat64: true},
		{desc: "power_one_argument", expr: `$power(2)`, code: "T0410", sameInFloat64: true},
		{desc: "sqrt", expr: `$sqrt(2)`, want: "1.41421356237309504880168872420969807856967187537694807317667973799073247846211"},
		{desc: "sqrt_exact", expr: `$sqrt(a)`, payload: `{"a":152415787532388367504942236884722755800955129}`, want: "12345678901234567890123"},
		{desc: "sqrt_negative", expr: `$sqrt(-1)`, code: "D3060", sameInFloat64: true},
		{desc: "power_string_exponent", expr: `$power(2, "2")`, code: "T0410", sameInFloat64: true},
	})
}

// TestDecimalNumberArguments covers builtins and path indexes that take a
// count or index, which must accept a json.Number as well as a float64.
func TestDecimalNumberArguments(t *testing.T) {
	runDecimalCases(t, []decimalCase{
		{desc: "split_limit", expr: `$split("a,b,c", ",", n)`, payload: `{"n":2}`, want: `["a","b"]`, sameInFloat64: true},
		{desc: "replace_limit", expr: `$replace("aaa", "a", "b", n)`, payload: `{"n":2}`, want: `"bba"`, sameInFloat64: true},
		{desc: "match_limit", expr: `$count($match("aaa", /a/, n))`, payload: `{"n":2}`, want: "2", sameInFloat64: true},
		{desc: "flatten_depth", expr: `$flatten([[1, [2]]], n)`, payload: `{"n":1}`, want: "[1,[2]]", sameInFloat64: true},
		{desc: "path_index", expr: `arr[$$.n]`, payload: `{"arr":["x","y"],"n":1}`, want: `"y"`, sameInFloat64: true},
		{desc: "flatten_depth_string", expr: `$flatten([1], "x")`, code: "T0410", sameInFloat64: true},
		{desc: "match_limit_string", expr: `$match("a", /a/, "x")`, code: "T0410", sameInFloat64: true},
	})
}

// TestDecimalFastPaths checks that and / or / $not expressions, which have a
// fast path of their own, compare numbers in decimal too.
func TestDecimalFastPaths(t *testing.T) {
	runDecimalCases(t, []decimalCase{
		{desc: "and_number_equal", expr: `a = 0.3 and b`, payload: `{"a":0.30000000000000001,"b":true}`, want: "false"},
		{desc: "and_number_not_equal", expr: `a != 0.3 and b`, payload: `{"a":0.30000000000000001,"b":true}`, want: "true"},
		{desc: "or_number_equal", expr: `a = 1e20 or b`, payload: `{"a":100000000000000000001,"b":false}`, want: "false"},
		{desc: "not_number_equal", expr: `$not(a = 1e20)`, payload: `{"a":100000000000000000001}`, want: "true"},
		{desc: "not_sum", expr: `$not($sum(a))`, payload: `{"a":[100000000000000000001,-100000000000000000000]}`, want: "false"},
		{desc: "and_string_leaves", expr: `$exists(a) and a = "x"`, payload: `{"a":"x"}`, want: "true", sameInFloat64: true},
	})
}

// TestDecimalEquality covers structural equality, in and $distinct, which must
// agree with = on numbers.
func TestDecimalEquality(t *testing.T) {
	const payload = `{"a":100000000000000000001,"b":100000000000000000000,"d":[1.0,2,1e0]}`
	runDecimalCases(t, []decimalCase{
		{desc: "scalar", expr: `a = b`, payload: payload, want: "false"},
		{desc: "in", expr: `a in [b]`, payload: payload, want: "false"},
		{desc: "in_same", expr: `a in [b, a]`, payload: payload, want: "true"},
		{desc: "array", expr: `[a] = [b]`, payload: payload, want: "false"},
		{desc: "object", expr: `{"x": a} = {"x": b}`, payload: payload, want: "false"},
		{desc: "distinct_close_values", expr: `$count($distinct([a, b]))`, payload: payload, want: "2"},
		{desc: "distinct_equal_forms", expr: `$distinct(d)`, payload: payload, want: "[1.0,2]"},
	})
}

// TestDecimalString covers numbers in exponent form, which $string and & must
// format with all their digits.
func TestDecimalString(t *testing.T) {
	//nolint:lll // large literals
	runDecimalCases(t, []decimalCase{
		{desc: "string_product", expr: `$string(a * 1000)`, payload: `{"a":` + u256 + `}`, want: `"1.15792089237316195423570985008687907853269984665640564039457584007913129639935e+80"`},
		{desc: "string_input_exponent", expr: `$string(a)`, payload: `{"a":1.1579208923731619542357098500868790785326998466564056403945758400791312963993e+77}`, want: `"1.1579208923731619542357098500868790785326998466564056403945758400791312963993e+77"`},
		{desc: "concat_input_exponent", expr: `"v=" & a`, payload: `{"a":1.1579208923731619542357098500868790785326998466564056403945758400791312963993e+77}`, want: `"v=1.1579208923731619542357098500868790785326998466564056403945758400791312963993e+77"`},
		{desc: "string_literal_exponent", expr: `$string(1e21)`, want: `"1e+21"`, sameInFloat64: true},
		{desc: "string_computed_exponent", expr: `$string(1e20 * 10)`, want: `"1e+21"`, sameInFloat64: true},
		{desc: "string_large_power", expr: `$string(10 ** 25)`, want: `"1e+25"`, sameInFloat64: true},
		{desc: "string_computed_small", expr: `$string(0.000001 / 10)`, want: `"1e-7"`, sameInFloat64: true},
		{desc: "concat_computed_exponent", expr: `"x" & 10 ** 21`, want: `"x1e+21"`, sameInFloat64: true},
		{desc: "string_array", expr: `$string([1e21, 0.5, a])`, payload: `{"a":0.30000000000000001}`, want: `"[1e+21,0.5,0.30000000000000001]"`},
		{desc: "string_matches_value", expr: `$string(a) = "0.3"`, payload: `{"a":0.30000000000000004}`, want: "false", sameInFloat64: true},
		{desc: "string_small_exponent", expr: `$string(1.5e-7)`, want: `"1.5e-7"`, sameInFloat64: true},
		{desc: "string_small_plain", expr: `$string(a)`, payload: `{"a":1.5e-6}`, want: `"0.0000015"`, sameInFloat64: true},
	})
}
