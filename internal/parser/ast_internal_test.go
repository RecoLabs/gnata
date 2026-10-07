package parser

import (
	"testing"
	"unsafe"
)

// Every AST node is allocated and kept with the compiled expression; growing
// a Node past 416 bytes moves it to a larger allocation size class.
func TestNodeSize(t *testing.T) {
	if size := unsafe.Sizeof(Node{}); size != 408 {
		t.Fatalf("Node is %d bytes, want 408", size)
	}
}
