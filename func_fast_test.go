package gnata_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/recolabs/gnata"
)

var funcTestJSON = json.RawMessage(`{
	"name": "Alice Smith",
	"age": 30,
	"score": -4.5,
	"active": true,
	"deleted": false,
	"email": null,
	"tags": ["admin", "user", "super-admin"],
	"scores": [10, 20, 30, 10],
	"ones": [1, 1, 1],
	"mixed": [1, "two", 3],
	"empty": [],
	"emptyobj": {},
	"profile": {"role": "admin", "level": 5},
	"greeting": "  Hello World  ",
	"spaced": "  hello   world  ",
	"unicode": "héllo",
	"zero": 0,
	"emptystr": "",
	"sci": 1.23e5,
	"numstr": "42",
	"floatstr": "3.14",
	"negstr": "-123",
	"badnum": "not-a-number",
	"bools_arr": [true, false, true],
	"mixed_types": [1, "1", true, 1],
	"with_nulls": [1, null, 2, null, 3],
	"obj_arr": [{"a":1}, {"b":2}],
	"has_space": "  hello  ",
	"special": "foo\tbar"
}`)

func evalBytesExpr(t *testing.T, expr string, jsonData json.RawMessage) any {
	t.Helper()
	e, err := gnata.Compile(expr)
	if err != nil {
		t.Fatalf("compile %q: %v", expr, err)
	}
	result, err := e.EvalBytes(context.Background(), jsonData)
	if err != nil {
		t.Fatalf("EvalBytes %q: %v", expr, err)
	}
	return result
}

func assertFastPathMatchesFull(t *testing.T, expr string, jsonData json.RawMessage) {
	t.Helper()
	e, err := gnata.Compile(expr)
	if err != nil {
		t.Fatalf("compile %q: %v", expr, err)
	}
	if !e.IsFuncFastPath() {
		t.Fatalf("expected %q to compile as function fast path", expr)
	}
	ctx := context.Background()

	fastResult, fastErr := e.EvalBytes(ctx, jsonData)

	parsed, parseErr := gnata.DecodeJSON(jsonData)
	if parseErr != nil {
		t.Fatalf("DecodeJSON: %v", parseErr)
	}
	fullResult, fullErr := e.Eval(ctx, parsed)

	if fastErr != nil && fullErr != nil {
		return
	}
	if fastErr != nil || fullErr != nil {
		t.Fatalf("error mismatch for %q: fast=%v full=%v", expr, fastErr, fullErr)
	}

	fastNorm := gnata.NormalizeValue(fastResult)
	fullNorm := gnata.NormalizeValue(fullResult)
	if !gnata.DeepEqual(fastNorm, fullNorm) {
		t.Fatalf("result mismatch for %q:\n  fast: %v (%T)\n  full: %v (%T)", expr, fastNorm, fastNorm, fullNorm, fullNorm)
	}

	fastType := fmt.Sprintf("%T", fastResult)
	fullType := fmt.Sprintf("%T", fullResult)
	if fastType != fullType {
		t.Fatalf("type mismatch for %q: fast=%s full=%s (value: %v)", expr, fastType, fullType, fastResult)
	}
}

func TestFuncFastPathExists(t *testing.T) {
	for _, tC := range []struct {
		name string
		expr string
	}{
		{"existing field", `$exists(name)`},
		{"missing field", `$exists(missing)`},
		{"null field", `$exists(email)`},
	} {
		t.Run(tC.name, func(t *testing.T) {
			assertFastPathMatchesFull(t, tC.expr, funcTestJSON)
		})
	}
}

func TestFuncFastPathContainsAndString(t *testing.T) {
	for _, tC := range []struct {
		name string
		expr string
		data json.RawMessage
	}{
		{"$contains substring match", `$contains(name, "Alice")`, funcTestJSON},
		{"$contains substring no match", `$contains(name, "Bob")`, funcTestJSON},
		{"$contains array auto-map", `$contains(tags, "admin")`, funcTestJSON},
		{"$contains case sensitive", `$contains(name, "smith")`, funcTestJSON},
		{"$contains empty literal", `$contains(name, "")`, funcTestJSON},
		{"$contains unicode", `$contains(unicode, "éll")`, funcTestJSON},
		{"$contains special chars", `$contains(special, "\t")`, funcTestJSON},
		{"$contains array no match", `$contains(tags, "nobody")`, funcTestJSON},
		{"$string number", `$string(age)`, funcTestJSON},
		{"$string boolean", `$string(active)`, funcTestJSON},
		{"$string string passthrough", `$string(name)`, funcTestJSON},
		{"$string negative float", `$string(score)`, funcTestJSON},
		{"$string null", `$string(email)`, funcTestJSON},
		{"$string zero", `$string(zero)`, funcTestJSON},
		{"$string scientific notation", `$string(sci)`, funcTestJSON},
		{"$string missing field", `$string(missing)`, funcTestJSON},
	} {
		t.Run(tC.name, func(t *testing.T) {
			assertFastPathMatchesFull(t, tC.expr, tC.data)
		})
	}
}

func TestFuncFastPathBoolean(t *testing.T) {
	for _, tC := range []struct {
		name string
		expr string
		data json.RawMessage
	}{
		{"true value", `$boolean(active)`, funcTestJSON},
		{"false value", `$boolean(deleted)`, funcTestJSON},
		{"non-empty string", `$boolean(name)`, funcTestJSON},
		{"non-zero number", `$boolean(age)`, funcTestJSON},
		{"null", `$boolean(email)`, funcTestJSON},
		{"zero", `$boolean(zero)`, funcTestJSON},
		{"empty string", `$boolean(emptystr)`, funcTestJSON},
		{"negative float", `$boolean(score)`, funcTestJSON},
		{"string false is truthy", `$boolean(val)`, json.RawMessage(`{"val": "false"}`)},
		{"string zero is truthy", `$boolean(val)`, json.RawMessage(`{"val": "0"}`)},
	} {
		t.Run(tC.name, func(t *testing.T) {
			assertFastPathMatchesFull(t, tC.expr, tC.data)
		})
	}
}

func TestFuncFastPathNumber(t *testing.T) {
	for _, tC := range []struct {
		name string
		expr string
	}{
		{"number passthrough", `$number(age)`},
		{"true to 1", `$number(active)`},
		{"false to 0", `$number(deleted)`},
		{"string integer", `$number(numstr)`},
		{"string float", `$number(floatstr)`},
		{"string negative", `$number(negstr)`},
		{"scientific notation", `$number(sci)`},
		{"zero", `$number(zero)`},
		{"negative float", `$number(score)`},
	} {
		t.Run(tC.name, func(t *testing.T) {
			assertFastPathMatchesFull(t, tC.expr, funcTestJSON)
		})
	}
}

func TestFuncFastPathNot(t *testing.T) {
	for _, tC := range []struct {
		name string
		expr string
	}{
		{"negate true", `$not(active)`},
		{"negate false", `$not(deleted)`},
		{"null", `$not(email)`},
	} {
		t.Run(tC.name, func(t *testing.T) {
			assertFastPathMatchesFull(t, tC.expr, funcTestJSON)
		})
	}
}

func TestFuncFastPathStringFuncs(t *testing.T) {
	for _, tC := range []struct {
		name string
		expr string
	}{
		{"$lowercase", `$lowercase(name)`},
		{"$uppercase", `$uppercase(name)`},
		{"$trim leading and trailing", `$trim(greeting)`},
		{"$trim internal whitespace normalization", `$trim(spaced)`},
		{"$length ascii string", `$length(name)`},
		{"$length unicode string", `$length(unicode)`},
	} {
		t.Run(tC.name, func(t *testing.T) {
			assertFastPathMatchesFull(t, tC.expr, funcTestJSON)
		})
	}
}

func TestFuncFastPathType(t *testing.T) {
	for _, tC := range []struct {
		name string
		expr string
	}{
		{"string", `$type(name)`},
		{"number", `$type(age)`},
		{"boolean", `$type(active)`},
		{"null", `$type(email)`},
		{"array", `$type(tags)`},
		{"object", `$type(profile)`},
	} {
		t.Run(tC.name, func(t *testing.T) {
			assertFastPathMatchesFull(t, tC.expr, funcTestJSON)
		})
	}
}

func TestFuncFastPathMath(t *testing.T) {
	for _, tC := range []struct {
		name string
		expr string
	}{
		{"$abs negative float", `$abs(score)`},
		{"$abs positive int", `$abs(age)`},
		{"$floor", `$floor(score)`},
		{"$ceil", `$ceil(score)`},
		{"$sqrt", `$sqrt(age)`},
	} {
		t.Run(tC.name, func(t *testing.T) {
			assertFastPathMatchesFull(t, tC.expr, funcTestJSON)
		})
	}

	t.Run("$round falls through to full evaluator", func(t *testing.T) {
		e, err := gnata.Compile(`$round(score)`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if e.IsFuncFastPath() {
			t.Fatal("$round should NOT be function fast path (uses banker's rounding)")
		}
		evalBytesExpr(t, `$round(score)`, funcTestJSON)
	})
}

func TestFuncFastPathCount(t *testing.T) {
	for _, tC := range []struct {
		name string
		expr string
	}{
		{"array 3", `$count(tags)`},
		{"array 4", `$count(scores)`},
		{"scalar", `$count(name)`},
		{"empty array", `$count(empty)`},
		{"missing field", `$count(missing)`},
	} {
		t.Run(tC.name, func(t *testing.T) {
			assertFastPathMatchesFull(t, tC.expr, funcTestJSON)
		})
	}

	t.Run("path through intermediate array", func(t *testing.T) {
		data := json.RawMessage(`{
			"payload": {
				"value": {
					"conditions": [
						{"users": {"excludeUsers": ["user1"]}},
						{"users": {"excludeUsers": ["user2"]}}
					]
				}
			}
		}`)
		e, err := gnata.Compile(`$count(payload.value.conditions.users.excludeUsers)`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		ctx := context.Background()

		fastResult, fastErr := e.EvalBytes(ctx, data)
		parsed, parseErr := gnata.DecodeJSON(data)
		if parseErr != nil {
			t.Fatalf("DecodeJSON: %v", parseErr)
		}
		fullResult, fullErr := e.Eval(ctx, parsed)

		if fastErr != nil || fullErr != nil {
			t.Fatalf("errors: fast=%v full=%v", fastErr, fullErr)
		}
		fastNorm := gnata.NormalizeValue(fastResult)
		fullNorm := gnata.NormalizeValue(fullResult)
		if !gnata.DeepEqual(fastNorm, fullNorm) {
			t.Fatalf("result mismatch:\n  fast: %v (%T)\n  full: %v (%T)", fastNorm, fastNorm, fullNorm, fullNorm)
		}
	})
}

func TestFuncFastPathArrayOps(t *testing.T) {
	t.Run("$reverse", func(t *testing.T) {
		for _, tC := range []struct {
			name string
			expr string
		}{
			{"array", `$reverse(tags)`},
			{"string", `$reverse(name)`},
			{"empty array", `$reverse(empty)`},
		} {
			t.Run(tC.name, func(t *testing.T) {
				assertFastPathMatchesFull(t, tC.expr, funcTestJSON)
			})
		}
	})

	t.Run("$distinct", func(t *testing.T) {
		for _, tC := range []struct {
			name string
			expr string
			data json.RawMessage
		}{
			{"with duplicates", `$distinct(scores)`, funcTestJSON},
			{"all same values", `$distinct(ones)`, funcTestJSON},
			{"empty array", `$distinct(empty)`, funcTestJSON},
			{"no duplicates", `$distinct(tags)`, funcTestJSON},
			{"mixed types number and string", `$distinct(mixed_types)`, funcTestJSON},
			{"with nulls", `$distinct(with_nulls)`, funcTestJSON},
			{"booleans", `$distinct(bools_arr)`, funcTestJSON},
			{"single element", `$distinct(arr)`, json.RawMessage(`{"arr": [42]}`)},
		} {
			t.Run(tC.name, func(t *testing.T) {
				assertFastPathMatchesFull(t, tC.expr, tC.data)
			})
		}

		t.Run("objects fall through to full evaluator", func(t *testing.T) {
			data := json.RawMessage(`{"arr": [{"a":1}, {"b":2}, {"a":1}]}`)
			e, err := gnata.Compile(`$distinct(arr)`)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			ctx := context.Background()
			fastResult, fastErr := e.EvalBytes(ctx, data)
			parsed, parseErr := gnata.DecodeJSON(data)
			if parseErr != nil {
				t.Fatalf("DecodeJSON: %v", parseErr)
			}
			fullResult, fullErr := e.Eval(ctx, parsed)
			if fastErr != nil || fullErr != nil {
				t.Fatalf("errors: fast=%v full=%v", fastErr, fullErr)
			}
			if !gnata.DeepEqual(gnata.NormalizeValue(fastResult), gnata.NormalizeValue(fullResult)) {
				t.Fatalf("result mismatch:\n  fast: %v\n  full: %v", fastResult, fullResult)
			}
		})
	})

	t.Run("$keys", func(t *testing.T) {
		for _, tC := range []struct {
			name string
			expr string
			data json.RawMessage
		}{
			{"non-empty object", `$keys(profile)`, funcTestJSON},
			{"empty object", `$keys(emptyobj)`, funcTestJSON},
			{"nested object", `$keys(obj)`, json.RawMessage(`{"obj": {"a": 1, "b": {"c": 2}, "d": [3]}}`)},
			{"many keys", `$keys(obj)`, json.RawMessage(`{"obj": {"w":1,"x":2,"y":3,"z":4}}`)},
		} {
			t.Run(tC.name, func(t *testing.T) {
				assertFastPathMatchesFull(t, tC.expr, tC.data)
			})
		}
	})
}

func TestFuncFastPathAggregates(t *testing.T) {
	for _, tC := range []struct {
		name string
		expr string
	}{
		{"$sum non-empty", `$sum(scores)`},
		{"$sum empty array", `$sum(empty)`},
		{"$max non-empty", `$max(scores)`},
		{"$max empty array", `$max(empty)`},
		{"$min non-empty", `$min(scores)`},
		{"$min empty array", `$min(empty)`},
		{"$average non-empty", `$average(scores)`},
		{"$average empty array", `$average(empty)`},
	} {
		t.Run(tC.name, func(t *testing.T) {
			assertFastPathMatchesFull(t, tC.expr, funcTestJSON)
		})
	}
}

func TestFuncFastPathFallthrough(t *testing.T) {
	data := json.RawMessage(`{
		"items": [
			{"name": "a", "subs": [1, 2]},
			{"name": "b", "subs": [3, 4]}
		],
		"nested": {"deep": {"value": 42}}
	}`)

	t.Run("$count on nested array path falls through", func(t *testing.T) {
		e, err := gnata.Compile(`$count(items.subs)`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		ctx := context.Background()
		fastResult, fastErr := e.EvalBytes(ctx, data)
		parsed, parseErr := gnata.DecodeJSON(data)
		if parseErr != nil {
			t.Fatalf("DecodeJSON: %v", parseErr)
		}
		fullResult, fullErr := e.Eval(ctx, parsed)
		if fastErr != nil || fullErr != nil {
			t.Fatalf("errors: fast=%v full=%v", fastErr, fullErr)
		}
		if !gnata.DeepEqual(gnata.NormalizeValue(fastResult), gnata.NormalizeValue(fullResult)) {
			t.Fatalf("mismatch:\n  fast: %v (%T)\n  full: %v (%T)", fastResult, fastResult, fullResult, fullResult)
		}
	})

	t.Run("$sum on mixed array falls through", func(t *testing.T) {
		mixed := json.RawMessage(`{"arr": [1, "two", 3]}`)
		assertFastPathMatchesFull(t, `$sum(arr)`, mixed)
	})

	t.Run("$max on mixed array falls through", func(t *testing.T) {
		mixed := json.RawMessage(`{"arr": [1, "two", 3]}`)
		assertFastPathMatchesFull(t, `$max(arr)`, mixed)
	})

	t.Run("$min on mixed array falls through", func(t *testing.T) {
		mixed := json.RawMessage(`{"arr": [1, "two", 3]}`)
		assertFastPathMatchesFull(t, `$min(arr)`, mixed)
	})

	t.Run("$average on mixed array falls through", func(t *testing.T) {
		mixed := json.RawMessage(`{"arr": [1, "two", 3]}`)
		assertFastPathMatchesFull(t, `$average(arr)`, mixed)
	})

	t.Run("$length on non-string falls through", func(t *testing.T) {
		e, err := gnata.Compile(`$length(nested)`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		ctx := context.Background()
		fastResult, fastErr := e.EvalBytes(ctx, data)
		parsed, parseErr := gnata.DecodeJSON(data)
		if parseErr != nil {
			t.Fatalf("DecodeJSON: %v", parseErr)
		}
		fullResult, fullErr := e.Eval(ctx, parsed)
		if fastErr != nil && fullErr != nil {
			return // both error -- consistent
		}
		if (fastErr != nil) != (fullErr != nil) {
			t.Fatalf("error mismatch: fast=%v full=%v", fastErr, fullErr)
		}
		if !gnata.DeepEqual(gnata.NormalizeValue(fastResult), gnata.NormalizeValue(fullResult)) {
			t.Fatalf("mismatch:\n  fast: %v\n  full: %v", fastResult, fullResult)
		}
	})

	t.Run("$abs on non-number falls through", func(t *testing.T) {
		str := json.RawMessage(`{"val": "hello"}`)
		assertFastPathMatchesFull(t, `$abs(val)`, str)
	})

	t.Run("$floor on non-number falls through", func(t *testing.T) {
		str := json.RawMessage(`{"val": "hello"}`)
		assertFastPathMatchesFull(t, `$floor(val)`, str)
	})

	t.Run("$ceil on non-number falls through", func(t *testing.T) {
		str := json.RawMessage(`{"val": "hello"}`)
		assertFastPathMatchesFull(t, `$ceil(val)`, str)
	})

	t.Run("$sqrt on non-number falls through", func(t *testing.T) {
		str := json.RawMessage(`{"val": "hello"}`)
		assertFastPathMatchesFull(t, `$sqrt(val)`, str)
	})
}
