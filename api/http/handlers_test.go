package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	api2 "lumenvec/internal/api"

	"github.com/gorilla/mux"
)

func testServer(t *testing.T) *api2.Server {
	t.Helper()
	base := t.TempDir()
	return api2.NewServerWithOptions(api2.ServerOptions{
		Port:         ":0",
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		SnapshotPath: filepath.Join(base, "snapshot.json"),
		WALPath:      filepath.Join(base, "wal.log"),
	})
}

func TestRegisterRoutesAndHealth(t *testing.T) {
	server := testServer(t)
	handlers := NewHandlers(server)
	r := mux.NewRouter()
	handlers.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandlerWrappers(t *testing.T) {
	server := testServer(t)
	handlers := NewHandlers(server)

	create := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/vectors", bytes.NewBufferString(`{"id":"a","values":[1,2,3]}`))
	handlers.AddVector(create, createReq)
	if create.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", create.Code)
	}

	list := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/vectors", nil)
	handlers.ListVectors(list, listReq)
	if list.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", list.Code)
	}
	var payload struct {
		Vectors []struct {
			ID string `json:"id"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(payload.Vectors) != 1 || payload.Vectors[0].ID != "a" {
		t.Fatalf("unexpected list: %+v", payload)
	}

	search := httptest.NewRecorder()
	searchReq := httptest.NewRequest(http.MethodPost, "/vectors/search", bytes.NewBufferString(`{"values":[1,2,3],"k":1}`))
	handlers.SearchVectors(search, searchReq)
	if search.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", search.Code)
	}

	get := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/vectors/a", nil)
	getReq = mux.SetURLVars(getReq, map[string]string{"id": "a"})
	handlers.GetVector(get, getReq)
	if get.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", get.Code)
	}

	del := httptest.NewRecorder()
	delReq := httptest.NewRequest(http.MethodDelete, "/vectors/a", nil)
	delReq = mux.SetURLVars(delReq, map[string]string{"id": "a"})
	handlers.DeleteVector(del, delReq)
	if del.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", del.Code)
	}
}
