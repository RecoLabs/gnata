package evaluator

import (
	"errors"
	"slices"
	"testing"

	"github.com/recolabs/gnata/internal/parser"
)

func compileContextSig(sig string) (*ContextSig, error) {
	specs, err := parser.ParseSig(sig)
	if err != nil {
		return nil, err
	}
	return newContextSig(specs)
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
		{desc: "too many parameters", sig: "s-nnnnnnnn", expectError: true},
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
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			sig, err := compileContextSig(tC.sig)
			if err != nil {
				t.Fatalf("compileContextSig(%q): %v", tC.sig, err)
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
