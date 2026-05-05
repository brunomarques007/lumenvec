package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lumenvec/internal/core"

	"google.golang.org/grpc"
)

func newAPITestServer(t *testing.T) *Server {
	t.Helper()
	base := t.TempDir()
	return NewServerWithOptions(ServerOptions{
		Port:          ":0",
		ReadTimeout:   5 * time.Second,
		WriteTimeout:  5 * time.Second,
		MaxBodyBytes:  1 << 20,
		MaxVectorDim:  16,
		MaxK:          5,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "exact",
	})
}

func TestApplyDefaults(t *testing.T) {
	opts := applyDefaults(ServerOptions{})
	if opts.Port != ":19190" || opts.SearchMode != "exact" {
		t.Fatal("unexpected defaults")
	}
	if opts.Protocol != "http" || opts.GRPCEnabled {
		t.Fatal("unexpected transport defaults")
	}
	if opts.ANNM != 16 || opts.ANNEfConstruct != 64 || opts.ANNEfSearch != 64 {
		t.Fatal("unexpected ann defaults")
	}

	opts = applyDefaults(ServerOptions{
		Protocol:          "grpc",
		Port:              "2000",
		GRPCPort:          "2001",
		DisableRateLimit:  true,
		SearchMode:        "ANN",
		ANNEvalSampleRate: -1,
		CacheMaxItems:     -1,
		CacheMaxBytes:     -1,
		CacheTTL:          -1,
	})
	if opts.Port != ":2000" || opts.GRPCPort != ":2001" || !opts.GRPCEnabled || opts.RateLimitRPS != 0 {
		t.Fatalf("unexpected explicit defaults: %+v", opts)
	}
	if opts.SearchMode != "ann" || opts.ANNEvalSampleRate != 0 || opts.CacheMaxItems != 1024 || opts.CacheMaxBytes != 8<<20 || opts.CacheTTL != 15*time.Minute {
		t.Fatalf("unexpected normalized defaults: %+v", opts)
	}
}

func TestServerHandlersLifecycleAndBatch(t *testing.T) {
	server := newAPITestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/vectors", bytes.NewBufferString(`{"id":"a","values":[1,2,3]}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/vectors/a", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/vectors", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var listPayload struct {
		Vectors []struct {
			ID     string    `json:"id"`
			Values []float64 `json:"values"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("json.Unmarshal() list vectors: %v", err)
	}
	if len(listPayload.Vectors) != 1 || listPayload.Vectors[0].ID != "a" {
		t.Fatalf("unexpected list after first upsert: %+v", listPayload)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/vectors/search", bytes.NewBufferString(`{"values":[1,2,3],"k":1}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/vectors/batch", bytes.NewBufferString(`{"vectors":[{"id":"b","values":[4,5,6]}]}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/vectors/search/batch", bytes.NewBufferString(`{"queries":[{"id":"q1","values":[4,5,6],"k":1}]}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var got []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(got) != 1 || got[0]["id"] != "q1" {
		t.Fatal("unexpected batch search payload")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/vectors", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 list, got %d", rec.Code)
	}
	var listPayload2 struct {
		Vectors []struct {
			ID string `json:"id"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listPayload2); err != nil {
		t.Fatalf("json.Unmarshal() list vectors: %v", err)
	}
	if len(listPayload2.Vectors) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(listPayload2.Vectors))
	}
	if listPayload2.Vectors[0].ID != "a" || listPayload2.Vectors[1].ID != "b" {
		t.Fatalf("expected sorted ids a,b, got %+v", listPayload2.Vectors)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/vectors/a", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestServerV1RoutesUseJSONErrorEnvelope(t *testing.T) {
	server := newAPITestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/vectors", bytes.NewBufferString(`{"id":"a","values":[1,2,3]}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/vectors/search", bytes.NewBufferString(`{"values":[1,2,3],"k":1}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var hits []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil {
		t.Fatalf("json.Unmarshal() search response: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "a" {
		t.Fatalf("unexpected v1 search results: %+v", hits)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/vectors", bytes.NewBufferString(`{"id":"","values":[1]}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != contentTypeJSON {
		t.Fatalf("expected JSON content type, got %q", got)
	}
	var payload errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error envelope: %v", err)
	}
	if payload.Error.Code != "invalid_argument" || payload.Error.Message != "id is required" {
		t.Fatalf("unexpected error envelope: %+v", payload)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/vectors/missing", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() not found envelope: %v", err)
	}
	if payload.Error.Code != "not_found" {
		t.Fatalf("unexpected not found envelope: %+v", payload)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/vectors/", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected empty ID 404, got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() empty ID envelope: %v", err)
	}
	if payload.Error.Code != "not_found" {
		t.Fatalf("unexpected empty ID envelope: %+v", payload)
	}
}

func TestRouterReturnsRequestIDHeader(t *testing.T) {
	server := newAPITestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set(requestIDHeader, "external-id-123")
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get(requestIDHeader); got != "external-id-123" {
		t.Fatalf("expected echoed request id, got %q", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/vectors", bytes.NewBufferString(`{`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if got := rec.Header().Get(requestIDHeader); got == "" {
		t.Fatal("expected generated request id")
	}
}

func TestServerValidationErrors(t *testing.T) {
	server := newAPITestServer(t)

	cases := []struct {
		method string
		path   string
		body   string
		code   int
	}{
		{http.MethodPost, "/vectors", "{", http.StatusBadRequest},
		{http.MethodPost, "/vectors", `{"id":"","values":[1]}`, http.StatusBadRequest},
		{http.MethodPost, "/vectors", `{"id":"too-wide","values":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17]}`, http.StatusBadRequest},
		{http.MethodPost, "/vectors/batch", "{", http.StatusBadRequest},
		{http.MethodPost, "/vectors/batch", `{"vectors":[{"id":"bad","values":[]}]}`, http.StatusBadRequest},
		{http.MethodPost, "/vectors/search", `{"values":[],"k":1}`, http.StatusBadRequest},
		{http.MethodPost, "/vectors/search/batch", "{", http.StatusBadRequest},
		{http.MethodPost, "/vectors/search/batch", `{"queries":[]}`, http.StatusBadRequest},
		{http.MethodPost, "/vectors/search/batch", `{"queries":[{"id":"q","values":[1],"k":0}]}`, http.StatusBadRequest},
	}

	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		server.Router().ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Fatalf("%s %s: expected %d, got %d", tc.method, tc.path, tc.code, rec.Code)
		}
	}
}

func TestHealthRouterAndStatusMapping(t *testing.T) {
	server := newAPITestServer(t)
	if server.Router() == nil {
		t.Fatal("expected router")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	server.HealthHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/livez", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("expected livez ok, got code=%d body=%q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ready" {
		t.Fatalf("expected readyz ready, got code=%d body=%q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/readyz", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ready" {
		t.Fatalf("expected v1 readyz ready, got code=%d body=%q", rec.Code, rec.Body.String())
	}

	if got := statusFromServiceError(core.ErrInvalidValues); got != http.StatusBadRequest {
		t.Fatalf("unexpected status %d", got)
	}
	if got := statusFromServiceError(core.ErrInvalidID); got != http.StatusBadRequest {
		t.Fatalf("unexpected invalid id status %d", got)
	}
	if got := statusFromServiceError(core.ErrInvalidK); got != http.StatusBadRequest {
		t.Fatalf("unexpected invalid k status %d", got)
	}
	if got := statusFromServiceError(core.ErrVectorDimTooHigh); got != http.StatusBadRequest {
		t.Fatalf("unexpected dim status %d", got)
	}
	if got := statusFromServiceError(core.ErrKTooHigh); got != http.StatusBadRequest {
		t.Fatalf("unexpected max k status %d", got)
	}
	if got := statusFromServiceError(errors.New("x")); got != http.StatusInternalServerError {
		t.Fatalf("unexpected status %d", got)
	}

	httpServer := server.httpServer()
	if httpServer.Addr != server.port || httpServer.Handler == nil {
		t.Fatal("expected configured http server")
	}
}

func TestReadinessHandlerReportsStorageFailure(t *testing.T) {
	base := t.TempDir()
	notDir := filepath.Join(base, "not-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	server := NewServerWithOptions(ServerOptions{
		Port:         ":0",
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		MaxBodyBytes: 1 << 20,
		MaxVectorDim: 16,
		MaxK:         5,
		SnapshotPath: filepath.Join(notDir, "snapshot.json"),
		WALPath:      filepath.Join(notDir, "wal.log"),
		VectorStore:  "memory",
		VectorPath:   filepath.Join(base, "vectors"),
		SearchMode:   "exact",
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/readyz", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	var payload errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() readiness error: %v", err)
	}
	if payload.Error.Code != "not_ready" {
		t.Fatalf("unexpected readiness error: %+v", payload)
	}
}

func TestNewServerAndStart(t *testing.T) {
	server := NewServer("19190")
	if server == nil {
		t.Fatal("expected server")
	}

	oldListen := listenAndServeFunc
	oldListenTLS := listenAndServeTLSFunc
	oldFatal := logFatalfAPI
	oldPrintf := logPrintfAPI
	t.Cleanup(func() {
		listenAndServeFunc = oldListen
		listenAndServeTLSFunc = oldListenTLS
		logFatalfAPI = oldFatal
		logPrintfAPI = oldPrintf
	})

	var loggedStart bool
	var fatalCalled bool
	logPrintfAPI = func(string, ...interface{}) { loggedStart = true }
	listenAndServeFunc = func(*http.Server) error { return errors.New("boom") }
	logFatalfAPI = func(string, ...interface{}) { fatalCalled = true }

	server.Start()
	if !loggedStart || !fatalCalled {
		t.Fatal("expected start path logging and fatal")
	}
}

func TestServerStartWithGRPCEnabled(t *testing.T) {
	server := newAPITestServer(t)
	server.protocol = "grpc"
	server.grpcEnabled = true
	server.grpcPort = ":19191"

	oldListen := listenAndServeFunc
	oldListenTLS := listenAndServeTLSFunc
	oldFatal := logFatalfAPI
	oldPrintf := logPrintfAPI
	oldGRPCListen := grpcListenFunc
	oldGRPCServe := grpcServeFunc
	t.Cleanup(func() {
		listenAndServeFunc = oldListen
		listenAndServeTLSFunc = oldListenTLS
		logFatalfAPI = oldFatal
		logPrintfAPI = oldPrintf
		grpcListenFunc = oldGRPCListen
		grpcServeFunc = oldGRPCServe
	})

	var grpcBound bool
	grpcServed := make(chan struct{}, 1)
	var fatalCalled bool
	logPrintfAPI = func(string, ...interface{}) {}
	logFatalfAPI = func(string, ...interface{}) { fatalCalled = true }
	grpcListenFunc = func(network, address string) (net.Listener, error) {
		grpcBound = true
		return newStubListener(), nil
	}
	grpcServeFunc = func(*grpc.Server, net.Listener) error {
		grpcServed <- struct{}{}
		return net.ErrClosed
	}
	server.Start()
	if !grpcBound {
		t.Fatal("expected grpc listener to bind")
	}
	select {
	case <-grpcServed:
	case <-time.After(time.Second):
		t.Fatal("expected grpc listener and server to start")
	}
	if fatalCalled {
		t.Fatal("did not expect fatal path when grpc exits with net.ErrClosed")
	}
}

func TestServerStartFailsWhenGRPCBindFails(t *testing.T) {
	server := newAPITestServer(t)
	server.protocol = "grpc"
	server.grpcEnabled = true

	oldListen := listenAndServeFunc
	oldListenTLS := listenAndServeTLSFunc
	oldFatal := logFatalfAPI
	oldPrintf := logPrintfAPI
	oldGRPCListen := grpcListenFunc
	t.Cleanup(func() {
		listenAndServeFunc = oldListen
		listenAndServeTLSFunc = oldListenTLS
		logFatalfAPI = oldFatal
		logPrintfAPI = oldPrintf
		grpcListenFunc = oldGRPCListen
	})

	var fatalCalled bool
	logPrintfAPI = func(string, ...interface{}) {}
	logFatalfAPI = func(string, ...interface{}) { fatalCalled = true }
	grpcListenFunc = func(string, string) (net.Listener, error) { return nil, errors.New("grpc bind error") }

	server.Start()
	if !fatalCalled {
		t.Fatal("expected grpc bind failure to trigger fatal path")
	}
}

func TestServerStartFailsWhenGRPCServeFails(t *testing.T) {
	server := newAPITestServer(t)
	server.protocol = "grpc"
	server.grpcEnabled = true

	oldFatal := logFatalfAPI
	oldPrintf := logPrintfAPI
	oldGRPCListen := grpcListenFunc
	oldGRPCServe := grpcServeFunc
	t.Cleanup(func() {
		logFatalfAPI = oldFatal
		logPrintfAPI = oldPrintf
		grpcListenFunc = oldGRPCListen
		grpcServeFunc = oldGRPCServe
	})

	var fatalCalled bool
	logPrintfAPI = func(string, ...interface{}) {}
	logFatalfAPI = func(string, ...interface{}) { fatalCalled = true }
	grpcListenFunc = func(string, string) (net.Listener, error) { return newStubListener(), nil }
	grpcServeFunc = func(*grpc.Server, net.Listener) error { return errors.New("serve failed") }

	server.Start()
	if !fatalCalled {
		t.Fatal("expected grpc serve failure to trigger fatal path")
	}
}

func TestServerStartWithHTTPSTLS(t *testing.T) {
	server := newAPITestServer(t)
	server.tlsEnabled = true
	server.tlsCertFile = "cert.pem"
	server.tlsKeyFile = "key.pem"

	oldListen := listenAndServeFunc
	oldListenTLS := listenAndServeTLSFunc
	oldFatal := logFatalfAPI
	oldPrintf := logPrintfAPI
	t.Cleanup(func() {
		listenAndServeFunc = oldListen
		listenAndServeTLSFunc = oldListenTLS
		logFatalfAPI = oldFatal
		logPrintfAPI = oldPrintf
	})

	var tlsCalled bool
	logPrintfAPI = func(string, ...interface{}) {}
	logFatalfAPI = func(string, ...interface{}) {}
	listenAndServeTLSFunc = func(*http.Server, string, string) error {
		tlsCalled = true
		return nil
	}

	server.Start()
	if !tlsCalled {
		t.Fatal("expected https start path")
	}
}

func TestServerRunStopsHTTPOnContextCancel(t *testing.T) {
	server := newAPITestServer(t)

	oldListen := listenAndServeFunc
	oldPrintf := logPrintfAPI
	t.Cleanup(func() {
		listenAndServeFunc = oldListen
		logPrintfAPI = oldPrintf
	})

	listening := make(chan struct{})
	release := make(chan struct{})
	logPrintfAPI = func(string, ...interface{}) {}
	listenAndServeFunc = func(*http.Server) error {
		close(listening)
		<-release
		return http.ErrServerClosed
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()

	<-listening
	cancel()
	close(release)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Run to stop after context cancel")
	}
}

func TestServerHandlerErrorBranches(t *testing.T) {
	server := newAPITestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/vectors", bytes.NewBufferString(`{"id":"dup","values":[1,2,3]}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/vectors", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/vectors/dup", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/vectors/", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/vectors", bytes.NewBufferString(`{"id":"dup","values":[1,2,3]}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/vectors/batch", bytes.NewBufferString(`{"vectors":[]}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/vectors/missing", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/vectors/missing", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	writeJSON(rec, http.StatusAccepted, map[string]string{"ok": "true"})
	if rec.Code != http.StatusAccepted || rec.Header().Get("Content-Type") != contentTypeJSON {
		t.Fatalf("expected explicit json status/header, got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestListVectorsPaginationInputs(t *testing.T) {
	server := newAPITestServer(t)
	for _, id := range []string{"a", "b", "c"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/vectors", bytes.NewBufferString(`{"id":"`+id+`","values":[1]}`))
		server.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("add %s code = %d", id, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/vectors?limit=999999&ids_only=yes", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected capped list success, got %d", rec.Code)
	}

	cursor := encodeListVectorsCursor("a")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/vectors?cursor="+cursor+"&ids_only=1", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected cursor list success, got %d", rec.Code)
	}

	paddedCursor := base64.URLEncoding.EncodeToString([]byte("a"))
	if decoded, err := decodeListVectorsCursor(paddedCursor); err != nil || decoded != "a" {
		t.Fatalf("decode padded cursor = %q, %v", decoded, err)
	}

	for _, query := range []string{"limit=bad", "limit=0", "cursor=!"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/vectors?"+query, nil)
		server.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", query, rec.Code)
		}
	}
}

func TestTrustedProxyParsingAndChecks(t *testing.T) {
	parsed := parseTrustedProxies([]string{"", "bad", "10.0.0.1", "192.168.0.0/24"})
	if len(parsed) != 2 {
		t.Fatalf("expected 2 trusted proxy entries, got %d", len(parsed))
	}
	server := newAPITestServer(t)
	server.trustedCIDRs = parsed
	if !server.isTrustedProxy("10.0.0.1:1234") {
		t.Fatal("expected host:port proxy to be trusted")
	}
	if !server.isTrustedProxy("192.168.0.44") {
		t.Fatal("expected raw proxy address to be trusted")
	}
	if server.isTrustedProxy("not an ip") {
		t.Fatal("did not expect invalid remote address to be trusted")
	}
}

type stubListener struct{}

func newStubListener() net.Listener { return &stubListener{} }

func (l *stubListener) Accept() (net.Conn, error) { return nil, errors.New("closed") }
func (l *stubListener) Close() error              { return nil }
func (l *stubListener) Addr() net.Addr            { return &net.TCPAddr{} }
