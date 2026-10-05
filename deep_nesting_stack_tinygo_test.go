//go:build tinygo

package gnata_test

import "testing"

// limitStack does nothing under TinyGo, whose goroutine stacks have a fixed
// size already.
func limitStack(*testing.T) {}
