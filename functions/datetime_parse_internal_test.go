package functions

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A cancelled evaluation stops a $toMillis match while it backtracks, before
// the call returns to the evaluator's own deadline check.
func TestParseWithPictureStops(t *testing.T) {
	errStop := errors.New("stop")
	calls := 0
	stop := func() error {
		calls++
		if calls > 10 {
			return errStop
		}
		return nil
	}
	_, matched, err := parseWithPicture(strings.Repeat("a", 1000), "[FNn][Z]", time.Now(), stop)
	if !errors.Is(err, errStop) || matched {
		t.Fatalf("got matched %v, error %v; want the stop error", matched, err)
	}
	if calls != 11 {
		t.Fatalf("stop was called %d times, want 11: its first error must end the match", calls)
	}
}
