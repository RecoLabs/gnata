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

func compileSig(sig string) (*Signature, error) {
	specs, err := parser.ParseSig(sig)
	if err != nil {
		return nil, err
	}
	return compileSignature(specs), nil
}

func TestCompileSignature(t *testing.T) {
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
			got, err := compileSig(tC.sig)
			if err != nil {
				if tC.expectError {
					return
				}
				t.Fatalf("expected no error but got: %v", err)
			}
			if tC.expectError {
				t.Fatal("expected error but got none")
			}
			if got.hasContext != tC.wantContext {
				t.Fatalf("compileSig(%q) has context %v, want %v", tC.sig, got.hasContext, tC.wantContext)
			}
		})
	}
}

func TestSignatureInject(t *testing.T) {
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
			sig, err := compileSig(tC.sig)
			if err != nil {
				t.Fatalf("compileSig(%q): %v", tC.sig, err)
			}
			got, err := sig.Inject(tC.args, tC.focus)
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

// matchFixed must choose what solve chooses for a signature without '+';
// mismatchError blames with solve.
func TestMatchFixedAgreesWithSolve(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	const modifiers = "?-"
	for range 20000 {
		var sig strings.Builder
		for range 1 + rng.IntN(5) {
			sig.WriteByte("snbalofx"[rng.IntN(8)])
			for range rng.IntN(3) {
				sig.WriteByte(modifiers[rng.IntN(len(modifiers))])
			}
		}
		compiled, err := compileSig(sig.String())
		if err != nil {
			continue
		}
		symbols := make([]uint8, rng.IntN(6))
		for i := range symbols {
			symbols[i] = symbolBit(sigSymbols[rng.IntN(len(sigSymbols))])
		}
		fixed, fixedOK := compiled.matchFixed(symbols, nil)
		solved, _, solvedOK := compiled.solve(symbols, len(compiled.params), true, nil)
		if fixedOK != solvedOK || fixedOK && !slices.Equal(fixed, solved) {
			t.Fatalf("<%s> on %v: matchFixed %v %v, solve %v %v", sig.String(), symbols, fixed, fixedOK, solved, solvedOK)
		}
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
// each followed by up to two of modifiers.
func randomSig(rng *rand.Rand, types, modifiers string) string {
	var sig strings.Builder
	for range 1 + rng.IntN(4) {
		if rng.IntN(8) == 0 {
			sig.WriteString("(sn)")
		} else {
			sig.WriteByte(types[rng.IntN(len(types))])
		}
		for range rng.IntN(3) {
			sig.WriteByte(modifiers[rng.IntN(len(modifiers))])
		}
	}
	return sig.String()
}

// randomArgs returns up to maxArgs random values from randomCallValues.
func randomArgs(rng *rand.Rand, maxArgs int) []any {
	args := make([]any, rng.IntN(maxArgs+1))
	for i := range args {
		args[i] = randomCallValues[rng.IntN(len(randomCallValues))]
	}
	return args
}

// sameValue is DeepEqual, with functions, which DeepEqual never finds
// equal, compared by identity.
func sameValue(a, b any) bool {
	if aa, isArray := a.([]any); isArray {
		ba, isArray := b.([]any)
		return isArray && slices.EqualFunc(aa, ba, sameValue)
	}
	if fa, isFunc := a.(BuiltinFunction); isFunc {
		fb, isFunc := b.(BuiltinFunction)
		return isFunc && reflect.ValueOf(fa).Pointer() == reflect.ValueOf(fb).Pointer()
	}
	return DeepEqual(a, b)
}

// Inject's and Validate's shortcuts must give what matching gives.
func TestSignatureShortcutsAgreeWithMatching(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	compared := 0
	for range 50_000 {
		sig := randomSig(rng, "snbaxjfol", "?-+")
		compiled, err := compileSig(sig)
		if err != nil {
			continue
		}
		compared++
		args := randomArgs(rng, 4)
		focus := randomCallValues[rng.IntN(len(randomCallValues))]
		check := func(name string, got, want []any, gotErr, wantErr error) {
			t.Helper()
			if (gotErr == nil) != (wantErr == nil) || gotErr != nil && gotErr.Error() != wantErr.Error() ||
				!slices.EqualFunc(got, want, sameValue) {
				t.Fatalf("<%s> %s on %v with focus %v: %v %v, matching gives %v %v", sig, name, args, focus, got, gotErr, want, wantErr)
			}
		}
		got, gotErr := compiled.Inject(args, focus)
		want, wantErr := compiled.injectMatched(args, focus)
		check("Inject", got, want, gotErr, wantErr)
		got, gotErr = compiled.Validate(args, focus)
		want, wantErr = compiled.validateMatched(args, focus)
		check("Validate", got, want, gotErr, wantErr)
	}
	if compared < 10_000 {
		t.Fatalf("compared only %d signatures", compared)
	}
}
