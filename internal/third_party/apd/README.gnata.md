# apd (vendored copy)

A copy of [github.com/cockroachdb/apd](https://github.com/cockroachdb/apd)
v3.2.3 (commit 6d9c587326e78bcfbea630bf52893ff45f6f9ed5), licensed under the
Apache License 2.0 (see `LICENSE`). It provides the decimal arithmetic behind
`gnata.WithDecimalPrecision`, through the wrapper in `internal/decimal`.

Changes from upstream:

- Removed `decomposer.go`, `decomposer_test.go` and `sql_test.go`, which
  implement and test `database/sql` support and depend on `github.com/lib/pq`.
- Test files import this package's path instead of
  `github.com/cockroachdb/apd/v3`; `example_test.go` carries a note saying so.

Keep the remaining files otherwise identical to upstream so that fixes can be
re-applied by copying the files of a newer release. The `testdata/*.decTest`
files are Mike Cowlishaw's General Decimal Arithmetic test cases, as
distributed with apd.

The test data adds about 1.4 MB to the gnata module download; it is kept
because it is the main evidence that the arithmetic is correct.

`go vet` reports a possible misuse of `unsafe.Pointer` in `bigint.go`'s
`noescape`, an intentional upstream technique. golangci-lint excludes this
directory, `go test` does not run that check, and CI's coverage gate excludes
the directory too. Run vet on gnata's own packages with
`go vet $(go list ./... | grep -v /internal/third_party/)`.
