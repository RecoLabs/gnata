package functions

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/recolabs/gnata/internal/evaluator"
)

// ── $formatBase ───────────────────────────────────────────────────────────────

func fnFormatBase(args []any, focus any) (any, error) {
	if len(args) < 1 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$formatBase: requires at least 1 argument"}
	}
	numArg := args[0]
	if numArg == nil {
		numArg = focus
	}
	if numArg == nil {
		return nil, nil
	}
	n, nOk := evaluator.ToFloat64(numArg)
	if !nOk {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$formatBase: argument 1 must be a number"}
	}
	base := 10
	if len(args) >= 2 && args[1] != nil {
		bf, bOk := evaluator.ToFloat64(args[1])
		if !bOk {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$formatBase: argument 2 must be a number"}
		}
		base = int(bf)
	}
	if base < 2 || base > 36 {
		return nil, &evaluator.JSONataError{Code: "D3100", Message: "$formatBase: base must be between 2 and 36"}
	}
	return strconv.FormatInt(int64(math.Round(n)), base), nil
}

// ── $formatInteger ────────────────────────────────────────────────────────────

func fnFormatInteger(args []any, _ any) (any, error) {
	if len(args) < 2 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$formatInteger: requires 2 arguments"}
	}
	if args[0] == nil {
		return nil, nil
	}
	n, nOk := evaluator.ToFloat64(args[0])
	if !nOk {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$formatInteger: argument 1 must be a number"}
	}
	picture, ok := args[1].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$formatInteger: argument 2 must be a string"}
	}
	truncated := math.Trunc(n)
	const maxInt64 = float64(1<<63 - 1024)
	if truncated > maxInt64 || truncated < -maxInt64 {
		formatToken, modifier := splitPictureModifier(picture)
		if formatToken == "w" || formatToken == "W" || formatToken == "Ww" {
			return formatBigFloatWords(truncated, formatToken, modifier), nil
		}
		return nil, &evaluator.JSONataError{Code: "D3137", Message: "$formatInteger: number too large for integer formatting"}
	}
	result, err := formatIntegerWithPicture(int64(truncated), picture)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func formatIntegerWithPicture(n int64, picture string) (string, error) {
	formatToken, modifier := splitPictureModifier(picture)

	negative, absN := n < 0, max(n, -n)

	var result string
	switch formatToken {
	case "w":
		if result = intToWords(absN); modifier == "o" {
			result = applyOrdinalWord(result)
		}
	case "W":
		if result = strings.ToUpper(intToWords(absN)); modifier == "o" {
			result = strings.ToUpper(applyOrdinalWord(intToWords(absN)))
		}
	case "Ww":
		if result = toTitleCase(intToWords(absN)); modifier == "o" {
			result = toTitleCase(applyOrdinalWord(intToWords(absN)))
		}
	case "i":
		result = toRoman(absN, false)
	case "I":
		result = toRoman(absN, true)
	default:
		runes := []rune(formatToken)
		if len(runes) == 1 {
			ch := runes[0]
			if ch >= 'a' && ch <= 'z' {
				result = toAlphabetic(absN, 'a')
				break
			}
			if ch >= 'A' && ch <= 'Z' && ch != 'W' && ch != 'I' {
				result = toAlphabetic(absN, 'A')
				break
			}
		}
		if !slices.ContainsFunc(runes, func(c rune) bool {
			return c == '#' || (c >= '0' && c <= '9') || unicodeDigitZero(c) != 0
		}) {
			return "", &evaluator.JSONataError{Code: "D3130", Message: fmt.Sprintf("$formatInteger: unsupported picture string %q", formatToken)}
		}
		var err error
		result, err = formatIntegerDecimal(absN, formatToken)
		if err != nil {
			return "", err
		}
		if modifier == "o" {
			result += ordinalSuffix(absN)
		}
		if negative {
			result = "-" + result
		}
		return result, nil
	}

	if negative {
		result = "-" + result
	}
	return result, nil
}

func splitPictureModifier(picture string) (token, modifier string) {
	if token, modifier, ok := strings.Cut(picture, ";"); ok {
		return token, modifier
	}
	return picture, "c"
}

func formatIntegerDecimal(n int64, picture string) (string, error) {
	runes := []rune(picture)

	zeroRune := '0'
	foundFamily := false
	for _, c := range runes {
		if c >= '0' && c <= '9' {
			if foundFamily && zeroRune != '0' {
				return "", &evaluator.JSONataError{Code: "D3131", Message: "$formatInteger: mixed digit families in picture"}
			}
			zeroRune = '0'
			foundFamily = true
			continue
		}
		if z := unicodeDigitZero(c); z != 0 {
			if foundFamily && zeroRune != z {
				return "", &evaluator.JSONataError{Code: "D3131", Message: "$formatInteger: mixed digit families in picture"}
			}
			if foundFamily && zeroRune == '0' {
				return "", &evaluator.JSONataError{Code: "D3131", Message: "$formatInteger: mixed digit families in picture"}
			}
			zeroRune = z
			foundFamily = true
		}
	}

	mandatoryCount := 0
	totalDigits := 0
	for _, c := range runes {
		if c == '#' {
			totalDigits++
		} else if (c >= '0' && c <= '9') || isUnicodeDigit(c) {
			mandatoryCount++
			totalDigits++
		}
	}
	if totalDigits == 0 {
		return "", &evaluator.JSONataError{Code: "D3131", Message: "$formatInteger: no digit placeholders in picture"}
	}

	type grpInfo struct {
		sep  rune
		posR int
	}
	var grpInfos []grpInfo
	digitFromRight := 0
	for i := len(runes) - 1; i >= 0; i-- {
		c := runes[i]
		if c == '#' || (c >= '0' && c <= '9') || isUnicodeDigit(c) {
			digitFromRight++
		} else if digitFromRight > 0 {
			grpInfos = append(grpInfos, grpInfo{c, digitFromRight})
		}
	}

	digits := strconv.FormatInt(n, 10)
	if len(digits) < mandatoryCount {
		for len(digits) < mandatoryCount {
			digits = "0" + digits
		}
	}

	type grpAnon = struct {
		sep  rune
		posR int
	}
	var grpAnons []grpAnon
	for _, g := range grpInfos {
		grpAnons = append(grpAnons, grpAnon{g.sep, g.posR})
	}

	if len(grpAnons) > 0 && digits != "" {
		digits = applyIntegerGrouping(digits, grpAnons)
	}

	if zeroRune != '0' {
		digits = applyDigitFamilyRune(digits, zeroRune)
	}

	return digits, nil
}

func applyIntegerGrouping(digits string, grps []struct {
	sep  rune
	posR int
},
) string {
	if len(grps) == 0 {
		return digits
	}

	posSet := map[int]rune{}
	for _, g := range grps {
		posSet[g.posR] = g.sep
	}

	allSameSep := true
	if len(grps) > 1 {
		for i := 1; i < len(grps); i++ {
			if grps[i].sep != grps[0].sep {
				allSameSep = false
				break
			}
		}
	}

	isRegular := true
	if len(grps) > 1 {
		gap0 := grps[0].posR
		for i := 1; i < len(grps); i++ {
			if grps[i].posR-grps[i-1].posR != gap0 {
				isRegular = false
				break
			}
		}
	}

	if allSameSep && isRegular && len(grps) > 0 {
		interval := grps[0].posR
		rightmostSep := grps[0].sep
		maxLen := len(digits) + 1
		for pos := interval; pos <= maxLen; pos += interval {
			if _, exists := posSet[pos]; !exists {
				posSet[pos] = rightmostSep
			}
		}
	}

	runes := []rune(digits)
	var result []rune
	for i, ch := range runes {
		posFromRight := len(runes) - i
		if sep, ok := posSet[posFromRight]; ok && i > 0 {
			result = append(result, sep)
		}
		result = append(result, ch)
	}
	return string(result)
}

func applyDigitFamilyRune(s string, zero rune) string {
	var sb strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			sb.WriteRune(zero + c - '0')
		} else {
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

func unicodeDigitZero(c rune) rune {
	for _, z := range unicodeZeros {
		if c >= z && c <= z+9 {
			return z
		}
	}
	return 0
}

var unicodeZeros = []rune{
	'\u0660', '\u06F0', '\u07C0', '\u0966', '\u09E6', '\u0A66', '\u0AE6',
	'\u0B66', '\u0BE6', '\u0C66', '\u0CE6', '\u0D66', '\u0DE6', '\u0E50',
	'\u0ED0', '\u0F20', '\u1040', '\u1090', '\u17E0', '\u1810', '\u1946',
	'\u19D0', '\u1A80', '\u1A90', '\u1B50', '\u1BB0', '\u1C40', '\u1C50',
	'\uA620', '\uA8D0', '\uA900', '\uA9D0', '\uA9F0', '\uAA50', '\uABF0',
	'\uFF10',
}

func isUnicodeDigit(c rune) bool {
	for _, z := range unicodeZeros {
		if c >= z && c <= z+9 {
			return true
		}
	}
	return false
}

func ordinalSuffix(n int64) string {
	abs := max(n, -n)
	mod100 := abs % 100
	mod10 := abs % 10
	if mod100 >= 11 && mod100 <= 13 {
		return "th"
	}
	switch mod10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	default:
		return "th"
	}
}

func applyOrdinalWord(word string) string {
	ordinals := map[string]string{
		"one": "first", "two": "second", "three": "third", "four": "fourth",
		"five": "fifth", "six": "sixth", "seven": "seventh", "eight": "eighth",
		"nine": "ninth", "ten": "tenth", "eleven": "eleventh", "twelve": "twelfth",
		"thirteen": "thirteenth", "fourteen": "fourteenth", "fifteen": "fifteenth",
		"sixteen": "sixteenth", "seventeen": "seventeenth", "eighteen": "eighteenth",
		"nineteen": "nineteenth", "twenty": "twentieth", "thirty": "thirtieth",
		"forty": "fortieth", "fifty": "fiftieth", "sixty": "sixtieth",
		"seventy": "seventieth", "eighty": "eightieth", "ninety": "ninetieth",
		"hundred": "hundredth", "thousand": "thousandth", "million": "millionth",
		"billion": "billionth", "trillion": "trillionth",
	}
	last := ""
	sep := ""
	prefix := ""
	for i := len(word) - 1; i >= 0; i-- {
		if word[i] == ' ' || word[i] == '-' {
			sep = string(word[i])
			prefix = word[:i]
			last = word[i+1:]
			break
		}
		if i == 0 {
			last = word
			prefix = ""
			sep = ""
		}
	}
	if ord, ok := ordinals[last]; ok {
		return prefix + sep + ord
	}
	if strings.HasSuffix(last, "y") {
		return prefix + sep + last[:len(last)-1] + "ieth"
	}
	return prefix + sep + last + "th"
}

func intToWords(n int64) string {
	if n == 0 {
		return "zero"
	}
	if n < 0 {
		return "minus " + intToWords(-n)
	}

	ones := []string{
		"", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
		"ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen",
	}
	tens := []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}

	var belowThousand func(int64) string
	belowThousand = func(n int64) string {
		if n == 0 {
			return ""
		}
		if n < 20 {
			return ones[n]
		}
		if n < 100 {
			if n%10 == 0 {
				return tens[n/10]
			}
			return tens[n/10] + "-" + ones[n%10]
		}
		rem := n % 100
		if rem == 0 {
			return ones[n/100] + " hundred"
		}
		return ones[n/100] + " hundred and " + belowThousand(rem)
	}

	type scale struct {
		name string
		val  int64
	}

	baseScales := []scale{
		{"trillion", 1_000_000_000_000},
		{"billion", 1_000_000_000},
		{"million", 1_000_000},
		{"thousand", 1_000},
	}

	var toWords func(int64) string
	toWords = func(n int64) string {
		if n == 0 {
			return ""
		}
		if n < 1000 {
			return belowThousand(n)
		}

		for _, sc := range baseScales {
			if n < sc.val {
				continue
			}
			q := n / sc.val
			rem := n % sc.val
			qWord := toWords(q)
			result := qWord + " " + sc.name
			if rem > 0 {
				remWord := toWords(rem)
				if rem < 100 {
					result += " and " + remWord
				} else {
					result += ", " + remWord
				}
			}
			return result
		}
		return belowThousand(n)
	}

	result := toWords(n)
	return result
}

func floatToWords(f float64) string {
	if f == 0 {
		return "zero"
	}
	const trillion = 1e12
	trillionCount := 0
	for f >= trillion {
		f /= trillion
		trillionCount++
	}
	var result strings.Builder
	result.WriteString(intToWords(int64(math.Round(f))))
	for range trillionCount {
		result.WriteString(" trillion")
	}
	return result.String()
}

func formatBigFloatWords(f float64, formatToken, modifier string) string {
	negative := f < 0
	if negative {
		f = -f
	}
	words := floatToWords(f)
	switch formatToken {
	case "w":
		if modifier == "o" {
			words = applyOrdinalWord(words)
		}
	case "W":
		words = strings.ToUpper(floatToWords(f))
		if modifier == "o" {
			words = strings.ToUpper(applyOrdinalWord(floatToWords(f)))
		}
	case "Ww":
		words = toTitleCase(floatToWords(f))
		if modifier == "o" {
			words = toTitleCase(applyOrdinalWord(floatToWords(f)))
		}
	}
	if negative {
		words = "minus " + words
	}
	return words
}

func toTitleCase(s string) string {
	lowercase := map[string]bool{"and": true, "or": true, "of": true, "the": true}
	var sb strings.Builder
	capitalizeNext := true
	wordBuf := strings.Builder{}
	flush := func() {
		if wordBuf.Len() == 0 {
			return
		}
		word := wordBuf.String()
		lower := strings.ToLower(word)
		if capitalizeNext || !lowercase[lower] {
			if lower != "" {
				sb.WriteString(strings.ToUpper(lower[:1]) + lower[1:])
			}
		} else {
			sb.WriteString(lower)
		}
		capitalizeNext = false
		wordBuf.Reset()
	}
	for _, c := range s {
		if c == ' ' || c == ',' || c == '-' {
			flush()
			sb.WriteRune(c)
			capitalizeNext = (c == '-')
		} else {
			wordBuf.WriteRune(c)
		}
	}
	flush()
	return sb.String()
}

func toRoman(n int64, upper bool) string {
	if n <= 0 {
		return ""
	}
	vals := []int64{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var sb strings.Builder
	for i, v := range vals {
		for n >= v {
			sb.WriteString(syms[i])
			n -= v
		}
	}
	result := sb.String()
	if !upper {
		return strings.ToLower(result)
	}
	return result
}

func toAlphabetic(n int64, base rune) string {
	if n <= 0 {
		return ""
	}
	var result []rune
	for n > 0 {
		n--
		result = append([]rune{base + rune(n%26)}, result...)
		n /= 26
	}
	return string(result)
}

// ── $parseInteger ─────────────────────────────────────────────────────────────

func fnParseInteger(args []any, _ any) (any, error) {
	if len(args) < 2 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$parseInteger: requires 2 arguments"}
	}
	if args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$parseInteger: argument 1 must be a string"}
	}
	picture, ok := args[1].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$parseInteger: argument 2 must be a string"}
	}
	n, err := parseIntegerWithPicture(s, picture)
	if err != nil {
		jerr := &evaluator.JSONataError{}
		if errors.As(err, &jerr) && jerr.Code == "D3137_FLOAT" {
			f, ferr := strconv.ParseFloat(jerr.Message, 64)
			if ferr == nil {
				return f, nil
			}
		}
		return nil, err
	}
	return float64(n), nil
}

func parseIntegerWithPicture(s, picture string) (int64, error) {
	formatToken, _ := splitPictureModifier(picture)

	switch formatToken {
	case "w", "W", "Ww":
		return wordsToInt(strings.ToLower(s))
	case "i", "I":
		return fromRoman(strings.ToUpper(s))
	default:
		runes := []rune(formatToken)
		if len(runes) == 1 {
			ch := runes[0]
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
				return fromAlphabetic(strings.ToLower(s))
			}
		}
	}

	zeroRune := '0'
	hasMandatory := false
	for _, c := range picture {
		if c >= '0' && c <= '9' {
			zeroRune = '0'
			hasMandatory = true
		} else if z := unicodeDigitZero(c); z != 0 {
			zeroRune = z
			hasMandatory = true
		}
	}
	if !hasMandatory {
		return 0, &evaluator.JSONataError{
			Code:    "D3130",
			Message: "$parseInteger: picture string must contain at least one mandatory digit placeholder",
		}
	}

	var digits strings.Builder
	for _, c := range s {
		switch {
		case c == '-':
			digits.WriteRune(c)
		case c >= '0' && c <= '9':
			digits.WriteRune(c)
		case zeroRune != '0' && c >= zeroRune && c <= zeroRune+9:
			digits.WriteRune('0' + (c - zeroRune))
		}
	}
	cleaned := digits.String()
	n, err := strconv.ParseInt(strings.TrimSpace(cleaned), 10, 64)
	if err != nil {
		return 0, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("$parseInteger: cannot parse %q as integer", s)}
	}
	return n, nil
}

func deOrdinalise(s string) string {
	irregulars := map[string]string{
		"first": "one", "second": "two", "third": "three", "fourth": "four",
		"fifth": "five", "sixth": "six", "seventh": "seven", "eighth": "eight",
		"ninth": "nine", "tenth": "ten", "eleventh": "eleven", "twelfth": "twelve",
		"thirteenth": "thirteen", "fourteenth": "fourteen", "fifteenth": "fifteen",
		"sixteenth": "sixteen", "seventeenth": "seventeen", "eighteenth": "eighteen",
		"nineteenth": "nineteen", "twentieth": "twenty", "thirtieth": "thirty",
		"fortieth": "forty", "fiftieth": "fifty", "sixtieth": "sixty",
		"seventieth": "seventy", "eightieth": "eighty", "ninetieth": "ninety",
		"hundredth": "hundred", "thousandth": "thousand", "millionth": "million",
		"billionth": "billion", "trillionth": "trillion",
		"zeroth": "zero",
	}
	lastIdx := -1
	lastSep := ' '
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ' ' || s[i] == '-' {
			lastIdx = i
			lastSep = rune(s[i])
			break
		}
	}
	var prefix, lastWord string
	if lastIdx >= 0 {
		prefix = s[:lastIdx]
		lastWord = s[lastIdx+1:]
	} else {
		lastWord = s
	}
	if cardinal, ok := irregulars[lastWord]; ok {
		if lastIdx >= 0 {
			return prefix + string(lastSep) + cardinal
		}
		return cardinal
	}
	return s
}

func wordsToFloat(s string) (float64, error) {
	s = deOrdinalise(s)

	wordVals := map[string]float64{
		"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
		"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
		"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19,
		"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50, "sixty": 60,
		"seventy": 70, "eighty": 80, "ninety": 90,
		"hundred":  100,
		"thousand": 1e3, "million": 1e6, "billion": 1e9, "trillion": 1e12,
	}

	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, ",", " ")
	words := strings.Fields(s)

	var total, current float64
	for _, w := range words {
		if w == "and" {
			continue
		}
		val, ok := wordVals[w]
		if !ok {
			return 0, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("$parseInteger: unknown word %q", w)}
		}
		switch {
		case val == 100:
			if current == 0 {
				current = 1
			}
			current *= 100
		case val >= 1000:
			if current == 0 {
				if total == 0 {
					total = 1
				}
				total *= val
			} else {
				total += current * val
				current = 0
			}
		default:
			current += val
		}
	}
	return total + current, nil
}

func wordsToInt(s string) (int64, error) {
	f, err := wordsToFloat(s)
	if err != nil {
		return 0, err
	}
	const maxInt64 float64 = 1<<63 - 1024
	if f > maxInt64 || f < -maxInt64 {
		return 0, &evaluator.JSONataError{Code: "D3137_FLOAT", Message: fmt.Sprintf("%g", f)}
	}
	return int64(f), nil
}

func fromRoman(s string) (int64, error) {
	vals := map[rune]int64{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}
	runes := []rune(s)
	var total int64
	for i, c := range runes {
		v, ok := vals[c]
		if !ok {
			return 0, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("$parseInteger: invalid Roman numeral %q", string(c))}
		}
		if i+1 < len(runes) {
			if next, ok2 := vals[runes[i+1]]; ok2 && next > v {
				total -= v
				continue
			}
		}
		total += v
	}
	return total, nil
}

func fromAlphabetic(s string) (int64, error) {
	var result int64
	for _, c := range s {
		if c < 'a' || c > 'z' {
			return 0, &evaluator.JSONataError{Code: "D3137", Message: fmt.Sprintf("$parseInteger: invalid alphabetic character %q", string(c))}
		}
		result = result*26 + int64(c-'a'+1)
	}
	return result, nil
}
