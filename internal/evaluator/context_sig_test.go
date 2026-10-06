package evaluator

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/recolabs/gnata/internal/parser"
)

func compileContextSig(sig string) (*ContextSig, error) {
	specs, err := parser.ParseSig(sig)
	if err != nil {
		return nil, err
	}
	return newContextSig(specs), nil
}

func TestNewContextSig(t *testing.T) {
	testCases := []struct {
		desc        string
		sig         string
		wantContext bool
		expectError bool
	}{
		{desc: "context parameter", sig: "s-nn?:s", wantContext: true},
		{desc: "context choice", sig: "(nsb)-:n", wantContext: true},
		{desc: "context before a subtyped function", sig: "s-f<s:o>n?:a<o>", wantContext: true},
		{desc: "no context parameter", sig: "a<n>:n"},
		{desc: "variadic", sig: "a+"},
		{desc: "gnata union type", sig: "u-:s", wantContext: true},
		{desc: "malformed signature", sig: "(sn-", expectError: true},
		{desc: "more parameters than the stack buffers", sig: "s-nnnnnnnnn", wantContext: true},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			got, err := compileContextSig(tC.sig)
			if err != nil {
				if tC.expectError {
					return
				}
				t.Fatalf("expected no error but got: %v", err)
			}
			if tC.expectError {
				t.Fatal("expected error but got none")
			}
			if (got != nil) != tC.wantContext {
				t.Fatalf("compileContextSig(%q) = %v, want context %v", tC.sig, got, tC.wantContext)
			}
		})
	}
}

func TestContextSigInject(t *testing.T) {
	regex := map[string]any{"pattern": "a", "flags": ""}
	patternObject := map[string]any{"pattern": "a", "b": 1.0}
	testCases := []struct {
		desc  string
		sig   string
		args  []any
		focus any
		want  []any
		code  string
	}{
		{desc: "missing context", sig: "s-nn?", args: []any{1.0}, focus: "ab", want: []any{"ab", 1.0}},
		{desc: "supplied context", sig: "s-nn?", args: []any{"cd", 1.0}, focus: "ab", want: []any{"cd", 1.0}},
		{desc: "backtracks into the context", sig: "x-s", args: []any{"k"}, focus: 1.0, want: []any{1.0, "k"}},
		{desc: "undefined argument", sig: "s-n", args: []any{nil}, focus: "ab", want: []any{"ab", nil}},
		{desc: "regex is a function", sig: "s-(sf)", args: []any{regex}, focus: "ab", want: []any{"ab", regex}},
		{desc: "object with a pattern field", sig: "o-n", args: []any{1.0}, focus: patternObject, want: []any{patternObject, 1.0}},
		{desc: "regex-shaped focus is an object", sig: "o-n", args: []any{1.0}, focus: regex, want: []any{regex, 1.0}},
		{desc: "variadic", sig: "s-n+", args: []any{1.0, 2.0}, focus: "ab", want: []any{"ab", 1.0, 2.0}},
		{desc: "mismatch is left to the builtin", sig: "s-n", args: []any{"a", "b"}, focus: "ab", want: []any{"a", "b"}},
		{desc: "focus of the wrong type", sig: "s-", focus: 1.0, code: "T0411"},
		{desc: "every missing context parameter", sig: "s-s-", focus: "q", want: []any{"q", "q"}},
		{desc: "a variadic context parameter is lazy, not optional", sig: "s+-", focus: "q", want: []any{}},
		{
			desc: "more arguments than the stack buffers", sig: "s-nnnnnnnnn",
			args: []any{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0}, focus: "q",
			want: []any{"q", 1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0},
		},
		{
			desc: "a wide signature missing one argument", sig: "s-" + strings.Repeat("n", 5000),
			args: slices.Repeat([]any{1.0}, 5000), focus: "q", want: append([]any{"q"}, slices.Repeat([]any{1.0}, 5000)...),
		},
		{
			desc: "many '+' parameters do not backtrack", sig: "s+s+s+s+s+s+s-n",
			args: slices.Repeat([]any{"a"}, 200), focus: "q", want: slices.Repeat([]any{"a"}, 200),
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			sig, err := compileContextSig(tC.sig)
			if err != nil {
				t.Fatalf("compileContextSig(%q): %v", tC.sig, err)
			}
			got, _, err := sig.Inject(tC.args, tC.focus)
			if tC.code != "" {
				var je *JSONataError
				if !errors.As(err, &je) || je.Code != tC.code {
					t.Fatalf("want error %s, got %v (%v)", tC.code, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.EqualFunc(got, tC.want, DeepEqual) {
				t.Fatalf("got %v, want %v", got, tC.want)
			}
		})
	}
}

// randomCallValues are argument and focus values for the randomized tests:
// one of each signature symbol, plus a regex and other value representations.
var randomCallValues = []any{
	"s", 1.0, json.Number("2"), true, nil, Null,
	[]any{1.0},
	map[string]any{"a": 1.0},
	NewOrderedMap(),
	map[string]any{"pattern": "a", "flags": ""},
	BuiltinFunction(func([]any, any) (any, error) { return nil, nil }),
}

// randomSig returns a random signature of up to 4 parameters from types,
// each followed by one of modifiers with probability 1/modifierOdds.
func randomSig(rng *rand.Rand, types, modifiers string, modifierOdds int) string {
	var sig strings.Builder
	for range 1 + rng.IntN(4) {
		if rng.IntN(8) == 0 {
			sig.WriteString("(sn)")
		} else {
			sig.WriteByte(types[rng.IntN(len(types))])
		}
		if rng.IntN(modifierOdds) == 0 {
			sig.WriteByte(modifiers[rng.IntN(len(modifiers))])
		}
	}
	return sig.String()
}

// sameValue is DeepEqual, with functions, which DeepEqual never finds
// equal, compared by identity.
func sameValue(a, b any) bool {
	if fa, isFunc := a.(BuiltinFunction); isFunc {
		fb, isFunc := b.(BuiltinFunction)
		return isFunc && reflect.ValueOf(fa).Pointer() == reflect.ValueOf(fb).Pointer()
	}
	return DeepEqual(a, b)
}

// randomArgs returns up to maxArgs random values from randomCallValues.
func randomArgs(rng *rand.Rand, maxArgs int) []any {
	args := make([]any, rng.IntN(maxArgs+1))
	for i := range args {
		args[i] = randomCallValues[rng.IntN(len(randomCallValues))]
	}
	return args
}

// Inject's shortcuts must give what matching gives.
func TestContextSigShortcutsAgreeWithMatching(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	compared := 0
	for range 20000 {
		sig := randomSig(rng, "snbaxjfol", "?-+", 2)
		compiled, err := compileContextSig(sig)
		if err != nil || compiled == nil {
			continue
		}
		args := randomArgs(rng, 3)
		focus := randomCallValues[rng.IntN(len(randomCallValues))]
		compared++
		got, gotContext, gotErr := compiled.Inject(args, focus)
		want, wantContext, wantErr := compiled.injectMatched(args, focus)
		if (gotErr == nil) != (wantErr == nil) || !slices.EqualFunc(got, want, sameValue) {
			t.Fatalf("<%s> on %v with focus %v: Inject %v %v, matching %v %v", sig, args, focus, got, gotErr, want, wantErr)
		}
		for i := range got {
			if gotContext.has(i) != wantContext.has(i) {
				t.Fatalf("<%s> on %v: argument %d context %v, want %v", sig, args, i, gotContext.has(i), wantContext.has(i))
			}
		}
	}
	if compared < 5000 {
		t.Fatalf("compared only %d signatures with a context parameter", compared)
	}
}

// plainlyValid may skip processCallArgs only where it would change nothing.
func TestPlainlyValidAgreesWithProcessCallArgs(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	skipped := 0
	for range 100_000 {
		sig := randomSig(rng, "snbalojxf", "?-+", 3)
		specs, err := parser.ParseSig(sig)
		if err != nil {
			continue
		}
		args := randomArgs(rng, 4)
		if !plainlyValid(specs, args) {
			continue
		}
		skipped++
		got, undefined, err := processCallArgs(specs, args, contextArgs{})
		if err != nil || undefined || !slices.EqualFunc(got, args, sameValue) {
			t.Fatalf("<%s> on %v: processCallArgs gives %v %v %v", sig, args, got, undefined, err)
		}
	}
	if skipped < 1000 {
		t.Fatalf("only %d calls were plainly valid", skipped)
	}
}
