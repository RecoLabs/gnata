//go:build tinygo

package gnata_test

import "testing"

// skipDeepDecode skips TestDeepNestingDecode, whose input the fast decoder
// recurses into up to its depth limit before handing it on.
const skipDeepDecode = true

// limitStack and limitStackTo do nothing under TinyGo, whose goroutine
// stacks have a fixed size already.
func limitStack(*testing.T) {}

func limitStackTo(*testing.T, int) {}
