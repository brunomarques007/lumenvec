package core

import (
	"sync"
	"testing"
	"time"

	"lumenvec/internal/index"
)

type countingVectorStore struct {
	mu      sync.Mutex
	vectors map[string]index.Vector
	gets    int
}

func newCountingVectorStore() *countingVectorStore {
	return &countingVectorStore{vectors: make(map[string]index.Vector)}
}

func (s *countingVectorStore) UpsertVector(vec index.Vector) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vectors[vec.ID] = index.Vector{ID: vec.ID, Values: cloneVectorValues(vec.Values)}
	return nil
}

func (s *countingVectorStore) GetVector(id string) (index.Vector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	vec, ok := s.vectors[id]
	if !ok {
		return index.Vector{}, index.ErrVectorNotFound
	}
	return index.Vector{ID: vec.ID, Values: cloneVectorValues(vec.Values)}, nil
}

func (s *countingVectorStore) DeleteVector(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.vectors, id)
	return nil
}

func (s *countingVectorStore) ListVectors() []index.Vector {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]index.Vector, 0, len(s.vectors))
	for _, vec := range s.vectors {
		out = append(out, index.Vector{ID: vec.ID, Values: cloneVectorValues(vec.Values)})
	}
	return out
}

func TestCachedVectorStoreHit(t *testing.T) {
	backend := newCountingVectorStore()
	if err := backend.UpsertVector(index.Vector{ID: "a", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	store := newCachedVectorStore(backend, CacheOptions{Enabled: true, MaxBytes: 1024, MaxItems: 2})

	if _, err := store.GetVector("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetVector("a"); err != nil {
		t.Fatal(err)
	}
	if backend.gets != 1 {
		t.Fatalf("expected backend get count 1, got %d", backend.gets)
	}
}

func TestCachedVectorStoreTTLExpiry(t *testing.T) {
	backend := newCountingVectorStore()
	if err := backend.UpsertVector(index.Vector{ID: "a", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	store := newCachedVectorStore(backend, CacheOptions{Enabled: true, MaxBytes: 1024, MaxItems: 2, TTL: 10 * time.Millisecond})

	if _, err := store.GetVector("a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := store.GetVector("a"); err != nil {
		t.Fatal(err)
	}
	if backend.gets != 2 {
		t.Fatalf("expected backend get count 2 after TTL expiry, got %d", backend.gets)
	}
}

func TestCachedVectorStoreEviction(t *testing.T) {
	backend := newCountingVectorStore()
	for _, id := range []string{"a", "b"} {
		if err := backend.UpsertVector(index.Vector{ID: id, Values: []float64{1, 2, 3}}); err != nil {
			t.Fatal(err)
		}
	}
	store := newCachedVectorStore(backend, CacheOptions{Enabled: true, MaxBytes: 1024, MaxItems: 1})

	if _, err := store.GetVector("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetVector("b"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetVector("a"); err != nil {
		t.Fatal(err)
	}
	if backend.gets != 3 {
		t.Fatalf("expected backend get count 3 with eviction, got %d", backend.gets)
	}
}

func TestCachedVectorStoreStats(t *testing.T) {
	backend := newCountingVectorStore()
	for _, id := range []string{"a", "b"} {
		if err := backend.UpsertVector(index.Vector{ID: id, Values: []float64{1, 2, 3}}); err != nil {
			t.Fatal(err)
		}
	}
	store := newCachedVectorStore(backend, CacheOptions{Enabled: true, MaxBytes: 1024, MaxItems: 1})
	cached, ok := store.(*cachedVectorStore)
	if !ok {
		t.Fatal("expected cachedVectorStore")
	}

	if _, err := cached.GetVector("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.GetVector("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.GetVector("b"); err != nil {
		t.Fatal(err)
	}

	stats := cached.Stats()
	if stats.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 2 {
		t.Fatalf("expected 2 misses, got %d", stats.Misses)
	}
	if stats.Evictions != 1 {
		t.Fatalf("expected 1 eviction, got %d", stats.Evictions)
	}
	if stats.Items != 1 {
		t.Fatalf("expected 1 cached item, got %d", stats.Items)
	}
	if stats.Bytes == 0 {
		t.Fatal("expected cached bytes to be tracked")
	}
}

func TestCachedVectorStoreEvictionByBytes(t *testing.T) {
	backend := newCountingVectorStore()
	if err := backend.UpsertVector(index.Vector{ID: "a", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	if err := backend.UpsertVector(index.Vector{ID: "b", Values: []float64{4, 5, 6}}); err != nil {
		t.Fatal(err)
	}

	store := newCachedVectorStore(backend, CacheOptions{
		Enabled:  true,
		MaxBytes: estimateVectorSizeBytes(index.Vector{ID: "a", Values: []float64{1, 2, 3}}),
		MaxItems: 10,
	})
	cached, ok := store.(*cachedVectorStore)
	if !ok {
		t.Fatal("expected cachedVectorStore")
	}

	if _, err := cached.GetVector("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.GetVector("b"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.GetVector("a"); err != nil {
		t.Fatal(err)
	}

	stats := cached.Stats()
	if stats.Evictions == 0 {
		t.Fatal("expected eviction by byte limit")
	}
}

func TestCachedVectorStoreConcurrentAccess(t *testing.T) {
	backend := newCountingVectorStore()
	store := newCachedVectorStore(backend, CacheOptions{
		Enabled:  true,
		MaxBytes: 1 << 20,
		MaxItems: 128,
		TTL:      time.Minute,
	})

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "vec-" + string(rune('a'+(n%8)))
			vec := index.Vector{ID: id, Values: []float64{float64(n), float64(n + 1), float64(n + 2)}}
			_ = store.UpsertVector(vec)
			_, _ = store.GetVector(id)
			_ = store.DeleteVector(id)
		}(i)
	}
	wg.Wait()
}
