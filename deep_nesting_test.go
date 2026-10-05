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
	var v any = 1.0
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
		{desc: "encode built objects", expr: "$length($string(" + deepBuilt + "))", want: float64(len(objectJSON))},
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
