package evaluator

import (
	"encoding/json"
	"unicode/utf16"
	"unicode/utf8"
)

// maxDecodeDepth mirrors encoding/json's nesting limit.
const maxDecodeDepth = 10000

// fastDecodeJSON is a single-pass decoder producing exactly what
// legacyDecodeJSON produces for valid input. It returns ok=false for anything
// it rejects so the caller can fall back to the legacy decoder, which then
// yields the canonical error.
//
// Every string is its own allocation rather than a slice of the input, so a
// value kept by the caller (or by a cache) never pins the whole payload.
func fastDecodeJSON(b []byte, freeze bool) (any, bool) {
	d := fastDecoder{src: b, freeze: freeze}
	d.skipWS()
	return d.value(0)
}

type kv struct {
	k string
	v any
}

type fastDecoder struct {
	src     []byte
	pos     int
	freeze  bool
	pairs   []kv
	scratch []byte
}

func (d *fastDecoder) skipWS() {
	for d.pos < len(d.src) {
		switch d.src[d.pos] {
		case ' ', '\t', '\n', '\r':
			d.pos++
		default:
			return
		}
	}
}

func (d *fastDecoder) value(depth int) (any, bool) {
	if d.pos >= len(d.src) {
		return nil, false
	}
	switch c := d.src[d.pos]; {
	case c == '{':
		return d.object(depth + 1)
	case c == '[':
		return d.array(depth + 1)
	case c == '"':
		s, ok := d.str()
		return s, ok
	case c == 't':
		return true, d.lit("true")
	case c == 'f':
		return false, d.lit("false")
	case c == 'n':
		return Null, d.lit("null")
	case c == '-' || (c >= '0' && c <= '9'):
		end, ok := scanJSONNumber(d.src, d.pos)
		if !ok {
			return nil, false
		}
		n := json.Number(d.src[d.pos:end])
		d.pos = end
		return n, true
	}
	return nil, false
}

func (d *fastDecoder) lit(s string) bool {
	if len(d.src)-d.pos < len(s) || string(d.src[d.pos:d.pos+len(s)]) != s {
		return false
	}
	d.pos += len(s)
	return true
}

func (d *fastDecoder) object(depth int) (any, bool) {
	if depth > maxDecodeDepth {
		return nil, false
	}
	d.pos++ // '{'
	base := len(d.pairs)
	d.skipWS()
	if d.pos < len(d.src) && d.src[d.pos] == '}' {
		d.pos++
		return d.newMap(nil), true
	}
	for {
		if d.pos >= len(d.src) || d.src[d.pos] != '"' {
			return nil, false
		}
		k, ok := d.str()
		if !ok {
			return nil, false
		}
		d.skipWS()
		if d.pos >= len(d.src) || d.src[d.pos] != ':' {
			return nil, false
		}
		d.pos++
		d.skipWS()
		v, ok := d.value(depth)
		if !ok {
			return nil, false
		}
		d.pairs = append(d.pairs, kv{k, v})
		d.skipWS()
		if d.pos >= len(d.src) {
			return nil, false
		}
		if d.src[d.pos] == ',' {
			d.pos++
			d.skipWS()
			continue
		}
		if d.src[d.pos] != '}' {
			return nil, false
		}
		d.pos++
		break
	}
	m := d.newMap(d.pairs[base:])
	clear(d.pairs[base:])
	d.pairs = d.pairs[:base]
	return m, true
}

func (d *fastDecoder) newMap(pairs []kv) *OrderedMap {
	m := NewOrderedMapWithCapacity(len(pairs))
	for _, p := range pairs {
		m.Set(p.k, p.v)
	}
	if d.freeze {
		m.freeze()
	}
	return m
}

func (d *fastDecoder) array(depth int) (any, bool) {
	if depth > maxDecodeDepth {
		return nil, false
	}
	d.pos++ // '['
	d.skipWS()
	arr := []any{}
	if d.pos < len(d.src) && d.src[d.pos] == ']' {
		d.pos++
		return arr, true
	}
	for {
		v, ok := d.value(depth)
		if !ok {
			return nil, false
		}
		arr = append(arr, v)
		d.skipWS()
		if d.pos >= len(d.src) {
			return nil, false
		}
		if d.src[d.pos] == ',' {
			d.pos++
			d.skipWS()
			continue
		}
		if d.src[d.pos] != ']' {
			return nil, false
		}
		d.pos++
		return arr, true
	}
}

// scanJSONNumber returns the end of the JSON number starting at s[i], following
// the RFC 8259 grammar exactly as encoding/json does.
func scanJSONNumber[T string | []byte](s T, i int) (end int, ok bool) {
	if i < len(s) && s[i] == '-' {
		i++
	}
	if i >= len(s) {
		return i, false
	}
	switch {
	case s[i] == '0':
		i++
	case s[i] >= '1' && s[i] <= '9':
		i = skipDigits(s, i)
	default:
		return i, false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return i, false
		}
		i = skipDigits(s, i)
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return i, false
		}
		i = skipDigits(s, i)
	}
	return i, true
}

func skipDigits[T string | []byte](s T, i int) int {
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

// isPlainJSONNumber reports whether s is exactly one JSON number.
func isPlainJSONNumber(s string) bool {
	end, ok := scanJSONNumber(s, 0)
	return ok && end == len(s)
}

// str decodes a JSON string at d.pos (which must be '"').
func (d *fastDecoder) str() (string, bool) {
	s := d.src
	start := d.pos + 1
	i := start
	for i < len(s) {
		c := s[i]
		if c == '"' {
			d.pos = i + 1
			return string(s[start:i]), true
		}
		if c == '\\' || c < 0x20 || c >= utf8.RuneSelf {
			break
		}
		i++
	}
	return d.strSlow(start, i)
}

// strSlow handles escapes, control characters and non-ASCII bytes with
// encoding/json's semantics: invalid UTF-8 and lone surrogates become U+FFFD.
func (d *fastDecoder) strSlow(start, i int) (string, bool) {
	s := d.src
	buf := d.scratch[:0]
	buf = append(buf, s[start:i]...)
	for i < len(s) {
		c := s[i]
		switch {
		case c == '"':
			d.pos = i + 1
			d.scratch = buf[:0]
			return string(buf), true
		case c < 0x20:
			return "", false
		case c == '\\':
			var ok bool
			if buf, i, ok = appendEscape(buf, s, i+1); !ok {
				return "", false
			}
		case c < utf8.RuneSelf:
			buf = append(buf, c)
			i++
		default:
			r, size := utf8.DecodeRune(s[i:])
			buf = utf8.AppendRune(buf, r)
			i += size
		}
	}
	return "", false
}

// appendEscape decodes the escape sequence whose letter is at s[i] (just past
// the backslash), appends it to buf and returns the index after it.
func appendEscape(buf, s []byte, i int) (out []byte, next int, ok bool) {
	if i >= len(s) {
		return buf, i, false
	}
	switch s[i] {
	case '"', '\\', '/':
		return append(buf, s[i]), i + 1, true
	case 'b':
		return append(buf, '\b'), i + 1, true
	case 'f':
		return append(buf, '\f'), i + 1, true
	case 'n':
		return append(buf, '\n'), i + 1, true
	case 'r':
		return append(buf, '\r'), i + 1, true
	case 't':
		return append(buf, '\t'), i + 1, true
	case 'u':
		r, hexOK := hex4(s, i+1)
		if !hexOK {
			return buf, i, false
		}
		i += 5
		if utf16.IsSurrogate(r) {
			r2 := rune(utf8.RuneError)
			if i+1 < len(s) && s[i] == '\\' && s[i+1] == 'u' {
				if lo, loOK := hex4(s, i+2); loOK {
					if dec := utf16.DecodeRune(r, lo); dec != utf8.RuneError {
						r2 = dec
						i += 6
					}
				}
			}
			r = r2
		}
		return utf8.AppendRune(buf, r), i, true
	}
	return buf, i, false
}

// hex4 parses the four hex digits at s[i:i+4].
func hex4(s []byte, i int) (rune, bool) {
	if i+4 > len(s) {
		return 0, false
	}
	var r rune
	for _, c := range s[i : i+4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}
