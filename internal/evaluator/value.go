package evaluator

import (
	"encoding/json"
	"math"
	"slices"

	"github.com/recolabs/gnata/internal/parser"
)

// Null is the singleton JSONata null value.
var Null any = JSONNull{}

// JSONNull is a sentinel type that represents JSON null explicitly,
// distinguishing it from Go nil (which represents JSONata undefined).
type JSONNull struct{}

func (JSONNull) MarshalJSON() ([]byte, error) { return []byte(parser.NullJSON), nil }

// NilAsNull returns an item of data as a value: a nil item is a JSON null
// in data decoded with encoding/json, not undefined.
func NilAsNull(v any) any {
	if v == nil {
		return Null
	}
	return v
}

// NullItems returns items with each nil item made a JSON null (see
// NilAsNull), copying items only when one is nil.
func NullItems(items []any) []any {
	if !slices.Contains(items, nil) {
		return items
	}
	out := slices.Clone(items)
	for i, item := range out {
		out[i] = NilAsNull(item)
	}
	return out
}

// IsNull reports whether v is the JSON null sentinel.
func IsNull(v any) bool {
	_, ok := v.(JSONNull)
	return ok
}

// ConsArray is a JSONata constructed array (`[...]` used as a path step).
// It is indexable like []any, but it is one value in a path: a later step
// does not auto-map into its elements the way it maps a result sequence.
type ConsArray []any

// KeptArray is a one-item result sequence the [] operator kept as an
// array, as in o.b[]. jsonata-js marks such a sequence keep-singleton: it
// reads as an array, but a path step flattens it like any sequence, so
// o.(b[]) is 5.
type KeptArray []any

// RawSequence is a result sequence of a built-in, with at most one item,
// that a higher-order function holds as an item, as in
// $map([{"a":1}], $keys). jsonata-js collapses a result sequence once, when
// an expression returns it, so such an item reads as an array (that result
// is ["a"]; $map([1], $keys) is []) and a path step flattens it, but an
// expression returning the item itself, such as a variable, block or
// numeric subscript, collapses it to its value or to undefined.
type RawSequence []any

// AsArray returns the slice behind a []any or a typed array (see typedArray).
func AsArray(v any) ([]any, bool) {
	if a, ok := v.([]any); ok {
		return a, true
	}
	return typedArray(v)
}

// typedArray returns the slice behind a ConsArray, KeptArray or RawSequence,
// the evaluator's internal array types.
func typedArray(v any) ([]any, bool) {
	switch a := v.(type) {
	case ConsArray:
		return []any(a), true
	case KeptArray:
		return []any(a), true
	case RawSequence:
		return []any(a), true
	}
	return nil, false
}

// settleRaw returns the result of an expression that evaluated to v: a
// RawSequence collapses to its item, or undefined when empty, or with
// keepArray (the [] suffix) becomes a KeptArray, as jsonata-js collapses a
// returned sequence. Every evaluator that can return a child's value
// unchanged (variable, block, condition, bind, ?:, ??, a number-literal
// subscript) applies it; a function call applies it through CollapseAndKeep.
func settleRaw(v any, keepArray bool) any {
	raw, ok := v.(RawSequence)
	switch {
	case !ok:
		return v
	case len(raw) == 0:
		return nil
	case keepArray:
		return KeptArray(raw)
	}
	return raw[0]
}

// evalSettled evaluates node and settles its result (see settleRaw).
func evalSettled(node *parser.Node, input any, env *Environment, keepArray bool) (any, error) {
	result, err := Eval(node, input, env)
	if err != nil {
		return nil, err
	}
	return settleRaw(result, keepArray), nil
}

// StripTypedArrays converts the evaluator's internal array types to []any,
// recursively, including inside objects the evaluator built, and returns a
// transform's clone of a Go map as a map[string]any. Used at the public
// Eval boundary so callers see ordinary values. It also clears the clone
// mark of transform objects it returns (see cloneIDs). An object or array a
// value contains itself through, which only Go input can, is passed
// through where the walk re-enters it: Go input holds no internal types.
func StripTypedArrays(v any) any {
	if isScalar(v) {
		return v
	}
	var s stripper
	stripped, _ := s.strip(v)
	return stripped
}

// stripMemoAfter is how many containers stripper visits before it memoizes
// them, so a result that shares subtrees, as {"a": $x, "b": $x} nested again
// and again does, is walked once per container rather than once per path.
const stripMemoAfter = 256

// stripper strips internal array types in one pass, copying a container
// only when something inside it changed. A frozen map is decoded input,
// which never holds one.
type stripper struct {
	visits int
	done   map[any]stripped // by *OrderedMap or sliceKey
	path   valuePath
}

type stripped struct {
	value   any
	changed bool
}

// sliceKey identifies a non-empty slice by its storage and length.
type sliceKey struct {
	first *any
	n     int
}

// strip returns v with its internal array types stripped, and whether that
// changed it. It walks nested containers from an explicit stack of frames
// rather than by recursion, so a deeply nested value cannot overflow the
// goroutine stack; path follows the frames, so a container the value
// contains itself through is passed through where the walk re-enters it.
func (s *stripper) strip(v any) (out any, changed bool) {
	out, changed, frame, open := s.open(v)
	if !open {
		return out, changed
	}
	s.path.enter(frame.identity())
	var buf [8]stripFrame
	stack := append(buf[:0], frame)
	for {
		top := &stack[len(stack)-1]
		copying := top.out != nil || top.outObj != nil || top.goMap != nil
		if !copying && top.obj == nil {
			for top.index < len(top.items) && isScalar(top.items[top.index]) {
				top.index++
			}
		}
		if child, ok := top.next(); ok {
			if isScalar(child) {
				if copying {
					top.set(child, false)
				}
				continue
			}
			out, changed, frame, open := s.open(child)
			switch {
			case !open:
				top.set(out, changed)
				continue
			case !s.path.enter(frame.identity()):
				top.set(child, false)
				continue
			}
			stack = append(stack, frame)
			continue
		}
		out, changed := top.finish()
		if top.key != nil {
			s.done[top.key] = stripped{value: out, changed: changed}
		}
		out, changed = top.result(out, changed)
		s.path.leave(top.identity())
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			return out, changed
		}
		stack[len(stack)-1].set(out, changed)
	}
}

// open starts stripping v: it returns v's result at once unless v is a
// container to walk, for which it returns the frame that walks it. A typed
// array becomes []any even when nothing inside it changes.
func (s *stripper) open(v any) (_ any, changed bool, _ stripFrame, open bool) {
	arr, typed := typedArray(v)
	if !typed {
		switch a := v.(type) {
		case []any:
			arr = a
		case *OrderedMap:
			if a.frozen {
				return v, false, stripFrame{}, false
			}
			key, hit, out, changed := s.recall(v)
			if hit {
				return out, changed, stripFrame{}, false
			}
			if a.clone.Load() != 0 {
				a.clone.Store(0)
			}
			frame := stripFrame{childCursor: childCursor{obj: a}, self: v, key: key}
			if a.goMap {
				frame.goMap = make(map[string]any, len(a.keys))
			}
			return nil, false, frame, true
		default:
			return v, false, stripFrame{}, false
		}
	}
	frame := stripFrame{childCursor: childCursor{items: arr}, self: v, typed: typed}
	key, hit, out, changed := s.recall(v)
	if hit {
		out, changed = frame.result(out, changed)
		return out, changed, stripFrame{}, false
	}
	frame.key = key
	return nil, false, frame, true
}

// isScalar reports whether v is a JSON scalar or undefined, which the
// stripper passes through without opening.
func isScalar(v any) bool {
	switch v.(type) {
	case nil, string, float64, bool, json.Number, JSONNull:
		return true
	}
	return false
}

// recall counts a visit to the container v. Once memoizing, it returns the
// result stored for v, or the key to store the result under, when v is
// worth memoizing (see worthMemo).
func (s *stripper) recall(v any) (store any, hit bool, out any, changed bool) {
	s.visits++
	if s.visits <= stripMemoAfter || !worthMemo(v) {
		return nil, false, nil, false
	}
	if s.done == nil {
		s.done = make(map[any]stripped)
	}
	key := containerKey(v)
	if key == nil {
		return nil, false, nil, false
	}
	if r, ok := s.done[key]; ok {
		return nil, true, r.value, r.changed
	}
	return key, false, nil, false
}

// stripFrame is a container strip is walking: an array's items, or an
// object, the index of the next one, and the copy made once a child changed.
type stripFrame struct {
	childCursor
	self   any            // the object or array walked, as it was passed
	out    []any          // the array's copy
	outObj *OrderedMap    // the object's copy
	goMap  map[string]any // the result for a transform's clone of a Go map
	typed  bool           // items are a typed array's
	key    any            // where to memoize the result, or nil
}

// identity is the object or array the frame walks (see valuePath).
func (f *stripFrame) identity() any {
	return f.self
}

// set records the stripped value of the child next last returned.
func (f *stripFrame) set(v any, changed bool) {
	i := f.index - 1
	switch {
	case f.goMap != nil:
		f.goMap[f.obj.keys[i]] = v
	case f.obj != nil:
		if changed && f.outObj == nil {
			f.outObj = NewOrderedMapWithCapacity(len(f.obj.keys))
			for _, prev := range f.obj.keys[:i] {
				f.outObj.Set(prev, f.obj.data[prev])
			}
		}
		if f.outObj != nil {
			f.outObj.Set(f.obj.keys[i], v)
		}
	default:
		if changed && f.out == nil {
			f.out = make([]any, len(f.items))
			copy(f.out, f.items[:i])
		}
		if f.out != nil {
			f.out[i] = v
		}
	}
}

// finish returns the walked container's result, before result applies a
// typed array's change.
func (f *stripFrame) finish() (any, bool) {
	switch {
	case f.goMap != nil:
		return f.goMap, true
	case f.outObj != nil:
		return f.outObj, true
	case f.out != nil:
		return f.out, true
	}
	return f.self, false
}

// result returns the container's result given its walked or memoized one:
// a typed array always changes, to its items when nothing inside changed.
func (f *stripFrame) result(out any, changed bool) (any, bool) {
	if f.typed && !changed {
		return f.items, true
	}
	return out, changed
}

// memoMinLen is the length from which a container is worth memoizing for
// its own size, so a large object or array shared by many parents is walked
// once.
const memoMinLen = 16

// worthMemo reports whether the object or array v is worth memoizing: a
// large one, one holding two or more containers, where the paths through
// shared subtrees multiply, or one heading a chain of single-child
// containers, so a long chain shared by many parents is walked once. Any
// other container ends, within chainLookahead small levels, in a leaf or in
// a container memoized itself, so walking it again costs a constant.
func worthMemo(v any) bool {
	for level := range chainLookahead + 1 {
		n, containers, only := 0, 0, any(nil)
		anyChild(v, func(c any) bool {
			n++
			if isContainer(c) {
				containers++
				only = c
			}
			return n >= memoMinLen || containers >= 2
		})
		switch {
		case n >= memoMinLen || containers >= 2:
			// A chain container like this is memoized itself.
			return level == 0
		case containers == 0:
			return false
		}
		v = only
	}
	return true
}

// chainLookahead is how many levels of single-child containers below a
// container worthMemo follows before it memoizes the container.
const chainLookahead = 3

// isContainer reports whether v is an object or array that can change or
// hold internal types: an object that is not frozen or a non-empty array.
// Anything else holds no container that can change. Unlike containerKey,
// it does not allocate.
func isContainer(v any) bool {
	if om, ok := v.(*OrderedMap); ok {
		return !om.frozen
	}
	arr, ok := AsArray(v)
	return ok && len(arr) > 0
}

// childCursor steps through the values of an object or items of an array.
type childCursor struct {
	obj   *OrderedMap
	items []any
	index int
}

func newChildCursor(v any) childCursor {
	if om, ok := v.(*OrderedMap); ok {
		return childCursor{obj: om}
	}
	arr, _ := AsArray(v)
	return childCursor{items: arr}
}

// next returns the next child, or false when there is none left.
func (c *childCursor) next() (any, bool) {
	if c.obj != nil {
		if c.index == len(c.obj.keys) {
			return nil, false
		}
		c.index++
		return c.obj.data[c.obj.keys[c.index-1]], true
	}
	if c.index == len(c.items) {
		return nil, false
	}
	c.index++
	return c.items[c.index-1], true
}

// Sequence is the core multi-value container used throughout evaluation.
// It represents an ordered collection of values that may be collapsed to a
// single value or remain as a sequence depending on context.
type Sequence struct {
	Values       []any
	ConsArray    bool // explicitly constructed via [...]; prevents flattening
	OuterWrapper bool // input was a JSON array; treated as a single document
	TupleStream  bool // contains tuple objects {"@": value, varName: value}
	ArgShaped    bool // a built-in result shaped like its first argument (see argShape)
}

// CreateSequence creates a Sequence optionally pre-populated with one value.
func CreateSequence(items ...any) *Sequence {
	s := &Sequence{Values: make([]any, 0, len(items)+4)}
	s.Values = append(s.Values, items...)
	return s
}

// CollapseSequence applies JSONata singleton-collapsing rules:
//   - len 0 → nil (undefined)
//   - len 1 → elem[0]
//   - len > 1 → []any(seq.Values) — ownership transfer; callers must not mutate
func CollapseSequence(s *Sequence) any {
	switch len(s.Values) {
	case 0:
		return nil
	case 1:
		return s.Values[0]
	default:
		return slices.Clip(s.Values)
	}
}

// CollapseSequences collapses v while it is a sequence, a lone item of which
// can be a sequence itself.
func CollapseSequences(v any) any {
	for {
		seq, ok := v.(*Sequence)
		if !ok {
			return v
		}
		v = CollapseSequence(seq)
	}
}

// CollapseAndKeep normalizes a function call result, as jsonata-js
// collapses the sequence an expression returns: a *Sequence collapses to a
// single value, an array or undefined, and with keepArray (the [] suffix) a
// one-item sequence becomes a KeptArray instead. [] leaves a result that is
// not a sequence unchanged: $sum([5])[] is 5.
func CollapseAndKeep(result any, keepArray bool) any {
	seq, ok := result.(*Sequence)
	if !ok {
		return settleRaw(result, keepArray)
	}
	if keepArray && len(seq.Values) == 1 {
		return KeptArray{seq.Values[0]}
	}
	return CollapseSequence(seq)
}

// holdResult normalizes a function result a higher-order function holds as
// an item: unlike CollapseAndKeep, it keeps an empty or one-item sequence as
// a RawSequence.
func holdResult(result any) any {
	seq, ok := result.(*Sequence)
	switch {
	case !ok:
		return result
	case seq.Values == nil:
		return make(RawSequence, 0, 1) // capacity lets sameArray recognize it
	case len(seq.Values) <= 1:
		return RawSequence(seq.Values)
	}
	return CollapseSequence(seq)
}

// IsArray reports whether AsArray accepts v (a *Sequence is not an array).
func IsArray(v any) bool {
	_, ok := AsArray(v)
	return ok
}

// CollapseToSlice returns the sequence values as a plain []any slice.
// Ownership transfer; callers must not mutate the returned slice.
func CollapseToSlice(s *Sequence) []any {
	return slices.Clip(s.Values)
}

// AppendItems returns the items $append adds for v, as jsonata-js fn.append
// does: an array's or sequence's items, nothing for undefined, or v itself.
// The array builtins take their array arguments the same way.
func AppendItems(v any) []any {
	if v == nil {
		return []any{}
	}
	if arr, ok := AsArray(v); ok {
		return arr
	}
	if seq, ok := v.(*Sequence); ok {
		return CollapseToSlice(seq)
	}
	return []any{v}
}

// EachLeaf calls visit, in order, for every item of arr that is not an
// array, descending up to depth levels (-1 for all) into nested arrays. It
// walks iteratively so that deep nesting cannot overflow the stack, and
// checks for cancellation every cancelCheckInterval items, first item
// included, as nested arrays can share items. It reports U1001, naming the
// walk what (as in "flatten"), for an array that contains itself, which Go
// input can.
func EachLeaf(what string, arr []any, depth int, env *Environment, visit func(item any) error) error {
	type frame struct {
		items       []any
		next, depth int
	}
	stack := []frame{{items: arr, depth: depth}}
	path := valuePath{what: what}
	path.enter(arr)
	visits := 0
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next == len(top.items) {
			path.leave(top.items)
			stack = stack[:len(stack)-1]
			continue
		}
		item := top.items[top.next]
		top.next++
		if visits%cancelCheckInterval == 0 {
			if err := env.Err(); err != nil {
				return err
			}
		}
		visits++
		if nested, ok := AsArray(item); ok && top.depth != 0 {
			if !path.enter(nested) {
				return path.cycleError()
			}
			stack = append(stack, frame{items: nested, depth: max(top.depth-1, -1)})
			continue
		}
		if err := visit(item); err != nil {
			return err
		}
	}
	return nil
}

// appendLength is len(AppendItems(v)) without allocating.
func appendLength(v any) int {
	if v == nil {
		return 0
	}
	if arr, ok := AsArray(v); ok {
		return len(arr)
	}
	if seq, ok := v.(*Sequence); ok {
		return len(seq.Values)
	}
	return 1
}

// ToFloat64 converts a numeric value to float64, handling both float64 and json.Number.
func ToFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// IsNumeric returns true for finite numeric values (float64 or json.Number).
func IsNumeric(v any) bool {
	switch n := v.(type) {
	case float64:
		return !math.IsInf(n, 0) && !math.IsNaN(n)
	case json.Number:
		_, err := n.Float64()
		return err == nil
	}
	return false
}

// ToBoolean implements JSONata boolean casting rules.
func ToBoolean(v any) bool {
	truthy, _ := ToBooleanEnv(v, nil)
	return truthy
}

// ToBooleanEnv is ToBoolean polling env, which may be nil, for
// cancellation while it walks nested arrays.
func ToBooleanEnv(v any, env *Environment) (bool, error) {
	if b, ok := v.(bool); ok {
		return b, nil
	}
	truthy, items := booleanOf(v)
	if items == nil {
		return truthy, nil
	}
	return anyTruthy(items, env)
}

// booleanOf returns v's boolean, or the items of an array, which is true when
// any of them is.
func booleanOf(v any) (truthy bool, items []any) {
	v = CollapseSequences(v)
	if v == nil || IsNull(v) {
		return false, nil
	}
	switch val := v.(type) {
	case bool:
		return val, nil
	case string:
		return val != "", nil
	case float64:
		return val != 0 && !math.IsNaN(val), nil
	case json.Number:
		f, err := val.Float64()
		return err == nil && f != 0, nil
	case *OrderedMap:
		return val.Len() > 0, nil
	case map[string]any:
		return len(val) > 0, nil
	case []any, ConsArray, KeptArray, RawSequence:
		arr, _ := AsArray(val)
		return false, arr
	}
	return false, nil
}

// anyTruthy reports whether any of items, or of the items of an array among
// them, is truthy.
func anyTruthy(items []any, env *Environment) (bool, error) {
	for i, v := range items {
		truthy, nested := booleanOf(v)
		if nested != nil {
			return anyTruthyNested(items[i+1:], nested, env)
		}
		if truthy {
			return true, nil
		}
	}
	return false, nil
}

// anyTruthyNested continues anyTruthy with the items of a nested array and
// then the rest. It walks nested arrays from an explicit stack rather than
// by recursion, so deep nesting cannot overflow the goroutine stack, and
// skips an array open on the path, as in a value that contains itself.
// Once it has entered stripMemoAfter arrays, it records the arrays it
// completes, which hold nothing truthy wherever they are, and skips them
// when they recur. It polls env, which may be nil, for cancellation.
func anyTruthyNested(rest, nested []any, env *Environment) (bool, error) {
	var (
		buf     [8]childCursor
		path    valuePath
		falsy   map[sliceKey]struct{}
		entered int
		visits  int
	)
	stack := append(buf[:0], childCursor{items: rest}, childCursor{items: nested})
	path.enter(nested)
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		v, ok := top.next()
		if !ok {
			stack = stack[:len(stack)-1]
			if len(stack) > 0 {
				path.leave(top.items)
				if falsy != nil && len(top.items) > 0 {
					falsy[sliceKey{first: &top.items[0], n: len(top.items)}] = struct{}{}
				}
			}
			continue
		}
		visits++
		if env != nil && visits%cancelCheckInterval == 0 {
			if err := env.Err(); err != nil {
				return false, err
			}
		}
		truthy, nested := booleanOf(v)
		switch {
		case truthy:
			return true, nil
		case len(nested) == 0:
			continue
		}
		if _, ok := falsy[sliceKey{first: &nested[0], n: len(nested)}]; ok || !path.enter(nested) {
			continue
		}
		stack = append(stack, childCursor{items: nested})
		entered++
		if entered == stripMemoAfter {
			falsy = make(map[sliceKey]struct{})
		}
	}
	return false, nil
}

func normalizeNumber(v any) any {
	if n, ok := v.(json.Number); ok {
		f, err := n.Float64()
		if err != nil {
			return v
		}
		return f
	}
	return v
}

// DeepEqual implements JSONata structural equality.
func DeepEqual(a, b any) bool {
	equal, _ := deepEqual(a, b, equality{}) // with no env, no error
	return equal
}

// DeepEqualNullAsNil is DeepEqual with the null sentinel equal to nil, for
// comparing evaluator output with values decoded by encoding/json.
func DeepEqualNullAsNil(a, b any) bool {
	equal, _ := deepEqual(a, b, equality{nullAsNil: true}) // with no env, no error
	return equal
}

// DeepEqualPrec is DeepEqual comparing numbers in decimal to prec significant
// digits, or in float64 when prec is 0. It compares nested arrays and objects
// from an explicit stack of frames rather than by recursion, so a deeply
// nested value cannot overflow the goroutine stack.
func DeepEqualPrec(a, b any, prec int) bool {
	equal, _ := deepEqual(a, b, equality{prec: prec}) // with no env, no error
	return equal
}

// DeepEqualEnv is DeepEqualPrec at env's decimal precision, polling env
// for cancellation, as arrays sharing their items can make a small value
// take long to compare.
func DeepEqualEnv(a, b any, env *Environment) (bool, error) {
	visits := 0
	return deepEqual(a, b, equality{prec: env.DecimalPrecision(), env: env, visits: &visits})
}

// Equaler compares values as DeepEqualEnv does, counting the visits of all
// its comparisons toward polling env, so a long run of short comparisons,
// as $distinct makes, still notices cancellation.
type Equaler struct {
	eq     equality
	visits int
}

// NewEqualer returns an Equaler comparing at env's decimal precision.
func NewEqualer(env *Environment) *Equaler {
	e := &Equaler{eq: equality{prec: env.DecimalPrecision(), env: env}}
	e.eq.visits = &e.visits
	return e
}

// Equal reports whether a and b are equal.
func (e *Equaler) Equal(a, b any) (bool, error) {
	return deepEqual(a, b, e.eq)
}

// equality is how deepEqual compares values: numbers in decimal to prec
// significant digits, or in float64 when prec is 0, and with nullAsNil the
// null sentinel equal to nil. env, when set, is polled for cancellation
// every cancelCheckInterval visits, which visits counts.
type equality struct {
	prec      int
	nullAsNil bool
	env       *Environment
	visits    *int
}

// poll counts a visit, checking env for cancellation every
// cancelCheckInterval visits.
func (eq equality) poll() error {
	if eq.env == nil {
		return nil
	}
	*eq.visits++
	if *eq.visits%cancelCheckInterval == 0 {
		return eq.env.Err()
	}
	return nil
}

func deepEqual(a, b any, eq equality) (bool, error) {
	if err := eq.poll(); err != nil {
		return false, err
	}
	var frame equalFrame
	if open, equal := equalShallow(a, b, eq, &frame); !open {
		return equal, nil
	}
	return equalItems(&frame, eq)
}

// equalItems compares the items of frame, keeping the frames of enclosing
// arrays and objects on a stack while it compares a nested one. A pair
// whose first container is open on the path, as in a value that contains
// itself, is equal only when it is one container, which equalShallow
// has already found.
func equalItems(frame *equalFrame, eq equality) (bool, error) {
	var (
		buf   [8]equalFrame
		child equalFrame
		path  valuePath
	)
	stack := buf[:0]
	path.enter(frame.self)
	for {
		if err := eq.poll(); err != nil {
			return false, err
		}
		a, b, more, equal := frame.next(eq)
		if !equal {
			return false, nil
		}
		if more {
			open, equal := equalShallow(a, b, eq, &child)
			if !equal {
				return false, nil
			}
			if open {
				if !path.enter(child.self) {
					return false, nil
				}
				stack = append(stack, *frame)
				*frame = child
			}
			continue
		}
		if len(stack) == 0 {
			return true, nil
		}
		path.leave(frame.self)
		*frame = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
	}
}

// equalFrame is an array or object pair DeepEqualPrec is comparing.
type equalFrame struct {
	// a and b are the items to compare pairwise: those of two arrays, or the
	// nested values of two plain maps, whose other values are compared when
	// the frame opens.
	a, b []any
	// ordered, when set, is an ordered object compared with other key by key.
	ordered *OrderedMap
	other   any
	// index is the position of the next item or key.
	index int
	// self is the first array or object of the pair (see valuePath).
	self any
}

// next compares the frame's items up to its next pair of arrays or objects,
// which it returns, or reports that the frame has none left. equal is false
// once an item differs.
func (f *equalFrame) next(eq equality) (a, b any, more, equal bool) {
	for {
		if f.ordered != nil {
			if f.index == len(f.ordered.keys) {
				return nil, nil, false, true
			}
			k := f.ordered.keys[f.index]
			var exists bool
			if b, exists = MapGet(f.other, k); !exists {
				return nil, nil, false, false
			}
			a = f.ordered.data[k]
		} else {
			if f.index == len(f.a) {
				return nil, nil, false, true
			}
			a, b = f.a[f.index], f.b[f.index]
		}
		f.index++
		if isCompound(a) {
			return a, b, true, true
		}
		if _, equal := equalShallow(a, b, eq, nil); !equal {
			return nil, nil, false, false
		}
	}
}

// isCompound reports whether v is an array or object, whose items
// deepEqual compares in a frame.
func isCompound(v any) bool {
	switch v.(type) {
	case []any, ConsArray, KeptArray, RawSequence, map[string]any, *OrderedMap:
		return true
	}
	return false
}

// openEqualFrame checks that b is an array or object like a with as many
// items, and sets frame to compare their items. For a plain map, whose
// iteration cannot be resumed, it compares the values that are not arrays
// or objects at once and keeps the others to compare pairwise, opening no
// frame when there are none.
func openEqualFrame(a, b any, eq equality, frame *equalFrame) (open, equal bool) {
	switch av := a.(type) {
	case map[string]any:
		if !IsMap(b) || MapLen(b) != len(av) {
			return false, false
		}
		var nestedA, nestedB []any
		for k, va := range av {
			vb, exists := MapGet(b, k)
			if !exists {
				return false, false
			}
			if isCompound(va) {
				nestedA, nestedB = append(nestedA, va), append(nestedB, vb)
			} else if _, equal := equalShallow(va, vb, eq, nil); !equal {
				return false, false
			}
		}
		*frame = equalFrame{a: nestedA, b: nestedB, self: av}
		return len(nestedA) > 0, true
	case *OrderedMap:
		if !IsMap(b) || MapLen(b) != av.Len() {
			return false, false
		}
		*frame = equalFrame{ordered: av, other: b, self: av}
		return true, true
	}
	bv, ok := AsArray(b)
	avSlice, _ := AsArray(a)
	if !ok || len(avSlice) != len(bv) {
		return false, false
	}
	*frame = equalFrame{a: avSlice, b: bv, self: a}
	return true, true
}

// equalShallow compares a and b, except that for arrays and objects it sets
// frame to compare their items and reports open. frame may be nil when a is
// neither.
func equalShallow(a, b any, eq equality, frame *equalFrame) (open, equal bool) {
	if eq.prec > 0 {
		if equal, ok := decimalEqual(a, b, eq.prec); ok {
			return false, equal
		}
	}
	switch av := a.(type) {
	case float64:
		if bv, ok := b.(float64); ok {
			return false, av == bv
		}
	case string:
		bv, ok := b.(string)
		return false, ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return false, ok && av == bv
	}

	a, b = normalizeNumber(a), normalizeNumber(b)
	if a == nil || b == nil || IsNull(a) || IsNull(b) {
		if eq.nullAsNil {
			return false, (a == nil || IsNull(a)) && (b == nil || IsNull(b))
		}
		return false, a == nil && b == nil || IsNull(a) && IsNull(b)
	}
	switch av := a.(type) {
	case bool:
		bv, ok := b.(bool)
		return false, ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return false, ok && av == bv
	case string:
		bv, ok := b.(string)
		return false, ok && av == bv
	case []any, ConsArray, KeptArray, RawSequence, map[string]any, *OrderedMap:
		if id := objectIdentity(av); id != nil && id == objectIdentity(b) {
			return false, true
		}
		return openEqualFrame(av, b, eq, frame)
	case *RegexLiteral, *Lambda, *SignedBuiltin, BuiltinFunction, EnvAwareBuiltin:
		// As in jsonata-js, a function equals only itself.
		return false, sameFunc(av, b)
	}
	return false, false
}

// JSONataError is the structured error type used throughout evaluation.
// Code matches the JSONata spec error codes (S0xxx, T0xxx, T1xxx, T2xxx, D1xxx, D2xxx, D3xxx).
type JSONataError struct {
	Code    string
	Token   string
	Value   any
	Message string
}

func (e *JSONataError) Error() string {
	if e.Message != "" && e.Code != "" {
		return e.Code + ": " + e.Message
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// ToIntClamped truncates a JSONata number to int, saturating outside the int32
// range. A plain int() of an out-of-range float is implementation-defined, and
// int is only 32 bits on some WebAssembly targets, so indexes, limits and
// widths beyond ±2^31 (all of which already mean "unbounded") are clamped.
func ToIntClamped(f float64) int {
	switch {
	case math.IsNaN(f):
		return 0
	case f >= math.MaxInt32:
		return math.MaxInt32
	case f <= math.MinInt32:
		return math.MinInt32
	}
	return int(f)
}

// RadixPrefix returns the base of a hex (0x), binary (0b) or octal (0o) prefix
// on s, and the bits each digit carries, or 0, 0 when there is none.
func RadixPrefix(s string) (base, digitBits int) {
	if len(s) < 2 || s[0] != '0' {
		return 0, 0
	}
	switch s[1] {
	case 'x', 'X':
		return 16, 4
	case 'b', 'B':
		return 2, 1
	case 'o', 'O':
		return 8, 3
	}
	return 0, 0
}
