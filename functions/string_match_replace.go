package functions

import (
	"fmt"
	"math"
	"strconv"
	"strings"

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

		limitArg, err := optionalLimit(args, 2, "$match", "D3040")
		if err != nil {
			return nil, err
		}
		limit := matchCountLimit(limitArg)

		switch args[1].(type) {
		case evaluator.BuiltinFunction, evaluator.EnvAwareBuiltin, *evaluator.Lambda, *evaluator.SignedBuiltin:
			return matchWithCustomMatcher(s, args[1], limit, evalFn, env)
		}

		re, err := compileRegexArg(args[1])
		if err != nil {
			return nil, err
		}

		var result []any
		err = eachRegexMatch(re, s, limit, func(m *evaluator.Match) error {
			result = append(result, matchResult(evaluator.NewMatchObject(s, m)))
			return nil
		})
		if err != nil {
			return nil, err
		}
		return matchResultSeq(result), nil
	}
}

// optionalLimit reads the optional, non-negative limit argument at args[i],
// -1 when it is absent; negativeCode is the function's error for a negative one.
func optionalLimit(args []any, i int, name, negativeCode string) (float64, error) {
	if len(args) <= i || args[i] == nil {
		return -1, nil
	}
	limit, ok := evaluator.ToFloat64(args[i])
	if !ok {
		return 0, &evaluator.JSONataError{Code: "T0410", Message: fmt.Sprintf("%s: argument %d must be a number", name, i+1)}
	}
	if limit < 0 {
		return 0, &evaluator.JSONataError{Code: negativeCode, Message: fmt.Sprintf("%s: the limit must not be negative", name)}
	}
	return limit, nil
}

// matchCountLimit converts a limit from optionalLimit into a match count,
// -1 for none. jsonata-js keeps matching while count < limit, so a
// fractional limit rounds up.
func matchCountLimit(limit float64) int {
	if limit < 0 {
		return -1
	}
	return evaluator.ToIntClamped(math.Ceil(limit))
}

// eachRegexMatch calls visit on the matches of re in s as jsonata-js's regex
// matcher yields them, at most limit of them (-1 for no limit). Like
// jsonata-js it finds the next match before checking the limit, so an empty
// match after the last one visited still raises D1004.
func eachRegexMatch(re *evaluator.Regex, s string, limit int, visit func(*evaluator.Match) error) error {
	if limit == 0 {
		return nil
	}
	m, err := re.FindStringMatch(s)
	for count := 0; err == nil && m != nil && (limit < 0 || count < limit); count++ {
		if err = visit(m); err == nil {
			m, err = evaluator.NextMatch(m)
		}
	}
	return err
}

// matchResult converts a matcher's {match, start, end, groups} object into the
// {match, index, groups} object $match returns.
func matchResult(m any) *evaluator.OrderedMap {
	matchVal, _ := evaluator.MapGet(m, "match")
	startVal, _ := evaluator.MapGet(m, "start")
	groupsVal, _ := evaluator.MapGet(m, "groups")
	obj := evaluator.NewOrderedMapWithCapacity(3)
	obj.Set("match", matchVal)
	obj.Set("index", startVal)
	obj.Set("groups", groupsVal)
	return obj
}

// isMatcherResult reports whether a custom matcher returned a match structure,
// using jsonata-js's test: a numeric start, an array of groups or a next function.
func isMatcherResult(m any) bool {
	if !evaluator.IsMap(m) {
		return false
	}
	start, _ := evaluator.MapGet(m, "start")
	groups, _ := evaluator.MapGet(m, "groups")
	if _, isNumber := evaluator.ToFloat64(start); isNumber || evaluator.IsArray(groups) {
		return true
	}
	next, _ := evaluator.MapGet(m, "next")
	switch next.(type) {
	case evaluator.BuiltinFunction, evaluator.EnvAwareBuiltin, *evaluator.Lambda, *evaluator.SignedBuiltin:
		return true
	}
	return false
}

// isFalsyJS reports whether jsonata-js would treat a matcher's return value as
// falsy (JavaScript truthiness, under which empty arrays and objects are true).
func isFalsyJS(v any) bool {
	if v == nil || evaluator.IsNull(v) {
		return true
	}
	switch x := v.(type) {
	case bool:
		return !x
	case string:
		return x == ""
	}
	f, isNumber := evaluator.ToFloat64(v)
	return isNumber && f == 0
}

func matchResultSeq(result []any) any {
	return &evaluator.Sequence{Values: result}
}

func matchWithCustomMatcher(s string, matcherFn any, limit int, evalFn EvalFn, env *evaluator.Environment) (any, error) {
	if limit == 0 {
		return nil, nil
	}
	var result []any
	res, err := callMatcher(matcherFn, []any{s, float64(0)}, evalFn, env)
	for count := 0; err == nil && res != nil && (limit < 0 || count < limit); count++ {
		result = append(result, matchResult(res))
		nextFn, _ := evaluator.MapGet(res, "next")
		if nextFn == nil {
			break
		}
		res, err = callMatcher(nextFn, nil, evalFn, env)
	}
	if err != nil {
		return nil, err
	}
	return matchResultSeq(result), nil
}

// callMatcher calls a custom matcher, or a match's next function, and checks
// the result as jsonata-js's evaluateMatcher does: a falsy result means no
// match, and anything else must be a match structure.
func callMatcher(fn any, args []any, evalFn EvalFn, env *evaluator.Environment) (any, error) {
	res, err := evalFn(fn, args, nil, env)
	if err != nil || isFalsyJS(res) {
		return nil, err
	}
	if !isMatcherResult(res) {
		return nil, &evaluator.JSONataError{Code: "T1010", Message: "$match: the matcher function did not return a match object"}
	}
	return res, nil
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

		limitArg, err := optionalLimit(args, 3, "$replace", "D3011")
		if err != nil {
			return nil, err
		}
		limit := matchCountLimit(limitArg)

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
			if repl, ok := args[2].(string); ok {
				return replaceRegexString(s, re, repl, limit)
			}
			return replaceWithFn(s, re, args[2], limit, evalFn, focus, env)

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

// replaceRegexString replaces regex matches with a JSONata template string.
func replaceRegexString(s string, re *evaluator.Regex, repl string, limit int) (string, error) {
	return replaceRegex(s, re, limit, func(m *evaluator.Match) (string, error) {
		return expandJSONataReplacement(repl, m.String(), m.Groups()), nil
	})
}

func replaceWithFn(s string, re *evaluator.Regex, fn any, limit int, evalFn EvalFn, focus any, env *evaluator.Environment) (any, error) {
	return replaceRegex(s, re, limit, func(m *evaluator.Match) (string, error) {
		val, err := evalFn(fn, []any{replacerMatchObject(s, m)}, focus, env)
		if err != nil {
			return "", err
		}
		sv, ok := val.(string)
		if !ok {
			return "", &evaluator.JSONataError{Code: "D3012", Message: "$replace: replacement function must return a string"}
		}
		return sv, nil
	})
}

// replaceRegex replaces the matches of re in s, at most limit of them (-1 for
// no limit), with what replacement returns for each.
func replaceRegex(s string, re *evaluator.Regex, limit int, replacement func(*evaluator.Match) (string, error)) (string, error) {
	var b strings.Builder
	prev := 0
	err := eachRegexMatch(re, s, limit, func(m *evaluator.Match) error {
		sub, err := replacement(m)
		if err != nil {
			return err
		}
		b.WriteString(s[prev:m.Index])
		b.WriteString(sub)
		prev = m.Index + m.Length
		return nil
	})
	if err != nil {
		return "", err
	}
	b.WriteString(s[prev:])
	return b.String(), nil
}

// replacerMatchObject is the match object $replace passes to a replacement
// function: a regex match whose next function returns the following one, as
// in jsonata-js. Only this object carries next; the match objects an
// evaluation can return omit it, since a function value has no JSON form.
func replacerMatchObject(s string, m *evaluator.Match) *evaluator.OrderedMap {
	obj := evaluator.NewMatchObject(s, m)
	obj.Set("next", evaluator.BuiltinFunction(func([]any, any) (any, error) {
		next, err := evaluator.NextMatch(m)
		if next == nil || err != nil {
			return nil, err
		}
		return replacerMatchObject(s, next), nil
	}))
	return obj
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
	err := eachRegexMatch(re, s, limit, func(m *evaluator.Match) error {
		parts = append(parts, s[lastEnd:m.Index])
		lastEnd = m.Index + m.Length
		return nil
	})
	if err != nil {
		return nil, err
	}
	if limit < 0 || len(parts) < limit {
		parts = append(parts, s[lastEnd:])
	}
	return parts, nil
}
