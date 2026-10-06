package parser

import "slices"

// The % operator reads the input of an earlier path step, which jsonata-js
// picks at parse time (seekParent, pushAncestry and resolveAncestry in its
// parser) and gnata ports here. Each % has a slot climbing Level steps: a
// name or wildcard step takes one, a % adds one, and a block or path passes
// the search to its last expression or step. The step where Level reaches 0
// binds the slot's label to its input while the path runs as a tuple stream,
// and the % looks the label up. ProcessAST resolves slots bottom-up; a slot
// that climbs past its path moves to the path's SeekingParent for the
// expression around it, and one left at the top raises S0217.
//
// gnata keeps a step's predicates as subscript nodes around it, where
// jsonata-js stores them on the step. A step's own slots therefore live on
// its base (see stepBase), while subscripts and paths report what
// jsonata-js's equivalent node would (see exprSlots).

// errNoParent is jsonata-js's S0217 for a % whose step cannot be derived.
func errNoParent(node *Node) error {
	return parseError("S0217", node.Type, "the object representing the 'parent' cannot be derived from this expression")
}

// seekParent climbs slot through node, a step or a block's last expression.
func seekParent(node *Node, slot *Slot) error {
	switch node.Type {
	case NodeBinary:
		if node.Value == "[" {
			return seekParent(node.Left, slot)
		}
	case NodeName, NodeWildcard:
		slot.Level--
		if slot.Level == 0 {
			if node.Ancestor != nil {
				slot.Label = node.Ancestor.Label
			}
			node.Ancestor = slot
		}
		return nil
	case NodeParent:
		slot.Level++
		return nil
	case NodeBlock:
		if len(node.Expressions) == 0 {
			return nil
		}
		node.TupleResult = true
		last := len(node.Expressions) - 1
		if err := seekParent(node.Expressions[last], slot); err != nil {
			return err
		}
		// jsonata-js makes a name or #$var step a path, which yields its
		// tuples; any other step yields its value.
		if expr := node.Expressions[last]; expr.Type != NodePath && isPathLike(expr) {
			node.Expressions[last] = wrapStep(expr, pathSlots(expr))
			node.Expressions[last].TupleResult = true
		}
		return nil
	case NodePath:
		node.TupleResult = true
		for _, v := range slices.Backward(node.Steps) {
			if err := seekParent(v, slot); err != nil {
				return err
			}
			if slot.Level == 0 {
				break
			}
		}
		return nil
	}
	return errNoParent(node)
}

// resolvePathAncestry resolves each step's slots against the steps before
// it, as jsonata-js does when each step joins the path, and sets the path's
// SeekingParent to the slots that climb past it. Only a leading % or sort
// passes its own slots on: jsonata-js drops those of any other first step.
func resolvePathAncestry(path *Node) error {
	var seeking []*Slot
	switch first := stepBase(path.Steps[0]); first.Type {
	case NodeParent:
		seeking = []*Slot{first.Slot}
	case NodeSort:
		seeking = slices.Clone(first.SeekingParent)
	}
	for i := 1; i < len(path.Steps); i++ {
		base := stepBase(path.Steps[i])
		var err error
		if seeking, err = climbSteps(path.Steps[:i], ownSlots(base), seeking); err != nil {
			return err
		}
	}
	path.SeekingParent = seeking
	return nil
}

// climbSteps climbs each slot back through steps, from the last, appending
// the slots that climb past the first step to seeking. A run of @$var steps
// counts as its first step, since a focus keeps the context.
func climbSteps(steps []*Node, slots, seeking []*Slot) ([]*Slot, error) {
	for _, slot := range slots {
		for i := len(steps) - 1; slot.Level > 0; {
			if i < 0 {
				seeking = append(seeking, slot)
				break
			}
			step := steps[i]
			i--
			for i >= 0 && stepBase(step).Focus != "" && stepBase(steps[i]).Focus != "" {
				step = steps[i]
				i--
			}
			if err := seekParent(step, slot); err != nil {
				return nil, err
			}
		}
	}
	return seeking, nil
}

// resolvePredicateAncestry handles subscript's predicate slots: one level up
// is the subscripted step's input, so the step is sought; the others climb
// one level and join the step's slots, unless the step is a sort, whose
// slots jsonata-js never resolves.
func resolvePredicateAncestry(subscript *Node) error {
	slots := predicateSlots(subscript.Right)
	if len(slots) == 0 {
		return nil
	}
	base := stepBase(subscript.Left)
	for _, slot := range slots {
		if slot.Level != 1 {
			slot.Level--
			continue
		}
		if err := seekParent(base, slot); err != nil {
			return err
		}
	}
	if base.Type != NodeSort {
		base.SeekingParent = append(base.SeekingParent, slots...)
	}
	return nil
}

// resolveSortAncestry resolves a sort's term slots against its Left's steps
// and sets the sort's SeekingParent as jsonata-js's path ending in the sort
// would have it. It also sets Tuple when Left binds anything.
func resolveSortAncestry(sort *Node) error {
	steps := []*Node{sort.Left}
	if sort.Left.Type == NodePath {
		steps = sort.Left.Steps
	}
	seeking := slices.Clone(pathSlots(sort.Left))
	for _, term := range sort.Terms {
		var err error
		if seeking, err = climbSteps(steps, exprSlots(term.Expression), seeking); err != nil {
			return err
		}
	}
	sort.SeekingParent = seeking
	sort.Tuple = hasBinding(sort.Left)
	return nil
}

// stepBase returns the step a subscript chain applies to, looking through a
// path to its last step. processSubscript records each subscript's, so a
// long chain is not rescanned at every link.
func stepBase(node *Node) *Node {
	for {
		switch {
		case node.base != nil:
			return node.base
		case node.Type == NodePath && len(node.Steps) > 0:
			node = node.Steps[len(node.Steps)-1]
		case node.Type == NodeBinary && node.Value == "[" && node.Left != nil:
			node = node.Left
		default:
			return node
		}
	}
}

// ownSlots returns the slots a step resolves when it joins a path: those
// passed up to it, and a %'s own.
func ownSlots(step *Node) []*Slot {
	if step.Type == NodeParent {
		return append(slices.Clip(step.SeekingParent), step.Slot)
	}
	return step.SeekingParent
}

// exprSlots returns the slots node passes to the expression around it.
// jsonata-js turns a name, a #$var step, a path or a sort, with any
// subscripts, into a path, which passes on only the slots that climbed past
// it; any other node passes on its own.
func exprSlots(node *Node) []*Slot {
	if node == nil {
		return nil
	}
	if isPathLike(node) {
		return pathSlots(node)
	}
	return ownSlots(stepBase(node))
}

// predicateSlots returns the slots a predicate passes to its step:
// jsonata-js leaves a bare % predicate, as in a[%], unresolved.
func predicateSlots(node *Node) []*Slot {
	if node.Type == NodeParent {
		return node.SeekingParent
	}
	return exprSlots(node)
}

// isPathLike reports whether jsonata-js turns node, processed or not, into
// a path.
func isPathLike(node *Node) bool {
	for {
		switch {
		case node.Type == NodePath || node.Type == NodeSort || node.Type == NodeName || node.Index != "" ||
			node.Type == NodeBinary && node.Value == ".":
			return true
		case node.Type == NodeBinary && node.Value == "[" && node.Left != nil:
			node = node.Left
		default:
			return false
		}
	}
}

// pathSlots returns the slots that climbed past the path node is, or that
// a subscript chain applies to; nil for any other node.
func pathSlots(node *Node) []*Slot {
	for node.Type == NodeBinary && node.Value == "[" && node.Left != nil {
		node = node.Left
	}
	if node.Type == NodePath || node.Type == NodeSort {
		return node.SeekingParent
	}
	return nil
}

// concatSlots joins the slots several operands pass to their expression.
func concatSlots(nodes ...*Node) (slots []*Slot) {
	for _, node := range nodes {
		slots = append(slots, exprSlots(node)...)
	}
	return slots
}
