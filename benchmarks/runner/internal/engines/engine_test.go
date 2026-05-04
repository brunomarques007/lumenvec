package engines

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lumenvec/benchmarks/runner/internal/dataset"
)

func TestNewEngine(t *testing.T) {
	for _, name := range []string{"lumenvec-exact", "lumenvec-ann", "lumenvec-http-ann-fast", "lumenvec-http-ann-quality", "lumenvec-grpc-exact", "lumenvec-grpc-ann", "lumenvec-grpc-ann-fast", "lumenvec-grpc-ann-quality", "qdrant", "chroma", "weaviate", "pgvector", "pgvector-hnsw", "pgvector-ivfflat"} {
		eng, err := New(Config{Name: name, Dimension: 3, Collection: "test"})
		if err != nil {
			t.Fatalf("New(%q) error = %v", name, err)
		}
		if eng.Name() == "" || eng.Profile() == "" {
			t.Fatalf("New(%q) returned incomplete engine metadata", name)
		}
	}
	if _, err := New(Config{Name: "qdrant"}); err == nil {
		t.Fatal("expected qdrant dimension validation error")
	}
	if _, err := New(Config{Name: "pgvector"}); err == nil {
		t.Fatal("expected pgvector dimension validation error")
	}
	if _, err := New(Config{Name: "weaviate"}); err == nil {
		t.Fatal("expected weaviate dimension validation error")
	}
	if _, err := New(Config{Name: "unknown"}); err == nil {
		t.Fatal("expected unsupported engine error")
	}
}

func TestQdrantLifecycleRequests(t *testing.T) {
	var sawSetup, sawInsert, sawSearch, sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/collections/bench":
			sawSetup = true
			var body struct {
				Vectors struct {
					Size     int    `json:"size"`
					Distance string `json:"distance"`
				} `json:"vectors"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Vectors.Size != 3 || body.Vectors.Distance != "Euclid" {
				t.Fatalf("unexpected setup body: %+v", body)
			}
			_, _ = w.Write([]byte(`{"result":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/bench/points":
			sawInsert = true
			if r.URL.Query().Get("wait") != "true" {
				t.Fatalf("expected wait=true, got %q", r.URL.RawQuery)
			}
			var body struct {
				Points []qdrantPoint `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Points) != 1 || body.Points[0].Payload["external_id"] != "vec-000000001" {
				t.Fatalf("unexpected insert body: %+v", body)
			}
			_, _ = w.Write([]byte(`{"result":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/bench/points/query":
			sawSearch = true
			var body struct {
				Query       []float64 `json:"query"`
				Limit       int       `json:"limit"`
				WithPayload bool      `json:"with_payload"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Query) != 3 || body.Limit != 1 || !body.WithPayload {
				t.Fatalf("unexpected query body: %+v", body)
			}
			_, _ = w.Write([]byte(`{"result":{"points":[{"id":1,"score":0.25,"payload":{"external_id":"vec-000000001"}}]}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/collections/bench":
			sawDelete = true
			_, _ = w.Write([]byte(`{"result":true}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	eng, err := New(Config{
		Name:       "qdrant",
		Dimension:  3,
		QdrantURL:  server.URL,
		Collection: "bench",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := eng.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := eng.Insert(ctx, []dataset.Vector{{ID: "vec-000000001", Values: []float64{1, 2, 3}}}); err != nil {
		t.Fatal(err)
	}
	results, err := eng.Search(ctx, []float64{1, 2, 3}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "vec-000000001" || results[0].Distance != 0.25 {
		t.Fatalf("unexpected results: %+v", results)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if !sawSetup || !sawInsert || !sawSearch || !sawDelete {
		t.Fatalf("missing lifecycle request: setup=%v insert=%v search=%v delete=%v", sawSetup, sawInsert, sawSearch, sawDelete)
	}
}

func TestQdrantErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	eng, err := New(Config{Name: "qdrant", Dimension: 3, QdrantURL: server.URL, Collection: "bench"})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Setup(context.Background()); err == nil {
		t.Fatal("expected qdrant setup error")
	}
}

func TestQdrantNumericIDIsStable(t *testing.T) {
	id1 := qdrantNumericID("vec-000000001")
	id2 := qdrantNumericID("vec-000000001")
	id3 := qdrantNumericID("vec-000000002")
	if id1 == 0 || id1 != id2 || id1 == id3 {
		t.Fatalf("unexpected qdrant numeric ids: %d %d %d", id1, id2, id3)
	}
}

func TestChromaLifecycleRequests(t *testing.T) {
	var sawHeartbeat, sawSetup, sawInsert, sawSearch, sawDelete bool
	queryRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/heartbeat":
			sawHeartbeat = true
			_, _ = w.Write([]byte(`{"nanosecond heartbeat":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/tenants/default_tenant/databases/default_database/collections":
			sawSetup = true
			var body struct {
				Name        string         `json:"name"`
				GetOrCreate bool           `json:"get_or_create"`
				Metadata    map[string]any `json:"metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Name != "bench" || !body.GetOrCreate || body.Metadata["hnsw:space"] != "l2" {
				t.Fatalf("unexpected setup body: %+v", body)
			}
			_, _ = w.Write([]byte(`{"id":"collection-id","name":"bench"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/tenants/default_tenant/databases/default_database/collections/collection-id/add":
			sawInsert = true
			var body struct {
				IDs        []string    `json:"ids"`
				Embeddings [][]float64 `json:"embeddings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.IDs) != 1 || body.IDs[0] != "vec-000000001" || len(body.Embeddings[0]) != 3 {
				t.Fatalf("unexpected insert body: %+v", body)
			}
			_, _ = w.Write([]byte(`true`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/tenants/default_tenant/databases/default_database/collections/collection-id/query":
			sawSearch = true
			queryRequests++
			var body struct {
				QueryEmbeddings [][]float64 `json:"query_embeddings"`
				NResults        int         `json:"n_results"`
				Include         []string    `json:"include"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.NResults != 1 || len(body.Include) != 1 || body.Include[0] != "distances" {
				t.Fatalf("unexpected query body: %+v", body)
			}
			switch len(body.QueryEmbeddings) {
			case 1:
				_, _ = w.Write([]byte(`{"ids":[["vec-000000001"]],"distances":[[0.25]]}`))
			case 2:
				_, _ = w.Write([]byte(`{"ids":[["vec-000000001"],["vec-000000002"]],"distances":[[0.25],[0.5]]}`))
			default:
				t.Fatalf("unexpected query embedding count: %d", len(body.QueryEmbeddings))
			}
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/tenants/default_tenant/databases/default_database/collections/collection-id":
			sawDelete = true
			_, _ = w.Write([]byte(`true`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	eng, err := New(Config{
		Name:       "chroma",
		Dimension:  3,
		ChromaURL:  server.URL,
		Collection: "bench",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := eng.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := eng.Insert(ctx, []dataset.Vector{{ID: "vec-000000001", Values: []float64{1, 2, 3}}}); err != nil {
		t.Fatal(err)
	}
	results, err := eng.Search(ctx, []float64{1, 2, 3}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "vec-000000001" || results[0].Distance != 0.25 {
		t.Fatalf("unexpected results: %+v", results)
	}
	batchResults, supported, err := eng.SearchBatch(ctx, []dataset.Vector{
		{ID: "q1", Values: []float64{1, 2, 3}},
		{ID: "q2", Values: []float64{2, 3, 4}},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !supported {
		t.Fatal("expected Chroma batch search support")
	}
	if len(batchResults) != 2 || len(batchResults[0]) != 1 || batchResults[0][0].ID != "vec-000000001" || batchResults[1][0].ID != "vec-000000002" {
		t.Fatalf("unexpected batch results: %+v", batchResults)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if !sawHeartbeat || !sawSetup || !sawInsert || !sawSearch || !sawDelete {
		t.Fatalf("missing lifecycle request: heartbeat=%v setup=%v insert=%v search=%v delete=%v", sawHeartbeat, sawSetup, sawInsert, sawSearch, sawDelete)
	}
	if queryRequests != 2 {
		t.Fatalf("expected single and batch query requests, got %d", queryRequests)
	}
}

func TestWeaviateLifecycleRequests(t *testing.T) {
	var sawReady, sawSetup, sawInsert, sawSearch, sawDelete bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/.well-known/ready":
			sawReady = true
			_, _ = w.Write([]byte(`OK`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/schema":
			sawSetup = true
			var body struct {
				Class             string         `json:"class"`
				Vectorizer        string         `json:"vectorizer"`
				VectorIndexType   string         `json:"vectorIndexType"`
				VectorIndexConfig map[string]any `json:"vectorIndexConfig"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Class != "Bench" || body.Vectorizer != "none" || body.VectorIndexType != "hnsw" || body.VectorIndexConfig["distance"] != "l2-squared" {
				t.Fatalf("unexpected setup body: %+v", body)
			}
			_, _ = w.Write([]byte(`{"class":"Bench"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/batch/objects":
			sawInsert = true
			var body struct {
				Objects []weaviateObject `json:"objects"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Objects) != 1 || body.Objects[0].Class != "Bench" || body.Objects[0].Properties["external_id"] != "vec-000000001" || len(body.Objects[0].Vector) != 3 {
				t.Fatalf("unexpected insert body: %+v", body)
			}
			_, _ = w.Write([]byte(`{"objects":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/graphql":
			sawSearch = true
			var body struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body.Query, "Bench(") || !strings.Contains(body.Query, "nearVector") || !strings.Contains(body.Query, "external_id") {
				t.Fatalf("unexpected graphql query: %s", body.Query)
			}
			_, _ = w.Write([]byte(`{"data":{"Get":{"Bench":[{"external_id":"vec-000000001","_additional":{"distance":0.25}}]}}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/schema/Bench":
			sawDelete = true
			_, _ = w.Write([]byte(`{"status":"SUCCESS"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	eng, err := New(Config{
		Name:        "weaviate",
		Dimension:   3,
		WeaviateURL: server.URL,
		Collection:  "bench",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := eng.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := eng.Insert(ctx, []dataset.Vector{{ID: "vec-000000001", Values: []float64{1, 2, 3}}}); err != nil {
		t.Fatal(err)
	}
	results, err := eng.Search(ctx, []float64{1, 2, 3}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "vec-000000001" || results[0].Distance != 0.25 {
		t.Fatalf("unexpected results: %+v", results)
	}
	if _, supported, err := eng.SearchBatch(ctx, []dataset.Vector{{ID: "q1", Values: []float64{1, 2, 3}}}, 1); err != nil || supported {
		t.Fatalf("expected unsupported batch search, supported=%v err=%v", supported, err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if !sawReady || !sawSetup || !sawInsert || !sawSearch || !sawDelete {
		t.Fatalf("missing lifecycle request: ready=%v setup=%v insert=%v search=%v delete=%v", sawReady, sawSetup, sawInsert, sawSearch, sawDelete)
	}
}

func TestPGVectorHelpers(t *testing.T) {
	if got := vectorLiteral([]float64{1, 2.5, -3}); got != "[1,2.5,-3]" {
		t.Fatalf("unexpected vector literal: %q", got)
	}
	if got := safeSQLIdentifier("bench-pgvector/run:1"); got != "bench_pgvector_run_1" {
		t.Fatalf("unexpected identifier: %q", got)
	}
	if got := safeSQLIdentifier("123"); got != "bench_123" {
		t.Fatalf("unexpected numeric identifier: %q", got)
	}
	if got := shortIdentifier("abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz1234567890"); len(got) > 55 {
		t.Fatalf("expected short identifier, got len=%d", len(got))
	}
	longName := "bench_20260426_214606_pgvector_ivfflat_b1000_sb100_run1_ivfflat_l100_p10_idx"
	if got := shortIdentifierWithHash(longName); len(got) > 55 || got == shortIdentifier(longName) {
		t.Fatalf("expected hashed short identifier, got %q len=%d", got, len(got))
	}
	eng, err := New(Config{Name: "pgvector-hnsw", Dimension: 3, Collection: "bench"})
	if err != nil {
		t.Fatal(err)
	}
	if eng.Profile() != "hnsw-m16-ef64" {
		t.Fatalf("unexpected hnsw profile: %q", eng.Profile())
	}
}

func TestWeaviateHelpers(t *testing.T) {
	if got := weaviateClassName("bench-weaviate/run:1"); got != "BenchWeaviateRun1" {
		t.Fatalf("unexpected class name: %q", got)
	}
	id1 := weaviateUUID("vec-000000001")
	id2 := weaviateUUID("vec-000000001")
	id3 := weaviateUUID("vec-000000002")
	if id1 == "" || id1 != id2 || id1 == id3 || len(id1) != 36 {
		t.Fatalf("unexpected weaviate UUIDs: %q %q %q", id1, id2, id3)
	}
}
