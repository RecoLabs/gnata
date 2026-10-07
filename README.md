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
  <a href="https://github.com/RecoLabs/gnata/actions/workflows/ci.yml"><img src="https://img.shields.io/badge/coverage-93.8%25-brightgreen" alt="Coverage" /></a>
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

Where `args` are the evaluated arguments passed by the JSONata expression and `focus` is the current context value. `focus` follows jsonata-js:

- it is `nil` when the function is passed as an argument, to `$map` or to a lambda, or applied by a bare `x ~> $f`;
- a call in tail position of a lambda body gets the context of the call that entered the lambda.

A custom function named `clone` replaces `$clone`, including the copy the transform operator (`~> |…|…|`) makes of its input.

### Regex Arguments (breaking change in v0.6.0)

A regex is a function inside an expression, as in jsonata-js. A custom function receives a regex argument as a `map[string]any{"pattern": …, "flags": …}`, and returning **that same map** returns the regex. Any other map of that shape is a plain object: one the function builds, a copy of the map it received, and one from input data, even when the function passes it through.

Before v0.6.0 a regex was such a map everywhere, so any `{pattern, flags}` map a custom function returned acted as a regex:

```go
// v0.5.x: $match("abc", $regex("b")) matched. Since v0.6.0 it raises T0410.
"regex": func(args []any, _ any) (any, error) {
    return map[string]any{"pattern": args[0], "flags": "g"}, nil
},
```

To migrate, take the regex as an argument and return the value you received; a custom function that passes an argument through, such as a `$coalesce(a, /b/)`, keeps working:

```go
// $match("abc", $pick(cond, /b/, /c/)) matches with either regex.
"pick": func(args []any, _ any) (any, error) {
    if args[0] == true {
        return args[1], nil
    }
    return args[2], nil
},
```

### Lambda Context (breaking change in v0.6.0)

A lambda's body evaluates against the context the lambda was defined in, as in jsonata-js. Before v0.6.0, a call that passed arguments evaluated the body against the context of the call, so over `{"name": "root", "items": [{"name": "a"}, {"name": "b"}]}`:

```
($f := function($x){ name & $x }; items.$f("!"))
// v0.5.x: ["a!", "b!"]. Since v0.6.0: ["root!", "root!"].
```

To read the item a call is made on, pass it as an argument, as in `($f := function($v, $x){ $v.name & $x }; items.$f($, "!"))`, which gives `["a!", "b!"]`. A lambda defined in the path step, as in `items.(function($x){ name & $x })("!")`, also reads each item.

### `functions.EvalFn` (breaking change in v0.6.0)

`functions.EvalFn`, the callback `functions.RegisterAll` takes to apply a function value, lost its `focus` parameter: it is now `func(fn any, args []any, env *evaluator.Environment) (any, error)`. As in jsonata-js, the standard library applies a function it is given, such as a `$map` callback, with a null context. `EvalFn` refers to the internal `evaluator.Environment` type, so code outside this module cannot implement it in practice.

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

All three are opt-in; without them gnata keeps its existing defaults (100-deep call stack → `U1001`, no timeout beyond the caller's `context.Context`, and the built-in 10,000,000-element hard caps on the range operator and `$append`). `WithSequence` bounds the sequences jsonata-js bounds: the range operator, array constructors, group-by, sorts, filters (including positions a filter repeats, `a[[0,0]]`), `$append`, `$map`, `$filter`, `$each`, `$keys`, `$spread`, `$lookup`, `$match`, wildcard (`*`), descendant (`**`), every path step (field, variable, block and function steps) and every tuple stream of a `#`/`@` binding, including its filter stages (`a.b#$i[p1][p2]`) and a binding with nothing after it (`a#$i`). As in jsonata-js, a step that maps over input data counts too: `a.b` over more than `n` items of `a` raises `D2015`. A last step returns the value of a lone context as is when that value is an array stored in the data: exactly one context yields a value, from a field of an object rather than a lookup over an array. So `$count(a)` and `$count(m.(b))` still see a long stored array, while `$count(oo.a)` with `oo` an array of arrays raises `D2015`, as the lookup over the inner array builds a sequence. With `WithSequence`, the gjson fast paths of `EvalBytes`, `EvalMap` and `StreamEvaluator` keep their single gjson lookup, which never crosses an array; a path that crosses one, and `$keys`, whose result the limit bounds, fall back to the full evaluator, where the limit is enforced. Independently of `WithStack`, calls through partial applications, compositions, builtin function arguments, transforms and builtins passed as higher-order functions' callbacks nested more than 1,500 deep return `U1001`, since each one recurses on the Go stack (a call to a lambda whose body nests more than 32 levels deep spends one unit of the same budget per 3 levels beyond, so `WithStack` recursion with such bodies is capped too, and a call to a transform spends one unit plus one per 3 levels of its clauses). This bound leaves room on js/wasm hosts such as Node with an 8 MB stack (`go_js_wasm_exec`'s `--stack-size=8192`); smaller stacks, such as Node's default, can still overflow below it; on TinyGo's fixed-size stacks, deep chains can still trap. Arrays an expression shares at many levels, as a `$reduce` that nests one array in itself twice per step builds, can hold exponentially many paths; `WithTimeout` stops every walk over them, but two are bounded only by it: `*` over such arrays, and the copy of such a value passed to a custom function, which gives each occurrence of an object its own map unless `WithReadOnlyCustomFuncArgs` is set. Use guardrails when evaluating expressions from an untrusted source.

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
| 1 | **Large integer precision** | `f` over `{"f":123456789012345678}` → `123456789012345678`; `o` over `{"o":{"x":1.50}}` → `{"x":1.50}` | `123456789012345680`; `{"x":1.5}` | A number read from raw JSON (`EvalBytes`, `EvalMap`, a `StreamEvaluator`, `DecodeJSON`, `json.Decoder.UseNumber`) is a `json.Number`, which can keep its text when returned or passed on unchanged; JS reads it as a float64. `$string` and `&` lay it out as jsonata-js does (`"1.5"`), except an integer literal beyond 2^53 (row 36), and arithmetic and comparisons use float64 (`f = 123456789012345680` is `true`). Compare results with relative tolerance ~1e-12. |
| 2 | **Null placeholders in auto-mapping** | `g.t[true]` over `{"g":[{"o":1},{"t":"ext1"},{"t":"ext2"}]}` → `["ext1","ext2"]` | `[null,"ext1","ext2"]` | jsonata-js filters each context's lookup on its own, so a context without `t` yields one undefined item, which a predicate true for it keeps and which serializes as `null`. gnata's sequences cannot hold undefined. A predicate on the value (`[$exists($)]`, `[$ != "x"]`) or a literal position (`[0]`) drops the item in both. Row 26 covers paths where no context has the field. |
| 3 | **Argument errors in partial applications** | `$join(?, ",")(["a",1])` → `T0412`; `$uppercase(?)(1)` → `T0410` | `"a,1"`; an uncoded `TypeError` | jsonata-js calls a partially applied built-in without validating its arguments, so it computes with mistyped ones as JavaScript does or crashes; gnata's built-ins check them themselves there. Higher-order built-ins follow jsonata-js: they do not wrap a non-array argument, so `$map(?)(f)` and `$map(?, f)(5)` are undefined and `$map(?, f)("ab")` maps each character. Direct calls and built-ins passed as callbacks validate their arguments against the jsonata-js signature in both (`T0410`/`T0411`/`T0412`, blaming the same argument), except `$contains` (row 7). |
| 4 | **Timezones** | `$fromMillis(0, "[H01]:[m01]", "+05:30")` → `"05:30"` | `"00:05"`; `"NaN"` for `"Europe/London"` | gnata accepts IANA zone names and `±HH:MM`, `±HHMM` and `HHMM` offsets and rejects any other (`D3137`), and `""` is UTC. jsonata-js reads the leading integer as `hhmm`, so `"+5"` and `"+05"` are five minutes, `"0"` and `"530"` are offsets, and a string without one, `""` included, formats as `"NaN"` fragments. `$toMillis` `[Z]` also parses `Z` and `±HHMM`, and rejects an offset over about 68 years (2³¹ seconds). |
| 5 | **`$toMillis` offset separators** | `$toMillis("2018-04-01 10:00-02x00", "[Y]-[M]-[D] [H]:[m][Z01.01]")` → undefined | `1522576800000` | gnata matches a picture's offset separator literally, so it also reads `[Z01*01]`. jsonata-js pastes the separator into a regex unescaped: `.` matches any character, and `[Z01*01]` throws an uncoded regex error. It also drops an offset it cannot split at the separator (`-` with a negative offset, a letter whose case differs from the picture's). |
| 6 | **`$base64decode`** | `D3137` for invalid input | `""` | Unpadded input decodes in both. |
| 7 | **`$contains`, `$values`, `$flatten`** | array search, object values, flattening | `T0410` / unknown function | gnata-only extensions. `$contains` checks only its argument count against its jsonata-js signature and its argument types itself, so an array is accepted as its first argument. |
| 8 | **Fractional seconds `[f]`** | `[f01]` → `"12"`, `[f0001]` → `"1230"` for .123 s | `"123"`, `"0123"` | gnata follows XPath F&O: one digit per picture character, padded on the right. jsonata-js formats milliseconds as an integer. |
| 9 | **Month names in `$toMillis`** | `"2018 April"` with `[MNn,3-3]` → April | January | jsonata-js only recognizes the exact truncated name and silently defaults any other word to January. |
| 10 | **Uncaptured regex groups** | `""` in `groups` | an undefined item (`null` once serialized) | gnata's arrays hold no undefined item, and a `null` would read as `"null"` in `&`. With `""`, `&` and `$replace`'s `$1` give what jsonata-js gives, but reading the item does not: `$exists` gives `true`, `$type` `"string"` and `$length` `0`, where jsonata-js gives `false` and undefined, and `$join` of the groups joins them where jsonata-js raises `T0412`. |
| 11 | **Date/time picture widths** | `D3010` when zero-padding widths total over 10,000; `$fromMillis(0, "[Y,2-]")` → `"70"` | builds the string, or crashes; `"NaN"` | Same limit as `$pad`; widths that cannot pad (names, words, `$toMillis`) are not limited. A width is an optional `+` and decimal digits; jsonata-js's `parseInt` also reads a negative or `0x` width, which can turn its parse regex into literal text. A non-numeric year maximum is ignored, so `[Y,2-]` formats as `[Y,2]` and `[Y,*-x]` as `[Y]`, where jsonata-js's `parseInt` reads it as `NaN` and formats `"NaN"`. `$toMillis` follows jsonata-js: the year reads every digit. |
| 12 | **Years 0–99 in `$toMillis`** | `$toMillis("18", "[Y01]")` → year 18 | year 1918 | JavaScript's `Date.UTC` maps years 0–99 to 1900–1999; gnata keeps the parsed year. |
| 13 | **Regex match positions** | `$match("😀ab", /a/).index` → `1` | `2` | gnata counts code points, like `$length` and `$substring`; jsonata-js counts UTF-16 units. Applies to `index`, `start`, `end` and the offset a regex called as a function searches from. |
| 14 | **Exponent mantissa width** | `$formatNumber(1000, "0.0e0")` → `"1.0e3"` | `"10.0e2"` | gnata keeps the mantissa within the picture's integer digits, also when rounding carries over (`9.95` → `"1.0e1"`, not `"10.0e0"`). |
| 15 | **Negative offsets in `[Z]`** | `$fromMillis(0, "[Z0000]", "-0530")` → `"-0530"` | `"-0630"` | jsonata-js floors a negative `hhmm` offset into its hours, so a zone west of UTC with minutes loses an hour (`-0045` is `"-01:45"`), and a one- or two-digit picture writes the minutes negative too (`[Z01]` → `"-06:-30"`). Other offsets format the same in both. Its `$toMillis` adds the minutes of a negative offset, reading `-02:30` as `-01:30`. |
| 16 | **Match object `next`** | `$replace("ababab", /b/, function($m){ $m.next() ? "X" : "Y" })` → `"aXaXaY"` | `"aXabaY"` | Only `$replace` callbacks get `next`, which returns the match after `$m`. jsonata-js advances the cursor `$replace` itself uses, skipping the returned match. `$match`, `~> /re/` and `/re/(s)` results omit `next` so they hold no function values. |
| 17 | **`in` with arrays or objects** | `[5] in [[5]]` → `true` | `false` | jsonata-js compares with `===`, so only the same object matches. `=` became deep equality in 1.8, and the docs describe `in` as value inclusion. |
| 18 | **Function identity through builtins** | `($f := function(){1}; $append($f, [])[0] = $f)` → `true` | `false` | Both compare functions by identity. jsonata-js wraps every function argument of a call in a fresh closure, so a builtin that returns its argument returns a different function; gnata wraps function arguments only in calls to lambdas, so a builtin returns the function itself. |
| 19 | **Self-referencing transform update** | `$ ~> \|a\|{"self": $}\|` stores a copy of `a` | a circular object it cannot serialize | Results stay finite trees. This also covers a variable that the pattern or an earlier target's update bound to an ancestor. |
| 20 | **Transform pattern reaching the input through `$$`** | `$ ~> \|$$.x\|{"z": 1}\|` leaves the input unchanged; reading `$$.x.z` afterwards sees no `z` | changes the input object in place | gnata never mutates decoded or caller-owned input. |
| 21 | **Transform pattern reaching other objects the expression holds** | `($v := {"a":{}}; $ ~> \|$v.a\|{"z": 1}\|; $v)` leaves `$v` unchanged, as does a nested transform's pattern reaching the outer target through a variable | changes the object in place | A transform changes only its own clone. Maps from `DecodeJSON` are never frozen yet belong to the caller, so the evaluator cannot tell its own objects from the caller's. |
| 22 | **`$distinct` of an array whose sequence mark gnata cannot see** | `$map([[5,5]], function($v){$type($distinct($v))})` → `"number"`; `($f := function(){a.b}; $distinct($f()))` → `[5]` | `"array"`; `5` | jsonata-js keeps a plain array a plain array and collapses a sequence, as gnata does for a variable a bind sets and a parameter bound to a call's argument. gnata counts a parameter that a built-in such as `$map`, `$filter`, `$reduce` or `$sort` binds as a sequence; a lambda's result, an object field holding a sequence (`$x.k` with `$x := {"k": a.b}`) and the context `$eval` takes as plain arrays; and a block last step that several contexts map over (`w.(k)`) or that ends in a variable (`q.($x := k; $x)`) as a sequence. |
| 23 | **Integer-like object keys** | `{"2":1,"1":2,"b":3}` keeps insertion order | `{"1":2,"2":1,"b":3}` | JavaScript objects list integer-like keys first, in numeric order. Group results show it too: `g{k: v}` with keys inserted as `"b"`, `"10"`, `"2"` gives `{"b":1,"10":2,"2":3}`, against `{"2":3,"10":2,"b":1}`. |
| 24 | **Subscripted `%` block outside a tuple step** | `x.((a)[%.c])` → `[{"b":1},{"b":2}]` | `[{"@":{"b":1},"!0":{…}}, …]` | jsonata-js leaks its internal tuple objects into the result; gnata returns the values. |
| 25 | **Wildcard over a sequence passed to `$eval`** | `$eval("*", a.b)` → `[1,3]` | `[1,3,true]` | jsonata-js's wildcard reads its internal `sequence` flag as a field. |
| 26 | **Filter over a field several contexts lack** | `w.a[true]` over `{"w":[[1,2],[3]]}` → `undefined`; `$count` → `0`, `$exists` → `false` | `[null,null]`; `2`, `true` | jsonata-js filters each context's missing field as one undefined item, which a truthy predicate or a computed position (`[[0]]`, not a literal `[0]`) keeps, so a mapped path collects undefined items that serialize as `null`. gnata's sequences cannot hold undefined. Over one context gnata follows jsonata-js: the predicate still runs, so `q[$error("x")]` raises `D3137`, and the next step takes the kept item as an undefined context, so `q[true].$count($)` is `0`. |
| 27 | **Regex called on a function or a constructed object** | `/a/($string)` → `undefined`; `/o/({})` → a match at `1` | a match at `0`; a `TypeError` | Both match the string JavaScript converts the argument to, searching from the offset in the second argument. jsonata-js converts a function to its JavaScript source text, which gnata does not have, and fails on an object the expression builds, which has no prototype; gnata reads every object as `"[object Object]"`, as jsonata-js reads one from the input. |
| 28 | **Regex values in Go** | `/a/` → `{"flags": "g", "pattern": "a"}` | the regex function | A regex is a function inside an expression. A custom function receives it as that map, and returning the same map returns the regex; any other map of that shape, built by the function or from input data, stays an object (see [Regex Arguments](#regex-arguments-breaking-change-in-v060)). A regex result is that map. Inside a result it encodes to that JSON, and `NormalizeValue` turns it into that map. |
| 29 | **Partial `$string`, and function arguments in partial built-ins** | `$string(?)("x")`, `"x" ~> $string(?)` → `"x"`; `$replace(?, "b", function($m){"X"})("abc")` → `"aXc"` | `S0208`; `"a[object Object]c"` | jsonata-js builds partial applications of built-ins from their JavaScript parameter lists, and `$string`'s default parameter does not parse. gnata follows those lists otherwise, applying a built-in to the parameters it declares, without a context. jsonata-js also applies the built-in without the context its own calls provide, so `$match` and `$replace` cannot call a function argument, nor `$sort` a comparator the partial application binds: they fail, or `$replace` inserts the function as text. |
| 30 | **Extra syntax** | `1..3`, `$count(1..3)`, `[1,]`, `{"a":1,}`, `$sum(1,)`, `function($x,){$x}`, `a^()`, `'it\'s'`, `2 ** 8` are accepted | `S0201`, `S0202`, `S0211` or `S0103` | Ranges work outside array constructors, lists allow a trailing comma, an empty sort keeps the order, `\'` escapes a quote, and `**` raises to a power. |
| 31 | **Regex literals** | `/[)]/`, `/a]/`, `/a}/` and `/(a})/` read as written; an invalid pattern such as `/a{2,1}/` raises an error when used (`D3137` in `$match`, `D1002` when called) | `S0302`; an uncoded `SyntaxError` while parsing | jsonata-js closes a regex at the first unescaped `/` where one count of its brackets, of any kind and in classes too, is zero; gnata closes every regex it closes at the same `/`. Where that count finds none, gnata reads character classes, a `)` closing the innermost open `(` or `{`, and treats a stray `]`, or a `}` that closes no `{` (`/(a})/`), as a literal, as RE2 does. |
| 32 | **Lambda signatures** | `S0402` for an unknown character, as in `function($a)<#n:n>{$a}` | ignores unknown characters | The same parser validates custom function signatures, where a typo should fail. |
| 33 | **Nesting depth** | `S0218` past 10,000 levels; `U1001` past 1,500 nested calls through partial applications, compositions, function arguments, transforms and builtin callbacks (see Guardrails) | a `RangeError` stack overflow from about 2,000 levels | Each nested expression and each chained operator is a level, in `Compile` and `$eval` alike. The limits keep deep expressions away from Go's fatal stack limit. |
| 34 | **`$toMillis` limits** | `$toMillis("275760-09-14", "[Y]-[M]-[D]")` → `null` | `NaN` | A result past a JavaScript Date's ±8.64e15 ms serializes as `null` in both, but in gnata it is `null` inside an expression too: `$type` gives `"null"` (jsonata-js: `"object"`), and `$fromMillis` raises T0410 where jsonata-js formats `"0NaN-NaN-…"`. gnata also bounds the backtracking that matches a picture to 256 parsed runes per input rune and picture part, and reads a match that needs more as undefined; jsonata-js's regex can run superlinear there. On 32-bit targets, a component above 2³¹−1 reads as `null`. |
| 35 | **Day of year out of range** | `$toMillis("2018-366", "[Y]-[d]")` → `2019-01-01` | `2018-01-01` | jsonata-js turns `[d]` into a month and day through a `Date`, then rebuilds the date with the parsed year, so a day past the year's end wraps back into it; it also treats day 0 as no day at all, giving 1 January. gnata counts the days on from 1 January, so day 0 is 31 December of the year before. |
| 36 | **Large integers in `$string` and `&`** | `$string(id)` over `{"id":12345678901234567890}` → `"12345678901234567890"` | `"12345678901234567000"` | An integer literal read from raw JSON (`EvalBytes`, `EvalMap`, a `StreamEvaluator`, `DecodeJSON`, `json.Decoder.UseNumber`) with no fraction or exponent and a magnitude above 2^53 keeps its digits when `$string` or `&` prints it, also inside an object or array (`$string(obj)` with `obj` holding the id), so large ids survive. Everything else prints as in jsonata-js: 2^53 itself, a fraction or exponent (`12345678901234567890.0`) and computed numbers print the float64, and a `float64` from Go-map input has already lost the digits. Arithmetic rounds (`$string(id + 0)` → `"12345678901234567000"`) and comparisons use float64, so ids that differ only in their last digits compare equal (`id = 12345678901234567891` is `true`) but print differently. |

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
│       ├── eval_transform.go    #   Transform expressions
│       ├── value_copy.go        #   Iterative copies for $clone, transforms and $string
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
