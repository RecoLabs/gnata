package functions

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/recolabs/gnata/internal/evaluator"
)

func parseTZ(v any) (*time.Location, error) {
	s, ok := v.(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "timezone must be a string"}
	}
	// First try named timezone
	loc, err := time.LoadLocation(s)
	if err == nil {
		return loc, nil
	}
	// Try numeric offset formats: "+05:30", "-05:00", "+0530", "-0500"
	offset, parseErr := parseNumericTZ(s)
	if parseErr == nil {
		return time.FixedZone(s, offset), nil
	}
	return nil, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("unknown timezone %q: %v", s, err)}
}

func parseNumericTZ(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty timezone")
	}
	sign := 1
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		sign = -1
		s = s[1:]
	}
	// If no sign prefix and all digits, treat as positive offset.
	// Remove colon if present
	s = strings.ReplaceAll(s, ":", "")
	if len(s) != 4 {
		return 0, fmt.Errorf("invalid tz format: %s", s)
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[2:])
	if err1 != nil || err2 != nil {
		return 0, fmt.Errorf("invalid tz digits")
	}
	return sign * (h*3600 + m*60), nil
}

var (
	weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	monthNames   = []string{
		"January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December",
	}
)

// defaultDatePresentations holds the presentation modifier used when a variable
// marker has none. A component missing from it is not a valid specifier.
var defaultDatePresentations = map[byte]string{
	'Y': "1", 'M': "1", 'D': "1", 'd': "1", 'F': "n", 'W': "1", 'w': "1", 'X': "1", 'x': "1", 'H': "1", 'h': "1",
	'P': "n", 'm': "01", 's': "01", 'f': "1", 'Z': "01:01", 'z': "01:01", 'C': "n", 'E': "n",
}

// noWidth marks an unspecified or "*" width in a variable marker.
const noWidth = -1

// dateMarker is a parsed variable marker such as [MNn,*-3] (XPath F&O §9.8.4).
type dateMarker struct {
	component byte
	ordinal   bool
	// tzSeparator is the rune between an offset's hours and minutes that
	// $toMillis expects for [Z] and [z], 0 for none.
	tzSeparator rune
	// maxWidthGiven is set for any maximum but "*", even one that reads as
	// noWidth, like "2-" or "*-x", which jsonata-js's parseInt reads as NaN.
	maxWidthGiven bool
	modifier      string // everything after the component, for the f/Z/z/P formatters
	presentation  string // first presentation modifier, e.g. "Nn", "01" or "w"
	minWidth      int
	maxWidth      int
	parseWidth    int // exact digits $toMillis reads; 0 reads every digit
}

// datePicturePart is a literal or, when isMarker is set, a variable marker.
type datePicturePart struct {
	literal  string
	marker   dateMarker
	isMarker bool
}

func formatWithPicture(t time.Time, picture string) (string, error) {
	parts, err := parseDatePicture(picture)
	if err != nil {
		return "", err
	}
	padding := 0
	for _, part := range parts {
		if part.isMarker {
			padding += min(paddingWidth(part.marker), maxPictureWidth+1)
		}
	}
	if padding > maxPictureWidth {
		return "", &evaluator.JSONataError{
			Code:    "D3010",
			Message: fmt.Sprintf("date/time picture widths exceed a total of %d", maxPictureWidth),
		}
	}
	var sb strings.Builder
	for _, part := range parts {
		if !part.isMarker {
			sb.WriteString(part.literal)
			continue
		}
		s, err := formatMarker(t, part.marker)
		if err != nil {
			return "", err
		}
		sb.WriteString(s)
	}
	return sb.String(), nil
}

// parseDatePicture analyses a whole picture string before anything is
// formatted, so its syntax errors win over formatting errors as in jsonata-js.
func parseDatePicture(picture string) ([]datePicturePart, error) {
	var parts []datePicturePart
	var literal strings.Builder
	runes := []rune(picture)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case ch == '[' && i+1 < len(runes) && runes[i+1] == '[', ch == ']' && i+1 < len(runes) && runes[i+1] == ']':
			literal.WriteRune(ch)
			i++
		case ch == '[':
			end := slices.Index(runes[i+1:], ']')
			if end < 0 {
				return nil, &evaluator.JSONataError{Code: "D3135", Message: "the picture string has an unclosed variable marker '[...'"}
			}
			marker, err := parseDateMarker(strings.Join(strings.Fields(string(runes[i+1:i+1+end])), ""))
			if err != nil {
				return nil, err
			}
			if literal.Len() > 0 {
				parts = append(parts, datePicturePart{literal: literal.String()})
				literal.Reset()
			}
			parts = append(parts, datePicturePart{marker: marker, isMarker: true})
			i += end + 1
		default:
			literal.WriteRune(ch)
		}
	}
	if literal.Len() > 0 {
		parts = append(parts, datePicturePart{literal: literal.String()})
	}
	return parts, nil
}

// parseDateMarker splits a variable marker into its component, presentation
// modifiers and width the way jsonata-js's analyseDateTimePicture does.
func parseDateMarker(marker string) (dateMarker, error) {
	if marker == "" {
		return dateMarker{}, &evaluator.JSONataError{Code: "D3132", Message: "the picture string has an empty variable marker []"}
	}
	m := dateMarker{component: marker[0], modifier: marker[1:], minWidth: noWidth, maxWidth: noWidth}
	defaultPresentation, known := defaultDatePresentations[m.component]
	if !known {
		return dateMarker{}, &evaluator.JSONataError{
			Code:    "D3132",
			Message: fmt.Sprintf("unknown component specifier %q in date/time picture string", marker[:1]),
		}
	}
	presentation := m.modifier
	if comma := strings.LastIndexByte(marker, ','); comma > 0 {
		presentation = marker[1:comma]
		minSpec, maxSpec, hasMax := strings.Cut(marker[comma+1:], "-")
		m.minWidth, m.maxWidth = parseMarkerWidth(minSpec), parseMarkerWidth(maxSpec)
		m.maxWidthGiven = hasMax && maxSpec != "*"
	}
	switch last := presentation[max(len(presentation)-1, 0):]; {
	case presentation == "":
		presentation = defaultPresentation
	case len(presentation) == 1 && strings.ContainsAny(presentation, "otc") && !strings.ContainsRune("fPZzCE", rune(m.component)):
		return dateMarker{}, &evaluator.JSONataError{
			Code:    "D3130",
			Message: fmt.Sprintf("the picture modifier %q has no presentation to apply to", presentation),
		}
	case len(presentation) > 1 && strings.ContainsAny(last, "atco"):
		m.ordinal = last == "o"
		presentation = presentation[:len(presentation)-1]
	}
	m.presentation = presentation
	return m, nil
}

// maxPictureWidth bounds the padding a picture's width modifiers can request
// in total, so a short picture cannot force an arbitrarily large output; it
// matches $pad's limit.
const maxPictureWidth = 10_000

// paddingWidth is the output width a marker's width modifier can force by
// padding: zeros for decimal integers and fractional seconds, nothing for
// names, words, roman numerals, am/pm or timezones.
func paddingWidth(m dateMarker) int {
	switch {
	case m.component == 'f':
		return max(m.minWidth, 0)
	case strings.ContainsRune("PZzCE", rune(m.component)), isNamePresentation(m.presentation):
		return 0
	}
	if mandatory, _ := decimalPictureDigits(m.presentation); mandatory == 0 {
		return 0
	}
	if m.component == 'Y' && m.maxWidth != noWidth {
		return m.maxWidth
	}
	return max(m.minWidth, 0)
}

// parseMarkerWidth reads a width the way JavaScript's parseInt does, taking an
// optional "+" and the leading digits and ignoring the rest; "*" or no digits
// means no width, and a width too large for an int reads as math.MaxInt.
func parseMarkerWidth(spec string) int {
	spec = strings.TrimPrefix(spec, "+")
	end := 0
	for end < len(spec) && spec[end] >= '0' && spec[end] <= '9' {
		end++
	}
	if end == 0 {
		return noWidth
	}
	width, err := strconv.Atoi(spec[:end])
	if err != nil {
		return math.MaxInt
	}
	return width
}

func formatMarker(t time.Time, m dateMarker) (string, error) {
	switch m.component {
	case 'f':
		return formatFracSecond(t.Nanosecond(), m), nil
	case 'Z', 'z':
		return formatTimezone(m.component, m.modifier, t)
	case 'P':
		return formatAMPM(t.Hour(), m.presentation), nil
	case 'C', 'E':
		return "ISO", nil
	}
	value := dateComponentValue(t, m.component)
	if isNamePresentation(m.presentation) {
		return formatDateName(m, value)
	}
	return formatDateInteger(value, m)
}

func dateComponentValue(t time.Time, component byte) int {
	switch component {
	case 'Y':
		return t.Year()
	case 'M':
		return int(t.Month())
	case 'D':
		return t.Day()
	case 'd':
		return t.YearDay()
	case 'F':
		return (int(t.Weekday())+6)%7 + 1
	case 'W':
		_, week := t.ISOWeek()
		return week
	case 'w':
		return weekOfMonth(isoWeekThursday(t))
	case 'X':
		year, _ := t.ISOWeek()
		return year
	case 'x':
		return int(isoWeekThursday(t).Month())
	case 'H':
		return t.Hour()
	case 'h':
		return (t.Hour()+11)%12 + 1
	case 'm':
		return t.Minute()
	default: // 's'
		return t.Second()
	}
}

// formatDateName renders a month or weekday name in the case the presentation
// asks for (n, N or Nn), truncated to the maximum width.
func formatDateName(m dateMarker, value int) (string, error) {
	var name string
	switch m.component {
	case 'M', 'x':
		name = monthNames[value-1]
	case 'F':
		name = weekdayNames[value%7]
	default:
		return "", &evaluator.JSONataError{
			Code:    "D3133",
			Message: fmt.Sprintf("the 'name' modifier can only be applied to months and days, not %c", m.component),
		}
	}
	switch p := m.presentation; {
	case p[0] == 'n':
		name = strings.ToLower(name)
	case len(p) == 1 || p[1] != 'n':
		name = strings.ToUpper(name)
	}
	if m.maxWidth != noWidth && len(name) > m.maxWidth {
		name = name[:m.maxWidth]
	}
	return name, nil
}

// formatDateInteger renders an integer component with $formatInteger, padding
// decimal output to the minimum width. A year with a maximum width, or a
// decimal picture of two or more digits, keeps only that many trailing digits.
func formatDateInteger(value int, m dateMarker) (string, error) {
	picture := m.presentation
	mandatory, optional := decimalPictureDigits(picture)
	yearDigits := m.component == 'Y' && m.maxWidth != noWidth
	if mandatory > 0 && m.minWidth > mandatory && !yearDigits {
		picture = padMandatoryDigits(picture, m.minWidth-mandatory)
		mandatory = m.minWidth
	}
	if m.component == 'Y' {
		digits := m.maxWidth
		if digits != noWidth {
			switch {
			case mandatory == 0 || digits == 0:
			case digits > mandatory:
				picture = padMandatoryDigits(picture, digits-mandatory)
			case digits < mandatory:
				picture = trimMandatoryDigits(picture, mandatory-digits)
			}
		} else if mandatory+optional >= 2 {
			digits = mandatory + optional
		}
		if digits != noWidth {
			value %= pow10(digits)
		}
	}
	if m.ordinal {
		picture += ";o"
	}
	return formatIntegerWithPicture(int64(value), picture)
}

// decimalPictureDigits counts the mandatory digits and optional '#' signs of a
// decimal-digit picture; both are zero for words, roman and alphabetic pictures.
func decimalPictureDigits(picture string) (mandatory, optional int) {
	for _, c := range picture {
		switch {
		case c == '#':
			optional++
		case isPictureDigit(c):
			mandatory++
		}
	}
	return mandatory, optional
}

// pictureZero returns the zero digit of a decimal picture's first digit
// family. A picture mixing families is rejected with D3131 only where it is
// formatted as an integer.
func pictureZero(picture string) rune {
	zero, _ := digitFamilyZero(picture)
	return zero
}

// padMandatoryDigits adds n mandatory zeros to a decimal picture, after any
// leading optional '#' signs, so "#1" padded by one becomes "#01".
func padMandatoryDigits(picture string, n int) string {
	at := strings.IndexFunc(picture, func(c rune) bool { return c != '#' })
	if at < 0 {
		at = len(picture)
	}
	return picture[:at] + strings.Repeat(string(pictureZero(picture)), n) + picture[at:]
}

// trimMandatoryDigits removes the n leftmost mandatory digits of a decimal
// picture with the separators before the next digit, so "0.0.0" trimmed by
// one becomes "0.0". The remaining separators keep their positions.
func trimMandatoryDigits(picture string, n int) string {
	var kept strings.Builder
	keptDigit := false
	for _, c := range picture {
		isDigit := isPictureDigit(c)
		switch {
		case c == '#' || (isDigit && n == 0):
			keptDigit = keptDigit || isDigit
			kept.WriteRune(c)
		case isDigit:
			n--
		case keptDigit:
			kept.WriteRune(c)
		}
	}
	return kept.String()
}

func pow10(n int) int {
	p := 1
	for range min(n, 18) {
		p *= 10
	}
	return p
}

// formatFracSecond renders fractional seconds as XPath does: one digit per
// picture character (three by default), within the marker's width range.
func formatFracSecond(nanosecond int, m dateMarker) string {
	width := 3
	if explicit, _, _ := strings.Cut(m.modifier, ","); explicit != "" {
		width = utf8.RuneCountInString(m.presentation)
	}
	if m.minWidth != noWidth {
		width = max(width, m.minWidth)
	}
	if m.maxWidth > 0 {
		width = min(width, m.maxWidth)
	}
	s := fmt.Sprintf("%09d", nanosecond)
	if width <= 9 {
		s = s[:width]
	} else {
		s += strings.Repeat("0", width-9)
	}
	return applyDigitFamily(s, pictureZero(m.presentation))
}

func formatAMPM(hour int, presentation string) string {
	s := "pm"
	if hour < 12 {
		s = "am"
	}
	if presentation == "N" {
		return strings.ToUpper(s)
	}
	return s
}

func isoWeekThursday(t time.Time) time.Time {
	return t.AddDate(0, 0, 3-(int(t.Weekday())+6)%7)
}

// weekOfMonth returns the week-of-month for a Thursday t.
// Callers always pass isoWeekThursday(t), so t.Day() mod 7 directly
// determines the ordinal Thursday position in the month.
func weekOfMonth(t time.Time) int {
	return (t.Day() + 6) / 7
}

func formatTimezone(component byte, modifier string, t time.Time) (string, error) {
	_, offset := t.Zone()

	useZ := strings.HasSuffix(modifier, "t")
	mod := strings.TrimSuffix(modifier, "t")

	if offset == 0 && useZ {
		return "Z", nil
	}

	if mod == "" {
		mod = "01:01"
	}

	prefix := "+"
	if offset < 0 {
		prefix = "-"
		offset = -offset
	}
	if component == 'z' {
		prefix = "GMT" + prefix
	}

	value, err := formatOffset(int64(offset/3600), int64(offset%3600/60), mod)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", &evaluator.JSONataError{Code: "D3134", Message: fmt.Sprintf("invalid picture component: [%c%s]", component, modifier)}
	}
	return prefix + value, nil
}

// formatOffset formats an unsigned timezone offset as XPath §9.8.4.6 does:
// a picture with regular grouping separators or three or four digits
// formats hhmm as one integer, while one or two digits format the hours and
// append ":mm" only when the minutes are nonzero. Any other picture yields
// "" so the caller can raise D3134.
func formatOffset(hours, mins int64, picture string) (string, error) {
	digits := 0
	for _, c := range picture {
		if isPictureDigit(c) {
			digits++
		}
	}
	switch {
	case regularGroupingSeparator(picture) != 0 || digits == 3 || digits == 4:
		return formatIntegerDecimal(hours*100+mins, picture)
	case digits == 1 || digits == 2:
		formatted, err := formatIntegerDecimal(hours, picture)
		if err != nil || mins == 0 {
			return formatted, err
		}
		return fmt.Sprintf("%s:%02d", formatted, mins), nil
	}
	return "", nil
}

// regularGroupingSeparator returns picture's grouping separator when it is one
// character placed at equal digit intervals counted from the right, else 0.
func regularGroupingSeparator(picture string) rune {
	var sep rune
	var positions []int
	digits := 0
	for _, c := range slices.Backward([]rune(picture)) {
		if c == '#' || isPictureDigit(c) {
			digits++
			continue
		}
		if sep != 0 && c != sep {
			return 0
		}
		sep = c
		positions = append(positions, digits)
	}
	if len(positions) == 0 || positions[0] == 0 {
		return 0
	}
	for i, pos := range positions {
		if pos != (i+1)*positions[0] {
			return 0
		}
	}
	return sep
}
