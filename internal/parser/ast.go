package parser

// Node represents one AST node produced by the parser.
// Fields are populated based on Type; unused fields are zero-valued.
type Node struct {
	Type  string // node type constant (see NodeType* constants)
	Value string // operator string, field name, variable name, or literal value
	Pos   int    // byte offset in source

	// Numeric literal value (populated when Type=="number")
	NumVal float64

	// Path / steps
	Steps []*Node // type="path": ordered list of path steps

	// Compound nodes
	Expressions []*Node // type="block" or type="unary"([)
	LHS         []*Node // type="unary"({) or type="binary"({): key-value pairs flat [k0,v0,k1,v1,...]
	Arguments   []*Node // type="function"|"partial"|"lambda"
	Body        *Node   // type="lambda"
	Procedure   *Node   // type="function"|"partial"

	// Binary operands
	Left  *Node
	Right *Node

	// Unary operand (-)
	Expression *Node

	// Condition / then / else
	Condition *Node
	Then      *Node
	Else      *Node // nil if no else branch

	// Sort: type="sort"
	Terms []SortTerm

	// Transform: type="transform"
	Pattern *Node
	Update  *Node
	Delete  *Node // nil if no delete clause

	// Lambda signature
	Signature *Signature // parsed signature; nil if absent

	// Metadata flags set by processAST
	KeepArray          bool // step has [] suffix → force array output
	KeepSingletonArray bool // path has at least one keepArray step
	ConsArray          bool // step is an array constructor used as a path step
	Thunk              bool // lambda is a TCO thunk
	Tuple              bool // step participates in a tuple stream

	// Focus / index variable names (set by @ and # operators)
	Focus string // variable name bound by @
	Index string // variable name bound by #

	// Ancestor slot (set by % operator)
	Slot *Slot

	// Stages: predicates and index bindings attached to a step
	Stages []Stage

	// Group expression attached to a path or step
	Group *GroupExpr

	// SeekingParent: unresolved parent slots in this subtree
	SeekingParent []*Slot

	// NextFunction: name of the next function (for T1005 error)
	NextFunction string
}

// SortTerm is one term in a sort expression.
type SortTerm struct {
	Descending bool
	Expression *Node
}

// Stage is a predicate or index binding attached to a path step.
type Stage struct {
	Type       string // "filter" | "index"
	Expression *Node  // for "filter"
	VarName    string // for "index"
	Pos        int
}

// GroupExpr holds the key-value pairs of a group expression {}.
type GroupExpr struct {
	Pairs [][2]*Node // [key-expr, value-expr] pairs
	Pos   int
}

// Slot represents an ancestor reference created by the % operator.
type Slot struct {
	Label string // "!N" where N is the ancestry index
	Level int    // always 1
	Index int    // sequential index across all parent operators
}

// Signature is the parsed type signature of a lambda or built-in function.
// The full parsing logic lives in functions/signature.go; this type is shared.
type Signature struct {
	Raw string // original signature string e.g. "<s-n?:s>"
}

// Node type string constants.
const (
	NodePath        = "path"
	NodeBinary      = "binary"
	NodeUnary       = "unary"
	NodeName        = "name"
	NodeString      = "string"
	NodeNumber      = "number"
	NodeValue       = "value"
	NodeVariable    = "variable"
	NodeWildcard    = "wildcard"
	NodeDescendant  = "descendant"
	NodeParent      = "parent"
	NodeRegex       = "regex"
	NodeBlock       = "block"
	NodeCondition   = "condition"
	NodeBind        = "bind"
	NodeFunction    = "function"
	NodePartial     = "partial"
	NodeLambda      = "lambda"
	NodeApply       = "apply"
	NodeTransform   = "transform"
	NodeSort        = "sort"
	NodePlaceholder = "operator" // placeholder "?" in partial application
)
