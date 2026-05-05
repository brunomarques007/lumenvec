# LumenVec Benchmark Plan

This directory defines how LumenVec will be benchmarked against other vector databases. The goal is to produce repeatable, fair, and inspectable results instead of one-off numbers.

## Scope

Initial benchmark targets:

- LumenVec HTTP `exact` running in Docker
- LumenVec HTTP `ann` profiles running in Docker
- LumenVec gRPC `exact` and `ann` profiles running in Docker
- Qdrant
- pgvector exact and ANN profiles running in Docker
- Chroma running in Docker
- Weaviate running in Docker

Later benchmark targets:

- Faiss, as an in-process exact/ANN baseline
- Milvus
- Pinecone, as a separate managed-service comparison

Pinecone should not be mixed into the first local benchmark table because cloud networking, service tier, region, and billing limits can dominate the measurement. It can be added later under a dedicated managed-service section.

## Benchmark Questions

The benchmark should answer:

- How fast can each engine ingest vectors?
- How fast can each engine answer similarity queries?
- What latency does each engine show at p50, p95, and p99?
- How much recall does each ANN configuration preserve against exact search?
- How much CPU, memory, and disk does each engine consume?
- How long does each engine take to start or recover after restart?
- Which operational tradeoffs are visible from setup, tuning, and runtime behavior?

## Datasets

Use deterministic synthetic datasets first. They are easier to reproduce and debug.

Initial sizes:

- `10k` vectors for the current publishable local baseline on this machine
- up to `100k` vectors for larger diagnostics when hardware allows
- larger runs should be treated as future stress tests, not part of the default local baseline

Initial dimensions:

- `128`
- `384`
- `768`
- optional `1536` for high-dimensional embedding scenarios

Query sets:

- use deterministic query generation from a fixed seed
- generate enough queries to stabilize p95 and p99 latency
- keep query vectors separate from inserted vectors unless the scenario explicitly measures exact-match lookup behavior

## Metrics

Every run should capture:

- ingest throughput: vectors per second
- ingest latency: p50, p95, p99 per batch
- search throughput: queries per second
- search latency: p50, p95, p99
- recall: `recall@1`, `recall@5`, `recall@10`
- error count and error rate
- process memory peak and average
- CPU peak and average
- final disk size
- startup or restart recovery time

For ANN engines, latency without recall is not sufficient. ANN results must always be reported with the matching recall value.

## Workloads

Each engine should run the same lifecycle:

1. start the service or initialize the in-process engine
2. create the collection, table, or index
3. ingest vectors using fixed batch sizes
4. run warmup queries
5. run measured searches
6. optionally delete a fixed percentage of vectors
7. restart the service when supported
8. run post-restart searches
9. export metrics and raw results
10. tear down the service and data directory

Initial batch sizes:

- `100`
- `500`
- `1000`

Initial search concurrency:

- `1`
- `4`
- `16`
- `64`

Initial `k` values:

- `1`
- `10`
- `100`

## Fairness Rules

The benchmark must document:

- machine model, CPU, RAM, disk, and operating system
- Go version
- Docker version, when Docker is used
- exact database versions or image tags
- vector count, dimension, seed, batch size, concurrency, and `k`
- distance metric
- index configuration
- whether the result is default configuration or tuned configuration
- whether data was warm or cold

Rules:

- use the same vectors and queries for every engine in the same run
- use the same distance metric for every engine in a table
- run comparable database engines under the same isolation model; the primary comparison uses Docker services for LumenVec, Qdrant, pgvector, Weaviate, Milvus, and Chroma
- use in-process adapters only for smoke tests, implementation profiling, or ground-truth baselines; do not mix them into Docker service comparison tables
- do not compare ANN latency without recall
- separate default-config results from tuned-config results
- separate local self-hosted results from managed cloud results
- keep raw JSON or CSV output locally so tables can be regenerated

## Output

The benchmark runner should write:

- `benchmarks/results/<run-name>/*.json` for raw structured results
- `benchmarks/results/<run-name>/aggregate.csv` for spreadsheet analysis
- `benchmarks/results/<run-name>/report.md` for the human-readable report
- `benchmarks/results/<run-name>/charts/*.svg` for generated visual summaries

Generated outputs under `benchmarks/results/` and `benchmarks/baselines/` are ignored by Git. Commit the runner, methodology, result schema, and reproducible commands, not local result files.

The report should include:

- environment summary
- dataset summary
- per-engine configuration
- ingest table
- search table
- recall table
- resource table
- notes about failures, skipped cases, or non-equivalent features

For matrix runs, `aggregate.csv` contains the same median rows shown in `report.md`. The generated SVG charts are derived from those rows and cover ingest throughput, search throughput, recall@10 versus search QPS, and memory/disk usage.

Matrix reports also include automatic top-5 rankings for ingest throughput, search QPS, native batch-search QPS, p95 latency, memory, and disk. Search-oriented rankings require `recall@10 >= 0.75`, so low-recall ANN profiles are visible in the raw table but do not appear as equivalent winners in quality-filtered rankings.

## Current Runner

The first runnable benchmark lives in `benchmarks/runner`.

Example quick run:

```bash
go run ./benchmarks/runner \
  --engine lumenvec-exact \
  --vectors 1000 \
  --dim 128 \
  --queries 100 \
  --warmup 10 \
  --batch-size 100 \
  --vector-id-prefix smoke \
  --concurrency 4 \
  --k 10 \
  --output benchmarks/results/lumenvec-exact-1k.json
```

Supported engines at this stage:

- `lumenvec-http-exact`, primary comparable LumenVec exact engine
- `lumenvec-http-ann`, primary comparable LumenVec ANN balanced engine
- `lumenvec-http-ann-fast`, LumenVec ANN fast profile
- `lumenvec-http-ann-quality`, LumenVec ANN quality profile
- `lumenvec-grpc-exact`, LumenVec exact profile through gRPC
- `lumenvec-grpc-ann`, LumenVec ANN balanced profile through gRPC
- `lumenvec-grpc-ann-fast`, LumenVec ANN fast profile through gRPC
- `lumenvec-grpc-ann-quality`, LumenVec ANN quality profile through gRPC
- `lumenvec-exact`, in-process smoke/profiling only
- `lumenvec-ann`, in-process smoke/profiling only
- `qdrant`
- `chroma`
- `weaviate`
- `pgvector`, exact PostgreSQL/pgvector baseline
- `pgvector-hnsw`, PostgreSQL/pgvector HNSW ANN profile
- `pgvector-ivfflat`, PostgreSQL/pgvector IVFFlat ANN profile

This runner is intentionally small. It validates the dataset generation, result schema, recall calculation, and adapters before larger scenario automation is added.

Primary comparable local run:

```bash
docker compose -f benchmarks/docker-compose.yml down -v
docker compose -f benchmarks/docker-compose.yml up -d lumenvec-exact lumenvec-ann lumenvec-ann-fast lumenvec-ann-quality lumenvec-grpc-exact lumenvec-grpc-ann lumenvec-grpc-ann-fast lumenvec-grpc-ann-quality qdrant pgvector

go run ./benchmarks/runner \
  --engine lumenvec-http-exact \
  --lumenvec-url http://localhost:19290 \
  --vectors 1000 \
  --dim 128 \
  --queries 100 \
  --warmup 10 \
  --batch-size 100 \
  --concurrency 4 \
  --k 10 \
  --output benchmarks/results/lumenvec-http-exact-1k.json

go run ./benchmarks/runner \
  --engine lumenvec-http-ann \
  --lumenvec-url http://localhost:19291 \
  --vectors 1000 \
  --dim 128 \
  --queries 100 \
  --warmup 10 \
  --batch-size 100 \
  --concurrency 4 \
  --k 10 \
  --output benchmarks/results/lumenvec-http-ann-1k.json

go run ./benchmarks/runner \
  --engine lumenvec-grpc-ann-quality \
  --lumenvec-grpc-address localhost:19393 \
  --vectors 1000 \
  --dim 128 \
  --queries 100 \
  --warmup 10 \
  --batch-size 100 \
  --concurrency 4 \
  --k 10 \
  --output benchmarks/results/lumenvec-grpc-ann-quality-1k.json
```

The benchmark LumenVec services use `benchmarks/Dockerfile.lumenvec`, not the production Dockerfile. This image keeps the same server binary but adds an entrypoint that fixes ownership of the persistent `/data` volume before dropping to the non-root UID used by LumenVec. This makes LumenVec and Qdrant both run with Docker-managed persistent volumes for the primary comparison.

Use `docker compose -f benchmarks/docker-compose.yml down -v` before a clean benchmark run. Docker named volumes are persistent by design, so recreating containers without removing volumes can leave old vectors behind and cause duplicate-ID ingest errors.

Qdrant local run:

```bash
docker compose -f benchmarks/docker-compose.yml up -d qdrant

go run ./benchmarks/runner \
  --engine qdrant \
  --qdrant-url http://localhost:6333 \
  --vectors 1000 \
  --dim 128 \
  --queries 100 \
  --warmup 10 \
  --batch-size 100 \
  --concurrency 4 \
  --k 10 \
  --output benchmarks/results/qdrant-1k.json
```

The Qdrant adapter uses the REST API:

- `PUT /collections/{collection_name}` to create the collection
- `PUT /collections/{collection_name}/points?wait=true` to upsert points
- `POST /collections/{collection_name}/points/query` to search
- `DELETE /collections/{collection_name}` during cleanup

Chroma local run:

```bash
docker compose -f benchmarks/docker-compose.yml up -d chroma

go run ./benchmarks/runner \
  --engine chroma \
  --chroma-url http://localhost:18000 \
  --vectors 1000 \
  --dim 128 \
  --queries 100 \
  --warmup 10 \
  --batch-size 100 \
  --concurrency 4 \
  --k 10 \
  --output benchmarks/results/chroma-1k.json
```

The Chroma adapter uses the REST API v2 with the default Docker tenant/database:

- `GET /api/v2/heartbeat` to wait for readiness
- `POST /api/v2/tenants/default_tenant/databases/default_database/collections` to create or reuse the collection
- `POST /api/v2/tenants/default_tenant/databases/default_database/collections/{collection_id}/add` to insert vectors
- `POST /api/v2/tenants/default_tenant/databases/default_database/collections/{collection_id}/query` to search
- the same `query` endpoint with multiple `query_embeddings` for native batch search
- `DELETE /api/v2/tenants/default_tenant/databases/default_database/collections/{collection_id}` during cleanup

Weaviate local run:

```bash
docker compose -f benchmarks/docker-compose.yml up -d weaviate

go run ./benchmarks/runner \
  --engine weaviate \
  --weaviate-url http://localhost:18080 \
  --vectors 1000 \
  --dim 128 \
  --queries 100 \
  --warmup 10 \
  --batch-size 100 \
  --concurrency 4 \
  --k 10 \
  --output benchmarks/results/weaviate-1k.json
```

The Weaviate adapter uses supplied vectors with `vectorizer = none`:

- `GET /v1/.well-known/ready` to wait for readiness
- `POST /v1/schema` to create one HNSW class per benchmark run
- `POST /v1/batch/objects` to insert vectors with an `external_id` property
- `POST /v1/graphql` with `nearVector` to search and return `_additional.distance`
- `DELETE /v1/schema/{class}` during cleanup

pgvector local run:

```bash
docker compose -f benchmarks/docker-compose.yml up -d pgvector

go run ./benchmarks/runner \
  --engine pgvector \
  --pgvector-dsn "postgres://postgres:postgres@localhost:15432/postgres?sslmode=disable" \
  --vectors 1000 \
  --dim 128 \
  --queries 100 \
  --warmup 10 \
  --batch-size 100 \
  --concurrency 4 \
  --k 10 \
  --output benchmarks/results/pgvector-1k.json
```

The first pgvector adapter is an exact baseline:

- `CREATE EXTENSION IF NOT EXISTS vector`
- one table per benchmark run
- `embedding vector(<dimension>)`
- batch inserts through PostgreSQL
- exact search with `ORDER BY embedding <-> query LIMIT k`

pgvector ANN runs:

```bash
go run ./benchmarks/runner \
  --engine pgvector-hnsw \
  --pgvector-dsn "postgres://postgres:postgres@localhost:15432/postgres?sslmode=disable" \
  --vectors 1000 \
  --dim 128 \
  --queries 100 \
  --warmup 10 \
  --batch-size 100 \
  --concurrency 4 \
  --k 10 \
  --output benchmarks/results/pgvector-hnsw-1k.json
```

The initial pgvector profiles are:

- `exact`: no ANN index; exact `ORDER BY embedding <-> query LIMIT k`
- `hnsw-m16-ef64`: HNSW index with `m = 16`, `ef_construction = 64`, and `hnsw.ef_search = 64`
- `ivfflat-l100-p10`: IVFFlat index with `lists = 100` and `ivfflat.probes = 10`

ANN indexes are built after ingest and before the first warmup search. The runner reports this cost as `index_build.total_duration_ms`, keeping measured ingest focused on inserting vectors and measured search focused on query latency.

Matrix runner:

```bash
go run ./benchmarks/runner/cmd/matrix \
  --runs 3 \
  --engines lumenvec-http-exact,lumenvec-http-ann,lumenvec-http-ann-fast,lumenvec-http-ann-quality,lumenvec-grpc-exact,lumenvec-grpc-ann,lumenvec-grpc-ann-fast,lumenvec-grpc-ann-quality \
  --vectors 10000 \
  --dim 128 \
  --queries 500 \
  --warmup 100 \
  --concurrency 4 \
  --search-batch-size 100 \
  --k 10 \
  --batch-sizes 100,500,1000,2000 \
  --output-dir benchmarks/results/matrix-10k-128-c4-k10
```

The matrix runner resets Docker volumes, executes LumenVec HTTP exact, LumenVec HTTP ANN fast/balanced/quality, LumenVec gRPC exact, LumenVec gRPC ANN fast/balanced/quality, Qdrant, Chroma, Weaviate, pgvector exact, pgvector HNSW, and pgvector IVFFlat for the configured ingest batch sizes, then writes raw JSON files plus aggregate `report.md`, `aggregate.csv`, and SVG charts with median values, including index-build time when a separate build phase exists and native batch-search metrics when the adapter supports them.

To regenerate only the aggregate files from existing raw JSON results, without rerunning Docker services:

```bash
go run ./benchmarks/runner/cmd/matrix \
  --aggregate-only \
  --vectors 10000 \
  --dim 128 \
  --queries 500 \
  --warmup 100 \
  --concurrency 4 \
  --search-batch-size 100 \
  --k 10 \
  --batch-sizes 100,500,1000,2000 \
  --output-dir benchmarks/results/matrix-10k-128-c4-k10
```

To compare a candidate result directory against a previous baseline directory, add `--compare-dir`. The matrix runner writes `comparison.csv` and `comparison.md` with row-level deltas and regression flags:

```bash
go run ./benchmarks/runner/cmd/matrix \
  --aggregate-only \
  --compare-dir benchmarks/baselines/matrix-10k-128-c4-k10 \
  --vectors 10000 \
  --dim 128 \
  --queries 500 \
  --warmup 100 \
  --concurrency 4 \
  --search-batch-size 100 \
  --k 10 \
  --batch-sizes 100,500,1000,2000 \
  --output-dir benchmarks/results/matrix-10k-128-c4-k10
```

Comparison flags a row as a regression when search QPS or native batch-search QPS drops by more than `5%`, p95 or p99 increases by more than `5%`, or recall@10 drops below the baseline. Ingest deltas are reported but do not override a search regression.

Pre-PR regression gate:

```powershell
.\scripts\benchmark-regression-gate.ps1
```

Unix-like shell:

```bash
make benchmark-gate
```

This gate runs a focused Docker matrix for LumenVec HTTP/gRPC exact and ANN quality using the local `10k / 128d / c4 / k10` shape, then fails if `comparison.csv` contains any regression row. It is intentionally smaller than the full matrix and is meant for cloud-readiness, API, transport, persistence, and search-adjacent changes. Run the full matrix before publishing benchmark claims or merging changes that alter search/index behavior.

Use `--search-batch-sizes` to diagnose native batch-search behavior separately from ingest batching:

```bash
go run ./benchmarks/runner/cmd/matrix \
  --runs 3 \
  --vectors 10000 \
  --dim 128 \
  --queries 500 \
  --warmup 100 \
  --concurrency 4 \
  --search-batch-sizes 10,25,50,100,250 \
  --k 10 \
  --batch-sizes 1000 \
  --output-dir benchmarks/results/matrix-10k-128-c4-k10-searchbatch
```

When `--search-batch-sizes` is set, the matrix expands every ingest batch size by every search batch size and the aggregate report includes both columns. Keep this diagnostic matrix narrow at first because every extra search batch size multiplies the number of Docker-reset benchmark runs.

Use `--engines` to run a focused diagnostic matrix. This is especially useful for native batch-search testing, because the current Qdrant and pgvector adapters report batch search as unsupported and do not need to be repeated for every search batch size.

The matrix runner passes a unique `--vector-id-prefix` to each child run. This keeps `--skip-docker` diagnostics usable with LumenVec HTTP services, where vector IDs live in the service-wide dataset rather than a per-run collection. Use normal Docker resets for publishable numbers; `--skip-docker` can reuse old LumenVec data and should be treated as a runner smoke test or implementation diagnostic only.

For Docker engines, the runner records resource snapshots automatically when the default Compose service names are used:

- `lumenvec-http-exact`: container `benchmarks-lumenvec-exact-1`, volume `benchmarks_lumenvec_exact_data`
- `lumenvec-http-ann`: container `benchmarks-lumenvec-ann-1`, volume `benchmarks_lumenvec_ann_data`
- `lumenvec-http-ann-fast`: container `benchmarks-lumenvec-ann-fast-1`, volume `benchmarks_lumenvec_ann_fast_data`
- `lumenvec-http-ann-quality`: container `benchmarks-lumenvec-ann-quality-1`, volume `benchmarks_lumenvec_ann_quality_data`
- `lumenvec-grpc-exact`: container `benchmarks-lumenvec-grpc-exact-1`, volume `benchmarks_lumenvec_grpc_exact_data`
- `lumenvec-grpc-ann`: container `benchmarks-lumenvec-grpc-ann-1`, volume `benchmarks_lumenvec_grpc_ann_data`
- `lumenvec-grpc-ann-fast`: container `benchmarks-lumenvec-grpc-ann-fast-1`, volume `benchmarks_lumenvec_grpc_ann_fast_data`
- `lumenvec-grpc-ann-quality`: container `benchmarks-lumenvec-grpc-ann-quality-1`, volume `benchmarks_lumenvec_grpc_ann_quality_data`
- `qdrant`: container `benchmarks-qdrant-1`, volume `benchmarks_qdrant_data`
- `chroma`: container `benchmarks-chroma-1`, volume `benchmarks_chroma_data`
- `weaviate`: container `benchmarks-weaviate-1`, volume `benchmarks_weaviate_data`
- `pgvector`: container `benchmarks-pgvector-1`, volume `benchmarks_pgvector_data`

Override the target with `--docker-container` and `--docker-volume` when running with different Compose project names. Use `--skip-docker-resources` for smoke tests where Docker metrics are not needed.

CPU and memory are sampled during the measured run with `docker stats --no-stream`. The default interval is `500ms`; override it with `--resource-sample-interval`.

## Suggested Implementation Order

1. Add the benchmark documentation and result schema. Done.
2. Implement deterministic vector and query generation. Done.
3. Implement LumenVec exact and LumenVec ANN adapters. In-process, Docker HTTP, and Docker gRPC versions done.
4. Implement Faiss baseline or a local exact baseline for recall validation. Local exact ground truth done; Faiss still pending.
5. Add Qdrant through Docker. Initial REST adapter done.
6. Add pgvector through Docker. Exact, HNSW, and IVFFlat profiles done.
7. Add Chroma through Docker. Initial REST adapter done.
8. Generate the first `10k` local baseline report. Done.
9. Add Weaviate and Milvus. Weaviate done; Milvus still pending.
10. Add Pinecone as a separate managed-service benchmark.

## Non-Goals for the First Version

- benchmarking every vector database in the market
- hand-tuning every engine before default results exist
- hiding failed or partial runs
- publishing a single ranking without workload context
