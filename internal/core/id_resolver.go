package core

import "sync"

type memoryIDResolver struct {
	mu           sync.RWMutex
	idToInternal map[string]int
	internalToID map[int]string
	nextInternal int
}

func newMemoryIDResolver() *memoryIDResolver {
	return &memoryIDResolver{
		idToInternal: make(map[string]int),
		internalToID: make(map[int]string),
	}
}

func (r *memoryIDResolver) Assign(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	if internalID, ok := r.idToInternal[id]; ok {
		return internalID
	}
	r.nextInternal++
	internalID := r.nextInternal
	r.idToInternal[id] = internalID
	r.internalToID[internalID] = id
	return internalID
}

func (r *memoryIDResolver) Lookup(internalID int) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.internalToID[internalID]
	return id, ok
}

func (r *memoryIDResolver) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	internalID, ok := r.idToInternal[id]
	if !ok {
		return
	}
	delete(r.idToInternal, id)
	delete(r.internalToID, internalID)
}
