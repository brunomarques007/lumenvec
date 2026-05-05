# Cloud Readiness Plan

## Goal

Make LumenVec ready for cloud and managed-platform adoption while preserving the current search-performance advantage. The first target is a reliable single-node deployment model with clear API contracts, operational signals, secure defaults, and reproducible packaging.

This plan intentionally avoids multi-node clustering, sharding, distributed consensus, or managed-control-plane work until the single-node cloud story is stable.

## Guiding Rules

- Search QPS, p95/p99 latency, and recall remain protected by the benchmark baseline.
- Public API behavior must be versioned and documented before being treated as stable.
- HTTP and gRPC should keep sharing the same core service and validation rules.
- Container defaults must work with non-root users and persistent volumes.
- Cloud features should improve operations, integration, and safety without changing ranking semantics.

## Current Baseline

Already available:

- HTTP and gRPC transports.
- API key authentication for HTTP and gRPC.
- TLS support for HTTP and gRPC.
- Prometheus metrics endpoint.
- JSON access logs when access logging is enabled.
- Docker packaging.
- Disk and memory vector-store modes.
- Benchmark matrix and local baseline comparison tooling.

Remaining gaps:

- None for the current single-node cloud-readiness slice. Future work remains for multi-node operation and managed-service control-plane features.

## Milestones

### Milestone 1: Public API Contract

Objective:
Define a stable, cloud-friendly HTTP API surface without removing the current unversioned routes.

Work items:

- Add `/v1` route aliases for the existing vector API.
- Keep existing routes during the compatibility window.
- Define a JSON error envelope.
- Add OpenAPI documentation for health, metrics, vector CRUD, search, and batch search.
- Document authentication headers and status-code semantics.

Acceptance criteria:

- Existing HTTP tests still pass.
- `/v1` routes have equivalent behavior to current routes.
- OpenAPI spec is checked in.
- Error responses for `/v1` routes use a stable JSON shape.
- README links to the API contract.

Status:

- `/v1` route aliases, `/v1` JSON error envelopes, initial `api/openapi.yaml`, and `docs/api.md` are implemented.

Suggested files:

- `internal/api/server.go`
- `internal/api/middleware.go`
- `docs/api.md`
- `api/openapi.yaml`
- `README.md`

### Milestone 2: Cloud Health and Readiness

Objective:
Expose separate probes for orchestrators and managed platforms.

Work items:

- Keep `/health` as a compatibility endpoint.
- Add liveness endpoint, for example `/livez`.
- Add readiness endpoint, for example `/readyz`.
- Readiness should verify that the service is initialized and persistence paths are usable.
- Document Kubernetes probe settings.

Acceptance criteria:

- Liveness remains cheap and does not depend on storage.
- Readiness can fail when startup or storage recovery is not usable.
- Health endpoints are public when auth is enabled, matching current `/health` behavior.
- Tests cover authenticated and unauthenticated probe behavior.

Status:

- `/livez`, `/readyz`, `/v1/livez`, and `/v1/readyz` are implemented. Readiness validates service initialization and writable storage paths.

Suggested files:

- `internal/api/server.go`
- `internal/api/server_test.go`
- `internal/api/middleware.go`
- `docs/operations.md`

### Milestone 3: Request Identity and Structured Logs

Objective:
Make LumenVec easy to observe behind load balancers, gateways, and cloud logging systems.

Work items:

- Accept `X-Request-ID`.
- Generate a request ID when one is not supplied.
- Return the request ID in HTTP responses.
- Include request ID in access logs.
- Add gRPC request ID metadata support if practical.

Acceptance criteria:

- Every HTTP response includes a request ID.
- Access logs include request ID, method, route, status, duration, and client address.
- Request ID behavior is tested.

Status:

- HTTP `X-Request-ID` is accepted, generated when absent or invalid, returned on responses, and included in JSON access logs with method, path, status, duration, and client address.

Suggested files:

- `internal/api/middleware.go`
- `internal/api/middleware_test.go`
- `docs/operations.md`

### Milestone 4: Container and Kubernetes Packaging

Objective:
Provide a documented single-node cloud deployment path.

Work items:

- Confirm production Dockerfile runs as non-root.
- Document persistent volume ownership and permissions.
- Add Kubernetes manifests for Deployment, Service, PVC, ConfigMap, and Secret.
- Add notes for reverse proxy or ingress TLS termination.
- Document resource requests and limits for small deployments.

Acceptance criteria:

- Manifests are checked in under `deploy/kubernetes/`.
- A local Kubernetes user can deploy LumenVec with a persistent volume.
- Secrets are not hardcoded.
- Volume permissions are documented for non-root operation.

Status:

- Initial single-node Kubernetes manifests are checked in under `deploy/kubernetes/`. The deployment uses non-root UID/GID `65532`, a PVC mounted at `/data`, public liveness/readiness probes, a ConfigMap for non-secret settings, and a Secret reference for the API key. Cloud deployment notes are documented in `docs/cloud.md`.

Suggested files:

- `Dockerfile`
- `deploy/kubernetes/*.yaml`
- `docs/cloud.md`
- `docs/operations.md`

### Milestone 5: Backup, Restore, and Shutdown

Objective:
Make single-node operations repeatable.

Work items:

- Document snapshot/WAL and disk-store backup expectations.
- Document restore procedure for both memory and disk vector-store modes.
- Document graceful shutdown behavior for HTTP and gRPC.
- Add tests where shutdown hooks can be verified without flakiness.

Acceptance criteria:

- Backup and restore docs include exact paths and mode-specific caveats.
- Operators know when to quiesce writes before backup.
- Shutdown behavior is documented for Docker and Kubernetes.

Status:

- `docs/operations.md` documents backup, restore, and shutdown for `memory` and `disk` modes. The server entrypoint handles `SIGINT`/`SIGTERM`, performs graceful HTTP/gRPC shutdown, and closes the core service so pending persistence state is synced before exit.

Suggested files:

- `docs/operations.md`
- `docs/cloud.md`
- `cmd/server`
- `internal/api`

### Milestone 6: Regression Gate

Objective:
Prevent cloud-readiness work from degrading search performance.

Work items:

- Use the existing benchmark baseline before and after API or packaging changes.
- Add a short smoke benchmark preset for local pre-PR checks.
- Document when full matrix comparison is required.

Acceptance criteria:

- `go test ./...` passes.
- Benchmark comparison can be run with `--compare-dir`.
- Any consistent search QPS, p95/p99, or recall regression blocks the change.

Status:

- `scripts/benchmark-regression-gate.ps1`, `scripts/benchmark-regression-gate.sh`, and `make benchmark-gate` provide a focused pre-PR Docker regression gate for LumenVec HTTP/gRPC exact and ANN quality. The gate compares against the local `10k / 128d / c4 / k10` baseline when present and fails on search QPS, batch-search QPS, p95/p99, or recall@10 regressions. `docs/testing.md` and `benchmarks/README.md` document when to use the short gate and when to run the full matrix.

Suggested files:

- `benchmarks/README.md`
- `docs/testing.md`
- `.github/workflows/ci.yml`

## Recommended Execution Order

1. Public API Contract.
2. Cloud Health and Readiness.
3. Request Identity and Structured Logs.
4. Container and Kubernetes Packaging.
5. Backup, Restore, and Shutdown.
6. Regression Gate.

The first implementation slice should be small: add `/v1` route aliases, define the error envelope for `/v1`, and create the initial OpenAPI skeleton. That gives cloud platforms and API gateways something stable to integrate with before deployment manifests are added.

## Out of Scope for This Branch

- Multi-node clustering.
- Distributed storage.
- Replication and leader election.
- Managed-service control plane.
- Per-tenant authorization model.
- Pinecone-style hosted benchmark runs.
