# Release Notes

## v0.2.1

Patch release focused on CI stabilization, automated promotion workflow support, and stronger test validation.

### Highlights
- Fixed GitHub Actions lint configuration for `golangci-lint` v2
- Eliminated the ANN race condition found by `go test -race`
- Increased real package coverage, including `internal/core` above the `90%` threshold
- Expanded local validation so CI, lint, coverage, and race checks align more closely

### Included in this release
- Automated branch-promotion workflows for `feature/* -> dev -> main`
- Release preparation on `dev` and GitHub Release publication on `main`
- Additional unit tests for server startup, gRPC handlers, client flows, cache/store behavior, and persistence
- Release packaging updates with explicit per-platform, per-transport bundle names

### Suggested tag
```bash
git tag -a v0.2.1 -m "LumenVec v0.2.1"
```

## v0.2.0

Release focused on the new runtime architecture, transport split, disk-backed payload mode, observability, and packaging for real deployment.

### Highlights
- Added gRPC transport, Go client support, and protobuf API coverage
- Introduced exclusive transport selection with `server.protocol=http|grpc`
- Added disk-backed payload storage with append-only binary data and compaction
- Added configurable in-memory cache for hot vectors
- Added Prometheus, Grafana, alert rules, validation scripts, and sample traffic generation
- Prepared release assets for HTTP and gRPC runtime bundles

### Included in this release
- New `configs/config.yaml` for HTTP mode
- New `configs/config.grpc.yaml` for gRPC mode
- Release packaging scripts producing transport-specific archives
- GitHub release workflow uploading packaged binaries as release assets
- Documentation updates for configuration, observability, architecture, and runtime usage

### Suggested tag
```bash
git tag -a v0.2.0 -m "LumenVec v0.2.0"
```

## v0.1.1

Patch release focused on test coverage enforcement and CI reliability.

### Highlights
- Fixed module-root resolution in the coverage checker used by CI
- Enforced `90%` minimum coverage across production packages
- Added package-local unit tests across production packages
- Kept examples outside the formal coverage threshold

### Included in this release
- Portable coverage verification via `go run ./tools/checkcoverage`
- CI workflow updated to fail on coverage regressions
- README, CONTRIBUTING, and Makefile updated to document the new workflow
- Test layout aligned with idiomatic Go package-local tests

### Suggested tag
```bash
git tag -a v0.1.1 -m "LumenVec v0.1.1"
```

## v0.1.0

Initial public release of LumenVec.

### Highlights
- HTTP-first vector database API in Go
- Single insert, get, delete, and similarity search endpoints
- Batch insert and batch search endpoints
- Local persistence via snapshot + WAL
- Exact and ANN search modes
- API key support and IP-based rate limiting
- Prometheus metrics endpoint
- Dockerfile and docker-compose example

### Included in this release
- Core service layer separated from HTTP transport
- Benchmarks for ingest and search paths
- Initial project publication files:
  - `LICENSE`
  - `CONTRIBUTING.md`
  - `SECURITY.md`
  - `CHANGELOG.md`
  - `Makefile`

### Suggested tag
```bash
git tag -a v0.1.0 -m "LumenVec v0.1.0"
```
