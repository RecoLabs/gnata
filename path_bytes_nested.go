package gnata

import (
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// gjson finds where an array item ends by scanning past it, so splitting each
// array of a chain nested N deep with Result.Array rescans the arrays inside
// it, which is quadratic in N. stepNestedArray instead walks a nested array's
// raw JSON once, descending into the arrays inside it as it meets them, and
// hands each object it meets to gjson for the field lookup. It relies on the
// JSON being valid, which validJSON checks in one pass; gjson is lenient
// about invalid JSON, so for that stepArray keeps splitting with gjson.
// validJSON is strict JSON, including UTF-8 and control characters in
// strings, which is more than the walk needs: the walk splits strings as
// gjson does whatever their contents. Being strict keeps the argument simple:
// on valid JSON the items are unambiguous.

// stepNestedArray maps a field lookup over the items of the valid JSON array
// raw, which starts at '[', flattening each nested array's result into its
// parent as stepArray does.
func stepNestedArray(step, raw string) (any, bool) {
	var buf [8]rawArrayFrame
	stack := append(buf[:0], rawArrayFrame{})
	pos := 1
	for {
		pos = skipJSONSpace(raw, pos)
		switch c := raw[pos]; c {
		case ',':
			pos++
		case ']':
			pos++
			val, ok := stack[len(stack)-1].result()
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return val, ok
			}
			stack[len(stack)-1].add(val, ok)
		case '[':
			pos++
			stack = append(stack, rawArrayFrame{})
		case '{':
			end := skipJSONValue(raw, pos)
			item := gjson.Parse(raw[pos:end])
			stack[len(stack)-1].add(stepSingle(step, &item))
			pos = end
		default:
			pos = skipJSONValue(raw, pos)
		}
	}
}

// rawArrayFrame is an array stepNestedArray is in: the values the lookup
// found in it so far, and whether it found the field at all.
type rawArrayFrame struct {
	flat  []gjson.Result
	found bool
}

// add adds the lookup's value for one item to the frame, as stepArray does.
func (f *rawArrayFrame) add(val any, ok bool) {
	if !ok {
		return
	}
	f.found = true
	switch inner := val.(type) {
	case gjson.Result:
		if inner.IsArray() {
			f.flat = append(f.flat, inner.Array()...)
		} else {
			f.flat = append(f.flat, inner)
		}
	case []gjson.Result:
		f.flat = append(f.flat, inner...)
	}
}

// result is the lookup's value over the frame's array, as stepArray returns
// it.
func (f *rawArrayFrame) result() (any, bool) {
	switch {
	case len(f.flat) == 0 && f.found:
		return []gjson.Result{}, true
	case len(f.flat) == 0:
		return nil, false
	case len(f.flat) == 1:
		return f.flat[0], true
	default:
		return f.flat, true
	}
}

func skipJSONSpace(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// skipJSONValue returns the end of the valid JSON value starting at s[i].
func skipJSONValue(s string, i int) int {
	switch s[i] {
	case '"':
		return skipJSONString(s, i)
	case '{', '[':
		depth := 0
		for {
			switch s[i] {
			case '"':
				i = skipJSONString(s, i)
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
	}
	for i < len(s) {
		switch s[i] {
		case ',', ']', '}', ' ', '\t', '\n', '\r':
			return i
		}
		i++
	}
	return i
}

// skipJSONString returns the end of the valid JSON string starting at s[i].
func skipJSONString(s string, i int) int {
	for i++; ; i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
}

// validJSON reports whether s is exactly one valid JSON value, surrounded by
// whitespace at most. It keeps the kinds of the open objects and arrays on an
// explicit stack, so any nesting depth is checked in one pass.
func validJSON(s string) bool {
	var buf [64]byte
	open := buf[:0]
	i := skipJSONSpace(s, 0)
	for {
		var (
			end int
			ok  bool
		)
		if end, open, ok = validJSONScalarOrOpen(s, i, open); !ok {
			return false
		}
		i = skipJSONSpace(s, end)
		// After a value: close containers, or continue with ',' in one.
		for {
			if len(open) == 0 {
				return i == len(s)
			}
			if i == len(s) {
				return false
			}
			top := open[len(open)-1]
			switch {
			case s[i] == ',':
				i = skipJSONSpace(s, i+1)
				if top == '{' {
					if i, ok = validJSONKey(s, i); !ok {
						return false
					}
				}
			case top == '{' && s[i] == '}', top == '[' && s[i] == ']':
				open = open[:len(open)-1]
				i = skipJSONSpace(s, i+1)
				continue
			default:
				return false
			}
			break
		}
	}
}

// validJSONScalarOrOpen checks the value starting at s[i]. A scalar is checked
// whole; an object or array is opened, together with its first key or value
// if it is not empty, and its kind pushed onto open. It returns where the
// checked part ends.
func validJSONScalarOrOpen(s string, i int, open []byte) (end int, _ []byte, ok bool) {
	for {
		if i >= len(s) {
			return i, open, false
		}
		switch c := s[i]; {
		case c == '{':
			open = append(open, '{')
			i = skipJSONSpace(s, i+1)
			if i < len(s) && s[i] == '}' {
				open = open[:len(open)-1]
				return i + 1, open, true
			}
			if i, ok = validJSONKey(s, i); !ok {
				return i, open, false
			}
		case c == '[':
			open = append(open, '[')
			i = skipJSONSpace(s, i+1)
			if i < len(s) && s[i] == ']' {
				open = open[:len(open)-1]
				return i + 1, open, true
			}
		case c == '"':
			end, ok = validJSONString(s, i)
			return end, open, ok
		case c == 't':
			end, ok = validJSONLiteral(s, i, "true")
			return end, open, ok
		case c == 'f':
			end, ok = validJSONLiteral(s, i, "false")
			return end, open, ok
		case c == 'n':
			end, ok = validJSONLiteral(s, i, "null")
			return end, open, ok
		case c == '-' || (c >= '0' && c <= '9'):
			end, ok = validJSONNumber(s, i)
			return end, open, ok
		default:
			return i, open, false
		}
	}
}

// validJSONKey checks an object key and its ':' at s[i], returning where the
// value after it starts.
func validJSONKey(s string, i int) (int, bool) {
	if i >= len(s) || s[i] != '"' {
		return i, false
	}
	end, ok := validJSONString(s, i)
	if !ok {
		return end, false
	}
	end = skipJSONSpace(s, end)
	if end >= len(s) || s[end] != ':' {
		return end, false
	}
	return skipJSONSpace(s, end+1), true
}

func validJSONLiteral(s string, i int, lit string) (int, bool) {
	if len(s)-i < len(lit) || s[i:i+len(lit)] != lit {
		return i, false
	}
	return i + len(lit), true
}

// validJSONString checks the string starting at s[i]: no control characters,
// only JSON escapes, and valid UTF-8.
func validJSONString(s string, i int) (int, bool) {
	for i++; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			return i + 1, true
		case c < 0x20:
			return i, false
		case c == '\\':
			if i+1 >= len(s) {
				return i, false
			}
			switch s[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
			case 'u':
				if i+6 > len(s) || !isHex4(s[i+2:i+6]) {
					return i, false
				}
				i += 6
			default:
				return i, false
			}
		case c < utf8.RuneSelf:
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				return i, false
			}
			i += size
		}
	}
	return i, false
}

func isHex4(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// validJSONNumber checks the number starting at s[i] against the RFC 8259
// grammar.
func validJSONNumber(s string, i int) (int, bool) {
	if s[i] == '-' {
		i++
	}
	switch {
	case i >= len(s):
		return i, false
	case s[i] == '0':
		i++
	case s[i] >= '1' && s[i] <= '9':
		i = skipJSONDigits(s, i)
	default:
		return i, false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return i, false
		}
		i = skipJSONDigits(s, i)
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return i, false
		}
		i = skipJSONDigits(s, i)
	}
	return i, true
}

func skipJSONDigits(s string, i int) int {
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}
