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

// An Environment is allocated per lambda call, block and path step that
// binds; growing it past 112 bytes on 64-bit targets moves it to a larger
// allocation size class.
func TestEnvironmentSize(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("sizes differ on 32-bit targets")
	}
	if size := unsafe.Sizeof(Environment{}); size > 112 {
		t.Fatalf("Environment is %d bytes, want at most 112", size)
	}
}
