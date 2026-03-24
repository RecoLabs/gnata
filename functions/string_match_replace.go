package functions

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/recolabs/gnata/internal/evaluator"
)

// ── $match ────────────────────────────────────────────────────────────────────

func makeFnMatch(evalFn EvalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		if len(args) < 2 {
			return nil, &evaluator.JSONataError{Code: "D3006", Message: "$match: requires at least 2 arguments"}
		}
		if args[0] == nil {
			return nil, nil
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$match: argument 1 must be a string"}
		}

		limit := -1
		if len(args) >= 3 && args[2] != nil {
			lf, ok := args[2].(float64)
			if !ok {
				return nil, &evaluator.JSONataError{Code: "T0410", Message: "$match: argument 3 must be a number"}
			}
			limit = int(lf)
		}

		switch args[1].(type) {
		case evaluator.BuiltinFunction, evaluator.EnvAwareBuiltin, *evaluator.Lambda, *evaluator.SignedBuiltin:
			return matchWithCustomMatcher(s, args[1], limit, evalFn, env)
		}

		re, err := compileRegexArg(args[1])
		if err != nil {
			return nil, err
		}

		result := make([]any, 0)
		m, matchErr := re.FindStringMatch(s)
		if matchErr != nil {
			return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("regex error: %v", matchErr)}
		}
		for m != nil {
			if limit >= 0 && len(result) >= limit {
				break
			}
			groups := make([]any, 0)
			for g := 1; g < m.GroupCount(); g++ {
				grp := m.GroupByNumber(g)
				if !grp.Captured {
					groups = append(groups, "")
					continue
				}
				groups = append(groups, grp.String())
			}
			obj := map[string]any{
				"match":  m.String(),
				"start":  float64(utf8.RuneCountInString(s[:m.Index])),
				"end":    float64(utf8.RuneCountInString(s[:m.Index+m.Length])),
				"groups": groups,
			}
			result = append(result, obj)
			m, matchErr = m.FindNextMatch()
			if matchErr != nil {
				return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("regex error: %v", matchErr)}
			}
		}
		return matchResultSeq(result), nil
	}
}

func matchResultSeq(result []any) any {
	if len(result) == 0 {
		return nil
	}
	return &evaluator.Sequence{Values: result}
}

func matchWithCustomMatcher(s string, matcherFn any, limit int, evalFn EvalFn, env *evaluator.Environment) (any, error) {
	var result []any

	res, err := evalFn(matcherFn, []any{s, float64(0)}, nil, env)
	if err != nil {
		return nil, err
	}

	for res != nil {
		if !evaluator.IsMap(res) {
			break
		}
		m := res

		matchVal, _ := evaluator.MapGet(m, "match")
		startVal, _ := evaluator.MapGet(m, "start")
		groupsVal, _ := evaluator.MapGet(m, "groups")

		obj := map[string]any{
			"match":  matchVal,
			"index":  startVal,
			"groups": groupsVal,
		}
		result = append(result, obj)

		if limit >= 0 && len(result) >= limit {
			break
		}

		nextFn, _ := evaluator.MapGet(m, "next")
		if nextFn == nil {
			break
		}
		res, err = evalFn(nextFn, nil, nil, env)
		if err != nil {
			return nil, err
		}
	}

	return matchResultSeq(result), nil
}

// ── $replace ──────────────────────────────────────────────────────────────────

func makeFnReplace(evalFn EvalFn) evaluator.EnvAwareBuiltin {
	return func(args []any, focus any, env *evaluator.Environment) (any, error) {
		if len(args) < 1 {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$replace: argument 1 must be a string"}
		}
		if args[0] == nil {
			return nil, nil
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$replace: argument 1 must be a string"}
		}
		if len(args) < 2 || args[1] == nil {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$replace: argument 2 must be a string or regex pattern"}
		}
		if len(args) < 3 {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$replace: argument 3 (replacement) is required"}
		}

		limit := -1
		if len(args) >= 4 {
			if args[3] == nil {
				return nil, &evaluator.JSONataError{Code: "T0410", Message: "$replace: argument 4 must be a number"}
			}
			lf, ok := args[3].(float64)
			if !ok {
				return nil, &evaluator.JSONataError{Code: "T0410", Message: "$replace: argument 4 must be a number"}
			}
			if lf < 0 {
				return nil, &evaluator.JSONataError{Code: "D3011", Message: "$replace: fourth argument must not be negative"}
			}
			limit = int(lf)
		}

		switch pattern := args[1].(type) {
		case string:
			if pattern == "" {
				return nil, &evaluator.JSONataError{Code: "D3010", Message: "$replace: pattern cannot be an empty string"}
			}
			switch repl := args[2].(type) {
			case string:
				if limit < 0 {
					return strings.ReplaceAll(s, pattern, repl), nil
				}
				return replaceNLiteral(s, pattern, repl, limit), nil
			default:
				literalRe, compErr := evaluator.CompileLiteralRegex(pattern)
				if compErr != nil {
					return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("regex error: %v", compErr)}
				}
				return replaceWithFn(s, literalRe, args[2], limit, evalFn, focus, env)
			}

		case map[string]any:
			re, err := compileRegex(pattern)
			if err != nil {
				return nil, err
			}
			switch repl := args[2].(type) {
			case string:
				return replaceRegexString(s, re, repl, limit)
			default:
				return replaceWithFn(s, re, args[2], limit, evalFn, focus, env)
			}

		default:
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$replace: argument 2 must be a string or regex"}
		}
	}
}

func replaceNLiteral(s, old, replacement string, limit int) string {
	if limit <= 0 {
		return s
	}
	result := &strings.Builder{}
	for range limit {
		idx := strings.Index(s, old)
		if idx < 0 {
			break
		}
		result.WriteString(s[:idx])
		result.WriteString(replacement)
		s = s[idx+len(old):]
	}
	result.WriteString(s)
	return result.String()
}

// expandJSONataReplacement expands a JSONata replacement template with back-references.
// $0 = full match, $1..$N = capture groups (1-indexed).
// For invalid group numbers (> numGroups), a greedy-left algorithm reduces the number:
// try progressively shorter prefixes; if none valid, output the digits after the first as literal.
// Non-digit after $ is output literally (e.g. $x → $x). Trailing $ is literal.
func expandJSONataReplacement(repl, fullMatch string, groups []string) string {
	var b strings.Builder
	numGroups, i := len(groups), 0
	for i < len(repl) {
		if repl[i] != '$' {
			b.WriteByte(repl[i])
			i++
			continue
		}
		i++ // skip '$'
		if i >= len(repl) {
			b.WriteByte('$')
			break
		}
		if repl[i] == '$' {
			// $$ → literal single $
			b.WriteByte('$')
			i++
			continue
		}
		if repl[i] < '0' || repl[i] > '9' {
			// Non-digit (other than $): output $ and the character literally.
			b.WriteByte('$')
			b.WriteByte(repl[i])
			i++
			continue
		}
		// Collect digit run.
		start := i
		for i < len(repl) && repl[i] >= '0' && repl[i] <= '9' {
			i++
		}
		numStr := repl[start:i]
		N, _ := strconv.Atoi(numStr)
		if N == 0 {
			b.WriteString(fullMatch)
			continue
		}
		if N <= numGroups {
			b.WriteString(groups[N-1])
			continue
		}
		// N > numGroups: try progressively shorter prefixes (greedy from left).
		// Find longest prefix that is a valid group index.
		found := false
		for prefLen := len(numStr) - 1; prefLen >= 1; prefLen-- {
			P := 0
			for _, ch := range numStr[:prefLen] {
				P = P*10 + int(ch-'0')
			}
			if P == 0 {
				b.WriteString(fullMatch)
				b.WriteString(numStr[prefLen:])
				found = true
				break
			}
			if P <= numGroups {
				b.WriteString(groups[P-1])
				b.WriteString(numStr[prefLen:])
				found = true
				break
			}
		}
		if !found {
			// No valid prefix: output digits after the first as literal.
			if len(numStr) > 1 {
				b.WriteString(numStr[1:])
			}
		}
	}
	return b.String()
}

// extractSubmatches returns the full match string and capture group strings from a Match.
func extractSubmatches(m *evaluator.Match) (fullMatch string, groups []string) {
	fullMatch = m.String()
	for g := 1; g < m.GroupCount(); g++ {
		grp := m.GroupByNumber(g)
		if !grp.Captured {
			groups = append(groups, "")
		} else {
			groups = append(groups, grp.String())
		}
	}
	return fullMatch, groups
}

// replaceRegexString replaces regex matches with a JSONata template string.
func replaceRegexString(s string, re *evaluator.Regex, repl string, limit int) (string, error) {
	var b strings.Builder
	prev := 0
	count := 0
	m, err := re.FindStringMatch(s)
	if err != nil {
		return "", &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("regex error: %v", err)}
	}
	for m != nil {
		if limit >= 0 && count >= limit {
			break
		}
		if m.Length == 0 {
			return "", &evaluator.JSONataError{Code: "D1004", Message: "$replace: the regex matched a zero-length string"}
		}
		b.WriteString(s[prev:m.Index])
		fullMatch, groups := extractSubmatches(m)
		b.WriteString(expandJSONataReplacement(repl, fullMatch, groups))
		prev = m.Index + m.Length
		count++
		m, err = m.FindNextMatch()
		if err != nil {
			return "", &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("regex error: %v", err)}
		}
	}
	b.WriteString(s[prev:])
	return b.String(), nil
}

func replaceWithFn(s string, re *evaluator.Regex, fn any, limit int, evalFn EvalFn, focus any, env *evaluator.Environment) (any, error) {
	var b strings.Builder
	prev := 0
	count := 0
	m, err := re.FindStringMatch(s)
	if err != nil {
		return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("regex error: %v", err)}
	}
	for m != nil {
		if limit >= 0 && count >= limit {
			break
		}
		if m.Length == 0 {
			return nil, &evaluator.JSONataError{Code: "D1004", Message: "$replace: the regex matched a zero-length string"}
		}
		matchStart := m.Index
		matchEnd := m.Index + m.Length
		b.WriteString(s[prev:matchStart])
		fullMatch, groups := extractSubmatches(m)
		groupsAny := make([]any, len(groups))
		for i, g := range groups {
			groupsAny[i] = g
		}
		matchObj := map[string]any{
			"match":  fullMatch,
			"start":  float64(utf8.RuneCountInString(s[:matchStart])),
			"end":    float64(utf8.RuneCountInString(s[:matchEnd])),
			"groups": groupsAny,
		}
		val, evalErr := evalFn(fn, []any{matchObj}, focus, env)
		if evalErr != nil {
			return nil, evalErr
		}
		sv, ok := val.(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "D3012", Message: "$replace: replacement function must return a string"}
		}
		b.WriteString(sv)
		prev = matchEnd
		count++
		m, err = m.FindNextMatch()
		if err != nil {
			return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("regex error: %v", err)}
		}
	}
	b.WriteString(s[prev:])
	return b.String(), nil
}

// ── regex helpers ─────────────────────────────────────────────────────────────

func compileRegex(m map[string]any) (*evaluator.Regex, error) {
	pattern, _ := m["pattern"].(string)
	flags, _ := m["flags"].(string)
	re, err := evaluator.CachedCompileRegex(pattern, flags)
	if err != nil {
		return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("invalid regex: %v", err)}
	}
	return re, nil
}

func compileRegexArg(v any) (*evaluator.Regex, error) {
	switch p := v.(type) {
	case string:
		return evaluator.CompileLiteralRegex(p)
	case map[string]any:
		return compileRegex(p)
	default:
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "expected a string or regex pattern"}
	}
}

func splitRegex(re *evaluator.Regex, s string, limit int) ([]string, error) {
	var parts []string
	lastEnd := 0
	m, err := re.FindStringMatch(s)
	if err != nil {
		return nil, err
	}
	for m != nil {
		if limit >= 0 && len(parts) >= limit {
			break
		}
		parts = append(parts, s[lastEnd:m.Index])
		lastEnd = m.Index + m.Length
		m, err = m.FindNextMatch()
		if err != nil {
			return nil, err
		}
	}
	if limit < 0 || len(parts) < limit {
		parts = append(parts, s[lastEnd:])
	}
	return parts, nil
}
