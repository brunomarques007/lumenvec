package index

import (
	"errors"
	"sync"
)

var (
	ErrVectorExists   = errors.New("vector with this ID already exists")
	ErrVectorNotFound = errors.New("vector not found")
)

// Vector represents a mathematical vector.
type Vector struct {
	ID     string
	Values []float64
}

// Index manages a collection of vectors for searching.
type Index struct {
	vectors map[string]Vector
	mu      sync.RWMutex
}

// NewIndex creates a new instance of Index.
func NewIndex() *Index {
	return &Index{
		vectors: make(map[string]Vector),
	}
}

// AddVector adds a new vector to the index.
func (i *Index) AddVector(vec Vector) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if _, exists := i.vectors[vec.ID]; exists {
		return ErrVectorExists
	}
	i.vectors[vec.ID] = Vector{ID: vec.ID, Values: cloneValues(vec.Values)}
	return nil
}

// SearchVector searches for a vector by its ID.
func (i *Index) SearchVector(id string) (Vector, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	vec, exists := i.vectors[id]
	if !exists {
		return Vector{}, ErrVectorNotFound
	}
	return Vector{ID: vec.ID, Values: cloneValues(vec.Values)}, nil
}

// DeleteVector removes a vector from the index by its ID.
func (i *Index) DeleteVector(id string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if _, exists := i.vectors[id]; !exists {
		return ErrVectorNotFound
	}
	delete(i.vectors, id)
	return nil
}

// ListVectors returns a snapshot copy of all indexed vectors.
func (i *Index) ListVectors() []Vector {
	i.mu.RLock()
	defer i.mu.RUnlock()

	out := make([]Vector, 0, len(i.vectors))
	for _, vec := range i.vectors {
		out = append(out, Vector{ID: vec.ID, Values: cloneValues(vec.Values)})
	}
	return out
}

// RangeVectors iterates over vectors under a read lock without copying each value slice.
// Callers must treat the provided vectors as read-only and must not retain references.
func (i *Index) RangeVectors(fn func(Vector) bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	for _, vec := range i.vectors {
		if !fn(vec) {
			return
		}
	}
}

func cloneValues(values []float64) []float64 {
	out := make([]float64, len(values))
	copy(out, values)
	return out
}
