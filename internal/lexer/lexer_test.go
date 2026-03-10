package lexer_test

import (
	"strings"
	"testing"

	"github.com/recolabs/gnata/internal/lexer"
)

// tokenizeAll is a helper that tokenizes a full expression, automatically
// tracking infix state so callers don't have to pass it manually.
// infix becomes true after a "value-producing" token, false otherwise.
func tokenizeAll(src string) ([]lexer.Token, error) {
	l := lexer.NewLexer(src)
	var tokens []lexer.Token
	infix := false
	for {
		tok, err := l.Next(infix)
		if err != nil {
			return nil, err
		}
		if tok.Type == lexer.TokenEOF {
			break
		}
		tokens = append(tokens, tok)
		switch tok.Type { //nolint:exhaustive // only infix-triggering tokens
		case lexer.TokenName, lexer.TokenString, lexer.TokenNumber, lexer.TokenValue,
			lexer.TokenVariable, lexer.TokenRegex, lexer.TokenRBracket, lexer.TokenRBrace, lexer.TokenRParen:
			infix = true
		default:
			infix = false
		}
	}
	return tokens, nil
}

// singleToken is a convenience for tests that expect exactly one token.
func singleToken(src string, infix bool) (lexer.Token, error) {
	return lexer.NewLexer(src).Next(infix)
}

func TestLexerOperators(t *testing.T) {
	cases := []struct {
		src       string
		want      lexer.TokenType
		infix     bool
		wantValue string
	}{
		// ── single-char (infix) ──
		{".", lexer.TokenDot, true, ""},
		{"[", lexer.TokenLBracket, true, ""},
		{"]", lexer.TokenRBracket, true, ""},
		{"{", lexer.TokenLBrace, true, ""},
		{"}", lexer.TokenRBrace, true, ""},
		{"(", lexer.TokenLParen, true, ""},
		{")", lexer.TokenRParen, true, ""},
		{",", lexer.TokenComma, true, ""},
		{"@", lexer.TokenAt, true, ""},
		{"#", lexer.TokenHash, true, ""},
		{";", lexer.TokenSemicolon, true, ""},
		{":", lexer.TokenColon, true, ""},
		{"?", lexer.TokenQuestion, true, ""},
		{"+", lexer.TokenPlus, true, ""},
		{"-", lexer.TokenMinus, true, ""},
		{"*", lexer.TokenStar, true, ""},
		{"/", lexer.TokenSlash, true, ""},
		{"%", lexer.TokenPercent, true, ""},
		{"|", lexer.TokenPipe, true, ""},
		{"=", lexer.TokenEquals, true, ""},
		{"<", lexer.TokenLT, true, ""},
		{">", lexer.TokenGT, true, ""},
		{"^", lexer.TokenCaret, true, ""},
		{"&", lexer.TokenAmp, true, ""},
		{"!", lexer.TokenBang, true, ""},
		{"~", lexer.TokenTilde, true, ""},
		// ── multi-char (infix, value must match src) ──
		{"..", lexer.TokenDotDot, true, ".."},
		{":=", lexer.TokenAssign, true, ":="},
		{"!=", lexer.TokenNE, true, "!="},
		{">=", lexer.TokenGE, true, ">="},
		{"<=", lexer.TokenLE, true, "<="},
		{"**", lexer.TokenStarStar, true, "**"},
		{"~>", lexer.TokenChain, true, "~>"},
		{"?:", lexer.TokenElvis, true, "?:"},
		{"??", lexer.TokenCoalesce, true, "??"},
		// ── keywords (prefix) ──
		{"and", lexer.TokenAnd, false, ""},
		{"or", lexer.TokenOr, false, ""},
		{"in", lexer.TokenIn, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			tok, err := singleToken(tc.src, tc.infix)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tok.Type != tc.want {
				t.Fatalf("got token type %v, want %v", tok.Type, tc.want)
			}
			if tc.wantValue != "" && tok.Value != tc.wantValue {
				t.Fatalf("got value %q, want %q", tok.Value, tc.wantValue)
			}
		})
	}
}

func TestLexerStrings(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		want    string
		isErr   bool
		errCode string
	}{
		{"double-quoted", `"hello"`, "hello", false, ""},
		{"single-quoted", `'world'`, "world", false, ""},
		{"escape-quote", `"say \"hi\""`, `say "hi"`, false, ""},
		{"escape-backslash", `"a\\b"`, `a\b`, false, ""},
		{"escape-slash", `"a\/b"`, `a/b`, false, ""},
		{"escape-backspace", `"\b"`, "\b", false, ""},
		{"escape-formfeed", `"\f"`, "\f", false, ""},
		{"escape-newline", `"\n"`, "\n", false, ""},
		{"escape-return", `"\r"`, "\r", false, ""},
		{"escape-tab", `"\t"`, "\t", false, ""},
		{"unicode-escape", `"\u0041"`, "A", false, ""},
		{"unicode-emoji-area", `"\u00E9"`, "\u00E9", false, ""},
		{"unterminated", `"hello`, "", true, "S0101"},
		{"invalid-escape", `"\q"`, "", true, "S0103"},
		{"invalid-unicode-short", `"\u00"`, "", true, ""},
		{"invalid-unicode-hex", `"\uXXXX"`, "", true, ""},
		{"invalid-unicode-nonhex", `"\u00ZZ"`, "", true, "S0104"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, err := singleToken(tc.src, false)
			if tc.isErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errCode != "" && !strings.Contains(err.Error(), tc.errCode) {
					t.Fatalf("error %q does not contain code %q", err.Error(), tc.errCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tok.Type != lexer.TokenString {
				t.Fatalf("got type %v, want TokenString", tok.Type)
			}
			if tok.Value != tc.want {
				t.Fatalf("got value %q, want %q", tok.Value, tc.want)
			}
		})
	}
}

func TestLexerNumbers(t *testing.T) {
	cases := []struct {
		src    string
		numVal float64
	}{
		{"0", 0},
		{"42", 42},
		{"3.14", 3.14},
		{"1e3", 1000},
		{"1E3", 1000},
		{"1.5e2", 150},
		{"9.99E-1", 0.999},
		{"100", 100},
		{"0.5", 0.5},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			tok, err := singleToken(tc.src, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tok.Type != lexer.TokenNumber {
				t.Fatalf("got type %v, want TokenNumber", tok.Type)
			}
			if tok.NumVal != tc.numVal {
				t.Fatalf("got %v, want %v", tok.NumVal, tc.numVal)
			}
		})
	}
}

func TestLexerVariables(t *testing.T) {
	cases := []struct {
		src   string
		value string
	}{
		{"$", ""},
		{"$foo", "foo"},
		{"$$", "$"},
		{"$myVar123", "myVar123"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			tok, err := singleToken(tc.src, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tok.Type != lexer.TokenVariable {
				t.Fatalf("got type %v, want TokenVariable", tok.Type)
			}
			if tok.Value != tc.value {
				t.Fatalf("got value %q, want %q", tok.Value, tc.value)
			}
		})
	}
}

func TestLexerValues(t *testing.T) {
	cases := []struct {
		src     string
		boolVal bool
		isNull  bool
	}{
		{"true", true, false},
		{"false", false, false},
		{"null", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			tok, err := singleToken(tc.src, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tok.Type != lexer.TokenValue {
				t.Fatalf("got %v, want TokenValue", tok.Type)
			}
			if tok.BoolVal != tc.boolVal {
				t.Fatalf("BoolVal: got %v, want %v", tok.BoolVal, tc.boolVal)
			}
			if tok.IsNull != tc.isNull {
				t.Fatalf("IsNull: got %v, want %v", tok.IsNull, tc.isNull)
			}
		})
	}
}

func TestLexerRegex(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantPat string
		wantFlg string
		isErr   bool
		errCode string
	}{
		{"simple", `/hello/`, "hello", "g", false, ""},
		{"with flags i and m", `/^hello/im`, "^hello", "img", false, ""},
		{"with flag i only", `/foo/i`, "foo", "ig", false, ""},
		{"with flag m only", `/bar/m`, "bar", "mg", false, ""},
		{"escaped slash in pattern", `/a\/b/`, `a\/b`, "g", false, ""},
		{"bracket depth", `/[a-z]/`, "[a-z]", "g", false, ""},
		{"empty pattern", `//`, "", "", true, "S0301"},
		{"unterminated", `/hello`, "", "", true, "S0302"},
		{"invalid flag", `/foo/x`, "", "", true, "S0302"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, err := singleToken(tc.src, false)
			if tc.isErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errCode != "" && !strings.Contains(err.Error(), tc.errCode) {
					t.Fatalf("error %q should contain %q", err.Error(), tc.errCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tok.Type != lexer.TokenRegex {
				t.Fatalf("got type %v, want TokenRegex", tok.Type)
			}
			if tok.RegexPat != tc.wantPat {
				t.Fatalf("pattern: got %q, want %q", tok.RegexPat, tc.wantPat)
			}
			if tok.RegexFlg != tc.wantFlg {
				t.Fatalf("flags: got %q, want %q", tok.RegexFlg, tc.wantFlg)
			}
		})
	}

	t.Run("division in infix position", func(t *testing.T) {
		tok, err := singleToken("/", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tok.Type != lexer.TokenSlash {
			t.Fatalf("got %v, want TokenSlash", tok.Type)
		}
	})
}

func TestLexerMisc(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantType  lexer.TokenType
		wantValue string
		isErr     bool
		errCode   string
	}{
		{"backtick name", "`hello world`", lexer.TokenName, "hello world", false, ""},
		{"unterminated backtick", "`unterminated", 0, "", true, "S0105"},
		{"block comment skipped", "/* comment */ hello", lexer.TokenName, "hello", false, ""},
		{"unclosed block comment", "/* unclosed", 0, "", true, "S0106"},
		{"whitespace skipping", "   \t\n  foo", lexer.TokenName, "foo", false, ""},
		{"EOF on empty input", "", lexer.TokenEOF, "", false, ""},
		{"EOF after whitespace", "   ", lexer.TokenEOF, "", false, ""},
		{"bare name", "Account", lexer.TokenName, "Account", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok, err := singleToken(tc.src, false)
			if tc.isErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.errCode) {
					t.Fatalf("error %q does not contain code %q", err.Error(), tc.errCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tok.Type != tc.wantType {
				t.Fatalf("got type %v, want %v", tok.Type, tc.wantType)
			}
			if tc.wantValue != "" && tok.Value != tc.wantValue {
				t.Fatalf("got value %q, want %q", tok.Value, tc.wantValue)
			}
		})
	}
}

// ---- TestLexerSequence: multi-token expression tests ----

func TestLexerSequence(t *testing.T) { //nolint:funlen // test data table
	type tokSpec struct {
		typ   lexer.TokenType
		value string // checked only when non-empty
	}

	cases := []struct {
		name   string
		src    string
		tokens []tokSpec
	}{
		{
			name: "field path",
			src:  "Account.Order.Product",
			tokens: []tokSpec{
				{lexer.TokenName, "Account"},
				{lexer.TokenDot, ""},
				{lexer.TokenName, "Order"},
				{lexer.TokenDot, ""},
				{lexer.TokenName, "Product"},
			},
		},
		{
			name: "function call with path",
			src:  "$sum(items.price)",
			tokens: []tokSpec{
				{lexer.TokenVariable, "sum"},
				{lexer.TokenLParen, ""},
				{lexer.TokenName, "items"},
				{lexer.TokenDot, ""},
				{lexer.TokenName, "price"},
				{lexer.TokenRParen, ""},
			},
		},
		{
			name: "equality check",
			src:  `payload.user = "test@example.com"`,
			tokens: []tokSpec{
				{lexer.TokenName, "payload"},
				{lexer.TokenDot, ""},
				{lexer.TokenName, "user"},
				{lexer.TokenEquals, ""},
				{lexer.TokenString, "test@example.com"},
			},
		},
		{
			name: "filter expression",
			src:  `items[status = "active"].amount`,
			tokens: []tokSpec{
				{lexer.TokenName, "items"},
				{lexer.TokenLBracket, ""},
				{lexer.TokenName, "status"},
				{lexer.TokenEquals, ""},
				{lexer.TokenString, "active"},
				{lexer.TokenRBracket, ""},
				{lexer.TokenDot, ""},
				{lexer.TokenName, "amount"},
			},
		},
		{
			name: "regex with flags",
			src:  `/^hello/im`,
			tokens: []tokSpec{
				{lexer.TokenRegex, ""},
			},
		},
		{
			name: "arithmetic expression",
			src:  "a + b * 3",
			tokens: []tokSpec{
				{lexer.TokenName, "a"},
				{lexer.TokenPlus, ""},
				{lexer.TokenName, "b"},
				{lexer.TokenStar, ""},
				{lexer.TokenNumber, "3"},
			},
		},
		{
			name: "multi-char ops",
			src:  "a != b and c <= d",
			tokens: []tokSpec{
				{lexer.TokenName, "a"},
				{lexer.TokenNE, ""},
				{lexer.TokenName, "b"},
				{lexer.TokenAnd, ""},
				{lexer.TokenName, "c"},
				{lexer.TokenLE, ""},
				{lexer.TokenName, "d"},
			},
		},
		{
			name: "block comment is transparent",
			src:  "a /* ignored */ + b",
			tokens: []tokSpec{
				{lexer.TokenName, "a"},
				{lexer.TokenPlus, ""},
				{lexer.TokenName, "b"},
			},
		},
		{
			name: "lambda assign and chain",
			src:  "x := 1 ~> f",
			tokens: []tokSpec{
				{lexer.TokenName, "x"},
				{lexer.TokenAssign, ""},
				{lexer.TokenNumber, "1"},
				{lexer.TokenChain, ""},
				{lexer.TokenName, "f"},
			},
		},
		{
			name: "null and booleans",
			src:  "null or true and false",
			tokens: []tokSpec{
				{lexer.TokenValue, "null"},
				{lexer.TokenOr, ""},
				{lexer.TokenValue, "true"},
				{lexer.TokenAnd, ""},
				{lexer.TokenValue, "false"},
			},
		},
		{
			name: "dollar",
			src:  "$$",
			tokens: []tokSpec{
				{lexer.TokenVariable, "$"},
			},
		},
		{
			name: "bare dollar",
			src:  "$",
			tokens: []tokSpec{
				{lexer.TokenVariable, ""},
			},
		},
		{
			name: "dotdot range",
			src:  "[1..5]",
			tokens: []tokSpec{
				{lexer.TokenLBracket, ""},
				{lexer.TokenNumber, "1"},
				{lexer.TokenDotDot, ""},
				{lexer.TokenNumber, "5"},
				{lexer.TokenRBracket, ""},
			},
		},
		{
			name: "coalesce and elvis",
			src:  "a ?? b ?: c",
			tokens: []tokSpec{
				{lexer.TokenName, "a"},
				{lexer.TokenCoalesce, ""},
				{lexer.TokenName, "b"},
				{lexer.TokenElvis, ""},
				{lexer.TokenName, "c"},
			},
		},
		{
			name: "star",
			src:  "a ** 2",
			tokens: []tokSpec{
				{lexer.TokenName, "a"},
				{lexer.TokenStarStar, ""},
				{lexer.TokenNumber, "2"},
			},
		},
		{
			name: "backtick name with spaces",
			src:  "`hello world`.foo",
			tokens: []tokSpec{
				{lexer.TokenName, "hello world"},
				{lexer.TokenDot, ""},
				{lexer.TokenName, "foo"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens, err := tokenizeAll(tc.src)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tokens) != len(tc.tokens) {
				t.Fatalf("got %d tokens, want %d:\n  got: %v\n  want: %v",
					len(tokens), len(tc.tokens), tokens, tc.tokens)
			}
			for i, want := range tc.tokens {
				got := tokens[i]
				if got.Type != want.typ {
					t.Errorf("token[%d]: type got %v, want %v", i, got.Type, want.typ)
				}
				if want.value != "" && got.Value != want.value {
					t.Errorf("token[%d]: value got %q, want %q", i, got.Value, want.value)
				}
			}
		})
	}

	t.Run("regex literal content", func(t *testing.T) {
		tokens, err := tokenizeAll("/^hello/im")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tokens) != 1 {
			t.Fatalf("got %d tokens, want 1", len(tokens))
		}
		tok := tokens[0]
		if tok.Type != lexer.TokenRegex {
			t.Fatalf("got type %v, want TokenRegex", tok.Type)
		}
		if tok.RegexPat != "^hello" {
			t.Fatalf("pattern: got %q, want %q", tok.RegexPat, "^hello")
		}
		if tok.RegexFlg != "img" {
			t.Fatalf("flags: got %q, want %q", tok.RegexFlg, "img")
		}
	})

	t.Run("variable then dot then name", func(t *testing.T) {
		tokens, err := tokenizeAll("$foo.bar")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tokens) != 3 {
			t.Fatalf("got %d tokens, want 3: %v", len(tokens), tokens)
		}
		if tokens[0].Type != lexer.TokenVariable || tokens[0].Value != "foo" {
			t.Fatalf("token[0]: got type=%v value=%q", tokens[0].Type, tokens[0].Value)
		}
		if tokens[1].Type != lexer.TokenDot {
			t.Fatalf("token[1]: got %v, want TokenDot", tokens[1].Type)
		}
		if tokens[2].Type != lexer.TokenName || tokens[2].Value != "bar" {
			t.Fatalf("token[2]: got type=%v value=%q", tokens[2].Type, tokens[2].Value)
		}
	})
}
