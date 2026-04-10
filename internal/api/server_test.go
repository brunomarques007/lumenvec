package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
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
	req = httptest.NewRequest(http.MethodDelete, "/vectors/a", nil)
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
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
		{http.MethodPost, "/vectors/search", `{"values":[],"k":1}`, http.StatusBadRequest},
		{http.MethodPost, "/vectors/search/batch", "{", http.StatusBadRequest},
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

	if got := statusFromServiceError(core.ErrInvalidValues); got != http.StatusBadRequest {
		t.Fatalf("unexpected status %d", got)
	}
	if got := statusFromServiceError(errors.New("x")); got != http.StatusInternalServerError {
		t.Fatalf("unexpected status %d", got)
	}

	httpServer := server.httpServer()
	if httpServer.Addr != server.port || httpServer.Handler == nil {
		t.Fatal("expected configured http server")
	}
}

func TestNewServerAndStart(t *testing.T) {
	server := NewServer("19190")
	if server == nil {
		t.Fatal("expected server")
	}

	oldListen := listenAndServeFunc
	oldFatal := logFatalfAPI
	oldPrintf := logPrintfAPI
	t.Cleanup(func() {
		listenAndServeFunc = oldListen
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
	oldFatal := logFatalfAPI
	oldPrintf := logPrintfAPI
	oldGRPCListen := grpcListenFunc
	oldGRPCServe := grpcServeFunc
	t.Cleanup(func() {
		listenAndServeFunc = oldListen
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
	oldFatal := logFatalfAPI
	oldPrintf := logPrintfAPI
	oldGRPCListen := grpcListenFunc
	t.Cleanup(func() {
		listenAndServeFunc = oldListen
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

func TestServerHandlerErrorBranches(t *testing.T) {
	server := newAPITestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/vectors", bytes.NewBufferString(`{"id":"dup","values":[1,2,3]}`))
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
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
}

type stubListener struct{}

func newStubListener() net.Listener { return &stubListener{} }

func (l *stubListener) Accept() (net.Conn, error) { return nil, errors.New("closed") }
func (l *stubListener) Close() error              { return nil }
func (l *stubListener) Addr() net.Addr            { return &net.TCPAddr{} }
