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
  <a href="https://github.com/RecoLabs/gnata/actions/workflows/ci.yml"><img src="https://img.shields.io/badge/coverage-92.7%25-brightgreen" alt="Coverage" /></a>
  <a href="https://pkg.go.dev/github.com/recolabs/gnata"><img src="https://pkg.go.dev/badge/github.com/recolabs/gnata.svg" alt="Go Reference" /></a>
  <a href="https://github.com/RecoLabs/gnata/blob/main/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/RecoLabs/gnata" alt="Go version" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License" /></a>
  <a href="https://www.npmjs.com/package/gnata-js"><img src="https://img.shields.io/npm/v/gnata-js" alt="npm version" /></a>
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
- **WASM support** — compile to WebAssembly for an in-browser playground, or with [TinyGo](https://tinygo.org) for WASI hosts (several times smaller binaries).

## Quick Start

```sh
go get github.com/recolabs/gnata
```

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
fmt.Println(expr.IsComparisonFastPath())  // true — zero-copy evaluation
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

// Alternative: already-unmarshaled Go value (skips JSON decoding)
results, _ = se.EvalPreparsed(ctx, eventMap, schemaKey, indices)
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
    ├── gjson.GetBytes per fast-path expression
    ├── Fast-path expressions: distribute extracted results (0 allocs)
    ├── Full-path expressions: selective unmarshal + AST eval
    └── results[]
```

### Key Properties

- **Efficient JSON field extraction** — fast-path expressions use `gjson.GetBytes` for zero-copy path lookups directly on raw JSON bytes.
- **Schema-keyed caching** — the `GroupPlan` (merged paths, expression groupings, selective unmarshal targets) is computed once per schema key and reused immutably.
- **Lock-free reads** — `BoundedCache` publishes an `atomic.Pointer` snapshot on every write; reads scan the snapshot without acquiring a lock. Writes are serialised by a mutex.
- **Selective unmarshal** — full-path expressions unmarshal only the subtrees they need (e.g., just the `items` array from a 10KB event), not the entire document.
- **Pre-decoded map input** — `EvalMap` accepts `map[string]json.RawMessage` directly, skipping full-document serialization when the caller already has individually-encoded fields. Fast paths resolve top-level keys via O(1) map lookup.
- **Pre-parsed Go values** — `EvalPreparsed` accepts `map[string]any`, `[]any`, or any other decoded value the AST evaluator already handles, skipping JSON decoding. GJSON fast paths are not used (they need raw bytes).
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

### Argument Copying

Object arguments reach custom functions as `map[string]any`. By default every call receives freshly copied maps, so a function may modify the maps it is given. Arrays are passed through without copying when they hold only scalars, so a function must not modify an array argument (or one nested inside a map) in place.

When many expressions pass the same payload objects to custom functions, the copies add up. `WithReadOnlyCustomFuncArgs` caches the normalized view on each object decoded from the input and shares it between calls:

```go
se := gnata.NewStreamEvaluator(nil,
    gnata.WithCustomFunctions(customFuncs),
    gnata.WithReadOnlyCustomFuncArgs(), // custom functions must not modify their arguments
)
```

Only objects gnata decodes itself from the input of `EvalMany`, `EvalManyWithVars`, `EvalOne` and `EvalMap` are cached; values you decode with `DecodeJSON` and pass to `EvalPreparsed` or as variables never are. Objects returned in results may carry the cache, so don't mutate them and feed them back in while the option is set.

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

All benchmarks on Apple M4 Pro. gnata is compared against the reference [jsonata-js](https://github.com/jsonata-js/jsonata) implementation running in Node.js, evaluation time only (no RPC/transport overhead). Simple field lookups and comparisons hit a zero-copy GJSON fast path — the JSON document is never fully parsed — and functions on a pure path (e.g. `$exists(a.b)`, `$lowercase(name)`) get a similar fast path via a single `gjson.GetBytes` call.

| Category | gnata latency | Speedup vs JSONata |
|---|---|---|
| Fast Path (GJSON zero-copy) | 41 ns – 142 ns | 570x – 1,500x* |
| Boolean Logic | 127 ns – 573 ns | 43x – 270x |
| Numeric & Arithmetic | 177 ns – 530 ns | 32x – 200x* |
| String Functions | 73 ns – 580 ns | 7x – 360x* |
| Array & Filtering | 195 ns – 573 ns | 24x – 140x |
| Higher-Order Functions & Lambdas | 425 ns – 1.5 µs | 8x – 88x* |
| Joins (`@` operator) | 545 ns – 1.6 µs | up to 19x* |
| Conditionals & Blocks | 170 ns – 7.1 µs | 3x – 68x* |
| Regex | 497 ns – 947 ns | up to 6x* |
| Complex Boolean Expressions | 202 ns – 928 ns | up to 31x* |

\* Some expressions in this category evaluate faster than JSONata's measurement floor (< 1 µs) — actual speedup is understated.

Fast-path expressions typically achieve **0-2 allocations** and **0-40 bytes** per evaluation. Supported fast-path functions (21): `$exists`, `$contains`, `$string`, `$boolean`, `$number`, `$keys`, `$distinct`, `$not`, `$lowercase`, `$uppercase`, `$trim`, `$length`, `$type`, `$abs`, `$floor`, `$ceil`, `$sqrt`, `$count`, `$reverse`, `$sum`, `$max`, `$min`, `$average`.

### StreamEvaluator (batch of 4 expressions)

| Metric | Value |
|---|---|
| ns/op | 20,500 |
| throughput | 29 MB/s |
| allocs/op | 517 |
| cache hit rate | 100% (after warm-up) |

## JSONata Compatibility

gnata targets full compatibility with [JSONata 2.x](https://docs.jsonata.org), validated against **1,704 test cases** from the official [jsonata-js test suite](https://github.com/jsonata-js/jsonata/tree/master/test/test-suite) at **v2.2.2** — **0 failures, 0 skips**.

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

### Guardrails

`Compile` accepts optional resource guardrails, matching jsonata-js 2.2's `stack` / `timeout` / `sequence` options:

```go
expr, err := gnata.Compile(userExpr,
    gnata.WithStack(100),                // error D1011 beyond this recursion depth
    gnata.WithTimeout(500*time.Millisecond), // error D1012 if evaluation runs longer
    gnata.WithSequence(1_000_000),       // error D2015 if a built sequence grows past this
)
```

`WithTimeout` is enforced by checking a deadline on every function call and periodically between expression nodes, rather than with a timer, so it also applies inside a synchronous call on single-threaded WebAssembly hosts. A single builtin or custom function call is not interrupted, so evaluation can exceed the timeout by at most the duration of the call in progress.

All three are opt-in; without them gnata keeps its existing defaults (100-deep call stack → `U1001`, no timeout beyond the caller's `context.Context`, and the built-in 10,000,000-element hard caps on the range operator and `$append`). `WithSequence` bounds every major sequence-growth path: the range operator, `$append`, `$map`, `$filter`, `$each`, wildcard (`*`), descendant (`**`), and repeated positions in a filter directly after a `#`/`@` binding (`a.b#$i[...]`). Use guardrails when evaluating expressions from an untrusted source.

### Decimal Precision

By default gnata computes and compares numbers in float64, so values beyond 2^53 and decimals such as `0.1 + 0.2` are rounded. `WithDecimalPrecision` opts in to decimal floating point, rounded half to even to a given number of significant digits:

```go
expr, err := gnata.Compile(`$sum(items.amount) = total`,
    gnata.WithDecimalPrecision(78), // 17–100 digits; 78 covers uint256
)
```

`0` (the default) leaves it off. Number literals, arithmetic (including fractional powers), comparisons, equality of arrays and objects, `in`, sorting, `$distinct`, `$string` and the numeric builtins (`$number`, `$sum`, `$round`, `$sqrt`, `$power`, ...) then work in decimal. Results computed in decimal are `json.Number` values with up to that many digits; other numbers keep their type, so results and custom-function arguments may be `float64` or `json.Number`. A `float64` counts as its shortest decimal form, so `0.1` is exactly `0.1`. `$formatNumber` is exact too, so `$formatNumber(1.015, '0.00')` gives `"1.02"` rather than float64's `"1.01"`. So is `$formatBase`, e.g. a uint256 in hex with `$formatBase(value, 16)`.

Input numbers keep more digits than float64 holds only if gnata sees their text: pass raw JSON to `EvalBytes`, `EvalMap` or a `StreamEvaluator`, or decode with `json.Decoder.UseNumber()` before calling `Eval`. Magnitudes stay within the float64 range, with the same out-of-range errors.

The arithmetic comes from a copy of [cockroachdb/apd](https://github.com/cockroachdb/apd) in `internal/third_party/apd`, an implementation of the General Decimal Arithmetic specification. A fractional power is the costliest operation, about a millisecond at 100 digits. The decimal support adds about 140 KB (11%) to a TinyGo WebAssembly build, whether or not it is enabled.

These still use float64:

- `$formatInteger` and `$parseInteger`, which are exact only up to 2^53.
- The date/time functions. Millisecond timestamps fit in float64 exactly.

## Known Behavioral Differences from jsonata-js

gnata targets exact parity with the JSONata reference implementation ([jsonata-js](https://github.com/jsonata-js/jsonata)). The differences below are deliberate: they stem from platform differences between Go and JavaScript, or gnata accepts input that jsonata-js mishandles.

| # | Area | gnata | jsonata-js | Notes |
|---|------|-------|------------|-------|
| 1 | **Large integer precision** | `"123456789012345678"` (exact) | `"123456789012345680"` (float64 rounding) | Go's `json.Number` preserves full precision; JS loses it beyond 2^53. Compare with relative tolerance ~1e-12. |
| 2 | **Null placeholders in auto-mapping** | `["ext1", "ext2"]` | `[null, "ext1", "ext2"]` | jsonata-js inserts `null` for groups with no predicate match. gnata omits them per spec. Strip `null` entries when comparing. |
| 3 | **Argument errors** | `D3006` for a missing argument | `T0410`, or `undefined` | gnata reports wrong argument counts and types consistently; codes for malformed calls can differ. |
| 4 | **Timezones** | `$fromMillis(0, "[H01]:[m01]", "+05:30")` → `"05:30"` | `"00:05"`; `"NaN"` for `"Europe/London"` | gnata accepts IANA zone names and `±HH:MM` offsets and rejects malformed ones (`D3137`). `$toMillis` `[Z]` also parses `Z` and `±HHMM`, rejects an offset over about 68 years (2³¹ seconds), and matches a picture's separator literally, where jsonata-js pastes it into a regex (`.` matches any character; `*`, `+` or `(` throw) and drops the offset when it cannot split at it (`-` with a negative offset, a letter in the other case). |
| 5 | **`$base64decode`** | `D3137` for invalid input | `""` | Unpadded input decodes in both. |
| 6 | **`$contains`, `$values`, `$flatten`** | array search, object values, flattening | `T0410` / unknown function | gnata-only extensions. |
| 7 | **Fractional seconds `[f]`** | `[f01]` → `"12"`, `[f0001]` → `"1230"` for .123 s | `"123"`, `"0123"` | gnata follows XPath F&O: one digit per picture character, padded on the right. jsonata-js formats milliseconds as an integer. |
| 8 | **Month names in `$toMillis`** | `"2018 April"` with `[MNn,3-3]` → April | January | jsonata-js only recognizes the exact truncated name and silently defaults any other word to January. |
| 9 | **Uncaptured regex groups** | `""` in `groups` | `undefined` (`null` once serialized) | Go arrays cannot hold `undefined`; `""` behaves the same in string operations. |
| 10 | **Date/time picture widths** | `D3010` when zero-padding widths total over 10,000 | builds the string, or crashes | Same limit as `$pad`; widths that cannot pad (names, words, `$toMillis`) are not limited. |
| 11 | **Years 0–99 in `$toMillis`** | `$toMillis("18", "[Y01]")` → year 18 | year 1918 | JavaScript's `Date.UTC` maps years 0–99 to 1900–1999; gnata keeps the parsed year. |
| 12 | **Regex match positions** | `$match("😀ab", /a/).index` → `1` | `2` | gnata counts code points, like `$length` and `$substring`; jsonata-js counts UTF-16 units. Applies to `index`, `start` and `end`. |
| 13 | **Exponent mantissa width** | `$formatNumber(1000, "0.0e0")` → `"1.0e3"` | `"10.0e2"` | gnata keeps the mantissa within the picture's integer digits, also when rounding carries over (`9.95` → `"1.0e1"`, not `"10.0e0"`). |
| 14 | **Negative offsets in `[Z]`** | `$fromMillis(0, "[Z0000]", "-0530")` → `"-0530"` | `"-0630"` | jsonata-js floors a negative `hhmm` offset into its hours, so half-hour zones west of UTC lose an hour. Its `$toMillis` adds the minutes of a negative offset, reading `-02:30` as `-01:30`. |
| 15 | **Match object `next`** | `$replace("ababab", /b/, function($m){ $m.next() ? "X" : "Y" })` → `"aXaXaY"` | `"aXabaY"` | Only `$replace` callbacks get `next`, which returns the match after `$m`. jsonata-js advances the cursor `$replace` itself uses, skipping the returned match. `$match` and `~> /re/` results omit `next` so they hold no function values. |
| 16 | **`$toMillis` limits** | `$toMillis("275760-09-14", "[Y]-[M]-[D]")` → `null` | `NaN` | A result past a JavaScript Date's ±8.64e15 ms serializes as `null` in both, but in gnata it is `null` inside an expression too: `$type` gives `"null"` (jsonata-js: `"object"`), and `$fromMillis` raises T0410 where jsonata-js formats `"0NaN-NaN-…"`. gnata also bounds the backtracking that matches a picture to 256 parsed runes per input rune and picture part, and reads a match that needs more as undefined; jsonata-js's regex can run superlinear there. On 32-bit targets, a component above 2³¹−1 reads as `null`. |

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
├── gnata.go                     # Public API: Compile, Eval, EvalBytes, EvalBytesWithVars, EvalMap, EvalWithVars, CustomEnvironment, OrderedMap, JSONNull
├── stream.go                    # StreamEvaluator, GroupPlan, EvalMany, EvalManyWithVars, EvalMap, EvalPreparsed, MetricsHook
├── bounded_cache.go             # Lock-free FIFO ring-buffer plan cache
├── deep_equal.go                # JSONata-compatible deep equality
├── internal/
│   ├── decimal/                 # Decimal floating point for WithDecimalPrecision
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
│   ├── numeric_decimal.go       # Decimal variants used under WithDecimalPrecision
│   ├── array_funcs.go           # $sort, $distinct, $flatten, etc.
│   ├── object_funcs.go          # $keys, $values, $merge, $sift, $each
│   ├── hof_funcs.go             # $map, $filter, $reduce, $single
│   ├── datetime_funcs.go        # $now, $millis, $fromMillis, $toMillis
│   ├── datetime_format.go       #   Datetime formatting (picture strings)
│   └── datetime_parse.go        #   Datetime parsing (picture strings)
├── testdata/                    # 1,298 test files from jsonata-js
├── wasm/                        # WASM entry point for browser playground
├── examples/                    # Runnable examples (basic, evalbytes, streaming, customfunc, wasi)
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

The WASM build exposes six functions for use from JavaScript (the raw exports are underscore-prefixed; `playground.html` wraps them into clean public names):

| Function | Description |
|---|---|
| `gnataEval(expr, jsonData)` | One-shot compile + evaluate (expressions are cached). |
| `gnataCompile(expr)` | Compile an expression and return a numeric handle. |
| `gnataEvalHandle(handle, jsonData)` | Evaluate a compiled handle against JSON data. Uses `EvalBytes` internally for gjson fast-path access. |
| `gnataReleaseHandle(handle)` | Free a compiled handle. |
| `gnataEvalMap(handle, jsonObject)` | Evaluate a compiled handle using `EvalMap` — O(1) top-level key lookup with gjson fast paths for nested access. Ideal for pre-destructured data. |
| `gnataEvalWithVars(handle, jsonData, varsJson)` | Evaluate with external `$`-variable bindings (e.g. `{"$threshold": 100}`). |

A ready-made `playground.html` is included — build the WASM binary, copy the Go WASM support file, and serve the directory:

```bash
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" .
```

### TinyGo (WASI)

gnata also builds with [TinyGo](https://tinygo.org) for `wasip1` hosts (wasmtime, wazero, JVM WASM runtimes, …). It passes the jsonata-js conformance suite there with results identical to the standard Go build.

```bash
GOEXPERIMENT=nojsonv2 tinygo build -target=wasip1 -scheduler=none -opt=1 -no-debug -o app.wasm ./your/cmd
```

- `GOEXPERIMENT=nojsonv2` is required: Go's json/v2-backed `encoding/json` relies on reflection TinyGo does not implement.
- `-scheduler=none` works because gnata starts no goroutines (including for `WithTimeout`).
- Prefer `-opt=1` when the host compiles WASM to JVM bytecode: higher levels inline large functions past the JVM's method-size limit, which forces those runtimes to interpret them.
- `recover` is unavailable on TinyGo/WASM, so a runtime panic traps the instance; hosts should treat a trap as a failed evaluation and re-instantiate the module. Size `-stack-size` for the recursion depth your expressions need.

[`examples/wasi`](examples/wasi/main.go) is a complete program built this way. It prints with the `println` builtin: under TinyGo, writing through `os.Stdout` (e.g. `fmt.Println`) pulls in timers that need a goroutine scheduler, so it cannot be used with `-scheduler=none`. Built with TinyGo 0.42 it is 1.2 MB (0.45 MB gzip), versus 6.5 MB (1.74 MB gzip) for `GOOS=wasip1` with Go 1.27. In our measurements on wazero's compiler (Apple M4), gnata evaluated real-world transformation expressions 1.6–1.9× faster in the TinyGo build than in the `GOOS=wasip1` build; results depend on the workload and runtime.

## License

gnata is licensed under the [MIT License](LICENSE).
