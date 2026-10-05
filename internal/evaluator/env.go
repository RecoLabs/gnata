package evaluator

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/recolabs/gnata/internal/parser"
)

// defaultMaxCallDepth is the maximum recursive call depth before U1001 is returned.
// Matches the JSONata reference implementation's default.
const defaultMaxCallDepth = 100

// inlineBindingCap is the number of variable bindings an Environment stores
// inline before spilling to a map. Most child environments created per-eval
// bind only "$" (and occasionally one more, e.g. a lambda parameter or a
// path index variable), so this avoids a map allocation on the common path.
const inlineBindingCap = 2

// callCounter tracks the current recursive call depth across all child environments.
// A pointer is shared so all nested envs increment/decrement the same counter.
type callCounter struct {
	// int32 keeps the struct in the same allocation size class as before the
	// deadline and context fields were added; it is allocated per evaluation.
	depth        int32
	max          int32
	maxSequence  int32 // 0 = unlimited; guardrail set via WithSequence (error D2015)
	evalDepth    int16 // $eval nesting, capped at a small constant by IncrEvalDepth's caller
	stackIsLimit bool  // true when max was set via the WithStack guardrail (error D1011 instead of U1001)
	hasNow       bool  // nowMillis has been captured

	// nowMillis is the evaluation's timestamp, captured on first use so every
	// $now, $millis and $toMillis call in one evaluation sees the same instant.
	nowMillis int64

	// deadline is the WithTimeout guardrail, nil when unset so evaluations
	// without a timeout pay one pointer. It is polled from Err rather than
	// enforced with a context timer, so it needs no timer goroutine and still
	// fires inside a synchronous call on single-threaded WebAssembly hosts.
	deadline *deadlineState
	// ctx is the evaluation's context, shared by every environment using this
	// counter (see SetContext).
	ctx context.Context
}

type deadlineState struct {
	at    time.Time
	ticks uint32
	hit   bool
}

// deadlinePollMask spaces out time.Now calls: the deadline is checked on the
// first poll and then every 128th, keeping the per-node cost to a counter.
const deadlinePollMask = 127

// ErrDeadlineExceeded is returned by Err once the guardrail deadline passes.
var ErrDeadlineExceeded = errors.New("gnata: evaluation deadline exceeded")

// binding is one name/value pair stored inline on an Environment.
type binding struct {
	name  string
	value any
}

// Environment holds variable bindings for an evaluation context.
// It forms a linked chain for lexical scoping.
//
// Bindings are stored in the inline array until more than inlineBindingCap
// accumulate, at which point they're moved into bindings (a lazily-allocated
// map) and inline is no longer consulted. This keeps the common per-eval
// child environment (bound to "$" and little else) allocation-free beyond
// the Environment struct itself.
type Environment struct {
	parent  *Environment
	inline  [inlineBindingCap]binding
	inlineN int32
	// hasDeadline mirrors whether the call counter has a WithTimeout deadline
	// so Err's fast path needs no pointer chase. SetDeadline is called before
	// evaluation starts, and child environments inherit it. It sits beside
	// inlineN to reuse that field's padding.
	hasDeadline bool
	// decimalPrecision is the significant digits set via WithDecimalPrecision
	// (0 = float64 only), inherited by children. At most 100, so it fits in
	// the padding after hasDeadline.
	decimalPrecision uint16
	bindings         map[string]any // nil until inline overflows
	calls            *callCounter   // shared call-depth counter; nil inherits from parent
	// done caches ctx.Done() so the per-node cancellation check in Eval is a
	// nil check for non-cancellable contexts instead of a walk up both the
	// environment chain and the context.valueCtx chain.
	done <-chan struct{}
}

// NewEnvironment creates a root environment with no bindings.
func NewEnvironment() *Environment {
	return &Environment{
		calls: &callCounter{max: defaultMaxCallDepth},
	}
}

// NewChildEnvironment creates a child scope inheriting from parent.
func NewChildEnvironment(parent *Environment) *Environment {
	env := &Environment{parent: parent}
	if parent != nil {
		env.calls = parent.callCounter()
		env.done = parent.done
		env.hasDeadline = parent.hasDeadline
		env.decimalPrecision = parent.decimalPrecision
	}
	return env
}

// callCounter returns the shared call counter, walking up the chain if needed.
func (e *Environment) callCounter() *callCounter {
	if e == nil {
		return &callCounter{max: defaultMaxCallDepth}
	}
	if e.calls != nil {
		return e.calls
	}
	return e.parent.callCounter()
}

// Bind sets a variable in this environment.
func (e *Environment) Bind(name string, value any) {
	if e.bindings != nil {
		e.bindings[name] = value
		return
	}
	for i := range e.inlineN {
		if e.inline[i].name == name {
			e.inline[i].value = value
			return
		}
	}
	if e.inlineN < inlineBindingCap {
		e.inline[e.inlineN] = binding{name: name, value: value}
		e.inlineN++
		return
	}
	// Overflow: spill the inline bindings into a map and clear inline state
	// so later lookups only need to check one representation.
	e.bindings = make(map[string]any, inlineBindingCap+1)
	for i := range e.inlineN {
		e.bindings[e.inline[i].name] = e.inline[i].value
	}
	e.bindings[name] = value
	e.inlineN = 0
}

// Parent returns the parent environment (nil for root environments).
func (e *Environment) Parent() *Environment {
	return e.parent
}

// Lookup looks up a variable, walking the parent chain.
// Returns (nil, false) if not found.
func (e *Environment) Lookup(name string) (any, bool) {
	if v, ok := e.LookupDirect(name); ok {
		return v, true
	}
	if e.parent != nil {
		return e.parent.Lookup(name)
	}
	return nil, false
}

// LookupWithEnv looks up a variable and returns both the value and the
// specific environment in which the binding was found. This is used by the
// parent operator (%) so that chained %.% navigations correctly use the
// parent of the binding's environment, not the parent of the starting env.
// Returns (nil, nil, false) if not found.
func (e *Environment) LookupWithEnv(name string) (any, *Environment, bool) {
	if v, ok := e.LookupDirect(name); ok {
		return v, e, true
	}
	if e.parent != nil {
		return e.parent.LookupWithEnv(name)
	}
	return nil, nil, false
}

// ResetCallCounter installs a fresh call-depth counter on this environment,
// decoupling it from any inherited parent counter. Use this when creating a
// per-eval child environment from a shared parent to avoid cross-eval interference.
func (e *Environment) ResetCallCounter() {
	e.calls = &callCounter{max: defaultMaxCallDepth}
}

// IncrEvalDepth increments the $eval nesting counter and returns an error if
// the maximum depth is exceeded. Must be paired with DecrEvalDepth via defer.
func (e *Environment) IncrEvalDepth(maxDepth int) error {
	c := e.callCounter()
	c.evalDepth++
	if int(c.evalDepth) > maxDepth {
		c.evalDepth--
		return &JSONataError{Code: "D3121", Message: "$eval: maximum nesting depth exceeded"}
	}
	return nil
}

// Now returns the evaluation's timestamp at millisecond precision, the same
// for every call within one evaluation as jsonata-js requires.
func (e *Environment) Now() time.Time {
	c := e.callCounter()
	if !c.hasNow {
		c.nowMillis, c.hasNow = time.Now().UnixMilli(), true
	}
	return time.UnixMilli(c.nowMillis).UTC()
}

// DecrEvalDepth decrements the $eval nesting counter.
func (e *Environment) DecrEvalDepth() {
	c := e.callCounter()
	c.evalDepth--
}

// SetMaxStackDepth overrides the recursion depth limit as a guardrail: once set,
// exceeding it returns error D1011 instead of the default U1001.
func (e *Environment) SetMaxStackDepth(n int) {
	c := e.callCounter()
	c.max = clampInt32(n)
	c.stackIsLimit = true
}

// SetMaxSequence sets the guardrail sequence-length limit (0 = unlimited).
// Exceeding it at a checked growth site returns error D2015.
func (e *Environment) SetMaxSequence(n int) {
	e.callCounter().maxSequence = clampInt32(n)
}

// clampInt32 saturates a configured limit to the int32 range; larger limits
// are effectively unbounded.
func clampInt32(n int) int32 {
	return int32(max(min(n, math.MaxInt32), math.MinInt32))
}

// SetDecimalPrecision enables decimal arithmetic to digits significant digits
// (0 = disabled) for this environment and children created after it.
func (e *Environment) SetDecimalPrecision(digits int) {
	e.decimalPrecision = uint16(digits)
}

// DecimalPrecision returns the decimal precision in significant digits, or 0 when disabled.
func (e *Environment) DecimalPrecision() int {
	return int(e.decimalPrecision)
}

// CheckSequence returns a D2015 error if n exceeds the configured sequence
// guardrail. No-op when no guardrail is set.
func (e *Environment) CheckSequence(n int) error {
	c := e.callCounter()
	if c.maxSequence > 0 && n > int(c.maxSequence) {
		return &JSONataError{Code: "D2015", Message: fmt.Sprintf("The maximum sequence length of %d was exceeded", c.maxSequence)}
	}
	return nil
}

// Context returns the evaluation's context.Context, or context.Background()
// when none was set.
func (e *Environment) Context() context.Context {
	if c := e.callCounter(); c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

// SetContext sets the evaluation's context. It is stored on the call counter,
// so it must be called after ResetCallCounter and before evaluation creates
// child environments, which inherit the cached Done channel.
func (e *Environment) SetContext(ctx context.Context) {
	e.callCounter().ctx = ctx
	e.done = nil
	if ctx != nil {
		e.done = ctx.Done()
	}
}

// Err reports why evaluation must stop: the context's cancellation error, or
// ErrDeadlineExceeded once the WithTimeout deadline has passed. To keep the
// per-node cost to a counter increment, the clock is read only on every
// deadlinePollMask+1 calls; errNow reads it unconditionally.
func (e *Environment) Err() error {
	// Inlinable fast path for the common case: a context that can never be
	// cancelled and no WithTimeout deadline. Err runs on every Eval node.
	if e.done == nil && !e.hasDeadline {
		return nil
	}
	return e.errSlow()
}

func (e *Environment) errSlow() error {
	if err := e.ctxErr(); err != nil {
		return err
	}
	return e.deadlineErr(false)
}

// errNow is Err with an unconditional clock read. It is used at function-call
// boundaries, where a single call (a builtin over a large value, or a slow
// custom function) can take arbitrarily long, so the deadline overrun stays
// bounded by one call rather than by deadlinePollMask calls.
func (e *Environment) errNow() error {
	if err := e.ctxErr(); err != nil {
		return err
	}
	return e.deadlineErr(true)
}

// ctxErr reports the context's cancellation error. A nil done means the
// context (if any) can never be cancelled.
func (e *Environment) ctxErr() error {
	if e.done == nil {
		return nil
	}
	select {
	case <-e.done:
		return e.Context().Err()
	default:
		return nil
	}
}

func (e *Environment) deadlineErr(force bool) error {
	c := e.calls
	if c == nil {
		c = e.callCounter()
	}
	d := c.deadline
	if d == nil {
		return nil
	}
	if d.hit {
		return ErrDeadlineExceeded
	}
	d.ticks++
	if (force || d.ticks&deadlinePollMask == 1) && !time.Now().Before(d.at) {
		d.hit = true
		return ErrDeadlineExceeded
	}
	return nil
}

// SetDeadline sets the evaluation deadline enforced by Err (zero = none).
func (e *Environment) SetDeadline(t time.Time) {
	c := e.callCounter()
	c.deadline = nil
	if !t.IsZero() {
		c.deadline = &deadlineState{at: t}
	}
	e.hasDeadline = c.deadline != nil
}

// DeadlineExceeded reports whether Err has observed the deadline passing.
func (e *Environment) DeadlineExceeded() bool {
	d := e.callCounter().deadline
	return d != nil && d.hit
}

// Range iterates over the bindings in this environment (not parents).
func (e *Environment) Range(fn func(name string, val any)) {
	if e.bindings != nil {
		for k, v := range e.bindings {
			fn(k, v)
		}
		return
	}
	for i := range e.inlineN {
		fn(e.inline[i].name, e.inline[i].value)
	}
}

// LookupDirect checks only the direct bindings (no parent chain walk).
func (e *Environment) LookupDirect(name string) (any, bool) {
	if e.bindings != nil {
		v, ok := e.bindings[name]
		return v, ok
	}
	for i := range e.inlineN {
		if e.inline[i].name == name {
			return e.inline[i].value, true
		}
	}
	return nil, false
}

// BuiltinFunction is a native Go function implementing a JSONata built-in.
// args are the evaluated arguments; focus is the current context value.
type BuiltinFunction func(args []any, focus any) (any, error)

// EnvAwareBuiltin is a built-in function that receives the current evaluation
// environment. This is required for functions that need to dispatch callbacks
// with the correct per-evaluation call counter (HOFs like $map, $filter) or
// that create child scopes ($eval).
type EnvAwareBuiltin func(args []any, focus any, env *Environment) (any, error)

// SignedBuiltin wraps a BuiltinFunction with a type signature for arity and
// type validation at the direct call site. HOF callbacks that invoke the
// function via ApplyFunction bypass signature validation, allowing extra
// arguments (key, index, array) to be passed silently.
type SignedBuiltin struct {
	Fn        BuiltinFunction
	Sig       string
	ParsedSig []parser.ParamSpec // pre-parsed signature; avoids re-parsing on every call
}

// Lambda represents a user-defined function (lambda expression).
type Lambda struct {
	Params        []string           // parameter names
	Body          *parser.Node       // function body AST node
	Closure       *Environment       // lexical scope at definition site
	Thunk         bool               // for tail-call optimization
	Sig           string             // type signature (Wave 5)
	ParsedSig     []parser.ParamSpec // pre-parsed signature; avoids re-parsing per call
	CapturedFocus any                // focus ($) captured at definition time for zero-param closures
}
