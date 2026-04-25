package core

import (
	"sync"

	"lumenvec/internal/index"
	"lumenvec/internal/vector"
)

type VectorStore interface {
	UpsertVector(vec index.Vector) error
	GetVector(id string) (index.Vector, error)
	DeleteVector(id string) error
	ListVectors() []index.Vector
}

type batchVectorStore interface {
	UpsertVectors(vectors []index.Vector) error
}

type readOnlyVectorReader interface {
	GetVectorReadOnly(id string) (index.Vector, error)
}

type readOnlyVector32Reader interface {
	// GetVectorReadOnly32 returns an internal read-only vector. Callers must not mutate it.
	GetVectorReadOnly32(id string) ([]float32, error)
}

type DiskStoreStats struct {
	FileBytes    uint64 `json:"file_bytes"`
	Records      uint64 `json:"records"`
	StaleRecords uint64 `json:"stale_records"`
	Compactions  uint64 `json:"compactions"`
}

type diskStatsReader interface {
	DiskStats() DiskStoreStats
}

type persistentVectorStore interface {
	IsPersistent() bool
}

type memoryVectorStore struct {
	mu      sync.RWMutex
	vectors map[string]memoryVector
	ids     *orderedIDIndex
}

type memoryVector struct {
	id     string
	values []float32
}

func newMemoryVectorStore() *memoryVectorStore {
	return &memoryVectorStore{
		vectors: make(map[string]memoryVector),
		ids:     newOrderedIDIndex(),
	}
}

func (s *memoryVectorStore) UpsertVector(vec index.Vector) error {
	return s.UpsertVectors([]index.Vector{vec})
}

func (s *memoryVectorStore) UpsertVectors(vectors []index.Vector) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, vec := range vectors {
		s.vectors[vec.ID] = memoryVector{id: vec.ID, values: vector.ToFloat32(vec.Values)}
		s.orderedIDsLocked().Upsert(vec.ID)
	}
	return nil
}

func (s *memoryVectorStore) GetVector(id string) (index.Vector, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	vec, ok := s.vectors[id]
	if !ok {
		return index.Vector{}, index.ErrVectorNotFound
	}
	return index.Vector{ID: vec.id, Values: vector.ToFloat64(vec.values)}, nil
}

func (s *memoryVectorStore) GetVectorReadOnly(id string) (index.Vector, error) {
	values, err := s.GetVectorReadOnly32(id)
	if err != nil {
		return index.Vector{}, err
	}
	return index.Vector{ID: id, Values: vector.ToFloat64(values)}, nil
}

func (s *memoryVectorStore) GetVectorReadOnly32(id string) ([]float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	vec, ok := s.vectors[id]
	if !ok {
		return nil, index.ErrVectorNotFound
	}
	return vec.values, nil
}

func (s *memoryVectorStore) DeleteVector(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.vectors[id]; !ok {
		return index.ErrVectorNotFound
	}
	delete(s.vectors, id)
	s.orderedIDsLocked().Delete(id)
	return nil
}

func (s *memoryVectorStore) ListVectors() []index.Vector {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]index.Vector, 0, len(s.vectors))
	for _, vec := range s.vectors {
		out = append(out, index.Vector{ID: vec.id, Values: vector.ToFloat64(vec.values)})
	}
	return out
}

func (s *memoryVectorStore) RangeVectorIDs(fn func(string) bool) {
	s.ensureOrderedIDs()
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.ids.Range(fn)
}

func (s *memoryVectorStore) PageVectorIDs(afterID string, limit int) []string {
	s.ensureOrderedIDs()
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.ids.PageAfter(afterID, limit)
}

func (s *memoryVectorStore) IsPersistent() bool {
	return false
}

func (s *memoryVectorStore) orderedIDsLocked() *orderedIDIndex {
	if s.ids == nil {
		s.ids = newOrderedIDIndexFromMap(s.vectors)
	}
	return s.ids
}

func (s *memoryVectorStore) ensureOrderedIDs() {
	s.mu.RLock()
	ready := s.ids != nil
	s.mu.RUnlock()
	if ready {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.orderedIDsLocked()
}

func cloneVectorValues(values []float64) []float64 {
	out := make([]float64, len(values))
	copy(out, values)
	return out
}
