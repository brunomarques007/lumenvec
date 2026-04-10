package api

import (
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumenvec/internal/core"

	"github.com/prometheus/client_golang/prometheus"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		hits:   make(map[string][]time.Time),
		limit:  limit,
		window: window,
	}
}

func (rl *rateLimiter) allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-rl.window)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	events := rl.hits[key]
	filtered := events[:0]
	for _, t := range events {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}

	if len(filtered) >= rl.limit {
		rl.hits[key] = filtered
		return false
	}

	rl.hits[key] = append(filtered, now)
	return true
}

func getClientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey == "" || r.URL.Path == "/health" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		key := strings.TrimSpace(r.Header.Get("X-API-Key"))
		if key == "" {
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
				key = strings.TrimSpace(auth[7:])
			}
		}

		if key != s.apiKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.rateLimiter == nil || r.URL.Path == "/health" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		ip := getClientIP(r)
		if !s.rateLimiter.allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		duration := time.Since(start).Seconds()

		if s.requestTotal != nil {
			s.requestTotal.WithLabelValues(r.Method, r.URL.Path, http.StatusText(rec.status)).Inc()
		}
		if s.requestDuration != nil {
			s.requestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration)
		}
	})
}

func (s *Server) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf(`{"event":"http_request","method":"%s","path":"%s","status":%d,"duration_ms":%d}`,
			r.Method, r.URL.Path, rec.status, time.Since(start).Milliseconds())
	})
}

type coreMetricsCollector struct {
	service *core.Service

	searchRequestsDesc     *prometheus.Desc
	exactSearchesDesc      *prometheus.Desc
	annSearchesDesc        *prometheus.Desc
	annSearchHitsDesc      *prometheus.Desc
	annSearchFallbacks     *prometheus.Desc
	annSearchErrorsDesc    *prometheus.Desc
	annCandidatesDesc      *prometheus.Desc
	annEvalSamplesDesc     *prometheus.Desc
	annEvalTop1MatchesDesc *prometheus.Desc
	annEvalOverlapDesc     *prometheus.Desc
	annEvalComparedDesc    *prometheus.Desc
	cacheHitsDesc          *prometheus.Desc
	cacheMissesDesc        *prometheus.Desc
	cacheEvictionsDesc     *prometheus.Desc
	cacheItemsDesc         *prometheus.Desc
	cacheBytesDesc         *prometheus.Desc
	diskFileBytesDesc      *prometheus.Desc
	diskRecordsDesc        *prometheus.Desc
	diskStaleRecordsDesc   *prometheus.Desc
	diskCompactionsDesc    *prometheus.Desc
	annConfigInfoDesc      *prometheus.Desc
}

func newCoreMetricsCollector(service *core.Service) *coreMetricsCollector {
	return &coreMetricsCollector{
		service: service,
		searchRequestsDesc: prometheus.NewDesc(
			"lumenvec_core_search_requests_total",
			"Total search requests handled by the core service.",
			nil,
			nil,
		),
		exactSearchesDesc: prometheus.NewDesc(
			"lumenvec_core_exact_searches_total",
			"Total exact searches executed by the core service.",
			nil,
			nil,
		),
		annSearchesDesc: prometheus.NewDesc(
			"lumenvec_core_ann_searches_total",
			"Total ANN searches attempted by the core service.",
			nil,
			nil,
		),
		annSearchHitsDesc: prometheus.NewDesc(
			"lumenvec_core_ann_search_hits_total",
			"Total ANN searches that returned candidates successfully.",
			nil,
			nil,
		),
		annSearchFallbacks: prometheus.NewDesc(
			"lumenvec_core_ann_fallbacks_total",
			"Total times the core service fell back from ANN to exact search.",
			nil,
			nil,
		),
		annSearchErrorsDesc: prometheus.NewDesc(
			"lumenvec_core_ann_errors_total",
			"Total ANN search errors observed by the core service.",
			nil,
			nil,
		),
		annCandidatesDesc: prometheus.NewDesc(
			"lumenvec_core_ann_candidates_returned_total",
			"Total ANN candidates returned before final re-scoring.",
			nil,
			nil,
		),
		annEvalSamplesDesc: prometheus.NewDesc(
			"lumenvec_core_ann_eval_samples_total",
			"Total sampled ANN queries evaluated against exact search.",
			nil,
			nil,
		),
		annEvalTop1MatchesDesc: prometheus.NewDesc(
			"lumenvec_core_ann_eval_top1_matches_total",
			"Total sampled ANN queries whose top-1 result matched exact search.",
			nil,
			nil,
		),
		annEvalOverlapDesc: prometheus.NewDesc(
			"lumenvec_core_ann_eval_overlap_results_total",
			"Total overlapping result IDs between sampled ANN and exact results.",
			nil,
			nil,
		),
		annEvalComparedDesc: prometheus.NewDesc(
			"lumenvec_core_ann_eval_compared_results_total",
			"Total result slots compared between sampled ANN and exact results.",
			nil,
			nil,
		),
		cacheHitsDesc: prometheus.NewDesc(
			"lumenvec_core_cache_hits_total",
			"Total vector store cache hits.",
			nil,
			nil,
		),
		cacheMissesDesc: prometheus.NewDesc(
			"lumenvec_core_cache_misses_total",
			"Total vector store cache misses.",
			nil,
			nil,
		),
		cacheEvictionsDesc: prometheus.NewDesc(
			"lumenvec_core_cache_evictions_total",
			"Total vector store cache evictions.",
			nil,
			nil,
		),
		cacheItemsDesc: prometheus.NewDesc(
			"lumenvec_core_cache_items",
			"Current number of items stored in the vector cache.",
			nil,
			nil,
		),
		cacheBytesDesc: prometheus.NewDesc(
			"lumenvec_core_cache_bytes",
			"Current approximate number of bytes stored in the vector cache.",
			nil,
			nil,
		),
		diskFileBytesDesc: prometheus.NewDesc(
			"lumenvec_core_disk_file_bytes",
			"Current size in bytes of the disk-backed vector store file.",
			nil,
			nil,
		),
		diskRecordsDesc: prometheus.NewDesc(
			"lumenvec_core_disk_records",
			"Current number of live records in the disk-backed vector store.",
			nil,
			nil,
		),
		diskStaleRecordsDesc: prometheus.NewDesc(
			"lumenvec_core_disk_stale_records",
			"Current number of stale records waiting for or surviving compaction in the disk-backed vector store.",
			nil,
			nil,
		),
		diskCompactionsDesc: prometheus.NewDesc(
			"lumenvec_core_disk_compactions_total",
			"Total disk-backed vector store compactions.",
			nil,
			nil,
		),
		annConfigInfoDesc: prometheus.NewDesc(
			"lumenvec_core_ann_config_info",
			"Effective ANN configuration exposed as labels.",
			[]string{"profile", "m", "ef_construction", "ef_search"},
			nil,
		),
	}
}

func (c *coreMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.searchRequestsDesc
	ch <- c.exactSearchesDesc
	ch <- c.annSearchesDesc
	ch <- c.annSearchHitsDesc
	ch <- c.annSearchFallbacks
	ch <- c.annSearchErrorsDesc
	ch <- c.annCandidatesDesc
	ch <- c.annEvalSamplesDesc
	ch <- c.annEvalTop1MatchesDesc
	ch <- c.annEvalOverlapDesc
	ch <- c.annEvalComparedDesc
	ch <- c.cacheHitsDesc
	ch <- c.cacheMissesDesc
	ch <- c.cacheEvictionsDesc
	ch <- c.cacheItemsDesc
	ch <- c.cacheBytesDesc
	ch <- c.diskFileBytesDesc
	ch <- c.diskRecordsDesc
	ch <- c.diskStaleRecordsDesc
	ch <- c.diskCompactionsDesc
	ch <- c.annConfigInfoDesc
}

func (c *coreMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.service.Stats()
	ch <- prometheus.MustNewConstMetric(c.searchRequestsDesc, prometheus.CounterValue, float64(stats.SearchRequestsTotal))
	ch <- prometheus.MustNewConstMetric(c.exactSearchesDesc, prometheus.CounterValue, float64(stats.ExactSearchesTotal))
	ch <- prometheus.MustNewConstMetric(c.annSearchesDesc, prometheus.CounterValue, float64(stats.ANNSearchesTotal))
	ch <- prometheus.MustNewConstMetric(c.annSearchHitsDesc, prometheus.CounterValue, float64(stats.ANNSearchHitsTotal))
	ch <- prometheus.MustNewConstMetric(c.annSearchFallbacks, prometheus.CounterValue, float64(stats.ANNSearchFallbacks))
	ch <- prometheus.MustNewConstMetric(c.annSearchErrorsDesc, prometheus.CounterValue, float64(stats.ANNSearchErrorsTotal))
	ch <- prometheus.MustNewConstMetric(c.annCandidatesDesc, prometheus.CounterValue, float64(stats.ANNCandidatesReturned))
	ch <- prometheus.MustNewConstMetric(c.annEvalSamplesDesc, prometheus.CounterValue, float64(stats.ANNEvalSamplesTotal))
	ch <- prometheus.MustNewConstMetric(c.annEvalTop1MatchesDesc, prometheus.CounterValue, float64(stats.ANNEvalTop1Matches))
	ch <- prometheus.MustNewConstMetric(c.annEvalOverlapDesc, prometheus.CounterValue, float64(stats.ANNEvalOverlapResults))
	ch <- prometheus.MustNewConstMetric(c.annEvalComparedDesc, prometheus.CounterValue, float64(stats.ANNEvalComparedResults))
	ch <- prometheus.MustNewConstMetric(c.cacheHitsDesc, prometheus.CounterValue, float64(stats.CacheHitsTotal))
	ch <- prometheus.MustNewConstMetric(c.cacheMissesDesc, prometheus.CounterValue, float64(stats.CacheMissesTotal))
	ch <- prometheus.MustNewConstMetric(c.cacheEvictionsDesc, prometheus.CounterValue, float64(stats.CacheEvictionsTotal))
	ch <- prometheus.MustNewConstMetric(c.cacheItemsDesc, prometheus.GaugeValue, float64(stats.CacheItems))
	ch <- prometheus.MustNewConstMetric(c.cacheBytesDesc, prometheus.GaugeValue, float64(stats.CacheBytes))
	ch <- prometheus.MustNewConstMetric(c.diskFileBytesDesc, prometheus.GaugeValue, float64(stats.DiskFileBytes))
	ch <- prometheus.MustNewConstMetric(c.diskRecordsDesc, prometheus.GaugeValue, float64(stats.DiskRecords))
	ch <- prometheus.MustNewConstMetric(c.diskStaleRecordsDesc, prometheus.GaugeValue, float64(stats.DiskStaleRecords))
	ch <- prometheus.MustNewConstMetric(c.diskCompactionsDesc, prometheus.CounterValue, float64(stats.DiskCompactionsTotal))
	ch <- prometheus.MustNewConstMetric(
		c.annConfigInfoDesc,
		prometheus.GaugeValue,
		1,
		stats.ANNProfile,
		strconv.Itoa(stats.ANNM),
		strconv.Itoa(stats.ANNEfConstruction),
		strconv.Itoa(stats.ANNEfSearch),
	)
}

func newMetricsRegistry(service *core.Service) (*prometheus.CounterVec, *prometheus.HistogramVec, *prometheus.Registry) {
	requestTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lumenvec_http_requests_total",
			Help: "Total HTTP requests by method, route, and status.",
		},
		[]string{"method", "route", "status"},
	)

	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "lumenvec_http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)

	registry := prometheus.NewRegistry()
	registry.MustRegister(requestTotal, requestDuration)
	if service != nil {
		registry.MustRegister(newCoreMetricsCollector(service))
	}
	return requestTotal, requestDuration, registry
}
