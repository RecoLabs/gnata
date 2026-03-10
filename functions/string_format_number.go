package functions

import (
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
	var opts map[string]any
	if len(args) >= 3 && args[2] != nil {
		if om, ok := args[2].(*evaluator.OrderedMap); ok {
			opts = om.ToMap()
		} else {
			opts, _ = args[2].(map[string]any)
		}
	}
	return formatNumberPicture(n, picture, opts)
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

// scanSubPictureRegion finds the active region (prefix/suffix boundaries),
// validates its characters, and determines the scaling factor.
func scanSubPictureRegion(runes []rune, fc fmtChars, sp *subPicture) (active []rune, _ error) {
	start := 0
	for start < len(runes) && !isActiveChar(runes[start], fc) {
		start++
	}
	end := len(runes) - 1
	for end >= 0 && !isActiveChar(runes[end], fc) {
		end--
	}
	if start > end {
		return nil, &evaluator.JSONataError{Code: "D3085", Message: "$formatNumber: picture has no digit or separator characters"}
	}
	sp.prefix = string(runes[:start])
	sp.suffix = string(runes[end+1:])
	active = runes[start : end+1]
	for _, c := range active {
		if !isActiveChar(c, fc) && c != fc.percent && c != fc.perMille {
			return nil, &evaluator.JSONataError{Code: "D3086", Message: "$formatNumber: invalid character in active picture region"}
		}
	}
	scale, err := containsScaling(string(runes), fc)
	if err != nil {
		return nil, err
	}
	sp.scale = scale
	return active, nil
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
	runes := []rune(pic)
	sp := subPicture{}

	active, err := scanSubPictureRegion(runes, fc, &sp)
	if err != nil {
		return sp, err
	}

	decPos, expPos, err := locateSubPictureSeparators(active, fc, sp.scale)
	if err != nil {
		return sp, err
	}

	var intPart, fracPart, expPart []rune
	switch {
	case decPos >= 0 && expPos >= 0:
		if expPos < decPos {
			return sp, &evaluator.JSONataError{Code: "D3085", Message: "$formatNumber: invalid picture"}
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

	if err := parseIntPart(intPart, fc, &sp); err != nil {
		return sp, err
	}

	hasFracDigit := false
	for _, c := range fracPart {
		if isDigitChar(c, fc) || c == fc.digit {
			hasFracDigit = true
			break
		}
	}
	if !sp.hasAnyIntDigit && !hasFracDigit && (decPos >= 0 || expPos >= 0) {
		return sp, &evaluator.JSONataError{Code: "D3085", Message: "$formatNumber: picture has no digit placeholders in mantissa"}
	}

	if err := parseFracPart(fracPart, fc, &sp); err != nil {
		return sp, err
	}

	for _, c := range expPart {
		if isDigitChar(c, fc) || c == fc.digit {
			sp.expMandatory++
		}
	}
	sp.expMinWidth = sp.expMandatory

	return sp, nil
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
	for i := len(intPart) - 1; i >= 0; i-- {
		c := intPart[i]
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

func formatNumberPicture(n float64, picture string, opts map[string]any) (string, error) {
	fc := fmtCharsFromOptions(opts)

	pics := splitOnPatternSep(picture, fc.patternSep)
	if len(pics) > 2 {
		return "", &evaluator.JSONataError{Code: "D3080", Message: "$formatNumber: picture has more than one pattern separator"}
	}

	posPic, err := parseSubPicture(pics[0], fc)
	if err != nil {
		return "", err
	}

	var negPic subPicture
	if len(pics) == 2 {
		negPic, err = parseSubPicture(pics[1], fc)
		if err != nil {
			return "", err
		}
	} else {
		negPic = posPic
		negPic.prefix = "-" + posPic.prefix
	}

	negative := n < 0
	sp := posPic
	if negative {
		sp = negPic
		n = -n
	}

	switch sp.scale {
	case 1:
		n *= 100
	case 2:
		n *= 1000
	}

	var result string
	if sp.expMandatory > 0 {
		result = formatWithExponent(n, &sp, fc)
	} else {
		result = formatFixed(n, &sp, fc)
	}

	result = applyDigitFamily(result, fc.zeroDigit)
	return sp.prefix + result + sp.suffix, nil
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

func formatFixed(n float64, sp *subPicture, fc fmtChars) string {
	totalFracDigits := sp.fracMandatory + sp.fracOptional
	formatted := strconv.FormatFloat(n, 'f', totalFracDigits, 64)
	parts := strings.SplitN(formatted, ".", 2)
	intStr := parts[0]
	fracStr := ""
	if len(parts) > 1 {
		fracStr = parts[1]
	}

	minInt := sp.intMandatory
	if minInt < 1 && !sp.hasDecimal && !sp.hasAnyIntDigit {
		minInt = 1
	} else if minInt < 1 && sp.intOptional > 0 {
		minInt = 1
	}
	for len(intStr) < minInt {
		intStr = "0" + intStr
	}

	if len(sp.intGrpPos) > 0 {
		intStr = applyIntGrouping(intStr, sp.intGrpPos, string(fc.groupingSep))
	}

	if sp.fracOptional > 0 && len(fracStr) > sp.fracMandatory {
		trimmed := strings.TrimRight(fracStr, "0")
		if len(trimmed) < sp.fracMandatory {
			trimmed = fracStr[:sp.fracMandatory]
		}
		fracStr = trimmed
	}
	for len(fracStr) < sp.fracMandatory {
		fracStr += "0"
	}

	if len(sp.fracGrpPos) > 0 && fracStr != "" {
		fracStr = applyFracGrouping(fracStr, sp.fracGrpPos, string(fc.groupingSep))
	}

	if fracStr != "" || sp.hasDecimal {
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

func formatWithExponent(n float64, sp *subPicture, fc fmtChars) string {
	N := sp.intMandatory
	fracSig := sp.fracMandatory + sp.fracOptional
	if N == 0 && sp.fracMandatory == 0 && sp.fracOptional == 0 {
		fracSig += sp.intOptional
	}

	exp := 0
	if n != 0 {
		logVal := math.Floor(math.Log10(math.Abs(n)))
		if N > 0 {
			exp = int(logVal) - (N - 1)
		} else {
			exp = int(logVal) + 1
		}
	}
	mantissa := n / math.Pow10(exp)

	factor := math.Pow10(fracSig)
	mantissa = math.Round(mantissa*factor) / factor

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

	mantissaStr := strconv.FormatFloat(math.Abs(mantissa), 'f', fracSig, 64)
	parts := strings.SplitN(mantissaStr, ".", 2)
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
		if fracStr != "" || sp.hasDecimal {
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
