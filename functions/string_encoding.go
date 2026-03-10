package functions

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/recolabs/gnata/internal/evaluator"
	"github.com/recolabs/gnata/internal/parser"
)

// ── $eval ─────────────────────────────────────────────────────────────────────

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
		p := parser.NewParser(expr)
		ast, parseErr := p.Parse()
		if parseErr != nil {
			return nil, &evaluator.JSONataError{Code: "D3120", Message: fmt.Sprintf("$eval: invalid expression: %v", parseErr)}
		}
		ast, processErr := parser.ProcessAST(ast)
		if processErr != nil {
			return nil, &evaluator.JSONataError{Code: "D3120", Message: fmt.Sprintf("$eval: invalid expression: %v", processErr)}
		}
		ctx := focus
		if len(args) >= 2 && args[1] != nil {
			ctx = args[1]
		}
		childEnv := evaluator.NewChildEnvironment(env)
		result, evalErr := evaluator.Eval(ast, ctx, childEnv)
		if evalErr != nil {
			je := &evaluator.JSONataError{}
			if errors.As(evalErr, &je) {
				if je.Code == "T1006" || je.Code == "T1005" {
					return nil, &evaluator.JSONataError{Code: "D3121", Message: fmt.Sprintf("$eval: %v", evalErr)}
				}
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
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.URLEncoding.DecodeString(s)
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
