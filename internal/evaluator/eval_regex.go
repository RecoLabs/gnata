package evaluator

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
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

// findMatchFrom returns the first match in s that starts at or after the
// byte offset from. A regex without left context matches the suffix exactly.
// One with left context is matched on all of s, so a match that starts before
// from and ends at or after it can hide one that starts at from.
func (r *Regex) findMatchFrom(s string, from int) *Match {
	if from > 0 && r.leftContext {
		// Matches start at distinct bytes, so at most from of them start
		// before from.
		for _, loc := range r.re.FindAllStringSubmatchIndex(s, from+1) {
			if loc[0] >= from {
				return r.matchAt(s, loc)
			}
		}
		return nil
	}
	loc := r.re.FindStringSubmatchIndex(s[from:])
	if loc == nil {
		return nil
	}
	for i := range loc {
		if loc[i] >= 0 {
			loc[i] += from
		}
	}
	return r.matchAt(s, loc)
}

func (r *Regex) matchAt(s string, loc []int) *Match {
	return &Match{Index: loc[0], Length: loc[1] - loc[0], input: s, regex: r, loc: loc}
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

// RegexLiteral is the value a regex literal evaluates to. jsonata-js treats it
// as a function, and so does the evaluator. At the API edges, in results and
// in custom function arguments and return values, it is the map ToMap returns.
type RegexLiteral struct {
	Pattern string
	Flags   string
}

// Compile compiles the regex, caching it by pattern and flags.
func (r *RegexLiteral) Compile() (*Regex, error) {
	return CachedCompileRegex(r.Pattern, r.Flags)
}

// ToMap returns the {"pattern", "flags"} map that stands for r at the API edges.
func (r *RegexLiteral) ToMap() map[string]any {
	return map[string]any{"pattern": r.Pattern, "flags": r.Flags}
}

// MarshalJSON encodes r as its ToMap form.
func (r *RegexLiteral) MarshalJSON() ([]byte, error) {
	return AppendJSON(nil, r)
}

// RegexFromMap returns the regex a custom function's return value stands for:
// a map with exactly the string "pattern" and "flags" ToMap produces.
func RegexFromMap(v any) (*RegexLiteral, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 2 {
		return nil, false
	}
	pattern, hasPattern := m["pattern"].(string)
	flags, hasFlags := m["flags"].(string)
	if !hasPattern || !hasFlags {
		return nil, false
	}
	return &RegexLiteral{Pattern: pattern, Flags: flags}, true
}

// CallRegexArity is the parameter count of jsonata-js's regex closure,
// (str, fromIndex), which sets how many arguments a callback receives.
const CallRegexArity = 2

// callRegex applies a regex called as a function, like jsonata-js's closure:
// the first match in the first argument, searching from the code point offset
// in the second.
func callRegex(r *RegexLiteral, args []any) (any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	from := 0
	if len(args) > 1 {
		from = regexOffset(args[1])
	}
	return applyRegex(args[0], r, from)
}

// regexOffset converts a fromIndex argument the way JavaScript sets a regex's
// lastIndex: a number, or a value that converts to one, truncated toward zero;
// anything else, or a negative number, is 0.
func regexOffset(v any) int {
	switch val := v.(type) {
	case float64:
		return max(ToIntClamped(val), 0)
	case json.Number:
		return regexOffset(jsStringToNumber(string(val)))
	case string:
		return regexOffset(jsStringToNumber(val))
	case bool:
		if val {
			return 1
		}
	}
	// JavaScript converts an array through its string, where a boolean item
	// is a word, not a number.
	if arr, ok := AsArray(v); ok && len(arr) == 1 {
		if _, isBool := arr[0].(bool); !isBool {
			return regexOffset(arr[0])
		}
	}
	return 0
}

// jsStringToNumber converts s the way JavaScript's Number does: a decimal,
// Infinity, or a hex, binary or octal integer, around white space; "" is 0
// and anything else NaN. A number too large for float64 is infinite.
func jsStringToNumber(s string) float64 {
	s = strings.TrimFunc(s, isJSWhiteSpace)
	switch s {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if base, _ := RadixPrefix(s); base != 0 {
		n, err := strconv.ParseUint(s[2:], base, 64)
		switch {
		case err == nil:
			return float64(n)
		case errors.Is(err, strconv.ErrRange):
			return math.Inf(1)
		}
		return math.NaN()
	}
	// Go also reads inf, nan, hex floats and digit underscores, which
	// JavaScript does not.
	if strings.ContainsAny(s, "iInNxXpP_") {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
	return f
}

// isJSWhiteSpace reports whether JavaScript's Number trims r: a space
// separator, a line terminator, tab, vertical tab, form feed or U+FEFF.
func isJSWhiteSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', '\u2028', '\u2029', '\uFEFF':
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// applyRegex applies a regex the way jsonata-js applies it as a function, in
// a call (/re/(s)) or on the right of ~>. It returns the first match object at
// or after the code point offset from, or nil (undefined) when there is none.
// A one-item array holding a string is that string, as JavaScript reads it.
func applyRegex(input any, r *RegexLiteral, from int) (any, error) {
	for arr, ok := AsArray(input); ok && len(arr) == 1; arr, ok = AsArray(input) {
		input = arr[0]
	}
	s, ok := input.(string)
	if !ok {
		return nil, nil
	}
	start, ok := byteOffset(s, from)
	if !ok {
		return nil, nil
	}
	re, err := r.Compile()
	if err != nil {
		return nil, &JSONataError{Code: "D1002", Message: fmt.Sprintf("invalid regex: %v", err)}
	}
	m := re.findMatchFrom(s, start)
	if m == nil {
		return nil, nil
	}
	return NewMatchObject(s, m), nil
}

// byteOffset returns the byte offset of the code point offset runes into s,
// and false when s is shorter than that.
func byteOffset(s string, runes int) (int, bool) {
	for i := range s {
		if runes == 0 {
			return i, true
		}
		runes--
	}
	return len(s), runes == 0
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

func evalRegex(raw string) *RegexLiteral {
	if idx := strings.LastIndex(raw, "/"); idx >= 0 {
		suffix := raw[idx+1:]
		if isRegexFlags(suffix) {
			return &RegexLiteral{Pattern: raw[:idx], Flags: suffix}
		}
	}
	return &RegexLiteral{Pattern: raw}
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
