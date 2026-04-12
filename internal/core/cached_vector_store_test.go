package core

import (
	"errors"
	"sync"
	"testing"
	"time"

	"lumenvec/internal/index"
)

type countingVectorStore struct {
	mu      sync.Mutex
	vectors map[string]index.Vector
	gets    int
	closed  bool
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

func (s *countingVectorStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

type readOnlyCountingVectorStore struct {
	*countingVectorStore
	readOnlyGets int
}

func (s *readOnlyCountingVectorStore) GetVectorReadOnly(id string) (index.Vector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readOnlyGets++
	vec, ok := s.vectors[id]
	if !ok {
		return index.Vector{}, index.ErrVectorNotFound
	}
	return vec, nil
}

type persistentCountingStore struct {
	*countingVectorStore
	diskStats DiskStoreStats
}

func (s *persistentCountingStore) IsPersistent() bool {
	return true
}

func (s *persistentCountingStore) DiskStats() DiskStoreStats {
	return s.diskStats
}

type failingDeleteStore struct {
	*countingVectorStore
}

func (s *failingDeleteStore) DeleteVector(string) error {
	return errors.New("delete failed")
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

func TestCachedVectorStoreReadOnlyAndBackendPassthroughs(t *testing.T) {
	backend := &readOnlyCountingVectorStore{countingVectorStore: newCountingVectorStore()}
	if err := backend.UpsertVector(index.Vector{ID: "a", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}

	store := newCachedVectorStore(backend, CacheOptions{Enabled: true, MaxBytes: 1024, MaxItems: 2})
	cached, ok := store.(*cachedVectorStore)
	if !ok {
		t.Fatal("expected cachedVectorStore")
	}

	vec, err := cached.GetVectorReadOnly("a")
	if err != nil {
		t.Fatal(err)
	}
	if vec.ID != "a" || backend.readOnlyGets != 1 {
		t.Fatal("expected read-only backend path")
	}

	vec.Values[0] = 99
	vec2, err := cached.GetVectorReadOnly("a")
	if err != nil {
		t.Fatal(err)
	}
	if vec2.Values[0] == 99 {
		t.Fatal("expected cached read-only value to remain immutable from caller perspective")
	}

	if len(cached.ListVectors()) != 1 {
		t.Fatal("expected list vectors passthrough")
	}
	if err := cached.Close(); err != nil || !backend.closed {
		t.Fatal("expected close passthrough")
	}
}

func TestCachedVectorStorePersistenceAndDiskStatsPassthrough(t *testing.T) {
	backend := &persistentCountingStore{
		countingVectorStore: newCountingVectorStore(),
		diskStats: DiskStoreStats{
			FileBytes:    123,
			Records:      4,
			StaleRecords: 1,
			Compactions:  2,
		},
	}
	store := newCachedVectorStore(backend, CacheOptions{Enabled: true, MaxBytes: 1024, MaxItems: 2})
	cached := store.(*cachedVectorStore)

	if !cached.IsPersistent() {
		t.Fatal("expected persistent backend passthrough")
	}
	stats := cached.DiskStats()
	if stats.FileBytes != 123 || stats.Compactions != 2 {
		t.Fatal("expected disk stats passthrough")
	}
}

func TestCachedVectorStorePutUpdateAndHelpers(t *testing.T) {
	store := newCachedVectorStore(newCountingVectorStore(), CacheOptions{Enabled: true, MaxBytes: 1024, MaxItems: 2})
	cached := store.(*cachedVectorStore)

	cached.mu.Lock()
	cached.putLocked(index.Vector{ID: "a", Values: []float64{1, 2, 3}})
	firstExpiry := cached.entries["a"].Value.(*cacheEntry).expiresAt
	cached.putLocked(index.Vector{ID: "a", Values: []float64{4, 5, 6}})
	cached.currentBytes = -5
	cached.mu.Unlock()

	stats := cached.Stats()
	if stats.Items != 1 || stats.Bytes != 0 {
		t.Fatal("expected updated entry and non-negative bytes")
	}
	if !firstExpiry.Equal(time.Time{}) && cached.entries["a"].Value.(*cacheEntry).expiresAt.Before(firstExpiry) {
		t.Fatal("expected expiry to move forward or remain zero")
	}

	if maxInt64(5, 1) != 5 || maxInt64(-1, 0) != 0 {
		t.Fatal("unexpected maxInt64 result")
	}
}

func TestCachedVectorStoreDisabledAndMissPaths(t *testing.T) {
	backend := newCountingVectorStore()
	store := newCachedVectorStore(backend, CacheOptions{})
	if store != backend {
		t.Fatal("expected disabled cache to return backend directly")
	}

	cached := newCachedVectorStore(newCountingVectorStore(), CacheOptions{Enabled: true, MaxBytes: 1024, MaxItems: 2}).(*cachedVectorStore)
	if _, ok := cached.getCachedReadOnly("missing"); ok {
		t.Fatal("expected cache miss")
	}
	if _, err := cached.GetVectorReadOnly("missing"); !errors.Is(err, index.ErrVectorNotFound) {
		t.Fatal("expected backend miss")
	}
}

func TestCachedVectorStoreDeleteErrorAndCloseWithoutCloser(t *testing.T) {
	store := newCachedVectorStore(&failingDeleteStore{countingVectorStore: newCountingVectorStore()}, CacheOptions{
		Enabled:  true,
		MaxBytes: 1024,
		MaxItems: 2,
	}).(*cachedVectorStore)
	if err := store.DeleteVector("missing"); err == nil {
		t.Fatal("expected delete error")
	}

	noCloser := newCachedVectorStore(struct{ VectorStore }{VectorStore: newMemoryVectorStore()}, CacheOptions{
		Enabled:  true,
		MaxBytes: 1024,
		MaxItems: 2,
	}).(*cachedVectorStore)
	if err := noCloser.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCachedVectorStoreDefaultsAndFallbackReadOnlyPath(t *testing.T) {
	store := newCachedVectorStore(nil, CacheOptions{Enabled: true})
	cached, ok := store.(*cachedVectorStore)
	if !ok {
		t.Fatal("expected cached vector store with default backend")
	}
	if cached.backend == nil || cached.maxBytes != 8<<20 || cached.maxItems != 1024 {
		t.Fatal("expected default cache configuration")
	}

	if err := cached.UpsertVector(index.Vector{ID: "a", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	vec, err := cached.GetVectorReadOnly("a")
	if err != nil {
		t.Fatal(err)
	}
	if vec.ID != "a" {
		t.Fatal("expected fallback read-only path through regular backend")
	}

	cached.mu.Lock()
	elem := cached.entries["a"]
	cached.removeElementLocked(nil)
	cached.removeElementLocked(elem)
	cached.mu.Unlock()
	if _, ok := cached.entries["a"]; ok {
		t.Fatal("expected element removal")
	}
}

func TestCachedVectorStoreReadOnlyCacheHitAndExpiry(t *testing.T) {
	backend := &readOnlyCountingVectorStore{countingVectorStore: newCountingVectorStore()}
	if err := backend.UpsertVector(index.Vector{ID: "a", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	cached := newCachedVectorStore(backend, CacheOptions{
		Enabled:  true,
		MaxBytes: 1024,
		MaxItems: 2,
		TTL:      10 * time.Millisecond,
	}).(*cachedVectorStore)

	if _, err := cached.GetVectorReadOnly("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cached.getCachedReadOnly("a"); !ok {
		t.Fatal("expected read-only cache hit")
	}

	time.Sleep(20 * time.Millisecond)
	if _, ok := cached.getCachedReadOnly("a"); ok {
		t.Fatal("expected expired read-only entry")
	}
}
