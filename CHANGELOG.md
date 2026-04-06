# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog.

## [Unreleased]

### Added
- HTTP batch ingest endpoint: `POST /vectors/batch`
- HTTP batch search endpoint: `POST /vectors/search/batch`
- Docker packaging via `Dockerfile`
- Local container orchestration via `docker-compose.yml`
- Benchmarks for core ingest and search paths
- Publication support files: `LICENSE`, `.gitignore`, `CONTRIBUTING.md`, `SECURITY.md`

### Changed
- Core logic extracted from HTTP transport into `internal/core`
- Exact batch search now uses a single index scan with incremental top-k accumulation
- README expanded to cover setup, Docker, configuration, API, persistence, and publication flow

