# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog.

## [Unreleased]

## [v0.1.1]

### Added
- Portable coverage checker via `go run ./tools/checkcoverage`
- CI coverage enforcement for production packages with a `90%` minimum threshold
- Unit tests across production packages, colocated with package code

### Changed
- Test layout aligned with idiomatic Go package-local unit tests
- Makefile, README, and CONTRIBUTING now document the enforced coverage workflow
- Coverage checker now resolves the module root explicitly, fixing CI path issues
