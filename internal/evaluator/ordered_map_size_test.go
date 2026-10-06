package evaluator

import (
	"testing"
	"unsafe"
)

// TestOrderedMapSize pins OrderedMap at 48 bytes on 64-bit platforms: every
// object an evaluation builds or decodes is one, and a field that does not
// fit in its padding moves it to the next allocation size class.
func TestOrderedMapSize(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("sizes differ on 32-bit platforms")
	}
	if size := unsafe.Sizeof(OrderedMap{}); size != 48 {
		t.Fatalf("OrderedMap is %d bytes, want 48", size)
	}
}
