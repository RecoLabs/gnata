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
	limitStackTo(t, 16<<20)
}

// limitStackTo lowers the goroutine stack limit to size bytes for the test.
func limitStackTo(t *testing.T, size int) {
	t.Helper()
	prev := debug.SetMaxStack(size)
	t.Cleanup(func() { debug.SetMaxStack(prev) })
}
