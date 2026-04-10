package ann

import (
	"fmt"
	"slices"
	"testing"
)

func TestAnnIndexBasicSearch(t *testing.T) {
	idx := NewAnnIndexWithOptions(Options{
		M:              8,
		EfConstruction: 32,
		EfSearch:       32,
		Seed:           7,
	})

	_ = idx.AddVector(1, []float64{0, 0})
	_ = idx.AddVector(2, []float64{1, 1})
	_ = idx.AddVector(3, []float64{10, 10})

	got, err := idx.Search([]float64{0.1, 0.1}, 2)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 neighbors, got %d", len(got))
	}
	if got[0] != 1 && got[0] != 2 {
		t.Fatalf("unexpected nearest id: %d", got[0])
	}
}

func TestAnnIndexDimensionValidation(t *testing.T) {
	idx := NewAnnIndex()
	if err := idx.AddVector(1, []float64{1, 2, 3}); err != nil {
		t.Fatalf("unexpected add error: %v", err)
	}
	if err := idx.AddVector(2, []float64{1, 2}); err == nil {
		t.Fatal("expected dimension error")
	}
	if _, err := idx.Search([]float64{1, 2}, 1); err == nil {
		t.Fatal("expected dimension error")
	}
}

func TestAnnIndexConcurrentAccess(t *testing.T) {
	idx := NewAnnIndex()
	done := make(chan struct{})

	for i := 0; i < 50; i++ {
		go func(n int) {
			_ = idx.AddVector(n, []float64{float64(n), float64(n + 1)})
			_, _ = idx.Search([]float64{1, 2}, 1)
			done <- struct{}{}
		}(i + 1)
	}

	for i := 0; i < 50; i++ {
		<-done
	}
}

func BenchmarkAnnSearch(b *testing.B) {
	idx := NewAnnIndexWithOptions(Options{
		M:              16,
		EfConstruction: 64,
		EfSearch:       64,
		Seed:           9,
	})

	for i := 0; i < 2000; i++ {
		v := []float64{float64(i % 97), float64((i * 3) % 89), float64((i * 7) % 83)}
		_ = idx.AddVector(i, v)
	}

	query := []float64{12.4, 18.2, 7.1}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := idx.Search(query, 10)
		if err != nil {
			b.Fatalf("search failed: %v", err)
		}
	}
}

func BenchmarkAnnSearchTuning(b *testing.B) {
	cases := []struct {
		name string
		opts Options
	}{
		{
			name: "m8_ef32_32",
			opts: Options{M: 8, EfConstruction: 32, EfSearch: 32, Seed: 9},
		},
		{
			name: "m16_ef64_64",
			opts: Options{M: 16, EfConstruction: 64, EfSearch: 64, Seed: 9},
		},
		{
			name: "m24_ef96_48",
			opts: Options{M: 24, EfConstruction: 96, EfSearch: 48, Seed: 9},
		},
		{
			name: "m24_ef96_96",
			opts: Options{M: 24, EfConstruction: 96, EfSearch: 96, Seed: 9},
		},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			idx := buildBenchmarkAnnIndex(tc.opts)
			query := []float64{12.4, 18.2, 7.1}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := idx.Search(query, 10)
				if err != nil {
					b.Fatalf("search failed: %v", err)
				}
			}
		})
	}
}

func TestAnnSearchTuningRecall(t *testing.T) {
	dataset := benchmarkAnnDataset(512)
	queries := [][]float64{
		{1.2, 5.4, 7.8},
		{9.1, 3.3, 4.4},
		{12.4, 18.2, 7.1},
		{22.0, 11.5, 0.9},
		{31.7, 6.8, 15.3},
	}
	expected := make([][]int, 0, len(queries))
	for _, query := range queries {
		expected = append(expected, bruteForceTopK(dataset, query, 10))
	}

	cases := []struct {
		name      string
		opts      Options
		minRecall float64
	}{
		{
			name:      "m8_ef32_32",
			opts:      Options{M: 8, EfConstruction: 32, EfSearch: 32, Seed: 9},
			minRecall: 0.50,
		},
		{
			name:      "m16_ef64_64",
			opts:      Options{M: 16, EfConstruction: 64, EfSearch: 64, Seed: 9},
			minRecall: 0.80,
		},
		{
			name:      "m24_ef96_96",
			opts:      Options{M: 24, EfConstruction: 96, EfSearch: 96, Seed: 9},
			minRecall: 0.85,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := NewAnnIndexWithOptions(tc.opts)
			for _, item := range dataset {
				if err := idx.AddVector(item.id, item.vector); err != nil {
					t.Fatalf("add failed: %v", err)
				}
			}

			var matched int
			var total int
			for i, query := range queries {
				got, err := idx.Search(query, 10)
				if err != nil {
					t.Fatalf("search failed: %v", err)
				}
				total += len(expected[i])
				for _, id := range got {
					if slices.Contains(expected[i], id) {
						matched++
					}
				}
			}

			recall := float64(matched) / float64(total)
			if recall < tc.minRecall {
				t.Fatalf("recall %.2f below threshold %.2f", recall, tc.minRecall)
			}
		})
	}
}

type benchmarkAnnItem struct {
	id     int
	vector []float64
}

func buildBenchmarkAnnIndex(opts Options) *AnnIndex {
	idx := NewAnnIndexWithOptions(opts)
	for _, item := range benchmarkAnnDataset(2000) {
		_ = idx.AddVector(item.id, item.vector)
	}
	return idx
}

func benchmarkAnnDataset(n int) []benchmarkAnnItem {
	out := make([]benchmarkAnnItem, 0, n)
	for i := 0; i < n; i++ {
		v := []float64{float64(i % 97), float64((i * 3) % 89), float64((i * 7) % 83)}
		out = append(out, benchmarkAnnItem{id: i, vector: v})
	}
	return out
}

func bruteForceTopK(dataset []benchmarkAnnItem, query []float64, k int) []int {
	type scored struct {
		id       int
		distance float64
	}
	scoredItems := make([]scored, 0, len(dataset))
	for _, item := range dataset {
		scoredItems = append(scoredItems, scored{
			id:       item.id,
			distance: euclideanDistance(query, item.vector),
		})
	}
	slices.SortFunc(scoredItems, func(a, b scored) int {
		switch {
		case a.distance < b.distance:
			return -1
		case a.distance > b.distance:
			return 1
		default:
			return 0
		}
	})
	if k > len(scoredItems) {
		k = len(scoredItems)
	}
	out := make([]int, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, scoredItems[i].id)
	}
	return out
}

func ExampleAnnIndex_searchTuning() {
	fmt.Println("ann tuning defaults: m=16 ef_construction=64 ef_search=64")
	// Output: ann tuning defaults: m=16 ef_construction=64 ef_search=64
}
