package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/recolabs/gnata/internal/lexer"
)

// bindingPower returns the left-denotation binding power for a token type.
func bindingPower(tt lexer.TokenType) int {
	switch tt { //nolint:exhaustive // only tokens with non-zero binding power
	case lexer.TokenLParen, lexer.TokenLBracket:
		return 80
	case lexer.TokenAt, lexer.TokenHash:
		return 75
	case lexer.TokenDot:
		return 75
	// TokenSemicolon and TokenColon are separators consumed explicitly by their
	// respective NUD handlers; they must not act as binary infix operators.
	case lexer.TokenSemicolon, lexer.TokenColon:
		return 0
	case lexer.TokenStarStar, lexer.TokenStar, lexer.TokenSlash, lexer.TokenPercent:
		return 60
	case lexer.TokenPlus, lexer.TokenMinus, lexer.TokenAmp:
		return 50
	case lexer.TokenNE, lexer.TokenLE, lexer.TokenGE,
		lexer.TokenEquals, lexer.TokenLT, lexer.TokenGT,
		lexer.TokenIn:
		return 40
	case lexer.TokenAnd:
		return 30
	case lexer.TokenOr:
		return 25
	case lexer.TokenDotDot, lexer.TokenPipe, lexer.TokenQuestion,
		lexer.TokenElvis, lexer.TokenCoalesce:
		return 20
	case lexer.TokenChain:
		return 45 // Higher than comparison ops (40) so "A ~> f() = B" → "(A ~> f()) = B"
	case lexer.TokenAssign:
		return 10
	case lexer.TokenLBrace:
		return 70
	case lexer.TokenCaret:
		return 40
	default:
		return 0
	}
}

func parseError(code, tok, msg string) error {
	return fmt.Errorf("JSONata error %s at token %q: %s", code, tok, msg)
}

// Parser is a top-down operator precedence (Pratt) parser for JSONata.
type Parser struct {
	lex     *lexer.Lexer
	token   lexer.Token
	infix   bool
	src     string
	initErr error // error from initial lexer prime
}

// NewParser creates a new Parser for the given source string.
func NewParser(src string) *Parser {
	p := &Parser{
		lex:   lexer.NewLexer(src),
		src:   src,
		infix: false,
	}
	// Prime the lookahead.
	tok, err := p.lex.Next(false)
	if err != nil {
		p.initErr = err
	}
	p.token = tok
	return p
}

// advance reads the next token from the lexer.
func (p *Parser) advance() error {
	tok, err := p.lex.Next(p.infix)
	if err != nil {
		return err
	}
	p.token = tok
	p.infix = false
	return nil
}

// advancePrefix reads the next token forcing prefix position (infix=false).
// Use after consuming separators/delimiters (comma, semicolon, opening bracket)
// where the next token starts a new expression and must be treated as prefix
// (e.g., / should be lexed as regex, not division).
func (p *Parser) advancePrefix() error {
	p.infix = false
	return p.advance()
}

// consume advances past a token that must have the given type.
func (p *Parser) consume(tt lexer.TokenType) error {
	if p.token.Type != tt {
		return parseError("S0202",
			p.token.Value,
			fmt.Sprintf("expected token %d, got %d (%q)", tt, p.token.Type, p.token.Value))
	}
	return p.advance()
}

// consumePrefix advances past a token that must have the given type, then
// forces the next token read to use prefix position.
func (p *Parser) consumePrefix(tt lexer.TokenType) error {
	if p.token.Type != tt {
		return parseError("S0202",
			p.token.Value,
			fmt.Sprintf("expected token %d, got %d (%q)", tt, p.token.Type, p.token.Value))
	}
	return p.advancePrefix()
}

// Parse parses the full expression and returns the root AST node.
func (p *Parser) Parse() (*Node, error) {
	if p.initErr != nil {
		return nil, p.initErr
	}
	node, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if p.token.Type != lexer.TokenEOF {
		return nil, parseError("S0201", p.token.Value, "unexpected token")
	}
	return node, nil
}

// expression is the core Pratt parsing function.
func (p *Parser) expression(bp int) (*Node, error) {
	left, err := p.nud()
	if err != nil {
		return nil, err
	}
	for bindingPower(p.token.Type) > bp {
		left, err = p.led(left)
		if err != nil {
			return nil, err
		}
	}
	return left, nil
}

// binaryRHS parses the right-hand side of a binary operator.
// Converts S0201 (unexpected end of expression) to S0207 (nothing follows operator).
func (p *Parser) binaryRHS(bp int, op string) (*Node, error) {
	if p.token.Type == lexer.TokenEOF {
		return nil, parseError("S0207", op, fmt.Sprintf("nothing follows the %q operator", op))
	}
	right, err := p.expression(bp)
	if err != nil {
		if strings.Contains(err.Error(), "S0201") && strings.Contains(err.Error(), "EOF") {
			return nil, parseError("S0207", op, fmt.Sprintf("nothing follows the %q operator", op))
		}
		return nil, err
	}
	return right, nil
}

// nud is the null denotation (prefix handler).
func (p *Parser) nud() (*Node, error) { //nolint:gocyclo,funlen // dispatch
	tok := p.token

	switch tok.Type { //nolint:exhaustive // prefix tokens only
	case lexer.TokenEOF:
		return nil, parseError("S0201", "EOF", "unexpected end of expression")

	case lexer.TokenName:
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		// Special case: lambda keyword — only when followed by '('.
		if (tok.Value == "function" || tok.Value == "λ") && p.token.Type == lexer.TokenLParen {
			return p.parseLambda(tok.Pos)
		}
		return &Node{Type: NodeName, Value: tok.Value, Pos: tok.Pos}, nil

	case lexer.TokenAnd, lexer.TokenOr, lexer.TokenIn:
		// "and" / "or" / "in" can appear as field names in prefix position.
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeName, Value: tok.Value, Pos: tok.Pos}, nil

	case lexer.TokenVariable:
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeVariable, Value: tok.Value, Pos: tok.Pos}, nil

	case lexer.TokenString:
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeString, Value: tok.Value, Pos: tok.Pos}, nil

	case lexer.TokenNumber:
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeNumber, Value: tok.Value, NumVal: tok.NumVal, Pos: tok.Pos}, nil

	case lexer.TokenValue:
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeValue, Value: tok.Value, Pos: tok.Pos}, nil

	case lexer.TokenRegex:
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		// Store pattern/flags in Value field.
		val := tok.RegexPat
		if tok.RegexFlg != "" {
			val = tok.RegexPat + "/" + tok.RegexFlg
		}
		return &Node{Type: NodeRegex, Value: val, Pos: tok.Pos}, nil

	case lexer.TokenMinus:
		if err := p.advance(); err != nil {
			return nil, err
		}
		sub, err := p.expression(70)
		if err != nil {
			return nil, err
		}
		// Fold unary minus into number literal.
		if sub.Type == NodeNumber {
			sub.NumVal = -sub.NumVal
			sub.Value = strconv.FormatFloat(sub.NumVal, 'f', -1, 64)
			return sub, nil
		}
		return &Node{Type: NodeUnary, Value: "-", Expression: sub, Pos: tok.Pos}, nil

	case lexer.TokenStar:
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeWildcard, Value: "*", Pos: tok.Pos}, nil

	case lexer.TokenStarStar:
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeDescendant, Value: "**", Pos: tok.Pos}, nil

	case lexer.TokenPercent:
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeParent, Value: "%", Pos: tok.Pos}, nil

	case lexer.TokenLBracket:
		// Array constructor.
		pos := tok.Pos
		if err := p.advancePrefix(); err != nil { // after [, next token is prefix
			return nil, err
		}
		var exprs []*Node
		for p.token.Type != lexer.TokenRBracket {
			if p.token.Type == lexer.TokenEOF {
				return nil, parseError("S0202", "EOF", "expected ]")
			}
			expr, err := p.expression(0)
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, expr)
			if p.token.Type == lexer.TokenComma {
				if err := p.advancePrefix(); err != nil { // after comma, next token is prefix
					return nil, err
				}
			} else if p.token.Type != lexer.TokenRBracket {
				// Structural close tokens (wrong delimiter) → S0202; operator-level tokens → S0204.
				switch p.token.Type { //nolint:exhaustive // structural tokens only
				case lexer.TokenRParen, lexer.TokenRBrace, lexer.TokenColon:
					return nil, parseError("S0202", p.token.Value, "unexpected token in array constructor")
				default:
					return nil, parseError("S0204", p.token.Value, "expected , or ] in array constructor")
				}
			}
		}
		p.infix = true
		if err := p.advance(); err != nil { // consume ]
			return nil, err
		}
		return &Node{Type: NodeUnary, Value: "[", Expressions: exprs, Pos: pos}, nil

	case lexer.TokenLBrace:
		// Object constructor.
		pos := tok.Pos
		if err := p.advancePrefix(); err != nil { // after {, next token is prefix
			return nil, err
		}
		pairs, err := p.parseObjectPairs()
		if err != nil {
			return nil, err
		}
		p.infix = true
		if err := p.advance(); err != nil { // consume }
			return nil, err
		}
		return &Node{Type: NodeUnary, Value: "{", LHS: pairs, Pos: pos}, nil

	case lexer.TokenLParen:
		return p.parseParenOrBlock(tok.Pos)

	case lexer.TokenQuestion:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodePlaceholder, Value: "?", Pos: tok.Pos}, nil

	case lexer.TokenPipe:
		// Transform expression: |pattern| update delete? |
		return p.parseTransform(tok.Pos)

	case lexer.TokenTilde:
		// Transform expression with ~ prefix (alternative syntax).
		return p.parseTransform(tok.Pos)

	case lexer.TokenChain:
		return nil, parseError("S0211", "~>", "invalid use of ~> as prefix")

	default:
		return nil, parseError("S0211", tok.Value, "invalid use of token as prefix")
	}
}

// parseLambda parses: function($p1, $p2, ...) { body }
// Called after consuming the "function" or "λ" name token.
func (p *Parser) parseLambda(pos int) (*Node, error) {
	if err := p.consume(lexer.TokenLParen); err != nil {
		return nil, err
	}
	var params []*Node
	for p.token.Type != lexer.TokenRParen {
		if p.token.Type == lexer.TokenEOF {
			return nil, parseError("S0202", "EOF", "expected ) in lambda parameter list")
		}
		if p.token.Type != lexer.TokenVariable {
			return nil, parseError("S0208", p.token.Value, "expected $parameter name in lambda")
		}
		param := &Node{Type: NodeVariable, Value: p.token.Value, Pos: p.token.Pos}
		params = append(params, param)
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.token.Type == lexer.TokenComma {
			if err := p.advance(); err != nil {
				return nil, err
			}
		} else if p.token.Type != lexer.TokenRParen {
			return nil, parseError("S0202", p.token.Value, "expected , or ) in parameter list")
		}
	}
	if err := p.advance(); err != nil { // consume )
		return nil, err
	}

	// Optional signature: <sig>
	var sig *Signature
	if p.token.Type == lexer.TokenLT {
		sigStr, err := p.parseSignatureString()
		if err != nil {
			return nil, err
		}
		if _, err := ParseSig(sigStr); err != nil {
			return nil, err
		}
		sig = &Signature{Raw: sigStr}
	}

	if err := p.consume(lexer.TokenLBrace); err != nil {
		return nil, err
	}
	body, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TokenRBrace); err != nil {
		return nil, err
	}
	p.infix = true
	return &Node{Type: NodeLambda, Arguments: params, Body: body, Signature: sig, Pos: pos}, nil
}

// parseSignatureString reads tokens until the matching >.
func (p *Parser) parseSignatureString() (string, error) {
	if err := p.advance(); err != nil { // consume <
		return "", err
	}
	var sb strings.Builder
	depth := 1
	for depth > 0 {
		if p.token.Type == lexer.TokenEOF {
			return "", parseError("S0202", "EOF", "unterminated signature")
		}
		if p.token.Type == lexer.TokenLT {
			depth++
		} else if p.token.Type == lexer.TokenGT {
			depth--
			if depth == 0 {
				if err := p.advance(); err != nil { // consume >
					return "", err
				}
				break
			}
		}
		sb.WriteString(p.token.Value)
		if err := p.advance(); err != nil {
			return "", err
		}
	}
	return sb.String(), nil
}

// parseParenOrBlock parses ( expr ) or ( expr; ... ).
func (p *Parser) parseParenOrBlock(pos int) (*Node, error) {
	if err := p.advancePrefix(); err != nil { // consume (, next token is prefix
		return nil, err
	}
	if p.token.Type == lexer.TokenRParen {
		// Empty parens () — treat as empty block.
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeBlock, Expressions: nil, Pos: pos}, nil
	}
	first, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if p.token.Type == lexer.TokenRParen {
		// Single expression in parens — still create a NodeBlock for proper lexical scoping.
		// Each (...) creates its own scope so that bindings inside do not escape.
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeBlock, Expressions: []*Node{first}, Pos: pos}, nil
	}
	// Block: multiple semicolon-separated expressions.
	exprs := []*Node{first}
	for p.token.Type == lexer.TokenSemicolon {
		if err := p.advancePrefix(); err != nil { // after ;, next token is prefix
			return nil, err
		}
		if p.token.Type == lexer.TokenRParen {
			break
		}
		expr, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
	}
	if err := p.consume(lexer.TokenRParen); err != nil {
		return nil, err
	}
	p.infix = true
	return &Node{Type: NodeBlock, Expressions: exprs, Pos: pos}, nil
}

// parseObjectPairs parses key: value pairs separated by commas.
// Caller has already consumed the opening {. Stops at }.
func (p *Parser) parseObjectPairs() ([]*Node, error) {
	var pairs []*Node
	for p.token.Type != lexer.TokenRBrace {
		if p.token.Type == lexer.TokenEOF {
			return nil, parseError("S0202", "EOF", "expected } in object constructor")
		}
		key, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		if err := p.consumePrefix(lexer.TokenColon); err != nil { // after :, next token is prefix
			return nil, err
		}
		val, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, key, val)
		if p.token.Type == lexer.TokenComma {
			if err := p.advancePrefix(); err != nil { // after comma, next token is prefix
				return nil, err
			}
		} else if p.token.Type != lexer.TokenRBrace {
			return nil, parseError("S0202", p.token.Value, "expected , or } in object")
		}
	}
	return pairs, nil
}

// parseTransform parses the | pattern | update [, delete] | transform expression.
// The | token acts as a delimiter so pattern and update must be parsed with a
// binding power of 20 to prevent | from being consumed as a binary infix operator.
func (p *Parser) parseTransform(pos int) (*Node, error) {
	if err := p.advancePrefix(); err != nil { // consume |, next token is prefix
		return nil, err
	}
	// bp=20 stops before the next | (bindingPower(|)=20, loop runs while bp(token) > bp).
	pattern, err := p.expression(20)
	if err != nil {
		return nil, err
	}
	if err := p.consumePrefix(lexer.TokenPipe); err != nil { // after |, next is prefix
		return nil, err
	}
	update, err := p.expression(20)
	if err != nil {
		return nil, err
	}
	var del *Node
	if p.token.Type == lexer.TokenComma {
		if err := p.advancePrefix(); err != nil { // after comma, next is prefix
			return nil, err
		}
		del, err = p.expression(20)
		if err != nil {
			return nil, err
		}
	}
	if err := p.consume(lexer.TokenPipe); err != nil {
		return nil, err
	}
	p.infix = true
	return &Node{Type: NodeTransform, Pattern: pattern, Update: update, Delete: del, Pos: pos}, nil
}

// led is the left denotation (infix handler).
func (p *Parser) led(left *Node) (*Node, error) { //nolint:gocyclo,funlen // dispatch
	tok := p.token
	bp := bindingPower(tok.Type)

	switch tok.Type { //nolint:exhaustive // infix tokens only
	case lexer.TokenLParen:
		// Function call.
		return p.parseFunctionCall(left, tok.Pos)

	case lexer.TokenLBracket:
		// Array index / predicate.
		return p.parseSubscript(left, tok.Pos)

	case lexer.TokenDot:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(74)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: ".", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenAt:
		// S0215: @ cannot follow a predicate (subscript) expression.
		if left.Type == NodeBinary && left.Value == "[" {
			return nil, parseError("S0215", "@", "the @ operator cannot follow a predicate expression")
		}
		// S0216: @ cannot follow a sort expression.
		if left.Type == NodeSort {
			return nil, parseError("S0216", "@", "the @ operator cannot follow a sort expression")
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		// S0214: the token after @ must be a variable ($name), not a plain name.
		if p.token.Type == lexer.TokenName {
			return nil, parseError("S0214", p.token.Value, "the @ operator must be followed by a $variable, not a plain name")
		}
		if p.token.Type != lexer.TokenVariable {
			return nil, parseError("S0202", p.token.Value, "expected $name after @")
		}
		name := p.token.Value
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		left.Focus = name
		return left, nil

	case lexer.TokenHash:
		if err := p.advance(); err != nil {
			return nil, err
		}
		// S0214: the token after # must be a variable ($name), not a plain name.
		if p.token.Type == lexer.TokenName {
			return nil, parseError("S0214", p.token.Value, "the # operator must be followed by a $variable, not a plain name")
		}
		if p.token.Type != lexer.TokenVariable {
			return nil, parseError("S0202", p.token.Value, "expected $name after #")
		}
		name := p.token.Value
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		left.Index = name
		return left, nil

	case lexer.TokenQuestion:
		// Conditional (ternary) operator.
		if err := p.advancePrefix(); err != nil { // after ?, then-branch is prefix
			return nil, err
		}
		then, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		node := &Node{Type: NodeCondition, Condition: left, Then: then, Pos: tok.Pos}
		if p.token.Type == lexer.TokenColon {
			if err := p.advancePrefix(); err != nil { // after :, else-branch is prefix
				return nil, err
			}
			elseBranch, err := p.expression(0)
			if err != nil {
				return nil, err
			}
			node.Else = elseBranch
		}
		return node, nil

	case lexer.TokenAssign:
		// S0212: left side must be a $variable.
		if left.Type != NodeVariable {
			return nil, parseError("S0212", tok.Value, "the left side of := must be a $variable name")
		}
		if err := p.advancePrefix(); err != nil { // after :=, RHS is prefix
			return nil, err
		}
		right, err := p.expression(bp - 1)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBind, Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenChain:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp - 1)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "~>", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenElvis:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp - 1)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "?:", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenCoalesce:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp - 1)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "??", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenDotDot:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp - 1)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "..", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenAnd:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "and", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenOr:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "or", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenIn:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "in", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenEquals:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.binaryRHS(bp, "=")
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "=", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenNE:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.binaryRHS(bp, "!=")
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "!=", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenLT:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.binaryRHS(bp, "<")
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "<", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenGT:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.binaryRHS(bp, ">")
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: ">", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenLE:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.binaryRHS(bp, "<=")
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "<=", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenGE:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: ">=", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenPlus:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "+", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenMinus:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "-", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenStar:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "*", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenSlash:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "/", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenPercent:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.binaryRHS(bp, "%")
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "%", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenStarStar:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "**", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenAmp:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "&", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenPipe:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp - 1)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "|", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenLBrace:
		// Group expression: attach key-value pairs to left.
		pos := tok.Pos
		// S0210: A step can only have one group-by expression.
		if left.Group != nil {
			return nil, parseError("S0210", "{", "each step can only have one grouping expression")
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		pairs, err := p.parseObjectPairs()
		if err != nil {
			return nil, err
		}
		p.infix = true
		if err := p.advance(); err != nil { // consume }
			return nil, err
		}
		group := &GroupExpr{Pairs: pairsToGroupPairs(pairs), Pos: pos}
		left.Group = group
		return left, nil

	case lexer.TokenCaret:
		// Sort: ^(term, term, ...)
		return p.parseSortExpr(left, tok.Pos)

	default:
		return nil, parseError("S0201", tok.Value, "unexpected token in infix position")
	}
}

// parseFunctionCall parses a function call: callee(arg, arg, ...).
// Called after consuming the opening (.
func (p *Parser) parseFunctionCall(callee *Node, pos int) (*Node, error) {
	if err := p.advancePrefix(); err != nil { // consume (, next token is prefix
		return nil, err
	}
	var args []*Node
	partial := false
	for p.token.Type != lexer.TokenRParen {
		if p.token.Type == lexer.TokenEOF {
			return nil, parseError("S0203", "EOF", "expected ) before end of expression")
		}
		arg, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		if arg.Type == NodePlaceholder {
			partial = true
		}
		args = append(args, arg)
		if p.token.Type == lexer.TokenComma {
			if err := p.advancePrefix(); err != nil { // after comma, next token is prefix
				return nil, err
			}
		} else if p.token.Type != lexer.TokenRParen {
			if p.token.Type == lexer.TokenEOF {
				return nil, parseError("S0203", "EOF", "expected ) before end of expression")
			}
			return nil, parseError("S0202", p.token.Value, "expected , or ) in argument list")
		}
	}
	p.infix = true
	if err := p.advance(); err != nil { // consume )
		return nil, err
	}

	nodeType := NodeFunction
	if partial {
		nodeType = NodePartial
	}
	return &Node{
		Type:      nodeType,
		Value:     callee.Value,
		Procedure: callee,
		Arguments: args,
		Pos:       pos,
	}, nil
}

// parseSubscript parses [ expr ] in infix position.
func (p *Parser) parseSubscript(left *Node, pos int) (*Node, error) {
	// S0209: A predicate/subscript cannot follow a group-by expression.
	if left.Group != nil {
		return nil, parseError("S0209", "[", "a predicate cannot follow a grouping expression in a step")
	}
	if err := p.advancePrefix(); err != nil { // consume [, subscript content is prefix
		return nil, err
	}
	if p.token.Type == lexer.TokenRBracket {
		// Empty [] → keep array flag.
		p.infix = true
		if err := p.advance(); err != nil {
			return nil, err
		}
		left.KeepArray = true
		return left, nil
	}
	expr, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TokenRBracket); err != nil {
		return nil, err
	}
	p.infix = true
	return &Node{Type: NodeBinary, Value: "[", Left: left, Right: expr, Pos: pos}, nil
}

// parseSortExpr parses ^(term, term, ...) sort expressions.
func (p *Parser) parseSortExpr(left *Node, pos int) (*Node, error) {
	if err := p.advance(); err != nil { // consume ^
		return nil, err
	}
	if err := p.consumePrefix(lexer.TokenLParen); err != nil { // after (, next is prefix
		return nil, err
	}
	var terms []SortTerm
	for p.token.Type != lexer.TokenRParen {
		if p.token.Type == lexer.TokenEOF {
			return nil, parseError("S0202", "EOF", "expected ) in sort expression")
		}
		descending := p.token.Type == lexer.TokenGT
		if descending || p.token.Type == lexer.TokenLT {
			if err := p.advancePrefix(); err != nil {
				return nil, err
			}
		}
		expr, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		terms = append(terms, SortTerm{Descending: descending, Expression: expr})
		if p.token.Type == lexer.TokenComma {
			if err := p.advancePrefix(); err != nil { // after comma, next is prefix
				return nil, err
			}
		} else if p.token.Type != lexer.TokenRParen {
			return nil, parseError("S0202", p.token.Value, "expected , or ) in sort expression")
		}
	}
	p.infix = true
	if err := p.advance(); err != nil { // consume )
		return nil, err
	}
	return &Node{Type: NodeSort, Left: left, Terms: terms, Pos: pos}, nil
}

// pairsToGroupPairs converts flat [k, v, k, v, ...] slice to [][2]*Node pairs.
func pairsToGroupPairs(flat []*Node) [][2]*Node {
	pairs := make([][2]*Node, 0, len(flat)/2)
	for i := 0; i+1 < len(flat); i += 2 {
		pairs = append(pairs, [2]*Node{flat[i], flat[i+1]})
	}
	return pairs
}
