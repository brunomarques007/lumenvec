# Design Notes

## Core Design

The current design separates four concerns inside the core:

- payload ownership through `VectorStore`
- exact lookup/search structure through `VectorIndex`
- ANN graph traversal through `AnnIndex`
- durability through `PersistenceBackend`

This separation exists to make future disk-first storage and cache policies possible without rewriting transport or search dispatch logic.

## Stable IDs

ANN uses stable internal integer IDs instead of hashing external string IDs on each request.

Why:

- avoids request-time rebuild of `hash -> vector` maps
- removes hash-collision risk from the query path
- lets ANN candidate traversal stay compact while external IDs remain string-based

## Payload Store and Cache

Vector payloads are not treated as an implementation detail of the exact index anymore.

Current design:

- `memoryVectorStore` stores canonical in-process payloads
- `fileVectorStore` stores canonical payloads on disk
- `cachedVectorStore` decorates a backend store
- cache capacity is controlled primarily by approximate bytes
- `max_items` remains a secondary guardrail
- TTL is optional and complements capacity-based eviction

This means:

- `GetVector` reads from the payload store
- snapshots are produced from the payload store only in `memory` mode
- ANN re-score reads payload from the payload store

## Exact Search

Exact search still uses the exact index for full scans.

That remains acceptable for the current in-memory design, but it is not the end state if datasets are expected to exceed RAM. The design now keeps that concern isolated so the exact path can evolve independently.

## ANN Search

The ANN path is intentionally two-stage:

1. candidate generation from the ANN graph
2. final re-score on real payload vectors

The important design shift is that stage 2 now uses `VectorStore`, which lets cache policy influence ANN latency directly and keeps ANN independent from exact-index payload ownership.

## Persistence

Durability is now mode-dependent:

- `memory` mode uses `PersistenceBackend`
- `disk` mode uses the disk-backed `VectorStore` as the persisted payload source

The default `PersistenceBackend` still uses `snapshot + WAL` because it is simple and testable:

- snapshots provide compact restart state
- WAL preserves recent writes between compactions

In `disk` mode, the service avoids duplicating payload writes into `snapshot + WAL` and rebuilds in-memory search state from the payload files on startup.

## Transport Reuse

HTTP and gRPC both call the same `core.Service`.

That is a deliberate design requirement:

- avoids duplicated validation logic
- keeps cache and persistence semantics aligned
- makes performance comparisons about transport overhead more meaningful

Transport posture:

- HTTP stays the default connection mode because it is simpler to integrate and operate
- gRPC stays available as the performance-oriented option when lower transport overhead matters

## Observability

Metrics are designed around behavior, not just request counts.

Important examples:

- ANN hit/fallback/error/candidate metrics
- cache hit/miss/eviction/item/byte metrics

This makes it possible to tune cache and ANN behavior with real feedback instead of intuition alone.

## Current Trade-offs

- the payload store is still memory-backed by default
- the current disk-backed store is intentionally simple and not yet optimized for large datasets
- the cache is useful today, but its biggest value comes once the backing store stops being memory-only
- gRPC is implemented, but transport-level throughput tuning is still at an early stage

## Near-Term Design Priorities

- harden the dual-listener startup and operational model
- continue transport benchmarking for HTTP versus gRPC
- prepare a disk-first payload store behind `VectorStore`
- keep ANN and cache behavior measurable as those backends evolve
