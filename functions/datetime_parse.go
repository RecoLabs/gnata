package functions

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/recolabs/gnata/internal/evaluator"
)

// parseWithPicture parses input against a picture string into epoch
// milliseconds, NaN outside the range of a JavaScript Date as in jsonata-js.
// It reports false when the input does not match the picture; defaults for
// unspecified components come from now, the evaluation's timestamp. stop
// reports why evaluation must end, such as a cancelled context.
func parseWithPicture(input, picture string, now time.Time, stop func() error) (millis float64, matched bool, err error) {
	parts, err := parsingPicture(picture)
	if err != nil {
		return 0, false, err
	}
	match := newPictureMatch(parts, input, stop)
	if !match.run() {
		return 0, false, match.err
	}
	components := map[byte]int{}
	for i, part := range parts {
		if part.isMarker {
			components[part.marker.component] = match.values[i]
		}
	}
	if len(components) == 0 || components['Z'] == tzOutOfRange || components['z'] == tzOutOfRange {
		return 0, false, nil
	}
	millis, err = resolveParsedDate(components, now)
	return millis, err == nil, err
}

// maxParsingPictures bounds parsingPictures, so pictures computed from data
// cannot grow it without limit.
const maxParsingPictures = 256

// parsingPictures caches the parts parsingPicture returns, by picture, as
// a $toMillis picture is usually a literal; cached parts are read only.
var (
	parsingPictures     sync.Map
	parsingPictureCount atomic.Int32
)

// parsingPicture returns the parts of a $toMillis picture, checked and with
// their parse widths set.
func parsingPicture(picture string) ([]datePicturePart, error) {
	if parts, ok := parsingPictures.Load(picture); ok {
		return parts.([]datePicturePart), nil
	}
	parts, err := parseDatePicture(picture)
	if err != nil {
		return nil, err
	}
	for i, part := range parts {
		m := part.marker
		if !part.isMarker {
			continue
		}
		if m.component == 'Z' || m.component == 'z' {
			parts[i].marker.tzSeparator = offsetSeparator(m.presentation)
		}
		// jsonata-js matches [C] and [E] as names whatever their presentation.
		if (isNamePresentation(m.presentation) && !strings.ContainsRune("MxFPZzf", rune(m.component))) ||
			m.component == 'C' || m.component == 'E' {
			return nil, &evaluator.JSONataError{
				Code:    "D3133",
				Message: fmt.Sprintf("$toMillis: the 'name' modifier can only be applied to months and days, not %c", m.component),
			}
		}
		if strings.ContainsRune(integerComponents, rune(m.component)) && lacksIntegerFormat(m) {
			return nil, &evaluator.JSONataError{
				Code:    "D3130",
				Message: fmt.Sprintf("$toMillis: unsupported picture %q", m.presentation),
			}
		}
	}
	setParseWidths(parts)
	if parsingPictureCount.Load() < maxParsingPictures && parsingPictureCount.Add(1) <= maxParsingPictures {
		parsingPictures.Store(picture, parts)
	}
	return parts, nil
}

// setParseWidths fixes the digit count of an integer marker directly followed
// by another one, as jsonata-js does: its mandatory digits raised to the
// minimum width, or a year's maximum width. Other integers read every digit,
// whatever their width. Fractional seconds always read every digit.
func setParseWidths(parts []datePicturePart) {
	for i := 1; i < len(parts); i++ {
		prev := &parts[i-1].marker
		if !isIntegerMarker(&parts[i-1]) || !isIntegerMarker(&parts[i]) || prev.component == 'f' {
			continue
		}
		mandatory, _ := decimalPictureDigits(prev.presentation)
		prev.parseWidth = max(mandatory, prev.minWidth)
		if prev.component == 'Y' && prev.maxWidthGiven {
			// A maximum that is 0 or not a number leaves jsonata-js no width.
			prev.parseWidth = max(prev.maxWidth, 0)
		}
	}
}

// integerComponents are the components a non-name presentation formats as an
// integer; fractional seconds also take an integer picture but parse apart.
const integerComponents = "YMDdFWwXxHhms"

func isIntegerMarker(part *datePicturePart) bool {
	c := rune(part.marker.component)
	return part.isMarker && (c == 'f' || strings.ContainsRune(integerComponents, c)) &&
		!isNamePresentation(part.marker.presentation)
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

// isASCIIDigit matches the digits jsonata-js reads in a parsed number or a
// picture width, [0-9].
func isASCIIDigit(c rune) bool {
	return c >= '0' && c <= '9'
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
		// jsonata-js reads any digits whatever the picture, keeping the first three.
		n := leadingDigits(runes, 0)
		if n == 0 {
			return -1, -1
		}
		v := 0
		for w := range 3 {
			v *= 10
			if w < n {
				v += int(runes[w] - '0')
			}
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
		return 0, consumeNameOrNumber(runes, m)
	case 'Z', 'z':
		return parseTZFromInput(runes, m)
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
// specified component after an unspecified one is an error. It returns epoch
// milliseconds, NaN when they fall outside a JavaScript Date's range.
func resolveParsedDate(components map[byte]int, now time.Time) (float64, error) {
	const dateCandidates, timeCandidates = "YXMxWwdD", "PHhmsf"
	if specifiedOnly(components, dateCandidates, "Xxw") || specifiedOnly(components, dateCandidates, "XW") {
		return 0, &evaluator.JSONataError{
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
				return 0, &evaluator.JSONataError{
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
			if hour > math.MaxInt-12 {
				return math.NaN(), nil // saturated, or past a 32-bit int
			}
			hour += 12
		}
	}
	month, day := max(components['M'], 1), components['D']
	if dayOfYear {
		month, day = 1, components['d']
	}
	if !nearDateRange(components['Y'], month, day, hour, components['m'], components['s']) {
		return math.NaN(), nil
	}
	t := time.Date(components['Y'], time.Month(month), day, hour, components['m'], components['s'],
		components['f']*int(time.Millisecond), time.UTC)
	millis := t.UnixMilli()
	if millis < -maxDateMillis || millis > maxDateMillis {
		return math.NaN(), nil
	}
	// jsonata-js applies the offset after Date.UTC's range check.
	if offset, ok := components['Z']; ok {
		millis -= int64(offset) * 1000
	} else if offset, ok := components['z']; ok {
		millis -= int64(offset) * 1000
	}
	return float64(millis), nil
}

// maxDateMillis is the largest epoch millisecond count a JavaScript Date
// holds, 10⁸ days; Date.UTC gives NaN beyond it.
const maxDateMillis = 8.64e15

// nearDateRange reports whether the non-negative date components land within
// about a day of a JavaScript Date's range, estimated in float64 so that
// components too large for time.Date's int arithmetic are rejected first.
func nearDateRange(year, month, day, hour, minute, second int) bool {
	const msPerDay, daysPerYear, slackDays = 86_400_000, 365.2425, 2
	// math.MaxInt marks a component that overflowed (atoiSaturating). On
	// 32-bit targets that is 2³¹−1, which as minutes, about 4,000 years,
	// would pass the estimate below.
	if slices.Contains([]int{year, month, day, hour, minute, second}, math.MaxInt) {
		return false
	}
	days := (float64(year)-1970)*daysPerYear + float64(month-1)*daysPerYear/12 + float64(day-1) +
		(float64(hour)*3600+float64(minute)*60+float64(second))/86_400
	return math.Abs(days) <= maxDateMillis/msPerDay+slackDays
}

// consumeNameOrNumber returns how many runes of a weekday or week marker
// start runes: a name (any run of letters, as jsonata-js accepts) or a number
// in the marker's presentation, 0 when none does.
func consumeNameOrNumber(runes []rune, m dateMarker) int {
	if !isNamePresentation(m.presentation) {
		_, consumed := parseTokenValue(runes, m)
		return max(consumed, 0)
	}
	i := 0
	for i < len(runes) && isASCIILetter(runes[i]) {
		i++
	}
	return i
}

// leadingDigits returns how many digits start runes: exactly width of them
// when width is positive, else all of them; 0 when they do not match.
func leadingDigits(runes []rune, width int) int {
	i := 0
	for i < len(runes) && isASCIIDigit(runes[i]) && (width <= 0 || i < width) {
		i++
	}
	if width > 0 && i < width {
		return 0
	}
	return i
}

// maxTZOffsetSeconds bounds a parsed timezone offset so it fits an int on
// every platform gnata builds for, 32-bit TinyGo included: about 68 years.
const maxTZOffsetSeconds = math.MaxInt32

// tzOutOfRange is the offset parseTZFromInput reads for one over
// maxTZOffsetSeconds, which no in-range offset can equal.
const tzOutOfRange = math.MinInt

// parseTZFromInput reads a [Z] or [z] offset in seconds, matched by scanTZ;
// an offset too large still matches, as tzOutOfRange, so that it does not
// give its digits to the parts after it.
func parseTZFromInput(runes []rune, m dateMarker) (offset, consumed int) {
	sign, hours, mins, n := scanTZ(runes, m)
	if n == 0 || sign == 0 {
		return 0, n
	}
	seconds, ok := offsetSeconds(hours, mins)
	if !ok {
		return tzOutOfRange, n
	}
	return sign * seconds, n
}

// scanTZ matches a [Z] or [z] offset as jsonata-js's regex does: "GMT" first
// for [z], then a sign and hours, then the minutes after the picture's regular
// grouping separator if it has one. Without one, the first two digits are
// hours and any others minutes. [Z] also reads "Z", with sign 0, and ±HHMM
// when its picture has a separator (README known difference #4). It returns
// the runes matched, 0 when the offset does not match.
func scanTZ(runes []rune, m dateMarker) (sign int, hours, mins []rune, consumed int) {
	i := 0
	switch {
	case m.component == 'z':
		if !hasFoldPrefix(runes, "GMT") {
			return 0, nil, nil, 0
		}
		i = 3
	case len(runes) > 0 && runes[0] == 'Z':
		return 0, nil, nil, 1
	}
	if i >= len(runes) || (runes[i] != '+' && runes[i] != '-') {
		return 0, nil, nil, 0
	}
	sign = 1
	if runes[i] == '-' {
		sign = -1
	}
	i++
	n := leadingDigits(runes[i:], 0)
	if n == 0 {
		return 0, nil, nil, 0
	}
	hours = runes[i : i+n]
	i += n
	separator := m.tzSeparator
	minuteDigits := 0
	if separator != 0 && i < len(runes) && runes[i] == separator {
		minuteDigits = leadingDigits(runes[i+1:], 0)
	}
	switch {
	case minuteDigits > 0:
		mins = runes[i+1 : i+1+minuteDigits]
		i += 1 + minuteDigits
	case separator != 0 && (m.component == 'z' || len(hours) != 4):
		return 0, nil, nil, 0
	case len(hours) > 2:
		hours, mins = hours[:2], hours[2:]
	}
	return sign, hours, mins, i
}

// offsetSeparator returns the rune an offset picture puts between hours and
// minutes: its regular grouping separator, read as jsonata-js does after
// dropping a ";" format modifier, or 0.
func offsetSeparator(presentation string) rune {
	if semicolon := strings.LastIndexByte(presentation, ';'); semicolon >= 0 {
		presentation = presentation[:semicolon]
	}
	if mandatory, _ := decimalPictureDigits(presentation); mandatory == 0 {
		return 0
	}
	return regularGroupingSeparator(presentation)
}

// offsetSeconds converts an offset's hour and minute digits to seconds,
// reporting false when it exceeds maxTZOffsetSeconds.
func offsetSeconds(hourDigits, minuteDigits []rune) (int, bool) {
	hours, okHours := offsetUnits(hourDigits, 3600)
	minutes, okMinutes := offsetUnits(minuteDigits, 60)
	if seconds := hours + minutes; okHours && okMinutes && seconds <= maxTZOffsetSeconds {
		return int(seconds), true
	}
	return 0, false
}

// offsetUnits returns digits × seconds, 0 for no digits, reporting false
// above maxTZOffsetSeconds.
func offsetUnits(digits []rune, seconds int64) (int64, bool) {
	if len(digits) == 0 {
		return 0, true
	}
	n, err := strconv.ParseInt(string(digits), 10, 64)
	if err != nil || n > maxTZOffsetSeconds/seconds {
		return 0, false
	}
	return n * seconds, true
}

// parseTokenValue reads an integer marker's value. Month names read their
// maximum width, decimal numbers parseWidth.
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
		return parseMonthName(runes, m.maxWidth)
	case p == "w" || p == "W" || p == "Ww" || p == "ww":
		return parseWordNumber(runes, p)
	case m.ordinal:
		return parseOrdinalNumber(runes, m.parseWidth)
	}
	return parseNumericValue(runes, m.parseWidth)
}

func parseNumericValue(runes []rune, width int) (value, consumed int) {
	i := leadingDigits(runes, width) // jsonata-js accepts no sign on a parsed integer
	if i == 0 {
		return -1, -1
	}
	return atoiSaturating(runes[:i]), i
}

// atoiSaturating reads ASCII digits, saturating at math.MaxInt on overflow;
// for $toMillis, nearDateRange reads that as out of range.
func atoiSaturating(digits []rune) int {
	n, _ := strconv.Atoi(string(digits)) // ASCII digits fail only by overflow
	return n
}

func parseOrdinalNumber(runes []rune, width int) (value, consumed int) {
	n, i := parseNumericValue(runes, width)
	if i <= 0 {
		return -1, -1
	}
	// jsonata-js requires the suffix, though not the one matching the number.
	if i+2 > len(runes) {
		return -1, -1
	}
	switch strings.ToLower(string(runes[i : i+2])) {
	case "st", "nd", "rd", "th":
		return n, i + 2
	}
	return -1, -1
}

// romanValues are the values of the roman numerals parseRoman reads.
var romanValues = map[rune]int{
	'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000,
	'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100, 'd': 500, 'm': 1000,
}

func parseRoman(runes []rune) (value, consumed int) {
	i := 0
	for i < len(runes) {
		if _, ok := romanValues[runes[i]]; !ok {
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
		v := romanValues[runes[j]]
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
		if result > (math.MaxInt-digit)/26 {
			result = math.MaxInt // saturates like atoiSaturating
		} else {
			result = result*26 + digit
		}
		i++
	}
	if i == 0 {
		return -1, -1
	}
	return result, i
}

// parseMonthName reads a month name, or its first maxWidth letters when
// maxWidth is positive.
func parseMonthName(runes []rune, maxWidth int) (month, consumed int) {
	maxLen := max(maxWidth, 0)

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

// maxWordNumberRunes bounds how much input a word number is read from, far
// beyond the longest one gnata parses, so that each try at matching a picture
// costs the same however much input follows.
const maxWordNumberRunes = 1024

func parseWordNumber(runes []rune, _ string) (value, consumed int) {
	n, v := parseWordNumberFromString(string(runes[:min(len(runes), maxWordNumberRunes)]))
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
