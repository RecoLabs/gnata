package functions

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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

const errTokenSentinel = "\x00ERR\x00"

const errTokenSentinelD3133 = "\x00D3133\x00"

func formatWithPicture(t time.Time, picture string) (string, error) {
	// Pre-scan: check for unclosed brackets before any other processing.
	runes0 := []rune(picture)
	for i := 0; i < len(runes0); i++ {
		if runes0[i] != '[' {
			continue
		}
		if i+1 < len(runes0) && runes0[i+1] == '[' {
			i++ // skip escaped [[
			continue
		}
		// Find closing ]
		j := i + 1
		for j < len(runes0) && runes0[j] != ']' {
			j++
		}
		if j >= len(runes0) {
			return "", &evaluator.JSONataError{Code: "D3135", Message: "the picture string has an unclosed variable marker '[...'"}
		}
		i = j
	}
	var sb strings.Builder
	runes := []rune(picture)
	i := 0
	for i < len(runes) {
		ch := runes[i]
		if ch == '[' {
			if i+1 < len(runes) && runes[i+1] == '[' {
				sb.WriteRune('[')
				i += 2
				continue
			}
			// Find closing ]
			j := i + 1
			for j < len(runes) && runes[j] != ']' {
				j++
			}
			if j >= len(runes) {
				// Should not happen - pre-scan caught this above.
				break
			}
			token := strings.ReplaceAll(string(runes[i+1:j]), " ", "")
			s := formatToken(t, token)
			if s == errTokenSentinelD3133 {
				return "", &evaluator.JSONataError{
					Code:    "D3133",
					Message: fmt.Sprintf("the picture string is not valid: unsupported modifier in [%s]", token),
				}
			}
			if s == errTokenSentinel {
				return "", &evaluator.JSONataError{Code: "D3134", Message: fmt.Sprintf("invalid picture component: [%s]", token)}
			}
			sb.WriteString(s)
			i = j + 1
			continue
		}
		if ch == ']' && i+1 < len(runes) && runes[i+1] == ']' {
			sb.WriteRune(']')
			i += 2
			continue
		}
		sb.WriteRune(ch)
		i++
	}
	return sb.String(), nil
}

func formatToken(t time.Time, token string) string {
	if token == "" {
		return ""
	}

	switch component, modifier := token[0], token[1:]; component {
	case 'Y':
		return formatYearComponent(t.Year(), modifier)
	case 'M':
		return formatMonthToken(t.Month(), modifier)
	case 'D':
		return formatDayComponent(t.Day(), modifier)
	case 'H':
		return formatInteger(t.Hour(), modifier)
	case 'h':
		return formatInteger((t.Hour()+11)%12+1, modifier)
	case 'm', 's':
		if modifier == "" {
			modifier = "01"
		}
		if component == 's' {
			return formatInteger(t.Second(), modifier)
		}
		return formatInteger(t.Minute(), modifier)
	case 'f':
		return formatFracSecond(t.Nanosecond(), modifier)
	case 'F':
		return formatWeekdayToken(t.Weekday(), modifier)
	case 'Z', 'z':
		return formatTimezone(component, modifier, t)
	case 'P':
		return formatAMPM(t.Hour(), modifier)
	case 'E', 'C':
		return "ISO"
	case 'd':
		return formatDayOfYearToken(t.YearDay(), modifier)
	case 'W':
		_, w := t.ISOWeek()
		return formatInteger(w, modifier)
	case 'X':
		y, _ := t.ISOWeek()
		return formatYearToken(y, modifier)
	case 'w':
		return formatInteger(weekOfMonth(isoWeekThursday(t)), modifier)
	case 'x':
		return formatISOWeekMonth(isoWeekThursday(t).Month(), modifier)
	}
	return "[" + token + "]"
}

func formatYearComponent(y int, modifier string) string {
	switch modifier {
	case "I", "i":
		return toRoman(int64(y), modifier == "I")
	case "w":
		return intToWords(int64(y))
	case "W":
		return strings.ToUpper(intToWords(int64(y)))
	case "a", "A":
		return toAlphabetic(int64(y), rune(modifier[0]))
	case "N":
		return errTokenSentinelD3133
	default:
		return formatYearToken(y, modifier)
	}
}

func formatDayComponent(day int, modifier string) string {
	switch modifier {
	case "I", "i":
		return toRoman(int64(day), modifier == "I")
	case "a", "A":
		return toAlphabetic(int64(day), rune(modifier[0]))
	case "wo":
		return intToWordsOrdinal(int64(day))
	case "Wo":
		return strings.ToUpper(intToWordsOrdinal(int64(day)))
	case "w":
		return intToWords(int64(day))
	case "W":
		return strings.ToUpper(intToWords(int64(day)))
	default:
		return formatDayToken(day, modifier)
	}
}

func formatFracSecond(nanosecond int, modifier string) string {
	width := 3
	if modifier != "" {
		width = len(modifier)
	}
	s := fmt.Sprintf("%09d", nanosecond)
	if width <= 9 {
		return s[:width]
	}
	return s + strings.Repeat("0", width-9)
}

func formatWeekdayToken(wd time.Weekday, modifier string) string {
	switch {
	case modifier == "" || modifier == "n":
		return strings.ToLower(weekdayNames[wd])
	case strings.HasPrefix(modifier, "Nn"):
		name := weekdayNames[wd]
		if _, suffix, ok := strings.Cut(modifier, ","); ok {
			var width int
			if before, _, ok := strings.Cut(suffix, "-"); ok {
				width, _ = strconv.Atoi(before)
			} else {
				width, _ = strconv.Atoi(suffix)
			}
			if width > 0 && len(name) > width {
				return name[:width]
			}
		}
		return name
	case modifier == "N":
		return strings.ToUpper(weekdayNames[wd])
	default:
		return strconv.Itoa(int(wd+6)%7 + 1)
	}
}

func formatAMPM(hour int, modifier string) string {
	s := "pm"
	if hour < 12 {
		s = "am"
	}
	if modifier == "N" {
		return strings.ToUpper(s)
	}
	return s
}

func formatDayOfYearToken(dayOfYear int, modifier string) string {
	switch modifier {
	case "wo":
		return intToWordsOrdinal(int64(dayOfYear))
	case "Wo":
		return strings.ToUpper(intToWordsOrdinal(int64(dayOfYear)))
	case "w":
		return intToWords(int64(dayOfYear))
	case "W":
		return strings.ToUpper(intToWords(int64(dayOfYear)))
	default:
		if baseM, ok := strings.CutSuffix(modifier, "o"); ok {
			return formatInteger(dayOfYear, baseM) + ordinalSuffix(int64(dayOfYear))
		}
		return formatInteger(dayOfYear, modifier)
	}
}

func formatISOWeekMonth(m time.Month, modifier string) string {
	switch modifier {
	case "Nn", "n":
		return monthNames[m-1]
	case "N":
		return strings.ToUpper(monthNames[m-1])
	default:
		return strconv.Itoa(int(m))
	}
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

func intToWordsOrdinal(n int64) string {
	return applyOrdinalWord(intToWords(n))
}

func formatYearToken(y int, modifier string) string {
	// [Y,n] or [Y,n-n]: modifier starts with "," → truncation to last n digits
	if strings.HasPrefix(modifier, ",") {
		return truncateYear(y, modifier[1:])
	}

	// [Y0001,n], [Y0001,n-n], [Y9,999,*]: modifier has comma inside
	if prefix, suffix, ok := strings.Cut(modifier, ","); ok {
		// Check for max-width truncation: "0001,2-2" → max=2
		if _, maxStr, ok := strings.Cut(suffix, "-"); ok {
			// min-max width: "n-m" → truncate to last m digits
			if maxWidth, _ := strconv.Atoi(strings.TrimLeft(maxStr, "#* ")); maxWidth > 0 {
				s := formatInteger(y, prefix)
				if len(s) > maxWidth {
					s = s[len(s)-maxWidth:]
				}
				return s
			}
		}

		// "9,999,*" style grouping → use commas
		if strings.Contains(prefix, "9") || strings.Contains(suffix, "9") {
			return formatIntegerWithGrouping(y)
		}

		// "0001,2" with no dash → show full year using primary format (no truncation)
		return formatInteger(y, prefix)
	}
	return formatInteger(y, modifier)
}

func truncateYear(y int, widthSpec string) string {
	var width int
	if before, _, ok := strings.Cut(widthSpec, "-"); ok {
		width, _ = strconv.Atoi(before)
	} else {
		width, _ = strconv.Atoi(widthSpec)
	}
	if width <= 0 {
		return strconv.Itoa(y)
	}
	s := strconv.Itoa(y)
	if len(s) > width {
		s = s[len(s)-width:]
	}
	return s
}

func formatIntegerWithGrouping(v int) string {
	s := strconv.Itoa(v)
	if len(s) <= 3 {
		return s
	}
	var result []rune
	rs := []rune(s)
	for i, r := range rs {
		if i > 0 && (len(rs)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, r)
	}
	return string(result)
}

func formatMonthToken(m time.Month, modifier string) string {
	switch {
	case strings.HasPrefix(modifier, "Nn"):
		name := monthNames[m-1]
		if _, suffix, ok := strings.Cut(modifier, ","); ok {
			var width int
			if before, _, ok := strings.Cut(suffix, "-"); ok {
				width, _ = strconv.Atoi(before)
			} else {
				width, _ = strconv.Atoi(suffix)
			}
			if width > 0 && len(name) > width {
				return name[:width]
			}
		}
		return name
	case modifier == "N":
		return strings.ToUpper(monthNames[m-1])
	case modifier == "a" || modifier == "A":
		return toAlphabetic(int64(m), rune(modifier[0]))
	case modifier == "I" || modifier == "i":
		return toRoman(int64(m), modifier == "I")
	default:
		return formatNumericWithMinWidth(int(m), modifier)
	}
}

func formatNumericWithMinWidth(v int, modifier string) string {
	// Extract primary modifier and width modifier
	minWidth := 0
	if primary, suffix, ok := strings.Cut(modifier, ","); ok {
		modifier = primary
		if before, _, ok := strings.Cut(suffix, "-"); ok {
			minWidth, _ = strconv.Atoi(before)
		} else {
			minWidth, _ = strconv.Atoi(suffix)
		}
	}
	s := formatInteger(v, modifier)
	if minWidth > 0 && len(s) < minWidth {
		s = strings.Repeat("0", minWidth-len(s)) + s
	}
	return s
}

func formatDayToken(d int, modifier string) string {
	// Check for ordinal suffix 'o'
	isOrdinal := strings.HasSuffix(modifier, "o")
	baseModifier := modifier
	if isOrdinal {
		baseModifier = strings.TrimSuffix(modifier, "o")
	}
	var s string
	if strings.Contains(baseModifier, ",") {
		s = formatNumericWithMinWidth(d, baseModifier)
	} else {
		s = formatInteger(d, baseModifier)
	}
	if isOrdinal {
		s += ordinalSuffix(int64(d))
	}
	return s
}

func formatTimezone(component byte, modifier string, t time.Time) string {
	_, offset := t.Zone()

	useZ := strings.HasSuffix(modifier, "t")
	mod := strings.TrimSuffix(modifier, "t")

	if offset == 0 && useZ {
		return "Z"
	}

	prefix := ""
	if component == 'z' {
		prefix = "GMT"
	}

	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	hours := offset / 3600
	mins := (offset % 3600) / 60

	switch mod {
	case "0":
		// Minimal: no leading zeros, skip minutes if zero
		if mins == 0 {
			return prefix + sign + strconv.Itoa(hours)
		}
		return prefix + fmt.Sprintf("%s%d:%02d", sign, hours, mins)
	case "0101":
		// No colon, fixed width hours+minutes
		return prefix + fmt.Sprintf("%s%02d%02d", sign, hours, mins)
	case "01:01":
		// With colon
		return prefix + fmt.Sprintf("%s%02d:%02d", sign, hours, mins)
	case "", "Z":
		// Default: with colon
		return prefix + fmt.Sprintf("%s%02d:%02d", sign, hours, mins)
	case "010101", "01:01:01":
		// 6 digits (hours+mins+secs) - not valid for timezone
		return errTokenSentinel
	default:
		// Fallback: with colon
		return prefix + fmt.Sprintf("%s%02d:%02d", sign, hours, mins)
	}
}

func formatInteger(v int, modifier string) string {
	// Remove whitespace from modifier
	modifier = strings.TrimSpace(modifier)

	if modifier == "" || modifier == "1" {
		return strconv.Itoa(v)
	}

	// Check for grouping separator (e.g., "9,999,*" or "9,999")
	if strings.ContainsAny(modifier, ",") {
		// Separate the sign from the digits so the comma-insertion loop
		// only operates on digit characters and cannot insert a comma
		// immediately after the '-' sign for negative numbers.
		s := strconv.Itoa(v)
		neg := s != "" && s[0] == '-'
		digits := s
		if neg {
			digits = s[1:]
		}
		if len(digits) > 3 {
			var result []rune
			rs := []rune(digits)
			for i, r := range rs {
				if i > 0 && (len(rs)-i)%3 == 0 {
					result = append(result, ',')
				}
				result = append(result, r)
			}
			formatted := string(result)
			if neg {
				return "-" + formatted
			}
			return formatted
		}
		return s
	}

	// Check for ordinal (#)
	if strings.HasPrefix(modifier, "#") {
		return strconv.Itoa(v)
	}

	// Count digit characters in the modifier to determine minimum width.
	// e.g. "0" → width 1 (no padding), "01" → width 2, "001" → width 3.
	if modifier != "" && modifier[0] == '0' {
		digitCount := 0
		for _, r := range modifier {
			if r >= '0' && r <= '9' {
				digitCount++
			} else {
				break
			}
		}
		if digitCount > 0 {
			return fmt.Sprintf("%0*d", digitCount, v)
		}
	}

	// Default: plain integer
	return strconv.Itoa(v)
}
