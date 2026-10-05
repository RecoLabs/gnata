package evaluator

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// Regex wraps Go's standard regexp (RE2) with the interface needed by gnata's
// JSONata functions ($match, $replace, $contains, $split, ~> chain operator).
// RE2 guarantees linear-time matching — no backtracking, no timeouts.
type Regex struct {
	re          *regexp.Regexp
	leftContext bool // has ^, \A, \b or \B, which a match on a suffix of the input misreads
}

// Match represents a single regex match.
type Match struct {
	Index  int
	Length int

	input    string
	regex    *Regex
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
		regex:    r,
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
		regex:    m.regex,
		loc:      loc,
		allLocs:  m.allLocs,
		matchIdx: nextIdx,
	}, nil
}

// NextMatch returns the match after m the way jsonata-js's regex matcher
// finds it: none once m reaches the end of the input, and D1004 when the
// search from m's end finds an empty match, which jsonata-js reports because
// it would never progress. After an empty match that is every later search.
func NextMatch(m *Match) (*Match, error) {
	end := m.Index + m.Length
	if end >= len(m.input) {
		return nil, nil
	}
	if m.Length == 0 || m.emptyMatchAt(end) {
		return nil, errEmptyMatch
	}
	next, err := m.FindNextMatch()
	if next != nil && next.Length == 0 {
		return nil, errEmptyMatch
	}
	return next, err
}

var errEmptyMatch = &JSONataError{Code: "D1004", Message: "the regular expression matches a zero-length string"}

// emptyMatchAt reports whether the regex's preferred match at pos is empty.
// FindAll skips such a match when it abuts the previous one, but jsonata-js
// finds it. A regex that reads left context is assumed not to, since a match
// on the suffix cannot see that context.
func (m *Match) emptyMatchAt(pos int) bool {
	if m.regex.leftContext {
		return false
	}
	loc := m.regex.re.FindStringIndex(m.input[pos:])
	return len(loc) == 2 && loc[0] == 0 && loc[1] == 0
}

// readsLeftContext reports whether re has an assertion that depends on the
// text before the match position.
func readsLeftContext(re *syntax.Regexp) bool {
	return slices.Contains(leftContextOps, re.Op) || slices.ContainsFunc(re.Sub, readsLeftContext)
}

var leftContextOps = []syntax.Op{syntax.OpBeginLine, syntax.OpBeginText, syntax.OpWordBoundary, syntax.OpNoWordBoundary}

// Groups returns the text of each capture group, "" for one that did not
// take part in the match.
func (m *Match) Groups() []string {
	groups := make([]string, 0, m.GroupCount()-1)
	for g := 1; g < m.GroupCount(); g++ {
		groups = append(groups, m.GroupByNumber(g).String())
	}
	return groups
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

	// Clone so the process-wide cache never retains a caller's string (which
	// may share memory with a larger buffer).
	fullPattern := strings.Clone(inlineFlags + pattern)
	re, err := regexp.Compile(fullPattern)
	if err != nil {
		return nil, err
	}
	parsed, err := syntax.Parse(fullPattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	r := &Regex{re: re, leftContext: readsLeftContext(parsed)}
	evalRegexCache.Store(key, r)
	return r, nil
}

// CompileLiteralRegex compiles an escaped literal string as an RE2 pattern.
func CompileLiteralRegex(literal string) (*Regex, error) {
	return CachedCompileRegex(regexp.QuoteMeta(literal), "")
}

// ── Regex as a function (~> and calls) ────────────────────────────────────────

// RegexValue returns v as a regex value: a map with exactly the string
// "pattern" and "flags" a regex literal evaluates to. Any other object,
// including one with only a "pattern", stays an object, as in jsonata-js.
func RegexValue(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 2 {
		return nil, false
	}
	_, hasPattern := m["pattern"].(string)
	_, hasFlags := m["flags"].(string)
	return m, hasPattern && hasFlags
}

// CompileRegexValue compiles a map that RegexValue accepted.
func CompileRegexValue(m map[string]any) (*Regex, error) {
	return CachedCompileRegex(m["pattern"].(string), m["flags"].(string))
}

// callRegex applies a regex called as a function to its first argument.
// jsonata-js reads a second argument as the offset to search from; gnata
// always searches from the start.
func callRegex(regexMap map[string]any, args []any) (any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	return applyRegexTest(args[0], regexMap)
}

// applyRegexTest applies a regex the way jsonata-js applies it as a function,
// in a call (/re/(s)) or on the right of ~>. It returns the first match object
// (like $match with limit 1) when the regex matches, or nil (undefined) when
// it does not.
func applyRegexTest(input any, regexMap map[string]any) (any, error) {
	s, ok := input.(string)
	if !ok {
		return nil, nil
	}
	re, err := CompileRegexValue(regexMap)
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
	return NewMatchObject(s, m), nil
}

// NewMatchObject builds the {match, start, end, groups} object jsonata-js
// produces for a regex match, with rune offsets into s. A capture group that
// did not participate in the match is "": jsonata-js leaves it undefined,
// which gnata cannot hold in an array, and "" behaves the same in string
// operations whereas nil would read as null.
func NewMatchObject(s string, m *Match) *OrderedMap {
	groups := make([]any, 0, m.GroupCount()-1)
	for _, g := range m.Groups() {
		groups = append(groups, g)
	}
	obj := NewOrderedMapWithCapacity(5)
	obj.Set("match", m.String())
	obj.Set("start", float64(utf8.RuneCountInString(s[:m.Index])))
	obj.Set("end", float64(utf8.RuneCountInString(s[:m.Index+m.Length])))
	obj.Set("groups", groups)
	return obj
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
