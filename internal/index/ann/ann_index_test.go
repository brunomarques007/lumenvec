package ann

import "testing"

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
