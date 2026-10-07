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
	case lexer.TokenDotDot, lexer.TokenQuestion,
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

// MaxDepth is the deepest expression nesting the parser accepts. Each nested
// expression and each chained operator is one level. It keeps the parser and
// the recursive passes over the tree (processing, analysis, evaluation) far
// from Go's fatal stack limit, which deep enough input would otherwise reach.
// jsonata-js itself overflows its stack at about 2,000 levels of nesting.
const MaxDepth = 10_000

// Parser is a top-down operator precedence (Pratt) parser for JSONata.
type Parser struct {
	lex     *lexer.Lexer
	token   lexer.Token
	src     string
	initErr error   // error from initial lexer prime
	slots   []*Slot // the slots of the % operators parsed so far, in order
	depth   int     // active expression calls
	height  int     // tallest subtree finished in the current expression call
	// functions reports that a lambda or transform was parsed, whose depth
	// markFunctionDepths records.
	functions bool
	// deferred is the first error jsonata-js reports only after the whole
	// expression has parsed, such as S0207 for a missing operand, so any
	// parse error later in the source takes precedence over it.
	deferred error
}

// NewParser creates a new Parser for the given source string.
func NewParser(src string) *Parser {
	p := &Parser{
		lex: lexer.NewLexer(src),
		src: src,
	}
	// Prime the lookahead.
	p.initErr = p.advance()
	return p
}

// The lexer reads '/' as division in infix position and as a regex in prefix
// position, and jsonata-js picks the position per token rather than from the
// grammar. The token after the first token of an expression is read in infix
// position, whether that first token is an operand or an opener ([, {, (, -
// or |), so [/a/] raises S0211. The token after an operator, a separator, the
// [ or ( of a subscript, call, lambda or sort, or the { of a group is read in
// prefix position. Among closers, the ] of an array or predicate, the } of an
// object or group and the ) of a block, call or lambda parameter list are
// followed by infix position; an empty [], a sort's ), a lambda body's } and
// a transform's closing | by prefix position.

// advance reads the next token in prefix position.
func (p *Parser) advance() error {
	return p.next(false)
}

// advanceInfix reads the next token in infix position.
func (p *Parser) advanceInfix() error {
	return p.next(true)
}

func (p *Parser) next(infix bool) error {
	tok, err := p.lex.Next(infix)
	if err != nil {
		return err
	}
	// jsonata-js lexes a lone ! or ~ as an operator it has no symbol for,
	// and rejects it as soon as it is read.
	if tok.Type == lexer.TokenBang || tok.Type == lexer.TokenTilde {
		return parseError("S0204", tok.Value, "unknown operator")
	}
	p.token = tok
	return nil
}

func (p *Parser) expect(tt lexer.TokenType) error {
	if p.token.Type == lexer.TokenEOF && tt != lexer.TokenEOF {
		return p.endError()
	}
	if p.token.Type != tt {
		return parseError("S0202",
			p.token.Value,
			fmt.Sprintf("expected token %d, got %d (%q)", tt, p.token.Type, p.token.Value))
	}
	return nil
}

// consume advances past a token that must have the given type, reading the
// next token in prefix position.
func (p *Parser) consume(tt lexer.TokenType) error {
	if err := p.expect(tt); err != nil {
		return err
	}
	return p.advance()
}

// consumeInfix advances past a token that must have the given type, reading
// the next token in infix position.
func (p *Parser) consumeInfix(tt lexer.TokenType) error {
	if err := p.expect(tt); err != nil {
		return err
	}
	return p.advanceInfix()
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
	if p.deferred != nil {
		return nil, p.deferred
	}
	return node, nil
}

// endError reports a token that was required where the expression ended.
func (p *Parser) endError() error {
	return parseError("S0203", "EOF", "expected a token before end of expression")
}

// deferError records err to report once the expression has parsed; the first
// recorded error wins.
func (p *Parser) deferError(err error) {
	if p.deferred == nil {
		p.deferred = err
	}
}

// expression is the core Pratt parsing function. It also tracks the height of
// the subtree it builds, so a chain like 1+1+...+1, which the loop below
// builds without recursing, counts toward MaxDepth like nesting does.
func (p *Parser) expression(bp int) (*Node, error) {
	if p.depth == MaxDepth {
		return nil, p.depthError()
	}
	p.depth++
	defer func() { p.depth-- }()
	outer := p.height
	p.height = 0
	left, err := p.nud()
	if err != nil {
		return nil, err
	}
	height := p.height + 1
	for {
		if height > MaxDepth {
			return nil, p.depthError()
		}
		if bindingPower(p.token.Type) <= bp {
			break
		}
		p.height = 0
		left, err = p.led(left)
		if err != nil {
			return nil, err
		}
		height = max(height, p.height) + 1
	}
	p.height = max(outer, height)
	return left, nil
}

func (p *Parser) depthError() error {
	return parseError("S0218", p.token.Value,
		fmt.Sprintf("expression nesting exceeds the maximum depth of %d", MaxDepth))
}

// nud is the null denotation (prefix handler).
func (p *Parser) nud() (*Node, error) { //nolint:gocyclo,funlen // dispatch
	tok := p.token

	switch tok.Type { //nolint:exhaustive // prefix tokens only
	case lexer.TokenEOF:
		// An operand is missing, as in 1 +. Parsing continues so that a
		// later expected token reports S0203 first, as in jsonata-js.
		p.deferError(parseError("S0207", "EOF", "unexpected end of expression"))
		return &Node{Type: NodeValue, Value: "null", Pos: tok.Pos}, nil

	case lexer.TokenName:
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		// Special case: lambda keyword — only when followed by '('.
		if (tok.Value == "function" || tok.Value == "λ") && p.token.Type == lexer.TokenLParen {
			return p.parseLambda(tok.Pos)
		}
		return &Node{Type: NodeName, Value: tok.Value, Pos: tok.Pos}, nil

	case lexer.TokenAnd, lexer.TokenOr, lexer.TokenIn:
		// "and" / "or" / "in" can appear as field names in prefix position.
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeName, Value: tok.Value, Pos: tok.Pos}, nil

	case lexer.TokenVariable:
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeVariable, Value: tok.Value, Pos: tok.Pos}, nil

	case lexer.TokenString:
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeString, Value: tok.Value, Pos: tok.Pos}, nil

	case lexer.TokenNumber:
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeNumber, Value: tok.Value, NumVal: tok.NumVal, Pos: tok.Pos}, nil

	case lexer.TokenValue:
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeValue, Value: tok.Value, Pos: tok.Pos}, nil

	case lexer.TokenRegex:
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		// Store pattern/flags in Value field.
		val := tok.RegexPat
		if tok.RegexFlg != "" {
			val = tok.RegexPat + "/" + tok.RegexFlg
		}
		return &Node{Type: NodeRegex, Value: val, Pos: tok.Pos}, nil

	case lexer.TokenMinus:
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		sub, err := p.expression(70)
		if err != nil {
			return nil, err
		}
		// Fold unary minus into number literal.
		if sub.Type == NodeNumber {
			sub.NumVal = -sub.NumVal
			if v, ok := strings.CutPrefix(sub.Value, "-"); ok {
				sub.Value = v
			} else {
				sub.Value = "-" + sub.Value
			}
			return sub, nil
		}
		return &Node{Type: NodeUnary, Value: "-", Expression: sub, Pos: tok.Pos}, nil

	case lexer.TokenStar:
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeWildcard, Value: "*", Pos: tok.Pos}, nil

	case lexer.TokenStarStar:
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeDescendant, Value: "**", Pos: tok.Pos}, nil

	case lexer.TokenPercent:
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		slot := &Slot{Label: "!" + strconv.Itoa(len(p.slots)), Level: 1}
		p.slots = append(p.slots, slot)
		return &Node{Type: NodeParent, Value: "%", Pos: tok.Pos, Slot: slot}, nil

	case lexer.TokenLBracket:
		// Array constructor.
		pos := tok.Pos
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		var exprs []*Node
		for p.token.Type != lexer.TokenRBracket {
			if p.token.Type == lexer.TokenEOF {
				return nil, p.endError()
			}
			expr, err := p.expression(0)
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, expr)
			if p.token.Type == lexer.TokenComma {
				if err := p.advance(); err != nil {
					return nil, err
				}
			} else if err := p.expect(lexer.TokenRBracket); err != nil {
				return nil, err
			}
		}
		if err := p.advanceInfix(); err != nil { // consume ]
			return nil, err
		}
		return &Node{Type: NodeUnary, Value: "[", Expressions: exprs, Pos: pos}, nil

	case lexer.TokenLBrace:
		// Object constructor.
		pos := tok.Pos
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		pairs, err := p.parseObjectPairs()
		if err != nil {
			return nil, err
		}
		if err := p.advanceInfix(); err != nil { // consume }
			return nil, err
		}
		return &Node{Type: NodeUnary, Value: "{", LHS: pairs, Pos: pos}, nil

	case lexer.TokenLParen:
		return p.parseParenOrBlock(tok.Pos)

	case lexer.TokenPipe:
		// Transform expression: |pattern| update delete? |
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
	// Like jsonata-js, parse each parameter as an expression and require a
	// $variable only once the list has parsed, so function($x/2) is S0208
	// but function($x/) reports the parse error. #, @ and a group make the
	// parameter an expression in jsonata-js; a trailing [] does not.
	var params []*Node
	for p.token.Type != lexer.TokenRParen {
		if p.token.Type == lexer.TokenEOF {
			return nil, p.endError()
		}
		param, err := p.parseArgument()
		if err != nil {
			return nil, err
		}
		params = append(params, param)
		if p.token.Type == lexer.TokenComma {
			if err := p.advance(); err != nil {
				return nil, err
			}
		} else if err := p.expect(lexer.TokenRParen); err != nil {
			return nil, err
		}
	}
	if err := p.advanceInfix(); err != nil { // consume )
		return nil, err
	}
	for _, param := range params {
		if param.Type != NodeVariable || param.Index != "" || param.Focus != "" || param.Group != nil {
			return nil, parseError("S0208", param.Value, "expected $parameter name in lambda")
		}
	}

	// Optional signature: <sig>
	var sig *Signature
	if p.token.Type == lexer.TokenLT {
		sigStr, err := p.parseSignatureString()
		if err != nil {
			return nil, err
		}
		params, err := ParseSig(sigStr)
		if err != nil {
			return nil, err
		}
		sig = &Signature{Params: params}
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
	p.functions = true
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
		// As in jsonata-js, a signature ends at the first { or the end of the
		// input, which then report the missing > as S0202 or S0203.
		if p.token.Type == lexer.TokenEOF || p.token.Type == lexer.TokenLBrace {
			return "", p.expect(lexer.TokenGT)
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
	if err := p.advanceInfix(); err != nil {
		return nil, err
	}
	if p.token.Type == lexer.TokenRParen {
		// Empty parens () — treat as empty block.
		if err := p.advanceInfix(); err != nil {
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
		if err := p.advanceInfix(); err != nil {
			return nil, err
		}
		return &Node{Type: NodeBlock, Expressions: []*Node{first}, Pos: pos}, nil
	}
	// Block: multiple semicolon-separated expressions.
	exprs := []*Node{first}
	for p.token.Type == lexer.TokenSemicolon {
		if err := p.advance(); err != nil {
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
	if err := p.consumeInfix(lexer.TokenRParen); err != nil {
		return nil, err
	}
	return &Node{Type: NodeBlock, Expressions: exprs, Pos: pos}, nil
}

// parseObjectPairs parses key: value pairs separated by commas.
// Caller has already consumed the opening {. Stops at }.
func (p *Parser) parseObjectPairs() ([]*Node, error) {
	var pairs []*Node
	for p.token.Type != lexer.TokenRBrace {
		if p.token.Type == lexer.TokenEOF {
			return nil, p.endError()
		}
		key, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TokenColon); err != nil {
			return nil, err
		}
		val, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, key, val)
		if p.token.Type == lexer.TokenComma {
			if err := p.advance(); err != nil {
				return nil, err
			}
		} else if err := p.expect(lexer.TokenRBrace); err != nil {
			return nil, err
		}
	}
	return pairs, nil
}

// parseTransform parses the | pattern | update [, delete] | transform expression.
// | has no infix meaning, so each part is a full expression that ends at it.
func (p *Parser) parseTransform(pos int) (*Node, error) {
	if err := p.advanceInfix(); err != nil {
		return nil, err
	}
	pattern, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TokenPipe); err != nil {
		return nil, err
	}
	update, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	var del *Node
	if p.token.Type == lexer.TokenComma {
		if err := p.advance(); err != nil {
			return nil, err
		}
		del, err = p.expression(0)
		if err != nil {
			return nil, err
		}
	}
	if err := p.consume(lexer.TokenPipe); err != nil {
		return nil, err
	}
	p.functions = true
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

	case lexer.TokenAt, lexer.TokenHash:
		return p.parseBinding(left, tok.Type == lexer.TokenAt)

	case lexer.TokenQuestion:
		// Conditional (ternary) operator.
		if err := p.advance(); err != nil {
			return nil, err
		}
		then, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		node := &Node{Type: NodeCondition, Condition: left, Then: then, Pos: tok.Pos}
		if p.token.Type == lexer.TokenColon {
			if err := p.advance(); err != nil {
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
		if err := p.advance(); err != nil {
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
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "=", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenNE:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "!=", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenLT:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: "<", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenGT:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
		if err != nil {
			return nil, err
		}
		return &Node{Type: NodeBinary, Value: ">", Left: left, Right: right, Pos: tok.Pos}, nil

	case lexer.TokenLE:
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.expression(bp)
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
		right, err := p.expression(bp)
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

	case lexer.TokenLBrace:
		// Group expression: attach key-value pairs to left.
		pos := tok.Pos
		// S0210: A step can only have one group-by expression.
		if left.Group != nil {
			p.deferError(parseError("S0210", "{", "each step can only have one grouping expression"))
		}
		if err := p.advance(); err != nil {
			return nil, err
		}
		pairs, err := p.parseObjectPairs()
		if err != nil {
			return nil, err
		}
		if err := p.advanceInfix(); err != nil { // consume }
			return nil, err
		}
		group := &GroupExpr{Pairs: pairsToGroupPairs(pairs), Pos: pos, OnPath: isPathLike(left)}
		left.Group = group
		return left, nil

	case lexer.TokenCaret:
		// Sort: ^(term, term, ...)
		return p.parseSortExpr(left, tok.Pos)

	default:
		return nil, parseError("S0201", tok.Value, "unexpected token in infix position")
	}
}

// parseBinding parses the $variable bound by @ (focus) or # (index). Like
// jsonata-js, it parses the right side as an expression, binding as tightly as
// a subscript, and then requires a plain $variable (S0214). It reports @ after
// a predicate or sort (S0215, S0216) only once the whole expression has parsed.
func (p *Parser) parseBinding(left *Node, focus bool) (*Node, error) {
	op := "#"
	if focus {
		op = "@"
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	rhs, err := p.expression(bindingPower(lexer.TokenLBracket))
	if err != nil {
		return nil, err
	}
	if rhs.Type != NodeVariable {
		return nil, parseError("S0214", op, "the right side of "+op+" must be a $variable")
	}
	left.indexLast = !focus
	if !focus {
		left.Index = rhs.Value
		return left, nil
	}
	switch {
	case left.Type == NodeBinary && left.Value == "[":
		p.deferError(parseError("S0215", "@", "the @ operator cannot follow a predicate expression"))
	case left.Type == NodeSort:
		p.deferError(parseError("S0216", "@", "the @ operator cannot follow a sort expression"))
	}
	left.Focus = rhs.Value
	return left, nil
}

// parseFunctionCall parses a function call: callee(arg, arg, ...).
// Called after consuming the opening (.
func (p *Parser) parseFunctionCall(callee *Node, pos int) (*Node, error) {
	if err := p.advance(); err != nil { // consume (
		return nil, err
	}
	var args []*Node
	partial := false
	for p.token.Type != lexer.TokenRParen {
		if p.token.Type == lexer.TokenEOF {
			return nil, p.endError()
		}
		arg, err := p.parseArgument()
		if err != nil {
			return nil, err
		}
		if arg.Type == NodePlaceholder {
			partial = true
		}
		args = append(args, arg)
		if p.token.Type == lexer.TokenComma {
			if err := p.advance(); err != nil {
				return nil, err
			}
		} else if err := p.expect(lexer.TokenRParen); err != nil {
			return nil, err
		}
	}
	if err := p.advanceInfix(); err != nil { // consume )
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

// parseArgument parses one call argument or lambda parameter. As in
// jsonata-js, a ? placeholder is only recognized as a whole argument, so
// $f(? + 1) is S0202 and a ? anywhere else is S0211.
func (p *Parser) parseArgument() (*Node, error) {
	if p.token.Type != lexer.TokenQuestion {
		return p.expression(0)
	}
	pos := p.token.Pos
	if err := p.advance(); err != nil {
		return nil, err
	}
	return &Node{Type: NodePlaceholder, Value: "?", Pos: pos}, nil
}

// parseSubscript parses [ expr ] in infix position.
func (p *Parser) parseSubscript(left *Node, pos int) (*Node, error) {
	if err := p.advance(); err != nil { // consume [
		return nil, err
	}
	if p.token.Type == lexer.TokenRBracket {
		// Empty [] → keep array flag.
		if err := p.advance(); err != nil {
			return nil, err
		}
		alreadyKept := left.KeepArray
		left.KeepArray = true
		// jsonata-js gives the [] to the # it follows, past any predicates
		// after it, or else to the chain's base.
		bound := left
		for !bound.indexLast && bound.Type == NodeBinary && bound.Value == "[" && bound.Left != nil {
			bound = bound.Left
		}
		if bound.indexLast {
			bound.KeptAfterIndex = true
			bound.IndexKeepArray = bound.IndexKeepArray || !alreadyKept
		}
		return left, nil
	}
	// S0209: A predicate cannot follow a step's group-by expression. A group
	// on a path, as in a{k: v}[0], applies after all of it instead (see
	// ProcessAST).
	if left.Group != nil && !left.Group.OnPath {
		p.deferError(parseError("S0209", "[", "a predicate cannot follow a grouping expression in a step"))
	}
	expr, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if err := p.consumeInfix(lexer.TokenRBracket); err != nil {
		return nil, err
	}
	return &Node{Type: NodeBinary, Value: "[", Left: left, Right: expr, Pos: pos}, nil
}

// parseSortExpr parses ^(term, term, ...) sort expressions.
func (p *Parser) parseSortExpr(left *Node, pos int) (*Node, error) {
	if err := p.advance(); err != nil { // consume ^
		return nil, err
	}
	if err := p.consume(lexer.TokenLParen); err != nil {
		return nil, err
	}
	var terms []SortTerm
	for p.token.Type != lexer.TokenRParen {
		if p.token.Type == lexer.TokenEOF {
			return nil, p.endError()
		}
		descending := p.token.Type == lexer.TokenGT
		if descending || p.token.Type == lexer.TokenLT {
			if err := p.advance(); err != nil {
				return nil, err
			}
		}
		expr, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		terms = append(terms, SortTerm{Descending: descending, Expression: expr})
		if p.token.Type == lexer.TokenComma {
			if err := p.advance(); err != nil {
				return nil, err
			}
		} else if err := p.expect(lexer.TokenRParen); err != nil {
			return nil, err
		}
	}
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
