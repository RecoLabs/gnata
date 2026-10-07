package evaluator_test

import (
	"testing"

	"github.com/recolabs/gnata/internal/evaluator"
)

func TestRegexMapsLookup(t *testing.T) {
	re := &evaluator.RegexLiteral{Pattern: "a+", Flags: "i"}
	var passed evaluator.RegexMaps
	recorded, err := evaluator.NormalizeTree(re, false, &passed, nil)
	if err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		desc  string
		maps  *evaluator.RegexMaps
		value any
		want  *evaluator.RegexLiteral
	}{
		{desc: "recorded map", maps: &passed, value: recorded, want: re},
		{desc: "equal map not recorded", maps: &passed, value: re.ToMap()},
		{desc: "not a map", maps: &passed, value: "a+"},
		{desc: "no maps recorded", maps: new(evaluator.RegexMaps), value: recorded},
		{desc: "nil receiver", value: recorded},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			if got := tC.maps.Lookup(tC.value); got != tC.want {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}
