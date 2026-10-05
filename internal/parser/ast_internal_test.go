package parser

import (
	"testing"
	"unsafe"
)

// Every AST node is allocated and kept with the compiled expression; growing
// a Node past 384 bytes moves it to a larger allocation size class.
func TestNodeSize(t *testing.T) {
	if size := unsafe.Sizeof(Node{}); size != 384 {
		t.Fatalf("Node is %d bytes, want 384", size)
	}
}
