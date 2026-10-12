package parser

// Accessors for the fields kept in nodeExt and nodeRare. Getters are
// nil-safe; setters allocate the holder only for a non-zero value and
// return the node so construction can be chained.

func (n *Node) extFor(set bool) *nodeExt {
	if n.ext == nil && set {
		n.ext = &nodeExt{}
	}
	return n.ext
}

func (n *Node) rareFor(set bool) *nodeRare {
	ext := n.extFor(set)
	if ext == nil {
		return nil
	}
	if ext.rare == nil && set {
		ext.rare = &nodeRare{}
	}
	return ext.rare
}

func (n *Node) NumVal() float64 {
	if n.ext == nil {
		return 0
	}
	return n.ext.numVal
}

func (n *Node) SetNumVal(v float64) *Node {
	if ext := n.extFor(v != 0); ext != nil {
		ext.numVal = v
	}
	return n
}

func (n *Node) Steps() []*Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.steps
}

func (n *Node) SetSteps(v []*Node) *Node {
	if ext := n.extFor(len(v) != 0); ext != nil {
		ext.steps = v
	}
	return n
}

func (n *Node) Expressions() []*Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.expressions
}

func (n *Node) SetExpressions(v []*Node) *Node {
	if ext := n.extFor(len(v) != 0); ext != nil {
		ext.expressions = v
	}
	return n
}

func (n *Node) LHS() []*Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.lhs
}

func (n *Node) SetLHS(v []*Node) *Node {
	if ext := n.extFor(len(v) != 0); ext != nil {
		ext.lhs = v
	}
	return n
}

func (n *Node) Arguments() []*Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.arguments
}

func (n *Node) SetArguments(v []*Node) *Node {
	if ext := n.extFor(len(v) != 0); ext != nil {
		ext.arguments = v
	}
	return n
}

func (n *Node) Body() *Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.body
}

func (n *Node) SetBody(v *Node) *Node {
	if ext := n.extFor(v != nil); ext != nil {
		ext.body = v
	}
	return n
}

func (n *Node) Procedure() *Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.procedure
}

func (n *Node) SetProcedure(v *Node) *Node {
	if ext := n.extFor(v != nil); ext != nil {
		ext.procedure = v
	}
	return n
}

func (n *Node) Expression() *Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.expression
}

func (n *Node) SetExpression(v *Node) *Node {
	if ext := n.extFor(v != nil); ext != nil {
		ext.expression = v
	}
	return n
}

func (n *Node) Condition() *Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.condition
}

func (n *Node) SetCondition(v *Node) *Node {
	if ext := n.extFor(v != nil); ext != nil {
		ext.condition = v
	}
	return n
}

func (n *Node) Then() *Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.then
}

func (n *Node) SetThen(v *Node) *Node {
	if ext := n.extFor(v != nil); ext != nil {
		ext.then = v
	}
	return n
}

func (n *Node) Else() *Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.els
}

func (n *Node) SetElse(v *Node) *Node {
	if ext := n.extFor(v != nil); ext != nil {
		ext.els = v
	}
	return n
}

func (n *Node) base() *Node {
	if n.ext == nil {
		return nil
	}
	return n.ext.base
}

func (n *Node) setBase(v *Node) *Node {
	if ext := n.extFor(v != nil); ext != nil {
		ext.base = v
	}
	return n
}

func (n *Node) Terms() []SortTerm {
	if n.ext == nil || n.ext.rare == nil {
		return nil
	}
	return n.ext.rare.terms
}

func (n *Node) SetTerms(v []SortTerm) *Node {
	if rare := n.rareFor(len(v) != 0); rare != nil {
		rare.terms = v
	}
	return n
}

func (n *Node) Pattern() *Node {
	if n.ext == nil || n.ext.rare == nil {
		return nil
	}
	return n.ext.rare.pattern
}

func (n *Node) SetPattern(v *Node) *Node {
	if rare := n.rareFor(v != nil); rare != nil {
		rare.pattern = v
	}
	return n
}

func (n *Node) Update() *Node {
	if n.ext == nil || n.ext.rare == nil {
		return nil
	}
	return n.ext.rare.update
}

func (n *Node) SetUpdate(v *Node) *Node {
	if rare := n.rareFor(v != nil); rare != nil {
		rare.update = v
	}
	return n
}

func (n *Node) Delete() *Node {
	if n.ext == nil || n.ext.rare == nil {
		return nil
	}
	return n.ext.rare.delete
}

func (n *Node) SetDelete(v *Node) *Node {
	if rare := n.rareFor(v != nil); rare != nil {
		rare.delete = v
	}
	return n
}

func (n *Node) Signature() *Signature {
	if n.ext == nil || n.ext.rare == nil {
		return nil
	}
	return n.ext.rare.signature
}

func (n *Node) SetSignature(v *Signature) *Node {
	if rare := n.rareFor(v != nil); rare != nil {
		rare.signature = v
	}
	return n
}

func (n *Node) Depth() uint16 {
	if n.ext == nil || n.ext.rare == nil {
		return 0
	}
	return n.ext.rare.depth
}

func (n *Node) SetDepth(v uint16) *Node {
	if rare := n.rareFor(v != 0); rare != nil {
		rare.depth = v
	}
	return n
}

func (n *Node) Focus() string {
	if n.ext == nil || n.ext.rare == nil {
		return ""
	}
	return n.ext.rare.focus
}

func (n *Node) SetFocus(v string) *Node {
	if rare := n.rareFor(v != ""); rare != nil {
		rare.focus = v
	}
	return n
}

func (n *Node) Index() string {
	if n.ext == nil || n.ext.rare == nil {
		return ""
	}
	return n.ext.rare.index
}

func (n *Node) SetIndex(v string) *Node {
	if rare := n.rareFor(v != ""); rare != nil {
		rare.index = v
	}
	return n
}

func (n *Node) Slot() *Slot {
	if n.ext == nil || n.ext.rare == nil {
		return nil
	}
	return n.ext.rare.slot
}

func (n *Node) SetSlot(v *Slot) *Node {
	if rare := n.rareFor(v != nil); rare != nil {
		rare.slot = v
	}
	return n
}

func (n *Node) Ancestor() *Slot {
	if n.ext == nil || n.ext.rare == nil {
		return nil
	}
	return n.ext.rare.ancestor
}

func (n *Node) SetAncestor(v *Slot) *Node {
	if rare := n.rareFor(v != nil); rare != nil {
		rare.ancestor = v
	}
	return n
}

func (n *Node) Group() *GroupExpr {
	if n.ext == nil || n.ext.rare == nil {
		return nil
	}
	return n.ext.rare.group
}

func (n *Node) SetGroup(v *GroupExpr) *Node {
	if rare := n.rareFor(v != nil); rare != nil {
		rare.group = v
	}
	return n
}

func (n *Node) SeekingParent() []*Slot {
	if n.ext == nil || n.ext.rare == nil {
		return nil
	}
	return n.ext.rare.seekingParent
}

func (n *Node) SetSeekingParent(v []*Slot) *Node {
	if rare := n.rareFor(len(v) != 0); rare != nil {
		rare.seekingParent = v
	}
	return n
}

func (n *Node) KeepArray() bool { return n.flags&flagKeepArray != 0 }

func (n *Node) SetKeepArray(v bool) *Node {
	if v {
		n.flags |= flagKeepArray
	} else {
		n.flags &^= flagKeepArray
	}
	return n
}

func (n *Node) KeepSingletonArray() bool { return n.flags&flagKeepSingletonArray != 0 }

func (n *Node) SetKeepSingletonArray(v bool) *Node {
	if v {
		n.flags |= flagKeepSingletonArray
	} else {
		n.flags &^= flagKeepSingletonArray
	}
	return n
}

func (n *Node) ConsArray() bool { return n.flags&flagConsArray != 0 }

func (n *Node) SetConsArray(v bool) *Node {
	if v {
		n.flags |= flagConsArray
	} else {
		n.flags &^= flagConsArray
	}
	return n
}

func (n *Node) Thunk() bool { return n.flags&flagThunk != 0 }

func (n *Node) SetThunk(v bool) *Node {
	if v {
		n.flags |= flagThunk
	} else {
		n.flags &^= flagThunk
	}
	return n
}

func (n *Node) TailContext() bool { return n.flags&flagTailContext != 0 }

func (n *Node) SetTailContext(v bool) *Node {
	if v {
		n.flags |= flagTailContext
	} else {
		n.flags &^= flagTailContext
	}
	return n
}

func (n *Node) Tuple() bool { return n.flags&flagTuple != 0 }

func (n *Node) SetTuple(v bool) *Node {
	if v {
		n.flags |= flagTuple
	} else {
		n.flags &^= flagTuple
	}
	return n
}

func (n *Node) RootContext() bool { return n.flags&flagRootContext != 0 }

func (n *Node) SetRootContext(v bool) *Node {
	if v {
		n.flags |= flagRootContext
	} else {
		n.flags &^= flagRootContext
	}
	return n
}

func (n *Node) PathStage() bool { return n.flags&flagPathStage != 0 }

func (n *Node) SetPathStage(v bool) *Node {
	if v {
		n.flags |= flagPathStage
	} else {
		n.flags &^= flagPathStage
	}
	return n
}

func (n *Node) IndexKeepArray() bool { return n.flags&flagIndexKeepArray != 0 }

func (n *Node) SetIndexKeepArray(v bool) *Node {
	if v {
		n.flags |= flagIndexKeepArray
	} else {
		n.flags &^= flagIndexKeepArray
	}
	return n
}

func (n *Node) KeptAfterIndex() bool { return n.flags&flagKeptAfterIndex != 0 }

func (n *Node) SetKeptAfterIndex(v bool) *Node {
	if v {
		n.flags |= flagKeptAfterIndex
	} else {
		n.flags &^= flagKeptAfterIndex
	}
	return n
}

func (n *Node) NoBinds() bool { return n.flags&flagNoBinds != 0 }

func (n *Node) SetNoBinds(v bool) *Node {
	if v {
		n.flags |= flagNoBinds
	} else {
		n.flags &^= flagNoBinds
	}
	return n
}

func (n *Node) TupleResult() bool { return n.flags&flagTupleResult != 0 }

func (n *Node) SetTupleResult(v bool) *Node {
	if v {
		n.flags |= flagTupleResult
	} else {
		n.flags &^= flagTupleResult
	}
	return n
}

func (n *Node) indexLast() bool { return n.flags&flagIndexLast != 0 }

func (n *Node) setIndexLast(v bool) *Node {
	if v {
		n.flags |= flagIndexLast
	} else {
		n.flags &^= flagIndexLast
	}
	return n
}

// Clone returns a shallow copy of n whose ext and rare holders are its own,
// so setting a field on the copy leaves n unchanged.
func (n *Node) Clone() *Node {
	c := *n
	if n.ext != nil {
		ext := *n.ext
		if ext.rare != nil {
			rare := *ext.rare
			ext.rare = &rare
		}
		c.ext = &ext
	}
	return &c
}
