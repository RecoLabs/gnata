package parser

// BoolFastOp identifies a node of a BoolFastPath tree.
type BoolFastOp int

const (
	BoolFastLeaf BoolFastOp = iota
	BoolFastAnd
	BoolFastOr
	BoolFastNot
)

// BoolFastPath holds a pre-analyzed and / or / $not composition whose
// leaves are pure-path, comparison, or function fast paths.
type BoolFastPath struct {
	Op        BoolFastOp
	Left      *BoolFastPath
	Right     *BoolFastPath
	PurePath  string
	PureSteps []string
	Cmp       *ComparisonFastPath
	Func      *FuncFastPath
}

func tryCollectBool(node *Node) *BoolFastPath {
	if node == nil {
		return nil
	}
	switch {
	case node.Type == NodeBlock && len(node.Expressions) == 1:
		return tryCollectBool(node.Expressions[0])
	case node.Type == NodeBinary && (node.Value == "and" || node.Value == "or"):
		left := tryCollectBoolOperand(node.Left)
		if left == nil {
			return nil
		}
		right := tryCollectBoolOperand(node.Right)
		if right == nil {
			return nil
		}
		op := BoolFastAnd
		if node.Value == "or" {
			op = BoolFastOr
		}
		return &BoolFastPath{Op: op, Left: left, Right: right}
	case isNotCall(node):
		operand := tryCollectBoolOperand(node.Arguments[0])
		if operand == nil {
			return nil
		}
		return &BoolFastPath{Op: BoolFastNot, Left: operand}
	}
	return nil
}

func tryCollectBoolOperand(node *Node) *BoolFastPath {
	if composed := tryCollectBool(node); composed != nil {
		return composed
	}
	if paths, ok := collectPaths(node); ok && len(paths) == 1 {
		return &BoolFastPath{Op: BoolFastLeaf, PurePath: paths[0], PureSteps: rawStepNames(node)}
	}
	if cmp := tryCollectComparison(node); cmp != nil {
		return &BoolFastPath{Op: BoolFastLeaf, Cmp: cmp}
	}
	if fn := tryCollectFunc(node); fn != nil {
		return &BoolFastPath{Op: BoolFastLeaf, Func: fn}
	}
	return nil
}

func isNotCall(node *Node) bool {
	return node.Type == NodeFunction && node.Procedure != nil && node.Procedure.Type == NodeVariable &&
		node.Procedure.Value == "not" && len(node.Arguments) == 1
}
