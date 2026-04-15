package client

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	lumenvecpb "lumenvec/api/proto"
	"lumenvec/internal/core"
	"lumenvec/internal/index"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type testVectorService struct {
	lumenvecpb.UnimplementedVectorServiceServer
	service *core.Service
}

func (s *testVectorService) Health(context.Context, *lumenvecpb.HealthRequest) (*lumenvecpb.HealthResponse, error) {
	return &lumenvecpb.HealthResponse{Status: "ok"}, nil
}

func (s *testVectorService) ListVectors(context.Context, *lumenvecpb.ListVectorsRequest) (*lumenvecpb.ListVectorsResponse, error) {
	vecs := s.service.ListVectors()
	out := make([]*lumenvecpb.Vector, 0, len(vecs))
	for _, vec := range vecs {
		out = append(out, &lumenvecpb.Vector{Id: vec.ID, Values: vec.Values})
	}
	return &lumenvecpb.ListVectorsResponse{Vectors: out}, nil
}

func (s *testVectorService) AddVector(_ context.Context, req *lumenvecpb.AddVectorRequest) (*lumenvecpb.AddVectorResponse, error) {
	if err := s.service.AddVector(req.GetId(), req.GetValues()); err != nil {
		return nil, err
	}
	return &lumenvecpb.AddVectorResponse{Success: true}, nil
}

func (s *testVectorService) AddVectorsBatch(_ context.Context, req *lumenvecpb.AddVectorsBatchRequest) (*lumenvecpb.AddVectorsBatchResponse, error) {
	vectors := make([]index.Vector, 0, len(req.GetVectors()))
	for _, vec := range req.GetVectors() {
		vectors = append(vectors, index.Vector{ID: vec.GetId(), Values: vec.GetValues()})
	}
	if err := s.service.AddVectors(vectors); err != nil {
		return nil, err
	}
	return &lumenvecpb.AddVectorsBatchResponse{Success: true}, nil
}

func (s *testVectorService) GetVector(_ context.Context, req *lumenvecpb.GetVectorRequest) (*lumenvecpb.GetVectorResponse, error) {
	vec, err := s.service.GetVector(req.GetId())
	if err != nil {
		return nil, err
	}
	return &lumenvecpb.GetVectorResponse{Vector: &lumenvecpb.Vector{Id: vec.ID, Values: vec.Values}}, nil
}

func (s *testVectorService) Search(_ context.Context, req *lumenvecpb.SearchRequest) (*lumenvecpb.SearchResponse, error) {
	results, err := s.service.Search(req.GetValues(), int(req.GetTopK()))
	if err != nil {
		return nil, err
	}
	return &lumenvecpb.SearchResponse{Results: toProtoResults(results)}, nil
}

func (s *testVectorService) SearchBatch(_ context.Context, req *lumenvecpb.SearchBatchRequest) (*lumenvecpb.SearchBatchResponse, error) {
	queries := make([]core.BatchSearchQuery, 0, len(req.GetQueries()))
	for _, query := range req.GetQueries() {
		queries = append(queries, core.BatchSearchQuery{
			ID:     query.GetId(),
			Values: query.GetValues(),
			K:      int(query.GetTopK()),
		})
	}
	results, err := s.service.SearchBatch(queries)
	if err != nil {
		return nil, err
	}
	out := make([]*lumenvecpb.SearchBatchResult, 0, len(results))
	for _, result := range results {
		out = append(out, &lumenvecpb.SearchBatchResult{Id: result.ID, Results: toProtoResults(result.Results)})
	}
	return &lumenvecpb.SearchBatchResponse{Results: out}, nil
}

func (s *testVectorService) DeleteVector(_ context.Context, req *lumenvecpb.DeleteVectorRequest) (*lumenvecpb.DeleteVectorResponse, error) {
	if err := s.service.DeleteVector(req.GetId()); err != nil {
		return nil, err
	}
	return &lumenvecpb.DeleteVectorResponse{Success: true}, nil
}

func toProtoResults(results []core.SearchResult) []*lumenvecpb.SearchResult {
	out := make([]*lumenvecpb.SearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, &lumenvecpb.SearchResult{Id: result.ID, Distance: result.Distance})
	}
	return out
}

func TestGRPCVectorClientLifecycle(t *testing.T) {
	base := t.TempDir()
	svc := core.NewService(core.ServiceOptions{
		MaxVectorDim:  16,
		MaxK:          5,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "ann",
	})

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	lumenvecpb.RegisterVectorServiceServer(server, &testVectorService{service: svc})
	defer server.Stop()
	go func() {
		_ = server.Serve(listener)
	}()

	client, err := NewGRPCVectorClientWithDialer("passthrough:///bufnet", []grpc.DialOption{
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	health, err := client.Health()
	if err != nil || health != "ok" {
		t.Fatal("expected grpc health")
	}
	if err := client.AddVectorWithID("doc-1", []float64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	vec, err := client.GetVector("doc-1")
	if err != nil || vec == nil || vec.ID != "doc-1" {
		t.Fatal("expected grpc get vector")
	}
	results, err := client.SearchVector([]float64{1, 2, 3.1}, 1)
	if err != nil || len(results) != 1 || results[0].ID != "doc-1" {
		t.Fatal("expected grpc search result")
	}
	batch, err := client.SearchVectors([]BatchSearchQuery{{ID: "q1", Values: []float64{1, 2, 3.1}, K: 1}})
	if err != nil || len(batch) != 1 || batch[0].ID != "q1" {
		t.Fatal("expected grpc batch search result")
	}
	if err := client.DeleteVector("doc-1"); err != nil {
		t.Fatal(err)
	}
}

func TestGRPCVectorClientConstructorsAndBatchPaths(t *testing.T) {
	base := t.TempDir()
	svc := core.NewService(core.ServiceOptions{
		MaxVectorDim:  16,
		MaxK:          5,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "ann",
	})

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	lumenvecpb.RegisterVectorServiceServer(server, &testVectorService{service: svc})
	defer server.Stop()
	go func() { _ = server.Serve(listener) }()

	client, err := NewGRPCVectorClient("passthrough:///bufnet")
	if err == nil {
		_ = client.Close()
	}

	client, err = NewGRPCVectorClientWithDialer("passthrough:///bufnet", []grpc.DialOption{
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	if err := client.AddVector([]float64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := client.AddVectors([]VectorPayload{{ID: "doc-2", Values: []float64{2, 3, 4}}}); err != nil {
		t.Fatal(err)
	}
	results, err := client.SearchVectors([]BatchSearchQuery{{ID: "q1", Values: []float64{2, 3, 4}, K: 1}})
	if err != nil || len(results) != 1 {
		t.Fatal("expected grpc batch search result")
	}
}

func TestGRPCVectorClientNilAndConversionHelpers(t *testing.T) {
	if err := (&GRPCVectorClient{}).Close(); err != nil {
		t.Fatal(err)
	}

	out := fromProtoSearchResults([]*lumenvecpb.SearchResult{{Id: "a", Distance: 0.1}})
	if len(out) != 1 || out[0].ID != "a" {
		t.Fatal("expected proto conversion")
	}
}

func TestGRPCVectorClientDefaultConstructor(t *testing.T) {
	client, err := NewGRPCVectorClient("dns:///localhost:19191")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}
