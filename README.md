# LumenVec

[![CI](https://github.com/brunomarques007/lumenvec/actions/workflows/ci.yml/badge.svg)](https://github.com/brunomarques007/lumenvec/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

LumenVec is an HTTP-first vector database written in Go, built for simple deployment, batch-oriented search workloads, and iterative performance tuning.

It currently provides a Go-native core with local `snapshot + WAL` persistence, `exact` and `ann` search modes, Prometheus metrics, and Docker-ready packaging.

## Highlights
- HTTP-first API for `upsert`, `get`, `delete`, `search`, and batch operations
- Go-native core extracted from the transport layer
- Local persistence with recovery from `snapshot` and `wal`
- `exact` and `ann` search modes
- Batch ingest and batch search endpoints
- Configurable payload, dimension, and `top-k` limits
- API key authentication and IP-based rate limiting
- Prometheus metrics at `/metrics`
- Docker image and `docker compose` example

## Requirements
- Go `1.23+` for local development
- Docker and Docker Compose for container-based execution
- Linux, macOS, or Windows

## Quick Start

### Run locally with Go
```bash
git clone https://github.com/brunomarques007/lumenvec.git
cd lumenvec
go mod tidy
go run ./cmd/server
```

Default server:
- URL: `http://localhost:19190`
- Health: `http://localhost:19190/health`
- Metrics: `http://localhost:19190/metrics`

### Run with the helper script
```bash
bash scripts/run.sh
```

### Run with Docker
```bash
docker build -t lumenvec:latest .
docker run --rm -p 19190:19190 -v "$(pwd)/data:/app/data" lumenvec:latest
```

PowerShell:
```powershell
docker build -t lumenvec:latest .
docker run --rm -p 19190:19190 -v ${PWD}/data:/app/data lumenvec:latest
```

### Run with Docker Compose
```bash
docker compose up --build
```

The `docker-compose.yml` example:
- publishes port `19190`
- persists data in `./data`
- injects baseline environment variables

To stop it:
```bash
docker compose down
```

## Configuration

Default config file: `configs/config.yaml`

```yaml
server:
  port: 19190
  read_timeout: 10s
  write_timeout: 10s
  api_key: ""
  rate_limit_rps: 100

database:
  snapshot_path: "./data/snapshot.json"
  wal_path: "./data/wal.log"
  snapshot_every: 25

limits:
  max_body_bytes: 1048576
  max_vector_dim: 4096
  max_k: 100

search:
  mode: "exact"
```

Relevant fields:
- `server.port`: HTTP service port
- `server.read_timeout`: HTTP read timeout
- `server.write_timeout`: HTTP write timeout
- `server.api_key`: optional API key protecting data endpoints
- `server.rate_limit_rps`: per-IP request limit per second
- `database.snapshot_path`: snapshot file path
- `database.wal_path`: write-ahead log path
- `database.snapshot_every`: number of operations before snapshot consolidation
- `limits.max_body_bytes`: maximum HTTP payload size
- `limits.max_vector_dim`: maximum accepted vector dimension
- `limits.max_k`: maximum `k`
- `search.mode`: `exact` or `ann`

Environment variables override YAML:
- `VECTOR_DB_PORT`
- `VECTOR_DB_READ_TIMEOUT`
- `VECTOR_DB_WRITE_TIMEOUT`
- `VECTOR_DB_API_KEY`
- `VECTOR_DB_RATE_LIMIT_RPS`
- `VECTOR_DB_SNAPSHOT_PATH`
- `VECTOR_DB_WAL_PATH`
- `VECTOR_DB_SNAPSHOT_EVERY`
- `VECTOR_DB_MAX_BODY_BYTES`
- `VECTOR_DB_MAX_VECTOR_DIM`
- `VECTOR_DB_MAX_K`
- `VECTOR_DB_SEARCH_MODE`

PowerShell example:
```powershell
$env:VECTOR_DB_PORT='19200'
$env:VECTOR_DB_SEARCH_MODE='ann'
go run ./cmd/server
```

Bash example:
```bash
VECTOR_DB_PORT=19200 VECTOR_DB_SEARCH_MODE=ann go run ./cmd/server
```

## Persistence

LumenVec persists locally in two files:
- `snapshot.json`: consolidated state
- `wal.log`: recent operations not yet compacted into a snapshot

On startup it:
1. loads the snapshot
2. replays the WAL
3. writes a consolidated snapshot
4. truncates the WAL

For Docker usage, mounting `./data` as a volume is recommended.

## Security and Observability

- If `server.api_key` is set, data endpoints require:
- `X-API-Key: <key>`
- or `Authorization: Bearer <key>`
- `GET /health` and `GET /metrics` remain public
- IP rate limiting uses `server.rate_limit_rps`
- Prometheus metrics are exposed at `GET /metrics`

## HTTP API

### Health
```bash
curl http://localhost:19190/health
```

Response:
```text
ok
```

### Create a vector
`POST /vectors`

```json
{
  "id": "doc-1",
  "values": [1.0, 2.0, 3.0]
}
```

Example:
```bash
curl -X POST http://localhost:19190/vectors \
  -H "Content-Type: application/json" \
  -d '{"id":"doc-1","values":[1.0,2.0,3.0]}'
```

Responses:
- `201 Created`
- `400 Bad Request`
- `409 Conflict`

### Create vectors in batch
`POST /vectors/batch`

```json
{
  "vectors": [
    {"id": "doc-1", "values": [1.0, 2.0, 3.0]},
    {"id": "doc-2", "values": [4.0, 5.0, 6.0]}
  ]
}
```

Notes:
- the batch is atomic from the in-memory index perspective
- if any item fails validation or conflicts on ID, the whole batch is rejected

Responses:
- `201 Created`
- `400 Bad Request`
- `409 Conflict`

### Get a vector by ID
`GET /vectors/{id}`

Example:
```bash
curl http://localhost:19190/vectors/doc-1
```

Response:
```json
{
  "id": "doc-1",
  "values": [1.0, 2.0, 3.0]
}
```

### Similarity search
`POST /vectors/search`

```json
{
  "values": [1.0, 2.0, 3.1],
  "k": 2
}
```

Response:
```json
[
  {"id": "doc-1", "distance": 0.1},
  {"id": "doc-2", "distance": 0.15}
]
```

### Batch similarity search
`POST /vectors/search/batch`

```json
{
  "queries": [
    {"id": "q1", "values": [1.0, 2.0, 3.1], "k": 2},
    {"id": "q2", "values": [4.0, 5.0, 6.1], "k": 2}
  ]
}
```

Response:
```json
[
  {
    "id": "q1",
    "results": [
      {"id": "doc-1", "distance": 0.1}
    ]
  },
  {
    "id": "q2",
    "results": [
      {"id": "doc-2", "distance": 0.1}
    ]
  }
]
```

### Delete a vector
`DELETE /vectors/{id}`

Example:
```bash
curl -X DELETE http://localhost:19190/vectors/doc-1
```

Responses:
- `204 No Content`
- `404 Not Found`

## Go Client

The example HTTP client is in `pkg/client/client.go`.

Current capabilities:
- `AddVectorWithID`
- `AddVectors`
- `SearchVector`
- `SearchVectors`
- `DeleteVector`

Basic example:
```go
client := client.NewVectorClient("http://localhost:19190")
err := client.AddVectorWithID("doc-1", []float64{1, 2, 3})
```

## Docker and Compose

Files included for distribution:
- `Dockerfile`: multi-stage build and minimal runtime image
- `.dockerignore`: smaller build context
- `docker-compose.yml`: simple local orchestration

Manual image build:
```bash
docker build -t lumenvec:latest .
```

Compose:
```bash
docker compose up --build -d
docker compose logs -f
docker compose down
```

## Release

Recommended minimum release flow:
1. run `go test ./...`
2. build the image with `docker build -t lumenvec:latest .`
3. test locally with `docker compose up --build`
4. publish the image to your target registry

Docker Hub example:
```bash
docker tag lumenvec:latest brunomarques007/lumenvec:latest
docker push brunomarques007/lumenvec:latest
```

Publication checklist:
1. review the final public Docker image name
2. confirm `LICENSE` matches the intended project license
3. tag the initial release as `v0.1.0`

## Development

Run checks:
```bash
go test ./...
go vet ./...
```

Core benchmark:
```bash
go test ./internal/core -bench . -benchmem
```

Makefile shortcuts:
```bash
make test
make vet
make build
make run
make bench
```

## Project Structure

```text
lumenvec/
  .gitignore
  CONTRIBUTING.md
  CHANGELOG.md
  SECURITY.md
  LICENSE
  VERSION
  RELEASE.md
  cmd/server/main.go
  configs/config.yaml
  internal/api/server.go
  internal/core/service.go
  internal/index/index.go
  pkg/client/client.go
  Dockerfile
  docker-compose.yml
  tests/integration/api_integration_test.go
```

## Current Project State

The project currently exposes HTTP only. There is a `.proto` file in the repository, but the gRPC server is not implemented yet.

Current architectural direction:
- HTTP as the public interface and control plane
- continued core optimization for throughput
- gRPC as a later step when a higher-performance data plane is justified

## Support Files

- `CONTRIBUTING.md`: contribution flow and pre-PR checks
- `CHANGELOG.md`: summary of notable changes
- `SECURITY.md`: lightweight disclosure and hardening guidance
- `Makefile`: shortcuts for build, test, benchmarks, and Docker
- `VERSION`: current release version
- `RELEASE.md`: initial release notes
