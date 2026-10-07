package evaluator

import (
	"errors"
	"fmt"
	"slices"

	"github.com/recolabs/gnata/internal/parser"
)

func evalFunction(node *parser.Node, input any, env *Environment) (any, error) {
	result, err := evalFunctionSequence(node, input, env)
	if err != nil {
		return nil, err
	}
	return CollapseAndKeep(result, node.KeepArray), nil
}

// evalFunctionSequence calls the function node without collapsing a
// *Sequence result, so a predicate on the call ($filter(...)[0]) sees the
// sequence's items, matching jsonata-js.
func evalFunctionSequence(node *parser.Node, input any, env *Environment) (any, error) {
	fn, err := evalCallee(node.Procedure, input, env)
	if err != nil {
		return nil, err
	}

	args := make([]any, 0, len(node.Arguments))
	var shape argShape
	// plain marks the arguments a lambda binds as plain arrays (see
	// plainArgs).
	var plain plainArgs
	// A function argument wrapping a lambda applies it with a null context,
	// as the call does here, so that it binds the marked parameters.
	target, wrapped := wrappedLambda(fn)
	lambdaCall := wrapped || isLambda(fn)
	for i, argNode := range node.Arguments {
		if argNode.Type == parser.NodePlaceholder {
			args = append(args, nil)
			continue
		}
		var val any
		if i == 0 || lambdaCall && i < maxPlainArgs {
			var valShape argShape
			val, valShape, err = evalShapedArg(argNode, input, env)
			if i == 0 {
				shape = valShape
			}
			if lambdaCall && err == nil && multiItemArray(val) {
				plain = plain.with(i, val, valShape)
			}
		} else {
			val, err = Eval(argNode, input, env)
		}
		if err != nil {
			return nil, err
		}
		// jsonata-js collapses a sequence once its expression ends.
		if seq, ok := val.(*Sequence); ok {
			val = CollapseSequence(seq)
		}
		args = append(args, val)
	}

	// jsonata-js runs a tail call from the trampoline of the call that
	// entered the lambda, so it takes that call's context.
	callFocus := input
	if node.TailContext {
		callFocus = env.callCounter().applyFocus
	}

	// jsonata-js applies a function argument with a null context, except in
	// a tail call, whose arguments its trampoline passes as they are. Named
	// builtins already apply function arguments with a null context.
	if !node.TailContext && reachesLambda(fn) {
		for i, arg := range args {
			args[i] = wrapFunctionArg(arg, env)
		}
	}

	// Tail-call optimization: if this call is in tail position within a
	// lambda body, return a TailCall sentinel instead of recursing, which
	// invokeFunction applies (see TailCall). A jsonata-js tail call to a
	// built-in is one too, so its result is returned uncollapsed.
	if node.Thunk {
		if wrapped {
			return &TailCall{Fn: target, Args: args, Focus: Null, Plain: plain}, nil
		}
		if lambdaCall {
			return &TailCall{Fn: fn, Args: args, Focus: callFocus, Plain: plain}, nil
		}
		if node.TailContext {
			if len(args) > 0 {
				plain = plain.with(0, args[0], shape)
			}
			return &TailCall{Fn: fn, Args: args, Focus: callFocus, Plain: plain}, nil
		}
	}

	var result any
	if wrapped {
		result, err = invokeFunction(target, args, plain, Null, env, true)
	} else {
		result, err = invokeFunction(fn, args, plain, callFocus, env, true)
	}
	// A lambda's ArgShaped result is its tail call's, shaped there.
	if seq, ok := result.(*Sequence); ok && seq.ArgShaped && !lambdaCall && !shape.sequence() {
		return slices.Clip(seq.Values), err
	}
	return result, err
}

// evalCallee evaluates a call's procedure, nil for none.
func evalCallee(procedure *parser.Node, input any, env *Environment) (any, error) {
	if procedure == nil {
		return nil, nil
	}
	fn, err := Eval(procedure, input, env)
	if err != nil {
		// When % is used as a function callee (e.g., %(1)), the parent
		// context error should become T1006 (not a function).
		je := &JSONataError{}
		if !errors.As(err, &je) || je.Code != "S0217" {
			return nil, err
		}
		fn = nil
	}
	// For NodeName procedures (without $): if input lookup yields nil but the name
	// IS in env, return T1005 ("the function has no definition" — accessed without $).
	// If it's completely unknown (not in env either), return T1006.
	if fn == nil && procedure.Type == parser.NodeName {
		if envFn, found := env.Lookup(procedure.Value); found && envFn != nil {
			return nil, &JSONataError{
				Code:    "T1005",
				Message: fmt.Sprintf("attempted to invoke a function that has no definition: %s", procedure.Value),
			}
		}
	}
	return fn, nil
}

// plainArgs marks, by position, the arguments of a call that are plain
// arrays in jsonata-js (see argShape): a lambda marks its parameters bound
// to them (see Environment.markPlainArray), and an ArgShaped built-in
// result keeps its first argument's shape. Arguments from maxPlainArgs on
// are not marked, so they count as sequences.
type plainArgs uint64

const maxPlainArgs = 64

// with returns p marking argument i, val of shape valShape, when it is a
// plain array of two or more items.
func (p plainArgs) with(i int, val any, valShape argShape) plainArgs {
	if multiItemArray(val) && !valShape.sequence() {
		return p | 1<<i
	}
	return p
}

func (p plainArgs) has(i int) bool {
	return i < maxPlainArgs && p&(1<<i) != 0
}

// argShape records whether a call's first argument is a sequence in
// jsonata-js, for a built-in whose result takes that shape (see
// Sequence.ArgShaped): $distinct keeps a plain array a plain array but
// collapses a sequence. An array constructor, a sort, a pick of one array
// item, a field that one object holds as an array, a call returning
// anything but a sequence, and a variable a bind set to one of these are
// plain arrays; anything else counts as a sequence, a function parameter
// included, since gnata keeps no sequence mark on argument values.
type argShape struct {
	plain  bool
	last   *parser.Node // a path's last step whose input decides (see lastStepSequence), or a variable
	lastIn any
	env    *Environment // the scope a variable in last is read from
}

func (a argShape) sequence() bool {
	switch {
	case a.env != nil:
		return !a.env.boundPlainArray(a.last.Value)
	case a.last != nil:
		return lastStepSequence(a.last, a.lastIn)
	}
	return !a.plain
}

// multiItemArray reports whether v is an array whose shape $distinct can
// see: one with at most one item it returns as is.
func multiItemArray(v any) bool {
	items, ok := AsArray(v)
	return ok && len(items) > 1
}

// evalShapedArg evaluates a call's first argument and its shape.
func evalShapedArg(node *parser.Node, input any, env *Environment) (any, argShape, error) {
	if node.Group != nil || node.KeepArray && !keepsShape(node) {
		v, err := Eval(node, input, env)
		return v, argShape{}, err
	}
	switch node.Type {
	case parser.NodeFunction:
		raw, err := evalFunctionSequence(node, input, env)
		_, seq := raw.(*Sequence)
		return CollapseAndKeep(raw, false), argShape{plain: !seq}, err
	case parser.NodeUnary, parser.NodeSort:
		v, err := Eval(node, input, env)
		return v, argShape{plain: node.Type == parser.NodeSort || node.Value == "["}, err
	case parser.NodeVariable:
		v, err := Eval(node, input, env)
		if node.Value == "" || node.Value == "$" {
			// The context and the root input are one item each, not
			// sequences.
			return v, argShape{plain: true}, err
		}
		return v, argShape{last: node, env: env}, err
	case parser.NodeBind:
		return evalShapedBind(node, input, env)
	case parser.NodeCondition:
		return evalShapedCondition(node, input, env)
	case parser.NodeBinary:
		switch node.Value {
		case "?:", "??":
			return evalShapedDefault(node, input, env)
		case "~>":
			return evalShapedChain(node, input, env)
		case "[":
			if !subscriptsName(node) || !isContextArray(input, env) {
				return evalShapedSubscript(node, input, env)
			}
		}
		v, err := Eval(node, input, env)
		return v, argShape{last: node, lastIn: input}, err
	case parser.NodeName:
		v, err := Eval(node, input, env)
		return v, argShape{last: node, lastIn: input}, err
	case parser.NodePath:
		if pathHasTupleStep(node.Steps) {
			v, err := Eval(node, input, env)
			return v, argShape{}, err
		}
		v, lastIn, err := evalPathSimpleLast(node, input, env)
		return v, argShape{last: node.Steps[len(node.Steps)-1], lastIn: lastIn}, err
	case parser.NodeBlock:
		return evalShapedBlock(node, input, env)
	}
	v, err := Eval(node, input, env)
	return v, argShape{}, err
}

// keepsShape reports whether a [] on node leaves its shape (see argShape):
// jsonata-js's [] makes only a constructed array, the value of an array
// constructor step, a sequence.
func keepsShape(node *parser.Node) bool {
	switch node.Type {
	case parser.NodeName, parser.NodeUnary, parser.NodeVariable:
		return true
	case parser.NodeBinary:
		return node.Value == "["
	}
	return false
}

// evalShapedBlock is evalBlock, taking the shape of its last expression.
func evalShapedBlock(node *parser.Node, input any, env *Environment) (any, argShape, error) {
	if len(node.Expressions) == 0 {
		return nil, argShape{}, nil
	}
	childEnv := NewChildEnvironment(env)
	last := len(node.Expressions) - 1
	for _, expr := range node.Expressions[:last] {
		if _, err := Eval(expr, input, childEnv); err != nil {
			return nil, argShape{}, err
		}
	}
	v, shape, err := evalShapedArg(node.Expressions[last], input, childEnv)
	return settleRaw(v, false), shape, err
}

// evalShapedBind is evalBind, taking the shape of its right side, which it
// marks on the variable when that holds a plain array.
func evalShapedBind(node *parser.Node, input any, env *Environment) (any, argShape, error) {
	val, shape, err := evalShapedArg(node.Right, input, env)
	if err != nil {
		return nil, argShape{}, err
	}
	if seq, ok := val.(*Sequence); ok {
		val = CollapseSequence(seq)
	}
	plain := multiItemArray(val) && !shape.sequence()
	env.Bind(node.Left.Value, val)
	if plain {
		env.markPlainArray(node.Left.Value)
	}
	return settleRaw(val, false), argShape{plain: plain}, nil
}

// evalShapedCondition is evalCondition, taking the shape of the branch it
// evaluates.
func evalShapedCondition(node *parser.Node, input any, env *Environment) (any, argShape, error) {
	cond, err := Eval(node.Condition, input, env)
	if err != nil {
		return nil, argShape{}, err
	}
	truthy, err := ToBooleanEnv(cond, env)
	if err != nil {
		return nil, argShape{}, err
	}
	branch := node.Then
	if !truthy {
		branch = node.Else
	}
	if branch == nil {
		return nil, argShape{}, nil
	}
	v, shape, err := evalShapedArg(branch, input, env)
	return settleRaw(v, false), shape, err
}

// evalShapedDefault is the ?: or ?? operator, taking the shape of the side
// it returns.
func evalShapedDefault(node *parser.Node, input any, env *Environment) (any, argShape, error) {
	if node.Thunk {
		v, err := Eval(node, input, env)
		return v, argShape{}, err
	}
	left, shape, err := evalShapedArg(node.Left, input, env)
	if err != nil {
		return nil, argShape{}, err
	}
	keep := left != nil
	if node.Value == "?:" {
		if keep, err = ToBooleanEnv(left, env); err != nil {
			return nil, argShape{}, err
		}
	}
	if keep {
		return settleRaw(left, false), shape, nil
	}
	right, shape, err := evalShapedArg(node.Right, input, env)
	return settleRaw(right, false), shape, err
}

// evalShapedChain is the ~> operator, taking the shape of its result.
func evalShapedChain(node *parser.Node, input any, env *Environment) (any, argShape, error) {
	left, shape, err := evalShapedArg(node.Left, input, env)
	if err != nil {
		return nil, argShape{}, err
	}
	return evalChain(node.Right, left, shape, input, env)
}

// evalShapedSubscript is evalSubscriptValue, taking the shape of the
// selection: a filter selecting one array item returns that item whole.
func evalShapedSubscript(node *parser.Node, input any, env *Environment) (any, argShape, error) {
	raw, err := evalSubscriptStage(node, input, env)
	if seq, ok := raw.(*Sequence); ok && err == nil {
		return CollapseSequence(seq), argShape{plain: len(seq.Values) == 1}, nil
	}
	return raw, argShape{last: node, lastIn: input}, err
}

// callShaped calls fn with args, the first of shape first, as a call
// expression does, returning the collapsed result and its shape (see
// argShape).
func callShaped(fn any, args []any, first argShape, focus any, env *Environment, keepArray bool) (any, argShape, error) {
	var plain plainArgs
	lambdaCall := isLambda(fn)
	if lambdaCall && len(args) > 0 {
		plain = plain.with(0, args[0], first)
	}
	result, err := invokeFunction(fn, args, plain, focus, env, true)
	if err != nil {
		return nil, argShape{}, err
	}
	seq, isSeq := result.(*Sequence)
	if isSeq && seq.ArgShaped && !lambdaCall && !first.sequence() {
		return slices.Clip(seq.Values), argShape{plain: true}, nil
	}
	return CollapseAndKeep(result, keepArray), argShape{plain: !isSeq}, nil
}

// lastStepSequence reports whether a path whose last step is step, mapped
// over in, yields a sequence in jsonata-js. It does unless the step,
// evaluated for exactly one context item, returns a plain array, which
// evaluateStep returns as is: a field that the item holds as a plain array
// (or that exactly one of several items holds), a step building a plain
// array, $ of an array item, a wildcard over an object with an array value
// (whose values jsonata-js concatenates into a plain array), or a block
// ending in one of these; or it is a number-literal pick from one item,
// which returns an array item whole. A filter always yields a sequence.
func lastStepSequence(step *parser.Node, in any) bool {
	items, mapped := in.([]any)
	item, oneItem := in, !mapped
	if mapped && len(items) == 1 {
		item, oneItem = items[0], true
	}
	switch {
	case step.Index != "" || step.Focus != "":
		return true
	case step.Type == parser.NodeBinary && step.Value == "[":
		return step.Right.Type != parser.NodeNumber || mapped && len(items) > 1
	case step.Type == parser.NodeSort:
		return false
	case plainArrayStep(step):
		// [] makes a constructor step's array, not a block's, a sequence.
		return step.KeepArray && step.Type == parser.NodeUnary || mapped && len(items) != 1
	case step.Type == parser.NodeVariable && step.Value == "":
		return !oneItem || !IsArray(item)
	case step.Type == parser.NodeWildcard:
		return !oneItem || !holdsArrayValue(item)
	case step.Type == parser.NodeBlock && len(step.Expressions) > 0:
		last := step.Expressions[len(step.Expressions)-1]
		if !oneItem {
			return true
		} else if last.Type == parser.NodeVariable && last.Value == "" {
			// The block's $ is the whole item, which a field inside it maps over.
			return !IsArray(item)
		}
		return lastStepSequence(last, item)
	case step.Type != parser.NodeName:
		return true
	case !mapped:
		return false
	}
	defined, plain := 0, false
	for _, item := range items {
		if _, nested := AsArray(item); nested {
			return true
		}
		if val, found := MapGet(item, step.Value); found {
			defined++
			if defined > 1 {
				return true
			}
			_, plain = val.([]any)
		}
	}
	return !plain
}

// holdsArrayValue reports whether v is an object with an array value.
func holdsArrayValue(v any) bool {
	found := false
	MapRange(v, func(_ string, val any) bool {
		found = IsArray(val)
		return !found
	})
	return found
}

// plainArrayStep reports whether step builds one plain array per context
// item: an array constructor, or a block ending in one.
func plainArrayStep(step *parser.Node) bool {
	for step.Type == parser.NodeBlock && len(step.Expressions) > 0 {
		step = step.Expressions[len(step.Expressions)-1]
	}
	return step.Type == parser.NodeUnary && step.Value == "["
}

func evalLambda(node *parser.Node, input any, env *Environment) (any, error) {
	params := make([]string, 0, len(node.Arguments))
	for _, arg := range node.Arguments {
		params = append(params, arg.Value)
	}
	var signature *Signature
	if node.Signature != nil {
		signature = compileSignature(node.Signature.Params)
	}
	return &Lambda{
		Params:        params,
		Body:          node.Body,
		Closure:       env,
		Thunk:         node.Thunk,
		Signature:     signature,
		CapturedFocus: input,
		DeepBody:      bodyCost(node.Depth),
	}, nil
}

// lambdaFreeDepth is how deeply the body of a lambda may nest before each
// call spends the nesting budget, one unit per levelsPerNestedCall levels
// beyond; the call depth alone bounds recursion with shallower bodies.
// The free levels of the default 100 calls, 3,200, plus a full budget at 3
// levels per unit, 4,500, stay near half of the 16,000 levels of the
// costliest body found, grouping constructors, at which 100 calls overflow
// a grown stack under js/wasm in Node with go_js_wasm_exec's 8 MB stack.
const (
	lambdaFreeDepth     = 32
	levelsPerNestedCall = 3
)

// bodyCost returns the nesting budget a call spends for a lambda body as
// deep as depth.
func bodyCost(depth uint16) int {
	return max(int(depth)-lambdaFreeDepth, 0) / levelsPerNestedCall
}

// evalBody evaluates the body of f, a lambda called with env, spending its
// nesting budget while it runs.
func evalBody(f *Lambda, env *Environment, counter *callCounter) (any, error) {
	if f.DeepBody == 0 {
		return Eval(f.Body, f.CapturedFocus, env)
	}
	return counter.callNested(f.DeepBody, func() (any, error) {
		return Eval(f.Body, f.CapturedFocus, env)
	})
}

func evalPartial(node *parser.Node, input any, env *Environment) (any, error) {
	fn, err := Eval(node.Procedure, input, env)
	if err != nil {
		return nil, err
	}
	// For NodeName procedures (without $), distinguish T1007 vs T1008:
	// - if the name is in env (it's a function reference without $), → T1007
	// - if completely unknown → T1008
	if fn == nil && node.Procedure != nil && node.Procedure.Type == parser.NodeName {
		if envFn, found := env.Lookup(node.Procedure.Value); found && envFn != nil {
			return nil, &JSONataError{Code: "T1007", Message: "attempted to partially apply a function referenced without $"}
		}
	}
	if fn == nil {
		return nil, &JSONataError{Code: "T1008", Message: "cannot partially apply a non-function: the function is not defined"}
	}
	if !IsFunction(fn) {
		return nil, &JSONataError{Code: "T1008", Message: fmt.Sprintf("cannot partially apply a non-function: %T", fn)}
	}

	boundArgs := make([]any, len(node.Arguments))
	isPlaceholder := make([]bool, len(node.Arguments))
	placeholders := 0
	for i, argNode := range node.Arguments {
		if argNode.Type == parser.NodePlaceholder {
			isPlaceholder[i] = true
			placeholders++
		} else {
			val, err := Eval(argNode, input, env)
			if err != nil {
				return nil, err
			}
			boundArgs[i] = val
		}
	}

	// jsonata-js makes a partial application a lambda with one parameter per
	// placeholder, and applies the function it wraps without validating its
	// arguments. It applies a built-in without a context, while a lambda's
	// tail call takes the context the partial application is called with.
	// It also applies a built-in to exactly the parameters its implementation
	// declares, dropping the arguments past them and leaving the rest
	// undefined; it reads one that declares none, such as the variadic
	// $zip, as declaring one.
	keepsFocus := reachesLambda(fn)
	if arity, known := FunctionArity(fn); known && !keepsFocus {
		arity = max(arity, 1)
		for _, placeholder := range isPlaceholder[min(arity, len(isPlaceholder)):] {
			if placeholder {
				placeholders--
			}
		}
		boundArgs = resize(boundArgs, arity)
		isPlaceholder = resize(isPlaceholder, arity)
	}
	partial := &SignedBuiltin{
		Fn: func(args []any, focus any, _ *Environment) (any, error) {
			fullArgs := slices.Clone(boundArgs)
			argIdx := 0
			for i, placeholder := range isPlaceholder {
				if placeholder && argIdx < len(args) {
					fullArgs[i] = args[argIdx]
					argIdx++
				}
			}
			if !keepsFocus {
				focus = nil
			}
			return invokeFunction(fn, fullArgs, 0, focus, env, false)
		},
		Arity:        placeholders,
		CallsLambdas: isLambda(fn),
	}
	return partial, nil
}

// resize returns the first n items of s, padded with zero values.
func resize[T any](s []T, n int) []T {
	resized := make([]T, n)
	copy(resized, s)
	return resized
}

// wrapFunctionArg wraps a function passed as an argument to a lambda so it
// is applied with a null context, as jsonata-js applies the closure it wraps
// function arguments in.
func wrapFunctionArg(arg any, env *Environment) any {
	if !isCallable(arg) {
		return arg
	}
	sb, isSigned := arg.(*SignedBuiltin)
	if isSigned && sb.Argument != nil {
		return arg
	}
	arity, known := FunctionArity(arg)
	if !known {
		arity = -1
	}
	return &SignedBuiltin{
		Fn: func(args []any, _ any, _ *Environment) (any, error) {
			return callFunction(arg, args, Null, env)
		},
		Arity:    arity,
		Argument: arg,
		// A partial application or composition of lambdas calls only
		// lambdas too, and as no wrapper wraps another argument wrapper, a
		// chain of these stays two wrappers long.
		CallsLambdas: isLambda(arg) || isSigned && sb.CallsLambdas,
	}
}

// FunctionArity returns the number of parameters fn declares: a lambda's
// parameters, or a builtin's, partial application's or composition's
// jsonata-js arity; a regex takes the value and index, like jsonata-js's
// regex closure. known is false for a custom function, whose arity gnata
// cannot see.
func FunctionArity(fn any) (arity int, known bool) {
	switch f := fn.(type) {
	case *Lambda:
		return len(f.Params), true
	case *RegexLiteral:
		return CallRegexArity, true
	case *SignedBuiltin:
		return f.Arity, f.Arity >= 0
	}
	return 0, false
}

// reachesLambda reports whether calling fn can evaluate a lambda body with
// its arguments: fn is a lambda, or a partial application, composition or
// function argument (a SignedBuiltin without a name).
func reachesLambda(fn any) bool {
	switch f := fn.(type) {
	case *Lambda:
		return true
	case *SignedBuiltin:
		return f.isWrapper()
	}
	return false
}

// isCallable reports whether v is a function value other than a regex,
// which IsFunction also counts. A function argument wrapper must not wrap a
// regex, since $match, $replace and $split take a regex by its type; and a
// transform rejects a regex bound to $clone, which jsonata-js calls and
// fails on with a JavaScript error rather than a JSONata one.
func isCallable(v any) bool {
	switch v.(type) {
	case BuiltinFunction, EnvAwareBuiltin, *Lambda, *SignedBuiltin:
		return true
	}
	return false
}

func isLambda(fn any) bool {
	_, ok := fn.(*Lambda)
	return ok
}

// wrappedLambda returns the lambda that fn wraps as a function argument.
func wrappedLambda(fn any) (*Lambda, bool) {
	sb, isSigned := fn.(*SignedBuiltin)
	if !isSigned {
		return nil, false
	}
	lambda, ok := sb.Argument.(*Lambda)
	return lambda, ok
}

// stackOverflowError reports the recursion-depth error for the given counter,
// using D1011 when the limit came from the WithStack guardrail and the
// built-in U1001 otherwise.
func stackOverflowError(counter *callCounter) error {
	if counter.flags&stackIsLimit != 0 {
		return &JSONataError{
			Code:    "D1011",
			Message: fmt.Sprintf("Stack overflow error: stack depth exceeded %d. Check for non-terminating recursive function", counter.max),
		}
	}
	return &JSONataError{Code: "U1001", Message: fmt.Sprintf("stack overflow error: evaluation exceeded stack depth %d", counter.max)}
}

// evalLambdaBody evaluates f's body for one call, counting its depth. The
// body's context is the one f was defined in, and focus, the call's
// context, is what a jsonata-js tail call in it takes (see TailContext).
// The result is a lambda's TailCall for the trampoline, or the body's
// value: a built-in tail call runs here while the depth is counted, so one
// calling back into a lambda stays within the stack limit.
func evalLambdaBody(f *Lambda, args []any, plain plainArgs, focus any, env *Environment, counter *callCounter) (any, error) {
	counter.depth++
	outerFocus := counter.applyFocus
	defer func() { counter.depth--; counter.applyFocus = outerFocus }()
	if counter.depth > counter.max {
		return nil, stackOverflowError(counter)
	}
	childEnv := NewChildEnvironment(f.Closure)
	childEnv.calls = counter
	for i, param := range f.Params {
		if i < len(args) {
			childEnv.Bind(param, args[i])
			if plain.has(i) {
				childEnv.markPlainArray(param)
			}
		} else {
			childEnv.Bind(param, nil)
		}
	}
	counter.applyFocus = focus
	result, err := evalBody(f, childEnv, counter)
	if tc, isTail := result.(*TailCall); isTail && err == nil {
		if _, isLambda := tc.Fn.(*Lambda); !isLambda {
			result, err := callFunction(tc.Fn, tc.Args, tc.Focus, env)
			if seq, ok := result.(*Sequence); ok && seq.ArgShaped && tc.Plain.has(0) {
				return slices.Clip(seq.Values), err
			}
			return result, err
		}
	}
	return result, err
}

// builtinResult returns the result of a builtin called with args, each
// typed array argument (see typedArray) passed as a plain array (see
// plainArrayArgs). jsonata-js passes the array object itself, so a builtin
// that returns an argument unchanged returns it as is. An ArgShaped result
// stays an array when the first argument is a constructed array, which
// jsonata-js never holds as a sequence, however it reached the call (a
// piped o.[b,b] ~> $distinct() included).
// typed reports that args held a typed array, without which the result
// stays as it is.
func builtinResult(args []any, typed bool, result any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if !typed {
		return result, nil
	}
	if seq, ok := result.(*Sequence); ok && seq.ArgShaped && len(args) > 0 {
		if _, cons := args[0].(ConsArray); cons {
			return slices.Clip(seq.Values), nil
		}
	}
	if arr, ok := result.([]any); ok {
		for _, arg := range args {
			if typed, ok := typedArray(arg); ok && sameArray(typed, arr) {
				return arg, nil
			}
		}
	}
	return result, nil
}

// sameArray reports whether a and b are the same slice, even when empty:
// $reverse of an empty raw sequence returns that sequence.
func sameArray(a, b []any) bool {
	return len(a) == len(b) && cap(a) > 0 && cap(b) > 0 && &a[:1][0] == &b[:1][0]
}

// plainArrayArgs returns args with each top-level typed array as a plain
// []any, copying args only when one is found, which copied reports.
func plainArrayArgs(args []any) (out []any, copied bool) {
	out = args
	for i, arg := range args {
		if typed, ok := typedArray(arg); ok {
			if !copied {
				out, copied = slices.Clone(args), true
			}
			out[i] = typed
		}
	}
	return out, copied
}

// callWrapper calls a partial application, composition or argument wrapper,
// which calls the function it wraps on the Go stack.
func callWrapper(f *SignedBuiltin, args []any, focus any, env *Environment, counter *callCounter) (any, error) {
	if f.CallsLambdas {
		return f.Fn(args, focus, env)
	}
	return counter.callNested(1, func() (any, error) {
		return f.Fn(args, focus, env)
	})
}

// callFunction applies fn as jsonata-js's apply does: focus is the call's
// context, which fills a missing context argument before the arguments are
// validated.
func callFunction(fn any, args []any, focus any, env *Environment) (any, error) {
	return invokeFunction(fn, args, 0, focus, env, true)
}

// invokeFunction is callFunction, checking the first call's arguments only
// when checked is set, with plain marking the arguments that are plain
// arrays. Tail calls are always checked.
func invokeFunction(fn any, args []any, plain plainArgs, focus any, env *Environment, checked bool) (any, error) {
	counter := env.callCounter()

	// Trampoline loop: if the body returns a TailCall, re-invoke without
	// growing the Go stack. This handles both self-recursion and mutual recursion.
	// The iteration limit (counter.max * tailCallMultiplier) prevents infinite
	// tail-recursive loops from running forever.
	maxIter, iter := int(counter.max)*10000, 0
	for {
		if err := env.errNow(); err != nil {
			return nil, err
		}
		if fn == nil {
			return nil, &JSONataError{Code: "T1006", Message: "attempted to invoke undefined function"}
		}
		if checked {
			n := len(args)
			var err error
			if args, err = checkCallArgs(fn, args, focus); err != nil {
				return nil, err
			}
			if len(args) != n {
				// A context argument moved the others along.
				plain = 0
			}
		}
		checked = true
		switch f := fn.(type) {
		case *SignedBuiltin:
			if !f.isWrapper() {
				plain, typed := plainArrayArgs(args)
				var result any
				var err error
				if f.Plain != nil {
					result, err = f.Plain(plain, focus)
				} else {
					result, err = f.Fn(plain, focus, env)
				}
				return builtinResult(args, typed, result, err)
			}
			return callWrapper(f, args, focus, env, counter)
		case BuiltinFunction:
			plain, typed := plainArrayArgs(args)
			result, err := f(plain, focus)
			return builtinResult(args, typed, result, err)
		case EnvAwareBuiltin:
			plain, typed := plainArrayArgs(args)
			result, err := f(plain, focus, env)
			return builtinResult(args, typed, result, err)
		case *Lambda:
			result, err := evalLambdaBody(f, args, plain, focus, env, counter)
			if err != nil {
				return nil, err
			}
			if tc, ok := result.(*TailCall); ok {
				iter++
				if iter > maxIter {
					return nil, &JSONataError{Code: "U1001", Message: fmt.Sprintf("stack overflow error: evaluation exceeded stack depth %d", counter.max)}
				}
				fn, args, plain, focus = tc.Fn, tc.Args, tc.Plain, tc.Focus
				continue
			}
			return result, nil
		case *RegexLiteral:
			return callRegex(f, args)
		default:
			return nil, &JSONataError{Code: "T1006", Message: fmt.Sprintf("not a function: %T", fn)}
		}
	}
}
