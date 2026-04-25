package ann

import (
	"errors"
	"math/rand"
	"sync"

	vectorutil "lumenvec/internal/vector"
)

var (
	ErrInvalidK         = errors.New("k must be greater than 0")
	ErrInvalidVectorDim = errors.New("query dimension mismatch")
)

type node struct {
	slot      int
	id        int
	vector    []float32
	neighbors []int
}

// AnnIndex is a graph-based ANN index inspired by HNSW/NSW principles.
type AnnIndex struct {
	nodes          []node
	idToSlot       map[int]int
	deleted        []bool
	deletedCount   int
	entrypoint     int
	hasEntrypoint  bool
	dim            int
	m              int
	efConstruction int
	efSearch       int
	rnd            *rand.Rand
	workspacePool  sync.Pool
	mu             sync.RWMutex
}

type Stats struct {
	Nodes   int
	Deleted int
}

type Result struct {
	ID       int
	Distance float64
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
	idx := &AnnIndex{
		idToSlot:       make(map[int]int),
		m:              opts.M,
		efConstruction: opts.EfConstruction,
		efSearch:       opts.EfSearch,
		rnd:            rand.New(rand.NewSource(opts.Seed)),
	}
	idx.workspacePool.New = func() any {
		return &searchWorkspace{}
	}
	return idx
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

	vecCopy := vectorutil.ToFloat32(vector)

	if slot, ok := a.idToSlot[id]; ok {
		a.nodes[slot].vector = vecCopy
		if a.deleted[slot] {
			a.deleted[slot] = false
			a.deletedCount--
		}
		return nil
	}

	slot := len(a.nodes)
	a.nodes = append(a.nodes, node{
		slot:      slot,
		id:        id,
		vector:    vecCopy,
		neighbors: make([]int, 0, a.m),
	})
	a.idToSlot[id] = slot
	a.deleted = append(a.deleted, false)

	if !a.hasEntrypoint {
		a.entrypoint = slot
		a.hasEntrypoint = true
		return nil
	}

	candidates := a.searchCandidates32Locked(vecCopy, a.efConstruction)
	linkSlots := nearestIDs(candidates, a.m)
	for _, neighborSlot := range linkSlots {
		if neighborSlot == slot {
			continue
		}
		a.nodes[slot].addNeighbor(neighborSlot)
		a.nodes[neighborSlot].addNeighbor(slot)
		a.pruneNeighborsLocked(&a.nodes[neighborSlot])
	}
	a.pruneNeighborsLocked(&a.nodes[slot])

	if a.rnd.Intn(100) < 5 {
		a.entrypoint = slot
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

	return a.searchIDs64Locked(query, k, a.efSearch), nil
}

func (a *AnnIndex) SearchWithDistances(query []float64, k int) ([]Result, error) {
	return a.SearchWithDistancesInto(query, k, nil)
}

func (a *AnnIndex) SearchWithDistancesInto(query []float64, k int, dst []Result) ([]Result, error) {
	if k <= 0 {
		return nil, ErrInvalidK
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(query) == 0 || len(query) != a.dim {
		return nil, ErrInvalidVectorDim
	}
	if len(a.nodes) == 0 {
		return dst[:0], nil
	}

	return a.searchResults64Locked(query, k, a.efSearch, dst), nil
}

func (a *AnnIndex) DeleteVector(id int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	slot, ok := a.idToSlot[id]
	if !ok || a.deleted[slot] {
		return
	}
	a.deleted[slot] = true
	a.deletedCount++
}

func (a *AnnIndex) Stats() Stats {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return Stats{
		Nodes:   len(a.nodes),
		Deleted: a.deletedCount,
	}
}

func (a *AnnIndex) searchCandidates32Locked(query []float32, ef int) []distancePair {
	if ef <= 0 {
		ef = a.efSearch
	}

	ws := a.workspacePool.Get().(*searchWorkspace)
	ws.reset(ef, len(a.nodes))
	defer a.workspacePool.Put(ws)

	candidates := a.searchCandidates32WithWorkspaceLocked(ws, ef, query)
	out := make([]distancePair, len(candidates))
	copy(out, candidates)
	return out
}

func (a *AnnIndex) searchIDs64Locked(query []float64, k int, ef int) []int {
	if ef <= 0 {
		ef = a.efSearch
	}

	ws := a.workspacePool.Get().(*searchWorkspace)
	ws.reset(ef, len(a.nodes))
	defer a.workspacePool.Put(ws)

	query32 := ws.query32From64(query)
	candidates := a.searchCandidates32WithWorkspaceLocked(ws, ef, query32)
	candidates = a.liveCandidates(candidates)
	return a.nearestExternalIDs(candidates, k)
}

func (a *AnnIndex) searchResults64Locked(query []float64, k int, ef int, dst []Result) []Result {
	if ef <= 0 {
		ef = a.efSearch
	}

	ws := a.workspacePool.Get().(*searchWorkspace)
	ws.reset(ef, len(a.nodes))
	defer a.workspacePool.Put(ws)

	query32 := ws.query32From64(query)
	candidates := a.searchCandidates32WithWorkspaceLocked(ws, ef, query32)
	candidates = a.liveCandidates(candidates)
	return a.nearestResults(candidates, k, dst)
}

func (a *AnnIndex) searchCandidates32WithWorkspaceLocked(ws *searchWorkspace, ef int, query []float32) []distancePair {
	minQ := ws.minQ
	maxQ := ws.maxQ

	start := a.entrypoint
	startNode := &a.nodes[start]
	startDist := squaredDistance32(query, startNode.vector)

	minQ.Push(distancePair{id: start, distance: startDist})
	maxQ.Push(distancePair{id: start, distance: startDist})
	ws.markVisited(start)

	for minQ.Len() > 0 {
		current := minQ.Pop()

		worst := maxQ.Peek()
		if maxQ.Len() >= ef && current.distance > worst.distance {
			break
		}

		currNode := &a.nodes[current.id]
		for _, nid := range currNode.neighbors {
			if ws.isVisited(nid) {
				continue
			}
			ws.markVisited(nid)

			neighbor := &a.nodes[nid]
			dist := squaredDistance32(query, neighbor.vector)
			dp := distancePair{id: nid, distance: dist}
			if maxQ.Len() < ef {
				minQ.Push(dp)
				maxQ.Push(dp)
				continue
			}
			if dist < maxQ.Peek().distance {
				minQ.Push(dp)
				maxQ.ReplaceTop(dp)
			}
		}
	}

	ws.minQ = minQ[:0]
	return ws.drainCandidates(maxQ)
}

func (w *searchWorkspace) drainCandidates(maxQ maxDistHeap) []distancePair {
	out := w.candidates[:0]
	if cap(out) < maxQ.Len() {
		out = make([]distancePair, 0, maxQ.Len())
	}
	out = out[:maxQ.Len()]
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = maxQ.Pop()
	}
	w.maxQ = maxQ[:0]
	w.candidates = out
	return out
}

type searchWorkspace struct {
	visitedMarks []uint32
	visitEpoch   uint32
	minQ         minDistHeap
	maxQ         maxDistHeap
	candidates   []distancePair
	query32      []float32
}

func (w *searchWorkspace) reset(ef int, nodes int) {
	if cap(w.visitedMarks) < nodes {
		w.visitedMarks = make([]uint32, nodes)
	} else {
		w.visitedMarks = w.visitedMarks[:nodes]
	}
	w.visitEpoch++
	if w.visitEpoch == 0 {
		clear(w.visitedMarks)
		w.visitEpoch = 1
	}
	if cap(w.minQ) < ef {
		w.minQ = make(minDistHeap, 0, ef)
	} else {
		w.minQ = w.minQ[:0]
	}
	if cap(w.maxQ) < ef {
		w.maxQ = make(maxDistHeap, 0, ef)
	} else {
		w.maxQ = w.maxQ[:0]
	}
	if cap(w.candidates) < ef {
		w.candidates = make([]distancePair, 0, ef)
	} else {
		w.candidates = w.candidates[:0]
	}
}

func (w *searchWorkspace) query32From64(query []float64) []float32 {
	if cap(w.query32) < len(query) {
		w.query32 = make([]float32, len(query))
	} else {
		w.query32 = w.query32[:len(query)]
	}
	for i, value := range query {
		w.query32[i] = float32(value)
	}
	return w.query32
}

func (w *searchWorkspace) isVisited(slot int) bool {
	return w.visitedMarks[slot] == w.visitEpoch
}

func (w *searchWorkspace) markVisited(slot int) {
	w.visitedMarks[slot] = w.visitEpoch
}

func (a *AnnIndex) liveCandidates(candidates []distancePair) []distancePair {
	if a.deletedCount == 0 {
		return candidates
	}
	out := candidates[:0]
	for _, candidate := range candidates {
		if candidate.id >= 0 && candidate.id < len(a.deleted) && a.deleted[candidate.id] {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func (a *AnnIndex) pruneNeighborsLocked(n *node) {
	if len(n.neighbors) <= a.m {
		return
	}
	pairs := make([]distancePair, 0, len(n.neighbors))
	for _, nid := range n.neighbors {
		neighbor := &a.nodes[nid]
		pairs = append(pairs, distancePair{
			id:       nid,
			distance: squaredDistance32(n.vector, neighbor.vector),
		})
	}
	keep := nearestIDs(pairs, a.m)
	n.neighbors = append(n.neighbors[:0], keep...)
}

func (n *node) addNeighbor(id int) {
	if id == n.slot {
		return
	}
	for _, existing := range n.neighbors {
		if existing == id {
			return
		}
	}
	n.neighbors = append(n.neighbors, id)
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

	selectNearest(candidates, k)

	out := make([]int, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, candidates[i].id)
	}
	return out
}

func (a *AnnIndex) nearestExternalIDs(candidates []distancePair, k int) []int {
	if k > len(candidates) {
		k = len(candidates)
	}
	if k <= 0 {
		return []int{}
	}

	selectNearest(candidates, k)

	out := make([]int, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, a.nodes[candidates[i].id].id)
	}
	return out
}

func (a *AnnIndex) nearestResults(candidates []distancePair, k int, dst []Result) []Result {
	if k > len(candidates) {
		k = len(candidates)
	}
	if k <= 0 {
		return dst[:0]
	}

	selectNearest(candidates, k)

	out := dst[:0]
	if cap(out) < k {
		out = make([]Result, 0, k)
	}
	for i := 0; i < k; i++ {
		out = append(out, Result{ID: a.nodes[candidates[i].id].id, Distance: candidates[i].distance})
	}
	return out
}

func selectNearest(candidates []distancePair, k int) {
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
}

func squaredDistance32(a, b []float32) float64 {
	sum := float64(0)
	i := 0
	for limit := len(a) - len(a)%4; i < limit; i += 4 {
		diff0 := float64(a[i] - b[i])
		diff1 := float64(a[i+1] - b[i+1])
		diff2 := float64(a[i+2] - b[i+2])
		diff3 := float64(a[i+3] - b[i+3])
		sum += diff0*diff0 + diff1*diff1 + diff2*diff2 + diff3*diff3
	}
	for ; i < len(a); i++ {
		diff := float64(a[i] - b[i])
		sum += diff * diff
	}
	return sum
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
func (h *maxDistHeap) ReplaceTop(x distancePair) {
	old := *h
	old[0] = x
	downMax(old, 0)
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
