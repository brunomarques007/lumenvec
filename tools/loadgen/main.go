package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	clientpkg "lumenvec/pkg/client"
)

type vectorClient interface {
	AddVectors(vectors []clientpkg.VectorPayload) error
	SearchVector(vector []float64, k int) ([]clientpkg.SearchResult, error)
	Close() error
}

type httpLoadClient struct {
	client *clientpkg.VectorClient
}

func (c *httpLoadClient) AddVectors(vectors []clientpkg.VectorPayload) error {
	return c.client.AddVectors(vectors)
}

func (c *httpLoadClient) SearchVector(vector []float64, k int) ([]clientpkg.SearchResult, error) {
	return c.client.SearchVector(vector, k)
}

func (c *httpLoadClient) Close() error { return nil }

func main() {
	var (
		transport = flag.String("transport", "http", "Transport to use: http or grpc")
		baseURL   = flag.String("base-url", "http://localhost:19190", "HTTP base URL of the LumenVec server")
		grpcAddr  = flag.String("grpc-addr", "localhost:19191", "gRPC address of the LumenVec server")
		prefix    = flag.String("prefix", "demo", "Prefix used for generated vector IDs")
		vectors   = flag.Int("vectors", 500, "Number of vectors to ingest")
		searches  = flag.Int("searches", 200, "Number of search requests to execute")
		dim       = flag.Int("dim", 8, "Vector dimension")
		batchSize = flag.Int("batch-size", 100, "Batch size used for ingest")
		topK      = flag.Int("k", 5, "Top-k used for searches")
	)
	flag.Parse()

	if *vectors <= 0 || *searches < 0 || *dim <= 0 || *batchSize <= 0 || *topK <= 0 {
		fmt.Fprintln(os.Stderr, "vectors, searches, dim, batch-size, and k must be positive")
		os.Exit(1)
	}

	client, target, err := newLoadClient(strings.ToLower(strings.TrimSpace(*transport)), *baseURL, *grpcAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "client init failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	fmt.Printf("Ingesting %d vectors in batches of %d against %s via %s\n", *vectors, *batchSize, target, *transport)
	namespace := namespaceOffset(*prefix)
	if err := ingestVectors(client, *prefix, namespace, *vectors, *dim, *batchSize); err != nil {
		fmt.Fprintf(os.Stderr, "ingest failed: %v\n", err)
		os.Exit(1)
	}

	if *searches > 0 {
		fmt.Printf("Running %d search requests with k=%d via %s\n", *searches, *topK, *transport)
		stats, err := runSearches(client, namespace, *vectors, *searches, *dim, *topK)
		if err != nil {
			fmt.Fprintf(os.Stderr, "search load failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Search summary: count=%d avg_ms=%.2f max_ms=%.2f sample_top1=%s\n",
			stats.count,
			stats.total.Seconds()*1000/float64(stats.count),
			stats.max.Seconds()*1000,
			stats.sampleTop1,
		)
	}

	fmt.Println("Load generation completed.")
}

func newLoadClient(transport, baseURL, grpcAddr string) (vectorClient, string, error) {
	switch transport {
	case "http":
		base := strings.TrimRight(baseURL, "/")
		return &httpLoadClient{client: clientpkg.NewVectorClient(base)}, base, nil
	case "grpc":
		grpcClient, err := clientpkg.NewGRPCVectorClient(grpcAddr)
		if err != nil {
			return nil, "", err
		}
		return grpcClient, grpcAddr, nil
	default:
		return nil, "", fmt.Errorf("unsupported transport %q", transport)
	}
}

func ingestVectors(client vectorClient, prefix string, namespace float64, total, dim, batchSize int) error {
	for start := 0; start < total; start += batchSize {
		end := min(start+batchSize, total)
		vectors := make([]clientpkg.VectorPayload, 0, end-start)
		for i := start; i < end; i++ {
			vectors = append(vectors, clientpkg.VectorPayload{
				ID:     fmt.Sprintf("%s-%06d", prefix, i),
				Values: generatedVector(namespace, i, dim),
			})
		}

		if err := client.AddVectors(vectors); err != nil {
			return fmt.Errorf("batch [%d:%d): %w", start, end, err)
		}
	}
	return nil
}

type searchStats struct {
	count      int
	total      time.Duration
	max        time.Duration
	sampleTop1 string
}

func runSearches(client vectorClient, namespace float64, totalVectors, searches, dim, topK int) (searchStats, error) {
	var stats searchStats
	for i := 0; i < searches; i++ {
		target := i % totalVectors
		query := perturbVector(generatedVector(namespace, target, dim), i)

		start := time.Now()
		results, err := client.SearchVector(query, topK)
		elapsed := time.Since(start)
		if err != nil {
			return searchStats{}, fmt.Errorf("search %d: %w", i, err)
		}

		stats.count++
		stats.total += elapsed
		if elapsed > stats.max {
			stats.max = elapsed
		}
		if i == 0 && len(results) > 0 {
			stats.sampleTop1 = results[0].ID
		}
	}
	return stats, nil
}

func generatedVector(namespace float64, seed, dim int) []float64 {
	cluster := seed % 10
	values := make([]float64, dim)
	base := namespace + float64(cluster)*10
	for i := range values {
		values[i] = base + math.Sin(float64(seed+i))*0.25 + float64((seed+i)%7)/100
	}
	return values
}

func perturbVector(values []float64, seed int) []float64 {
	out := make([]float64, len(values))
	copy(out, values)
	for i := range out {
		out[i] += math.Cos(float64(seed+i)) * 0.01
	}
	return out
}

func namespaceOffset(prefix string) float64 {
	var sum int
	for _, ch := range prefix {
		sum += int(ch)
	}
	return float64(sum%1000) * 100
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
