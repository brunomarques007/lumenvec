package api

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newMiddlewareServer(t *testing.T, apiKey string) *Server {
	t.Helper()
	base := t.TempDir()
	return NewServerWithOptions(ServerOptions{
		Port:         ":0",
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		APIKey:       apiKey,
		RateLimitRPS: 1,
		SnapshotPath: filepath.Join(base, "snapshot.json"),
		WALPath:      filepath.Join(base, "wal.log"),
	})
}

func TestGetClientIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := getClientIP(req); got != "1.2.3.4" {
		t.Fatalf("getClientIP() = %q", got)
	}
}

func TestAuthMiddleware(t *testing.T) {
	server := newMiddlewareServer(t, "secret")
	nextCalled := false
	handler := server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/vectors", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/vectors", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !nextCalled {
		t.Fatal("expected authenticated request to pass")
	}
}

func TestRateLimitAndMetricsMiddleware(t *testing.T) {
	server := newMiddlewareServer(t, "")
	handler := server.rateLimitMiddleware(server.metricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})))

	req1 := httptest.NewRequest(http.MethodGet, "/vectors", nil)
	req1.RemoteAddr = "127.0.0.1:1234"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/vectors", nil)
	req2.RemoteAddr = "127.0.0.1:1234"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec2.Code)
	}
}

func TestMetricsRegistryAndStatusRecorder(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	sr.WriteHeader(http.StatusAccepted)
	if sr.status != http.StatusAccepted {
		t.Fatal("expected updated status")
	}
	total, duration, registry := newMetricsRegistry(nil)
	if total == nil || duration == nil || registry == nil {
		t.Fatal("expected metrics registry")
	}
}

func TestMiddlewarePublicPathsAndAccessLog(t *testing.T) {
	server := newMiddlewareServer(t, "secret")

	auth := server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	auth.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/vectors", nil)
	req.Header.Set("X-API-Key", "secret")
	auth.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	rate := server.rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rate.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var buf bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(oldWriter) })

	access := server.accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	access.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || buf.Len() == 0 {
		t.Fatal("expected access log output")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:9999"
	if got := getClientIP(req); got != "10.0.0.1" {
		t.Fatalf("unexpected client ip %q", got)
	}
}

func TestMetricsEndpointExposesCoreStats(t *testing.T) {
	base := t.TempDir()
	server := NewServerWithOptions(ServerOptions{
		Port:              ":0",
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		MaxVectorDim:      8,
		MaxK:              5,
		SnapshotPath:      filepath.Join(base, "snapshot.json"),
		WALPath:           filepath.Join(base, "wal.log"),
		SnapshotEvery:     2,
		SearchMode:        "ann",
		ANNProfile:        "balanced",
		ANNEvalSampleRate: 100,
		CacheEnabled:      true,
		CacheMaxBytes:     2048,
		CacheMaxItems:     2,
		CacheTTL:          time.Minute,
	})
	t.Cleanup(func() { _ = server.service.Close() })

	if err := server.service.AddVector("a", []float64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.service.Search([]float64{1, 2, 3}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := server.service.GetVector("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := server.service.GetVector("a"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, metric := range []string{
		"lumenvec_core_search_requests_total",
		"lumenvec_core_ann_searches_total",
		"lumenvec_core_ann_search_hits_total",
		"lumenvec_core_ann_candidates_returned_total",
		"lumenvec_core_ann_eval_samples_total",
		"lumenvec_core_ann_eval_top1_matches_total",
		"lumenvec_core_ann_eval_overlap_results_total",
		"lumenvec_core_ann_eval_compared_results_total",
		"lumenvec_core_cache_hits_total",
		"lumenvec_core_cache_misses_total",
		"lumenvec_core_cache_items",
		"lumenvec_core_cache_bytes",
		"lumenvec_core_ann_config_info",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("expected metrics output to contain %q", metric)
		}
	}
}

func TestMetricsEndpointExposesDiskStoreStats(t *testing.T) {
	base := t.TempDir()
	server := NewServerWithOptions(ServerOptions{
		Port:           ":0",
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   5 * time.Second,
		MaxVectorDim:   8,
		MaxK:           5,
		SnapshotPath:   filepath.Join(base, "snapshot.json"),
		WALPath:        filepath.Join(base, "wal.log"),
		SnapshotEvery:  2,
		SearchMode:     "ann",
		ANNProfile:     "quality",
		ANNM:           24,
		ANNEfConstruct: 96,
		ANNEfSearch:    96,
		VectorStore:    "disk",
		VectorPath:     filepath.Join(base, "vectors"),
	})
	t.Cleanup(func() { _ = server.service.Close() })

	for i := 0; i < 6; i++ {
		id := "vec-" + string(rune('a'+i))
		if err := server.service.AddVector(id, []float64{float64(i), 2, 3}); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if err := server.service.DeleteVector(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := server.service.AddVector("steady", []float64{9, 9, 9}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	stats := server.service.Stats()
	if stats.ANNProfile != "quality" {
		t.Fatalf("expected service stats ann profile quality, got %q", stats.ANNProfile)
	}
	for _, metric := range []string{
		"lumenvec_core_disk_file_bytes",
		"lumenvec_core_disk_records",
		"lumenvec_core_disk_stale_records",
		"lumenvec_core_disk_compactions_total",
		"lumenvec_core_ann_config_info",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("expected metrics output to contain %q", metric)
		}
	}
	if !strings.Contains(body, "quality") {
		t.Fatal("expected ann config metric output to include the configured profile")
	}
}
