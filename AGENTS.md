# AGENTS.md

This file provides guidance to AI agents (Claude Code, GitHub Copilot, Cursor, etc.) when working with code in this repository.

## What Is Gnata

Gnata is a full JSONata 2.x implementation in Go, built for production streaming workloads. JSONata is a query and transformation language for JSON data. Gnata provides two-tier evaluation: fast-path (GJSON zero-copy) for simple expressions and full AST evaluation for complex ones, plus a lock-free `StreamEvaluator` for high-throughput batched evaluation.

## Development Commands

```sh
# Lint (from package root)
golangci-lint run

# Run all tests (1,273 JSON test cases from official jsonata-js suite + unit tests)
go test ./...

# Run a single test
go test -run TestName

# Run benchmarks
go test -bench=. -benchmem
```

## Architecture

### Compilation Pipeline

Lexer → Parser → AST Processing → Fast-Path Analysis → Expression

1. **Lexer** (`internal/lexer/`) — Tokenizes JSONata expression strings
2. **Parser** (`internal/parser/`) — Pratt (top-down operator precedence) parser producing AST nodes
3. **AST Processing** (`parser.ProcessAST`) — Normalizes and optimizes the AST
4. **Fast-Path Analysis** (`parser.AnalyzeFastPath`) — Classifies expressions into:
   - Pure-path fast path (e.g., `Account.Name`) — uses GJSON zero-copy
   - Comparison fast path (e.g., `a.b = "x"`) — zero allocations
   - Full AST evaluation required

### Two-Tier Evaluation

- `Eval(ctx, any)` — Evaluate against pre-parsed Go values via full AST walk
- `EvalBytes(ctx, json.RawMessage)` — Fast-path expressions use GJSON directly on raw JSON bytes; full-path falls back to unmarshal + Eval

### StreamEvaluator (stream.go)

Batch-evaluates multiple expressions against events. Schema-keyed `GroupPlan` caching classifies expressions into fast-path vs full-eval at plan-build time. Lock-free reads via `atomic.Pointer` snapshot; writes serialized by `sync.Mutex`. Fast-path expressions use `gjson.GetBytes` for zero-copy field extraction.

### Evaluator Dispatch (internal/evaluator/)

`evaluator.Eval(node, input, env)` dispatches by `node.Type`. Each eval category is in its own file:
- `eval_binary.go` — Binary operators, subscripts, filtering
- `eval_function.go` — Function calls, lambdas, partial application
- `eval_chain.go` — Path chaining, pipes, blocks, conditions
- `eval_sort.go` — Sorting with generic `SortItemsErr[T any]` helper
- `eval_transform.go` — JSONata transform operator (`|obj|updates|deletes|` syntax)
- `eval_group.go` — Group-by reduction (`{key: val}` syntax)
- `eval_range.go` — Range operator (`[start..end]`)
- `eval_regex.go` — Regex compilation and matching
- `eval_unary.go` — Unary operators (negation, array constructor)

### Standard Library (functions/)

55+ built-in JSONata functions across categorized files: `string_funcs.go`, `string_match_replace.go`, `string_format_number.go`, `string_format_integer.go`, `string_encoding.go`, `numeric_funcs.go`, `array_funcs.go`, `object_funcs.go`, `hof_funcs.go`, `boolean_funcs.go`, `datetime_funcs.go`, `datetime_format.go`, `datetime_parse.go`. All registered via `functions.RegisterAll`.

### Fast-Path Byte Evaluation (func_fast.go)

Dispatch-map of `funcFastHandlers` maps each `FuncFastKind` to a standalone handler function (e.g., `evalFuncContains`, `evalFuncString`). Each handler operates directly on `gjson.Result` for zero-copy evaluation. `FuncFastRound` is intentionally absent — it requires banker's rounding handled by the full evaluator.

### Key Types

- **Expression** (`gnata.go`) — Compiled, goroutine-safe JSONata expression with fast-path metadata
- **StreamEvaluator** (`stream.go`) — Copy-on-write expression slice + `BoundedCache` for schema plans
- **BoundedCache** (`bounded_cache.go`) — Lock-free FIFO ring-buffer cache (atomic pointer reads)
- **OrderedMap** (`internal/evaluator/ordered_map.go`) — Insertion-ordered map preserving JSON field order
- **Environment** (`internal/evaluator/env.go`) — Lexical scope chain for variable bindings

## Dependencies

Only one direct dependency (pure Go, no CGo):
- `tidwall/gjson` — Zero-copy JSON field extraction for fast-path byte-level evaluation

Regex uses Go's standard `regexp` package (no external regex library).

## Testing

Tests use separate `_test` packages (`gnata_test`, `lexer_test`, `parser_test`). The primary test suite (`suite_test.go`) loads 1,200+ JSON test cases from `testdata/groups/` (100+ subdirectories) — each `.json` file is a case with `expr`, `dataset`, `bindings`, and `result` fields. Datasets live in `testdata/datasets/`. Additional unit tests in `evaluator_test.go` cover regression tests using table-driven test (TDT) style.

## Custom Functions

Register custom functions via `StreamEvaluator` options or standalone `CustomEnv`:
```go
customFuncs := map[string]gnata.CustomFunc{
    "md5": func(args []any, focus any) (any, error) { ... },
}
se := gnata.NewStreamEvaluator(nil, gnata.WithCustomFunctions(customFuncs))
```

## WASM

`wasm/main.go` exports six JS functions: `gnataEval`, `gnataCompile`, `gnataEvalHandle`, `gnataReleaseHandle`, `gnataEvalMap` (O(1) top-level key lookup via `EvalMap`), and `gnataEvalWithVars` (external `$`-variable bindings). `gnataEval` and `gnataEvalHandle` use `EvalBytes`; `gnataEvalMap` uses `EvalMap`; `gnataEvalWithVars` uses `EvalBytesWithVars`. All paths leverage gjson fast-path access where applicable. Build with `GOOS=js GOARCH=wasm go build -ldflags="-s -w" -trimpath -o gnata.wasm ./wasm/`.
