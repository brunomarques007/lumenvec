#!/usr/bin/env bash
set -euo pipefail

BASELINE_DIR="${BASELINE_DIR:-benchmarks/baselines/matrix-10k-128-c4-k10}"
OUTPUT_DIR="${OUTPUT_DIR:-benchmarks/results/pre-pr-regression-gate}"
ENGINES="${ENGINES:-lumenvec-http-exact,lumenvec-http-ann-quality,lumenvec-grpc-exact,lumenvec-grpc-ann-quality}"
BATCH_SIZES="${BATCH_SIZES:-1000}"
RUNS="${RUNS:-1}"
VECTORS="${VECTORS:-10000}"
DIM="${DIM:-128}"
QUERIES="${QUERIES:-500}"
WARMUP="${WARMUP:-100}"
CONCURRENCY="${CONCURRENCY:-4}"
SEARCH_BATCH_SIZE="${SEARCH_BATCH_SIZE:-100}"
K="${K:-10}"
SKIP_COMPARE="${SKIP_COMPARE:-false}"

command -v docker >/dev/null 2>&1 || {
  echo "docker is required for the benchmark regression gate" >&2
  exit 1
}

COMPARE_ARGS=()
if [[ "$SKIP_COMPARE" != "true" ]]; then
  if [[ ! -d "$BASELINE_DIR" ]]; then
    echo "baseline directory not found: $BASELINE_DIR. Set SKIP_COMPARE=true for a smoke run without regression comparison." >&2
    exit 1
  fi
  COMPARE_ARGS=(--compare-dir "$BASELINE_DIR")
fi

go run ./benchmarks/runner/cmd/matrix \
  --runs "$RUNS" \
  --engines "$ENGINES" \
  --vectors "$VECTORS" \
  --dim "$DIM" \
  --queries "$QUERIES" \
  --warmup "$WARMUP" \
  --concurrency "$CONCURRENCY" \
  --search-batch-size "$SEARCH_BATCH_SIZE" \
  --k "$K" \
  --batch-sizes "$BATCH_SIZES" \
  --output-dir "$OUTPUT_DIR" \
  "${COMPARE_ARGS[@]}"

COMPARISON_PATH="$OUTPUT_DIR/comparison.csv"
if [[ "$SKIP_COMPARE" != "true" && -f "$COMPARISON_PATH" ]]; then
  if awk -F, 'NR > 1 && $1 == "regression" { found = 1 } END { exit found ? 0 : 1 }' "$COMPARISON_PATH"; then
    echo "benchmark regression gate failed; see $OUTPUT_DIR/comparison.md" >&2
    exit 1
  fi
fi

echo "benchmark regression gate passed"
