package evaluator

// TailCall is a sentinel returned by tail-position function calls.
// The trampoline loop in callFunction catches it and re-invokes.
type TailCall struct {
	Fn   any
	Args []any
}
