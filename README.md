<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/gnata-dark.png" />
    <source media="(prefers-color-scheme: light)" srcset="assets/gnata-light.png" />
    <img src="assets/gnata-light.png" alt="gnata" width="720" />
  </picture>
</p>

<p align="center">
  A full JSONata 2.x implementation in Go, built for production streaming workloads.
</p>

<p align="center">
  <a href="https://github.com/RecoLabs/gnata/actions/workflows/ci.yml"><img src="https://github.com/RecoLabs/gnata/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://pkg.go.dev/github.com/recolabs/gnata"><img src="https://pkg.go.dev/badge/github.com/recolabs/gnata.svg" alt="Go Reference" /></a>
  <a href="https://github.com/RecoLabs/gnata/blob/main/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/RecoLabs/gnata" alt="Go version" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License" /></a>
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> &middot;
  <a href="#streaming-api">Streaming API</a> &middot;
  <a href="#metrics--observability">Metrics</a> &middot;
  <a href="#performance">Performance</a> &middot;
  <a href="#jsonata-compatibility">Compatibility</a> &middot;
  <a href="#wasm">WASM Playground</a> &middot;
  <a href="CONTRIBUTING.md">Contributing</a> &middot;
  <a href="SECURITY.md">Security</a>
</p>

---

## What is gnata?

[JSONata](https://jsonata.org) is a lightweight query and transformation language for JSON data — think "jq meets XPath with lambda functions." gnata brings the full JSONata 2.x specification to Go, with a production-grade streaming tier designed for evaluating thousands of expressions against millions of events per day with zero contention.

## Features

- **Full JSONata 2.x** — path navigation, wildcards, descendants, predicates, sorting, grouping, lambdas, closures, higher-order functions, transforms, regex, and the complete 50+ function standard library.
- **Two-tier evaluation** — simple expressions use a zero-copy fast path (GJSON); complex expressions fall back to a full AST evaluator.
- **Lock-free streaming** — `StreamEvaluator` batches multiple expressions per event with schema-keyed plan caching. After warm-up, the hot path uses only atomic loads — no mutexes, no RWLocks, no channels.
- **Zero allocations** — simple field comparisons like `user.email = "admin@co.com"` evaluate with **0 heap allocations** via GJSON zero-copy string views.
- **Bounded memory** — schema plan cache uses a FIFO ring-buffer with configurable capacity (`WithMaxCachedSchemas`), evicting the oldest entry on overflow.
- **Context-aware** — all evaluation methods accept `context.Context` for cancellation and timeouts. Long-running expressions check context at loop boundaries.
- **Linear-time regex** — uses Go's standard `regexp` (RE2 engine) for guaranteed linear-time matching with no timeouts or backtracking.
- **1,778 test cases** — ported from the official jsonata-js test suite (0 failures, 0 skips).
- **One dependency** — [`tidwall/gjson`](https://github.com/tidwall/gjson) for fast-path byte-level field extraction.
- **~13K lines of Go** — complete implementation with no code generation.
- **WASM support** — compile to WebAssembly for an in-browser playground.

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "github.com/recolabs/gnata"
)

func main() {
    expr, _ := gnata.Compile(`Account.Order.Product.Price`)

    data := map[string]any{
        "Account": map[string]any{
            "Order": []any{
                map[string]any{"Product": map[string]any{"Price": 34.45}},
                map[string]any{"Product": map[string]any{"Price": 21.67}},
            },
        },
    }

    result, _ := expr.Eval(context.Background(), data)
    fmt.Println(result) // [34.45 21.67]
}
```

## API Overview

gnata provides three evaluation tiers, each building on the previous:

### Tier 1 — `Eval(context.Context, any)`

Evaluate against pre-parsed Go values. Pass a context for cancellation and timeouts.

```go
expr, err := gnata.Compile(`$sum(orders.amount)`)
result, err := expr.Eval(ctx, data) // data is map[string]any
```

### Tier 2 — `EvalBytes(context.Context, json.RawMessage)`

Evaluate directly against raw JSON bytes. For fast-path-eligible expressions, fields are extracted via GJSON with zero-copy — the entire document is never materialized.

```go
expr, _ := gnata.Compile(`user.email = "admin@example.com"`)
result, _ := expr.EvalBytes(ctx, rawJSON) // rawJSON is json.RawMessage
fmt.Println(expr.IsFastPath())            // true — zero-copy evaluation
```

### Tier 3 — `StreamEvaluator`

Batch-evaluate multiple compiled expressions against each event in a streaming pipeline. Schema-keyed plan caching deduplicates field extraction across expressions. Lock-free after warm-up.

```go
se := gnata.NewStreamEvaluator(nil,
    gnata.WithPoolSize(500),
    gnata.WithMaxCachedSchemas(50000),
)

// Compile expressions (goroutine-safe)
indices := make([]int, len(rules))
for i, rule := range rules {
    indices[i], _ = se.Compile(rule.Expr)
}

// Hot path — millions of times, hundreds of goroutines
results, _ := se.EvalMany(ctx, eventBytes, schemaKey, indices)
for i, result := range results {
    if result != nil {
        handleMatch(indices[i], result)
    }
}

// Alternative: pre-decoded map input (avoids re-serialization)
results, _ = se.EvalMap(ctx, fieldMap, schemaKey, indices)
```

### Custom Function Registration

Register domain-specific functions via `WithCustomFunctions` on `StreamEvaluator` or `NewCustomEnv` for standalone expressions. See [Custom Functions](#custom-functions) below.

## Streaming API

The `StreamEvaluator` is designed for high-throughput event processing where the same expressions are evaluated against millions of structurally similar events.

### How It Works

```
Startup (once)
  Compile N expressions ──> Analyze AST: classify fast/full
  Configure stable routing: schemaKey -> exprIndices

Hot Path (millions/day, lock-free)
  Raw json.RawMessage event + schemaKey
    ├── BoundedCache lookup (atomic pointer read)
    │   ├── HIT  ──> Immutable GroupPlan
    │   └── MISS ──> Build plan (merge GJSON paths, atomic CAS store)
    ├── gjson.GetManyBytes: SINGLE scan for ALL expressions
    ├── Fast-path expressions: distribute extracted results (0 allocs)
    ├── Full-path expressions: selective unmarshal + AST eval
    └── results[]
```

### Key Properties

- **One JSON scan per event** — all field paths needed by all expressions are merged into a single `gjson.GetManyBytes` call.
- **Schema-keyed caching** — the `GroupPlan` (merged paths, expression groupings, selective unmarshal targets) is computed once per schema key and reused immutably.
- **Lock-free reads** — `BoundedCache` publishes an `atomic.Pointer` snapshot on every write; reads scan the snapshot without acquiring a lock. Writes are serialised by a mutex.
- **Selective unmarshal** — full-path expressions unmarshal only the subtrees they need (e.g., just the `items` array from a 10KB event), not the entire document.
- **Pre-decoded map input** — `EvalMap` accepts `map[string]json.RawMessage` directly, skipping full-document serialization when the caller already has individually-encoded fields. Fast paths resolve top-level keys via O(1) map lookup.
- **Dynamic mutation** — `Replace`, `Remove`, and `Reset` methods allow modifying registered expressions at runtime with automatic cache invalidation.
- **Observability** — implement `MetricsHook` to receive per-evaluation callbacks for cache hits/misses, eval latency, fast-path usage, and errors.

## Custom Functions

gnata supports registering user-defined functions that extend the standard JSONata library.

### Defining Custom Functions

Custom functions implement the `CustomFunc` signature:

```go
type CustomFunc func(args []any, focus any) (any, error)
```

Where `args` are the evaluated arguments passed by the JSONata expression and `focus` is the current context value.

### Registration

Register custom functions via `WithCustomFunctions` when creating a `StreamEvaluator`:

```go
customFuncs := map[string]gnata.CustomFunc{
    "md5": func(args []any, focus any) (any, error) {
        if len(args) == 0 || args[0] == nil {
            return nil, nil
        }
        h := md5.Sum([]byte(fmt.Sprint(args[0])))
        return fmt.Sprintf("%x", h), nil
    },
    "parseEpochSeconds": func(args []any, focus any) (any, error) {
        // Convert epoch seconds to ISO 8601
        f, ok := args[0].(float64)
        if !ok {
            return nil, nil
        }
        return time.Unix(int64(f), 0).UTC().Format(time.RFC3339), nil
    },
}

se := gnata.NewStreamEvaluator(nil,
    gnata.WithCustomFunctions(customFuncs),
    gnata.WithMaxCachedSchemas(10000),
)

// Expressions can now use $md5() and $parseEpochSeconds()
idx, _ := se.Compile(`$md5(user.email)`)
result, _ := se.EvalOne(ctx, eventJSON, "schema1", idx)
```

### Standalone Expression Evaluation

For one-off evaluations with custom functions, create a custom environment and pass it to `EvalWithCustomFuncs`:

```go
env := gnata.NewCustomEnv(customFuncs)
expr, _ := gnata.Compile(`$md5(payload.email)`)
result, _ := expr.EvalWithCustomFuncs(ctx, data, env)
```

The environment should be created once and reused across evaluations for best performance.

## Metrics & Observability

The `StreamEvaluator` accepts an optional `MetricsHook` (via `WithMetricsHook`) for production telemetry. Implement the interface and wire it in — a nil hook (the default) adds zero overhead.

| Callback | Arguments | What to monitor |
|---|---|---|
| `OnEval` | `exprIndex`, `fastPath`, `duration`, `err` | Fast-path ratio; per-expression latency |
| `OnCacheHit` | `schemaKey` | Cache hit rate — should approach 100% after warm-up |
| `OnCacheMiss` | `schemaKey` | Cache misses trigger plan rebuilds |
| `OnEviction` | — | Cache at capacity, evicting plans; increase `WithMaxCachedSchemas` |

For point-in-time cache stats without a hook, use `se.Stats()` which returns hit/miss/entry/eviction counts.

## Performance

All benchmarks on Apple M4 Pro. gnata is compared against the reference [jsonata-js](https://github.com/jsonata-js/jsonata) implementation running in Node.js. The **JSONata (eval)** column estimates pure evaluation time by subtracting RPC overhead from the total; entries showing `< 1 us` mean the expression evaluated faster than the measurement floor.

### Fast Path (GJSON zero-copy)

Simple field lookups and comparisons are evaluated directly against raw `json.RawMessage` via GJSON — the JSON document is never fully parsed.

| Expression | gnata | JSONata (eval) | JSONata (RPC) | Speedup |
|---|---|---|---|---|
| `field.lookup` | **55 ns** | 83 us | 232 us | 1,500x |
| `nested.3.deep` | **95 ns** | 56 us | 205 us | 590x |
| `field = "string"` | **42 ns** | 49 us | 197 us | 1,170x |
| `field = 2` (numeric) | **142 ns** | 163 us | 311 us | 1,150x |
| `field = true` (bool) | **111 ns** | 64 us | 212 us | 570x |
| `field != null` | **41 ns** | 23 us | 172 us | 570x |
| `field != "value"` | **41 ns** | < 1 us | 147 us | — |

Fast-path expressions typically achieve **0-2 allocations** and **0-40 bytes** per evaluation.

### Function fast path

Expressions calling a supported built-in function on a pure path (e.g. `$exists(a.b)`, `$lowercase(name)`, `$contains(path, "literal")`) are classified at compile time. At runtime, the field is extracted with a single `gjson.GetBytes` call and the function is applied directly — no `json.Unmarshal`, no AST walk.

Supported functions (21): `$exists`, `$contains`, `$string`, `$boolean`, `$number`, `$keys`, `$distinct`, `$not`, `$lowercase`, `$uppercase`, `$trim`, `$length`, `$type`, `$abs`, `$floor`, `$ceil`, `$sqrt`, `$count`, `$reverse`, `$sum`, `$max`, `$min`, `$average`.

### Boolean Logic

| Expression | gnata | JSONata (eval) | JSONata (RPC) | Speedup |
|---|---|---|---|---|
| `a = "x" and b = "y"` | **573 ns** | 24 us | 173 us | 43x |
| `a = "x" or a = "y"` | **144 ns** | 38 us | 187 us | 270x |
| `$not(field)` | **127 ns** | 7 us | 156 us | 58x |
| `(a or b) and c` | **194 ns** | 17 us | 166 us | 88x |
| `a and b and c` (3-way) | **180 ns** | 11 us | 159 us | 59x |

### Numeric & Arithmetic

| Expression | gnata | JSONata (eval) | JSONata (RPC) | Speedup |
|---|---|---|---|---|
| `field > 1` | **191 ns** | < 1 us | 148 us | — |
| `(field + 1) * 10` | **200 ns** | 12 us | 160 us | 58x |
| `field in [1, 2, 3]` | **530 ns** | 17 us | 165 us | 32x |
| `$sum(array)` | **177 ns** | < 1 us | 144 us | — |
| `$average(array)` | **190 ns** | < 1 us | 143 us | — |
| `$max(a) - $min(a)` | **264 ns** | 52 us | 201 us | 200x |

### String Functions

| Expression | gnata | JSONata (eval) | JSONata (RPC) | Speedup |
|---|---|---|---|---|
| `$uppercase(field)` | **117 ns** | < 1 us | 139 us | — |
| `$contains(field, "sub")` | **73 ns** | < 1 us | 111 us | — |
| `$split(email, "@")` | **247 ns** | 90 us | 238 us | 360x |
| `$join(array, ", ")` | **233 ns** | < 1 us | 140 us | — |
| `a & "-" & b` (concat) | **580 ns** | 4 us | 153 us | 7x |
| `$replace(field, "x", "y")` | **234 ns** | < 1 us | 142 us | — |
| `$uppercase($substringBefore(...))` | **314 ns** | < 1 us | 140 us | — |

### Array & Filtering

| Expression | gnata | JSONata (eval) | JSONata (RPC) | Speedup |
|---|---|---|---|---|
| `$count(array)` | **406 ns** | 13 us | 162 us | 33x |
| `items[active = true].name` | **551 ns** | 13 us | 162 us | 24x |
| `items[value > 20].name` | **573 ns** | 39 us | 188 us | 68x |
| `$count(items[active])` | **378 ns** | 42 us | 190 us | 110x |
| `items.name` (auto-map) | **247 ns** | 34 us | 183 us | 140x |
| `items^(value).name` (sort) | **524 ns** | 68 us | 216 us | 130x |
| `items^(>value).name` (desc) | **509 ns** | 41 us | 189 us | 80x |
| `$reverse(array)` | **195 ns** | 20 us | 168 us | 100x |

### Higher-Order Functions & Lambdas

| Expression | gnata | JSONata (eval) | JSONata (RPC) | Speedup |
|---|---|---|---|---|
| `$map(items, function($v) { ... })` | **1.2 us** | 108 us | 256 us | 88x |
| `$filter(items, function($v) { ... })` | **1.1 us** | 41 us | 190 us | 38x |
| `$reduce(array, function($p,$c) { ... })` | **1.0 us** | 8 us | 156 us | 8x |
| `$sort(items, function($a,$b) { ... })` | **1.5 us** | 79 us | 228 us | 53x |
| `$map(tags, $uppercase)` | **425 ns** | < 1 us | 143 us | — |
| `$single(users, function($v) { ... })` | **806 ns** | 7 us | 155 us | 8x |

### Joins (`@` operator)

| Expression | gnata | JSONata (eval) | JSONata (RPC) | Speedup |
|---|---|---|---|---|
| 2-way join (`loans@$l.books@$b[...]`) | **545 ns** | < 1 us | 144 us | — |
| Join with index (`#$i`) | **1.6 us** | 31 us | 180 us | 19x |

### Conditionals & Blocks

| Expression | gnata | JSONata (eval) | JSONata (RPC) | Speedup |
|---|---|---|---|---|
| `a = 2 ? "yes" : "no"` | **170 ns** | < 1 us | 146 us | — |
| Nested ternary | **188 ns** | < 1 us | 144 us | — |
| `( $x := field; $x * $x )` | **268 ns** | < 1 us | 146 us | — |
| Multi-variable block | **257 ns** | 18 us | 166 us | 68x |
| `**.field` (recursive descent) | **7.1 us** | 20 us | 168 us | 3x |

### Regex

| Expression | gnata | JSONata (eval) | JSONata (RPC) | Speedup |
|---|---|---|---|---|
| `$contains(field, /pattern/)` | **497 ns** | 3 us | 151 us | 6x |
| `$match(field, /groups/)` | **947 ns** | < 1 us | 144 us | — |
| `$replace(field, /capture/, "$1")` | **835 ns** | < 1 us | 141 us | — |

### Complex Boolean Expressions

| Expression | gnata | JSONata (eval) | JSONata (RPC) | Speedup |
|---|---|---|---|---|
| 3-way AND compound | **202 ns** | < 1 us | 141 us | — |
| Membership + numeric guard | **215 ns** | < 1 us | 147 us | — |
| `$exists` + field checks | **219 ns** | < 1 us | 148 us | — |
| String pattern matching | **221 ns** | 7 us | 155 us | 31x |
| Filter + count threshold | **487 ns** | < 1 us | 143 us | — |
| Filter + map + join pipeline | **928 ns** | < 1 us | 146 us | — |

### StreamEvaluator (batch of 4 expressions)

| Metric | Value |
|---|---|
| ns/op | 20,500 |
| throughput | 29 MB/s |
| allocs/op | 517 |
| cache hit rate | 100% (after warm-up) |

## JSONata Compatibility

gnata targets full compatibility with [JSONata 2.x](https://docs.jsonata.org), validated against **1,778 test cases** from the official [jsonata-js test suite](https://github.com/jsonata-js/jsonata/tree/master/test/test-suite) — **0 failures, 0 skips**.

### Supported Features

- Path navigation, wildcards (`*`), descendants (`**`), parent (`%`)
- Array predicates, numeric indexing, filter expressions
- Sorting (`^`), grouping (`{}`), transforms (`|...|...|`)
- Lambda functions, closures, tail-call optimization
- Partial application, function composition (`~>`)
- Focus (`@`) and index (`#`) variable binding
- Conditional expressions (`? :`)
- Range operator (`..`), string concatenation (`&`)
- Join operator (`@`) with lateral-join semantics

### Standard Library (50+ functions)

| Category | Functions |
|---|---|
| **String** | `$string` `$length` `$substring` `$uppercase` `$lowercase` `$trim` `$pad` `$contains` `$split` `$join` `$match` `$replace` `$eval` `$base64encode` `$base64decode` `$encodeUrl` `$decodeUrl` `$encodeUrlComponent` `$decodeUrlComponent` `$formatNumber` `$formatBase` `$formatInteger` `$parseInteger` |
| **Numeric** | `$number` `$abs` `$floor` `$ceil` `$round` `$power` `$sqrt` `$random` `$sum` `$max` `$min` `$average` |
| **Array** | `$count` `$append` `$sort` `$reverse` `$shuffle` `$distinct` `$flatten` `$zip` |
| **Object** | `$keys` `$values` `$lookup` `$spread` `$merge` `$each` `$sift` `$type` `$error` `$assert` |
| **Boolean** | `$boolean` `$not` `$exists` |
| **Higher-Order** | `$map` `$filter` `$reduce` `$single` |
| **Date/Time** | `$now` `$millis` `$fromMillis` `$toMillis` |

## Known Behavioral Differences from jsonata-js

gnata targets exact parity with the JSONata reference implementation ([jsonata-js](https://github.com/jsonata-js/jsonata)). The differences below stem from platform differences between Go and JavaScript, not implementation bugs. In every case gnata's behavior is spec-correct or more correct than jsonata-js.

| # | Area | gnata | jsonata-js | Notes |
|---|------|-------|------------|-------|
| 1 | **Large integer precision** | `"123456789012345678"` (exact) | `"123456789012345680"` (float64 rounding) | Go's `json.Number` preserves full precision; JS loses it beyond 2^53. Compare with relative tolerance ~1e-12. |
| 2 | **Null placeholders in auto-mapping** | `["ext1", "ext2"]` | `[null, "ext1", "ext2"]` | jsonata-js inserts `null` for groups with no predicate match. gnata omits them per spec. Strip `null` entries when comparing. |

## Regex Engine: RE2 vs JavaScript RegExp

The JSONata specification inherits JavaScript's `RegExp` engine (ECMA-262), which uses backtracking and supports lookahead, lookbehind, and backreferences. gnata uses Go's `regexp` package, which implements [RE2](https://github.com/google/re2) — a linear-time regex engine that guarantees O(n) matching regardless of pattern complexity.

This is a **deliberate architectural choice**. RE2 makes [ReDoS](https://owasp.org/www-community/attacks/Regular_expression_Denial_of_Service_-_ReDoS) structurally impossible, which matters when evaluating untrusted or user-authored expressions at scale.

The following JavaScript RegExp features are **not supported** in gnata:

- Lookahead: `(?=...)`, `(?!...)`
- Lookbehind: `(?<=...)`, `(?<!...)`
- Backreferences: `\1`, `\2`, `(?P=name)`
- Atomic groups: `(?>...)`
- Possessive quantifiers: `x*+`, `x++`, `x?+`

All standard regex features (character classes, quantifiers, alternation, grouping, anchors, word boundaries) work identically in both engines. The unsupported features above are rarely used in typical JSONata expressions.

## Project Structure

```
gnata/
├── gnata.go                     # Public API: Compile, Eval, EvalBytes, EvalWithVars, CustomFunc
├── stream.go                    # StreamEvaluator, GroupPlan, EvalMany, EvalMap, MetricsHook
├── bounded_cache.go             # Lock-free FIFO ring-buffer plan cache
├── deep_equal.go                # JSONata-compatible deep equality
├── internal/
│   ├── lexer/                   # Tokenizer (all JSONata 2.x token types)
│   ├── parser/                  # Pratt parser, AST, processAST, fast-path analysis
│   └── evaluator/               # Core eval dispatch, environment, OrderedMap, signatures
│       ├── evaluator.go         #   Main Eval dispatch + ApplyFunction
│       ├── eval_binary.go       #   Binary ops, subscript, array filtering
│       ├── eval_function.go     #   Function calls, lambdas, partial application
│       ├── eval_chain.go        #   Path chaining, pipe, block, condition
│       ├── eval_transform.go    #   Transform expressions, deep clone
│       ├── eval_group.go        #   Group-by aggregation
│       ├── eval_sort.go         #   Sort expressions
│       └── ...                  #   helpers, regex, range, unary, etc.
├── functions/
│   ├── register.go              # RegisterAll — binds all 50+ stdlib functions
│   ├── string_funcs.go          # Core string functions ($substring, $trim, ...)
│   ├── string_match_replace.go  # $match, $replace, regex compilation cache
│   ├── string_encoding.go       # $eval, $base64*, $encodeUrl*
│   ├── string_format_number.go  # $formatNumber (XSLT 3.0 picture strings)
│   ├── string_format_integer.go # $formatInteger, $formatBase, $parseInteger
│   ├── numeric_funcs.go         # $sum, $round, $power, etc.
│   ├── array_funcs.go           # $sort, $distinct, $flatten, etc.
│   ├── object_funcs.go          # $keys, $values, $merge, $sift, $each
│   ├── hof_funcs.go             # $map, $filter, $reduce, $single
│   ├── datetime_funcs.go        # $now, $millis, $fromMillis, $toMillis
│   ├── datetime_format.go       #   Datetime formatting (picture strings)
│   └── datetime_parse.go        #   Datetime parsing (picture strings)
├── testdata/                    # 1,298 test files from jsonata-js
├── wasm/                        # WASM entry point for browser playground
└── assets/                      # Project logo
```

## Dependencies

| Package | Purpose |
|---|---|
| [`tidwall/gjson`](https://github.com/tidwall/gjson) | Zero-copy JSON field extraction for `EvalBytes` fast path |
| [`regexp`](https://pkg.go.dev/regexp) (stdlib) | RE2 linear-time regex for `$match`, `$replace`, `$contains`, `$split` |

One external dependency. Pure Go with no CGo or system library requirements.

## WASM

gnata compiles to WebAssembly for use in browsers:

```bash
GOOS=js GOARCH=wasm go build -ldflags="-s -w" -trimpath -o gnata.wasm ./wasm/
```

The `-s -w` flags strip the symbol table and DWARF debug info; `-trimpath` removes local path prefixes. Together they reduce the binary from ~5.5 MB to ~5.4 MB raw. The real gain is at the network layer — serve with brotli or gzip and browsers receive ~1.2–1.4 MB regardless of whether `wasm-opt` is applied.

To serve the playground locally:

```bash
# Python 3.x — no gzip
python3 -m http.server 8899

# Caddy — automatic brotli + gzip (recommended for production)
caddy file-server --root . --listen :8899
```

The WASM build exposes `gnataEval`, `gnataCompile`, and `gnataEvalHandle` functions for use from JavaScript, with a compiled-expression cache for repeated evaluations. A ready-made `playground.html` is included — build the WASM binary, copy the Go WASM support file, and serve the directory:

```bash
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" .
```

## License

gnata is licensed under the [MIT License](LICENSE).
