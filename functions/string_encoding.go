package functions

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

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

const encodeURISafe = encodeURIComponentSafe + uriReserved

const encodeURIComponentSafe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"

// uriReserved are the characters ECMAScript's encodeURI leaves and decodeURI
// keeps escaped, so a URL's delimiters keep their meaning.
const uriReserved = ";/?:@&=+$,#"

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
	return decodeURIArg("$decodeUrl", args, uriReserved)
}

func fnDecodeURLComponent(args []any, _ any) (any, error) {
	return decodeURIArg("$decodeUrlComponent", args, "")
}

// jsonQuote quotes s as JSON, as jsonata-js quotes the values in its error
// messages.
func jsonQuote(s string) string {
	quoted, _ := evaluator.AppendJSON(nil, s) // only numbers fail to encode
	return string(quoted)
}

func decodeURIArg(name string, args []any, reserved string) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: name + ": argument must be a string"}
	}
	decoded, ok := decodeURI(s, reserved)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "D3140", Message: fmt.Sprintf("Malformed URL passed to %s(): %s", name, jsonQuote(s))}
	}
	return decoded, nil
}

// decodeURI ports ECMAScript's Decode, used by decodeURI and
// decodeURIComponent: it decodes %XX escapes as UTF-8, keeps an escape of a
// character in reserved as it is, and fails on a malformed escape or when a
// run of decoded escapes is not valid UTF-8. Text outside escapes is copied
// as it is.
//
// https://tc39.es/ecma262/#sec-decode
func decodeURI(s, reserved string) (string, bool) {
	if strings.IndexByte(s, '%') < 0 {
		return s, true
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		run := b.Len()
		for i < len(s) && s[i] == '%' {
			if i+3 > len(s) {
				return "", false
			}
			c, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return "", false
			}
			if c < utf8.RuneSelf && strings.IndexByte(reserved, byte(c)) >= 0 {
				break
			}
			b.WriteByte(byte(c))
			i += 3
		}
		if !utf8.ValidString(b.String()[run:]) {
			return "", false
		}
		if i < len(s) && s[i] == '%' {
			// A reserved character's escape, kept as written.
			b.WriteString(s[i : i+3])
			i += 3
		}
	}
	return b.String(), true
}
