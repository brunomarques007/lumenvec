# Technical Roadmap

## Goal

This roadmap turns the planned improvements for LumenVec into an execution backlog. It focuses on four outcomes:

- reduce search-path overhead in the current core
- prepare the storage model for hot/cold data management with memory cache
- keep HTTP as the default transport while adding and tuning gRPC as a higher-throughput option without splitting business logic
- make LumenVec operationally ready for cloud and managed-service environments

## Current State

The current implementation is still memory-first:

- the exact index keeps the full vector set in memory
- the ANN index also keeps graph nodes and vector payloads in memory
- durability is provided by `snapshot + WAL` in `memory` mode or file-backed payload storage in `disk` mode
- HTTP and gRPC are both active transports
- the first gRPC server and client path are implemented

That means memory cache should not be the first change. The first step is to remove avoidable overhead from the current hot path and separate storage concerns so cache becomes useful instead of redundant.

## Design Principles

- disk should be the source of truth for persisted vector payloads
- memory should hold the working set, not an uncontrolled duplicate of all data
- cache lookup should be `memory first`, then `store fallback`; not simultaneous reads
- HTTP and gRPC must reuse the same core service
- HTTP should remain the default public transport unless there is an explicit product decision to change it
- the public HTTP surface should evolve into a formally versioned REST API with OpenAPI, stable error shapes, TLS/auth guidance, and cloud-provider integration notes
- all performance work must be measured with benchmarks and exposed through metrics

## Delivery Phases

### Phase 1: Baseline and Hot Path

Objective:
Establish measurements and fix obvious inefficiencies in the current search path.

Work items:

- add benchmarks for `AddVector`, `GetVector`, `Search`, `SearchBatch`, and ANN search
- add metrics for search latency, candidate counts, and ANN fallback behavior
- profile allocations in `internal/core/service.go`
- remove per-request full-map reconstruction in ANN search
- replace string-hash lookup shortcuts with a stable internal identifier design

Target files:

- `internal/core/service.go`
- `internal/core/service_bench_test.go`
- `internal/index/ann/ann_index.go`
- `internal/api/server.go`

Acceptance criteria:

- benchmark baseline is committed
- ANN search no longer rebuilds a full `hash -> vector` map on every request
- metrics expose the current hot-path behavior

Risks:

- current `hashID` mapping can collide
- hot-path changes can affect test expectations if ordering is too strict

### Phase 2: Storage and Ownership Refactor

Objective:
Separate vector payload persistence, in-memory indexing, and identifier resolution.

Work items:

- introduce a stable internal ID type
- add interfaces for vector persistence, cache, and ID resolution
- move `Service` to depend on interfaces instead of concrete in-memory structures
- define storage ownership rules for vector payload copies versus shared read-only slices
- decide whether snapshot/WAL remains the primary persistence layer or becomes an implementation detail of a store backend

Proposed interfaces:

- `VectorStore`
- `VectorCache`
- `IDResolver`

Target files:

- `internal/core/service.go`
- `internal/storage/store.go`
- `internal/storage/leveldb_store.go`
- `internal/index/index.go`

Acceptance criteria:

- service can be constructed from interfaces
- vector payload persistence is no longer tightly coupled to the in-memory index
- delete and restore paths still pass existing tests

Status:

- completed for `VectorStore`, `IDResolver`, `PersistenceBackend`, disk-backed payload storage, and mode-aware recovery
- follow-up work should target a more efficient disk backend than file-per-vector JSON

### Phase 3: Configurable Memory Cache

Objective:
Introduce a cache for hot vectors with explicit memory limits and measurable behavior.

Initial policy:

- primary limit by memory usage
- optional secondary limit by item count
- LRU eviction first
- TTL as a complement, not the main policy

Configuration to add:

```yaml
cache:
  enabled: true
  max_bytes: 536870912
  max_items: 100000
  ttl: 15m
  warm_on_start: false
  eviction_policy: "lru"
```

Environment variables to add:

- `VECTOR_DB_CACHE_ENABLED`
- `VECTOR_DB_CACHE_MAX_BYTES`
- `VECTOR_DB_CACHE_MAX_ITEMS`
- `VECTOR_DB_CACHE_TTL`
- `VECTOR_DB_CACHE_WARM_ON_START`
- `VECTOR_DB_CACHE_EVICTION_POLICY`

Read path:

1. lookup vector payload in cache
2. on miss, load from store
3. promote loaded payload into cache
4. return immutable or cloned payload according to ownership rules

Write path:

1. persist to store
2. update indexes
3. populate or invalidate cache entry

Delete path:

1. remove from store
2. remove from indexes
3. evict cache entry

Target files:

- `internal/config/config.go`
- `configs/config.yaml`
- `internal/core/service.go`
- new cache package under `internal/storage` or `internal/cache`

Acceptance criteria:

- cache is optional and disabled by default
- cache honors configured limits
- hit and miss metrics are exposed
- repeated-read benchmark shows a measurable gain

Risks:

- hidden memory growth if accounting is imprecise
- lock contention if the cache is implemented with coarse locking

### Phase 4: ANN Alignment with Hybrid Storage

Objective:
Make ANN compatible with a hot/cold model without forcing full payload scans.

Work items:

- make ANN nodes use stable internal IDs instead of hashed external IDs
- keep ANN graph metadata in memory
- fetch only candidate payloads for re-scoring, not the full vector set
- make ANN parameters configurable through config
- decide whether ANN state is rebuilt on startup or serialized as part of snapshots

Configuration to add:

```yaml
search:
  mode: "ann"
  ann:
    m: 16
    ef_construction: 64
    ef_search: 64
```

Target files:

- `internal/index/ann/ann_index.go`
- `internal/core/service.go`
- `internal/config/config.go`

Acceptance criteria:

- ANN search re-scores only candidate vectors
- ANN no longer depends on request-time full-dataset reconstruction
- ANN parameters are externally configurable

Risks:

- delete/update operations may become expensive if graph maintenance stays naive
- startup rebuild may become too slow for larger datasets

### Phase 5: gRPC Transport

Objective:
Add a higher-throughput transport layer while preserving a single core service.

Work items:

- expand the proto definition to cover batch and get operations
- change vector fields to `double` if core keeps `[]float64`
- generate Go stubs
- implement a gRPC server package
- wire HTTP and gRPC to the same `core.Service`
- add a gRPC client alongside the current HTTP client

Recommended RPC surface:

- `AddVector`
- `AddVectorsBatch`
- `GetVector`
- `DeleteVector`
- `Search`
- `SearchBatch`
- `Health`

Configuration to add:

```yaml
grpc:
  enabled: true
  port: 19191
  max_recv_msg_size: 16777216
  max_send_msg_size: 16777216
```

Environment variables to add:

- `VECTOR_DB_GRPC_ENABLED`
- `VECTOR_DB_GRPC_PORT`
- `VECTOR_DB_GRPC_MAX_RECV_MSG_SIZE`
- `VECTOR_DB_GRPC_MAX_SEND_MSG_SIZE`

Target files:

- `api/proto/service.proto`
- `cmd/server/main.go`
- `internal/config/config.go`
- new transport package under `internal/api/grpc`
- `pkg/client`

Acceptance criteria:

- HTTP and gRPC run from the same binary
- both transports pass equivalent integration tests
- benchmark comparison exists for HTTP versus gRPC on single and batch search

Status:

- completed for baseline unary and batch operations
- follow-up work should focus on transport benchmarks, rollout docs, and optional streaming APIs

Risks:

- schema drift if validation rules diverge between transports
- operational complexity from running two listeners

### Phase 6: Hardening and Documentation

Objective:
Prepare the system for maintainability and production-like behavior.

Work items:

- add race-condition coverage to CI where practical
- add restart and crash-recovery tests
- add benchmarks for warm-cache and cold-cache scenarios
- document new configuration and transport behavior
- formalize the HTTP API as a public REST contract: versioned routes, OpenAPI spec, standard status codes, consistent error bodies, authentication headers, TLS requirements, and compatibility notes for managed cloud platforms
- update architecture documentation to match the real implementation
- define rollout notes for mixed HTTP and gRPC deployments

Target files:

- `README.md`
- `docs/architecture.md`
- `docs/design.md`
- CI workflow files

Acceptance criteria:

- docs match the implemented architecture
- recovery behavior is covered by tests
- new features are discoverable from README and config examples

### Phase 7: Cloud Readiness

Objective:
Make LumenVec suitable for cloud deployment, managed platform integration, and repeatable production operations without weakening the current search performance focus.

Principles:

- search performance and recall remain the primary product advantage
- cloud readiness should improve operability, security, and integration without changing ranking behavior
- all externally visible APIs must be versioned and documented before being treated as stable
- container and orchestration defaults must be safe for non-root, persistent-volume deployments

Work items:

- formalize the public HTTP API as REST under versioned routes such as `/v1/vectors`
- add an OpenAPI specification for HTTP routes, request bodies, response bodies, status codes, and error envelopes
- define stable error response shapes for HTTP and stable status mapping for gRPC
- split health checks into liveness and readiness endpoints suitable for Kubernetes and managed platforms
- document production TLS termination patterns: in-process TLS, reverse proxy TLS, and service mesh TLS
- document authentication expectations for cloud stacks, including API key headers and future token-based auth
- add structured JSON logging with request IDs and operation-level fields
- expose operational metrics for readiness, storage state, persistence failures, WAL/snapshot activity, and request limits
- add Kubernetes deployment examples for single-node persistent volume deployments
- document persistent volume ownership, non-root container behavior, backup, restore, and disaster-recovery expectations
- add graceful shutdown behavior documentation and tests for in-flight HTTP/gRPC requests
- document cloud-provider integration notes for platforms that expect REST/OpenAPI, health checks, metrics, TLS, and stateless container conventions

Target files:

- `internal/api/server.go`
- `internal/api/middleware.go`
- `api/proto/service.proto`
- `configs/config.yaml`
- `Dockerfile`
- `README.md`
- `docs/api.md`
- `docs/operations.md`
- `docs/cloud.md`
- Kubernetes examples under `deploy/`

Acceptance criteria:

- HTTP API has a checked-in OpenAPI spec
- liveness and readiness are separate and tested
- API errors are consistent and documented
- Docker image runs as non-root with documented persistent-volume permissions
- Prometheus metrics cover API, core search, ANN, cache, disk store, and persistence health
- a minimal Kubernetes manifest can run LumenVec with a persistent volume
- backup and restore are documented and tested at least at single-node level
- graceful shutdown is tested for HTTP and gRPC
- cloud readiness work does not regress search benchmarks

Risks:

- premature multi-node design could distract from single-node correctness
- API versioning can create compatibility burden if introduced before error and payload shapes are stable
- cloud deployment examples can become stale if not validated in CI or a reproducible local cluster

## Execution Order

Recommended order:

1. Phase 1
2. Phase 2
3. Phase 3
4. Phase 4
5. Phase 5
6. Phase 6
7. Phase 7

gRPC can start in parallel after Phase 1 if transport work must begin early, but storage and ANN refactors still have higher impact on end-to-end performance.

Cloud readiness can start in parallel after the public API shape is stable enough to document. It should begin with contracts, health checks, observability, and deployment packaging before any multi-node or distributed-storage work.

## Backlog by Milestone

### Milestone A: Core performance baseline

- benchmark current exact and ANN search behavior
- add search metrics
- remove ANN request-time full-map rebuild
- introduce stable internal IDs

### Milestone B: Data layer refactor

- define `VectorStore`, `VectorCache`, and `IDResolver`
- refactor `Service` construction and ownership boundaries
- adapt restore and delete flows to the new model

### Milestone C: Hot vector cache

- implement LRU cache with memory accounting
- add config parsing and defaults
- add hit/miss metrics
- benchmark repeated reads and ANN candidate fetches

### Milestone D: ANN modernization

- switch ANN to internal IDs
- re-score only ANN candidates
- expose ANN config
- decide on rebuild versus persisted ANN metadata

### Milestone E: gRPC delivery

- expand proto
- generate code
- implement gRPC server
- add gRPC client
- benchmark HTTP versus gRPC

### Milestone F: Hardening

- recovery and race tests
- documentation updates
- operational rollout notes
- REST API contract and cloud integration guide

### Milestone G: External vector database benchmark

- document benchmark methodology and fairness rules
- build deterministic dataset and query generation
- implement LumenVec exact and ANN benchmark adapters
- add a ground-truth path for recall calculation
- add Qdrant and pgvector Docker-based comparisons; Qdrant plus pgvector exact, HNSW, and IVFFlat are implemented
- add Faiss as an in-process baseline
- generate JSON, CSV, and Markdown reports
- add named benchmark presets such as `smoke`, `local-10k`, `searchbatch`, and focused engine subsets
- generate automatic summary reports with recall-filtered rankings for ingest, search QPS, batch-search QPS, memory, and disk
- add baseline comparison and regression detection for repeated local runs
- separate publishable result snapshots from diagnostic and temporary benchmark outputs
- extend later to Milvus and a separately labeled Pinecone managed-service run; Weaviate and Chroma Docker adapters are implemented

### Milestone H: Cloud-ready single-node deployment

- define `/v1` REST API contract and OpenAPI spec
- standardize HTTP and gRPC error contracts
- split health endpoints into liveness and readiness
- add structured request logging and request IDs
- document TLS/auth deployment patterns for cloud environments
- add Kubernetes manifests for single-node persistent-volume deployment
- document non-root volume ownership, backup, restore, and graceful shutdown
- validate that cloud packaging changes do not regress benchmark search results

## Definition of Done

A phase is done only when all of the following are true:

- tests pass
- relevant benchmarks exist and are checked in
- configuration is documented
- metrics expose the new behavior
- code paths are shared between HTTP and gRPC where applicable

## Suggested First Implementation Slice

The highest-value first slice is:

1. benchmark and profile the current service
2. remove ANN request-time full-map reconstruction
3. replace hash-based external ID lookup with stable internal IDs

That slice improves the current code immediately and creates the foundation needed for both cache and gRPC work.
