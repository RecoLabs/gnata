package evaluator

// TailCall is a sentinel returned by tail-position function calls.
// callFunction applies a built-in's at once, within the calling lambda's
// depth, and trampolines a lambda's.
//
// jsonata-js applies a tail call to any function from its trampoline, with
// the input and environment of the lambda's own call, so a built-in's result
// sequence is returned uncollapsed and a [] suffix on the call is ignored.
type TailCall struct {
	Fn   any
	Args []any
}
