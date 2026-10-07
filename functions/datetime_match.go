package functions

import (
	"math"
	"unicode/utf8"
)

// matchStepsPerRune bounds the runes a match may parse in total, per rune of
// input and part of picture, so that backtracking stays linear in both; a
// match that runs out reads as no match (README known difference #34).
const matchStepsPerRune = 256

// pictureMatch matches input against a picture's parts as jsonata-js's
// anchored regex does: each marker reads as much as it can, then gives back
// one rune at a time until the parts after it match. failed memoizes the
// (part, position) pairs that cannot match.
type pictureMatch struct {
	parts  []datePicturePart
	input  []rune
	values []int
	failed map[[2]int]bool
	steps  int // runes the match may still parse
	stop   func() error
	err    error // why the match stopped early, from stop
}

func newPictureMatch(parts []datePicturePart, input string, stop func() error) pictureMatch {
	runes := []rune(input)
	return pictureMatch{
		parts: parts, input: runes, values: make([]int, len(parts)),
		steps: matchStepsPerRune * min(len(runes)+len(parts)+1, math.MaxInt/matchStepsPerRune), stop: stop,
	}
}

// spend charges n parsed runes, plus one for the attempt, to the match,
// reporting false once its steps run out or stop reports an error.
func (pm *pictureMatch) spend(n int) bool {
	if pm.steps -= n + 1; pm.steps < 0 {
		return false
	}
	if pm.err == nil {
		pm.err = pm.stop()
	}
	return pm.err == nil
}

// matchFrame is part i being matched at input position pos; next is the
// next length to try for it, 0 when none is left.
type matchFrame struct{ i, pos, next int }

// run reports whether the parts match the whole input, recording each
// marker's value in values. It searches depth first on an explicit stack, so
// a long picture cannot exhaust the goroutine's.
func (pm *pictureMatch) run() bool {
	var buf [16]matchFrame
	stack := append(buf[:0], pm.enter(0, 0))
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.i == len(pm.parts) {
			if top.pos == len(pm.input) {
				return true
			}
			stack = stack[:len(stack)-1]
			continue
		}
		k, value, ok := pm.nextLength(top)
		if !ok {
			if pm.steps < 0 || pm.err != nil {
				return false
			}
			if pm.failed == nil {
				pm.failed = map[[2]int]bool{}
			}
			pm.failed[[2]int{top.i, top.pos}] = true
			stack = stack[:len(stack)-1]
			continue
		}
		pm.values[top.i] = value
		stack = append(stack, pm.enter(top.i+1, top.pos+k))
	}
	return false
}

// enter starts matching part i at pos: a literal has one length to try, a
// marker every length from the longest it can read down to one.
func (pm *pictureMatch) enter(i, pos int) matchFrame {
	f := matchFrame{i: i, pos: pos}
	switch {
	case i == len(pm.parts), pm.failed[[2]int{i, pos}]:
	case !pm.parts[i].isMarker:
		f.next = utf8.RuneCountInString(pm.parts[i].literal)
	default:
		_, n := parseMarkerValue(pm.input[pos:], pm.parts[i].marker)
		f.next = max(n, 0)
	}
	return f
}

// nextLength returns the next length at which the frame's part matches, with
// the marker's value, reporting false when none is left or the match stops.
func (pm *pictureMatch) nextLength(f *matchFrame) (length, value int, ok bool) {
	part, rest := &pm.parts[f.i], pm.input[f.pos:]
	if !part.isMarker {
		k := f.next
		f.next = 0
		return k, 0, k > 0 && pm.spend(k) && hasFoldPrefix(rest, part.literal)
	}
	for ; f.next > 0; f.next-- {
		k := f.next
		if !pm.spend(k) {
			return 0, 0, false
		}
		if value, n := parseMarkerValue(rest[:k], part.marker); n == k {
			f.next--
			return k, value, true
		}
	}
	return 0, 0, false
}
