package evaluator

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// Regex wraps Go's standard regexp (RE2) with the interface needed by gnata's
// JSONata functions ($match, $replace, $contains, $split, ~> chain operator).
// RE2 guarantees linear-time matching — no backtracking, no timeouts.
type Regex struct {
	re *regexp.Regexp
}

// Match represents a single regex match.
type Match struct {
	Index  int
	Length int

	input    string
	re       *regexp.Regexp
	loc      []int   // submatch index pairs for this match
	allLocs  [][]int // all matches from FindAllStringSubmatchIndex
	matchIdx int     // index into allLocs for this match
}

// Group represents a capture group within a match.
type Group struct {
	Index    int
	Length   int
	Captured bool
	value    string
}

// ── Regex methods ─────────────────────────────────────────────────────────────

func (r *Regex) FindStringMatch(s string) (*Match, error) {
	allLocs := r.re.FindAllStringSubmatchIndex(s, -1)
	if len(allLocs) == 0 {
		return nil, nil
	}
	loc := allLocs[0]
	return &Match{
		Index:    loc[0],
		Length:   loc[1] - loc[0],
		input:    s,
		re:       r.re,
		loc:      loc,
		allLocs:  allLocs,
		matchIdx: 0,
	}, nil
}

func (r *Regex) MatchString(s string) (bool, error) {
	return r.re.MatchString(s), nil
}

// ── Match methods ─────────────────────────────────────────────────────────────

func (m *Match) String() string {
	return m.input[m.Index : m.Index+m.Length]
}

func (m *Match) GroupCount() int {
	return len(m.loc) / 2
}

func (m *Match) GroupByNumber(i int) *Group {
	idx := i * 2
	if idx+1 >= len(m.loc) || m.loc[idx] < 0 {
		return &Group{Captured: false}
	}
	start := m.loc[idx]
	end := m.loc[idx+1]
	return &Group{
		Index:    start,
		Length:   end - start,
		Captured: true,
		value:    m.input[start:end],
	}
}

// FindNextMatch returns the next match from the pre-computed results.
// Matches are computed on the full original string (via FindAllStringSubmatchIndex)
// to preserve anchor semantics (^, $, \b).
func (m *Match) FindNextMatch() (*Match, error) {
	nextIdx := m.matchIdx + 1
	if nextIdx >= len(m.allLocs) {
		return nil, nil
	}
	loc := m.allLocs[nextIdx]
	return &Match{
		Index:    loc[0],
		Length:   loc[1] - loc[0],
		input:    m.input,
		re:       m.re,
		loc:      loc,
		allLocs:  m.allLocs,
		matchIdx: nextIdx,
	}, nil
}

// ── Group methods ─────────────────────────────────────────────────────────────

func (g *Group) String() string {
	return g.value
}

// ── Compilation ───────────────────────────────────────────────────────────────

var evalRegexCache sync.Map

func re2InlineFlags(flags string) string {
	var buf strings.Builder
	if strings.ContainsRune(flags, 'i') {
		buf.WriteByte('i')
	}
	if strings.ContainsRune(flags, 'm') {
		buf.WriteByte('m')
	}
	if strings.ContainsRune(flags, 's') {
		buf.WriteByte('s')
	}
	if buf.Len() == 0 {
		return ""
	}
	return "(?" + buf.String() + ")"
}

// CachedCompileRegex compiles a regex pattern with caching using Go's standard
// regexp package (RE2 engine, guaranteed linear-time matching).
func CachedCompileRegex(pattern, flags string) (*Regex, error) {
	inlineFlags := re2InlineFlags(flags)
	key := inlineFlags + ":" + pattern
	if cached, ok := evalRegexCache.Load(key); ok {
		return cached.(*Regex), nil
	}

	fullPattern := inlineFlags + pattern
	re, err := regexp.Compile(fullPattern)
	if err != nil {
		return nil, err
	}
	r := &Regex{re: re}
	evalRegexCache.Store(key, r)
	return r, nil
}

// CompileLiteralRegex compiles an escaped literal string as an RE2 pattern.
func CompileLiteralRegex(literal string) (*Regex, error) {
	return CachedCompileRegex(regexp.QuoteMeta(literal), "")
}

// ── Chain operator (~>) with regex ────────────────────────────────────────────

// applyRegexTest implements the JSONata chain operator (~>) with a regex on the
// right-hand side. It returns the first match object (like $match with limit 1)
// when the regex matches, or nil (undefined) when it does not.
func applyRegexTest(input any, regexMap map[string]any) (any, error) {
	s, ok := input.(string)
	if !ok {
		return nil, nil
	}
	pattern, _ := regexMap["pattern"].(string)
	flags, _ := regexMap["flags"].(string)
	re, err := CachedCompileRegex(pattern, flags)
	if err != nil {
		return nil, &JSONataError{Code: "D1002", Message: fmt.Sprintf("invalid regex: %v", err)}
	}
	m, err := re.FindStringMatch(s)
	if err != nil {
		return nil, &JSONataError{Code: "D1002", Message: fmt.Sprintf("regex error: %v", err)}
	}
	if m == nil {
		return nil, nil
	}
	groups := make([]any, 0, m.GroupCount()-1)
	for g := 1; g < m.GroupCount(); g++ {
		grp := m.GroupByNumber(g)
		if !grp.Captured {
			groups = append(groups, "")
			continue
		}
		groups = append(groups, grp.String())
	}
	return map[string]any{
		"match":  m.String(),
		"start":  float64(utf8.RuneCountInString(s[:m.Index])),
		"end":    float64(utf8.RuneCountInString(s[:m.Index+m.Length])),
		"groups": groups,
	}, nil
}

// ── Regex parsing ─────────────────────────────────────────────────────────────

func evalRegex(raw string) map[string]any {
	if idx := strings.LastIndex(raw, "/"); idx >= 0 {
		suffix := raw[idx+1:]
		if isRegexFlags(suffix) {
			return map[string]any{"pattern": raw[:idx], "flags": suffix}
		}
	}
	return map[string]any{"pattern": raw, "flags": ""}
}

func isRegexFlags(s string) bool {
	for _, c := range s {
		switch c {
		case 'g', 'i', 'm', 'x', 's', 'u':
		default:
			return false
		}
	}
	return true
}
