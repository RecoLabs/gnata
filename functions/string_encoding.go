package functions

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
)

// ── $eval ─────────────────────────────────────────────────────────────────────

// evalPassthroughCodes are guardrail errors (stack depth, sequence length,
// $eval nesting) that $eval reports as-is rather than wrapping in D3121, so a
// caller can still tell which limit was hit.
var evalPassthroughCodes = []string{"D1011", "D2015", "D3121", "U1001"}

func makeFnEval() evaluator.EnvAwareBuiltin {
	const maxEvalDepth = 5
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		if len(args) == 0 {
			return nil, &evaluator.JSONataError{Code: "D3006", Message: "$eval: requires at least 1 argument"}
		}
		if args[0] == nil {
			return nil, nil
		}
		expr, ok := args[0].(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$eval: argument must be a string"}
		}
		if err := env.IncrEvalDepth(maxEvalDepth); err != nil {
			return nil, err
		}
		defer env.DecrEvalDepth()
		ast, parseErr := parser.ParseAndProcess(expr)
		if parseErr != nil {
			return nil, &evaluator.JSONataError{Code: "D3120", Message: fmt.Sprintf("$eval: invalid expression: %v", parseErr)}
		}
		ctx := focus
		childEnv := evaluator.NewChildEnvironment(env)
		if len(args) >= 2 && args[1] != nil {
			ctx = args[1]
			childEnv.SetRootInput(ctx)
		}
		result, evalErr := evaluator.Eval(ast, ctx, childEnv)
		if evalErr != nil {
			if je := new(evaluator.JSONataError); errors.As(evalErr, &je) && !slices.Contains(evalPassthroughCodes, je.Code) {
				return nil, &evaluator.JSONataError{Code: "D3121", Message: fmt.Sprintf("$eval: %v", evalErr)}
			}
			return nil, evalErr
		}
		return result, nil
	}
}

// ── $base64encode / $base64decode ─────────────────────────────────────────────

func fnBase64Encode(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$base64encode: argument must be a string"}
	}
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}

func fnBase64Decode(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$base64decode: argument must be a string"}
	}
	// Padding is optional, as in the forgiving-base64 decode behind JavaScript's atob.
	s = strings.TrimRight(s, "=")
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("$base64decode: invalid base64 string: %v", err)}
		}
	}
	return string(b), nil
}

// ── $encodeUrl / $encodeUrlComponent / $decodeUrl / $decodeUrlComponent ───────

const encodeURISafe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'();/?:@&=+$,#"

const encodeURIComponentSafe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"

func fnEncodeURL(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$encodeUrl: argument must be a string"}
	}
	if hasLoneSurrogate(s) {
		return nil, &evaluator.JSONataError{Code: "D3140", Message: "$encodeUrl: string contains illegal character"}
	}
	return encodeWithSafeChars(s, encodeURISafe), nil
}

func fnEncodeURLComponent(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$encodeUrlComponent: argument must be a string"}
	}
	if hasLoneSurrogate(s) {
		return nil, &evaluator.JSONataError{Code: "D3140", Message: "$encodeUrlComponent: string contains illegal character"}
	}
	return encodeWithSafeChars(s, encodeURIComponentSafe), nil
}

func hasLoneSurrogate(s string) bool {
	for _, r := range s {
		if r >= 0xD800 && r <= 0xDFFF {
			return true
		}
		if r == 0xFFFD {
			return true
		}
	}
	return false
}

func encodeWithSafeChars(s, safe string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(safe, r) {
			b.WriteRune(r)
		} else {
			encoded := url.QueryEscape(string(r))
			encoded = strings.ReplaceAll(encoded, "+", "%20")
			b.WriteString(encoded)
		}
	}
	return b.String()
}

func fnDecodeURL(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$decodeUrl: argument must be a string"}
	}
	decoded, err := url.PathUnescape(s)
	if err != nil {
		return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("$decodeUrl: %v", err)}
	}
	return decoded, nil
}

func fnDecodeURLComponent(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$decodeUrlComponent: argument must be a string"}
	}
	decoded, err := url.PathUnescape(s)
	if err != nil {
		return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("$decodeUrlComponent: %v", err)}
	}
	return decoded, nil
}
