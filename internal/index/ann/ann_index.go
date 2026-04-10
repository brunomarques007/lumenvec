package ann

import (
	"errors"
	"math"
	"math/rand"
	"sync"
)

var (
	ErrInvalidK         = errors.New("k must be greater than 0")
	ErrInvalidVectorDim = errors.New("query dimension mismatch")
)

type node struct {
	id        int
	vector    []float64
	neighbors map[int]struct{}
}

// AnnIndex is a graph-based ANN index inspired by HNSW/NSW principles.
type AnnIndex struct {
	nodes          map[int]*node
	entrypoint     int
	hasEntrypoint  bool
	dim            int
	m              int
	efConstruction int
	efSearch       int
	rnd            *rand.Rand
	mu             sync.RWMutex
}

type Options struct {
	M              int
	EfConstruction int
	EfSearch       int
	Seed           int64
}

func NewAnnIndex() *AnnIndex {
	return NewAnnIndexWithOptions(Options{})
}

func NewAnnIndexWithOptions(opts Options) *AnnIndex {
	if opts.M <= 0 {
		opts.M = 16
	}
	if opts.EfConstruction <= 0 {
		opts.EfConstruction = 64
	}
	if opts.EfSearch <= 0 {
		opts.EfSearch = 64
	}
	if opts.Seed == 0 {
		opts.Seed = 42
	}
	return &AnnIndex{
		nodes:          make(map[int]*node),
		m:              opts.M,
		efConstruction: opts.EfConstruction,
		efSearch:       opts.EfSearch,
		rnd:            rand.New(rand.NewSource(opts.Seed)),
	}
}

func (a *AnnIndex) AddVector(id int, vector []float64) error {
	if len(vector) == 0 {
		return ErrInvalidVectorDim
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.dim == 0 {
		a.dim = len(vector)
	}
	if len(vector) != a.dim {
		return ErrInvalidVectorDim
	}

	vecCopy := make([]float64, len(vector))
	copy(vecCopy, vector)

	if existing, ok := a.nodes[id]; ok {
		existing.vector = vecCopy
		return nil
	}

	n := &node{
		id:        id,
		vector:    vecCopy,
		neighbors: make(map[int]struct{}),
	}
	a.nodes[id] = n

	if !a.hasEntrypoint {
		a.entrypoint = id
		a.hasEntrypoint = true
		return nil
	}

	candidates := a.searchCandidatesLocked(vecCopy, a.efConstruction)
	linkIDs := nearestIDs(candidates, a.m)
	for _, neighborID := range linkIDs {
		if neighborID == id {
			continue
		}
		n.neighbors[neighborID] = struct{}{}
		if neighbor, ok := a.nodes[neighborID]; ok {
			neighbor.neighbors[id] = struct{}{}
			a.pruneNeighborsLocked(neighbor)
		}
	}
	a.pruneNeighborsLocked(n)

	if a.rnd.Intn(100) < 5 {
		a.entrypoint = id
	}
	return nil
}

func (a *AnnIndex) Search(query []float64, k int) ([]int, error) {
	if k <= 0 {
		return nil, ErrInvalidK
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(query) == 0 || len(query) != a.dim {
		return nil, ErrInvalidVectorDim
	}
	if len(a.nodes) == 0 {
		return []int{}, nil
	}

	candidates := a.searchCandidatesLocked(query, a.efSearch)
	results := nearestIDs(candidates, k)
	return results, nil
}

func (a *AnnIndex) searchCandidatesLocked(query []float64, ef int) []distancePair {
	if ef <= 0 {
		ef = a.efSearch
	}

	visited := make(map[int]struct{}, ef*2)
	minQ := make(minDistHeap, 0, ef)
	maxQ := make(maxDistHeap, 0, ef)

	start := a.entrypoint
	startNode := a.nodes[start]
	startDist := euclideanDistance(query, startNode.vector)

	minQ.Push(distancePair{id: start, distance: startDist})
	maxQ.Push(distancePair{id: start, distance: startDist})
	visited[start] = struct{}{}

	for minQ.Len() > 0 {
		current := minQ.Pop()

		worst := maxQ.Peek()
		if maxQ.Len() >= ef && current.distance > worst.distance {
			break
		}

		currNode := a.nodes[current.id]
		for nid := range currNode.neighbors {
			if _, ok := visited[nid]; ok {
				continue
			}
			visited[nid] = struct{}{}

			neighbor := a.nodes[nid]
			dist := euclideanDistance(query, neighbor.vector)
			if maxQ.Len() < ef || dist < maxQ.Peek().distance {
				dp := distancePair{id: nid, distance: dist}
				minQ.Push(dp)
				maxQ.Push(dp)
				if maxQ.Len() > ef {
					maxQ.Pop()
				}
			}
		}
	}

	out := make([]distancePair, maxQ.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = maxQ.Pop()
	}
	return out
}

func (a *AnnIndex) pruneNeighborsLocked(n *node) {
	if len(n.neighbors) <= a.m {
		return
	}
	pairs := make([]distancePair, 0, len(n.neighbors))
	for nid := range n.neighbors {
		neighbor := a.nodes[nid]
		pairs = append(pairs, distancePair{
			id:       nid,
			distance: euclideanDistance(n.vector, neighbor.vector),
		})
	}
	keep := nearestIDs(pairs, a.m)
	next := make(map[int]struct{}, len(keep))
	for _, id := range keep {
		next[id] = struct{}{}
	}
	n.neighbors = next
}

type distancePair struct {
	id       int
	distance float64
}

func nearestIDs(candidates []distancePair, k int) []int {
	if k > len(candidates) {
		k = len(candidates)
	}
	if k <= 0 {
		return []int{}
	}

	// partial selection sort for small-k use.
	for i := 0; i < k; i++ {
		best := i
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].distance < candidates[best].distance {
				best = j
			}
		}
		candidates[i], candidates[best] = candidates[best], candidates[i]
	}

	out := make([]int, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, candidates[i].id)
	}
	return out
}

func euclideanDistance(a, b []float64) float64 {
	if len(a) != len(b) {
		return math.Inf(1)
	}
	sum := 0.0
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return math.Sqrt(sum)
}

type minDistHeap []distancePair

func (h minDistHeap) Len() int { return len(h) }
func (h minDistHeap) Peek() distancePair {
	return h[0]
}
func (h *minDistHeap) Push(x distancePair) {
	*h = append(*h, x)
	upMin(*h, len(*h)-1)
}
func (h *minDistHeap) Pop() distancePair {
	old := *h
	n := len(old) - 1
	old[0], old[n] = old[n], old[0]
	downMin(old[:n], 0)
	item := old[n]
	*h = old[:n]
	return item
}

type maxDistHeap []distancePair

func (h maxDistHeap) Len() int { return len(h) }
func (h maxDistHeap) Peek() distancePair {
	return h[0]
}
func (h *maxDistHeap) Push(x distancePair) {
	*h = append(*h, x)
	upMax(*h, len(*h)-1)
}
func (h *maxDistHeap) Pop() distancePair {
	old := *h
	n := len(old) - 1
	old[0], old[n] = old[n], old[0]
	downMax(old[:n], 0)
	item := old[n]
	*h = old[:n]
	return item
}

func upMin(h minDistHeap, j int) {
	for {
		i := (j - 1) / 2
		if i == j || h[j].distance >= h[i].distance {
			break
		}
		h[i], h[j] = h[j], h[i]
		j = i
	}
}

func downMin(h minDistHeap, i int) {
	for {
		left := 2*i + 1
		if left >= len(h) {
			break
		}
		child := left
		right := left + 1
		if right < len(h) && h[right].distance < h[left].distance {
			child = right
		}
		if h[i].distance <= h[child].distance {
			break
		}
		h[i], h[child] = h[child], h[i]
		i = child
	}
}

func upMax(h maxDistHeap, j int) {
	for {
		i := (j - 1) / 2
		if i == j || h[j].distance <= h[i].distance {
			break
		}
		h[i], h[j] = h[j], h[i]
		j = i
	}
}

func downMax(h maxDistHeap, i int) {
	for {
		left := 2*i + 1
		if left >= len(h) {
			break
		}
		child := left
		right := left + 1
		if right < len(h) && h[right].distance > h[left].distance {
			child = right
		}
		if h[i].distance >= h[child].distance {
			break
		}
		h[i], h[child] = h[child], h[i]
		i = child
	}
}
