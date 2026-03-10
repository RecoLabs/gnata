package parser

import (
	"strconv"
	"strings"
)

// NullJSON is the string "null" used when serializing JSONata null/undefined to JSON text.
const NullJSON = "null"

const (
	// RHSKind identifies the type of the right-hand operand in a comparison fast path.
	RHSKindString rhsKind = iota
	RHSKindNumber
	RHSKindBool
	RHSKindNull

	// FuncFastKind identifies which built-in function a FuncFastPath represents.
	FuncFastExists    FuncFastKind = iota // $exists(path)
	FuncFastContains                      // $contains(path, "literal")
	FuncFastString                        // $string(path)
	FuncFastBoolean                       // $boolean(path)
	FuncFastNumber                        // $number(path)
	FuncFastKeys                          // $keys(path)
	FuncFastDistinct                      // $distinct(path)
	FuncFastNot                           // $not(path)
	FuncFastLowercase                     // $lowercase(path)
	FuncFastUppercase                     // $uppercase(path)
	FuncFastTrim                          // $trim(path)
	FuncFastLength                        // $length(path)
	FuncFastType                          // $type(path)
	FuncFastAbs                           // $abs(path)
	FuncFastFloor                         // $floor(path)
	FuncFastCeil                          // $ceil(path)
	FuncFastRound                         // $round — reserved for iota stability; not in funcFastKinds (banker's rounding)
	FuncFastSqrt                          // $sqrt(path)
	FuncFastCount                         // $count(path)
	FuncFastReverse                       // $reverse(path)
	FuncFastSum                           // $sum(path)
	FuncFastMax                           // $max(path)
	FuncFastMin                           // $min(path)
	FuncFastAverage                       // $average(path)
)

// funcFastKinds maps function names (as stored by the parser, without the $
// prefix) to their FuncFastKind. Only single-argument functions are listed;
// $contains is handled separately because it takes two arguments.
var funcFastKinds = map[string]FuncFastKind{
	"exists":    FuncFastExists,
	"string":    FuncFastString,
	"boolean":   FuncFastBoolean,
	"number":    FuncFastNumber,
	"keys":      FuncFastKeys,
	"distinct":  FuncFastDistinct,
	"not":       FuncFastNot,
	"lowercase": FuncFastLowercase,
	"uppercase": FuncFastUppercase,
	"trim":      FuncFastTrim,
	"length":    FuncFastLength,
	"type":      FuncFastType,
	"abs":       FuncFastAbs,
	"floor":     FuncFastFloor,
	"ceil":      FuncFastCeil,
	// $round uses banker's rounding (round half to even) which is too complex
	// to replicate in the fast path. Let it fall through to the full evaluator.
	"sqrt":    FuncFastSqrt,
	"count":   FuncFastCount,
	"reverse": FuncFastReverse,
	"sum":     FuncFastSum,
	"max":     FuncFastMax,
	"min":     FuncFastMin,
	"average": FuncFastAverage,
}

type (
	rhsKind int

	FuncFastKind int

	// ComparisonFastPath holds a pre-analyzed simple comparison of the form:
	//
	//	<pure-path> = <literal>
	//	<pure-path> != <literal>
	//
	// where literal is a string, number, boolean, or null constant.
	// Both fields are populated at Compile() time so that EvalBytes can evaluate
	// the expression with a single gjson scan — no json.Unmarshal, no AST walk.
	ComparisonFastPath struct {
		LHSPath      string  // gjson path string for the left-hand operand
		Op           string  // "=" or "!="
		RHSKind      rhsKind // type of the right-hand literal
		RHSString    string  // valid when RHSKind == RHSKindString
		RHSNumber    float64 // valid when RHSKind == RHSKindNumber
		RHSNumberStr string  // pre-formatted RHSNumber for fast integer comparison
		RHSBool      bool    // valid when RHSKind == RHSKindBool
	}

	// FuncFastPath holds a pre-analyzed function call of the form $func(pure-path)
	// (or $contains(pure-path, "literal") for the two-arg variant).
	// Populated at Compile() time so that EvalBytes can evaluate the expression
	// with a single gjson scan — no json.Unmarshal, no AST walk.
	FuncFastPath struct {
		Kind   FuncFastKind
		Path   string // gjson path for the primary argument
		StrArg string // second string literal for $contains; empty for all others
	}

	// fastPathResult holds the result of analyzing an expression for fast-path eligibility.
	fastPathResult struct {
		// IsFastPath is true when the expression is a simple dotted path with no filters,
		// no functions, no operators — just field name navigation.
		IsFastPath bool
		// GJSONPaths is the set of GJSON path strings that this expression reads.
		// Only populated when IsFastPath is true.
		GJSONPaths []string
		// CmpFast is non-nil when the expression is a simple path-vs-literal comparison
		// that can be evaluated with a single gjson scan.
		CmpFast *ComparisonFastPath
		// FuncFast is non-nil when the expression is a supported built-in function
		// applied to a pure path (e.g. $exists(a.b), $lowercase(name)).
		FuncFast *FuncFastPath
	}
)

// AnalyzeFastPath examines an AST node and determines whether it qualifies
// for zero-copy GJSON fast-path evaluation.
//
// An expression is fast-path eligible if it is one of:
//   - A simple "name" node: "Account" → GJSON path "Account"
//   - A "path" node whose every step is a simple "name":
//     "Account.Name" → GJSON path "Account.Name"
//     "Account.Order.Product.Price" → "Account.Order.Product.Price"
//   - A binary "=" or "!=" where the LHS is a pure path and the RHS is a
//     string / number / boolean / null literal  →  ComparisonFastPath
//   - A supported built-in function applied to a pure path (e.g. $exists(a.b),
//     $lowercase(name), $contains(path, "literal"))  →  FuncFastPath
//
// An expression is NOT fast-path eligible if it contains:
//   - Binary operators (+, -, *, /, comparisons, etc.) other than the above
//   - Unsupported function calls or nested function arguments
//   - Array constructors or object constructors
//   - Predicates / filters ([...])
//   - Wildcards (*) or descendant operators (**)
//   - Lambda or partial application
//   - Sort, group, transform
//   - Condition (ternary)
//   - Variable references
//
// For eligible expressions, GJSONPaths contains the GJSON path string(s).
func AnalyzeFastPath(node *Node) fastPathResult {
	paths, ok := collectPaths(node)
	if ok {
		return fastPathResult{IsFastPath: true, GJSONPaths: paths}
	}
	if cmp := tryCollectComparison(node); cmp != nil {
		return fastPathResult{CmpFast: cmp}
	}
	if fn := tryCollectFunc(node); fn != nil {
		return fastPathResult{FuncFast: fn}
	}
	return fastPathResult{IsFastPath: false}
}

// tryCollectFunc returns a FuncFastPath when node is a call to a supported
// built-in function with a pure-path first argument. Returns nil when the
// pattern does not match.
func tryCollectFunc(node *Node) *FuncFastPath {
	if node == nil || node.Type != NodeFunction {
		return nil
	}
	if node.Procedure == nil || node.Procedure.Type != NodeVariable {
		return nil
	}
	name := node.Procedure.Value

	// $contains(path, "literal") — two-argument special case.
	if name == "contains" {
		if len(node.Arguments) != 2 {
			return nil
		}
		if node.Arguments[1] == nil || node.Arguments[1].Type != NodeString {
			return nil
		}
		paths, ok := collectPaths(node.Arguments[0])
		if !ok || len(paths) != 1 {
			return nil
		}
		return &FuncFastPath{
			Kind:   FuncFastContains,
			Path:   paths[0],
			StrArg: node.Arguments[1].Value,
		}
	}

	kind, ok := funcFastKinds[name]
	if !ok {
		return nil
	}
	if len(node.Arguments) != 1 {
		return nil
	}
	paths, pathOK := collectPaths(node.Arguments[0])
	if !pathOK || len(paths) != 1 {
		return nil
	}
	return &FuncFastPath{
		Kind: kind,
		Path: paths[0],
	}
}

// tryCollectComparison returns a ComparisonFastPath when node is a binary = or !=
// with a pure path on the left and a literal (string/number/bool/null) on the right.
// Returns nil when the pattern does not match.
func tryCollectComparison(node *Node) *ComparisonFastPath {
	if node == nil || node.Type != NodeBinary {
		return nil
	}
	op := node.Value
	if op != "=" && op != "!=" {
		return nil
	}
	if node.Left == nil || node.Right == nil {
		return nil
	}
	lhsPaths, ok := collectPaths(node.Left)
	if !ok || len(lhsPaths) != 1 {
		return nil
	}
	lhsPath := lhsPaths[0]

	switch node.Right.Type {
	case NodeString:
		return &ComparisonFastPath{
			LHSPath: lhsPath, Op: op,
			RHSKind: RHSKindString, RHSString: node.Right.Value,
		}
	case NodeNumber:
		return &ComparisonFastPath{
			LHSPath: lhsPath, Op: op,
			RHSKind: RHSKindNumber, RHSNumber: node.Right.NumVal,
			RHSNumberStr: strconv.FormatFloat(node.Right.NumVal, 'f', -1, 64),
		}
	case NodeValue:
		switch node.Right.Value {
		case "true":
			return &ComparisonFastPath{LHSPath: lhsPath, Op: op, RHSKind: RHSKindBool, RHSBool: true}
		case "false":
			return &ComparisonFastPath{LHSPath: lhsPath, Op: op, RHSKind: RHSKindBool, RHSBool: false}
		case "null":
			return &ComparisonFastPath{LHSPath: lhsPath, Op: op, RHSKind: RHSKindNull}
		}
	}
	return nil
}

// collectPaths returns the GJSON paths for the node if it is fast-path eligible.
func collectPaths(node *Node) ([]string, bool) {
	if node == nil {
		return nil, false
	}
	switch node.Type {
	case NodeName:
		// Simple field name — GJSON path is just the name.
		if len(node.Stages) > 0 || node.Group != nil || node.Focus != "" {
			return nil, false
		}
		escaped, ok := gjsonEscapeName(node.Value)
		if !ok {
			return nil, false
		}
		return []string{escaped}, true

	case NodePath:
		// All steps must be simple name nodes with no predicates/stages.
		parts := make([]string, 0, len(node.Steps))
		for _, step := range node.Steps {
			if step.Type != NodeName {
				return nil, false
			}
			if len(step.Stages) > 0 || step.Group != nil || step.Focus != "" {
				return nil, false
			}
			if step.KeepArray || step.ConsArray {
				return nil, false
			}
			escaped, ok := gjsonEscapeName(step.Value)
			if !ok {
				return nil, false
			}
			parts = append(parts, escaped)
		}
		if len(parts) == 0 {
			return nil, false
		}
		return []string{strings.Join(parts, ".")}, true

	case NodeNumber, NodeString, NodeValue:
		// Constants — let the full evaluator handle cheaply.
		return nil, false

	case NodeVariable:
		// Variable reference — not a JSON path.
		return nil, false

	default:
		return nil, false
	}
}

// gjsonEscapeName escapes a field name for use in a GJSON path.
// GJSON uses dot as separator; names containing special chars must be escaped with backticks.
// Returns ("", false) if the name cannot be safely represented in a GJSON path.
func gjsonEscapeName(name string) (string, bool) {
	// gjson interprets @ as a modifier prefix even inside backtick-escaped names
	// (e.g., `@odata.count` is treated as modifier @odata). Field names starting
	// with @ must be excluded from the fast path entirely.
	if strings.HasPrefix(name, "@") {
		return "", false
	}
	if strings.ContainsAny(name, ".*?|#[]!{}\\") || strings.Contains(name, " ") {
		return "`" + strings.ReplaceAll(name, "`", "\\`") + "`", true
	}
	return name, true
}
