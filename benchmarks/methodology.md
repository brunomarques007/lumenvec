# Benchmark Methodology

This methodology is the contract for benchmark runs. If a result does not follow this document, it should be treated as exploratory and not used in comparison tables.

## Run Identity

Every run must have a stable run ID.

Recommended format:

```text
YYYYMMDD-HHMMSS-<machine>-<dataset>-<dimension>
```

Example:

```text
20260425-143000-local-100k-384
```

## Environment Metadata

Record the following before each run:

- operating system and version
- CPU model and core count
- total RAM
- disk type when known, for example NVMe, SSD, HDD, or network volume
- Go version
- Docker version
- Git commit SHA
- active branch
- whether the tree is clean

For Docker-based engines, record:

- image name
- image tag or digest
- exposed ports
- mounted volumes
- memory and CPU limits, if any

## Isolation Model

Primary market comparisons must use the same isolation model for every database engine.

For local self-hosted comparisons, the standard isolation model is Docker:

- LumenVec runs as a Docker service.
- Qdrant runs as a Docker service.
- pgvector runs as a Docker service.
- Weaviate, Milvus, and Chroma should also run as Docker services when added.

For persistence-sensitive runs, each database should use Docker-managed persistent volumes unless the report explicitly says it is a tmpfs or in-memory-storage run. LumenVec benchmark containers use a benchmark-specific Dockerfile that prepares `/data` permissions and then runs the server as a non-root user.

Clean benchmark runs must remove old Docker volumes before setup:

```bash
docker compose -f benchmarks/docker-compose.yml down -v
```

Container recreation alone is not enough for persistent-volume scenarios because previous vectors can remain on disk and affect ingest, recovery, recall, and duplicate-ID behavior.

In-process adapters are allowed only for:

- smoke tests
- implementation profiling
- ground-truth calculation
- library baselines such as Faiss, clearly labeled as in-process

Do not place in-process LumenVec results and Docker Qdrant results in the same ranking table. If both are shown, they must be in separate sections with the transport and isolation model visible.

## Dataset Metadata

Record:

- vector count
- dimension
- random seed
- data distribution
- distance metric
- number of query vectors
- whether query vectors are sampled from the dataset or generated separately

Initial synthetic generator:

- use a fixed seed
- generate `float32` values
- store IDs as deterministic strings: `vec-000000001`
- generate queries from the same distribution but with independent IDs: `query-000000001`

## Distance Metrics

Initial benchmark metric:

- squared L2 or L2, depending on engine support

Rules:

- one report table must contain only comparable metrics
- if an engine reports squared distance and another reports L2, ranking by distance may still be equivalent, but the report must document the difference
- cosine should be measured in a separate run

## Ground Truth

Ground truth is required for recall.

Preferred order:

1. exact search from a trusted local implementation over the same vectors
2. Faiss exact index
3. LumenVec exact mode, after validating it against small hand-checkable cases

Store ground-truth results separately from engine results. The benchmark should be able to recompute recall after the run without querying any database again.

## Recall

For each query and `k`, compute:

```text
recall@k = intersection(result_ids, ground_truth_ids) / k
```

Report:

- average `recall@1`
- average `recall@5`
- average `recall@10`
- optional average `recall@100`

If the measured `k` is smaller than the recall target, skip that recall target.

## Latency

Record per-operation latency in milliseconds.

Report:

- p50
- p95
- p99
- min
- max
- mean

Rules:

- separate ingest latency from search latency
- separate explicit index-build latency from ingest and search when an engine exposes a build step
- report native batch-search latency separately from single-query search latency
- separate warmup from measured requests
- do not mix single-query and batch-query latency in the same column
- record timeout counts separately

## Throughput

Report:

- vectors per second for ingest
- queries per second for search
- queries per second for native batch search, when supported
- batches per second when batch operations are used

Throughput must include client-side request/response time because users experience the full operation, not only the internal engine time.

## Resource Usage

For local processes and containers, capture:

- peak memory
- average memory
- peak CPU
- average CPU
- final disk size

For Docker runs, repeated `docker stats --no-stream` sampling is acceptable for the first implementation. Later versions can add cgroup or Prometheus-based collection.

Current runner behavior:

- Docker CPU and memory are sampled continuously during ingest, explicit index build, warmup, measured single-query search, and measured batch search.
- The default resource sample interval is `500ms` and can be changed with `--resource-sample-interval`.
- Docker disk usage is measured from the Docker-managed volume with `du -sk`.
- The reported CPU and memory values are sampled peaks and averages. They are still not as precise as cgroup or Prometheus time series, but they are more useful than end-of-run snapshots.

## Configuration Profiles

Each engine should have at least two profiles:

- `default`: minimal setup with documented defaults
- `tuned`: explicit settings chosen for the benchmark workload

Never mix default and tuned results in the same ranking without a visible `profile` column.

Current pgvector profile:

- `exact`: PostgreSQL table with `vector(<dimension>)` and exact `ORDER BY embedding <-> query`.
- `hnsw-m16-ef64`: HNSW index with `m = 16`, `ef_construction = 64`, and `hnsw.ef_search = 64`.
- `ivfflat-l100-p10`: IVFFlat index with `lists = 100` and `ivfflat.probes = 10`.

pgvector ANN indexes are built after ingest and before warmup. Reports include this as `index_build.total_duration_ms`, so ANN profiles should be interpreted with both index-build cost and query-serving latency visible.

Current LumenVec ANN profiles:

- `ann-fast`: `m = 8`, `ef_construction = 32`, `ef_search = 32`
- `ann-balanced`: `m = 16`, `ef_construction = 64`, `ef_search = 64`
- `ann-quality`: `m = 24`, `ef_construction = 96`, `ef_search = 96`

LumenVec currently builds its ANN graph during ingest, so reports show `index_build.built: false` for these profiles until LumenVec exposes a separate offline build phase.

The benchmark Compose file sets `VECTOR_DB_ANN_M`, `VECTOR_DB_ANN_EF_CONSTRUCTION`, and `VECTOR_DB_ANN_EF_SEARCH` explicitly for each profile so the measured containers do not depend on default profile expansion behavior.

## Warmup

Before measuring searches:

- run a fixed number of warmup queries
- discard warmup latencies from the measured sample
- record warmup duration separately

Initial warmup:

- `100` queries for `10k`
- `1000` queries for `100k` and larger

## Repetitions

Each benchmark scenario should run at least three times.

Report:

- best result only when clearly labeled as best
- median run as the primary comparison
- worst run or variance when instability is high

The first implementation may run once to validate the runner, but published comparisons should use repeated runs.

## Failure Handling

A benchmark case should be marked failed when:

- setup fails
- ingest fails
- search error rate is above the configured threshold
- recall cannot be computed
- resource metrics cannot be collected for a required table

Do not delete failed raw output. Failed cases are useful for reproducibility.

## Report Interpretation

The report should avoid claiming a universal winner.

Use workload-specific language:

- fastest ingest for this dataset
- lowest p95 latency for this `k` and concurrency
- best recall-latency tradeoff under this configuration
- lowest memory for this vector count and dimension

This keeps the benchmark useful and defensible.
