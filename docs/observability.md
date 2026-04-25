# Observability

## Metrics

LumenVec exposes Prometheus metrics at `GET /metrics`.

Operational note:

- HTTP is the default transport and should be treated as the baseline integration path
- gRPC should be preferred when the goal is to minimize transport overhead under sustained search load

Core ANN health metrics:

- `lumenvec_core_ann_searches_total`
- `lumenvec_core_ann_search_hits_total`
- `lumenvec_core_ann_fallbacks_total`
- `lumenvec_core_ann_errors_total`
- `lumenvec_core_ann_candidates_returned_total`

ANN runtime config metric:

- `lumenvec_core_ann_config_info{profile,m,ef_construction,ef_search} 1`

ANN sampled quality metrics:

- `lumenvec_core_ann_eval_samples_total`
- `lumenvec_core_ann_eval_top1_matches_total`
- `lumenvec_core_ann_eval_overlap_results_total`
- `lumenvec_core_ann_eval_compared_results_total`

Cache metrics:

- `lumenvec_core_cache_hits_total`
- `lumenvec_core_cache_misses_total`
- `lumenvec_core_cache_evictions_total`
- `lumenvec_core_cache_items`
- `lumenvec_core_cache_bytes`

Disk store metrics:

- `lumenvec_core_disk_file_bytes`
- `lumenvec_core_disk_records`
- `lumenvec_core_disk_stale_records`
- `lumenvec_core_disk_compactions_total`

## Recommended Queries

ANN fallback ratio:

```promql
rate(lumenvec_core_ann_fallbacks_total[10m])
/
clamp_min(rate(lumenvec_core_ann_searches_total[10m]), 0.0001)
```

ANN sampled top-1 match ratio:

```promql
rate(lumenvec_core_ann_eval_top1_matches_total[15m])
/
clamp_min(rate(lumenvec_core_ann_eval_samples_total[15m]), 0.0001)
```

ANN sampled overlap ratio:

```promql
rate(lumenvec_core_ann_eval_overlap_results_total[15m])
/
clamp_min(rate(lumenvec_core_ann_eval_compared_results_total[15m]), 0.0001)
```

Cache hit ratio:

```promql
rate(lumenvec_core_cache_hits_total[10m])
/
clamp_min(rate(lumenvec_core_cache_hits_total[10m]) + rate(lumenvec_core_cache_misses_total[10m]), 0.0001)
```

Disk store stale-to-live pressure:

```promql
lumenvec_core_disk_stale_records
/
clamp_min(lumenvec_core_disk_records, 1)
```

## Dashboard Guidance

Useful dashboard panels:

- ANN searches, hits, fallbacks, and errors
- ANN fallback ratio
- ANN sampled top-1 match ratio
- ANN sampled overlap ratio
- ANN config info by instance
- Cache hit ratio, items, and bytes
- Disk file size, stale records, and compaction count

Operational reading:

- rising ANN fallback ratio usually means candidate quality is degrading or dimensional/data drift increased
- low top-1 match ratio means ANN tuning is too aggressive for the current dataset
- low overlap ratio means the ANN result set is diverging from exact search
- high stale-to-live disk ratio means compaction pressure is building
- low cache hit ratio with high ANN traffic means the working set does not fit current cache settings

## Alert Rules

Example Prometheus rules are provided in [alerts.yml](/C:/app/testes/golang/database/lumenvec/configs/prometheus/alerts.yml).

These rules cover:

- ANN fallback rate
- ANN sampled top-1 match rate
- ANN sampled overlap rate
- disk store stale-record pressure
