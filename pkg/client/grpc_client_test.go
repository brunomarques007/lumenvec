package client

import (
	"context"
	"errors"
	"testing"
	"time"

	lumenvecpb "lumenvec/api/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fakeVecService struct{}

func (f *fakeVecService) Health(ctx context.Context, in *lumenvecpb.HealthRequest, opts ...grpc.CallOption) (*lumenvecpb.HealthResponse, error) {
	return &lumenvecpb.HealthResponse{Status: "ok"}, nil
}
func (f *fakeVecService) ListVectors(ctx context.Context, in *lumenvecpb.ListVectorsRequest, opts ...grpc.CallOption) (*lumenvecpb.ListVectorsResponse, error) {
	return &lumenvecpb.ListVectorsResponse{Vectors: []*lumenvecpb.Vector{{Id: "x", Values: []float64{1}}}}, nil
}
func (f *fakeVecService) AddVector(ctx context.Context, in *lumenvecpb.AddVectorRequest, opts ...grpc.CallOption) (*lumenvecpb.AddVectorResponse, error) {
	return &lumenvecpb.AddVectorResponse{Success: true}, nil
}
func (f *fakeVecService) AddVectorsBatch(ctx context.Context, in *lumenvecpb.AddVectorsBatchRequest, opts ...grpc.CallOption) (*lumenvecpb.AddVectorsBatchResponse, error) {
	return &lumenvecpb.AddVectorsBatchResponse{Success: true}, nil
}
func (f *fakeVecService) GetVector(ctx context.Context, in *lumenvecpb.GetVectorRequest, opts ...grpc.CallOption) (*lumenvecpb.GetVectorResponse, error) {
	return &lumenvecpb.GetVectorResponse{Vector: &lumenvecpb.Vector{Id: in.GetId(), Values: []float64{2}}}, nil
}
func (f *fakeVecService) Search(ctx context.Context, in *lumenvecpb.SearchRequest, opts ...grpc.CallOption) (*lumenvecpb.SearchResponse, error) {
	return &lumenvecpb.SearchResponse{Results: []*lumenvecpb.SearchResult{{Id: "x", Distance: 0.5}}}, nil
}
func (f *fakeVecService) SearchBatch(ctx context.Context, in *lumenvecpb.SearchBatchRequest, opts ...grpc.CallOption) (*lumenvecpb.SearchBatchResponse, error) {
	return &lumenvecpb.SearchBatchResponse{Results: []*lumenvecpb.SearchBatchResult{{Id: "q1", Results: []*lumenvecpb.SearchResult{{Id: "x", Distance: 0.5}}}}}, nil
}
func (f *fakeVecService) DeleteVector(ctx context.Context, in *lumenvecpb.DeleteVectorRequest, opts ...grpc.CallOption) (*lumenvecpb.DeleteVectorResponse, error) {
	return &lumenvecpb.DeleteVectorResponse{Success: true}, nil
}

func TestGRPCClientMappings(t *testing.T) {
	c := &GRPCVectorClient{client: &fakeVecService{}, timeout: time.Second}

	// Health
	h, err := c.Health()
	if err != nil || h != "ok" {
		t.Fatalf("unexpected health: %v %v", h, err)
	}

	// ListVectors
	vecs, err := c.ListVectors()
	if err != nil || len(vecs) != 1 || vecs[0].ID != "x" {
		t.Fatalf("unexpected list vectors: %v %v", vecs, err)
	}

	// GetVector
	got, err := c.GetVector("y")
	if err != nil || got == nil || got.ID != "y" {
		t.Fatalf("unexpected get vector: %v %v", got, err)
	}

	// SearchVector
	sr, err := c.SearchVector([]float64{1}, 1)
	if err != nil || len(sr) != 1 || sr[0].ID != "x" {
		t.Fatalf("unexpected search vector: %v %v", sr, err)
	}

	// SearchVectors (batch)
	_, err = c.SearchVectors([]BatchSearchQuery{{ID: "q1", Values: []float64{1}, K: 1}})
	if err != nil {
		t.Fatalf("unexpected error from SearchVectors: %v", err)
	}

	// AddVectors
	if err := c.AddVectors([]VectorPayload{{ID: "a", Values: []float64{1}}}); err != nil {
		t.Fatalf("unexpected add vectors error: %v", err)
	}
	// DeleteVector
	if err := c.DeleteVector("x"); err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	// Close when conn nil should not error
	if err := c.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

}

func TestGRPCClientConstructorsAndAddVector(t *testing.T) {
	client, err := NewGRPCVectorClient("dns:///localhost:19191")
	if err != nil {
		t.Fatalf("NewGRPCVectorClient() error = %v", err)
	}
	if client.timeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", client.timeout)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	client, err = NewGRPCVectorClientWithDialer("dns:///localhost:19191", []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	})
	if err != nil {
		t.Fatalf("NewGRPCVectorClientWithDialer() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	c := &GRPCVectorClient{client: &fakeVecService{}, timeout: time.Second}
	if err := c.AddVector([]float64{1, 2, 3}); err != nil {
		t.Fatalf("AddVector() error = %v", err)
	}
	if err := c.AddVectorWithID("doc-1", []float64{1, 2, 3}); err != nil {
		t.Fatalf("AddVectorWithID() error = %v", err)
	}
}

type nilVecService struct {
	fakeVecService
}

func (f *nilVecService) ListVectors(ctx context.Context, in *lumenvecpb.ListVectorsRequest, opts ...grpc.CallOption) (*lumenvecpb.ListVectorsResponse, error) {
	return &lumenvecpb.ListVectorsResponse{Vectors: []*lumenvecpb.Vector{nil, {Id: "kept", Values: []float64{4}}}}, nil
}

func (f *nilVecService) GetVector(ctx context.Context, in *lumenvecpb.GetVectorRequest, opts ...grpc.CallOption) (*lumenvecpb.GetVectorResponse, error) {
	return &lumenvecpb.GetVectorResponse{}, nil
}

func TestGRPCClientNilProtoValues(t *testing.T) {
	c := &GRPCVectorClient{client: &nilVecService{}, timeout: time.Second}

	vecs, err := c.ListVectors()
	if err != nil {
		t.Fatalf("ListVectors() error = %v", err)
	}
	if len(vecs) != 1 || vecs[0].ID != "kept" {
		t.Fatalf("ListVectors() = %+v, want only kept vector", vecs)
	}

	vec, err := c.GetVector("missing")
	if err != nil {
		t.Fatalf("GetVector() error = %v", err)
	}
	if vec != nil {
		t.Fatalf("GetVector() = %+v, want nil", vec)
	}
}

type pagedVecService struct {
	fakeVecService
	requests []*lumenvecpb.ListVectorsRequest
}

func (f *pagedVecService) ListVectors(ctx context.Context, in *lumenvecpb.ListVectorsRequest, opts ...grpc.CallOption) (*lumenvecpb.ListVectorsResponse, error) {
	f.requests = append(f.requests, in)
	if in.GetCursor() == "" {
		return &lumenvecpb.ListVectorsResponse{
			Vectors:    []*lumenvecpb.Vector{{Id: "a", Values: []float64{1}}},
			NextCursor: "next-page",
		}, nil
	}
	return &lumenvecpb.ListVectorsResponse{
		Vectors: []*lumenvecpb.Vector{{Id: "b", Values: []float64{2}}},
	}, nil
}

func TestGRPCClientListVectorsUsesPagedRequests(t *testing.T) {
	svc := &pagedVecService{}
	c := &GRPCVectorClient{client: svc, timeout: time.Second}

	vecs, err := c.ListVectors()
	if err != nil {
		t.Fatalf("ListVectors() error = %v", err)
	}
	if len(vecs) != 2 || vecs[0].ID != "a" || vecs[1].ID != "b" {
		t.Fatalf("ListVectors() = %+v, want a,b", vecs)
	}
	if len(svc.requests) != 2 {
		t.Fatalf("ListVectors made %d requests, want 2", len(svc.requests))
	}
	if svc.requests[0].GetLimit() != defaultGRPCListVectorsLimit || svc.requests[0].GetCursor() != "" {
		t.Fatalf("first request = %+v", svc.requests[0])
	}
	if svc.requests[1].GetLimit() != defaultGRPCListVectorsLimit || svc.requests[1].GetCursor() != "next-page" {
		t.Fatalf("second request = %+v", svc.requests[1])
	}
}

func TestGRPCClientListVectorsPageOptions(t *testing.T) {
	svc := &pagedVecService{}
	c := &GRPCVectorClient{client: svc, timeout: time.Second}

	page, err := c.ListVectorsPage(ListVectorsOptions{Limit: 25, Cursor: "cursor", IDsOnly: true})
	if err != nil {
		t.Fatalf("ListVectorsPage() error = %v", err)
	}
	if len(page.Vectors) != 1 || page.Vectors[0].ID != "b" {
		t.Fatalf("ListVectorsPage() = %+v", page)
	}
	if len(svc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(svc.requests))
	}
	req := svc.requests[0]
	if req.GetLimit() != 25 || req.GetCursor() != "cursor" || !req.GetIdsOnly() {
		t.Fatalf("request = %+v, want limit/cursor/ids_only", req)
	}
}

type errorVecService struct {
	err error
}

func (f *errorVecService) Health(ctx context.Context, in *lumenvecpb.HealthRequest, opts ...grpc.CallOption) (*lumenvecpb.HealthResponse, error) {
	return nil, f.err
}
func (f *errorVecService) ListVectors(ctx context.Context, in *lumenvecpb.ListVectorsRequest, opts ...grpc.CallOption) (*lumenvecpb.ListVectorsResponse, error) {
	return nil, f.err
}
func (f *errorVecService) AddVector(ctx context.Context, in *lumenvecpb.AddVectorRequest, opts ...grpc.CallOption) (*lumenvecpb.AddVectorResponse, error) {
	return nil, f.err
}
func (f *errorVecService) AddVectorsBatch(ctx context.Context, in *lumenvecpb.AddVectorsBatchRequest, opts ...grpc.CallOption) (*lumenvecpb.AddVectorsBatchResponse, error) {
	return nil, f.err
}
func (f *errorVecService) GetVector(ctx context.Context, in *lumenvecpb.GetVectorRequest, opts ...grpc.CallOption) (*lumenvecpb.GetVectorResponse, error) {
	return nil, f.err
}
func (f *errorVecService) Search(ctx context.Context, in *lumenvecpb.SearchRequest, opts ...grpc.CallOption) (*lumenvecpb.SearchResponse, error) {
	return nil, f.err
}
func (f *errorVecService) SearchBatch(ctx context.Context, in *lumenvecpb.SearchBatchRequest, opts ...grpc.CallOption) (*lumenvecpb.SearchBatchResponse, error) {
	return nil, f.err
}
func (f *errorVecService) DeleteVector(ctx context.Context, in *lumenvecpb.DeleteVectorRequest, opts ...grpc.CallOption) (*lumenvecpb.DeleteVectorResponse, error) {
	return nil, f.err
}

func TestGRPCClientPropagatesErrors(t *testing.T) {
	wantErr := errors.New("rpc failed")
	c := &GRPCVectorClient{client: &errorVecService{err: wantErr}, timeout: time.Second}

	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, wantErr) {
			t.Fatalf("%s error = %v, want %v", name, err, wantErr)
		}
	}

	_, err := c.Health()
	check("Health", err)
	_, err = c.ListVectors()
	check("ListVectors", err)
	check("AddVectorWithID", c.AddVectorWithID("a", []float64{1}))
	check("AddVectors", c.AddVectors([]VectorPayload{{ID: "a", Values: []float64{1}}}))
	_, err = c.GetVector("a")
	check("GetVector", err)
	_, err = c.SearchVector([]float64{1}, 1)
	check("SearchVector", err)
	_, err = c.SearchVectors([]BatchSearchQuery{{ID: "q", Values: []float64{1}, K: 1}})
	check("SearchVectors", err)
	check("DeleteVector", c.DeleteVector("a"))
}
