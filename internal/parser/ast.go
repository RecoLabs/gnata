package parser

// Node represents one AST node produced by the parser. Every compiled
// expression keeps its whole tree, and most nodes are leaves (names,
// variables, strings), so a Node holds only what nearly every node uses;
// the fields of the less common node types sit in ext, allocated on first
// set and read through nil-safe accessors.
type Node struct {
	Type  NodeType
	flags nodeFlags
	Pos   int    // byte offset in source
	Value string // operator string, field name, variable name, or literal value

	// Binary operands
	Left  *Node
	Right *Node

	ext *nodeExt
}

// nodeExt holds the fields of compound nodes: paths, calls, blocks,
// conditions, lambdas, numbers and subscripts.
type nodeExt struct {
	numVal float64 // type="number"

	steps       []*Node // type="path": ordered list of path steps
	expressions []*Node // type="block" or type="unary"([)
	lhs         []*Node // type="unary"({) or type="binary"({): key-value pairs flat [k0,v0,k1,v1,...]
	arguments   []*Node // type="function"|"partial"|"lambda"
	body        *Node   // type="lambda"
	procedure   *Node   // type="function"|"partial"
	expression  *Node   // unary operand (-)
	condition   *Node
	then        *Node
	els         *Node // nil if no else branch
	base        *Node // a subscript's step (see StepBase)

	rare *nodeRare
}

// nodeRare holds the fields of node types and annotations that are rare
// in practice: sorts, transforms, signatures, focus/index bindings,
// ancestry and groups.
type nodeRare struct {
	terms     []SortTerm // type="sort"
	pattern   *Node      // type="transform"
	update    *Node
	delete    *Node      // nil if no delete clause
	signature *Signature // parsed lambda signature; nil if absent
	depth     uint16     // type="transform" or "lambda": how deeply evaluating its clauses or body recurses, saturating

	focus string // variable name bound by @
	index string // variable name bound by #

	// slot is the ancestor a % reads (type="parent"); ancestor is the slot a
	// name or wildcard step binds to its input for a later % (see ancestry.go).
	slot     *Slot
	ancestor *Slot

	group *GroupExpr // group expression attached to a path or step

	// seekingParent holds the % slots this node passes to the expression
	// around it (see ancestry.go).
	seekingParent []*Slot
}

// nodeFlags are the metadata flags set by processAST.
type nodeFlags uint16

const (
	flagKeepArray          nodeFlags = 1 << iota // step has [] suffix → force array output
	flagKeepSingletonArray                       // path has a keepArray step; a subscript or sort, on its Left chain (see ChainKeepsArray)
	flagConsArray                                // step is an array constructor used as a path step
	flagThunk                                    // call trampolined as a tail call, or ?: or ?? operator in a lambda's tail position
	flagTailContext                              // call is a jsonata-js tail call, taking the entering call's context
	flagTuple                                    // sort whose Left binds #$var, @$var or an ancestor; on a path, one wrapStep made
	flagRootContext                              // step starting a path over the root input (see markRootContext)
	flagPathStage                                // subscript applied as a stage of a path step (see markSubscriptStages)
	flagIndexKeepArray                           // the step's first [] comes right after its #$var binding
	flagKeptAfterIndex                           // a [] comes after the step's #$var binding (see wrapKeepsArray)
	flagNoBinds                                  // transform whose pattern, update and delete bind no variable
	flagTupleResult                              // block or path a % reaches into, which yields its tuples (see seekParent)
	flagIndexLast                                // # bound after any @ on this node (parser only)
)

// SortTerm is one term in a sort expression.
type SortTerm struct {
	Descending bool
	Expression *Node
}

// GroupExpr holds the key-value pairs of a group expression {}.
type GroupExpr struct {
	Pairs [][2]*Node // [key-expr, value-expr] pairs
	Pos   int
	// OnPath is set for a group applied to something jsonata-js makes a
	// path, which groups the whole path's result (see ProcessAST).
	OnPath bool
}

// Slot is the ancestor a % operator reads, as in jsonata-js: the evaluator
// binds Label, which no variable name can spell, to the input of the step
// resolution picked.
type Slot struct {
	Label string // "!N" for the expression's Nth %, or a label it shares
	Level int    // steps still to climb; 0 once resolved
}

// Signature is a lambda's type signature, as parsed by ParseSig.
type Signature struct {
	Params []ParamSpec // the parsed parameters of a signature such as "<s-n?:s>"
}

// NodeType identifies the kind of an AST node.
type NodeType uint8

// Node types.
const (
	NodePath NodeType = iota + 1
	NodeBinary
	NodeUnary
	NodeName
	NodeString
	NodeNumber
	NodeValue
	NodeVariable
	NodeWildcard
	NodeDescendant
	NodeParent
	NodeRegex
	NodeBlock
	NodeCondition
	NodeBind
	NodeFunction
	NodePartial
	NodeLambda
	NodeApply
	NodeTransform
	NodeSort
	NodePlaceholder // placeholder "?" in partial application
)

var nodeTypeNames = [...]string{
	NodePath:        "path",
	NodeBinary:      "binary",
	NodeUnary:       "unary",
	NodeName:        "name",
	NodeString:      "string",
	NodeNumber:      "number",
	NodeValue:       "value",
	NodeVariable:    "variable",
	NodeWildcard:    "wildcard",
	NodeDescendant:  "descendant",
	NodeParent:      "parent",
	NodeRegex:       "regex",
	NodeBlock:       "block",
	NodeCondition:   "condition",
	NodeBind:        "bind",
	NodeFunction:    "function",
	NodePartial:     "partial",
	NodeLambda:      "lambda",
	NodeApply:       "apply",
	NodeTransform:   "transform",
	NodeSort:        "sort",
	NodePlaceholder: "operator",
}

// String returns the jsonata-js name of the node type.
func (t NodeType) String() string {
	if int(t) < len(nodeTypeNames) {
		return nodeTypeNames[t]
	}
	return ""
}
