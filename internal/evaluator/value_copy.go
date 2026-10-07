package evaluator

import (
	"encoding/json"
	"reflect"
	"slices"
	"unsafe"
)

// copyTree copies the objects and arrays of v as copier says, walking nested
// ones from an explicit stack of frames rather than by recursion, so a deeply
// nested value cannot overflow the goroutine stack. path follows the frames,
// so a value that contains itself is reported, or passed through where the
// copier takes it as it is (see treeCopier.reentered).
//
// It copies every container it opens, so the other tree copiers are not
// built on it: the stripper (see StripTypedArrays) copies a container only
// when something inside it changed and memoizes shared subtrees, and
// detacher.copy copies only the containers that reach a transform's target,
// memoized by identity.
func copyTree(v any, copier treeCopier, path *valuePath) (any, error) {
	v, isLeaf := copier.leaf(v)
	if isLeaf {
		return v, nil
	}
	var buf [8]copyFrame
	stack := buf[:0]
	// open pushes the frame copying v, unless v is open on the path, for
	// which it returns what the copier takes in its place.
	open := func(v any) (out any, pushed bool, _ error) {
		if !path.enter(v) {
			if out, ok := copier.reentered(v); ok {
				return out, false, nil
			}
			return nil, false, path.cycleError()
		}
		frame, err := copier.frame(v)
		if err != nil {
			return nil, false, err
		}
		stack = append(stack, frame)
		return nil, true, nil
	}
	if out, pushed, err := open(v); err != nil || !pushed {
		return out, err
	}
	for {
		top := &stack[len(stack)-1]
		if child, ok := top.next(); ok {
			out, isLeaf := copier.leaf(child)
			if !isLeaf {
				var pushed bool
				var err error
				if out, pushed, err = open(out); err != nil {
					return nil, err
				}
				if pushed {
					continue
				}
			}
			top.set(out)
			continue
		}
		out := top.result()
		copier.done(top)
		path.leave(top.identity())
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			return out, nil
		}
		stack[len(stack)-1].set(out)
	}
}

// treeCopier says how copyTree copies values.
type treeCopier interface {
	// leaf returns the copy of v, or with isLeaf false the object or array v
	// stands for, which is copied with a frame.
	leaf(v any) (out any, isLeaf bool)
	// frame returns the frame that copies an object or array leaf returned.
	frame(v any) (copyFrame, error)
	// reentered returns what stands in the copy for an object or array
	// that is open on the path, so the value contains itself, or false to
	// fail the copy with U1001.
	reentered(v any) (out any, ok bool)
	// done is called with each frame once its copy is complete.
	done(frame *copyFrame)
}

// failsOnCycle gives a treeCopier the reentered of a copy that fails on a
// value that contains itself.
type failsOnCycle struct{}

func (failsOnCycle) reentered(any) (any, bool) { return nil, false }

// ignoresDone gives a treeCopier a done that does nothing.
type ignoresDone struct{}

func (ignoresDone) done(*copyFrame) {}

// copyFrame is an object or array copyTree is copying: its keys or items,
// the index of the next one, and the copy.
type copyFrame struct {
	ordered *OrderedMap    // the object, when ordered
	plain   map[string]any // the object, when a Go map
	keys    []string       // the object's keys, in the order they are copied
	items   []any          // the array's items
	index   int
	outObj  *OrderedMap
	outMap  map[string]any
	outArr  []any
}

// cloneFrame returns a frame copying the object or array v: an *OrderedMap
// into one that keeps its goMap mark, a Go map into a Go map, or with
// ordered into an *OrderedMap, and an array into []any.
func cloneFrame(v any, ordered bool) copyFrame {
	switch val := v.(type) {
	case *OrderedMap:
		out := NewOrderedMapWithCapacity(len(val.keys))
		out.goMap = val.goMap
		return copyFrame{ordered: val, keys: val.keys, outObj: out}
	case map[string]any:
		if ordered {
			return copyFrame{plain: val, keys: MapKeys(val), outObj: NewOrderedMapWithCapacity(len(val))}
		}
		return copyFrame{plain: val, keys: MapKeys(val), outMap: make(map[string]any, len(val))}
	}
	items, _ := AsArray(v)
	return copyFrame{items: items, outArr: make([]any, len(items))}
}

// identity is the object or array the frame copies (see valuePath).
func (f *copyFrame) identity() any {
	if f.ordered != nil {
		return f.ordered
	}
	if f.plain != nil {
		return f.plain
	}
	return f.items
}

// next returns the frame's next child to copy.
func (f *copyFrame) next() (any, bool) {
	switch {
	case f.ordered != nil || f.plain != nil:
		if f.index == len(f.keys) {
			return nil, false
		}
		k := f.keys[f.index]
		f.index++
		if f.ordered != nil {
			return f.ordered.data[k], true
		}
		return f.plain[k], true
	case f.index == len(f.items):
		return nil, false
	}
	f.index++
	return f.items[f.index-1], true
}

// set stores the copy of the child next last returned.
func (f *copyFrame) set(v any) {
	switch {
	case f.outObj != nil:
		f.outObj.Set(f.keys[f.index-1], v)
	case f.outMap != nil:
		f.outMap[f.keys[f.index-1]] = v
	default:
		f.outArr[f.index-1] = v
	}
}

// result returns the frame's copy.
func (f *copyFrame) result() any {
	switch {
	case f.outObj != nil:
		return f.outObj
	case f.outMap != nil:
		return f.outMap
	}
	return f.outArr
}

// valueCloner copies values as $clone does (see CloneValue).
type valueCloner struct {
	failsOnCycle
	ignoresDone
	env *Environment
}

func (valueCloner) leaf(v any) (any, bool) { return cloneLeaf(v) }

// cloneLeaf is the leaf of a copy made as through JSON, as $clone and a
// transform copy: a function, a regex included, becomes "".
func cloneLeaf(v any) (any, bool) {
	v = CollapseSequences(v)
	switch {
	case isCompound(v):
		return v, false
	case IsFunction(v):
		return "", true
	}
	return v, true
}

func (c valueCloner) frame(v any) (copyFrame, error) {
	if err := c.env.Err(); err != nil {
		return copyFrame{}, err
	}
	return cloneFrame(v, false), nil
}

// CloneValue deep-copies v as jsonata-js's $clone does through JSON, where a
// function, a regex included, becomes "". Numbers and other Go values,
// including nil (which is null in Go input), are kept as they are, so no
// precision is lost. It polls env, since shared subtrees make the copy
// exponential in the expression's size.
func CloneValue(v any, env *Environment) (any, error) {
	return copyTree(v, valueCloner{env: env}, &valuePath{what: "clone"})
}

// jsonCopier prepares values for JSON as JSONValue does.
type jsonCopier struct {
	failsOnCycle
	ignoresDone
	prec int
	env  *Environment
}

func (c jsonCopier) leaf(v any) (any, bool) {
	v = CollapseSequences(v)
	if IsNull(v) {
		return nil, true
	}
	if isCompound(v) {
		return v, false
	}
	switch val := v.(type) {
	case json.Number:
		if s, ok := FormatDecimal(val, c.prec); ok {
			return json.Number(s), true
		}
		if isLargeInteger(val) {
			return v, true
		}
		if f, err := val.Float64(); err == nil && c.prec == 0 {
			return roundJS(f), true
		}
		return v, true
	case float64:
		if s, ok := FormatDecimal(val, c.prec); ok {
			return json.Number(s), true
		}
		return roundJS(val), true
	}
	if IsFunction(v) {
		return "", true
	}
	return v, true
}

// frame copies an object into an *OrderedMap, a Go map's keys in sorted
// order, and an array into []any.
func (c jsonCopier) frame(v any) (copyFrame, error) {
	if err := c.env.Err(); err != nil {
		return copyFrame{}, err
	}
	return cloneFrame(v, true), nil
}

// NormalizeTree converts v's internal types to standard Go types for a
// custom function: an *OrderedMap becomes a map[string]any, the null
// sentinel nil and a regex its {"pattern", "flags"} map, which a non-nil
// passed records. An array is copied only when an item needs converting,
// and a Go map is passed through. With shared, an object decoded from input
// gets a normalized view cached on it and shared by every caller, which
// must therefore not modify it. An object or array the value contains
// itself through is passed through unconverted where the walk re-enters it.
// Once the walk is large (see stripMemoAfter), an array it has already
// converted is converted to the same copy when that copy holds no map it
// made, so arrays the value shares may come back shared; with shared an
// object is converted to the same copy too. Without shared every
// occurrence of an object or regex gets a map of its own, which the caller
// may modify, so an object-heavy value that shares subtrees is walked in
// full. A non-nil env is polled for cancellation.
func NormalizeTree(v any, shared bool, passed *RegexMaps, env *Environment) (any, error) {
	if out, isLeaf := normalizeLeaf(v, shared, passed); isLeaf {
		return out, nil
	}
	n := &normalizer{shared: shared, record: passed != nil, env: env}
	if n.record {
		n.passed = *passed
	}
	out, err := copyTree(v, n, new(valuePath))
	if n.record {
		*passed = n.passed
	}
	return out, err
}

// RegexMaps records the maps that stand for regexes in one custom function
// call's arguments, by identity, so returning one returns its regex.
type RegexMaps map[unsafe.Pointer]*RegexLiteral

// add returns re's map, recording it when r is not nil.
func (r *RegexMaps) add(re *RegexLiteral) map[string]any {
	m := re.ToMap()
	if r != nil {
		if *r == nil {
			*r = make(RegexMaps)
		}
		(*r)[reflect.ValueOf(m).UnsafePointer()] = re
	}
	return m
}

// Lookup returns the regex v stands for, if v is one of the recorded maps,
// which none is when r is nil.
func (r *RegexMaps) Lookup(v any) *RegexLiteral {
	m, ok := v.(map[string]any)
	if !ok || r == nil || *r == nil {
		return nil
	}
	return (*r)[reflect.ValueOf(m).UnsafePointer()]
}

// normalizer copies values as NormalizeTree does, recording regexes in
// passed when record is set. It holds the caller's RegexMaps by value, so
// the caller's stays off the heap when the value is a leaf.
type normalizer struct {
	shared bool
	record bool
	passed RegexMaps
	env    *Environment // polled for cancellation, when not nil
	frames int
	// hasMap follows the open frames, reporting whether each one's copy
	// holds a map, which without shared rules out memoizing it.
	hasMap []bool
	// The copies of the objects and arrays copied once memoizing.
	doneObjs map[*OrderedMap]any
	doneArrs map[sliceKey]any
}

func (n *normalizer) leaf(v any) (any, bool) {
	if n.doneObjs != nil {
		if out, ok := n.recall(v); ok {
			return out, true
		}
	}
	var passed *RegexMaps
	if n.record {
		passed = &n.passed
	}
	out, isLeaf := normalizeLeaf(v, n.shared, passed)
	if _, isMap := out.(map[string]any); isMap && len(n.hasMap) > 0 {
		// A Go map is passed through, so only a regex's map or a cached
		// view is made here.
		if _, isGoMap := CollapseSequences(v).(map[string]any); !isGoMap {
			n.hasMap[len(n.hasMap)-1] = true
		}
	}
	return out, isLeaf
}

// normalizeLeaf returns the normalized v, or with isLeaf false the object or
// array v stands for, which NormalizeTree copies with a frame.
func normalizeLeaf(v any, shared bool, passed *RegexMaps) (out any, isLeaf bool) {
	v = CollapseSequences(v)
	switch val := v.(type) {
	case nil, JSONNull:
		return nil, true
	case *RegexLiteral:
		return passed.add(val), true
	case *OrderedMap:
		if shared {
			if view, _ := cachedNormalizedView(val); view != nil {
				return view, true
			}
		}
		return v, false
	case []any, ConsArray, KeptArray, RawSequence:
		arr, _ := AsArray(val)
		if !slices.ContainsFunc(arr, needsNormalize) {
			return arr, true
		}
		return v, false
	}
	return v, true
}

// recall returns the copy already made of the object or array v.
func (n *normalizer) recall(v any) (any, bool) {
	v = CollapseSequences(v)
	if om, ok := v.(*OrderedMap); ok {
		out, ok := n.doneObjs[om]
		return out, ok
	}
	if arr, ok := AsArray(v); ok && len(arr) > 0 {
		out, ok := n.doneArrs[sliceKey{first: &arr[0], n: len(arr)}]
		return out, ok
	}
	return nil, false
}

func (n *normalizer) frame(v any) (copyFrame, error) {
	n.frames++
	if n.env != nil && n.frames%cancelCheckInterval == 0 {
		if err := n.env.Err(); err != nil {
			return copyFrame{}, err
		}
	}
	if n.frames > stripMemoAfter && n.doneObjs == nil {
		n.doneObjs = make(map[*OrderedMap]any)
		n.doneArrs = make(map[sliceKey]any)
	}
	om, ok := v.(*OrderedMap)
	n.hasMap = append(n.hasMap, ok)
	if !ok {
		items, _ := AsArray(v)
		return copyFrame{items: items, outArr: make([]any, len(items))}, nil
	}
	return copyFrame{ordered: om, keys: om.keys, outMap: make(map[string]any, len(om.keys))}, nil
}

func (*normalizer) reentered(v any) (any, bool) { return v, true }

// done caches, with shared, the view of a frozen object the frame copied
// (see cachedNormalizedView), and memoizes the copy once memoizing: with
// shared any copy, and otherwise only one that holds no map, so every
// occurrence of an object gets a map of its own.
func (n *normalizer) done(f *copyFrame) {
	if n.shared && f.ordered != nil {
		cacheNormalizedView(f.ordered, f.outMap)
	}
	hasMap := n.hasMap[len(n.hasMap)-1]
	n.hasMap = n.hasMap[:len(n.hasMap)-1]
	if hasMap && len(n.hasMap) > 0 {
		n.hasMap[len(n.hasMap)-1] = true
	}
	switch {
	case n.doneObjs == nil, hasMap && !n.shared:
	case f.ordered != nil:
		n.doneObjs[f.ordered] = f.outMap
	case len(f.items) > 0:
		n.doneArrs[sliceKey{first: &f.items[0], n: len(f.items)}] = f.outArr
	}
}

// needsNormalize reports whether NormalizeTree converts v or a value in it.
func needsNormalize(v any) bool {
	switch v.(type) {
	case nil:
		return false
	case *OrderedMap, *Sequence, *RegexLiteral, JSONNull, []any, ConsArray, KeptArray, RawSequence:
		return true
	}
	return false
}

// cycleCheckDepth is the depth past which valuePath records the objects it
// walks, so a value that contains itself (which Go input can) is caught one
// cycle later; ordinary values never reach it.
const cycleCheckDepth = 1000

// valuePath tracks the depth of a walk over a value and, past
// cycleCheckDepth, the objects open on the current path. The evaluator's
// walkers over data that keep an explicit stack call enter as they push a
// container's frame and leave as they pop it.
type valuePath struct {
	what  string // the walk, for errors, as in "clone" or "stringify"
	depth int
	open  map[any]struct{}
	all   bool // records containers at every depth (see trackAll)
}

// enter descends into container. It reports false, entering nothing, when
// container is already open on the path, so the value contains itself.
func (p *valuePath) enter(container any) bool {
	if !p.all && p.depth < cycleCheckDepth {
		p.depth++
		return true
	}
	if key := objectIdentity(container); key != nil {
		if _, open := p.open[key]; open {
			return false
		}
		p.mark(key)
	}
	p.depth++
	return true
}

// leave returns from the container enter descended into.
func (p *valuePath) leave(container any) {
	p.depth--
	if p.all || p.depth >= cycleCheckDepth {
		if key := objectIdentity(container); key != nil {
			delete(p.open, key)
		}
	}
}

// trackAll makes p record the containers entered from now on at every
// depth, for a walk that has grown large enough to memoize, so a value
// that contains itself is caught where it first re-enters itself. The walk
// marks the containers already open with markOpen.
func (p *valuePath) trackAll() {
	p.all = true
}

// markOpen records container, entered before trackAll, as open.
func (p *valuePath) markOpen(container any) {
	if key := objectIdentity(container); key != nil {
		p.mark(key)
	}
}

func (p *valuePath) mark(key any) {
	if p.open == nil {
		p.open = make(map[any]struct{})
	}
	p.open[key] = struct{}{}
}

// cycleError is the U1001 a walk that can fail reports where enter found
// the value containing itself.
func (p *valuePath) cycleError() error {
	return &JSONataError{Code: "U1001", Message: "cannot " + p.what + " a value that contains itself"}
}

// objectIdentity returns a comparable identity for an object or a non-empty
// array (see arrayKey), or nil for other values, which cannot contain
// themselves. Go input can contain itself through an object or an array, as
// a := []any{nil}; a[0] = a does.
func objectIdentity(v any) any {
	switch val := v.(type) {
	case *OrderedMap:
		return val
	case map[string]any:
		return reflect.ValueOf(val).UnsafePointer()
	}
	return arrayKey(v)
}

// sameFunc reports whether a and b hold one function value of a pointer or
// func type. Func values are not comparable, but the data word of the
// interface holding one identifies it: the closure in gc, the boxed value
// in TinyGo, which copies of the interface share; for a pointer it is the
// pointer.
func sameFunc(a, b any) bool {
	return reflect.TypeOf(a) == reflect.TypeOf(b) && interfaceData(a) == interfaceData(b)
}

// Assumes a two-word interface whose data word identifies the value; boxing func values otherwise only yields false "unequal".
func interfaceData(v any) unsafe.Pointer {
	return (*[2]unsafe.Pointer)(unsafe.Pointer(&v))[1]
}

// arrayKey identifies a non-empty array by its storage and length (see
// sliceKey), or is nil for an empty array or any other value.
func arrayKey(v any) any {
	if arr, ok := AsArray(v); ok && len(arr) > 0 {
		return sliceKey{first: &arr[0], n: len(arr)}
	}
	return nil
}
