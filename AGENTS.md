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

# Run tests as js/wasm under Node (browser parity)
env -i PATH="$(go env GOROOT)/lib/wasm:$PATH" HOME="$HOME" GOOS=js GOARCH=wasm go test ./...

# Check the TinyGo/WASI build (must keep compiling; see "TinyGo / WASI" below)
GOEXPERIMENT=nojsonv2 tinygo build -target=wasip1 -scheduler=none -o /dev/null ./examples/wasi

# Run benchmarks
go test -bench=. -benchmem
```

## Architecture

### Compilation Pipeline

Lexer → Parser → AST Processing → Fast-Path Analysis → Expression

1. **Lexer** (`internal/lexer/`) — Tokenizes JSONata expression strings
2. **Parser** (`internal/parser/`) — Pratt (top-down operator precedence) parser producing AST nodes
3. **AST Processing** (`parser.ProcessAST`) — Normalizes and optimizes the AST, and resolves each `%` to the path step whose input it reads. `parser.ParseAndProcess` runs steps 2–3, rejects a `%` with no such step (S0217), and marks the wildcard and ancestor steps reading the root input; every entry point (`Compile`, `$eval`) must use it
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

55+ built-in JSONata functions across categorized files: `string_funcs.go`, `string_match_replace.go`, `string_format_number.go`, `string_format_integer.go`, `string_encoding.go`, `numeric_funcs.go`, `numeric_decimal.go`, `array_funcs.go`, `object_funcs.go`, `hof_funcs.go`, `boolean_funcs.go`, `datetime_funcs.go`, `datetime_format.go`, `datetime_parse.go`. All registered via `functions.RegisterAll`.

### Fast-Path Byte Evaluation (func_fast.go)

Dispatch-map of `funcFastHandlers` maps each `FuncFastKind` to a standalone handler function (e.g., `evalFuncContains`, `evalFuncString`). Each handler operates directly on `gjson.Result` for zero-copy evaluation. `FuncFastRound` is intentionally absent — it requires banker's rounding handled by the full evaluator.

### Decimal Precision (internal/decimal/)

Opt-in via `WithDecimalPrecision(digits)`, read with `env.DecimalPrecision()` (0 = off). Operators, comparisons, structural equality (`DeepEqualPrec`), numeric builtins, `$distinct`, `$string`, `$formatNumber` and `$formatBase` compute in `decimal.Decimal`, a thin layer over the copy of cockroachdb/apd in `internal/third_party/apd` (keep that copy unmodified so upstream fixes can be re-applied; its tests include the General Decimal Arithmetic test cases). Values stay `float64 | json.Number`; anything out of range returns `ok=false` so the unchanged float64 path runs. Fast paths that compare or compute numbers in float64 are dropped at compile time by `fastPathResult.DecimalSafe`; every new `FuncFastKind` must be classified in `decimalSafeFuncKinds` (a test enforces it).

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

Object arguments reach custom functions as `map[string]any`. By default each call gets freshly copied maps; arrays of scalars are passed through uncopied and must not be modified in place. `WithReadOnlyCustomFuncArgs()` instead hands out a normalized view cached on each object the evaluator decoded from its input and shared by every call — cheaper when many expressions pass the same payload objects, but functions must then treat their arguments as read-only. Only evaluator-decoded input is frozen and cached (`evaluator.DecodeInput`, `DecodeRawMap`); the public `DecodeJSON` returns caller-owned, unfrozen maps.

## WASM

`wasm/main.go` exports six JS functions: `gnataEval`, `gnataCompile`, `gnataEvalHandle`, `gnataReleaseHandle`, `gnataEvalMap` (O(1) top-level key lookup via `EvalMap`), and `gnataEvalWithVars` (external `$`-variable bindings). `gnataEval` and `gnataEvalHandle` use `EvalBytes`; `gnataEvalMap` uses `EvalMap`; `gnataEvalWithVars` uses `EvalBytesWithVars`. All paths leverage gjson fast-path access where applicable. Build with `GOOS=js GOARCH=wasm go build -ldflags="-s -w" -trimpath -o gnata.wasm ./wasm/`.

`WithTimeout` is enforced without a timer goroutine: `Environment.Err` samples the clock every 128 calls on the per-node path, and `callFunction` checks it on every call (`errNow`), so the overrun is bounded by one builtin/custom-function call and the guardrail also fires inside a synchronous call on single-threaded WebAssembly hosts.

## TinyGo / WASI

gnata also builds with [TinyGo](https://tinygo.org) for `wasip1`, including `-scheduler=none`, and passes the jsonata-js conformance suite there with results identical to the standard Go build. Keep it that way:

- **Build flags:** `GOEXPERIMENT=nojsonv2` (Go's json/v2-backed `encoding/json` needs reflection TinyGo lacks). Use `-scheduler=none` for library/reactor modules.
- **No goroutines or timers in library code** — e.g. no `context.WithTimeout`/`time.AfterFunc`. TinyGo refuses to build them with `-scheduler=none`, and on single-threaded WASM hosts they cannot fire during a synchronous call anyway. The same applies to writing through `os.Stdout` (`fmt.Print*`), which is why `examples/wasi` uses `println`.
- **Don't encode gnata values with `encoding/json`:** use `evaluator.AppendJSON`. `encoding/json`'s encoder reports errors by panicking and recovering internally, and `recover` is unavailable on TinyGo/WASM, so any encode error would abort the module. Decoding (`DecodeJSON`) does not use `encoding/json` on the hot path.
- **Convert JSONata numbers to `int` with `evaluator.ToIntClamped`**, never a bare `int(f)`: `int` is 32 bits on TinyGo/WASM and an out-of-range float→int conversion is implementation-defined.
- **Runtime panics trap instead of being recovered** on TinyGo/WASM. Hosts should treat a trap as a failed evaluation and re-instantiate the module.
- **Stack depth:** the 1,500 nested-call limit for function values (`maxNestedCalls`) fits gc's native stacks and js/wasm in Node, which overflows at about 4,900 partial-application `$sort` comparators once an earlier chain in the same evaluation has grown the stack (12,500 on a fresh one); measure crash points after such a warm-up. TinyGo's stacks are fixed, so deep chains can overflow below it; tests that build chains near the limit are `//go:build !tinygo`.
- **Stack depth:** deep JSONata recursion needs a sufficient `-stack-size`; an overflow traps rather than corrupting memory.
- **JVM-hosted runtimes** (WASM compiled to JVM bytecode) cannot compile very large functions; at `-opt=2` LLVM inlines `evaluator.Eval` past that limit, so prefer `-opt=1` for those hosts.
- **Running the test suite under TinyGo:** `GOEXPERIMENT=nojsonv2 tinygo test -c -target=wasip1 -stack-size=1MB -o gnata.test.wasm .` and run it with a WASI runtime with the package directory mounted as `/` (e.g. `wazero run -mount=.:/ -env=PWD=/ gnata.test.wasm -test.v`). TinyGo runs every subtest in its own fixed-size goroutine stack, so a full run needs plenty of memory; the two 10,000,000-element range cases are the heaviest.

