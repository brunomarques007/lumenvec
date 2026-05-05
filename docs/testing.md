# Testing

LumenVec uses the standard Go test runner. The default project check is:

```bash
go test ./...
```

The coverage gate is implemented by `tools/checkcoverage`:

```bash
go run ./tools/checkcoverage
```

The checker focuses on production packages and intentionally excludes generated protobuf code, examples, integration-only packages, and command-line support tools. Set `COVERAGE_THRESHOLD` to validate a stricter target locally:

```bash
COVERAGE_THRESHOLD=100 go run ./tools/checkcoverage
```

Current coverage priorities:

- keep deterministic package tests close to the code they exercise
- cover validation, error mapping, pagination, persistence recovery, cache behavior, and ANN fallback paths
- use integration tests for end-to-end HTTP and transport behavior
- avoid asserting generated protobuf internals unless a public wrapper depends on them

Before opening a pull request, run:

```bash
go test ./...
go vet ./...
go run ./tools/checkcoverage
```

## Benchmark Regression Gate

Cloud-readiness, API, transport, storage, and search changes must not degrade the current search baseline. Use the short pre-PR benchmark gate when a change can affect request routing, serialization, persistence, HTTP/gRPC behavior, or search latency:

```powershell
.\scripts\benchmark-regression-gate.ps1
```

On Unix-like shells:

```bash
make benchmark-gate
```

The gate runs a focused Docker matrix with the same baseline shape used for publishable local results:

- `10000` vectors
- `128` dimensions
- `500` measured queries
- `100` warmup queries
- concurrency `4`
- `k=10`
- ingest batch `1000`
- search batch `100`
- engines: LumenVec HTTP exact, HTTP ANN quality, gRPC exact, and gRPC ANN quality

By default it compares against `benchmarks/baselines/matrix-10k-128-c4-k10`. Benchmark results and baselines are ignored by Git; they are local artifacts.

The gate fails if any compared row shows:

- search QPS below `-5%`
- native batch-search QPS below `-5%`
- p95 or p99 latency above `+5%`
- recall@10 below the baseline

Use a smoke run without comparison only to verify runner health:

```powershell
.\scripts\benchmark-regression-gate.ps1 -SkipCompare
```

Run the full benchmark matrix before publishing performance claims, before merging search/index changes, or when the short gate reports a regression that needs confirmation.
