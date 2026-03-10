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

var validComponents = map[rune]bool{
	'Y': true, 'M': true, 'D': true, 'd': true, 'H': true, 'h': true,
	'm': true, 's': true, 'f': true, 'F': true, 'Z': true, 'z': true,
	'P': true, 'C': true, 'E': true, 'W': true, 'w': true, 'X': true, 'x': true,
}

// Extract picture tokens in order.
type picturePart struct {
	isToken   bool
	token     string // if isToken
	component rune
	modifier  string
	literal   string // if !isToken
}

func parseWithPicture(input, picture string) (time.Time, bool, error) { //nolint:gocyclo,funlen // dispatch
	var parts []picturePart
	runes := []rune(picture)
	i := 0
	for i < len(runes) {
		switch {
		case runes[i] == '[':
			if i+1 < len(runes) && runes[i+1] == '[' {
				parts = append(parts, picturePart{literal: "["})
				i += 2
				continue
			}
			j := i + 1
			for j < len(runes) && runes[j] != ']' {
				j++
			}
			if j >= len(runes) {
				return time.Time{}, false, nil // Unclosed - undefined
			}
			tok := strings.ReplaceAll(string(runes[i+1:j]), " ", "")
			if tok == "" {
				i = j + 1
				continue
			}
			comp := rune(tok[0])
			if !validComponents[comp] {
				return time.Time{}, false, &evaluator.JSONataError{
					Code:    "D3132",
					Message: fmt.Sprintf("$toMillis: unknown picture component '%c'", comp),
				}
			}
			mod := tok[1:]
			// Check for [YN] = invalid modifier → D3133
			if comp == 'Y' && mod == "N" {
				return time.Time{}, false, &evaluator.JSONataError{
					Code:    "D3133",
					Message: "$toMillis: the picture string is not valid: unsupported modifier [YN]",
				}
			}
			parts = append(parts, picturePart{isToken: true, token: tok, component: comp, modifier: mod})
			i = j + 1
		case runes[i] == ']' && i+1 < len(runes) && runes[i+1] == ']':
			parts = append(parts, picturePart{literal: "]"})
			i += 2
		default:
			parts = append(parts, picturePart{literal: string(runes[i])})
			i++
		}
	}

	// Track which components appear in the picture for validation.
	var hasCalY, hasWeekY, hasM, hasD, hasDOY, hasH, hasHour, hasMin, hasSec bool
	for _, p := range parts {
		if !p.isToken {
			continue
		}
		switch p.component {
		case 'Y':
			hasCalY = true
		case 'X':
			hasWeekY = true
		case 'M':
			hasM = true
		case 'D':
			hasD = true
		case 'd':
			hasDOY = true
		case 'H':
			hasH = true
			hasHour = true
		case 'h':
			hasHour = true
		case 'm':
			hasMin = true
		case 's':
			hasSec = true
		}
	}

	// Validate underspecified pictures (D3136):
	// - Year + day but no month → gap
	// - Min or sec but no hour (H or h) → gap
	// - Week-based year (X) without calendar year (Y), month (M), day (D) → can't compute calendar date
	if hasD && !hasM && !hasDOY {
		return time.Time{}, false, &evaluator.JSONataError{
			Code:    "D3136",
			Message: "$toMillis: the date/time picture is underspecified; missing month component",
		}
	}
	if (hasMin || hasSec) && !hasHour && !hasH {
		return time.Time{}, false, &evaluator.JSONataError{
			Code:    "D3136",
			Message: "$toMillis: the date/time picture is underspecified; missing hour component",
		}
	}
	// Week-based components without full calendar date specification.
	if hasWeekY && !hasCalY {
		return time.Time{}, false, &evaluator.JSONataError{
			Code:    "D3136",
			Message: "$toMillis: the date/time picture is underspecified; week-based year requires full calendar date",
		}
	}
	_ = hasCalY // suppress unused warning

	// Parse the input using the parts.
	var year, month, day, hour, minute, second, millisec, dayOfYear, tzOffset, pos int
	var isPM, is12h, hasTZ, hasYear bool
	inputRunes := []rune(input)

	for _, part := range parts {
		if !part.isToken {
			// Consume literal from input.
			for _, lr := range part.literal {
				if pos >= len(inputRunes) {
					return time.Time{}, false, nil
				}
				if unicode.ToLower(inputRunes[pos]) != unicode.ToLower(lr) {
					return time.Time{}, false, nil
				}
				pos++
			}
			continue
		}

		component := part.component
		modifier := part.modifier

		switch component {
		case 'Y', 'X': // Year or ISO week-based year
			hasYear = true
			v, n := parseTokenValue(inputRunes[pos:], modifier)
			if n < 0 {
				return time.Time{}, false, nil
			}
			year = v
			pos += n
		case 'M': // Month
			v, n := parseTokenValue(inputRunes[pos:], modifier)
			if n < 0 {
				return time.Time{}, false, nil
			}
			month = v
			pos += n
		case 'D': // Day of month
			v, n := parseTokenValue(inputRunes[pos:], modifier)
			if n < 0 {
				return time.Time{}, false, nil
			}
			day = v
			pos += n
		case 'd': // Day of year
			v, n := parseTokenValue(inputRunes[pos:], modifier)
			if n < 0 {
				return time.Time{}, false, nil
			}
			dayOfYear = v
			pos += n
		case 'H': // 24-hour
			v, n := parseTokenValue(inputRunes[pos:], modifier)
			if n < 0 {
				return time.Time{}, false, nil
			}
			hour = v
			pos += n
		case 'h': // 12-hour
			is12h = true
			v, n := parseTokenValue(inputRunes[pos:], modifier)
			if n < 0 {
				return time.Time{}, false, nil
			}
			hour = v
			pos += n
		case 'm': // Minutes
			v, n := parseTokenValue(inputRunes[pos:], modifier)
			if n < 0 {
				return time.Time{}, false, nil
			}
			minute = v
			pos += n
		case 's': // Seconds
			v, n := parseTokenValue(inputRunes[pos:], modifier)
			if n < 0 {
				return time.Time{}, false, nil
			}
			second = v
			pos += n
		case 'f': // Fractional seconds
			v, n := parseTokenValue(inputRunes[pos:], modifier)
			if n < 0 {
				return time.Time{}, false, nil
			}
			// Normalize to milliseconds.
			w := n
			if w < 3 {
				for k := w; k < 3; k++ {
					v *= 10
				}
			} else if w > 3 {
				for k := 3; k < w; k++ {
					v /= 10
				}
			}
			millisec = v
			pos += n
		case 'P': // AM/PM
			if pos >= len(inputRunes) {
				return time.Time{}, false, nil
			}
			if pos+2 <= len(inputRunes) {
				part2 := strings.ToLower(string(inputRunes[pos : pos+2]))
				switch part2 {
				case "am":
					isPM = false
					pos += 2
				case "pm":
					isPM = true
					pos += 2
				default:
					return time.Time{}, false, nil
				}
			} else {
				return time.Time{}, false, nil
			}
		case 'F': // Day of week - consume but ignore (not needed for date construction)
			n := consumeNameOrNumber(inputRunes[pos:], modifier)
			if n > 0 {
				pos += n
			}
		case 'Z', 'z': // Timezone - parse and apply
			offset, n := parseTZFromInput(inputRunes[pos:], modifier, component)
			if n > 0 {
				tzOffset = offset
				hasTZ = true
				pos += n
			}
		case 'W', 'w', 'x': // Week-related - consume but ignore for now
			n := consumeNameOrNumber(inputRunes[pos:], modifier)
			if n > 0 {
				pos += n
			}
		}
	}

	// Adjust for 12-hour clock.
	if is12h {
		if isPM && hour != 12 {
			hour += 12
		} else if !isPM && hour == 12 {
			hour = 0
		}
	}

	// If no year seen, use today's date for time-only pictures.
	if !hasYear && year == 0 {
		if hasHour || hasMin {
			now := time.Now().UTC()
			year = now.Year()
			month = int(now.Month())
			day = now.Day()
		}
	}

	if dayOfYear > 0 {
		t := time.Date(year, 1, 1, hour, minute, second, millisec*1e6, time.UTC).AddDate(0, 0, dayOfYear-1)
		if hasTZ {
			// tzOffset is seconds from UTC; subtract to convert local→UTC (same as month/day branch).
			t = t.Add(-time.Duration(tzOffset) * time.Second)
		}
		return t, true, nil
	}

	if month == 0 {
		month = 1
	}
	if day == 0 {
		day = 1
	}

	t := time.Date(year, time.Month(month), day, hour, minute, second, millisec*1e6, time.UTC)
	if hasTZ {
		// tzOffset is seconds from UTC, negative = west (e.g. +02:00 = +7200s from UTC, so we subtract it)
		t = t.Add(-time.Duration(tzOffset) * time.Second)
	}
	return t, true, nil
}

func consumeNameOrNumber(runes []rune, modifier string) int {
	if len(runes) == 0 {
		return 0
	}
	// Try name match (weekday names).
	for _, name := range weekdayNames {
		if modifier == "N" || modifier == "n" || modifier == "Nn" || strings.HasPrefix(modifier, "Nn") || strings.HasPrefix(modifier, "N") {
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
						if v, err2 := strconv.Atoi(rangeParts[1]); err2 == nil {
							maxLen = v
						}
					}
				}
			}
			nameRunes := []rune(name)
			if maxLen > 0 && maxLen < len(nameRunes) {
				// Abbreviated match: compare against the first maxLen runes.
				abbr := string(nameRunes[:maxLen])
				if len(runes) >= maxLen && strings.EqualFold(string(runes[:maxLen]), abbr) {
					return maxLen
				}
			} else if len(runes) >= len(nameRunes) && strings.EqualFold(string(runes[:len(nameRunes)]), name) {
				// Full-name match: covers maxLen==0 and maxLen>=len(nameRunes).
				return len(nameRunes)
			}
		}
	}
	// Try numeric.
	i := 0
	for i < len(runes) && unicode.IsDigit(runes[i]) {
		i++
	}
	return i
}

func parseTZFromInput(runes []rune, _ string, component rune) (offset, consumed int) {
	if len(runes) == 0 {
		return 0, 0
	}
	s := string(runes)

	// Handle "GMT±HH:MM" or "GMT±HH" format (z component).
	if component == 'z' || strings.HasPrefix(s, "GMT") {
		if strings.HasPrefix(s, "GMT") {
			rest := s[3:]
			restRunes := []rune(rest)
			offset, n := parseTZFromInput(restRunes, "", 'Z')
			return offset, 3 + n
		}
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

func parseTokenValue(runes []rune, modifier string) (value, consumed int) {
	if len(runes) == 0 {
		return -1, -1
	}

	// Determine representation type from modifier.
	// Roman numeral: modifier ends with 'I' or 'i'
	if modifier == "I" || modifier == "i" {
		return parseRoman(runes)
	}
	// Alphabetic: modifier 'a' or 'A'
	if modifier == "a" || modifier == "A" {
		return parseAlphabetic(runes, modifier)
	}
	// Month name: modifier starts with 'N' or 'Nn' or 'n'
	if modifier == "N" || modifier == "n" || modifier == "Nn" || strings.HasPrefix(modifier, "Nn") || strings.HasPrefix(modifier, "N") {
		return parseMonthName(runes, modifier)
	}
	// Word-based: modifier starts with 'w' or 'W' (e.g., "w", "W", "wo", "Wo", "Wwo", "wwo")
	if modifier == "w" || modifier == "W" ||
		strings.HasPrefix(modifier, "wo") || strings.HasPrefix(modifier, "Wo") ||
		strings.HasPrefix(modifier, "Ww") || strings.HasPrefix(modifier, "ww") {
		return parseWordNumber(runes, modifier)
	}
	// Ordinal suffix: modifier ends with 'o'
	if strings.HasSuffix(modifier, "o") {
		return parseOrdinalNumber(runes)
	}
	// Default: numeric
	return parseNumericValue(runes, modifier)
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
	i := 0
	sign := 1
	if i < len(runes) && runes[i] == '-' {
		sign = -1
		i++
	}
	start := i
	maxW := modifierFieldWidth(modifier)
	for i < len(runes) && unicode.IsDigit(runes[i]) {
		if maxW > 0 && i-start >= maxW {
			break
		}
		i++
	}
	if i == start {
		return -1, -1
	}
	n, err := strconv.Atoi(string(runes[start:i]))
	if err != nil {
		return -1, -1
	}
	return sign * n, i
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

	// Try matching full month names first, then abbreviated.
	for mi, name := range monthNames {
		if maxLen > 0 && maxLen < len([]rune(name)) {
			// Abbreviated: use first maxLen chars.
			abbr := string([]rune(name)[:maxLen])
			if len(runes) >= maxLen && strings.EqualFold(string(runes[:maxLen]), abbr) {
				return mi + 1, maxLen
			}
		} else {
			nameRunes := []rune(name)
			if len(runes) >= len(nameRunes) && strings.EqualFold(string(runes[:len(nameRunes)]), name) {
				return mi + 1, len(nameRunes)
			}
		}
	}
	return -1, -1
}

func parseWordNumber(runes []rune, _ string) (value, consumed int) {
	// Consume up to the end of a word-number expression.
	// Word numbers end at a non-word char that's not '-' or space.
	s := string(runes)
	n, v := parseWordNumberFromString(s)
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
