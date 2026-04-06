# Release Notes

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

