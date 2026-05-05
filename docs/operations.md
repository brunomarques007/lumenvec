# Operations

This guide documents single-node backup, restore, and shutdown procedures for LumenVec.

## Storage Modes

LumenVec supports two persistence shapes:

- `memory` vector store: vector payloads are restored from `snapshot + WAL`.
- `disk` vector store: vector payloads are stored under `vector_path`; the search index is rebuilt from that store during startup.

Default cloud paths:

- Snapshot: `/data/snapshot.json`
- WAL: `/data/wal.log`
- Disk vector store: `/data/vectors`

## Backup

For the safest backup, quiesce writes before copying files. Reads can continue, but writes should be stopped at the application, gateway, or orchestration layer.

For Kubernetes:

```sh
kubectl -n lumenvec scale deployment/lumenvec --replicas=0
```

Then back up the persistent volume contents.

For `memory` mode, include:

- `/data/snapshot.json`
- `/data/wal.log`

For `disk` mode, include:

- `/data/vectors`

If `snapshot.json` and `wal.log` also exist in the same volume, include them as diagnostic state, but disk mode rebuilds from `vector_path`.

## Restore

1. Stop LumenVec.
2. Restore the files into the configured persistent volume paths.
3. Verify volume ownership allows UID/GID `65532` to read and write `/data`.
4. Start LumenVec.
5. Check readiness with `/readyz`.

For Kubernetes:

```sh
kubectl -n lumenvec scale deployment/lumenvec --replicas=0
# restore the PVC contents with your storage platform tooling
kubectl -n lumenvec scale deployment/lumenvec --replicas=1
kubectl -n lumenvec rollout status deployment/lumenvec
kubectl -n lumenvec port-forward svc/lumenvec 19190:19190
curl -i http://localhost:19190/readyz
```

## Shutdown

The server handles `SIGINT` and `SIGTERM` in the `cmd/server` entrypoint. During shutdown it:

- stops accepting new HTTP requests or gRPC calls;
- gives in-flight work a short grace period to finish;
- calls the core service `Close`;
- syncs pending WAL writes when snapshot/WAL persistence is active;
- closes the disk vector store when disk mode is active.

For Docker:

```sh
docker stop lumenvec
```

For Kubernetes, use the Deployment lifecycle. The default manifests rely on Kubernetes sending `SIGTERM` and waiting for the pod termination grace period.

## Operational Checks

Use:

- `/livez` for process health.
- `/readyz` for storage readiness and traffic routing.
- `/metrics` for Prometheus scraping.
- `X-Request-ID` to correlate gateway, application, and LumenVec logs.

Before backup or maintenance, verify the target instance:

```sh
curl -i http://localhost:19190/readyz
curl -i http://localhost:19190/metrics
```
