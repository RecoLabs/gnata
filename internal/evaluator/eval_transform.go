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
// internal array type becomes a plain array, and a function becomes "", so
// no value the clone shares can hold an object of another clone (a
// function's captured context could). A Go map becomes an
// *OrderedMap marked goMap, so evaluation sees one object type and the
// public boundary returns it as a Go map again. It polls env, since shared
// subtrees make the copy exponential in the expression's size.
func (c *transformClone) clone(v any) (any, error) {
	switch val := v.(type) {
	case *OrderedMap, map[string]any:
		if err := c.env.Err(); err != nil {
			return nil, err
		}
		m := NewOrderedMapWithCapacity(MapLen(val))
		if om, ok := val.(*OrderedMap); ok {
			m.goMap = om.goMap
		} else {
			m.goMap = true
		}
		m.clone.Store(c.id)
		var err error
		MapRange(val, func(k string, vv any) bool {
			if vv, err = c.clone(vv); err == nil {
				m.Set(k, vv)
			}
			return err == nil
		})
		return m, err
	case []any, ConsArray, KeptArray, RawSequence:
		if err := c.env.Err(); err != nil {
			return nil, err
		}
		arr, _ := AsArray(val)
		s := make([]any, len(arr))
		for i, vv := range arr {
			var err error
			if s[i], err = c.clone(vv); err != nil {
				return nil, err
			}
		}
		return s, nil
	case BuiltinFunction, EnvAwareBuiltin, *Lambda, *SignedBuiltin:
		return "", nil
	default:
		return v, nil
	}
}

// owns reports whether v is one of the clone's objects.
func (c *transformClone) owns(v any) bool {
	om, ok := v.(*OrderedMap)
	return ok && om.clone.Load() == c.id
}

// leadsToMember reports whether v is or holds one of the clone's objects.
func (c *transformClone) leadsToMember(v any) bool {
	switch {
	case c.owns(v):
		return true
	case !isContainer(v):
		return false
	case !worthMemo(v):
		return anyChild(v, c.leadsToMember)
	}
	key := containerKey(v)
	if leads, ok := c.leadsIn[key]; ok {
		return leads
	}
	leads := anyChild(v, c.leadsToMember)
	c.leadsIn[key] = leads
	return leads
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
	d := detacher{transformClone: c, target: target}
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
	if om, ok := v.(*OrderedMap); ok && om == d.target {
		return true, nil
	}
	if d.ownedLeaf && d.owns(v) || !d.leadsToMember(v) {
		return false, nil
	}
	if err := d.env.Err(); err != nil {
		return false, err
	}
	var err error
	walk := func() bool {
		return anyChild(v, func(child any) bool {
			var found bool
			found, err = d.reach(child)
			return found || err != nil
		}) && err == nil
	}
	if !worthMemo(v) {
		return walk(), err
	}
	key := containerKey(v)
	if r, ok := d.reaches[key]; ok {
		return r, nil
	}
	if d.reaches == nil {
		d.reaches = make(map[any]bool)
	}
	d.reaches[key] = walk()
	return d.reaches[key], err
}

// copy copies the containers of v that reach target, keeping their types,
// and target itself without copying its children.
func (d *detacher) copy(v any) (any, error) {
	if reaches, err := d.reach(v); err != nil || !reaches {
		return v, err
	}
	key := containerKey(v)
	if cp, ok := d.copies[key]; ok {
		return cp, nil
	}
	if om, ok := v.(*OrderedMap); ok {
		m := NewOrderedMapWithCapacity(len(om.keys))
		m.goMap = om.goMap
		d.copies[key] = m
		for _, k := range om.keys {
			val := om.data[k]
			if om != d.target {
				var err error
				if val, err = d.copy(val); err != nil {
					return nil, err
				}
			}
			m.Set(k, val)
		}
		return m, nil
	}
	arr, _ := AsArray(v)
	out := make([]any, len(arr))
	for i, child := range arr {
		var err error
		if out[i], err = d.copy(child); err != nil {
			return nil, err
		}
	}
	var copied any = out
	switch v.(type) {
	case ConsArray:
		copied = ConsArray(out)
	case KeptArray:
		copied = KeptArray(out)
	case RawSequence:
		copied = RawSequence(out)
	}
	d.copies[key] = copied
	return copied, nil
}

// containerKey returns the identity the walkers key a container by: an
// object that is not frozen, or a non-empty array's storage and length.
// It is nil for anything else, which holds no container that can change.
func containerKey(v any) any {
	if !isContainer(v) {
		return nil
	}
	if om, ok := v.(*OrderedMap); ok {
		return om
	}
	arr, _ := AsArray(v)
	return sliceKey{first: &arr[0], n: len(arr)}
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
		return applyTransform(node, doc, env)
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
	cloned, err := c.clone(input)
	if err != nil {
		return nil, err
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
