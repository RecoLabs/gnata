package evaluator

import (
	"slices"
	"sync/atomic"

	"github.com/recolabs/gnata/internal/parser"
)

// cloneIDs numbers transforms process-wide, so a transform recognizes the
// objects it cloned (OrderedMap.clone). The public boundary clears the mark
// on results (see stripper.object), so only an object still inside an
// evaluation, or captured by a function value it returned, keeps one; such
// an object is taken for a later transform's own only if 2^32 transforms
// run in the process while it lives.
var cloneIDs atomic.Uint32

// newCloneID returns a transform's id, skipping 0, which marks no clone.
func newCloneID() uint32 {
	for {
		if id := cloneIDs.Add(1); id != 0 {
			return id
		}
	}
}

// transformClone is a transform's copy of its input. Its objects, marked
// with id, are the only ones the transform may change.
type transformClone struct {
	failsOnCycle
	ignoresDone
	env *Environment
	id  uint32
	// ownedLeaf reports that an update cannot hold one of the clone's
	// objects that leads back to the target (see detach).
	ownedLeaf bool
	// leadsIn caches, for a container that is not the clone's and is worth
	// memoizing, whether it holds one of the clone's objects; such
	// containers never change.
	leadsIn map[any]bool
}

// clone copies v as jsonata-js clones a transform's input, through JSON: an
// internal array type becomes a plain array, and a function, a regex
// included, becomes "", so no value the clone shares can hold an object of
// another clone (a function's captured context could). A Go map becomes an
// *OrderedMap marked goMap, so evaluation sees one object type and the
// public boundary returns it as a Go map again. It polls env, since shared
// subtrees make the copy exponential in the expression's size.
func (c *transformClone) clone(v any) (any, error) {
	return copyTree(v, c, &valuePath{what: "clone"})
}

func (c *transformClone) leaf(v any) (any, bool) { return cloneLeaf(v) }

func (c *transformClone) frame(v any) (copyFrame, error) {
	if err := c.env.Err(); err != nil {
		return copyFrame{}, err
	}
	f := cloneFrame(v, true)
	if f.outObj != nil {
		f.outObj.clone.Store(c.id)
		if f.plain != nil {
			f.outObj.goMap = true
		}
	}
	return f, nil
}

// owns reports whether v is one of the clone's objects.
func (c *transformClone) owns(v any) bool {
	om, ok := v.(*OrderedMap)
	return ok && om.clone.Load() == c.id
}

// leadsToMember reports whether v is or holds one of the clone's objects.
// It walks nested containers from an explicit stack (see searchTree).
func (c *transformClone) leadsToMember(v any) (bool, error) {
	return searchTree(v, c.leadsIn, c.env, func(u any) (bool, bool, error) {
		switch {
		case c.owns(u):
			return true, false, nil
		case !isContainer(u):
			return false, false, nil
		}
		return false, true, nil
	})
}

// searchTree reports whether visit finds a value in the tree under v. visit
// returns, for a value, whether it is a match, or else whether to search
// its children. A container worth memoizing (see worthMemo) records its
// answer in memo, so a subtree shared by many parents is searched once. It
// walks nested containers from an explicit stack of frames rather than by
// recursion, so a deeply nested value cannot overflow the goroutine stack,
// and polls env. A container the value contains itself through, which only
// Go input can, is skipped where the walk re-enters it, as the frame open
// on it searches its children; a match is a transform's object, which Go
// input does not hold.
func searchTree(v any, memo map[any]bool, env *Environment, visit func(any) (found, open bool, _ error)) (bool, error) {
	type frame struct {
		children childCursor
		self     any
		key      any // where to memoize the answer, or nil
	}
	var buf [8]frame
	stack := buf[:0]
	var path valuePath
	visits := 0
	// open reports whether u is a match, or else pushes its frame when its
	// children are to be searched.
	open := func(u any) (bool, error) {
		if visits%cancelCheckInterval == 0 {
			if err := env.Err(); err != nil {
				return false, err
			}
		}
		visits++
		found, search, err := visit(u)
		if found || !search || err != nil {
			return found, err
		}
		var key any
		if worthMemo(u) {
			key = containerKey(u)
			if found, ok := memo[key]; ok {
				return found, nil
			}
		}
		if !path.enter(u) {
			return false, nil
		}
		stack = append(stack, frame{children: newChildCursor(u), self: u, key: key})
		return false, nil
	}
	found, err := open(v)
	for !found && err == nil && len(stack) > 0 {
		top := &stack[len(stack)-1]
		child, ok := top.children.next()
		if !ok {
			if top.key != nil {
				memo[top.key] = false
			}
			path.leave(top.self)
			stack = stack[:len(stack)-1]
			continue
		}
		found, err = open(child)
	}
	if found {
		// Every container on the stack holds the match.
		for _, f := range stack {
			if f.key != nil {
				memo[f.key] = true
			}
		}
	}
	return found, err
}

// detach returns update with every path from it to target copied, ending
// at a copy of target that shares target's children, so merging it into
// target cannot make the document contain itself. jsonata-js merges the
// value as is, and so can build a cycle it then fails to serialize; any
// other part stays shared, as there.
//
// An update is evaluated with target as its context, so unless the
// transform binds a variable (one bound by its pattern or an earlier
// target's update stays bound, as in jsonata-js), the only objects of the
// clone it can hold are target and target's descendants; since the
// document has no cycle, no descendant leads back to target, and reach
// stops at them.
func (c *transformClone) detach(update any, target *OrderedMap) (any, error) {
	d := detacher{transformClone: c, target: target, reaches: make(map[any]bool)}
	if reaches, err := d.reach(update); err != nil || !reaches {
		return update, err
	}
	d.copies = make(map[any]any)
	return d.copy(update)
}

// detacher is one detach call, memoizing by container identity.
type detacher struct {
	*transformClone
	target  *OrderedMap
	reaches map[any]bool
	copies  map[any]any
}

// reach reports whether target is v or reachable from v.
func (d *detacher) reach(v any) (bool, error) {
	return searchTree(v, d.reaches, d.env, func(u any) (found, open bool, _ error) {
		if om, ok := u.(*OrderedMap); ok && om == d.target {
			return true, false, nil
		}
		if d.ownedLeaf && d.owns(u) {
			return false, false, nil
		}
		leads, err := d.leadsToMember(u)
		return false, leads, err
	})
}

// copy copies the containers of v that reach target, keeping their types,
// and target itself without copying its children. It walks nested
// containers from an explicit stack of frames rather than by recursion, so
// a deeply nested value cannot overflow the goroutine stack.
func (d *detacher) copy(v any) (any, error) {
	var buf [8]detachFrame
	stack := buf[:0]
	// open returns v's copy, unless it is a container to copy child by
	// child, for which it pushes a frame.
	open := func(v any) (_ any, pushed bool, _ error) {
		if reaches, err := d.reach(v); err != nil || !reaches {
			return v, false, err
		}
		key := containerKey(v)
		if cp, ok := d.copies[key]; ok {
			return cp, false, nil
		}
		if om, ok := v.(*OrderedMap); ok {
			m := NewOrderedMapWithCapacity(len(om.keys))
			m.goMap = om.goMap
			d.copies[key] = m
			if om == d.target {
				for _, k := range om.keys {
					m.Set(k, om.data[k])
				}
				return m, false, nil
			}
			stack = append(stack, detachFrame{children: newChildCursor(om), key: key, obj: m})
			return nil, true, nil
		}
		arr, _ := AsArray(v)
		stack = append(stack, detachFrame{children: newChildCursor(v), src: v, key: key, out: make([]any, 0, len(arr))})
		return nil, true, nil
	}
	if copied, pushed, err := open(v); err != nil || !pushed {
		return copied, err
	}
	for {
		top := &stack[len(stack)-1]
		if child, ok := top.children.next(); ok {
			copied, pushed, err := open(child)
			if err != nil {
				return nil, err
			}
			if !pushed {
				top.add(copied)
			}
			continue
		}
		copied := top.result()
		d.copies[top.key] = copied
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			return copied, nil
		}
		stack[len(stack)-1].add(copied)
	}
}

// detachFrame is a container detacher.copy is copying.
type detachFrame struct {
	children childCursor
	src      any         // the array copied, for its type
	key      any         // the container's identity (see containerKey)
	obj      *OrderedMap // the object's copy
	out      []any       // the array's copy
}

// add records the copy of the child children.next last returned.
func (f *detachFrame) add(v any) {
	if f.obj != nil {
		f.obj.Set(f.children.obj.keys[f.children.index-1], v)
		return
	}
	f.out = append(f.out, v)
}

// result returns the container's copy, an array keeping its type.
func (f *detachFrame) result() any {
	if f.obj != nil {
		return f.obj
	}
	switch f.src.(type) {
	case ConsArray:
		return ConsArray(f.out)
	case KeptArray:
		return KeptArray(f.out)
	case RawSequence:
		return RawSequence(f.out)
	}
	return f.out
}

// containerKey returns the identity the walkers key a container (see
// isContainer) by: the object, or the array's storage and length. It is nil
// for anything else.
func containerKey(v any) any {
	if !isContainer(v) {
		return nil
	}
	if om, ok := v.(*OrderedMap); ok {
		return om
	}
	return arrayKey(v)
}

// anyChild reports whether yield returns true for a child of the object or
// array v.
func anyChild(v any, yield func(any) bool) bool {
	if om, ok := v.(*OrderedMap); ok {
		return slices.ContainsFunc(om.keys, func(k string) bool { return yield(om.data[k]) })
	}
	arr, _ := AsArray(v)
	return slices.ContainsFunc(arr, yield)
}

func evalTransform(node *parser.Node, _ any, env *Environment) (any, error) {
	return BuiltinFunction(func(args []any, focus any) (any, error) {
		var doc any
		if len(args) > 0 {
			doc = args[0]
		} else {
			doc = focus
		}
		// Each call evaluates the clauses on the Go stack. Unlike a lambda's
		// body, they get no free depth: transforms can call each other with
		// no lambda call between, so the call depth does not bound them.
		return env.callCounter().callNested(1+int(node.Depth)/levelsPerNestedCall, func() (any, error) {
			return applyTransform(node, doc, env)
		})
	}), nil
}

var (
	errTransformUpdate = JSONataError{
		Code: "T2011", Message: "the insert/update clause of the transform expression must evaluate to an object",
	}
	errTransformDelete = JSONataError{
		Code: "T2012", Message: "the delete clause of the transform expression must evaluate to an array of strings",
	}
)

func applyTransform(node *parser.Node, input any, env *Environment) (any, error) {
	if input == nil {
		return nil, nil
	}
	if !IsMap(input) && !IsArray(input) {
		return nil, &JSONataError{Code: "T0410", Message: "the transform expression must be applied to an object or an array"}
	}
	c := &transformClone{
		env: env, id: newCloneID(), leadsIn: make(map[any]bool),
		ownedLeaf: node.NoBinds,
	}
	// jsonata-js copies the input with whatever $clone is bound to. The
	// clauses below modify the copy, so a rebound $clone is given, and its
	// result taken as, a copy gnata owns: input can be shared by concurrent
	// evaluations.
	cloneFn, _ := env.Lookup("clone")
	if !isCallable(cloneFn) {
		return nil, &JSONataError{Code: "T2013", Message: "the transform expression requires $clone to be a function"}
	}
	cloned, err := c.clone(input)
	if err != nil {
		return nil, err
	}
	if sb, isBuiltin := cloneFn.(*SignedBuiltin); !isBuiltin || sb.Name != "clone" {
		result, err := callFunction(cloneFn, []any{cloned}, Null, env)
		if err != nil {
			return nil, err
		}
		if cloned, err = c.clone(result); err != nil {
			return nil, err
		}
	}

	matched, err := Eval(node.Pattern, cloned, env)
	if err != nil {
		return nil, err
	}

	var targets []any
	if IsMap(matched) {
		targets = []any{matched}
	} else if items, ok := AsArray(matched); ok {
		for _, item := range items {
			if IsMap(item) {
				targets = append(targets, item)
			}
		}
	}

	if len(targets) == 0 && matched != nil {
		// Pattern matched a non-object value: validate update/delete types
		// but don't mutate (original jsonata-js behavior for non-object patterns).
		return cloned, validateTransformClauses(node, cloned, env)
	}

	for _, target := range targets {
		// Only the clone changes: a pattern reaching an object outside it, as
		// through $$, must not change the caller's input (jsonata-js does).
		if !c.owns(target) {
			if err := validateTransformClauses(node, target, env); err != nil {
				return nil, err
			}
			continue
		}
		if err := c.applyTarget(node, target.(*OrderedMap)); err != nil {
			return nil, err
		}
	}
	return cloned, nil
}

// validateTransformClauses evaluates update/delete clauses for type-checking only,
// without mutating the target. Used when the pattern matches a non-object value.
func validateTransformClauses(node *parser.Node, target any, env *Environment) error {
	if node.Update != nil {
		if updateVal, err := Eval(node.Update, target, env); err != nil {
			return err
		} else if updateVal != nil && !IsNull(updateVal) && !IsMap(updateVal) {
			return &errTransformUpdate
		}
	}
	if node.Delete != nil {
		_, err := deleteKeys(node, target, env)
		return err
	}
	return nil
}

func (c *transformClone) applyTarget(node *parser.Node, target *OrderedMap) error {
	if node.Update != nil {
		if updateVal, err := Eval(node.Update, target, c.env); err != nil {
			return err
		} else if updateVal != nil && !IsNull(updateVal) {
			if !IsMap(updateVal) {
				return &errTransformUpdate
			}
			if updateVal, err = c.detach(updateVal, target); err != nil {
				return err
			}
			MapRange(updateVal, func(k string, v any) bool {
				target.Set(k, v)
				return true
			})
		}
	}
	if node.Delete != nil {
		keys, err := deleteKeys(node, target, c.env)
		if err != nil {
			return err
		}
		for _, k := range keys {
			target.Delete(k)
		}
	}
	return nil
}

// deleteKeys evaluates a transform's delete clause, which must be a string
// or an array of strings, as in jsonata-js.
func deleteKeys(node *parser.Node, target any, env *Environment) ([]string, error) {
	deleteVal, err := Eval(node.Delete, target, env)
	if err != nil {
		return nil, err
	}
	if deleteVal = settleRaw(deleteVal, false); deleteVal == nil || IsNull(deleteVal) {
		return nil, nil
	}
	if s, ok := deleteVal.(string); ok {
		return []string{s}, nil
	}
	items, isArr := AsArray(deleteVal)
	if !isArr {
		return nil, &errTransformDelete
	}
	keys := make([]string, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, &errTransformDelete
		}
		keys[i] = s
	}
	return keys, nil
}
