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
