package api

import (
	"context"
	"errors"
	"net"
	"strings"

	lumenvecpb "lumenvec/api/proto"
	"lumenvec/internal/core"
	"lumenvec/internal/index"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	grpcListenFunc = func(network, address string) (net.Listener, error) { return net.Listen(network, address) }
	grpcServeFunc  = func(server *grpc.Server, listener net.Listener) error { return server.Serve(listener) }
)

type grpcHandler struct {
	lumenvecpb.UnimplementedVectorServiceServer
	service *core.Service
}

func (h *grpcHandler) Health(context.Context, *lumenvecpb.HealthRequest) (*lumenvecpb.HealthResponse, error) {
	return &lumenvecpb.HealthResponse{Status: "ok"}, nil
}

func (h *grpcHandler) ListVectors(context.Context, *lumenvecpb.ListVectorsRequest) (*lumenvecpb.ListVectorsResponse, error) {
	vecs := h.service.ListVectors()
	out := make([]*lumenvecpb.Vector, 0, len(vecs))
	for _, vec := range vecs {
		out = append(out, &lumenvecpb.Vector{Id: vec.ID, Values: vec.Values})
	}
	return &lumenvecpb.ListVectorsResponse{Vectors: out}, nil
}

func (h *grpcHandler) AddVector(_ context.Context, req *lumenvecpb.AddVectorRequest) (*lumenvecpb.AddVectorResponse, error) {
	if err := h.service.AddVector(req.GetId(), req.GetValues()); err != nil {
		return nil, grpcStatusFromError(err)
	}
	return &lumenvecpb.AddVectorResponse{Success: true}, nil
}

func (h *grpcHandler) AddVectorsBatch(_ context.Context, req *lumenvecpb.AddVectorsBatchRequest) (*lumenvecpb.AddVectorsBatchResponse, error) {
	vectors := make([]index.Vector, 0, len(req.GetVectors()))
	for _, vec := range req.GetVectors() {
		vectors = append(vectors, index.Vector{ID: vec.GetId(), Values: vec.GetValues()})
	}
	if err := h.service.AddVectors(vectors); err != nil {
		return nil, grpcStatusFromError(err)
	}
	return &lumenvecpb.AddVectorsBatchResponse{Success: true}, nil
}

func (h *grpcHandler) GetVector(_ context.Context, req *lumenvecpb.GetVectorRequest) (*lumenvecpb.GetVectorResponse, error) {
	vec, err := h.service.GetVector(req.GetId())
	if err != nil {
		return nil, grpcStatusFromError(err)
	}
	return &lumenvecpb.GetVectorResponse{
		Vector: &lumenvecpb.Vector{Id: vec.ID, Values: vec.Values},
	}, nil
}

func (h *grpcHandler) Search(_ context.Context, req *lumenvecpb.SearchRequest) (*lumenvecpb.SearchResponse, error) {
	results, err := h.service.Search(req.GetValues(), int(req.GetTopK()))
	if err != nil {
		return nil, grpcStatusFromError(err)
	}
	return &lumenvecpb.SearchResponse{Results: toProtoSearchResults(results)}, nil
}

func (h *grpcHandler) SearchBatch(_ context.Context, req *lumenvecpb.SearchBatchRequest) (*lumenvecpb.SearchBatchResponse, error) {
	queries := make([]core.BatchSearchQuery, 0, len(req.GetQueries()))
	for _, query := range req.GetQueries() {
		queries = append(queries, core.BatchSearchQuery{
			ID:     query.GetId(),
			Values: query.GetValues(),
			K:      int(query.GetTopK()),
		})
	}
	results, err := h.service.SearchBatch(queries)
	if err != nil {
		return nil, grpcStatusFromError(err)
	}
	out := make([]*lumenvecpb.SearchBatchResult, 0, len(results))
	for _, result := range results {
		out = append(out, &lumenvecpb.SearchBatchResult{
			Id:      result.ID,
			Results: toProtoSearchResults(result.Results),
		})
	}
	return &lumenvecpb.SearchBatchResponse{Results: out}, nil
}

func (h *grpcHandler) DeleteVector(_ context.Context, req *lumenvecpb.DeleteVectorRequest) (*lumenvecpb.DeleteVectorResponse, error) {
	if err := h.service.DeleteVector(req.GetId()); err != nil {
		return nil, grpcStatusFromError(err)
	}
	return &lumenvecpb.DeleteVectorResponse{Success: true}, nil
}

func (s *Server) grpcServer() (*grpc.Server, error) {
	options := []grpc.ServerOption{grpc.UnaryInterceptor(s.grpcAuthInterceptor())}
	if s.tlsEnabled {
		creds, err := credentials.NewServerTLSFromFile(s.tlsCertFile, s.tlsKeyFile)
		if err != nil {
			return nil, err
		}
		options = append(options, grpc.Creds(creds))
	}
	server := grpc.NewServer(options...)
	lumenvecpb.RegisterVectorServiceServer(server, &grpcHandler{service: s.service})
	return server, nil
}

func (s *Server) grpcListener() (net.Listener, error) {
	return grpcListenFunc("tcp", s.grpcPort)
}

func (s *Server) serveGRPC(listener net.Listener) error {
	server, err := s.grpcServer()
	if err != nil {
		return err
	}
	return grpcServeFunc(server, listener)
}

func (s *Server) grpcAuthInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if !s.grpcAuth || !s.authEnabled || strings.TrimSpace(s.apiKey) == "" || info.FullMethod == "/lumenvec.VectorService/Health" {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing credentials")
		}
		key := grpcAuthKeyFromMetadata(md)
		if !validateAPIKey(key, s.apiKey) {
			return nil, status.Error(codes.Unauthenticated, "unauthorized")
		}
		return handler(ctx, req)
	}
}

func grpcAuthKeyFromMetadata(md metadata.MD) string {
	if values := md.Get("x-api-key"); len(values) > 0 {
		return strings.TrimSpace(values[0])
	}
	if values := md.Get("authorization"); len(values) > 0 {
		auth := strings.TrimSpace(values[0])
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			return strings.TrimSpace(auth[7:])
		}
	}
	return ""
}

func toProtoSearchResults(results []core.SearchResult) []*lumenvecpb.SearchResult {
	out := make([]*lumenvecpb.SearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, &lumenvecpb.SearchResult{
			Id:       result.ID,
			Distance: result.Distance,
		})
	}
	return out
}

func grpcStatusFromError(err error) error {
	switch {
	case err == nil:
		return nil
	case grpcCodeFromError(err) == codes.AlreadyExists:
		return status.Error(codes.AlreadyExists, err.Error())
	case grpcCodeFromError(err) == codes.NotFound:
		return status.Error(codes.NotFound, err.Error())
	case grpcCodeFromError(err) == codes.InvalidArgument:
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func grpcCodeFromError(err error) codes.Code {
	switch {
	case errors.Is(err, index.ErrVectorExists):
		return codes.AlreadyExists
	case errors.Is(err, index.ErrVectorNotFound):
		return codes.NotFound
	case errors.Is(err, core.ErrInvalidID),
		errors.Is(err, core.ErrInvalidValues),
		errors.Is(err, core.ErrInvalidK),
		errors.Is(err, core.ErrVectorDimTooHigh),
		errors.Is(err, core.ErrKTooHigh):
		return codes.InvalidArgument
	default:
		return codes.Internal
	}
}
