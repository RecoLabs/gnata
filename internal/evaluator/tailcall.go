package evaluator

// TailCall is a sentinel returned by tail-position function calls.
// The trampoline loop in invokeFunction catches it and re-invokes.
type TailCall struct {
	Fn    any
	Args  []any
	Focus any // the call's context: the entering call's for a jsonata-js tail call
}
