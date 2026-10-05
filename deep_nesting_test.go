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
