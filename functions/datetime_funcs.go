package functions

import (
	"fmt"
	"time"

	"github.com/recolabs/gnata/internal/evaluator"
)

func fnNow(args []any, _ any) (any, error) {
	now := time.Now().UTC()
	if len(args) >= 1 && args[0] != nil {
		picture, ok := args[0].(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$now: picture argument must be a string"}
		}
		tz := time.UTC
		if len(args) >= 2 && args[1] != nil {
			var err error
			tz, err = parseTZ(args[1])
			if err != nil {
				return nil, err
			}
		}
		s, err := formatWithPicture(now.In(tz), picture)
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	return now.Format(time.RFC3339Nano), nil
}

func fnMillis(_ []any, _ any) (any, error) {
	return float64(time.Now().UnixMilli()), nil
}

func fnFromMillis(args []any, focus any) (any, error) {
	if len(args) == 0 {
		// Called with no arguments - use the current context ($) if available.
		if focus != nil {
			args = []any{focus}
		} else {
			return nil, nil
		}
	}
	if args[0] == nil {
		return nil, nil
	}
	ms, ok := evaluator.ToFloat64(args[0])
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$fromMillis: argument must be a number"}
	}
	t := time.UnixMilli(int64(ms)).UTC()

	// Resolve timezone if provided (args[2]).
	tz := time.UTC
	if len(args) >= 3 && args[2] != nil {
		var err error
		tz, err = parseTZ(args[2])
		if err != nil {
			return nil, err
		}
	}

	if len(args) >= 2 && args[1] != nil {
		picture, ok := args[1].(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$fromMillis: picture argument must be a string"}
		}
		s, err := formatWithPicture(t.In(tz), picture)
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	// No picture: use default ISO 8601 format with milliseconds and timezone offset.
	return formatDefaultISO(t.In(tz)), nil
}

func formatDefaultISO(t time.Time) string {
	_, offset := t.Zone()
	ms := t.UnixMilli() % 1000
	if ms < 0 {
		ms += 1000
	}
	base := t.Format("2006-01-02T15:04:05")
	millis := fmt.Sprintf(".%03d", ms)
	if offset == 0 {
		return base + millis + "Z"
	}
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	hours := offset / 3600
	mins := (offset % 3600) / 60
	return fmt.Sprintf("%s%s%s%02d:%02d", base, millis, sign, hours, mins)
}

func fnToMillis(args []any, _ any) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, &evaluator.JSONataError{Code: "T0410", Message: "$toMillis: argument must be a string"}
	}

	if len(args) >= 2 && args[1] != nil {
		// Picture-based parsing — use custom XPath picture parser.
		picture, ok := args[1].(string)
		if !ok {
			return nil, &evaluator.JSONataError{Code: "T0410", Message: "$toMillis: picture argument must be a string"}
		}
		t, ok2, err2 := parseWithPicture(s, picture)
		if err2 != nil {
			return nil, err2
		}
		if !ok2 {
			return nil, nil
		}
		if t.Year() == 0 {
			return nil, nil
		}
		return float64(t.UnixMilli()), nil
	}

	// Try RFC 3339 / ISO 8601 formats (with and without colon in timezone offset).
	isoFormats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999-0700", // ISO 8601 without colon in offset
		"2006-01-02T15:04:05-0700",           // ISO 8601 without fractional seconds, no colon
		"2006-01-02T15:04:05.999999999Z0700", // Z or numeric offset
		"2006-01-02T15:04:05Z0700",
		"2006-01-02T15:04:05",
		"2006-01-02",
		"2006",
	}
	for _, layout := range isoFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.UnixMilli()), nil
		}
	}
	return nil, &evaluator.JSONataError{
		Code:    "D3110",
		Message: fmt.Sprintf("$toMillis: the value '%s' does not match the standard datetime format", s),
	}
}
