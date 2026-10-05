package evaluator

import (
	"errors"
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
