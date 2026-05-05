# Cloud Deployment

This guide covers the supported single-node cloud deployment shape for LumenVec. It focuses on a container running as a non-root user with persistent storage, health probes, metrics, and API-key authentication.

Distributed clustering, replication, sharding, and a managed control plane are outside this deployment model.

## Container Defaults

The production image uses a distroless non-root runtime and runs as UID/GID `65532`. The writable data path is `/data`.

Important defaults:

- HTTP listens on `19190`.
- gRPC is exposed on `19191`, but the default HTTP config keeps HTTP as the active transport.
- Snapshot path: `/data/snapshot.json`.
- WAL path: `/data/wal.log`.
- Disk vector-store path: `/data/vectors`.
- Health endpoints: `/livez`, `/readyz`, `/health`.
- Metrics endpoint: `/metrics`.

When mounting a host path or persistent volume, make sure `/data` is writable by UID/GID `65532`. Kubernetes deployments should use `fsGroup: 65532` when the storage class supports ownership management.

## Kubernetes

Example manifests are checked in under [deploy/kubernetes](../deploy/kubernetes).

Create the API-key secret before applying the deployment:

```sh
kubectl create namespace lumenvec
kubectl -n lumenvec create secret generic lumenvec-secret \
  --from-literal=VECTOR_DB_SECURITY_API_KEY='replace-with-a-long-random-api-key'
kubectl apply -k deploy/kubernetes
```

The manifests provide:

- `Deployment` with one replica and `Recreate` strategy.
- `Service` exposing HTTP and gRPC inside the cluster.
- `PersistentVolumeClaim` mounted at `/data`.
- `ConfigMap` for non-secret environment settings.
- `Secret` reference for the API key.
- Liveness probe on `/livez`.
- Readiness probe on `/readyz`.
- Non-root security context, dropped capabilities, no privilege escalation, and read-only root filesystem.

The deployment intentionally uses `replicas: 1`. Running multiple pods against the same writable volume is not supported.

## Storage

For cloud deployments, prefer `VECTOR_DB_VECTOR_STORE=disk` with a persistent volume mounted at `/data`. This keeps vector payloads file-backed and stores persistence data under the same mounted path.

Recommended storage settings:

```text
VECTOR_DB_VECTOR_STORE=disk
VECTOR_DB_VECTOR_PATH=/data/vectors
VECTOR_DB_SNAPSHOT_PATH=/data/snapshot.json
VECTOR_DB_WAL_PATH=/data/wal.log
VECTOR_DB_STRICT_FILE_PERMISSIONS=true
VECTOR_DB_STORAGE_DIR_MODE=0700
VECTOR_DB_STORAGE_FILE_MODE=0600
```

Use a storage class with `ReadWriteOnce` semantics for the single-node deployment.

## Probes

Use `/livez` for process liveness and `/readyz` for traffic routing.

Recommended starting point:

```yaml
livenessProbe:
  httpGet:
    path: /livez
    port: http
  initialDelaySeconds: 10
  periodSeconds: 20

readinessProbe:
  httpGet:
    path: /readyz
    port: http
  initialDelaySeconds: 5
  periodSeconds: 10
```

Readiness checks that the service is initialized and that configured persistence paths are usable.

## TLS And Ingress

For Kubernetes and most managed platforms, terminate public TLS at an ingress controller, gateway, service mesh, or cloud load balancer. Keep `VECTOR_DB_TLS_ENABLED=false` inside the pod unless you need end-to-end TLS to the container.

When LumenVec runs behind a trusted proxy and you need client IPs from `X-Forwarded-For`, enable:

```text
VECTOR_DB_TRUST_FORWARDED_FOR=true
VECTOR_DB_TRUSTED_PROXIES=10.0.0.0/8
```

Only configure trusted proxy ranges that are controlled by your platform.

## Resource Sizing

The example deployment starts with:

```yaml
requests:
  cpu: 250m
  memory: 512Mi
limits:
  cpu: "2"
  memory: 2Gi
```

Tune memory based on vector count, dimension, cache settings, and ANN profile. Keep benchmark comparisons for search QPS, p95/p99 latency, and recall before increasing production limits or changing ANN settings.

## Operations

Use:

- `/metrics` for Prometheus scraping.
- `X-Request-ID` for request tracing across gateways and logs.
- JSON access logs with `VECTOR_DB_ACCESS_LOG=true`.

Backup, restore, and shutdown procedures are documented in [docs/operations.md](operations.md).

Before production traffic, verify:

```sh
kubectl -n lumenvec rollout status deployment/lumenvec
kubectl -n lumenvec port-forward svc/lumenvec 19190:19190
curl -i http://localhost:19190/readyz
curl -i -H "Authorization: Bearer <api-key>" http://localhost:19190/v1/vectors
```
