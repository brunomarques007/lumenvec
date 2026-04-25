# Architecture

## Overview

LumenVec is organized around a shared Go-native core service with two transport layers:

- HTTP as the default transport for simple operational access and broad compatibility
- gRPC as the lower-overhead option for service-to-service and batch-oriented traffic

Both transports call the same `core.Service`, so validation, persistence, cache behavior, and search semantics stay aligned. A single process runs exactly one transport, selected by configuration. The intended product posture is HTTP by default, with gRPC used when transport overhead becomes a meaningful part of end-to-end latency.

## Main Components

### Transport Layer

The transport layer lives in `internal/api`.

- `server.go` provides the HTTP server, middleware, metrics endpoint, and startup wiring
- `grpc.go` provides the gRPC server and protobuf-backed method handlers
- `server.protocol` selects which listener is started for a given process

### Core Service

The core logic lives in `internal/core/service.go`.

It is responsible for:

- vector validation
- write orchestration
- exact and ANN search dispatch
- recovery orchestration across memory-backed and disk-backed payload modes
- service-level stats used by Prometheus collectors

The core no longer depends directly on one concrete in-memory layout. It is built around explicit dependencies for payload storage, indexing, persistence, and ID resolution.

### Payload Storage

Vector payload ownership is handled by `VectorStore` implementations in `internal/core`.

Current behavior:

- a memory-backed vector store acts as the primary payload store
- a file-backed vector store can act as the persistent payload source
- an optional cache decorator keeps hot vectors in memory with:
- LRU-style ordering
- TTL support
- `max_bytes` as the primary capacity limit
- `max_items` as a secondary guardrail

The ANN re-score path now reads vector payloads from `VectorStore`, not from the exact index.

### Indexing

There are two search structures:

- exact index in `internal/index`
- ANN graph index in `internal/index/ann`

Exact search iterates the exact index.

ANN search:

- uses stable internal IDs
- gets candidate IDs from the ANN graph
- fetches only candidate payloads from `VectorStore`
- computes final distances and orders the results

### Persistence

Persistence is split across two modes:

- memory-backed payloads use `PersistenceBackend` in `internal/core/persistence.go`
- disk-backed payloads use `VectorStore` itself as the payload source of truth

The default `PersistenceBackend` implementation uses:

- `snapshot.json` for compacted state
- `wal.log` for recent operations

Startup recovery in `memory` mode:

1. load snapshot
2. replay WAL
3. write a fresh snapshot
4. truncate WAL

Startup recovery in `disk` mode:

1. list payloads from the disk-backed `VectorStore`
2. rebuild the exact index
3. rebuild stable ID mappings
4. rebuild the ANN graph

### Configuration

Configuration is loaded from `configs/config.yaml` and optional environment overrides in `internal/config/config.go`.

Current operational knobs include:

- transport protocol selection
- HTTP port and timeouts
- gRPC port
- payload size, dimension, and `top-k` limits
- search mode
- ANN graph tuning parameters
- cache size and TTL
- snapshot/WAL paths for `memory` mode
- payload store mode and payload directory for `disk` mode

## Request Flow

### Write Path

1. transport validates payload format
2. `core.Service` validates semantic constraints
3. payload is written to `VectorStore`
4. exact index is updated
5. ANN index is updated
6. in `memory` mode, a WAL entry is appended
7. in `memory` mode, snapshot compaction runs when the configured threshold is reached

### Exact Search Path

1. transport calls `core.Service`
2. exact index iterates current vectors
3. distances are computed directly
4. top-k results are returned

### ANN Search Path

1. transport calls `core.Service`
2. ANN index returns candidate internal IDs
3. IDs are resolved through `IDResolver`
4. payloads are loaded from `VectorStore`
5. exact distances are recomputed for the candidate set
6. top-k results are returned
7. exact search is used as fallback if ANN yields no usable result

## Observability

Prometheus metrics are exposed at `/metrics`.

Current metrics include:

- HTTP request totals and duration
- ANN attempts, hits, fallbacks, errors, and candidate counts
- cache hits, misses, evictions, item count, and approximate bytes
- disk-store file bytes, live records, stale records, and compaction count

## Current Constraints

- the default payload store is still memory-backed
- the disk-backed store is a simple append-only binary file with periodic compaction, not yet an optimized large-scale backend
- gRPC support currently covers unary request/response methods; streaming is not implemented

## Direction

The current architecture is ready for the next stage:

- replace or augment the in-memory payload store with a disk-first backend
- evolve cache admission and memory accounting further
- add gRPC performance tuning and optional streaming APIs
