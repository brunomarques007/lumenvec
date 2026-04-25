package api

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	lumenvecpb "lumenvec/api/proto"
	"lumenvec/internal/core"
	"lumenvec/internal/index"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCVectorLifecycle(t *testing.T) {
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
	apiServer := NewServerWithOptions(ServerOptions{
		Port:          ":0",
		ReadTimeout:   5 * time.Second,
		WriteTimeout:  5 * time.Second,
		MaxVectorDim:  16,
		MaxK:          5,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "ann",
	})
	apiServer.service = svc
	server, err := apiServer.grpcServer()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	go func() {
		_ = server.Serve(listener)
	}()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	client := lumenvecpb.NewVectorServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	health, err := client.Health(ctx, &lumenvecpb.HealthRequest{})
	if err != nil || health.GetStatus() != "ok" {
		t.Fatal("expected grpc health response")
	}

	if _, err := client.AddVector(ctx, &lumenvecpb.AddVectorRequest{
		Id:     "doc-1",
		Values: []float64{1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := client.GetVector(ctx, &lumenvecpb.GetVectorRequest{Id: "doc-1"})
	if err != nil || got.GetVector().GetId() != "doc-1" {
		t.Fatal("expected grpc get response")
	}

	search, err := client.Search(ctx, &lumenvecpb.SearchRequest{
		Values: []float64{1, 2, 3.1},
		TopK:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.GetResults()) != 1 || search.GetResults()[0].GetId() != "doc-1" {
		t.Fatal("expected grpc search result")
	}

	if _, err := client.DeleteVector(ctx, &lumenvecpb.DeleteVectorRequest{Id: "doc-1"}); err != nil {
		t.Fatal(err)
	}
}

func TestGRPCConcurrentSearch(t *testing.T) {
	base := t.TempDir()
	svc := core.NewService(core.ServiceOptions{
		MaxVectorDim:  16,
		MaxK:          5,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "ann",
	})
	if err := svc.AddVector("doc-1", []float64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}

	listener := bufconn.Listen(1024 * 1024)
	apiServer := NewServerWithOptions(ServerOptions{
		Port:          ":0",
		ReadTimeout:   5 * time.Second,
		WriteTimeout:  5 * time.Second,
		MaxVectorDim:  16,
		MaxK:          5,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "ann",
	})
	apiServer.service = svc
	server, err := apiServer.grpcServer()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	go func() {
		_ = server.Serve(listener)
	}()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	client := lumenvecpb.NewVectorServiceClient(conn)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = client.Search(ctx, &lumenvecpb.SearchRequest{
				Values: []float64{1, 2, 3.1},
				TopK:   1,
			})
		}()
	}
	wg.Wait()
}

func TestGRPCBatchAndErrorMappings(t *testing.T) {
	base := t.TempDir()
	handler := &grpcHandler{service: core.NewService(core.ServiceOptions{
		MaxVectorDim:  16,
		MaxK:          5,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "ann",
	})}

	if _, err := handler.AddVectorsBatch(context.Background(), &lumenvecpb.AddVectorsBatchRequest{
		Vectors: []*lumenvecpb.Vector{{Id: "doc-1", Values: []float64{1, 2, 3}}},
	}); err != nil {
		t.Fatal(err)
	}

	list, err := handler.ListVectors(context.Background(), &lumenvecpb.ListVectorsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetVectors()) != 1 || list.GetVectors()[0].GetId() != "doc-1" {
		t.Fatalf("expected grpc list response, got %+v", list.GetVectors())
	}

	resp, err := handler.SearchBatch(context.Background(), &lumenvecpb.SearchBatchRequest{
		Queries: []*lumenvecpb.SearchBatchQuery{{Id: "q1", Values: []float64{1, 2, 3}, TopK: 1}},
	})
	if err != nil || len(resp.GetResults()) != 1 || resp.GetResults()[0].GetId() != "q1" {
		t.Fatal("expected grpc batch search result")
	}

	if _, err := handler.AddVector(context.Background(), &lumenvecpb.AddVectorRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", err)
	}
	if _, err := handler.GetVector(context.Background(), &lumenvecpb.GetVectorRequest{Id: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected not found, got %v", err)
	}
	if _, err := handler.DeleteVector(context.Background(), &lumenvecpb.DeleteVectorRequest{Id: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected not found, got %v", err)
	}

	if grpcStatusFromError(nil) != nil {
		t.Fatal("expected nil grpc error")
	}
	if status.Code(grpcStatusFromError(index.ErrVectorExists)) != codes.AlreadyExists {
		t.Fatal("expected already exists code")
	}
	if status.Code(grpcStatusFromError(index.ErrVectorNotFound)) != codes.NotFound {
		t.Fatal("expected not found code")
	}
	if status.Code(grpcStatusFromError(core.ErrInvalidValues)) != codes.InvalidArgument {
		t.Fatal("expected invalid argument code")
	}
	if status.Code(grpcStatusFromError(errors.New("boom"))) != codes.Internal {
		t.Fatal("expected internal code")
	}
}

func TestGRPCListVectorsPagination(t *testing.T) {
	base := t.TempDir()
	handler := &grpcHandler{service: core.NewService(core.ServiceOptions{
		MaxVectorDim:  16,
		MaxK:          5,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "exact",
	})}

	for _, vec := range []*lumenvecpb.Vector{
		{Id: "a", Values: []float64{1}},
		{Id: "b", Values: []float64{2}},
		{Id: "c", Values: []float64{3}},
	} {
		if _, err := handler.AddVector(context.Background(), &lumenvecpb.AddVectorRequest{
			Id:     vec.GetId(),
			Values: vec.GetValues(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := handler.ListVectors(context.Background(), &lumenvecpb.ListVectorsRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.GetVectors()) != 2 || first.GetVectors()[0].GetId() != "a" || first.GetVectors()[1].GetId() != "b" {
		t.Fatalf("first page = %+v, want a,b", first.GetVectors())
	}
	if first.GetNextCursor() == "" {
		t.Fatal("expected next cursor")
	}

	second, err := handler.ListVectors(context.Background(), &lumenvecpb.ListVectorsRequest{Cursor: first.GetNextCursor(), IdsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.GetVectors()) != 1 || second.GetVectors()[0].GetId() != "c" {
		t.Fatalf("second page = %+v, want c", second.GetVectors())
	}
	if len(second.GetVectors()[0].GetValues()) != 0 {
		t.Fatalf("ids_only vector included values: %+v", second.GetVectors()[0])
	}
	if second.GetNextCursor() != "" {
		t.Fatalf("second next cursor = %q, want empty", second.GetNextCursor())
	}

	if _, err := handler.ListVectors(context.Background(), &lumenvecpb.ListVectorsRequest{Limit: -1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("negative limit code = %v, want invalid argument", status.Code(err))
	}
	if _, err := handler.ListVectors(context.Background(), &lumenvecpb.ListVectorsRequest{Cursor: "!"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad cursor code = %v, want invalid argument", status.Code(err))
	}
}

func TestGRPCAuthInterceptor(t *testing.T) {
	base := t.TempDir()
	server := NewServerWithOptions(ServerOptions{
		Port:            ":0",
		ReadTimeout:     5 * time.Second,
		WriteTimeout:    5 * time.Second,
		MaxVectorDim:    16,
		MaxK:            5,
		SnapshotPath:    filepath.Join(base, "snapshot.json"),
		WALPath:         filepath.Join(base, "wal.log"),
		SnapshotEvery:   2,
		SearchMode:      "exact",
		AuthEnabled:     true,
		AuthAPIKey:      "secret",
		GRPCAuthEnabled: true,
	})

	listener := bufconn.Listen(1024 * 1024)
	grpcServer, err := server.grpcServer()
	if err != nil {
		t.Fatal(err)
	}
	defer grpcServer.Stop()

	go func() {
		_ = grpcServer.Serve(listener)
	}()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	client := lumenvecpb.NewVectorServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.AddVector(ctx, &lumenvecpb.AddVectorRequest{Id: "doc-1", Values: []float64{1, 2, 3}}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated without credentials, got %v", err)
	}

	authCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer secret"))
	if _, err := client.AddVector(authCtx, &lumenvecpb.AddVectorRequest{Id: "doc-1", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatalf("expected authenticated grpc request, got %v", err)
	}

	if _, err := client.Health(ctx, &lumenvecpb.HealthRequest{}); err != nil {
		t.Fatalf("expected health to remain public, got %v", err)
	}
}

func TestGRPCServerTLSConfigError(t *testing.T) {
	server := NewServerWithOptions(ServerOptions{
		Port:            ":0",
		GRPCAuthEnabled: true,
		TLSEnabled:      true,
		TLSCertFile:     "missing-cert.pem",
		TLSKeyFile:      "missing-key.pem",
	})
	if _, err := server.grpcServer(); err == nil {
		t.Fatal("expected grpcServer to fail with missing TLS files")
	}
}

func TestGRPCAuthInterceptorMissingMetadataAndXAPIKey(t *testing.T) {
	server := NewServerWithOptions(ServerOptions{
		Port:            ":0",
		AuthEnabled:     true,
		AuthAPIKey:      "secret",
		GRPCAuthEnabled: true,
	})
	interceptor := server.grpcAuthInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: "/lumenvec.VectorService/AddVector"}
	handler := func(context.Context, interface{}) (interface{}, error) {
		return "ok", nil
	}

	if _, err := interceptor(context.Background(), nil, info, handler); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected missing metadata to be unauthenticated, got %v", err)
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", " secret "))
	got, err := interceptor(ctx, nil, info, handler)
	if err != nil || got != "ok" {
		t.Fatalf("expected x-api-key auth to pass, got %v err=%v", got, err)
	}
}
