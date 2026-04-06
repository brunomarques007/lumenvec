# Release Notes

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
