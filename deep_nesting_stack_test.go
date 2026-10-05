//go:build !tinygo

package gnata_test

import (
	"runtime/debug"
	"testing"
)

const skipDeepDecode = false

// limitStack lowers the goroutine stack limit to 16 MB for the test, so a
// walk that recurses once per level fails at deepNesting levels.
func limitStack(t *testing.T) {
	t.Helper()
	prev := debug.SetMaxStack(16 << 20)
	t.Cleanup(func() { debug.SetMaxStack(prev) })
}
