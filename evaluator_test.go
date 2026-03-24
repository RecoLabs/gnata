package gnata_test

import (
	"context"
	"encoding/json"
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
