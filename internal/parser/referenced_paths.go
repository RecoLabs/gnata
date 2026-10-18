package parser

import "slices"

// maxPathNodes caps the distinct field paths ReferencedPaths tracks, and
// maxPathElements the field names it returns, so a deep and wide expression
// can't grow either without bound.
const (
	maxPathNodes    = 1 << 16
	maxPathElements = 1 << 16
)

// ReadsContext reports whether calling the function name with arguments of
// argTypes may read the call's context value. An argument's type is its
// signature symbol when it is a literal (s, n, b, l, a, o or f) and 0
// otherwise. ReferencedPaths asks it, once per name and argument types, only
// for names the expression doesn't bind itself.
type ReadsContext func(name string, argTypes []byte) bool

// ReferencedPaths returns the input fields an expression reads, as field
// names from the root of the input, each distinct path once in the order
// first seen. See gnata's Expression.ReferencedPaths for the semantics.
// complete is false when the expression may also read input it doesn't name.
func ReferencedPaths(ast *Node, readsContext ReadsContext) (paths [][]string, complete bool) {
	bound, unstable := boundVariables(ast)
	c := &pathCollector{
		root:         &pathNode{},
		readsContext: readsContext,
		contextReads: map[string]bool{},
		bindings:     map[string][]binding{},
		scopes:       [][]string{nil},
		unstable:     unstable,
		boundNames:   bound,
		complete:     true,
	}
	c.value(ast, c.root)
	if focusAndParent(ast) {
		// After a focus binding the context is the step's input again, so
		// a % may climb from a step other than the one it follows.
		c.complete = false
	}
	return c.result()
}

// pathNode is a field path from the root of the input. Paths share their
// prefixes, and a nil *pathNode is a value whose field reads are already
// recorded or that isn't the input's: reading a field of it records nothing.
type pathNode struct {
	name     string
	parent   *pathNode
	children map[string]*pathNode
	recorded bool
}

// binding is what a variable the expression binds holds.
type binding struct {
	path   *pathNode // the field path it holds; nil when not a field of the input
	lambda bool      // a function the expression defines
	// readsCaller: the lambda's body reads its caller's context, through
	// a tail call, which JSONata runs in the context of the call that
	// entered the lambda.
	readsCaller bool
	free        bool // any value, e.g. a lambda parameter
}

type pathCollector struct {
	root         *pathNode
	nodes        int
	recorded     []*pathNode
	readsContext ReadsContext
	contextReads map[string]bool
	// bindings holds each variable's bindings, innermost last, and scopes
	// the variables each open scope bound, so a lookup costs the same at
	// any depth.
	bindings map[string][]binding
	scopes   [][]string
	// lambdas holds, for each lambda body being walked, whether it reads
	// its caller's context; lastLambda is the last walked body's.
	lambdas    []bool
	lastLambda bool
	// boundNames are the variables the expression binds anywhere, and
	// unstable those a binding can't describe (see boundVariables).
	boundNames map[string]bool
	unstable   map[string]bool
	complete   bool
}

// value records the reads of n evaluated against ctx, including reading its
// result.
func (c *pathCollector) value(n *Node, ctx *pathNode) {
	c.read(c.eval(n, ctx))
}

// read records reading the whole value at path. Reading the whole input
// can't be described by field names.
func (c *pathCollector) read(path *pathNode) {
	switch {
	case path == nil:
	case path == c.root:
		c.complete = false
	case !path.recorded:
		path.recorded = true
		c.recorded = append(c.recorded, path)
	}
}

// eval records the reads of n evaluated against ctx, other than reading its
// result, and returns the path of its result.
func (c *pathCollector) eval(n *Node, ctx *pathNode) *pathNode {
	if n == nil {
		return nil
	}
	if n.Index() != "" {
		c.bind(n.Index(), binding{})
	}
	result := c.evalNode(n, ctx)
	if focus := n.Focus(); focus != "" {
		// A focus binding keeps the step's input as the context.
		c.read(result)
		c.bind(focus, binding{path: result})
		result = ctx
	}
	if group := n.Group(); group != nil {
		for _, pair := range group.Pairs {
			c.value(pair[0], result)
			c.value(pair[1], result)
		}
	}
	return result
}

func (c *pathCollector) evalNode(n *Node, ctx *pathNode) *pathNode {
	//nolint:exhaustive // strings, numbers, values, regexes and placeholders read nothing
	switch n.Type {
	case NodeName:
		return c.appendField(ctx, n.Value)
	case NodeVariable:
		return c.variable(n.Value, ctx)
	case NodePath:
		return c.path(n, ctx)
	case NodeWildcard, NodeDescendant:
		c.read(ctx)
	case NodeParent:
		if ctx == nil || ctx == c.root {
			// The parent may lie above what is recorded, or depend on how
			// the root was reached.
			c.complete = false
			return nil
		}
		c.read(ctx)
		return ctx.parent
	case NodeBinary:
		return c.binary(n, ctx)
	case NodeApply:
		return c.apply(c.eval(n.Left, ctx), n.Right, ctx)
	case NodeSort:
		elements := c.eval(n.Left, ctx)
		for _, term := range n.Terms() {
			c.value(term.Expression, elements)
		}
		return elements
	case NodeBlock:
		return c.block(n, ctx)
	case NodeBind:
		result := c.eval(n.Right, ctx)
		isLambda := n.Right.Type == NodeLambda
		c.bind(n.Left.Value, binding{path: result, lambda: isLambda, readsCaller: isLambda && c.lastLambda})
		return result
	case NodeFunction, NodePartial:
		c.call(n, ctx, false)
	case NodeLambda:
		if c.lambda(n, ctx) {
			// Called where it is defined, as a function argument is.
			c.read(ctx)
		}
	case NodeTransform:
		// Not applied here: a function value whose input is unknown.
		c.transform(n, nil)
	case NodeUnary, NodeCondition:
		c.value(n.Expression(), ctx)
		c.value(n.Condition(), ctx)
		c.value(n.Then(), ctx)
		c.value(n.Else(), ctx)
		c.values(n.Expressions(), ctx)
		c.values(n.LHS(), ctx)
	}
	return nil
}

func (c *pathCollector) values(nodes []*Node, ctx *pathNode) {
	for _, n := range nodes {
		c.value(n, ctx)
	}
}

// variable resolves a variable read against ctx. In a path, the parser
// names $ by "" and $$ by "$".
func (c *pathCollector) variable(name string, ctx *pathNode) *pathNode {
	switch name {
	case "":
		return ctx
	case "$":
		return c.root
	}
	b, ok := c.lookup(name)
	switch {
	case !ok || b.free:
		c.complete = false
		return nil
	case b.readsCaller:
		// Passed on, it may be called in this context.
		c.read(ctx)
	}
	return b.path
}

// path resolves each step against the result of the steps before it. Its
// focus and index bindings exist only for the rest of the path.
func (c *pathCollector) path(n *Node, ctx *pathNode) *pathNode {
	c.pushScope()
	defer c.popScope()
	cur := ctx
	for i, step := range n.Steps() {
		if i > 0 && !readsFields(step) && cur != c.root {
			// The steps so far still decide whether, and how many times,
			// this step runs.
			c.read(cur)
		}
		cur = c.eval(step, cur)
	}
	return cur
}

// readsFields reports whether a path step reads fields of its input, so the
// paths it records already cover how the steps before it decided it runs.
func readsFields(step *Node) bool {
	base := StepBase(step)
	//nolint:exhaustive // any other step reads its input some other way
	switch base.Type {
	case NodeName, NodeWildcard, NodeDescendant, NodeParent:
		return true
	case NodeVariable:
		// $ passes its input on unchanged.
		return base.Value == ""
	}
	return false
}

func (c *pathCollector) binary(n *Node, ctx *pathNode) *pathNode {
	switch n.Value {
	case "[":
		elements := c.eval(n.Left, ctx)
		if n.Right != nil && n.Right.Type != NodeNumber {
			c.value(n.Right, elements)
		}
		return elements
	case "~>":
		return c.apply(c.eval(n.Left, ctx), n.Right, ctx)
	}
	c.value(n.Left, ctx)
	c.value(n.Right, ctx)
	return nil
}

// apply records fn applied to input with ~>. ~> is right-associative, so
// a ~> f ~> g arrives as a ~> (f ~> g), which is g applied to f(a).
func (c *pathCollector) apply(input *pathNode, fn *Node, ctx *pathNode) *pathNode {
	switch {
	case fn == nil:
		return nil
	case fn.Type == NodeTransform:
		// A transform copies its whole input.
		c.read(input)
		c.transform(fn, input)
		return nil
	case fn.Type == NodeBinary && fn.Value == "~>":
		return c.apply(c.apply(input, fn.Left, ctx), fn.Right, ctx)
	case fn.Type == NodeFunction || fn.Type == NodePartial:
		c.read(input)
		c.call(fn, ctx, true)
		return nil
	}
	c.read(input)
	c.value(fn, ctx)
	return nil
}

// transform records a transform applied to input: its pattern reads input,
// and its update and delete read each object the pattern matched.
func (c *pathCollector) transform(n *Node, input *pathNode) {
	matched := c.eval(n.Pattern(), input)
	c.read(matched)
	c.value(n.Update(), matched)
	c.value(n.Delete(), matched)
}

// block evaluates its expressions in a scope of their own; its result is
// the last one's.
func (c *pathCollector) block(n *Node, ctx *pathNode) *pathNode {
	c.pushScope()
	defer c.popScope()
	exprs := n.Expressions()
	for i, expr := range exprs {
		switch {
		case i == len(exprs)-1:
			return c.eval(expr, ctx)
		case expr.Type == NodeBind:
			// A statement's value is dropped; the variable is read where
			// it is used.
			c.eval(expr, ctx)
		default:
			c.value(expr, ctx)
		}
	}
	return nil
}

// call records a function call against ctx. A piped call (~>) also gets the
// value on its left as its first argument. A function the call computes,
// from an array, an object, another call or an inline lambda, is treated as
// any other function: it is given the context.
func (c *pathCollector) call(n *Node, ctx *pathNode, piped bool) {
	c.values(n.Arguments(), ctx)
	proc := n.Procedure()
	if proc == nil {
		return
	}
	readsContext := true
	switch b, bound := c.lookup(proc.Value); {
	case proc.Type != NodeVariable:
		c.value(proc, ctx)
	case bound:
		if b.free || !b.lambda {
			c.complete = false
		}
		readsContext = b.readsCaller
	case c.boundNames[proc.Value]:
		// Bound later or elsewhere: a closure calls whatever the name holds
		// when it runs, not the builtin of that name.
	default:
		if proc.Value == "eval" {
			// Its expression can read anything, $$ and variables included.
			c.complete = false
		}
		argTypes := literalTypes(n.Arguments())
		if piped {
			argTypes = append([]byte{0}, argTypes...)
		}
		readsContext = n.Type != NodePartial && c.builtinReadsContext(proc.Value, argTypes)
	}
	if !readsContext {
		return
	}
	c.read(ctx)
	if n.TailContext() && len(c.lambdas) > 0 {
		c.lambdas[len(c.lambdas)-1] = true
	}
}

// builtinReadsContext is readsContext, asked once per name and argument
// types.
func (c *pathCollector) builtinReadsContext(name string, argTypes []byte) bool {
	key := name + "(" + string(argTypes)
	reads, ok := c.contextReads[key]
	if !ok {
		reads = c.readsContext(name, argTypes)
		c.contextReads[key] = reads
	}
	return reads
}

// literalTypes is the signature symbol of each literal argument, and 0 for
// any other.
func literalTypes(args []*Node) []byte {
	types := make([]byte, len(args))
	for i, arg := range args {
		switch {
		case arg.Type == NodeString:
			types[i] = 's'
		case arg.Type == NodeNumber:
			types[i] = 'n'
		case arg.Type == NodeValue && arg.Value == "null":
			types[i] = 'l'
		case arg.Type == NodeValue:
			types[i] = 'b'
		case arg.Type == NodeUnary && arg.Value == "[":
			types[i] = 'a'
		case arg.Type == NodeUnary && arg.Value == "{":
			types[i] = 'o'
		case arg.Type == NodeLambda, arg.Type == NodeRegex:
			types[i] = 'f'
		}
	}
	return types
}

// lambda records the reads of a lambda's body, which JSONata evaluates
// against the input the lambda is defined in, except a tail call, which
// takes its caller's. Its parameters hold any value. It returns whether the
// body reads its caller's context, also kept in lastLambda.
func (c *pathCollector) lambda(n *Node, ctx *pathNode) bool {
	c.pushScope()
	c.lambdas = append(c.lambdas, false)
	for _, param := range n.Arguments() {
		c.bind(param.Value, binding{free: true})
	}
	c.value(n.Body(), ctx)
	c.lastLambda = c.lambdas[len(c.lambdas)-1]
	c.lambdas = c.lambdas[:len(c.lambdas)-1]
	c.popScope()
	return c.lastLambda
}

func (c *pathCollector) pushScope() {
	c.scopes = append(c.scopes, nil)
}

func (c *pathCollector) popScope() {
	for _, name := range c.scopes[len(c.scopes)-1] {
		stack := c.bindings[name]
		c.bindings[name] = stack[:len(stack)-1]
	}
	c.scopes = c.scopes[:len(c.scopes)-1]
}

// bind binds name in the innermost scope. An unstable variable may hold
// another value where it is read, so it holds any value.
func (c *pathCollector) bind(name string, b binding) {
	if c.unstable[name] {
		b = binding{free: true}
	}
	c.bindings[name] = append(c.bindings[name], b)
	c.scopes[len(c.scopes)-1] = append(c.scopes[len(c.scopes)-1], name)
}

func (c *pathCollector) lookup(name string) (binding, bool) {
	stack := c.bindings[name]
	if len(stack) == 0 {
		return binding{}, false
	}
	return stack[len(stack)-1], true
}

// appendField returns the path extended by name, or nil once the collector
// tracks maxPathNodes paths, which leaves it incomplete.
func (c *pathCollector) appendField(path *pathNode, name string) *pathNode {
	if path == nil {
		return nil
	}
	if child, ok := path.children[name]; ok {
		return child
	}
	if c.nodes >= maxPathNodes {
		c.complete = false
		return nil
	}
	c.nodes++
	child := &pathNode{name: name, parent: path}
	if path.children == nil {
		path.children = map[string]*pathNode{}
	}
	path.children[name] = child
	return child
}

// result returns the recorded paths, up to maxPathElements field names.
func (c *pathCollector) result() (paths [][]string, complete bool) {
	elements := 0
	for _, node := range c.recorded {
		var path []string
		for n := node; n != c.root; n = n.parent {
			path = append(path, n.name)
		}
		elements += len(path)
		if elements > maxPathElements {
			return paths, false
		}
		slices.Reverse(path)
		paths = append(paths, path)
	}
	return paths, c.complete
}

// boundVariables returns the variables the expression binds anywhere, and
// those a binding can't describe where they are read: bound more than once
// anywhere (a closure sees whichever value is current when it runs), or
// bound by := other than as a statement of a block (in a predicate or a
// branch, which may not run).
func boundVariables(ast *Node) (bound, unstable map[string]bool) {
	counts := map[string]int{}
	unstable = map[string]bool{}
	var walk func(n, parent *Node)
	walk = func(n, parent *Node) {
		if n == nil {
			return
		}
		for _, name := range []string{n.Focus(), n.Index()} {
			if name != "" {
				counts[name]++
			}
		}
		//nolint:exhaustive // only binding nodes name variables
		switch n.Type {
		case NodeBind:
			counts[n.Left.Value]++
			if parent == nil || parent.Type != NodeBlock {
				unstable[n.Left.Value] = true
			}
		case NodeLambda:
			for _, param := range n.Arguments() {
				counts[param.Value]++
			}
		}
		for _, child := range childNodes(n) {
			walk(child, n)
		}
	}
	walk(ast, nil)
	bound = make(map[string]bool, len(counts))
	for name, count := range counts {
		bound[name] = true
		if count > 1 {
			unstable[name] = true
		}
	}
	return bound, unstable
}

// focusAndParent reports whether the expression has both a focus binding
// and a %.
func focusAndParent(ast *Node) bool {
	var focus, parent bool
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil || focus && parent {
			return
		}
		focus = focus || n.Focus() != ""
		parent = parent || n.Type == NodeParent
		for _, child := range childNodes(n) {
			walk(child)
		}
	}
	walk(ast)
	return focus && parent
}

// childNodes returns every node n evaluates. A subscript's base repeats its
// left side, so it isn't included.
func childNodes(n *Node) []*Node {
	children := []*Node{
		n.Left, n.Right, n.Body(), n.Procedure(), n.Expression(), n.Condition(),
		n.Then(), n.Else(), n.Pattern(), n.Update(), n.Delete(),
	}
	children = append(children, n.Steps()...)
	children = append(children, n.Expressions()...)
	children = append(children, n.LHS()...)
	children = append(children, n.Arguments()...)
	for _, term := range n.Terms() {
		children = append(children, term.Expression)
	}
	if group := n.Group(); group != nil {
		for _, pair := range group.Pairs {
			children = append(children, pair[0], pair[1])
		}
	}
	return children
}
