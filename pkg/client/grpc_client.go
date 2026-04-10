package client

import (
	"context"
	"fmt"
	"time"

	lumenvecpb "lumenvec/api/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type GRPCVectorClient struct {
	conn    *grpc.ClientConn
	client  lumenvecpb.VectorServiceClient
	timeout time.Duration
}

func NewGRPCVectorClient(address string) (*GRPCVectorClient, error) {
	return NewGRPCVectorClientWithDialer(address, nil)
}

func NewGRPCVectorClientWithDialer(address string, dialOptions []grpc.DialOption) (*GRPCVectorClient, error) {
	options := append([]grpc.DialOption{}, dialOptions...)
	if len(options) == 0 {
		options = append(options, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	conn, err := grpc.NewClient(address, options...)
	if err != nil {
		return nil, err
	}
	return &GRPCVectorClient{
		conn:    conn,
		client:  lumenvecpb.NewVectorServiceClient(conn),
		timeout: 10 * time.Second,
	}, nil
}

func (c *GRPCVectorClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *GRPCVectorClient) AddVector(vector []float64) error {
	return c.AddVectorWithID(fmt.Sprintf("vec-%d", time.Now().UnixNano()), vector)
}

func (c *GRPCVectorClient) AddVectorWithID(id string, vector []float64) error {
	ctx, cancel := c.context()
	defer cancel()
	_, err := c.client.AddVector(ctx, &lumenvecpb.AddVectorRequest{
		Id:     id,
		Values: vector,
	})
	return err
}

func (c *GRPCVectorClient) AddVectors(vectors []VectorPayload) error {
	items := make([]*lumenvecpb.Vector, 0, len(vectors))
	for _, vec := range vectors {
		items = append(items, &lumenvecpb.Vector{
			Id:     vec.ID,
			Values: vec.Values,
		})
	}
	ctx, cancel := c.context()
	defer cancel()
	_, err := c.client.AddVectorsBatch(ctx, &lumenvecpb.AddVectorsBatchRequest{Vectors: items})
	return err
}

func (c *GRPCVectorClient) GetVector(id string) (*VectorPayload, error) {
	ctx, cancel := c.context()
	defer cancel()
	resp, err := c.client.GetVector(ctx, &lumenvecpb.GetVectorRequest{Id: id})
	if err != nil {
		return nil, err
	}
	vec := resp.GetVector()
	if vec == nil {
		return nil, nil
	}
	return &VectorPayload{
		ID:     vec.GetId(),
		Values: vec.GetValues(),
	}, nil
}

func (c *GRPCVectorClient) SearchVector(vector []float64, k int) ([]SearchResult, error) {
	ctx, cancel := c.context()
	defer cancel()
	resp, err := c.client.Search(ctx, &lumenvecpb.SearchRequest{
		Values: vector,
		TopK:   int32(k),
	})
	if err != nil {
		return nil, err
	}
	return fromProtoSearchResults(resp.GetResults()), nil
}

func (c *GRPCVectorClient) SearchVectors(queries []BatchSearchQuery) ([]BatchSearchResult, error) {
	items := make([]*lumenvecpb.SearchBatchQuery, 0, len(queries))
	for _, query := range queries {
		items = append(items, &lumenvecpb.SearchBatchQuery{
			Id:     query.ID,
			Values: query.Values,
			TopK:   int32(query.K),
		})
	}
	ctx, cancel := c.context()
	defer cancel()
	resp, err := c.client.SearchBatch(ctx, &lumenvecpb.SearchBatchRequest{Queries: items})
	if err != nil {
		return nil, err
	}
	results := make([]BatchSearchResult, 0, len(resp.GetResults()))
	for _, item := range resp.GetResults() {
		results = append(results, BatchSearchResult{
			ID:      item.GetId(),
			Results: fromProtoSearchResults(item.GetResults()),
		})
	}
	return results, nil
}

func (c *GRPCVectorClient) DeleteVector(id string) error {
	ctx, cancel := c.context()
	defer cancel()
	_, err := c.client.DeleteVector(ctx, &lumenvecpb.DeleteVectorRequest{Id: id})
	return err
}

func (c *GRPCVectorClient) Health() (string, error) {
	ctx, cancel := c.context()
	defer cancel()
	resp, err := c.client.Health(ctx, &lumenvecpb.HealthRequest{})
	if err != nil {
		return "", err
	}
	return resp.GetStatus(), nil
}

func (c *GRPCVectorClient) context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), c.timeout)
}

func fromProtoSearchResults(results []*lumenvecpb.SearchResult) []SearchResult {
	out := make([]SearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, SearchResult{
			ID:       result.GetId(),
			Distance: result.GetDistance(),
		})
	}
	return out
}
