//go:build js && wasm

// Package main is the WebAssembly entry point for Gnata.
// Build with: GOOS=js GOARCH=wasm go build -ldflags="-s -w" -trimpath -o gnata.wasm ./wasm/
//
// The -s -w flags strip debug info; -trimpath removes local paths.
// When served with brotli or gzip the browser transfer size is ~1.2–1.4 MB.
//
// Raw WASM exports (registered on the JS global object with underscore prefix):
//
//	_gnataEval(expr, jsonData)                → string | Error
//	_gnataCompile(expr)                       → number | Error
//	_gnataEvalHandle(handle, jsonData)        → string | Error
//	_gnataReleaseHandle(handle)               → undefined | Error
//	_gnataEvalMap(handle, jsonObject)          → string | Error
//	_gnataEvalWithVars(handle, jsonData, vars) → string | Error
//
// playground.html wraps these with a wrapWasm factory that converts returned
// Error values into thrown exceptions, exposing the public names without the
// underscore prefix. Consumers embedding this WASM outside the playground must
// implement their own wrapper or use the underscore names directly.
//
// Cache lifetime: exprCache (keyed by expression string) and compiledCache (keyed
// by handle) are intentionally unbounded for the playground use-case — sessions
// are short-lived and the expression universe is small. Call gnataReleaseHandle
// when a compiled handle is no longer needed to free the associated memory.
//
// Error handling: Go WASM's js.FuncOf does not reliably recover panics,
// so we return JS Error objects and let a thin JS wrapper throw them.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall/js"

	"github.com/recolabs/gnata"
)

var (
	compiledCache sync.Map
	nextHandle    atomic.Uint32
	exprCache     sync.Map // string → *gnata.Expression
)

func main() {
	js.Global().Set("_gnataEval", js.FuncOf(jsEval))
	js.Global().Set("_gnataCompile", js.FuncOf(jsCompile))
	js.Global().Set("_gnataEvalHandle", js.FuncOf(jsEvalHandle))
	js.Global().Set("_gnataReleaseHandle", js.FuncOf(jsReleaseHandle))
	js.Global().Set("_gnataEvalMap", js.FuncOf(jsEvalMap))
	js.Global().Set("_gnataEvalWithVars", js.FuncOf(jsEvalWithVars))

	select {}
}

// jsEval: _gnataEval(expr, jsonData) → string | Error
func jsEval(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return jsError("gnataEval requires 2 arguments: expr, jsonData")
	}
	result, err := doEval(args[0].String(), args[1].String())
	if err != nil {
		return jsError(err.Error())
	}
	return js.ValueOf(result)
}

// jsCompile: _gnataCompile(expr) → number | Error
func jsCompile(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return jsError("gnataCompile requires 1 argument: expr")
	}
	handle, err := doCompile(args[0].String())
	if err != nil {
		return jsError(err.Error())
	}
	return js.ValueOf(handle)
}

// jsReleaseHandle: _gnataReleaseHandle(handle) → undefined | Error
func jsReleaseHandle(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return jsError("gnataReleaseHandle requires 1 argument: handle")
	}
	if args[0].Type() != js.TypeNumber {
		return jsError("gnataReleaseHandle: handle must be a number")
	}
	compiledCache.Delete(uint32(args[0].Int()))
	return js.Undefined()
}

// jsEvalHandle: _gnataEvalHandle(handle, jsonData) → string | Error
func jsEvalHandle(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return jsError("gnataEvalHandle requires 2 arguments: handle, jsonData")
	}
	if args[0].Type() != js.TypeNumber {
		return jsError("gnataEvalHandle: handle must be a number")
	}
	result, err := doEvalHandle(uint32(args[0].Int()), args[1].String())
	if err != nil {
		return jsError(err.Error())
	}
	return js.ValueOf(result)
}

func doEval(expr, jsonData string) (result string, err error) {
	defer catchPanic(&err)

	var e *gnata.Expression
	if cached, ok := exprCache.Load(expr); ok {
		e = cached.(*gnata.Expression)
	} else {
		compiled, compileErr := gnata.Compile(expr)
		if compileErr != nil {
			return "", compileErr
		}
		exprCache.Store(expr, compiled)
		e = compiled
	}

	return evalAndMarshal(e, jsonData)
}

func doCompile(expr string) (handle int, err error) {
	defer catchPanic(&err)

	e, compileErr := gnata.Compile(expr)
	if compileErr != nil {
		return 0, compileErr
	}

	h := nextHandle.Add(1)
	if h == 0 {
		// uint32 wrapped — 2^32 compile calls exhausted the handle space.
		// Note: after this wrap nextHandle continues from 0, so subsequent Add(1)
		// calls will yield 1, 2, … again — silently overwriting any live entries
		// already stored under those keys. This is unreachable in practice (requires
		// exactly 2^32 compile calls) and is documented here for completeness.
		return 0, fmt.Errorf("handle counter overflow: too many compiled expressions")
	}
	compiledCache.Store(h, e)
	return int(h), nil
}

func doEvalHandle(handle uint32, jsonData string) (result string, err error) {
	defer catchPanic(&err)

	val, ok := compiledCache.Load(handle)
	if !ok {
		return "", fmt.Errorf("unknown handle %d", handle)
	}
	e := val.(*gnata.Expression)

	return evalAndMarshal(e, jsonData)
}

// jsEvalMap: _gnataEvalMap(handle, jsonObject) → string | Error
// Evaluates a compiled expression against a JSON object using EvalMap for O(1)
// top-level key lookup with gjson fast paths for nested access.
func jsEvalMap(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return jsError("gnataEvalMap requires 2 arguments: handle, jsonObject")
	}
	if args[0].Type() != js.TypeNumber {
		return jsError("gnataEvalMap: handle must be a number")
	}
	result, err := doEvalMap(uint32(args[0].Int()), args[1].String())
	if err != nil {
		return jsError(err.Error())
	}
	return js.ValueOf(result)
}

// jsEvalWithVars: _gnataEvalWithVars(handle, jsonData, varsJson) → string | Error
// Evaluates a compiled expression with external variable bindings ($-prefixed
// names accessible in the expression).
func jsEvalWithVars(_ js.Value, args []js.Value) any {
	if len(args) < 3 {
		return jsError("gnataEvalWithVars requires 3 arguments: handle, jsonData, varsJson")
	}
	if args[0].Type() != js.TypeNumber {
		return jsError("gnataEvalWithVars: handle must be a number")
	}
	result, err := doEvalWithVars(uint32(args[0].Int()), args[1].String(), args[2].String())
	if err != nil {
		return jsError(err.Error())
	}
	return js.ValueOf(result)
}

func doEvalMap(handle uint32, jsonObject string) (result string, err error) {
	defer catchPanic(&err)

	val, ok := compiledCache.Load(handle)
	if !ok {
		return "", fmt.Errorf("unknown handle %d", handle)
	}
	e := val.(*gnata.Expression)

	var data map[string]json.RawMessage
	if jsonObject != "" {
		if err := json.Unmarshal([]byte(jsonObject), &data); err != nil {
			return "", fmt.Errorf("invalid JSON object: %w", err)
		}
	}

	res, evalErr := e.EvalMap(context.Background(), data)
	if evalErr != nil {
		return "", evalErr
	}
	return marshalResult(res)
}

func doEvalWithVars(handle uint32, jsonData, varsJSON string) (result string, err error) {
	defer catchPanic(&err)

	val, ok := compiledCache.Load(handle)
	if !ok {
		return "", fmt.Errorf("unknown handle %d", handle)
	}
	e := val.(*gnata.Expression)

	var vars map[string]any
	if varsJSON != "" && varsJSON != "{}" {
		if unmarshalErr := json.Unmarshal([]byte(varsJSON), &vars); unmarshalErr != nil {
			return "", fmt.Errorf("invalid vars JSON: %w", unmarshalErr)
		}
	}

	var res any
	var evalErr error
	if jsonData == "" {
		res, evalErr = e.EvalWithVars(context.Background(), nil, vars)
	} else {
		res, evalErr = e.EvalBytesWithVars(context.Background(), json.RawMessage(jsonData), vars)
	}
	if evalErr != nil {
		return "", evalErr
	}
	return marshalResult(res)
}

// evalAndMarshal evaluates expr against jsonData and marshals the result to JSON.
// Uses EvalBytes to enable gjson fast paths (pure path, comparison, function).
//
// Return values:
//   - ("", nil)    → expression evaluated to undefined (no match).
//   - ("null", nil) → expression evaluated to JSON null (actual null value).
//   - (json, nil)  → expression evaluated to a concrete value.
//   - ("", err)    → evaluation or marshal error.
func evalAndMarshal(e *gnata.Expression, jsonData string) (string, error) {
	var res any
	var err error
	if jsonData == "" {
		res, err = e.Eval(context.Background(), nil)
	} else {
		res, err = e.EvalBytes(context.Background(), json.RawMessage(jsonData))
	}
	if err != nil {
		return "", err
	}
	return marshalResult(res)
}

// marshalResult marshals an evaluation result to JSON.
// Returns ("", nil) for undefined (Go nil), letting the JS wrapper map it to
// JavaScript undefined. Actual JSON null is the evaluator.Null sentinel
// which marshals to "null" via MarshalJSON.
func marshalResult(res any) (string, error) {
	if res == nil {
		return "", nil
	}
	out, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("cannot marshal result: %w", err)
	}
	return string(out), nil
}

// catchPanic recovers from any panic and stores it as an error.
// Unlike gnata's unexported recoverEvalPanic (which maps *evaluator.JSONataError
// to a structured error for internal use), this function surfaces all panics
// uniformly as "internal error: …" strings. The full error detail is intentional
// for the developer playground context; strip the message before the JS boundary
// if this WASM module is ever embedded in an end-user-facing product.
func catchPanic(errp *error) {
	if r := recover(); r != nil {
		switch v := r.(type) {
		case error:
			*errp = fmt.Errorf("internal error: %w", v)
		default:
			*errp = fmt.Errorf("internal error: %v", r)
		}
	}
}

// jsError creates a JavaScript Error object without panicking.
func jsError(msg string) js.Value {
	return js.Global().Get("Error").New(msg)
}
