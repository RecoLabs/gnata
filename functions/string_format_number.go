package functions

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/recolabs/gnata/internal/evaluator"
)

// ── $formatNumber ─────────────────────────────────────────────────────────────

func fnFormatNumber(args []any, _ any) (any, error) {
	if len(args) < 2 {
		return nil, &evaluator.JSONataError{Code: "D3006", Message: "$formatNumber: requires at least 2 arguments"}
	}
	if args[0] == nil {
		return nil, nil
	}
	n, nOk := evaluator.ToFloat64(args[0])
	if !nOk {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$formatNumber: argument 1 must be a number"}
	}
	picture, ok := args[1].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$formatNumber: argument 2 must be a string"}
	}
	return formatNumberPicture(n, picture, formatNumberOptions(args))
}

// formatNumberOptions returns the optional third argument of $formatNumber.
func formatNumberOptions(args []any) map[string]any {
	if len(args) < 3 || args[2] == nil {
		return nil
	}
	if om, ok := args[2].(*evaluator.OrderedMap); ok {
		return om.ToMap()
	}
	opts, _ := args[2].(map[string]any)
	return opts
}

type fmtChars struct {
	decimalSep  rune
	groupingSep rune
	percent     rune
	perMille    rune
	zeroDigit   rune
	digit       rune
	patternSep  rune
	exponentSep rune
	perMilleStr string
}

func defaultFmtChars() fmtChars {
	return fmtChars{
		decimalSep:  '.',
		groupingSep: ',',
		percent:     '%',
		perMille:    '‰',
		zeroDigit:   '0',
		digit:       '#',
		patternSep:  ';',
		exponentSep: 'e',
		perMilleStr: "‰",
	}
}

func fmtCharsFromOptions(opts map[string]any) fmtChars {
	fc := defaultFmtChars()
	if opts == nil {
		return fc
	}
	if v, ok := opts["decimal-separator"].(string); ok && utf8.RuneCountInString(v) == 1 {
		fc.decimalSep, _ = utf8.DecodeRuneInString(v)
	}
	if v, ok := opts["grouping-separator"].(string); ok && utf8.RuneCountInString(v) == 1 {
		fc.groupingSep, _ = utf8.DecodeRuneInString(v)
	}
	if v, ok := opts["percent"].(string); ok && utf8.RuneCountInString(v) == 1 {
		fc.percent, _ = utf8.DecodeRuneInString(v)
	}
	if v, ok := opts["per-mille"].(string); ok && v != "" {
		fc.perMilleStr = v
		r := []rune(v)
		fc.perMille = r[0]
	}
	if v, ok := opts["zero-digit"].(string); ok && utf8.RuneCountInString(v) == 1 {
		fc.zeroDigit, _ = utf8.DecodeRuneInString(v)
	}
	if v, ok := opts["digit"].(string); ok && utf8.RuneCountInString(v) == 1 {
		fc.digit, _ = utf8.DecodeRuneInString(v)
	}
	if v, ok := opts["pattern-separator"].(string); ok && utf8.RuneCountInString(v) == 1 {
		fc.patternSep, _ = utf8.DecodeRuneInString(v)
	}
	return fc
}

type subPicture struct {
	prefix         string
	suffix         string
	intMandatory   int
	intOptional    int
	fracMandatory  int
	fracOptional   int
	expMandatory   int
	expMinWidth    int
	scale          int
	intGrpPos      []int
	fracGrpPos     []int
	hasDecimal     bool
	hasAnyIntDigit bool
}

func isDigitChar(c rune, fc fmtChars) bool {
	return c >= fc.zeroDigit && c < fc.zeroDigit+10
}

func isActiveChar(c rune, fc fmtChars) bool {
	return isDigitChar(c, fc) || c == fc.digit || c == fc.groupingSep ||
		c == fc.decimalSep || c == fc.exponentSep
}

func containsScaling(s string, fc fmtChars) (int, error) {
	percentCount := 0
	perMilleCount := 0
	for _, c := range s {
		if c == fc.percent {
			percentCount++
		}
		if c == fc.perMille {
			perMilleCount++
		}
	}
	if percentCount > 1 {
		return 0, &evaluator.JSONataError{Code: "D3082", Message: "$formatNumber: picture has more than one percent character"}
	}
	if perMilleCount > 1 {
		return 0, &evaluator.JSONataError{Code: "D3083", Message: "$formatNumber: picture has more than one per-mille character"}
	}
	if percentCount > 0 && perMilleCount > 0 {
		return 0, &evaluator.JSONataError{Code: "D3084", Message: "$formatNumber: picture has both percent and per-mille characters"}
	}
	if percentCount > 0 {
		return 1, nil
	}
	if perMilleCount > 0 {
		return 2, nil
	}
	return 0, nil
}

// scanSubPictureRegion finds the active region (prefix/suffix boundaries)
// and determines the scaling factor. passiveInside reports a passive
// character within the active region, which the caller turns into D3086 once
// it knows no later check fails: jsonata-js runs every picture check in code
// order and reports the last failure.
func scanSubPictureRegion(runes []rune, fc fmtChars, sp *subPicture) (active []rune, passiveInside bool, _ error) {
	start := 0
	for start < len(runes) && !isActiveChar(runes[start], fc) {
		start++
	}
	end := len(runes) - 1
	for end >= 0 && !isActiveChar(runes[end], fc) {
		end--
	}
	if start > end {
		if len(runes) > 0 {
			return nil, false, &evaluator.JSONataError{Code: "D3086", Message: "$formatNumber: picture has only passive characters"}
		}
		return nil, false, &evaluator.JSONataError{Code: "D3085", Message: "$formatNumber: picture has no digit or separator characters"}
	}
	sp.prefix = string(runes[:start])
	sp.suffix = string(runes[end+1:])
	active = runes[start : end+1]
	passiveInside = slices.ContainsFunc(active, func(c rune) bool {
		return !isActiveChar(c, fc) && c != fc.percent && c != fc.perMille
	})
	scale, err := containsScaling(string(runes), fc)
	if err != nil {
		return nil, passiveInside, err
	}
	sp.scale = scale
	return active, passiveInside, nil
}

// misplacedPassiveError is D3086 for a passive character inside the active
// region, unless err is a check jsonata-js runs after it (a higher code).
func misplacedPassiveError(err error) error {
	if je := new(evaluator.JSONataError); errors.As(err, &je) && je.Code > "D3086" {
		return err
	}
	return &evaluator.JSONataError{Code: "D3086", Message: "$formatNumber: invalid character in active picture region"}
}

// locateSubPictureSeparators finds the decimal and exponent positions within
// the active region and validates their combination with the scaling factor.
func locateSubPictureSeparators(active []rune, fc fmtChars, scale int) (decPos, expPos int, _ error) {
	decPos, expPos = -1, -1
	for i, c := range active {
		if c == fc.decimalSep {
			if decPos >= 0 {
				return 0, 0, &evaluator.JSONataError{Code: "D3081", Message: "$formatNumber: picture has more than one decimal separator"}
			}
			decPos = i
		}
		if c == fc.exponentSep && expPos < 0 {
			expPos = i
		}
	}
	if expPos >= 0 && scale != 0 {
		return 0, 0, &evaluator.JSONataError{
			Code:    "D3092",
			Message: "$formatNumber: percent/per-mille cannot appear in picture with exponent separator",
		}
	}
	if expPos >= 0 && slices.Contains(active[expPos:], fc.groupingSep) {
		return 0, 0, &evaluator.JSONataError{Code: "D3093", Message: "$formatNumber: grouping separator cannot appear in exponent"}
	}
	return decPos, expPos, nil
}

func parseSubPicture(pic string, fc fmtChars) (subPicture, error) {
	var sp subPicture
	active, passiveInside, err := scanSubPictureRegion([]rune(pic), fc, &sp)
	if err == nil {
		err = parseActiveRegion(active, fc, &sp)
	}
	if passiveInside {
		return sp, misplacedPassiveError(err)
	}
	return sp, err
}

// parseActiveRegion fills sp from the active region of a sub-picture.
func parseActiveRegion(active []rune, fc fmtChars, sp *subPicture) error {
	decPos, expPos, err := locateSubPictureSeparators(active, fc, sp.scale)
	if err != nil {
		return err
	}

	var intPart, fracPart, expPart []rune
	switch {
	case decPos >= 0 && expPos >= 0:
		if expPos < decPos {
			return &evaluator.JSONataError{Code: "D3085", Message: "$formatNumber: invalid picture"}
		}
		intPart = active[:decPos]
		fracPart = active[decPos+1 : expPos]
		expPart = active[expPos+1:]
	case decPos >= 0:
		intPart = active[:decPos]
		fracPart = active[decPos+1:]
	case expPos >= 0:
		intPart = active[:expPos]
		expPart = active[expPos+1:]
	default:
		intPart = active
	}

	if decPos >= 0 {
		sp.hasDecimal = true
	}

	if err := parseIntPart(intPart, fc, sp); err != nil {
		return err
	}

	hasFracDigit := false
	for _, c := range fracPart {
		if isDigitChar(c, fc) || c == fc.digit {
			hasFracDigit = true
			break
		}
	}
	if expPos >= 0 && (len(expPart) == 0 || slices.ContainsFunc(expPart, func(c rune) bool { return !isDigitChar(c, fc) })) {
		return &evaluator.JSONataError{Code: "D3093", Message: "$formatNumber: exponent part must consist of decimal digits only"}
	}
	if !sp.hasAnyIntDigit && !hasFracDigit && (decPos >= 0 || expPos >= 0) {
		return &evaluator.JSONataError{Code: "D3085", Message: "$formatNumber: picture has no digit placeholders in mantissa"}
	}

	if err := parseFracPart(fracPart, fc, sp); err != nil {
		return err
	}

	for _, c := range expPart {
		if isDigitChar(c, fc) || c == fc.digit {
			sp.expMandatory++
		}
	}
	sp.expMinWidth = sp.expMandatory

	return nil
}

func parseIntPart(intPart []rune, fc fmtChars, sp *subPicture) error {
	lastWasGroup := false
	seenMandatory := false

	for i, c := range intPart {
		switch {
		case isDigitChar(c, fc):
			seenMandatory = true
			lastWasGroup = false
			sp.intMandatory++
			sp.hasAnyIntDigit = true
		case c == fc.digit:
			if seenMandatory {
				return &evaluator.JSONataError{Code: "D3090", Message: "$formatNumber: optional digit cannot follow mandatory digit in integer part"}
			}
			lastWasGroup = false
			sp.intOptional++
			sp.hasAnyIntDigit = true
		case c == fc.groupingSep:
			if lastWasGroup {
				return &evaluator.JSONataError{Code: "D3089", Message: "$formatNumber: adjacent grouping separators in picture"}
			}
			if i == len(intPart)-1 {
				if sp.hasDecimal {
					return &evaluator.JSONataError{Code: "D3087", Message: "$formatNumber: grouping separator adjacent to decimal separator"}
				}
				return &evaluator.JSONataError{Code: "D3088", Message: "$formatNumber: grouping separator at end of integer part"}
			}
			lastWasGroup = true
		case c == fc.percent || c == fc.perMille:
			lastWasGroup = false
		}
	}

	intDigitCountFromRight := 0
	for _, c := range slices.Backward(intPart) {
		if isDigitChar(c, fc) || c == fc.digit {
			intDigitCountFromRight++
		} else if c == fc.groupingSep {
			sp.intGrpPos = append(sp.intGrpPos, intDigitCountFromRight)
		}
	}

	return nil
}

func parseFracPart(fracPart []rune, fc fmtChars, sp *subPicture) error {
	seenOptional := false
	fracDigitCount := 0
	for _, c := range fracPart {
		switch {
		case isDigitChar(c, fc):
			if seenOptional {
				return &evaluator.JSONataError{Code: "D3091", Message: "$formatNumber: mandatory digit cannot follow optional digit in fraction part"}
			}
			fracDigitCount++
			sp.fracMandatory++
		case c == fc.digit:
			seenOptional = true
			fracDigitCount++
			sp.fracOptional++
		case c == fc.groupingSep:
			sp.fracGrpPos = append(sp.fracGrpPos, fracDigitCount)
		}
	}
	return nil
}

func computeIntGroupPositions(grpPos []int, intLen int) map[int]bool {
	if len(grpPos) == 0 {
		return nil
	}
	result := make(map[int]bool)
	primary := grpPos[0]
	allEqual := true
	for i := 1; i < len(grpPos); i++ {
		if grpPos[i]-grpPos[i-1] != primary {
			allEqual = false
			break
		}
	}
	if len(grpPos) == 1 || allEqual {
		for pos := primary; pos < intLen; pos += primary {
			result[pos] = true
		}
	} else {
		for _, pos := range grpPos {
			result[pos] = true
		}
	}
	return result
}

func applyDigitFamily(s string, zeroDigit rune) string {
	if zeroDigit == '0' {
		return s
	}
	runes := []rune(s)
	for i, c := range runes {
		if c >= '0' && c <= '9' {
			runes[i] = zeroDigit + (c - '0')
		}
	}
	return string(runes)
}

// scaleMultiplier returns the factor a percent (scale 1) or per-mille (scale 2)
// sign in a picture applies.
func scaleMultiplier(scale int) int64 {
	switch scale {
	case 1:
		return 100
	case 2:
		return 1000
	}
	return 1
}

func formatNumberPicture(n float64, picture string, opts map[string]any) (string, error) {
	var sp subPicture
	fc, err := numberPicture(&sp, picture, opts, n < 0)
	if err != nil {
		return "", err
	}
	if n < 0 {
		n = -n
	}

	n *= float64(scaleMultiplier(sp.scale))

	var result string
	if sp.expMandatory > 0 {
		result = formatWithExponent(n, &sp, fc)
	} else {
		frac := sp.fracMandatory + sp.fracOptional
		result = formatFixed(strconv.FormatFloat(bankersRound(n, frac), 'f', frac, 64), &sp, fc)
	}
	return sp.prefix + applyDigitFamily(result, fc.zeroDigit) + sp.suffix, nil
}

// numberPicture parses picture into sp, the sub-picture for a negative or
// non-negative number.
func numberPicture(sp *subPicture, picture string, opts map[string]any, negative bool) (fmtChars, error) {
	fc := fmtCharsFromOptions(opts)

	pics := splitOnPatternSep(picture, fc.patternSep)
	if len(pics) > 2 {
		return fc, &evaluator.JSONataError{Code: "D3080", Message: "$formatNumber: picture has more than one pattern separator"}
	}

	posPic, err := parseSubPicture(pics[0], fc)
	if err != nil {
		return fc, err
	}

	*sp = posPic
	if len(pics) == 2 {
		negPic, err := parseSubPicture(pics[1], fc)
		if err != nil {
			return fc, err
		}
		if negative {
			*sp = negPic
		}
	} else if negative {
		sp.prefix = "-" + posPic.prefix
	}
	return fc, nil
}

func splitOnPatternSep(picture string, sep rune) []string {
	var parts []string
	var cur []rune
	for _, c := range picture {
		if c == sep {
			parts = append(parts, string(cur))
			cur = cur[:0]
		} else {
			cur = append(cur, c)
		}
	}
	parts = append(parts, string(cur))
	return parts
}

// formatFixed lays out formatted, the number in plain digits with
// fracMandatory+fracOptional fraction digits, as sp describes.
func formatFixed(formatted string, sp *subPicture, fc fmtChars) string {
	parts := strings.SplitN(formatted, ".", 2)
	intStr := parts[0]
	fracStr := ""
	if len(parts) > 1 {
		fracStr = parts[1]
	}

	// XPath F&O §4.7.5: the integer part keeps only its mandatory digits, so
	// "#.0" renders 0.5 as ".5", unless the picture has no digits at all.
	minInt, minFrac := sp.intMandatory, sp.fracMandatory
	if minInt == 0 && sp.fracMandatory+sp.fracOptional == 0 {
		minInt = 1
	}
	if minInt == 0 && minFrac == 0 {
		minFrac = 1
	}
	intStr = strings.TrimLeft(intStr, "0")
	for len(intStr) < minInt {
		intStr = "0" + intStr
	}

	if len(sp.intGrpPos) > 0 {
		intStr = applyIntGrouping(intStr, sp.intGrpPos, string(fc.groupingSep))
	}

	if len(fracStr) > minFrac {
		trimmed := strings.TrimRight(fracStr, "0")
		if len(trimmed) < minFrac {
			trimmed = fracStr[:minFrac]
		}
		fracStr = trimmed
	}
	for len(fracStr) < minFrac {
		fracStr += "0"
	}

	if len(sp.fracGrpPos) > 0 && fracStr != "" {
		fracStr = applyFracGrouping(fracStr, sp.fracGrpPos, string(fc.groupingSep))
	}

	if fracStr != "" {
		return intStr + string(fc.decimalSep) + fracStr
	}
	return intStr
}

func applyIntGrouping(intStr string, grpPos []int, sep string) string {
	groupMap := computeIntGroupPositions(grpPos, len(intStr))
	if len(groupMap) == 0 {
		return intStr
	}
	runes := []rune(intStr)
	var result []rune
	for i, c := range runes {
		posFromRight := len(runes) - i
		if groupMap[posFromRight] {
			result = append(result, []rune(sep)...)
		}
		result = append(result, c)
	}
	return string(result)
}

func applyFracGrouping(fracStr string, grpPos []int, sep string) string {
	posSet := make(map[int]bool)
	for _, p := range grpPos {
		posSet[p] = true
	}
	runes := []rune(fracStr)
	var result []rune
	for i, c := range runes {
		result = append(result, c)
		if posSet[i+1] && i+1 < len(runes) {
			result = append(result, []rune(sep)...)
		}
	}
	return string(result)
}

// expFracDigits returns the mantissa fraction digits for an exponent picture.
func expFracDigits(sp *subPicture) int {
	fracSig := sp.fracMandatory + sp.fracOptional
	if sp.intMandatory == 0 && fracSig == 0 {
		fracSig += sp.intOptional
	}
	return fracSig
}

func formatWithExponent(n float64, sp *subPicture, fc fmtChars) string {
	N := sp.intMandatory
	fracSig := expFracDigits(sp)

	mantissa, exp := scaleMantissa(n, N)
	mantissa = bankersRound(mantissa, fracSig)

	var threshold float64
	if N > 0 {
		threshold = math.Pow10(N)
	} else {
		threshold = 1.0
	}
	if math.Abs(mantissa) >= threshold {
		mantissa /= 10
		exp++
	}

	return formatExponent(strconv.FormatFloat(math.Abs(mantissa), 'f', fracSig, 64), exp, sp, fc)
}

// scaleMantissa returns mantissa and exp with mantissa × 10^exp = n and
// 10^(intDigits-1) <= mantissa < 10^intDigits, for a non-negative n. It
// scales by ten one step at a time, as jsonata-js does, so the mantissa
// carries the same float64 rounding and rounds to the same digits.
func scaleMantissa(n float64, intDigits int) (mantissa float64, exp int) {
	if n == 0 || math.IsInf(n, 0) || math.IsNaN(n) {
		return n, 0
	}
	maxMantissa, minMantissa := math.Pow10(intDigits), math.Pow10(intDigits-1)
	if math.IsInf(maxMantissa, 0) {
		// Stepping toward a bound float64 cannot hold overflows on the way,
		// so place the shortest decimal form's digits directly.
		digits, exponent, _ := strings.Cut(strconv.FormatFloat(n, 'e', -1, 64), "e")
		logVal, _ := strconv.Atoi(exponent)
		mantissa, _ = strconv.ParseFloat(digits+"e"+strconv.Itoa(intDigits-1), 64)
		return mantissa, logVal - (intDigits - 1)
	}
	for n < minMantissa {
		n *= 10
		exp--
	}
	for n >= maxMantissa {
		n /= 10
		exp++
	}
	return n, exp
}

// formatExponent lays out mantissa, in plain digits with expFracDigits
// fraction digits, and exp as sp describes.
func formatExponent(mantissa string, exp int, sp *subPicture, fc fmtChars) string {
	parts := strings.SplitN(mantissa, ".", 2)
	intStr := parts[0]
	fracStr := ""
	if len(parts) > 1 {
		fracStr = parts[1]
	}

	for len(intStr) < sp.intMandatory {
		intStr = "0" + intStr
	}
	if sp.intMandatory == 0 && sp.intOptional > 0 && (intStr == "" || intStr == "0") {
		intStr = "0"
	}

	if sp.fracOptional > 0 && len(fracStr) > sp.fracMandatory {
		trimmed := strings.TrimRight(fracStr, "0")
		if len(trimmed) < sp.fracMandatory {
			trimmed = fracStr[:sp.fracMandatory]
		}
		fracStr = trimmed
	}

	var mantissaPart string
	if sp.hasAnyIntDigit || sp.intMandatory > 0 {
		if fracStr != "" {
			mantissaPart = intStr + string(fc.decimalSep) + fracStr
		} else {
			mantissaPart = intStr
		}
	} else {
		if fracStr != "" {
			mantissaPart = string(fc.decimalSep) + fracStr
		}
	}

	expSign := ""
	if exp < 0 {
		expSign = "-"
		exp = -exp
	}
	expStr := strconv.Itoa(exp)
	for len(expStr) < sp.expMinWidth {
		expStr = "0" + expStr
	}

	return mantissaPart + string(fc.exponentSep) + expSign + expStr
}
