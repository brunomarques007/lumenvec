# LumenVec

[![CI](https://github.com/brunomarques007/lumenvec/actions/workflows/ci.yml/badge.svg)](https://github.com/brunomarques007/lumenvec/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

LumenVec is a vector database written in Go, built for simple deployment, batch-oriented search workloads, and iterative performance tuning across HTTP and gRPC.

It currently provides a Go-native core with local persistence, a configurable in-memory hot-vector cache, `exact` and `ann` search modes, Prometheus metrics, exclusive HTTP or gRPC transport selection per process, and Docker-ready packaging.

## Highlights
- HTTP and gRPC APIs for `upsert`, `get`, `delete`, `search`, and batch operations
- explicit transport selection so one process runs either HTTP or gRPC, not both
- Go-native core extracted from the transport layer
- Local persistence through `snapshot + WAL` or disk-backed payload files
- Configurable in-memory cache with TTL and memory/item limits
- `exact` and `ann` search modes
- Batch ingest and batch search endpoints
- Configurable payload, dimension, and `top-k` limits
- API key authentication and IP-based rate limiting
- Prometheus metrics at `/metrics`
- Docker image and `docker compose` example

## Requirements
- Go `1.24+` for local development
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
- gRPC: `localhost:19191` when `server.protocol=grpc`

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
- publishes port `9090` for Prometheus
- publishes port `3000` for Grafana
- persists data in `./data`
- injects baseline environment variables for HTTP mode
- provisions Prometheus scraping and a Grafana dashboard

To stop it:
```bash
docker compose down
```

## Configuration

Default config file: `configs/config.yaml`

```yaml
server:
  protocol: "http"
  port: 19190
  read_timeout: 10s
  write_timeout: 10s
  api_key: ""
  rate_limit_rps: 100

database:
  snapshot_path: "./data/snapshot.json"
  wal_path: "./data/wal.log"
  snapshot_every: 25
  vector_store: "memory"
  vector_path: "./data/vectors"
  cache_enabled: false
  cache_max_bytes: 8388608
  cache_max_items: 1024
  cache_ttl: "15m"

limits:
  max_body_bytes: 1048576
  max_vector_dim: 4096
  max_k: 100

search:
  mode: "exact"
  ann_profile: "balanced"
  ann_m: 16
  ann_ef_construction: 64
  ann_ef_search: 64
  ann_eval_sample_rate: 0

grpc:
  enabled: false
  port: 19191
```

Relevant fields:
- `server.protocol`: `http` or `grpc`; exactly one transport runs per process
- `server.port`: HTTP service port
- `server.read_timeout`: HTTP read timeout
- `server.write_timeout`: HTTP write timeout
- `server.api_key`: optional API key protecting data endpoints
- `server.rate_limit_rps`: per-IP request limit per second
- `database.snapshot_path`: snapshot file path
- `database.wal_path`: write-ahead log path
- `database.snapshot_every`: number of operations before snapshot consolidation
- `database.vector_store`: payload backend, currently `memory` or `disk`
- `database.vector_path`: directory used by the disk-backed payload store
- `database.cache_enabled`: enables the in-memory vector cache
- `database.cache_max_bytes`: primary cache capacity limit
- `database.cache_max_items`: secondary cache capacity limit
- `database.cache_ttl`: optional entry TTL
- `limits.max_body_bytes`: maximum HTTP payload size
- `limits.max_vector_dim`: maximum accepted vector dimension
- `limits.max_k`: maximum `k`
- `search.mode`: `exact` or `ann`
- `search.ann_profile`: `fast`, `balanced`, or `quality`
- `search.ann_m`: max neighbor links kept per ANN node
- `search.ann_ef_construction`: ANN candidate breadth during index construction
- `search.ann_ef_search`: ANN candidate breadth during query search
- `search.ann_eval_sample_rate`: percentage of ANN searches also checked against exact search for quality metrics
- `grpc.port`: gRPC listener port
- `grpc.enabled`: derived from `server.protocol` and kept for backward compatibility in config files

Environment variables override YAML:
- `VECTOR_DB_PROTOCOL`
- `VECTOR_DB_PORT`
- `VECTOR_DB_READ_TIMEOUT`
- `VECTOR_DB_WRITE_TIMEOUT`
- `VECTOR_DB_API_KEY`
- `VECTOR_DB_RATE_LIMIT_RPS`
- `VECTOR_DB_SNAPSHOT_PATH`
- `VECTOR_DB_WAL_PATH`
- `VECTOR_DB_SNAPSHOT_EVERY`
- `VECTOR_DB_VECTOR_STORE`
- `VECTOR_DB_VECTOR_PATH`
- `VECTOR_DB_CACHE_ENABLED`
- `VECTOR_DB_CACHE_MAX_BYTES`
- `VECTOR_DB_CACHE_MAX_ITEMS`
- `VECTOR_DB_CACHE_TTL`
- `VECTOR_DB_MAX_BODY_BYTES`
- `VECTOR_DB_MAX_VECTOR_DIM`
- `VECTOR_DB_MAX_K`
- `VECTOR_DB_SEARCH_MODE`
- `VECTOR_DB_ANN_PROFILE`
- `VECTOR_DB_ANN_M`
- `VECTOR_DB_ANN_EF_CONSTRUCTION`
- `VECTOR_DB_ANN_EF_SEARCH`
- `VECTOR_DB_ANN_EVAL_SAMPLE_RATE`
- `VECTOR_DB_GRPC_PORT`
- `VECTOR_DB_GRPC_ENABLED`
- `VECTOR_DB_CONFIG`

PowerShell example:
```powershell
$env:VECTOR_DB_PROTOCOL='http'
$env:VECTOR_DB_PORT='19200'
$env:VECTOR_DB_SEARCH_MODE='ann'
go run ./cmd/server
```

Bash example:
```bash
VECTOR_DB_PROTOCOL=http VECTOR_DB_PORT=19200 VECTOR_DB_SEARCH_MODE=ann go run ./cmd/server
```

Run the binary with an explicit config file:
```bash
./lumenvec -config ./configs/config.yaml
./lumenvec -config ./configs/config.grpc.yaml
```

Companion config files:
- `configs/config.yaml`: HTTP mode
- `configs/config.grpc.yaml`: gRPC mode

## Persistence

LumenVec supports two payload persistence modes:

- `memory`: vectors live in the in-process store and durability is provided by `snapshot + WAL`
- `disk`: vectors are stored as payload files under `database.vector_path`, and startup rebuilds the exact index and ANN state from those files

`memory` mode uses:
- `snapshot.json`: consolidated state
- `wal.log`: recent operations not yet compacted into a snapshot

In `memory` mode, startup does:
1. load the snapshot
2. replay the WAL
3. write a consolidated snapshot
4. truncate the WAL

In `disk` mode:
1. load vector payloads from `database.vector_path`
2. rebuild the exact index
3. rebuild the ANN index

In `disk` mode, payload writes do not also write `snapshot + WAL`, so there is no duplicate persistence path for the same vector data.

For Docker usage, mounting `./data` as a volume is recommended.

## Security and Observability

- If `server.api_key` is set, data endpoints require:
- `X-API-Key: <key>`
- or `Authorization: Bearer <key>`
- `GET /health` and `GET /metrics` remain public
- IP rate limiting uses `server.rate_limit_rps`
- Prometheus metrics are exposed at `GET /metrics`
- Core metrics include ANN counters, cache hit/miss/eviction/bytes tracking, and disk-store file/record/compaction tracking

Important disk-store metrics:
- `lumenvec_core_disk_file_bytes`
- `lumenvec_core_disk_records`
- `lumenvec_core_disk_stale_records`
- `lumenvec_core_disk_compactions_total`

ANN runtime config metric:
- `lumenvec_core_ann_config_info{profile,m,ef_construction,ef_search}=1`

ANN quality metrics:
- `lumenvec_core_ann_eval_samples_total`
- `lumenvec_core_ann_eval_top1_matches_total`
- `lumenvec_core_ann_eval_overlap_results_total`
- `lumenvec_core_ann_eval_compared_results_total`

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

HTTP client:
- `pkg/client/client.go`

gRPC client:
- `pkg/client/grpc_client.go`

Current capabilities:
- `AddVectorWithID`
- `AddVectors`
- `GetVector`
- `SearchVector`
- `SearchVectors`
- `DeleteVector`
- `Health` on the gRPC client

Basic example:
```go
client := client.NewVectorClient("http://localhost:19190")
err := client.AddVectorWithID("doc-1", []float64{1, 2, 3})
```

gRPC example:
```go
grpcClient, err := client.NewGRPCVectorClient("localhost:19191")
if err != nil {
    panic(err)
}
defer grpcClient.Close()

err = grpcClient.AddVectorWithID("doc-1", []float64{1, 2, 3})
```

## gRPC API

Implemented RPCs:
- `Health`
- `AddVector`
- `AddVectorsBatch`
- `GetVector`
- `Search`
- `SearchBatch`
- `DeleteVector`

The protobuf definition is in `api/proto/service.proto`.

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
make compose-validate
```

Observability endpoints with the bundled compose stack:
- Prometheus: `http://localhost:9090`
- Grafana: `http://localhost:3000`
- Grafana default login: `admin` / `admin`

Validation helpers:
- Bash: `bash scripts/validate-observability.sh`
- Bash with startup: `bash scripts/validate-observability.sh --start`
- PowerShell: `powershell -ExecutionPolicy Bypass -File scripts/validate-observability.ps1`
- PowerShell with startup: `powershell -ExecutionPolicy Bypass -File scripts/validate-observability.ps1 -Start`

Generate sample traffic for dashboards:
- Go tool: `go run ./tools/loadgen`
- Makefile: `make loadgen`
- Example: `go run ./tools/loadgen --vectors 1000 --searches 500 --batch-size 200 --k 10`
- gRPC example: `go run ./tools/loadgen --transport grpc --grpc-addr localhost:19191 --vectors 1000 --searches 500`

Release packaging:
- Bash: `bash scripts/package-release.sh`
- PowerShell: `powershell -ExecutionPolicy Bypass -File scripts/package-release.ps1`
- Makefile: `make release-assets`
- Output: transport-specific archives under `dist/release`, one `http` and one `grpc` package per supported OS
- bundles include `CHANGELOG.md` instead of static release-note snapshots
- merges into `main` trigger the release workflow, which computes the next patch version from the latest tag, creates the new tag, updates the changelog through the promotion flow, and uploads the packaged assets automatically

## Release

Recommended minimum release flow:
1. run `go test ./...`
2. open or update a `feature/*` branch and let CI promote it to a draft PR against `dev`
3. merge validated changes into `dev`, then review the automated draft PR from `release/dev-to-main-vX.Y.Z` to `main`
4. merge into `main` to trigger the GitHub release workflow
5. let the workflow compute the next patch version, update `CHANGELOG.md`, and generate release notes from the commits since the previous tag
6. publish the image to your target registry if you also distribute containers

Docker Hub example:
```bash
docker tag lumenvec:latest brunomarques007/lumenvec:latest
docker push brunomarques007/lumenvec:latest
```

Publication checklist:
1. review the final public Docker image name
2. confirm `LICENSE` matches the intended project license
3. confirm the commit history between releases is ready to be turned into automated release notes and changelog entries

## Delivery Pipeline

Branch roles:
- `feature/*`: implementation branches; every push runs CI
- `dev`: integration branch promoted automatically from successful feature branches; merges into `dev` run the release-preparation workflow
- `main`: protected release branch; merges from `dev` publish the GitHub release

Workflow behavior:
- pushes to `feature/*`, `bugfix/*`, and `hotfix/*` run `.github/workflows/ci.yml`
- pull requests targeting `dev` and `main` also run `.github/workflows/ci.yml`
- successful CI runs on `feature/*` open or update a draft PR to `dev`
- pushes to `dev` run `.github/workflows/release.yml`, which computes the next patch version from the latest tag, generates release notes from commit history, and builds the release bundles
- successful runs of `.github/workflows/release.yml` on `dev` create or update a promotion branch `release/dev-to-main-vX.Y.Z`, update `CHANGELOG.md` there, and open a draft PR to `main`
- pushes to `main` run `.github/workflows/publish-release.yml`, which computes the next patch version, creates the git tag, and publishes the GitHub release with bundles rebuilt from `main`
- release asset names follow `lumenvec-vX.Y.Z-<os>-<arch>-<transport>.<ext>`

Recommended repository settings:
- create the `dev` branch before enabling the automation
- protect `dev` and `main` with required status checks from `CI`
- block direct pushes to `main`
- require review before merging the automated promotion PRs

## Development

Run checks:
```bash
go test ./...
go vet ./...
go run ./tools/checkcoverage
```

Core benchmark:
```bash
go test ./internal/core -bench . -benchmem
```

Core storage benchmark:
```bash
go test ./internal/core -run ^$ -bench "BenchmarkService(GetVectorByStore|SearchByStore)" -benchmem
```

ANN tuning benchmark:
```bash
go test ./internal/index/ann -run ^$ -bench "BenchmarkAnnSearch(Tuning)?$" -benchmem
```

Transport benchmark:
```bash
go test ./internal/api -run ^$ -bench BenchmarkTransport -benchmem
```

Initial ANN tuning guidance:
- `m=8`, `ef_construction=32`, `ef_search=32`: lower latency, lower recall headroom
- `m=16`, `ef_construction=64`, `ef_search=64`: balanced default for general use
- `m=24`, `ef_construction=96`, `ef_search=96`: higher search cost, better recall headroom for stricter quality targets

Makefile shortcuts:
```bash
make test
make vet
make build
make run
make bench
make coverage
```

Coverage policy:
- production packages must remain at or above `90%` statement coverage
- the enforced package set excludes `examples` and integration-only packages

## Project Structure

```text
lumenvec/
  .gitignore
  CONTRIBUTING.md
  CHANGELOG.md
  SECURITY.md
  LICENSE
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

The project currently exposes HTTP or gRPC over the same Go-native core service, with one transport selected per process.

Current architectural direction:
- keep HTTP as the simple public interface
- use gRPC in a dedicated process for higher-throughput internal and batch-oriented workloads
- continue evolving the hot-vector cache and ANN path for lower latency

## Support Files

- `CONTRIBUTING.md`: contribution flow and pre-PR checks
- `CHANGELOG.md`: release history updated automatically by the promotion flow
- `SECURITY.md`: lightweight disclosure and hardening guidance
- `docs/roadmap.md`: execution backlog for cache, ANN refactor, and gRPC delivery
- `docs/observability.md`: Prometheus queries, dashboard guidance, and ANN alerting notes
- `Makefile`: shortcuts for build, test, benchmarks, and Docker
