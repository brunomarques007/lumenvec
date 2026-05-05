package engines

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"lumenvec/benchmarks/runner/internal/dataset"
	"lumenvec/internal/core"
	"lumenvec/internal/index"
	"lumenvec/internal/index/ann"
	lumenclient "lumenvec/pkg/client"

	_ "github.com/lib/pq"
)

type Engine interface {
	Name() string
	Profile() string
	Transport() string
	Setup(context.Context) error
	Insert(context.Context, []dataset.Vector) error
	BuildIndex(context.Context) (bool, error)
	Search(context.Context, []float64, int) ([]core.SearchResult, error)
	SearchBatch(context.Context, []dataset.Vector, int) ([][]core.SearchResult, bool, error)
	Close() error
}

type Config struct {
	Name         string
	Dimension    int
	MaxK         int
	QdrantURL    string
	ChromaURL    string
	WeaviateURL  string
	LumenVecURL  string
	LumenVecGRPC string
	PGVectorDSN  string
	Collection   string
}

type LumenVec struct {
	mode    string
	service *core.Service
	tempDir string
}

func New(cfg Config) (Engine, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Name)) {
	case "lumenvec-exact":
		return &LumenVec{mode: "exact"}, nil
	case "lumenvec-ann":
		return &LumenVec{mode: "ann"}, nil
	case "lumenvec-http-exact":
		return &LumenVecHTTP{
			baseURL: strings.TrimRight(firstNonEmpty(cfg.LumenVecURL, "http://localhost:19290"), "/"),
			profile: "exact",
		}, nil
	case "lumenvec-http-ann":
		return &LumenVecHTTP{
			baseURL: strings.TrimRight(firstNonEmpty(cfg.LumenVecURL, "http://localhost:19291"), "/"),
			profile: "ann-balanced",
		}, nil
	case "lumenvec-http-ann-fast":
		return &LumenVecHTTP{
			baseURL: strings.TrimRight(firstNonEmpty(cfg.LumenVecURL, "http://localhost:19292"), "/"),
			profile: "ann-fast",
		}, nil
	case "lumenvec-http-ann-quality":
		return &LumenVecHTTP{
			baseURL: strings.TrimRight(firstNonEmpty(cfg.LumenVecURL, "http://localhost:19293"), "/"),
			profile: "ann-quality",
		}, nil
	case "lumenvec-grpc-exact":
		return &LumenVecGRPC{
			address: firstNonEmpty(cfg.LumenVecGRPC, "localhost:19390"),
			profile: "exact",
		}, nil
	case "lumenvec-grpc-ann":
		return &LumenVecGRPC{
			address: firstNonEmpty(cfg.LumenVecGRPC, "localhost:19391"),
			profile: "ann-balanced",
		}, nil
	case "lumenvec-grpc-ann-fast":
		return &LumenVecGRPC{
			address: firstNonEmpty(cfg.LumenVecGRPC, "localhost:19392"),
			profile: "ann-fast",
		}, nil
	case "lumenvec-grpc-ann-quality":
		return &LumenVecGRPC{
			address: firstNonEmpty(cfg.LumenVecGRPC, "localhost:19393"),
			profile: "ann-quality",
		}, nil
	case "qdrant":
		if cfg.Dimension <= 0 {
			return nil, fmt.Errorf("qdrant dimension must be positive")
		}
		collection := strings.TrimSpace(cfg.Collection)
		if collection == "" {
			collection = "lumenvec-benchmark"
		}
		return &Qdrant{
			baseURL:    strings.TrimRight(firstNonEmpty(cfg.QdrantURL, "http://localhost:6333"), "/"),
			collection: collection,
			dimension:  cfg.Dimension,
			client:     &http.Client{Timeout: 30 * time.Second},
		}, nil
	case "chroma":
		collection := strings.TrimSpace(cfg.Collection)
		if collection == "" {
			collection = "lumenvec-benchmark"
		}
		return &Chroma{
			baseURL:    strings.TrimRight(firstNonEmpty(cfg.ChromaURL, "http://localhost:18000"), "/"),
			tenant:     "default_tenant",
			database:   "default_database",
			collection: collection,
			client:     &http.Client{Timeout: 60 * time.Second},
		}, nil
	case "weaviate":
		if cfg.Dimension <= 0 {
			return nil, fmt.Errorf("weaviate dimension must be positive")
		}
		collection := strings.TrimSpace(cfg.Collection)
		if collection == "" {
			collection = "lumenvec-benchmark"
		}
		return &Weaviate{
			baseURL:   strings.TrimRight(firstNonEmpty(cfg.WeaviateURL, "http://localhost:18080"), "/"),
			className: weaviateClassName(collection),
			dimension: cfg.Dimension,
			client:    &http.Client{Timeout: 60 * time.Second},
		}, nil
	case "pgvector":
		return newPGVector(cfg, "exact")
	case "pgvector-hnsw":
		return newPGVector(cfg, "hnsw-m16-ef64")
	case "pgvector-ivfflat":
		return newPGVector(cfg, "ivfflat-l100-p10")
	default:
		return nil, fmt.Errorf("unsupported engine %q", cfg.Name)
	}
}

func newPGVector(cfg Config, profile string) (Engine, error) {
	if cfg.Dimension <= 0 {
		return nil, fmt.Errorf("pgvector dimension must be positive")
	}
	collection := strings.TrimSpace(cfg.Collection)
	if collection == "" {
		collection = "lumenvec_benchmark"
	}
	return &PGVector{
		dsn:       firstNonEmpty(cfg.PGVectorDSN, "postgres://postgres:postgres@localhost:15432/postgres?sslmode=disable"),
		table:     safeSQLIdentifier(collection),
		dimension: cfg.Dimension,
		profile:   profile,
	}, nil
}

func (e *LumenVec) Name() string { return "lumenvec" }

func (e *LumenVec) Transport() string { return "in-process" }

func (e *LumenVec) Profile() string {
	if e.mode == "ann" {
		return "ann-balanced"
	}
	return "exact"
}

func (e *LumenVec) Setup(context.Context) error {
	tempDir, err := os.MkdirTemp("", "lumenvec-benchmark-*")
	if err != nil {
		return err
	}
	e.tempDir = tempDir
	e.service = core.NewService(core.ServiceOptions{
		MaxVectorDim:  1 << 20,
		MaxK:          1 << 20,
		SnapshotPath:  filepath.Join(tempDir, "snapshot.json"),
		WALPath:       filepath.Join(tempDir, "wal.log"),
		SnapshotEvery: 1 << 20,
		SearchMode:    e.mode,
		ANNProfile:    "balanced",
		ANNOptions: ann.Options{
			M:              16,
			EfConstruction: 64,
			EfSearch:       64,
		},
	})
	return nil
}

func (e *LumenVec) Insert(_ context.Context, vectors []dataset.Vector) error {
	items := make([]index.Vector, 0, len(vectors))
	for _, vector := range vectors {
		items = append(items, index.Vector{ID: vector.ID, Values: vector.Values})
	}
	return e.service.AddVectors(items)
}

func (e *LumenVec) BuildIndex(context.Context) (bool, error) { return false, nil }

func (e *LumenVec) Search(_ context.Context, query []float64, k int) ([]core.SearchResult, error) {
	return e.service.Search(query, k)
}

func (e *LumenVec) SearchBatch(_ context.Context, queries []dataset.Vector, k int) ([][]core.SearchResult, bool, error) {
	items := make([]core.BatchSearchQuery, 0, len(queries))
	for _, query := range queries {
		items = append(items, core.BatchSearchQuery{ID: query.ID, Values: query.Values, K: k})
	}
	results, err := e.service.SearchBatch(items)
	if err != nil {
		return nil, true, err
	}
	out := make([][]core.SearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, result.Results)
	}
	return out, true, nil
}

func (e *LumenVec) Close() error {
	if e.service == nil {
		return nil
	}
	err := e.service.Close()
	if e.tempDir != "" {
		if removeErr := os.RemoveAll(e.tempDir); err == nil {
			err = removeErr
		}
	}
	return err
}

type LumenVecHTTP struct {
	baseURL string
	profile string
	client  *lumenclient.VectorClient
}

func (e *LumenVecHTTP) Name() string { return "lumenvec" }

func (e *LumenVecHTTP) Profile() string { return e.profile }

func (e *LumenVecHTTP) Transport() string { return "http" }

func (e *LumenVecHTTP) Setup(ctx context.Context) error {
	e.client = lumenclient.NewVectorClient(e.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("lumenvec health failed: %s", resp.Status)
	}
	return nil
}

func (e *LumenVecHTTP) Insert(_ context.Context, vectors []dataset.Vector) error {
	items := make([]lumenclient.VectorPayload, 0, len(vectors))
	for _, vector := range vectors {
		items = append(items, lumenclient.VectorPayload{ID: vector.ID, Values: vector.Values})
	}
	return e.client.AddVectors(items)
}

func (e *LumenVecHTTP) BuildIndex(context.Context) (bool, error) { return false, nil }

func (e *LumenVecHTTP) Search(_ context.Context, query []float64, k int) ([]core.SearchResult, error) {
	results, err := e.client.SearchVector(query, k)
	if err != nil {
		return nil, err
	}
	out := make([]core.SearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, core.SearchResult{ID: result.ID, Distance: result.Distance})
	}
	return out, nil
}

func (e *LumenVecHTTP) SearchBatch(_ context.Context, queries []dataset.Vector, k int) ([][]core.SearchResult, bool, error) {
	items := make([]lumenclient.BatchSearchQuery, 0, len(queries))
	for _, query := range queries {
		items = append(items, lumenclient.BatchSearchQuery{ID: query.ID, Values: query.Values, K: k})
	}
	results, err := e.client.SearchVectors(items)
	if err != nil {
		return nil, true, err
	}
	out := make([][]core.SearchResult, 0, len(results))
	for _, result := range results {
		hits := make([]core.SearchResult, 0, len(result.Results))
		for _, hit := range result.Results {
			hits = append(hits, core.SearchResult{ID: hit.ID, Distance: hit.Distance})
		}
		out = append(out, hits)
	}
	return out, true, nil
}

func (e *LumenVecHTTP) Close() error { return nil }

type LumenVecGRPC struct {
	address string
	profile string
	client  *lumenclient.GRPCVectorClient
}

func (e *LumenVecGRPC) Name() string { return "lumenvec" }

func (e *LumenVecGRPC) Profile() string { return e.profile }

func (e *LumenVecGRPC) Transport() string { return "grpc" }

func (e *LumenVecGRPC) Setup(context.Context) error {
	client, err := lumenclient.NewGRPCVectorClient(e.address)
	if err != nil {
		return err
	}
	e.client = client
	status, err := e.client.Health()
	if err != nil {
		_ = e.client.Close()
		e.client = nil
		return err
	}
	if strings.ToLower(strings.TrimSpace(status)) != "ok" {
		_ = e.client.Close()
		e.client = nil
		return fmt.Errorf("lumenvec grpc health failed: %s", status)
	}
	return nil
}

func (e *LumenVecGRPC) Insert(_ context.Context, vectors []dataset.Vector) error {
	items := make([]lumenclient.VectorPayload, 0, len(vectors))
	for _, vector := range vectors {
		items = append(items, lumenclient.VectorPayload{ID: vector.ID, Values: vector.Values})
	}
	return e.client.AddVectors(items)
}

func (e *LumenVecGRPC) BuildIndex(context.Context) (bool, error) { return false, nil }

func (e *LumenVecGRPC) Search(_ context.Context, query []float64, k int) ([]core.SearchResult, error) {
	results, err := e.client.SearchVector(query, k)
	if err != nil {
		return nil, err
	}
	out := make([]core.SearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, core.SearchResult{ID: result.ID, Distance: result.Distance})
	}
	return out, nil
}

func (e *LumenVecGRPC) SearchBatch(_ context.Context, queries []dataset.Vector, k int) ([][]core.SearchResult, bool, error) {
	items := make([]lumenclient.BatchSearchQuery, 0, len(queries))
	for _, query := range queries {
		items = append(items, lumenclient.BatchSearchQuery{ID: query.ID, Values: query.Values, K: k})
	}
	results, err := e.client.SearchVectors(items)
	if err != nil {
		return nil, true, err
	}
	out := make([][]core.SearchResult, 0, len(results))
	for _, result := range results {
		hits := make([]core.SearchResult, 0, len(result.Results))
		for _, hit := range result.Results {
			hits = append(hits, core.SearchResult{ID: hit.ID, Distance: hit.Distance})
		}
		out = append(out, hits)
	}
	return out, true, nil
}

func (e *LumenVecGRPC) Close() error {
	if e.client == nil {
		return nil
	}
	return e.client.Close()
}

type Qdrant struct {
	baseURL    string
	collection string
	dimension  int
	client     *http.Client
}

func (q *Qdrant) Name() string { return "qdrant" }

func (q *Qdrant) Transport() string { return "rest" }

func (q *Qdrant) Profile() string { return "default" }

func (q *Qdrant) Setup(ctx context.Context) error {
	body := map[string]any{
		"vectors": map[string]any{
			"size":     q.dimension,
			"distance": "Euclid",
		},
	}
	return q.do(ctx, http.MethodPut, "/collections/"+q.collection, body, nil)
}

func (q *Qdrant) Insert(ctx context.Context, vectors []dataset.Vector) error {
	points := make([]qdrantPoint, 0, len(vectors))
	for _, vector := range vectors {
		points = append(points, qdrantPoint{
			ID:      qdrantNumericID(vector.ID),
			Vector:  vector.Values,
			Payload: map[string]string{"external_id": vector.ID},
		})
	}
	body := map[string]any{"points": points}
	return q.do(ctx, http.MethodPut, "/collections/"+q.collection+"/points?wait=true", body, nil)
}

func (q *Qdrant) BuildIndex(context.Context) (bool, error) { return false, nil }

func (q *Qdrant) Search(ctx context.Context, query []float64, k int) ([]core.SearchResult, error) {
	body := map[string]any{
		"query":        query,
		"limit":        k,
		"with_payload": true,
	}
	var response qdrantQueryResponse
	if err := q.do(ctx, http.MethodPost, "/collections/"+q.collection+"/points/query", body, &response); err != nil {
		return nil, err
	}
	out := make([]core.SearchResult, 0, len(response.Result.Points))
	for _, point := range response.Result.Points {
		id := strings.Trim(string(point.ID), `"`)
		if point.Payload.ExternalID != "" {
			id = point.Payload.ExternalID
		}
		out = append(out, core.SearchResult{
			ID:       id,
			Distance: point.Score,
		})
	}
	return out, nil
}

func (q *Qdrant) SearchBatch(context.Context, []dataset.Vector, int) ([][]core.SearchResult, bool, error) {
	return nil, false, nil
}

func (q *Qdrant) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return q.do(ctx, http.MethodDelete, "/collections/"+q.collection, nil, nil)
}

type Chroma struct {
	baseURL      string
	tenant       string
	database     string
	collection   string
	collectionID string
	client       *http.Client
}

func (c *Chroma) Name() string { return "chroma" }

func (c *Chroma) Transport() string { return "rest" }

func (c *Chroma) Profile() string { return "default" }

func (c *Chroma) Setup(ctx context.Context) error {
	if err := c.wait(ctx); err != nil {
		return err
	}
	body := map[string]any{
		"name":          c.collection,
		"get_or_create": true,
		"metadata": map[string]any{
			"hnsw:space": "l2",
		},
	}
	var response chromaCollection
	if err := c.do(ctx, http.MethodPost, c.databasePath("/collections"), body, &response); err != nil {
		return err
	}
	if strings.TrimSpace(response.ID) == "" {
		return fmt.Errorf("chroma create collection returned empty id")
	}
	c.collectionID = response.ID
	return nil
}

func (c *Chroma) wait(ctx context.Context) error {
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v2/heartbeat", nil)
		if err != nil {
			return err
		}
		resp, err := c.client.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = resp.Body.Close()
			return nil
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("chroma heartbeat failed: %s", resp.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("chroma heartbeat timed out: %w", lastErr)
}

func (c *Chroma) Insert(ctx context.Context, vectors []dataset.Vector) error {
	if len(vectors) == 0 {
		return nil
	}
	ids := make([]string, 0, len(vectors))
	embeddings := make([][]float64, 0, len(vectors))
	for _, vector := range vectors {
		ids = append(ids, vector.ID)
		embeddings = append(embeddings, vector.Values)
	}
	body := map[string]any{
		"ids":        ids,
		"embeddings": embeddings,
	}
	return c.do(ctx, http.MethodPost, c.collectionPath("/add"), body, nil)
}

func (c *Chroma) BuildIndex(context.Context) (bool, error) { return false, nil }

func (c *Chroma) Search(ctx context.Context, query []float64, k int) ([]core.SearchResult, error) {
	body := map[string]any{
		"query_embeddings": [][]float64{query},
		"n_results":        k,
		"include":          []string{"distances"},
	}
	var response chromaQueryResponse
	if err := c.do(ctx, http.MethodPost, c.collectionPath("/query"), body, &response); err != nil {
		return nil, err
	}
	results := chromaResults(response)
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

func (c *Chroma) SearchBatch(ctx context.Context, queries []dataset.Vector, k int) ([][]core.SearchResult, bool, error) {
	if len(queries) == 0 {
		return nil, true, nil
	}
	embeddings := make([][]float64, 0, len(queries))
	for _, query := range queries {
		embeddings = append(embeddings, query.Values)
	}
	body := map[string]any{
		"query_embeddings": embeddings,
		"n_results":        k,
		"include":          []string{"distances"},
	}
	var response chromaQueryResponse
	if err := c.do(ctx, http.MethodPost, c.collectionPath("/query"), body, &response); err != nil {
		return nil, true, err
	}
	return chromaResults(response), true, nil
}

func (c *Chroma) Close() error {
	if strings.TrimSpace(c.collectionID) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.do(ctx, http.MethodDelete, c.collectionPath(""), nil, nil)
}

func (c *Chroma) databasePath(path string) string {
	return "/api/v2/tenants/" + c.tenant + "/databases/" + c.database + path
}

func (c *Chroma) collectionPath(path string) string {
	return c.databasePath("/collections/" + c.collectionID + path)
}

type Weaviate struct {
	baseURL   string
	className string
	dimension int
	client    *http.Client
}

func (w *Weaviate) Name() string { return "weaviate" }

func (w *Weaviate) Transport() string { return "graphql" }

func (w *Weaviate) Profile() string { return "hnsw-default" }

func (w *Weaviate) Setup(ctx context.Context) error {
	if err := w.wait(ctx); err != nil {
		return err
	}
	body := map[string]any{
		"class":           w.className,
		"vectorizer":      "none",
		"vectorIndexType": "hnsw",
		"vectorIndexConfig": map[string]any{
			"distance": "l2-squared",
		},
		"properties": []map[string]any{
			{
				"name":     "external_id",
				"dataType": []string{"text"},
			},
		},
	}
	return w.do(ctx, http.MethodPost, "/v1/schema", body, nil)
}

func (w *Weaviate) wait(ctx context.Context) error {
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.baseURL+"/v1/.well-known/ready", nil)
		if err != nil {
			return err
		}
		resp, err := w.client.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = resp.Body.Close()
			return nil
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("weaviate readiness failed: %s", resp.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("weaviate readiness timed out: %w", lastErr)
}

func (w *Weaviate) Insert(ctx context.Context, vectors []dataset.Vector) error {
	if len(vectors) == 0 {
		return nil
	}
	objects := make([]weaviateObject, 0, len(vectors))
	for _, vector := range vectors {
		objects = append(objects, weaviateObject{
			Class:      w.className,
			ID:         weaviateUUID(vector.ID),
			Properties: map[string]string{"external_id": vector.ID},
			Vector:     vector.Values,
		})
	}
	body := map[string]any{"objects": objects}
	return w.do(ctx, http.MethodPost, "/v1/batch/objects", body, nil)
}

func (w *Weaviate) BuildIndex(context.Context) (bool, error) { return false, nil }

func (w *Weaviate) Search(ctx context.Context, query []float64, k int) ([]core.SearchResult, error) {
	body := map[string]string{
		"query": fmt.Sprintf(`{ Get { %s(nearVector: {vector: %s} limit: %d) { external_id _additional { distance } } } }`, w.className, vectorLiteral(query), k),
	}
	var response weaviateGraphQLResponse
	if err := w.do(ctx, http.MethodPost, "/v1/graphql", body, &response); err != nil {
		return nil, err
	}
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("weaviate graphql error: %s", response.Errors[0].Message)
	}
	items := response.Data.Get[w.className]
	out := make([]core.SearchResult, 0, len(items))
	for _, item := range items {
		id := item.ExternalID
		if id == "" {
			id = item.Additional.ID
		}
		out = append(out, core.SearchResult{ID: id, Distance: item.Additional.Distance})
	}
	return out, nil
}

func (w *Weaviate) SearchBatch(context.Context, []dataset.Vector, int) ([][]core.SearchResult, bool, error) {
	return nil, false, nil
}

func (w *Weaviate) Close() error {
	if strings.TrimSpace(w.className) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return w.do(ctx, http.MethodDelete, "/v1/schema/"+w.className, nil, nil)
}

func (w *Weaviate) do(ctx context.Context, method, path string, body any, dst any) error {
	return doJSON(ctx, w.client, w.baseURL, "weaviate", method, path, body, dst)
}

type PGVector struct {
	dsn        string
	table      string
	dimension  int
	profile    string
	db         *sql.DB
	indexMu    sync.Mutex
	indexBuilt bool
}

func (p *PGVector) Name() string { return "pgvector" }

func (p *PGVector) Transport() string { return "postgres" }

func (p *PGVector) Profile() string { return p.profile }

func (p *PGVector) Setup(ctx context.Context) error {
	db, err := sql.Open("postgres", p.dsn)
	if err != nil {
		return err
	}
	p.db = db
	if err := p.wait(ctx); err != nil {
		return err
	}
	statements := []string{
		"CREATE EXTENSION IF NOT EXISTS vector",
		fmt.Sprintf("DROP TABLE IF EXISTS %s", quoteIdentifier(p.table)),
		fmt.Sprintf("CREATE TABLE %s (id text PRIMARY KEY, embedding vector(%d) NOT NULL)", quoteIdentifier(p.table), p.dimension),
	}
	for _, statement := range statements {
		if _, err := p.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (p *PGVector) wait(ctx context.Context) error {
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := p.db.PingContext(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("pgvector ping timed out: %w", lastErr)
}

func (p *PGVector) Insert(ctx context.Context, vectors []dataset.Vector) error {
	if len(vectors) == 0 {
		return nil
	}
	var b strings.Builder
	args := make([]any, 0, len(vectors)*2)
	b.WriteString("INSERT INTO ")
	b.WriteString(quoteIdentifier(p.table))
	b.WriteString(" (id, embedding) VALUES ")
	for i, vector := range vectors {
		if i > 0 {
			b.WriteString(",")
		}
		idParam := i*2 + 1
		vectorParam := i*2 + 2
		fmt.Fprintf(&b, "($%d, $%d::vector)", idParam, vectorParam)
		args = append(args, vector.ID, vectorLiteral(vector.Values))
	}
	_, err := p.db.ExecContext(ctx, b.String(), args...)
	return err
}

func (p *PGVector) BuildIndex(ctx context.Context) (bool, error) {
	if p.profile == "exact" {
		return false, nil
	}
	return true, p.ensureIndex(ctx)
}

func (p *PGVector) Search(ctx context.Context, query []float64, k int) ([]core.SearchResult, error) {
	if err := p.ensureIndex(ctx); err != nil {
		return nil, err
	}
	if p.profile == "exact" {
		return p.searchWith(ctx, p.db, query, k)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, p.searchParameterSQL()); err != nil {
		return nil, err
	}
	out, err := p.searchWith(ctx, tx, query, k)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *PGVector) SearchBatch(context.Context, []dataset.Vector, int) ([][]core.SearchResult, bool, error) {
	return nil, false, nil
}

type queryContext interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (p *PGVector) searchWith(ctx context.Context, db queryContext, query []float64, k int) ([]core.SearchResult, error) {
	rows, err := db.QueryContext(
		ctx,
		fmt.Sprintf("SELECT id, embedding <-> $1::vector AS distance FROM %s ORDER BY embedding <-> $1::vector LIMIT $2", quoteIdentifier(p.table)),
		vectorLiteral(query),
		k,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []core.SearchResult
	for rows.Next() {
		var result core.SearchResult
		if err := rows.Scan(&result.ID, &result.Distance); err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, rows.Err()
}

func (p *PGVector) ensureIndex(ctx context.Context) error {
	if p.profile == "exact" {
		return nil
	}
	p.indexMu.Lock()
	defer p.indexMu.Unlock()
	if p.indexBuilt {
		return nil
	}
	statements := []string{}
	indexName := quoteIdentifier(shortIdentifierWithHash(p.table + "_" + strings.ReplaceAll(p.profile, "-", "_") + "_idx"))
	switch p.profile {
	case "hnsw-m16-ef64":
		statements = append(statements, fmt.Sprintf(
			"CREATE INDEX %s ON %s USING hnsw (embedding vector_l2_ops) WITH (m = 16, ef_construction = 64)",
			indexName,
			quoteIdentifier(p.table),
		))
	case "ivfflat-l100-p10":
		statements = append(statements, fmt.Sprintf(
			"CREATE INDEX %s ON %s USING ivfflat (embedding vector_l2_ops) WITH (lists = 100)",
			indexName,
			quoteIdentifier(p.table),
		))
	default:
		return fmt.Errorf("unsupported pgvector profile %q", p.profile)
	}
	statements = append(statements, fmt.Sprintf("ANALYZE %s", quoteIdentifier(p.table)))
	for _, statement := range statements {
		if _, err := p.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	p.indexBuilt = true
	return nil
}

func (p *PGVector) searchParameterSQL() string {
	switch p.profile {
	case "hnsw-m16-ef64":
		return "SET LOCAL hnsw.ef_search = 64"
	case "ivfflat-l100-p10":
		return "SET LOCAL ivfflat.probes = 10"
	default:
		return "SELECT 1"
	}
}

func (p *PGVector) Close() error {
	if p.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := p.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", quoteIdentifier(p.table)))
	closeErr := p.db.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (q *Qdrant) do(ctx context.Context, method, path string, body any, dst any) error {
	return doJSON(ctx, q.client, q.baseURL, "qdrant", method, path, body, dst)
}

func (c *Chroma) do(ctx context.Context, method, path string, body any, dst any) error {
	return doJSON(ctx, c.client, c.baseURL, "chroma", method, path, body, dst)
}

func doJSON(ctx context.Context, client *http.Client, baseURL, label, method, path string, body any, dst any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s %s failed: %s: %s", label, method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if dst == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

type qdrantPoint struct {
	ID      uint64            `json:"id"`
	Vector  []float64         `json:"vector"`
	Payload map[string]string `json:"payload"`
}

type qdrantQueryResponse struct {
	Result struct {
		Points []struct {
			ID      json.RawMessage `json:"id"`
			Score   float64         `json:"score"`
			Payload struct {
				ExternalID string `json:"external_id"`
			} `json:"payload"`
		} `json:"points"`
	} `json:"result"`
}

type chromaCollection struct {
	ID string `json:"id"`
}

type chromaQueryResponse struct {
	IDs       [][]string  `json:"ids"`
	Distances [][]float64 `json:"distances"`
}

type weaviateObject struct {
	Class      string            `json:"class"`
	ID         string            `json:"id"`
	Properties map[string]string `json:"properties"`
	Vector     []float64         `json:"vector"`
}

type weaviateGraphQLResponse struct {
	Data struct {
		Get map[string][]weaviateGraphQLItem `json:"Get"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type weaviateGraphQLItem struct {
	ExternalID string `json:"external_id"`
	Additional struct {
		ID       string  `json:"id"`
		Distance float64 `json:"distance"`
	} `json:"_additional"`
}

func chromaResults(response chromaQueryResponse) [][]core.SearchResult {
	out := make([][]core.SearchResult, 0, len(response.IDs))
	for queryIndex, ids := range response.IDs {
		hits := make([]core.SearchResult, 0, len(ids))
		for hitIndex, id := range ids {
			distance := 0.0
			if queryIndex < len(response.Distances) && hitIndex < len(response.Distances[queryIndex]) {
				distance = response.Distances[queryIndex][hitIndex]
			}
			hits = append(hits, core.SearchResult{ID: id, Distance: distance})
		}
		out = append(out, hits)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func qdrantNumericID(id string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(id))
	return hash.Sum64()
}

func weaviateUUID(id string) string {
	sum := md5.Sum([]byte(id))
	sum[6] = (sum[6] & 0x0f) | 0x30
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func weaviateClassName(value string) string {
	base := safeSQLIdentifier(value)
	var b strings.Builder
	upperNext := true
	for _, r := range base {
		if r == '_' {
			upperNext = true
			continue
		}
		if upperNext {
			b.WriteString(strings.ToUpper(string(r)))
			upperNext = false
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if out == "" {
		return "LumenvecBenchmark"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "Bench" + out
	}
	if len(out) > 55 {
		out = out[:55]
	}
	return out
}

func vectorLiteral(values []float64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconvFormatFloat(value))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func strconvFormatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func safeSQLIdentifier(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "lumenvec_benchmark"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "bench_" + out
	}
	return out
}

func shortIdentifier(value string) string {
	value = safeSQLIdentifier(value)
	if len(value) <= 55 {
		return value
	}
	return value[:55]
}

func shortIdentifierWithHash(value string) string {
	value = safeSQLIdentifier(value)
	if len(value) <= 55 {
		return value
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(value))
	suffix := "_" + strconv.FormatUint(uint64(hash.Sum32()), 36)
	return value[:55-len(suffix)] + suffix
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
