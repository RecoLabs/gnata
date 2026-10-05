package evaluator

import (
	"testing"
	"unsafe"
)

// A callCounter is allocated per evaluation; growing it past 64 bytes moves
// it to a larger allocation size class.
func TestCallCounterSize(t *testing.T) {
	if size := unsafe.Sizeof(callCounter{}); size != 64 {
		t.Fatalf("callCounter is %d bytes, want 64", size)
	}
}
