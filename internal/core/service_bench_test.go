package core

import (
	"fmt"
	"path/filepath"
	"testing"

	"lumenvec/internal/index"
)

func BenchmarkServiceAddVector(b *testing.B) {
	for _, batchSize := range []int{1, 32} {
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				svc := benchmarkService(b, "exact")
				if batchSize == 1 {
					if err := svc.AddVector("vec-0", benchmarkVector(256, 1)); err != nil {
						b.Fatal(err)
					}
					continue
				}

				vectors := make([]index.Vector, 0, batchSize)
				for j := 0; j < batchSize; j++ {
					vectors = append(vectors, index.Vector{
						ID:     fmt.Sprintf("vec-%d", j),
						Values: benchmarkVector(256, float64(j)),
					})
				}
				if err := svc.AddVectors(vectors); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkServiceSearch(b *testing.B) {
	for _, mode := range []string{"exact", "ann"} {
		b.Run(mode, func(b *testing.B) {
			svc := benchmarkService(b, mode)
			vectors := make([]index.Vector, 0, 512)
			for i := 0; i < 512; i++ {
				vectors = append(vectors, index.Vector{
					ID:     fmt.Sprintf("vec-%d", i),
					Values: benchmarkVector(256, float64(i%13)),
				})
			}
			if err := svc.AddVectors(vectors); err != nil {
				b.Fatal(err)
			}

			query := benchmarkVector(256, 3)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := svc.Search(query, 10); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkServiceGetVector(b *testing.B) {
	svc := benchmarkService(b, "exact")
	if err := svc.AddVector("vec-0", benchmarkVector(256, 1)); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := svc.GetVector("vec-0"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkServiceGetVectorByStore(b *testing.B) {
	for _, tc := range []struct {
		name  string
		store string
		cache CacheOptions
	}{
		{name: "memory", store: "memory"},
		{name: "disk", store: "disk"},
		{
			name:  "disk_cache",
			store: "disk",
			cache: CacheOptions{Enabled: true, MaxBytes: 8 << 20, MaxItems: 1024},
		},
	} {
		b.Run(tc.name, func(b *testing.B) {
			svc := benchmarkServiceWithStore(b, "exact", tc.store, tc.cache)
			if err := svc.AddVector("vec-0", benchmarkVector(256, 1)); err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := svc.GetVector("vec-0"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkServiceSearchByStore(b *testing.B) {
	for _, tc := range []struct {
		name  string
		mode  string
		store string
		cache CacheOptions
	}{
		{name: "exact_memory", mode: "exact", store: "memory"},
		{name: "exact_disk", mode: "exact", store: "disk"},
		{name: "ann_disk", mode: "ann", store: "disk"},
		{
			name:  "ann_disk_cache",
			mode:  "ann",
			store: "disk",
			cache: CacheOptions{Enabled: true, MaxBytes: 8 << 20, MaxItems: 1024},
		},
	} {
		b.Run(tc.name, func(b *testing.B) {
			svc := benchmarkServiceWithStore(b, tc.mode, tc.store, tc.cache)
			vectors := make([]index.Vector, 0, 512)
			for i := 0; i < 512; i++ {
				vectors = append(vectors, index.Vector{
					ID:     fmt.Sprintf("vec-%d", i),
					Values: benchmarkVector(256, float64(i%13)),
				})
			}
			if err := svc.AddVectors(vectors); err != nil {
				b.Fatal(err)
			}

			query := benchmarkVector(256, 3)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := svc.Search(query, 10); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkServiceSearchBatch(b *testing.B) {
	svc := benchmarkService(b, "exact")
	vectors := make([]index.Vector, 0, 512)
	for i := 0; i < 512; i++ {
		vectors = append(vectors, index.Vector{
			ID:     fmt.Sprintf("vec-%d", i),
			Values: benchmarkVector(256, float64(i%17)),
		})
	}
	if err := svc.AddVectors(vectors); err != nil {
		b.Fatal(err)
	}

	singleQuery := benchmarkVector(256, 5)
	batchQueries := make([]BatchSearchQuery, 0, 16)
	for i := 0; i < 16; i++ {
		batchQueries = append(batchQueries, BatchSearchQuery{
			ID:     fmt.Sprintf("q-%d", i),
			Values: benchmarkVector(256, float64(i)),
			K:      10,
		})
	}

	b.Run("single_x16", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for j := 0; j < 16; j++ {
				if _, err := svc.Search(singleQuery, 10); err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Run("batch_16", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := svc.SearchBatch(batchQueries); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkServiceSearchBatchScales(b *testing.B) {
	for _, queryCount := range []int{4, 16, 64} {
		b.Run(fmt.Sprintf("queries_%d", queryCount), func(b *testing.B) {
			svc := benchmarkService(b, "exact")
			vectors := make([]index.Vector, 0, 1024)
			for i := 0; i < 1024; i++ {
				vectors = append(vectors, index.Vector{
					ID:     fmt.Sprintf("vec-%d", i),
					Values: benchmarkVector(256, float64(i%29)),
				})
			}
			if err := svc.AddVectors(vectors); err != nil {
				b.Fatal(err)
			}

			queries := make([]BatchSearchQuery, 0, queryCount)
			for i := 0; i < queryCount; i++ {
				queries = append(queries, BatchSearchQuery{
					ID:     fmt.Sprintf("q-%d", i),
					Values: benchmarkVector(256, float64(i%11)),
					K:      10,
				})
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := svc.SearchBatch(queries); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkService(tb testing.TB, mode string) *Service {
	return benchmarkServiceWithStore(tb, mode, "memory", CacheOptions{})
}

func benchmarkServiceWithStore(tb testing.TB, mode, store string, cache CacheOptions) *Service {
	tb.Helper()
	base := tb.TempDir()
	svc := NewService(ServiceOptions{
		MaxVectorDim:  1024,
		MaxK:          64,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 1 << 30,
		SearchMode:    mode,
		VectorStore:   store,
		VectorPath:    filepath.Join(base, "vectors"),
		Cache:         cache,
	})
	tb.Cleanup(func() { _ = svc.Close() })
	return svc
}

func benchmarkVector(dim int, seed float64) []float64 {
	values := make([]float64, dim)
	for i := range values {
		values[i] = seed + float64(i%7)*0.125
	}
	return values
}
