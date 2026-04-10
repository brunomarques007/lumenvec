# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog.

## [Unreleased]

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
