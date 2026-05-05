# LumenVec

[![CI](https://github.com/brunomarques007/lumenvec/actions/workflows/ci.yml/badge.svg)](https://github.com/brunomarques007/lumenvec/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

LumenVec is a small vector database written in Go. It provides HTTP or gRPC APIs over the same core service, local persistence, exact and ANN search, optional hot-vector caching, Prometheus metrics, and Docker packaging.

## Features

- HTTP and gRPC transports, with one active transport per process
- Vector create, batch create, get, list, search, batch search, and delete
- Exact search and ANN search profiles: `fast`, `balanced`, `quality`
- Persistence through `snapshot + WAL` or disk-backed vector payloads
- Optional in-memory cache with TTL, item, and byte limits
- API key auth, trusted proxy handling, rate limiting, and TLS support
- Prometheus metrics for HTTP, core search, ANN quality, cache, and disk store state

## Requirements

- Go `1.24+`
- Docker and Docker Compose for container workflows

## Quick Start

```bash
git clone https://github.com/brunomarques007/lumenvec.git
cd lumenvec
go mod tidy
go run ./cmd/server
```

Default endpoints:

- HTTP: `http://localhost:19190`
- Health: `http://localhost:19190/health`
- Metrics: `http://localhost:19190/metrics`
- gRPC: `localhost:19191` when `server.protocol=grpc`

Docker:

```bash
docker build -t lumenvec:latest .
docker run --rm -p 19190:19190 -p 19191:19191 -v "$(pwd)/data:/data" lumenvec:latest
```

Compose with Prometheus and Grafana:

```bash
docker compose up --build
```

Kubernetes single-node examples are available in [deploy/kubernetes](deploy/kubernetes). Cloud deployment notes are in [docs/cloud.md](docs/cloud.md).

## Configuration

Default config: [configs/config.yaml](configs/config.yaml)

gRPC config: [configs/config.grpc.yaml](configs/config.grpc.yaml)

Useful fields:

- `server.protocol`: `http` or `grpc`
- `server.port`: HTTP port
- `grpc.port`: gRPC port
- `database.vector_store`: `memory` or `disk`
- `database.vector_path`: disk payload directory
- `database.sync_every`: fsync grouping for disk-backed writes
- `database.cache_enabled`: enables hot-vector caching
- `limits.max_body_bytes`, `limits.max_vector_dim`, `limits.max_k`: request limits
- `search.mode`: `exact` or `ann`
- `search.ann_profile`: `fast`, `balanced`, or `quality`
- `security.auth.*`: HTTP and gRPC API key settings
- `security.transport.*`: TLS settings
- `security.proxy.*`: trusted `X-Forwarded-For` handling
- `security.storage.*`: stricter file and directory permissions

Environment variables override YAML. Common examples:

```bash
VECTOR_DB_PROTOCOL=http VECTOR_DB_PORT=19200 VECTOR_DB_SEARCH_MODE=ann go run ./cmd/server
```

```powershell
$env:VECTOR_DB_PROTOCOL='grpc'
$env:VECTOR_DB_GRPC_PORT='19191'
go run ./cmd/server
```

## HTTP API

Create a vector:

```bash
curl -X POST http://localhost:19190/vectors \
  -H "Content-Type: application/json" \
  -d '{"id":"doc-1","values":[1.0,2.0,3.0]}'
```

Search:

```bash
curl -X POST http://localhost:19190/vectors/search \
  -H "Content-Type: application/json" \
  -d '{"values":[1.0,2.0,3.1],"k":2}'
```

List with pagination:

```bash
curl "http://localhost:19190/vectors?limit=100"
curl "http://localhost:19190/vectors?cursor=<next_cursor>&ids_only=true"
```

Endpoints:

- `GET /health`
- `GET /livez`
- `GET /readyz`
- `GET /metrics`
- `GET /vectors?limit=&cursor=&after=&ids_only=`
- `POST /vectors`
- `POST /vectors/batch`
- `GET /vectors/{id}`
- `DELETE /vectors/{id}`
- `POST /vectors/search`
- `POST /vectors/search/batch`

## Go Clients

HTTP:

```go
c := client.NewVectorClient("http://localhost:19190")
err := c.AddVectorWithID("doc-1", []float64{1, 2, 3})
```

gRPC:

```go
c, err := client.NewGRPCVectorClient("localhost:19191")
if err != nil {
    panic(err)
}
defer c.Close()

err = c.AddVectorWithID("doc-1", []float64{1, 2, 3})
```

The protobuf definition lives in [api/proto/service.proto](api/proto/service.proto).

## Development

```bash
go test ./...
go vet ./...
go run ./tools/checkcoverage
```

Useful Make targets:

```bash
make test
make vet
make build
make run
make coverage
make loadgen
```

Benchmarks:

```bash
go test ./internal/core -bench . -benchmem
go test ./internal/api -run ^$ -bench BenchmarkTransport -benchmem
go test ./internal/index/ann -run ^$ -bench "BenchmarkAnnSearch(Tuning)?$" -benchmem
```

External benchmark planning:

- [benchmarks/README.md](benchmarks/README.md)
- [benchmarks/methodology.md](benchmarks/methodology.md)
- [benchmarks/result_schema.md](benchmarks/result_schema.md)

More details:

- [docs/architecture.md](docs/architecture.md)
- [docs/api.md](docs/api.md)
- [docs/cloud.md](docs/cloud.md)
- [docs/design.md](docs/design.md)
- [docs/cloud-readiness-plan.md](docs/cloud-readiness-plan.md)
- [docs/observability.md](docs/observability.md)
- [docs/operations.md](docs/operations.md)
- [docs/testing.md](docs/testing.md)
- [docs/roadmap.md](docs/roadmap.md)
- [CONTRIBUTING.md](CONTRIBUTING.md)
- [SECURITY.md](SECURITY.md)
