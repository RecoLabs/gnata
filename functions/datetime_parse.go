package functions

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/recolabs/gnata/internal/evaluator"
)

// parseWithPicture parses input against a picture string. It reports false when
// the input does not match the picture; defaults for unspecified components
// come from now, the evaluation's timestamp.
func parseWithPicture(input, picture string, now time.Time) (time.Time, bool, error) {
	parts, err := parseDatePicture(picture)
	if err != nil {
		return time.Time{}, false, err
	}
	for _, part := range parts {
		m := part.marker
		if !part.isMarker {
			continue
		}
		if isNamePresentation(m.presentation) && !strings.ContainsRune("MxFPZzf", rune(m.component)) {
			return time.Time{}, false, &evaluator.JSONataError{
				Code:    "D3133",
				Message: fmt.Sprintf("$toMillis: the 'name' modifier can only be applied to months and days, not %c", m.component),
			}
		}
		if strings.ContainsRune("YMDdFWwXxHhms", rune(m.component)) && lacksIntegerFormat(m) {
			return time.Time{}, false, &evaluator.JSONataError{
				Code:    "D3130",
				Message: fmt.Sprintf("$toMillis: unsupported picture %q", m.presentation),
			}
		}
	}

	components := map[byte]int{}
	inputRunes := []rune(input)
	pos := 0
	for i, part := range parts {
		if !part.isMarker {
			if !hasFoldPrefix(inputRunes[pos:], part.literal) {
				return time.Time{}, false, nil
			}
			pos += utf8.RuneCountInString(part.literal)
			continue
		}
		if c := part.marker.component; c == 'C' || c == 'E' {
			continue
		}
		value, n := parseMarkerValue(inputRunes[pos:], part.marker)
		if n <= 0 {
			return time.Time{}, false, nil
		}
		if isNamePresentation(part.marker.presentation) && i+1 < len(parts) {
			if yielded := yieldToNext(inputRunes[pos:], n, &parts[i+1]); yielded != n {
				if value, n = parseMarkerValue(inputRunes[pos:pos+yielded], part.marker); n != yielded {
					return time.Time{}, false, nil
				}
			}
		}
		components[part.marker.component] = value
		pos += n
	}
	if pos != len(inputRunes) || len(components) == 0 {
		return time.Time{}, false, nil
	}
	t, err := resolveParsedDate(components, now)
	return t, err == nil, err
}

func isNamePresentation(presentation string) bool {
	return presentation != "" && (presentation[0] == 'n' || presentation[0] == 'N')
}

// lacksIntegerFormat reports whether an integer marker's picture has no
// decimal digit and is not an alphabetic, roman, word or name presentation,
// which jsonata-js rejects with D3130.
func lacksIntegerFormat(m dateMarker) bool {
	switch m.presentation {
	case "a", "A", "i", "I", "w", "W", "Ww":
		return false
	}
	mandatory, _ := decimalPictureDigits(m.presentation)
	return mandatory == 0 && !isNamePresentation(m.presentation)
}

// isASCIILetter matches the letters jsonata-js accepts in a name, [a-zA-Z].
func isASCIILetter(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// maxYieldLetters caps how many letters yieldToNext gives back. It covers any
// literal, word or numeral that can follow a name while keeping the search
// linear in the input: each retry re-parses the rest of it.
const maxYieldLetters = 64

// yieldToNext gives back trailing letters of the n runes consumed for a name
// when the picture part after it would otherwise not match, as jsonata-js's
// backtracking regex does: "MayT" against [MNn,3-3]T reads "May".
func yieldToNext(runes []rune, n int, next *datePicturePart) int {
	if next.isMarker && (next.marker.component == 'C' || next.marker.component == 'E') {
		return n
	}
	matches := func(k int) bool {
		if !next.isMarker {
			return hasFoldPrefix(runes[k:], next.literal)
		}
		_, consumed := parseMarkerValue(runes[k:], next.marker)
		return consumed > 0
	}
	if matches(n) {
		return n
	}
	for k := n - 1; k >= max(1, n-maxYieldLetters) && isASCIILetter(runes[k]); k-- {
		if matches(k) {
			return k
		}
	}
	return n
}

func hasFoldPrefix(runes []rune, literal string) bool {
	i := 0
	for _, lr := range literal {
		if i >= len(runes) || unicode.ToLower(runes[i]) != unicode.ToLower(lr) {
			return false
		}
		i++
	}
	return true
}

// parseMarkerValue reads one component's value from the start of runes and
// returns it with the number of runes consumed, 0 or less when nothing matched.
func parseMarkerValue(runes []rune, m dateMarker) (value, consumed int) {
	switch m.component {
	case 'f':
		v, n := parseTokenValue(runes, m)
		for w := n; w < 3 && n > 0; w++ {
			v *= 10
		}
		for w := n; w > 3; w-- {
			v /= 10
		}
		return v, n
	case 'P':
		if len(runes) < 2 {
			return 0, 0
		}
		switch strings.ToLower(string(runes[:2])) {
		case "am":
			return 0, 2
		case "pm":
			return 1, 2
		}
		return 0, 0
	case 'F', 'W', 'w', 'x':
		return 0, consumeNameOrNumber(runes, m.presentation)
	case 'Z', 'z':
		return parseTZFromInput(runes)
	default:
		return parseTokenValue(runes, m)
	}
}

// specifiedOnly reports whether the specified components among candidates are
// all in allowed, with at least one of them present (jsonata-js's isType).
func specifiedOnly(components map[byte]int, candidates, allowed string) bool {
	found := false
	for i := range len(candidates) {
		if _, ok := components[candidates[i]]; !ok {
			continue
		}
		if !strings.Contains(allowed, candidates[i:i+1]) {
			return false
		}
		found = true
	}
	return found
}

// resolveParsedDate fills in the components the picture left out the way
// jsonata-js does: those more significant than the first specified one come
// from now, less significant ones are zero (one for month and day), and a
// specified component after an unspecified one is an error.
func resolveParsedDate(components map[byte]int, now time.Time) (time.Time, error) {
	const dateCandidates, timeCandidates = "YXMxWwdD", "PHhmsf"
	if specifiedOnly(components, dateCandidates, "Xxw") || specifiedOnly(components, dateCandidates, "XW") {
		return time.Time{}, &evaluator.JSONataError{
			Code:    "D3136",
			Message: "$toMillis: parsing an ISO week date is not supported",
		}
	}
	dayOfYear := !specifiedOnly(components, dateCandidates, "YMD") && specifiedOnly(components, dateCandidates, "Yd")
	twelveHour := !specifiedOnly(components, timeCandidates, "Hmsf") && specifiedOnly(components, timeCandidates, "Phmsf")

	order := "YMD"
	if dayOfYear {
		order = "Yd"
	}
	if twelveHour {
		order += "Phmsf"
	} else {
		order += "Hmsf"
	}
	startSpecified, endSpecified := false, false
	for i := range len(order) {
		part := order[i]
		if _, ok := components[part]; ok {
			if endSpecified {
				return time.Time{}, &evaluator.JSONataError{
					Code:    "D3136",
					Message: "$toMillis: the date/time picture string is missing specifiers required to parse the timestamp",
				}
			}
			startSpecified = true
			continue
		}
		switch {
		case startSpecified:
			components[part] = 0
			if strings.ContainsRune("MDd", rune(part)) {
				components[part] = 1
			}
			endSpecified = true
		case part == 'f':
			components[part] = now.Nanosecond() / int(time.Millisecond)
		case part == 'P':
			components[part] = 0
		default:
			components[part] = dateComponentValue(now, part)
		}
	}

	hour := components['H']
	if twelveHour {
		if hour = components['h']; hour == 12 {
			hour = 0
		}
		if components['P'] == 1 {
			hour += 12
		}
	}
	month, day := max(components['M'], 1), components['D']
	if dayOfYear {
		month, day = 1, components['d']
	}
	t := time.Date(components['Y'], time.Month(month), day, hour, components['m'], components['s'],
		components['f']*int(time.Millisecond), time.UTC)
	if offset, ok := components['Z']; ok {
		t = t.Add(-time.Duration(offset) * time.Second)
	} else if offset, ok := components['z']; ok {
		t = t.Add(-time.Duration(offset) * time.Second)
	}
	return t, nil
}

// consumeNameOrNumber returns how many runes of a name (any run of letters, as
// jsonata-js accepts) or number start runes, depending on the presentation.
func consumeNameOrNumber(runes []rune, presentation string) int {
	isName := isNamePresentation(presentation)
	i := 0
	for i < len(runes) && ((isName && isASCIILetter(runes[i])) || (!isName && unicode.IsDigit(runes[i]))) {
		i++
	}
	return i
}

func parseTZFromInput(runes []rune) (offset, consumed int) {
	if len(runes) == 0 {
		return 0, 0
	}

	// Handle "GMT±HH:MM" or "GMT±HH" format (z component).
	if hasFoldPrefix(runes, "GMT") {
		offset, n := parseTZFromInput(runes[3:])
		return offset, 3 + n
	}

	// Handle ±HH:MM, ±HHMM, Z.
	if len(runes) > 0 && runes[0] == 'Z' {
		return 0, 1
	}

	sign := 1
	i := 0
	switch {
	case i < len(runes) && runes[i] == '+':
		i++
	case i < len(runes) && runes[i] == '-':
		sign = -1
		i++
	default:
		return 0, 0
	}

	// Parse up to 2 digits for hours.
	hStart := i
	for i < len(runes) && unicode.IsDigit(runes[i]) && i-hStart < 2 {
		i++
	}
	if i == hStart {
		return 0, 0
	}
	hours, _ := strconv.Atoi(string(runes[hStart:i]))
	mins := 0

	// Optional colon.
	if i < len(runes) && runes[i] == ':' {
		i++
	}

	// Parse up to 2 digits for minutes.
	mStart := i
	for i < len(runes) && unicode.IsDigit(runes[i]) && i-mStart < 2 {
		i++
	}
	if i > mStart {
		mins, _ = strconv.Atoi(string(runes[mStart:i]))
	}

	return sign * (hours*3600 + mins*60), i
}

// parseTokenValue reads an integer marker's value. Month names and plain
// numbers read their width from the full modifier.
func parseTokenValue(runes []rune, m dateMarker) (value, consumed int) {
	if len(runes) == 0 {
		return -1, -1
	}
	switch p := m.presentation; {
	case p == "I" || p == "i":
		return parseRoman(runes)
	case p == "a" || p == "A":
		return parseAlphabetic(runes, p)
	case isNamePresentation(p):
		return parseMonthName(runes, m.modifier)
	case p == "w" || p == "W" || p == "Ww" || p == "ww":
		return parseWordNumber(runes, p)
	case m.ordinal:
		return parseOrdinalNumber(runes)
	}
	return parseNumericValue(runes, m.modifier)
}

func modifierFieldWidth(modifier string) int {
	if modifier == "" {
		return -1
	}
	// Handle grouping/truncation modifier: starts with ","
	// Pattern: ",[minWidth]-[maxWidth]" or ",*-[maxWidth]"
	if strings.HasPrefix(modifier, ",") {
		// Look for "-N" at the end (max width).
		if idx := strings.LastIndex(modifier, "-"); idx >= 0 {
			part := modifier[idx+1:]
			if n, err := strconv.Atoi(part); err == nil && n > 0 {
				return n
			}
		}
		return -1
	}
	// All-digit modifier: length = field width.
	if len(modifier) < 2 {
		return -1 // "1" = variable width
	}
	for _, r := range modifier {
		if r != '0' && r != '1' {
			return -1
		}
	}
	return len(modifier)
}

func parseNumericValue(runes []rune, modifier string) (value, consumed int) {
	i := 0 // jsonata-js accepts no sign on a parsed integer
	maxW := modifierFieldWidth(modifier)
	for i < len(runes) && unicode.IsDigit(runes[i]) {
		if maxW > 0 && i >= maxW {
			break
		}
		i++
	}
	if i == 0 {
		return -1, -1
	}
	n, err := strconv.Atoi(string(runes[:i]))
	if err != nil {
		return -1, -1
	}
	return n, i
}

func parseOrdinalNumber(runes []rune) (value, consumed int) {
	i := 0
	for i < len(runes) && unicode.IsDigit(runes[i]) {
		i++
	}
	if i == 0 {
		return -1, -1
	}
	n, err := strconv.Atoi(string(runes[:i]))
	if err != nil {
		return -1, -1
	}
	// Consume optional ordinal suffix (st/nd/rd/th).
	if i+2 <= len(runes) {
		suffix := strings.ToLower(string(runes[i : i+2]))
		if suffix == "st" || suffix == "nd" || suffix == "rd" || suffix == "th" {
			i += 2
		}
	}
	return n, i
}

func parseRoman(runes []rune) (value, consumed int) {
	romanVals := map[rune]int{
		'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000,
		'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100, 'd': 500, 'm': 1000,
	}
	i := 0
	for i < len(runes) {
		if _, ok := romanVals[runes[i]]; !ok {
			break
		}
		i++
	}
	if i == 0 {
		return -1, -1
	}
	// Evaluate Roman numeral.
	total := 0
	prev := 0
	for j := i - 1; j >= 0; j-- {
		v := romanVals[runes[j]]
		if v < prev {
			total -= v
		} else {
			total += v
			prev = v
		}
	}
	return total, i
}

func parseAlphabetic(runes []rune, modifier string) (value, consumed int) {
	base := 'a'
	if modifier == "A" {
		base = 'A'
	}
	i := 0
	result := 0
	for i < len(runes) {
		r := runes[i]
		if unicode.ToLower(r) < 'a' || unicode.ToLower(r) > 'z' {
			break
		}
		// Make it lowercase-relative to base.
		digit := int(unicode.ToLower(r) - unicode.ToLower(base) + 1)
		result = result*26 + digit
		i++
	}
	if i == 0 {
		return -1, -1
	}
	return result, i
}

func parseMonthName(runes []rune, modifier string) (month, consumed int) {
	// Determine max length to match.
	maxLen := 0
	if strings.Contains(modifier, ",") {
		parts := strings.SplitN(modifier, ",", 2)
		if len(parts) == 2 {
			rangePart := parts[1]
			rangeParts := strings.Split(rangePart, "-")
			if v, err := strconv.Atoi(rangeParts[0]); err == nil {
				maxLen = v
			}
			if len(rangeParts) == 2 {
				if v, err := strconv.Atoi(rangeParts[1]); err == nil {
					maxLen = v
				}
			}
		}
	}

	// Try matching full month names first, then abbreviated. With a maximum
	// width the match consumes the rest of the word, so "January" reads as "Jan".
	for mi, name := range monthNames {
		if maxLen > 0 {
			abbr := string([]rune(name)[:min(maxLen, utf8.RuneCountInString(name))])
			if hasFoldPrefix(runes, abbr) {
				end := utf8.RuneCountInString(abbr)
				for end < len(runes) && isASCIILetter(runes[end]) {
					end++
				}
				return mi + 1, end
			}
		} else if hasFoldPrefix(runes, name) {
			return mi + 1, utf8.RuneCountInString(name)
		}
	}
	return -1, -1
}

func parseWordNumber(runes []rune, _ string) (value, consumed int) {
	// Consume up to the end of a word-number expression.
	// Word numbers end at a non-word char that's not '-' or space.
	n, v := parseWordNumberFromString(string(runes))
	if n == 0 {
		return -1, -1
	}
	return v, n
}

func parseWordNumberFromString(s string) (consumed, value int) {
	ones := []string{
		"zero", "one", "two", "three", "four", "five", "six", "seven",
		"eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
		"sixteen", "seventeen", "eighteen", "nineteen",
	}
	tens := []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}

	slower := strings.ToLower(s)

	result, n := parseCardinalNumber(slower, ones, tens)
	if n > 0 {
		return n, result
	}
	return 0, -1
}

func parseCardinalNumber(s string, ones, tens []string) (value, consumed int) {
	val, n := parseComplexNumber(s, ones, tens)
	if n > 0 {
		return val, n
	}
	return 0, -1
}

type wordNumberParser struct {
	s    string // lowercase input
	pos  int
	ones []string
	tens []string
}

var onesWithOrdinals = [][]string{
	{"zero", "zeroth"},
	{"one", "first"},
	{"two", "second"},
	{"three", "third"},
	{"four", "fourth"},
	{"five", "fifth"},
	{"six", "sixth"},
	{"seven", "seventh"},
	{"eight", "eighth"},
	{"nine", "ninth"},
	{"ten", "tenth"},
	{"eleven", "eleventh"},
	{"twelve", "twelfth"},
	{"thirteen", "thirteenth"},
	{"fourteen", "fourteenth"},
	{"fifteen", "fifteenth"},
	{"sixteen", "sixteenth"},
	{"seventeen", "seventeenth"},
	{"eighteen", "eighteenth"},
	{"nineteen", "nineteenth"},
}

var tensWithOrdinals = [][]string{
	nil, nil,
	{"twenty", "twentieth"},
	{"thirty", "thirtieth"},
	{"forty", "fortieth"},
	{"fifty", "fiftieth"},
	{"sixty", "sixtieth"},
	{"seventy", "seventieth"},
	{"eighty", "eightieth"},
	{"ninety", "ninetieth"},
}

func (p *wordNumberParser) skipSep() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == ',') {
		p.pos++
	}
	if p.pos+4 <= len(p.s) && strings.HasPrefix(p.s[p.pos:], "and ") {
		p.pos += 4
	}
}

func (p *wordNumberParser) tryWord(word string) bool {
	return p.tryWordOrOrdinal(word, "")
}

func (p *wordNumberParser) tryWordOrOrdinal(word, ordinal string) bool {
	save := p.pos
	p.skipSep()
	for _, candidate := range []string{word, ordinal} {
		if candidate == "" {
			continue
		}
		if !strings.HasPrefix(p.s[p.pos:], candidate) {
			continue
		}
		after := p.s[p.pos+len(candidate):]
		if r, _ := utf8.DecodeRuneInString(after); after != "" && unicode.IsLetter(r) {
			continue
		}
		p.pos += len(candidate)
		return true
	}
	p.pos = save
	return false
}

func (p *wordNumberParser) parseSub100() (int, bool) {
	save := p.pos
	// Try teens/ones (19 down to 10) - with ordinal forms.
	for i := 19; i >= 10; i-- {
		ord := ""
		if i < len(onesWithOrdinals) {
			ord = onesWithOrdinals[i][1]
		}
		if p.tryWordOrOrdinal(p.ones[i], ord) {
			return i, true
		}
	}
	// Try tens (ninety down to twenty) - with ordinal forms.
	for i := 9; i >= 2; i-- {
		tenOrd := ""
		if tensWithOrdinals[i] != nil {
			tenOrd = tensWithOrdinals[i][1]
		}
		if p.tryWordOrOrdinal(p.tens[i], tenOrd) {
			v := i * 10
			// Check if this was ordinal form (standalone, no ones follow).
			// tenOrd already handled by tryWordOrOrdinal above.
			// Optional dash.
			dashSave := p.pos
			if p.pos < len(p.s) && p.s[p.pos] == '-' {
				p.pos++
			}
			// Try ones (with ordinal forms).
			for j := 9; j >= 1; j-- {
				onesOrd := ""
				if j < len(onesWithOrdinals) {
					onesOrd = onesWithOrdinals[j][1]
				}
				if p.tryWordOrOrdinal(p.ones[j], onesOrd) {
					v += j
					return v, true
				}
			}
			// No ones after dash - backtrack dash.
			p.pos = dashSave
			return v, true
		}
	}
	// Try ones (nine down to one) - with ordinal forms.
	for i := 9; i >= 1; i-- {
		ord := ""
		if i < len(onesWithOrdinals) {
			ord = onesWithOrdinals[i][1]
		}
		if p.tryWordOrOrdinal(p.ones[i], ord) {
			return i, true
		}
	}
	p.pos = save
	return 0, false
}

func (p *wordNumberParser) parseSub1000() (int, bool) {
	save := p.pos
	// Try ones/teens as hundreds.
	for i := 9; i >= 1; i-- {
		if p.tryWord(p.ones[i]) {
			if p.tryWordOrOrdinal("hundred", "hundredth") {
				v := i * 100
				rem, ok := p.parseSub100()
				if ok {
					v += rem
				}
				return v, true
			}
			// Not hundred - backtrack.
			p.pos = save
			break
		}
	}
	// No hundreds - try direct sub100.
	return p.parseSub100()
}

func parseComplexNumber(s string, ones, tens []string) (total, consumed int) {
	p := &wordNumberParser{s: strings.ToLower(s), ones: ones, tens: tens}
	save := p.pos

	// Try "X hundred" style directly (e.g., "nineteen hundred").
	for i := 19; i >= 1; i-- {
		if p.tryWord(p.ones[i]) {
			if p.tryWordOrOrdinal("hundred", "hundredth") {
				total = i * 100
				rem, ok := p.parseSub100()
				if ok {
					total += rem
				}
				return total, p.pos
			}
			p.pos = save
			break
		}
	}

	// Try "X thousand, Y hundred and Z" style.
	thousandPart, ok := p.parseSub1000()
	if ok {
		if p.tryWordOrOrdinal("thousand", "thousandth") {
			total += thousandPart * 1000
			// Parse hundreds part.
			hundredPart, ok2 := p.parseSub1000()
			if ok2 {
				total += hundredPart
			}
			return total, p.pos
		}
		// No "thousand" - just sub1000.
		total = thousandPart
		return total, p.pos
	}

	return 0, 0
}
