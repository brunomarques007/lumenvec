package core

import (
	"sync"

	"lumenvec/internal/index"
)

type VectorStore interface {
	UpsertVector(vec index.Vector) error
	GetVector(id string) (index.Vector, error)
	DeleteVector(id string) error
	ListVectors() []index.Vector
}

type readOnlyVectorReader interface {
	GetVectorReadOnly(id string) (index.Vector, error)
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
	vectors map[string]index.Vector
}

func newMemoryVectorStore() *memoryVectorStore {
	return &memoryVectorStore{vectors: make(map[string]index.Vector)}
}

func (s *memoryVectorStore) UpsertVector(vec index.Vector) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.vectors[vec.ID] = index.Vector{ID: vec.ID, Values: cloneVectorValues(vec.Values)}
	return nil
}

func (s *memoryVectorStore) GetVector(id string) (index.Vector, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	vec, ok := s.vectors[id]
	if !ok {
		return index.Vector{}, index.ErrVectorNotFound
	}
	return index.Vector{ID: vec.ID, Values: cloneVectorValues(vec.Values)}, nil
}

func (s *memoryVectorStore) GetVectorReadOnly(id string) (index.Vector, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	vec, ok := s.vectors[id]
	if !ok {
		return index.Vector{}, index.ErrVectorNotFound
	}
	return vec, nil
}

func (s *memoryVectorStore) DeleteVector(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.vectors[id]; !ok {
		return index.ErrVectorNotFound
	}
	delete(s.vectors, id)
	return nil
}

func (s *memoryVectorStore) ListVectors() []index.Vector {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]index.Vector, 0, len(s.vectors))
	for _, vec := range s.vectors {
		out = append(out, index.Vector{ID: vec.ID, Values: cloneVectorValues(vec.Values)})
	}
	return out
}

func (s *memoryVectorStore) IsPersistent() bool {
	return false
}

func cloneVectorValues(values []float64) []float64 {
	out := make([]float64, len(values))
	copy(out, values)
	return out
}
