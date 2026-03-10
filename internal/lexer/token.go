package lexer

// TokenType identifies the category of a lexed token.
type TokenType int

const (
	// Terminal tokens
	TokenEOF      TokenType = iota
	TokenName               // bare identifier (not a keyword)
	TokenVariable           // $name — value is text after $; "" for $$
	TokenString             // "…" or '…'
	TokenNumber             // numeric literal
	TokenValue              // true | false | null
	TokenRegex              // /pattern/flags

	// Single-character operators
	TokenDot       // .  bp=75
	TokenLBracket  // [  bp=80
	TokenRBracket  // ]  bp=0
	TokenLBrace    // {  bp=70
	TokenRBrace    // }  bp=0
	TokenLParen    // (  bp=80
	TokenRParen    // )  bp=0
	TokenComma     // ,  bp=0
	TokenAt        // @  bp=80
	TokenHash      // #  bp=80
	TokenSemicolon // ;  bp=80
	TokenColon     // :  bp=80
	TokenQuestion  // ?  bp=20
	TokenPlus      // +  bp=50
	TokenMinus     // -  bp=50
	TokenStar      // *  bp=60
	TokenSlash     // /  bp=60
	TokenPercent   // %  bp=60
	TokenPipe      // |  bp=20
	TokenEquals    // =  bp=40
	TokenLT        // <  bp=40
	TokenGT        // >  bp=40
	TokenCaret     // ^  bp=40
	TokenAmp       // &  bp=50
	TokenBang      // !  bp=0
	TokenTilde     // ~  bp=0

	// Multi-character operators
	TokenStarStar // **  bp=60
	TokenDotDot   // ..  bp=20
	TokenAssign   // :=  bp=10
	TokenNE       // !=  bp=40
	TokenLE       // <=  bp=40
	TokenGE       // >=  bp=40
	TokenChain    // ~>  bp=40
	TokenElvis    // ?:  bp=40
	TokenCoalesce // ??  bp=40

	// Keywords
	TokenAnd // and  bp=30
	TokenOr  // or   bp=25
	TokenIn  // in   bp=40
)

// Token is a single lexed unit.
type Token struct {
	Type     TokenType
	Value    string  // raw string value
	NumVal   float64 // populated when Type==TokenNumber
	BoolVal  bool    // populated when Type==TokenValue and value is true/false
	IsNull   bool    // populated when Type==TokenValue and value is null
	RegexPat string  // populated when Type==TokenRegex
	RegexFlg string  // populated when Type==TokenRegex (flags; 'g' always appended)
	Pos      int     // byte offset in source
}
