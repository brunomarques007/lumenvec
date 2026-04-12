# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog.

## [Unreleased]

## [v0.2.2] - 2026-04-12

Automated release from `v0.2.1` to `v0.2.2`.

### Commits
- Automate release versioning (817d96b)
- Refine CI and promotion workflow triggers (4b0fd72)

## [v0.2.1] - 2026-04-12

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

## [v0.2.0]

### Added
- gRPC transport with protobuf API, server implementation, and Go client
- exclusive transport selection per process via `server.protocol`
- disk-backed `VectorStore` with append-only binary payload file and compaction
- configurable in-memory hot-vector cache with TTL and byte/item limits
- Prometheus metrics for ANN behavior, cache usage, and disk-store health
- Grafana dashboard, Prometheus alert rules, and local observability compose stack
- observability validation scripts for PowerShell and Bash
- HTTP and gRPC sample load generator in `tools/loadgen`
- release-ready companion configs for HTTP and gRPC modes

### Changed
- refactored core service around `VectorStore`, `PersistenceBackend`, and stable ID resolution
- ANN search now re-scores from `VectorStore` and uses typed heaps to reduce allocations
- disk mode now treats the payload store as the source of truth instead of duplicating `snapshot + WAL`
- startup now runs exactly one transport per process, selected by configuration
- Docker, README, architecture docs, and release guidance updated for the new runtime model

### Fixed
- removed request-time hash map rebuilds from the ANN path
- hardened Docker and Grafana provisioning for the observability stack
- improved startup validation and runtime instrumentation across HTTP, gRPC, cache, and disk storage

## [v0.1.1]

### Added
- Portable coverage checker via `go run ./tools/checkcoverage`
- CI coverage enforcement for production packages with a `90%` minimum threshold
- Unit tests across production packages, colocated with package code

### Changed
- Test layout aligned with idiomatic Go package-local unit tests
- Makefile, README, and CONTRIBUTING now document the enforced coverage workflow
- Coverage checker now resolves the module root explicitly, fixing CI path issues
