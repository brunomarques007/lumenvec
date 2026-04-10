package api

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	lumenvecpb "lumenvec/api/proto"
	"lumenvec/internal/core"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	server := grpc.NewServer()
	lumenvecpb.RegisterVectorServiceServer(server, &grpcHandler{service: svc})
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
	defer conn.Close()

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
	server := grpc.NewServer()
	lumenvecpb.RegisterVectorServiceServer(server, &grpcHandler{service: svc})
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
	defer conn.Close()

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
