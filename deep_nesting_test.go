package gnata_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/recolabs/gnata"
)

// deepNesting levels overflow the stack limitStack sets in a walk that
// recurses once per level, which Go cannot recover from.
const deepNesting = 200_000

// deepBuilt is an expression building deepNesting nested objects.
var deepBuilt = `$reduce([1..` + strconv.Itoa(deepNesting) + `], function($a, $x){{"a": $a}}, 1)`

// A built object returned as a result marshals with gnata's own encoder.
func TestDeepNestingMarshal(t *testing.T) {
	limitStack(t)
	e, err := gnata.Compile(deepBuilt)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := e.Eval(context.Background(), nil)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	m, ok := got.(json.Marshaler)
	if !ok {
		t.Fatalf("result is %T, want a json.Marshaler", got)
	}
	out, err := m.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := strings.Repeat(`{"a":`, deepNesting) + "1" + strings.Repeat("}", deepNesting); string(out) != want {
		t.Fatal("marshaled JSON differs from the built object")
	}
}

func deepArray() any {
	return deepArrayOf(1.0)
}

// deepArrayOf nests leaf in deepNesting arrays.
func deepArrayOf(leaf any) any {
	v := leaf
	for range deepNesting {
		v = []any{v}
	}
	return v
}

func deepObject() any {
	var v any = 1.0
	for range deepNesting {
		v = map[string]any{"a": v}
	}
	return v
}

func TestDeepNesting(t *testing.T) {
	limitStack(t)
	arrayJSON := strings.Repeat("[", deepNesting) + "1" + strings.Repeat("]", deepNesting)
	objectJSON := strings.Repeat(`{"a":`, deepNesting) + "1" + strings.Repeat("}", deepNesting)
	testCases := []struct {
		desc string
		expr string
		data any
		want any
	}{
		{desc: "encode an array", expr: "$length($string($))", data: deepArray(), want: float64(len(arrayJSON))},
		{desc: "encode an object", expr: "$length($string($))", data: deepObject(), want: float64(len(objectJSON))},
		{desc: "compare arrays", expr: "$[0] = $[1]", data: []any{deepArray(), deepArray()}, want: true},
		{desc: "compare objects", expr: "$[0] = $[1]", data: []any{deepObject(), deepObject()}, want: true},
		{desc: "descendants of an array", expr: "$count([**])", data: deepArray(), want: float64(deepNesting)},
		{desc: "descendants of an object", expr: "$count([**])", data: deepObject(), want: float64(deepNesting)},
		{desc: "encode built objects", expr: "$length($string(" + deepBuilt + "))", want: float64(len(objectJSON))},
		{desc: "compare built objects", expr: "($f := function(){" + deepBuilt + "}; $f() = $f())", want: true},
		{desc: "field over nested arrays", expr: "a", data: deepArrayOf(map[string]any{"a": 1.0}), want: 1.0},
		{
			desc: "transform an object", expr: `$length($string($ ~> |$|{"b": 1}|))`, data: deepObject(),
			want: float64(len(objectJSON) + len(`,"b":1`)),
		},
		{desc: "boolean of an array", expr: "$boolean($)", data: deepArrayOf(0.0), want: false},
		{desc: "flatten an array", expr: "$count($flatten($))", data: deepArray(), want: 1.0},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			e, err := gnata.Compile(tC.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, err := e.Eval(context.Background(), tC.data)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if got != tC.want {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}

func TestDeepNestingDeepEqual(t *testing.T) {
	limitStack(t)
	if !gnata.DeepEqual(deepArray(), deepArray()) {
		t.Fatal("deep arrays differ")
	}
	if !gnata.DeepEqual(deepObject(), deepObject()) {
		t.Fatal("deep objects differ")
	}
}

// Past the fast decoder's depth limit, decoding falls back to encoding/json's
// token decoder, which errors on input this deep from Go 1.27 and accepts it
// before. Either way it must not overflow the stack.
func TestDeepNestingDecode(t *testing.T) {
	if skipDeepDecode {
		t.Skip("the fast decoder recurses up to its depth limit, which can exceed TinyGo's fixed stack")
	}
	limitStack(t)
	e, err := gnata.Compile("$length($string($))")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	testCases := []struct {
		desc string
		data string
	}{
		{desc: "array", data: strings.Repeat("[", deepNesting) + "1" + strings.Repeat("]", deepNesting)},
		{desc: "object", data: strings.Repeat(`{"a":`, deepNesting) + "1" + strings.Repeat("}", deepNesting)},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			got, err := e.EvalBytes(context.Background(), json.RawMessage(tC.data))
			if err != nil {
				if !strings.Contains(err.Error(), "exceeded max depth") {
					t.Fatalf("eval bytes: %v", err)
				}
				return
			}
			if got != float64(len(tC.data)) {
				t.Fatalf("got %v, want %d", got, len(tC.data))
			}
		})
	}
}
