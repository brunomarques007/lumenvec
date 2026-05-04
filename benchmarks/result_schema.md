# Benchmark Result Schema

The first benchmark runner should emit one JSON object per scenario.

## Top-Level Shape

```json
{
  "run_id": "20260425-143000-local-100k-384",
  "git_commit": "abcdef123",
  "started_at": "2026-04-25T14:30:00Z",
  "ended_at": "2026-04-25T14:45:00Z",
  "environment": {},
  "dataset": {},
  "engine": {},
  "workload": {},
  "ingest": {},
  "index_build": {},
  "search": {},
  "recall": {},
  "batch_search": {},
  "batch_recall": {},
  "resources": {},
  "errors": []
}
```

The current runner emits this shape from `benchmarks/runner`. For Docker engines, CPU, memory, and disk are collected from the matching container and Docker-managed volume. For in-process smoke runs, resource values are limited to Go runtime memory unless a separate collector is added.

## Environment

```json
{
  "os": "windows",
  "cpu_model": "example cpu",
  "cpu_cores": 16,
  "memory_bytes": 34359738368,
  "go_version": "go1.24.x",
  "docker_version": "28.x",
  "machine_id": "local"
}
```

## Dataset

```json
{
  "name": "synthetic-uniform",
  "vector_count": 100000,
  "dimension": 384,
  "query_count": 1000,
  "seed": 42,
  "distance_metric": "l2"
}
```

## Engine

```json
{
  "name": "lumenvec",
  "version": "git",
  "profile": "ann-balanced",
  "transport": "grpc",
  "config": {
    "search_mode": "ann",
    "ann_profile": "balanced",
    "batch_size": 1000,
    "vector_id_prefix": "vec"
  }
}
```

## Workload

```json
{
  "batch_size": 1000,
  "search_batch_size": 100,
  "search_concurrency": 16,
  "k": 10,
  "warmup_queries": 1000,
  "measured_queries": 10000
}
```

## Ingest

```json
{
  "total_vectors": 100000,
  "total_duration_ms": 12345.6,
  "vectors_per_second": 8100.2,
  "batch_latency_ms": {
    "min": 1.2,
    "mean": 4.5,
    "p50": 4.1,
    "p95": 7.8,
    "p99": 10.2,
    "max": 20.0
  }
}
```

## Index Build

```json
{
  "built": true,
  "total_duration_ms": 1234.5
}
```

`built` is `true` when the runner executed a distinct index-build phase after ingest and before warmup. Engines that build indexes during ingest or do not expose a separate build step report `built: false` and `total_duration_ms: 0`.

## Search

```json
{
  "total_queries": 10000,
  "total_duration_ms": 4567.8,
  "queries_per_second": 2189.2,
  "latency_ms": {
    "min": 0.4,
    "mean": 2.1,
    "p50": 1.8,
    "p95": 4.9,
    "p99": 8.6,
    "max": 30.0
  }
}
```

## Recall

```json
{
  "recall_at_1": 0.98,
  "recall_at_5": 0.96,
  "recall_at_10": 0.95
}
```

## Batch Search

```json
{
  "supported": true,
  "batch_size": 100,
  "total_queries": 10000,
  "total_batches": 100,
  "total_duration_ms": 1234.5,
  "queries_per_second": 8100.2,
  "batch_latency_ms": {
    "min": 1.2,
    "mean": 4.5,
    "p50": 4.1,
    "p95": 7.8,
    "p99": 10.2,
    "max": 20.0
  }
}
```

`batch_search` measures native batch-query APIs. Engines without a real batch-search adapter report `supported: false`. `batch_latency_ms` is latency per batch request, not per individual query.

`batch_recall` uses the same shape as `recall`, computed from batch-search results when supported.

## Resources

```json
{
  "peak_memory_bytes": 2147483648,
  "average_memory_bytes": 1879048192,
  "peak_cpu_percent": 380.0,
  "average_cpu_percent": 240.0,
  "disk_bytes": 1073741824,
  "startup_ms": 1200.0,
  "restart_recovery_ms": 1800.0
}
```

For Docker engines, `peak_memory_bytes`, `average_memory_bytes`, `peak_cpu_percent`, and `average_cpu_percent` come from repeated `docker stats --no-stream` samples collected during ingest, explicit index build, warmup, measured single-query search, and measured batch search. `disk_bytes` is measured from the Docker volume after the run.

## Errors

```json
[
  {
    "phase": "search",
    "count": 3,
    "message": "deadline exceeded"
  }
]
```

The schema can grow, but existing fields should remain stable once reports are published.
