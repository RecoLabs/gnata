package lexer

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexer tokenizes a JSONata expression.
type Lexer struct {
	src string
	pos int
}

// NewLexer creates a new Lexer for the given source string.
func NewLexer(src string) *Lexer {
	return &Lexer{src: src}
}

func lexError(code, msg string) error {
	return fmt.Errorf("JSONata error %s: %s", code, msg)
}

// isStopChar reports whether ch is a character that terminates identifier scanning.
func isStopChar(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '\v',
		'.', '[', ']', '{', '}', '(', ')', ',', '@', '#', ';', ':', '?',
		'+', '-', '*', '/', '%', '|', '=', '<', '>', '^', '&', '!', '~':
		return true
	}
	return false
}

// Next returns the next token.
// infix=true means we are after a value (closing bracket, identifier, etc.).
// infix=false means we are in prefix position; a '/' starts a regex literal.
func (l *Lexer) Next(infix bool) (Token, error) { //nolint:gocyclo,funlen // dispatch
	// Skip whitespace.
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\v' {
			l.pos++
		} else {
			break
		}
	}

	if l.pos >= len(l.src) {
		return Token{Type: TokenEOF}, nil
	}

	startPos, ch := l.pos, l.src[l.pos]

	// Block comments: /* ... */
	if ch == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '*' {
		for l.pos += 2; l.pos < len(l.src); l.pos++ {
			if l.src[l.pos] == '*' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/' {
				l.pos += 2
				return l.Next(infix)
			}
		}
		return Token{}, lexError("S0106", "unclosed block comment")
	}

	// Regex literal — only in prefix position.
	if ch == '/' && !infix {
		l.pos++ // consume opening '/'
		patStart, depth := l.pos, 0
		for l.pos < len(l.src) {
			switch c := l.src[l.pos]; c {
			case '(', '[', '{':
				depth++
				l.pos++
			case ')', ']', '}':
				depth--
				l.pos++
			case '/':
				if depth == 0 {
					// Count backslashes immediately before this '/'.
					bsCount := 0
					for i := l.pos - 1; i >= patStart && l.src[i] == '\\'; i-- {
						bsCount++
					}
					if bsCount%2 == 0 {
						// Even number of backslashes → unescaped closing '/'.
						pattern := l.src[patStart:l.pos]
						if pattern == "" {
							return Token{}, lexError("S0301", "empty regex pattern")
						}
						l.pos++ // consume closing '/'

						// Collect flags: only 'i' and 'm' are valid.
						var flags strings.Builder
						for l.pos < len(l.src) && unicode.IsLetter(rune(l.src[l.pos])) {
							if fc := l.src[l.pos]; fc == 'i' || fc == 'm' {
								flags.WriteByte(fc)
								l.pos++
							} else {
								return Token{}, lexError("S0302", "invalid regex flag")
							}
						}
						flags.WriteByte('g')

						return Token{
							Type:     TokenRegex,
							RegexPat: pattern,
							RegexFlg: flags.String(),
							Pos:      startPos,
						}, nil
					}
					l.pos++
				} else {
					l.pos++
				}
			default:
				l.pos++
			}
		}
		return Token{}, lexError("S0302", "unterminated regex")
	}

	// Two-character operators — must be checked before single-character.
	if l.pos+1 < len(l.src) {
		two := l.src[l.pos : l.pos+2]
		var tokType TokenType
		switch two {
		case "..":
			tokType = TokenDotDot
		case ":=":
			tokType = TokenAssign
		case "!=":
			tokType = TokenNE
		case ">=":
			tokType = TokenGE
		case "<=":
			tokType = TokenLE
		case "**":
			tokType = TokenStarStar
		case "~>":
			tokType = TokenChain
		case "?:":
			tokType = TokenElvis
		case "??":
			tokType = TokenCoalesce
		}
		if tokType != 0 {
			l.pos += 2
			return Token{Type: tokType, Value: two, Pos: startPos}, nil
		}
	}

	// Single-character operators.
	if tt, ok := singleCharOp(ch); ok {
		l.pos++
		return Token{Type: tt, Value: string(ch), Pos: startPos}, nil
	}

	// String literals.
	if ch == '"' || ch == '\'' {
		return l.scanString(ch, startPos)
	}

	// Number literals: leading digit.
	if ch >= '0' && ch <= '9' {
		return l.scanNumber(startPos)
	}

	// Backtick names.
	if ch == '`' {
		l.pos++
		nameStart := l.pos
		for l.pos < len(l.src) && l.src[l.pos] != '`' {
			l.pos++
		}
		if l.pos >= len(l.src) {
			return Token{}, lexError("S0105", "unterminated backtick name")
		}
		name := l.src[nameStart:l.pos]
		l.pos++
		return Token{Type: TokenName, Value: name, Pos: startPos}, nil
	}

	// Identifiers, keywords, and variables.
	idStart := l.pos
	for l.pos < len(l.src) && !isStopChar(l.src[l.pos]) {
		l.pos++
	}
	id := l.src[idStart:l.pos]

	if id == "" {
		// Unrecognised character — skip it and try again.
		_, size := utf8.DecodeRuneInString(l.src[l.pos:])
		l.pos += size
		return l.Next(infix)
	}

	if id[0] == '$' {
		rest := id[1:]
		if rest == "$" {
			// $$ → variable named "$"
			return Token{Type: TokenVariable, Value: "$", Pos: startPos}, nil
		}
		// bare $ → variable named ""; $foo → variable named "foo"
		return Token{Type: TokenVariable, Value: rest, Pos: startPos}, nil
	}

	switch id {
	case "or":
		return Token{Type: TokenOr, Value: id, Pos: startPos}, nil
	case "in":
		return Token{Type: TokenIn, Value: id, Pos: startPos}, nil
	case "and":
		return Token{Type: TokenAnd, Value: id, Pos: startPos}, nil
	case "true":
		return Token{Type: TokenValue, Value: id, BoolVal: true, Pos: startPos}, nil
	case "false":
		return Token{Type: TokenValue, Value: id, BoolVal: false, Pos: startPos}, nil
	case "null":
		return Token{Type: TokenValue, Value: id, IsNull: true, Pos: startPos}, nil
	}

	return Token{Type: TokenName, Value: id, Pos: startPos}, nil
}

// singleCharOps maps single-character operator bytes to their TokenType.
var singleCharOps = [256]TokenType{
	'.': TokenDot, '[': TokenLBracket, ']': TokenRBracket,
	'{': TokenLBrace, '}': TokenRBrace, '(': TokenLParen, ')': TokenRParen,
	',': TokenComma, '@': TokenAt, '#': TokenHash, ';': TokenSemicolon,
	':': TokenColon, '?': TokenQuestion, '+': TokenPlus, '-': TokenMinus,
	'*': TokenStar, '/': TokenSlash, '%': TokenPercent, '|': TokenPipe,
	'=': TokenEquals, '<': TokenLT, '>': TokenGT, '^': TokenCaret,
	'&': TokenAmp, '!': TokenBang, '~': TokenTilde,
}

// singleCharOp maps a byte to its single-character TokenType.
func singleCharOp(ch byte) (TokenType, bool) {
	tt := singleCharOps[ch]
	return tt, tt != 0
}

// simpleEscapes maps single-character escape codes to their replacement bytes.
var simpleEscapes = [256]byte{
	'"': '"', '\'': '\'', '\\': '\\', '/': '/',
	'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t',
}

// simpleEscapeValid tracks which entries in simpleEscapes are real mappings
// (needed because '\0' is a valid zero-value byte for unused slots).
var simpleEscapeValid = [256]bool{
	'"': true, '\'': true, '\\': true, '/': true,
	'b': true, 'f': true, 'n': true, 'r': true, 't': true,
}

// scanString scans a quoted string starting at startPos.
// quote is either '"' or '\”.
func (l *Lexer) scanString(quote byte, startPos int) (Token, error) {
	l.pos++ // consume opening quote
	var sb strings.Builder
	for l.pos < len(l.src) {
		if c := l.src[l.pos]; c == quote {
			l.pos++
			return Token{Type: TokenString, Value: sb.String(), Pos: startPos}, nil
		} else if c != '\\' {
			_, size := utf8.DecodeRuneInString(l.src[l.pos:])
			sb.WriteString(l.src[l.pos : l.pos+size])
			l.pos += size
			continue
		}
		// Escape sequence.
		l.pos++
		if l.pos >= len(l.src) {
			return Token{}, lexError("S0101", "unterminated string literal")
		}
		esc := l.src[l.pos]
		l.pos++
		if simpleEscapeValid[esc] {
			sb.WriteByte(simpleEscapes[esc])
			continue
		}
		if esc != 'u' {
			return Token{}, lexError("S0103", "invalid escape sequence: \\"+string(esc))
		}
		if l.pos+4 > len(l.src) {
			return Token{}, lexError("S0104", "invalid unicode escape: too short")
		}
		hex := l.src[l.pos : l.pos+4]
		r, err := strconv.ParseInt(hex, 16, 32)
		if err != nil {
			return Token{}, lexError("S0104", "invalid unicode escape: \\u"+hex)
		}
		l.pos += 4
		// Handle UTF-16 surrogate pairs: high surrogate + low surrogate -> single code point.
		if r >= 0xD800 && r <= 0xDBFF && l.pos+6 <= len(l.src) && l.src[l.pos] == '\\' && l.src[l.pos+1] == 'u' {
			if r2, err2 := strconv.ParseInt(l.src[l.pos+2:l.pos+6], 16, 32); err2 == nil && r2 >= 0xDC00 && r2 <= 0xDFFF {
				sb.WriteRune(rune(0x10000 + (r-0xD800)*0x400 + (r2 - 0xDC00)))
				l.pos += 6
				continue
			}
		}
		sb.WriteRune(rune(r))
	}
	return Token{}, lexError("S0101", "unterminated string literal")
}

// scanNumber scans a numeric literal starting at startPos (first char is a digit).
func (l *Lexer) scanNumber(startPos int) (Token, error) {
	numStart := l.pos

	// Integer part: 0 or [1-9][0-9]*
	if l.src[l.pos] == '0' {
		l.pos++
	} else {
		for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
			l.pos++
		}
	}

	// Fractional part.
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		if l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '9' {
			for l.pos++; l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9'; l.pos++ {
			}
		}
		// If next char after '.' is not a digit, leave '.' for the parser (e.g., "0.foo").
	}

	// Exponent part.
	if l.pos < len(l.src) && (l.src[l.pos] == 'e' || l.src[l.pos] == 'E') {
		l.pos++
		if l.pos < len(l.src) && (l.src[l.pos] == '+' || l.src[l.pos] == '-') {
			l.pos++
		}
		for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
			l.pos++
		}
	}

	numStr := l.src[numStart:l.pos]
	f, err := strconv.ParseFloat(numStr, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return Token{}, lexError("S0102", "invalid number literal: "+numStr)
	}
	return Token{Type: TokenNumber, Value: numStr, NumVal: f, Pos: startPos}, nil
}
