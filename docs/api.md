# LumenVec API

LumenVec exposes HTTP and gRPC transports over the same core service.

The cloud-ready HTTP contract starts at `/v1`. Existing unversioned routes remain available for compatibility, but new integrations should prefer `/v1`.

## HTTP Versioning

Versioned routes:

- `GET /v1/health`
- `GET /v1/livez`
- `GET /v1/readyz`
- `GET /v1/vectors?limit=&cursor=&after=&ids_only=`
- `POST /v1/vectors`
- `POST /v1/vectors/batch`
- `GET /v1/vectors/{id}`
- `DELETE /v1/vectors/{id}`
- `POST /v1/vectors/search`
- `POST /v1/vectors/search/batch`

Compatibility routes without `/v1` currently expose equivalent behavior.

## Error Shape

Versioned HTTP routes return JSON errors:

```json
{
  "error": {
    "code": "invalid_argument",
    "message": "id is required"
  }
}
```

Current error codes:

- `invalid_argument`
- `invalid_json`
- `unauthorized`
- `not_found`
- `already_exists`
- `method_not_allowed`
- `rate_limited`
- `internal`

Unversioned compatibility routes may still return plain-text errors.

## Authentication

When API key authentication is enabled, clients can authenticate with either:

- `Authorization: Bearer <api-key>`
- `X-API-Key: <api-key>`

Health, liveness, readiness, and metrics endpoints are public by default so orchestrators and monitoring systems can call them without application credentials.

## Request IDs

HTTP clients may send `X-Request-ID`. When the header is present and valid, LumenVec returns the same value in the response.

When the header is absent or invalid, LumenVec generates a request ID and returns it in `X-Request-ID`. Access logs include the request ID, method, path, status, duration, and client address.

## Health Probes

- `/health` and `/v1/health` are compatibility health checks.
- `/livez` and `/v1/livez` are cheap liveness checks for orchestrators.
- `/readyz` and `/v1/readyz` validate that the service is initialized and storage paths are usable.

Use liveness for process restart decisions and readiness for traffic routing.

## OpenAPI

The initial OpenAPI contract is checked in at [api/openapi.yaml](../api/openapi.yaml).
