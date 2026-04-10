#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

wait_until() {
  local description="$1"
  local attempts="$2"
  local delay="$3"
  shift 3

  local i
  for ((i = 0; i < attempts; i++)); do
    if "$@"; then
      return 0
    fi
    sleep "$delay"
  done

  echo "Timed out while waiting for $description." >&2
  exit 1
}

check_lumenvec_health() {
  curl -fsS http://localhost:19190/health | grep -qx "ok"
}

check_prometheus_health() {
  curl -fsS http://localhost:9090/-/healthy | grep -q "Healthy"
}

check_prometheus_target() {
  local targets
  targets="$(curl -fsS http://localhost:9090/api/v1/targets)"
  grep -q '"job":"lumenvec"' <<<"$targets" && grep -q '"health":"up"' <<<"$targets"
}

check_grafana_health() {
  curl -fsS -u admin:admin http://localhost:3000/api/health | grep -q '"database": "ok"\|"database":"ok"'
}

check_grafana_dashboard() {
  curl -fsS -u admin:admin "http://localhost:3000/api/search?query=LumenVec" | grep -q '"uid":"lumenvec-overview"'
}

START_STACK=false
if [[ "${1:-}" == "--start" ]]; then
  START_STACK=true
fi

if [[ "$START_STACK" == "true" ]]; then
  echo "Starting docker compose stack..."
  docker compose up --build -d
fi

echo "Checking Docker Compose services..."
compose_json="$(docker compose ps --format json)"
if [[ -z "$compose_json" ]]; then
  echo "No Docker Compose services are running." >&2
  exit 1
fi

for service in lumenvec prometheus grafana; do
  if ! echo "$compose_json" | grep -q "\"Service\":\"$service\""; then
    echo "Service '$service' is missing from docker compose." >&2
    exit 1
  fi
  if ! echo "$compose_json" | grep -q "\"Service\":\"$service\".*\"State\":\"running\""; then
    echo "Service '$service' is not running." >&2
    exit 1
  fi
done

echo "Checking LumenVec health..."
wait_until "LumenVec health endpoint" 20 2 check_lumenvec_health
health="$(curl -fsS http://localhost:19190/health)"
if [[ "$health" != "ok" ]]; then
  echo "Unexpected LumenVec /health response: $health" >&2
  exit 1
fi

echo "Checking LumenVec metrics..."
metrics="$(curl -fsS http://localhost:19190/metrics)"
if ! grep -q "lumenvec_core_ann_config_info" <<<"$metrics"; then
  echo "LumenVec metrics are missing 'lumenvec_core_ann_config_info'." >&2
  exit 1
fi

echo "Checking Prometheus health and target scraping..."
wait_until "Prometheus health endpoint" 20 2 check_prometheus_health
prom_health="$(curl -fsS http://localhost:9090/-/healthy)"
if ! grep -q "Healthy" <<<"$prom_health"; then
  echo "Unexpected Prometheus /-/healthy response: $prom_health" >&2
  exit 1
fi

wait_until "Prometheus scraping the lumenvec target" 20 2 check_prometheus_target
targets="$(curl -fsS http://localhost:9090/api/v1/targets)"
if ! grep -q '"job":"lumenvec"' <<<"$targets"; then
  echo "Prometheus target 'lumenvec' was not found." >&2
  exit 1
fi
if ! grep -q '"health":"up"' <<<"$targets"; then
  echo "Prometheus target 'lumenvec' is not healthy." >&2
  exit 1
fi

echo "Checking Grafana health and dashboard provisioning..."
wait_until "Grafana health endpoint" 20 2 check_grafana_health
grafana_health="$(curl -fsS -u admin:admin http://localhost:3000/api/health)"
if ! grep -q '"database": "ok"\|"database":"ok"' <<<"$grafana_health"; then
  echo "Grafana database health is not ok." >&2
  exit 1
fi

wait_until "Grafana dashboard provisioning" 20 2 check_grafana_dashboard
grafana_search="$(curl -fsS -u admin:admin "http://localhost:3000/api/search?query=LumenVec")"
if ! grep -q '"uid":"lumenvec-overview"' <<<"$grafana_search"; then
  echo "Grafana dashboard 'lumenvec-overview' was not provisioned." >&2
  exit 1
fi

echo "Observability stack validation passed."
