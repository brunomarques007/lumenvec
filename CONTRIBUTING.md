# Contributing

## Development Setup

1. Install Go `1.23+`.
2. Clone the repository.
3. Run:

```bash
go mod tidy
go test ./...
```

## Project Conventions

- Keep changes focused and small.
- Prefer preserving the current API unless the change explicitly targets API evolution.
- Update `README.md` when behavior, configuration, or public endpoints change.
- Add or update tests for behavior changes.

## Before Opening a PR

Run:

```bash
go test ./...
go vet ./...
go run ./tools/checkcoverage
```

If you change core search or ingest behavior, also run:

```bash
go test ./internal/core -bench . -benchmem
```

## Coverage Policy

Production packages must keep at least `90%` statement coverage.

The enforced package set is checked by:

```bash
go run ./tools/checkcoverage
```

Examples and integration-only packages are not part of this threshold.

## Pull Requests

- Describe the problem and the chosen approach.
- Mention any API, config, persistence, or benchmark impact.
- Keep unrelated refactors out of the same PR.
