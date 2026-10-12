package parser

import (
	"testing"
	"unsafe"
)

// Every AST node is allocated and kept with the compiled expression, and
// most nodes are leaves that only use the fields kept inline in Node. A
// field added to Node is paid by every node (64-byte size class today);
// add it to nodeExt or nodeRare unless nearly every node needs it.
func TestNodeSize(t *testing.T) {
	if size := unsafe.Sizeof(Node{}); size != 56 {
		t.Fatalf("Node is %d bytes, want 56", size)
	}
	if size := unsafe.Sizeof(nodeExt{}); size != 168 {
		t.Fatalf("nodeExt is %d bytes, want 168", size)
	}
}

func TestSetZeroValueAllocatesNothing(t *testing.T) {
	n := &Node{Type: NodeName}
	n.SetSteps(nil).SetFocus("").SetGroup(nil).SetNumVal(0)
	if n.ext != nil {
		t.Fatal("setting zero values allocated nodeExt")
	}
	n.SetFocus("x")
	if n.ext == nil || n.ext.rare == nil || n.Focus() != "x" {
		t.Fatal("SetFocus did not store the value")
	}
}

func TestCloneOwnsHolders(t *testing.T) {
	n := (&Node{Type: NodePath}).SetSteps([]*Node{{Type: NodeName}}).SetGroup(&GroupExpr{})
	c := n.Clone()
	c.SetGroup(nil).SetSteps(nil)
	if n.Group() == nil || len(n.Steps()) != 1 {
		t.Fatal("changing the clone changed the original")
	}
}
