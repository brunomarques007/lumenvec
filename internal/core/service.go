package core

import (
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"lumenvec/internal/index"
	"lumenvec/internal/index/ann"
	"lumenvec/internal/vector"
)

var (
	ErrInvalidID        = errors.New("id is required")
	ErrInvalidValues    = errors.New("values are required")
	ErrVectorDimTooHigh = errors.New("vector dimension exceeds configured max")
	ErrInvalidK         = errors.New("k must be greater than 0")
	ErrKTooHigh         = errors.New("k exceeds configured max")
	errSkipWALOp        = errors.New("skip wal op")
)

const (
	annDeleteRebuildMinDeleted = 1024
	annDeleteRebuildRatio      = 0.25
)

type SearchResult struct {
	ID       string  `json:"id"`
	Distance float64 `json:"distance"`
}

type BatchSearchQuery struct {
	ID     string
	Values []float64
	K      int
}

type BatchSearchResult struct {
	ID      string         `json:"id"`
	Results []SearchResult `json:"results"`
}

type ServiceOptions struct {
	MaxVectorDim      int
	MaxK              int
	SnapshotPath      string
	WALPath           string
	SnapshotEvery     int
	SearchMode        string
	ANNProfile        string
	ANNOptions        ann.Options
	ANNEvalSampleRate int
	VectorStore       string
	VectorPath        string
	Cache             CacheOptions
	StorageSecurity   StorageSecurityOptions
	SyncEvery         int
}

type VectorIndex interface {
	AddVector(vec index.Vector) error
	SearchVector(id string) (index.Vector, error)
	DeleteVector(id string) error
	ListVectors() []index.Vector
	RangeVectors(fn func(index.Vector) bool)
}

type vectorIndex32Ranger interface {
	RangeVectors32(fn func(id string, values []float32) bool)
}

type IDResolver interface {
	Assign(id string) int
	Lookup(internalID int) (string, bool)
	Remove(id string)
}

type ServiceDeps struct {
	Index       VectorIndex
	VectorStore VectorStore
	ANNIndex    *ann.AnnIndex
	IDResolver  IDResolver
	Persistence PersistenceBackend
}

type Service struct {
	index             VectorIndex
	annIndex          *ann.AnnIndex
	annMu             sync.RWMutex
	maxVectorDim      int
	maxK              int
	snapshotPath      string
	walPath           string
	snapshotEvery     int
	searchMode        string
	annProfile        string
	annOptions        ann.Options
	annEvalSampleRate int
	persistOps        int
	persistMu         sync.Mutex
	syncEvery         int
	vectorStore       VectorStore
	idResolver        IDResolver
	persistence       PersistenceBackend
	annResultPool     sync.Pool
	query32Pool       sync.Pool
	batchQuery32Pool  sync.Pool
	batchPreparedPool sync.Pool
	stats             serviceStats
}

type ServiceStats struct {
	SearchRequestsTotal    uint64 `json:"search_requests_total"`
	ExactSearchesTotal     uint64 `json:"exact_searches_total"`
	ANNSearchesTotal       uint64 `json:"ann_searches_total"`
	ANNSearchHitsTotal     uint64 `json:"ann_search_hits_total"`
	ANNSearchFallbacks     uint64 `json:"ann_search_fallbacks_total"`
	ANNSearchErrorsTotal   uint64 `json:"ann_search_errors_total"`
	ANNCandidatesReturned  uint64 `json:"ann_candidates_returned_total"`
	ANNEvalSamplesTotal    uint64 `json:"ann_eval_samples_total"`
	ANNEvalTop1Matches     uint64 `json:"ann_eval_top1_matches_total"`
	ANNEvalOverlapResults  uint64 `json:"ann_eval_overlap_results_total"`
	ANNEvalComparedResults uint64 `json:"ann_eval_compared_results_total"`
	ANNNodes               int    `json:"ann_nodes"`
	ANNDeleted             int    `json:"ann_deleted"`
	CacheHitsTotal         uint64 `json:"cache_hits_total"`
	CacheMissesTotal       uint64 `json:"cache_misses_total"`
	CacheEvictionsTotal    uint64 `json:"cache_evictions_total"`
	CacheItems             uint64 `json:"cache_items"`
	CacheBytes             uint64 `json:"cache_bytes"`
	DiskFileBytes          uint64 `json:"disk_file_bytes"`
	DiskRecords            uint64 `json:"disk_records"`
	DiskStaleRecords       uint64 `json:"disk_stale_records"`
	DiskCompactionsTotal   uint64 `json:"disk_compactions_total"`
	ANNProfile             string `json:"ann_profile"`
	ANNM                   int    `json:"ann_m"`
	ANNEfConstruction      int    `json:"ann_ef_construction"`
	ANNEfSearch            int    `json:"ann_ef_search"`
}

type serviceStats struct {
	searchRequestsTotal    atomic.Uint64
	exactSearchesTotal     atomic.Uint64
	annSearchesTotal       atomic.Uint64
	annSearchHitsTotal     atomic.Uint64
	annSearchFallbacks     atomic.Uint64
	annSearchErrorsTotal   atomic.Uint64
	annCandidatesReturned  atomic.Uint64
	annEvalSamplesTotal    atomic.Uint64
	annEvalTop1Matches     atomic.Uint64
	annEvalOverlapResults  atomic.Uint64
	annEvalComparedResults atomic.Uint64
}

type walOp struct {
	Op     string    `json:"op"`
	ID     string    `json:"id"`
	Values []float64 `json:"values,omitempty"`
}

type preparedBatchQuery struct {
	id     string
	vals   []float64
	vals32 []float32
	acc    topKAccumulator
}

type topKAccumulator struct {
	limit int
	items []SearchResult
}

const exactBatchDistanceWidth = 4

func NewService(opts ServiceOptions) *Service {
	return NewServiceWithDeps(opts, ServiceDeps{})
}

func NewServiceWithDeps(opts ServiceOptions, deps ServiceDeps) *Service {
	if deps.Index == nil {
		deps.Index = index.NewIndex()
	}
	if deps.VectorStore == nil {
		deps.VectorStore = newDefaultVectorStoreWithOptions(opts.VectorStore, opts.VectorPath, opts.StorageSecurity, opts.SyncEvery)
	}
	deps.VectorStore = newCachedVectorStore(deps.VectorStore, opts.Cache)
	if deps.ANNIndex == nil {
		deps.ANNIndex = ann.NewAnnIndexWithOptions(opts.ANNOptions)
	}
	if deps.IDResolver == nil {
		deps.IDResolver = newMemoryIDResolver()
	}
	if deps.Persistence == nil {
		deps.Persistence = newSnapshotWALBackendWithOptions(opts.SnapshotPath, opts.WALPath, opts.StorageSecurity, opts.SyncEvery)
	}

	svc := &Service{
		index:             deps.Index,
		annIndex:          deps.ANNIndex,
		maxVectorDim:      opts.MaxVectorDim,
		maxK:              opts.MaxK,
		snapshotPath:      opts.SnapshotPath,
		walPath:           opts.WALPath,
		snapshotEvery:     opts.SnapshotEvery,
		searchMode:        normalizeSearchMode(opts.SearchMode),
		annProfile:        normalizeANNProfile(opts.ANNProfile),
		annOptions:        opts.ANNOptions,
		annEvalSampleRate: clampPercent(opts.ANNEvalSampleRate),
		syncEvery:         normalizeSyncEvery(opts.SyncEvery),
		vectorStore:       deps.VectorStore,
		idResolver:        deps.IDResolver,
		persistence:       deps.Persistence,
	}
	svc.annResultPool.New = func() any {
		capHint := 64
		if svc.maxK > 0 && svc.maxK < capHint {
			capHint = svc.maxK
		}
		buf := make([]ann.Result, 0, capHint)
		return &buf
	}
	svc.query32Pool.New = func() any {
		buf := make([]float32, 0, maxIntOrOne(svc.maxVectorDim))
		return &buf
	}
	svc.batchQuery32Pool.New = func() any {
		buf := make([]float32, 0, maxIntOrOne(svc.maxVectorDim))
		return &buf
	}
	svc.batchPreparedPool.New = func() any {
		buf := make([]preparedBatchQuery, 0, 16)
		return &buf
	}

	_ = svc.restoreState()
	return svc
}

func (s *Service) AddVector(id string, values []float64) error {
	return s.AddVectors([]index.Vector{{ID: id, Values: values}})
}

func (s *Service) AddVectors(vectors []index.Vector) error {
	s.ensureRuntimeDeps()
	if len(vectors) == 0 {
		return ErrInvalidValues
	}

	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	addedIDs := make([]string, 0, len(vectors))
	for _, vec := range vectors {
		if strings.TrimSpace(vec.ID) == "" {
			s.rollbackAddedVectors(addedIDs)
			return ErrInvalidID
		}
		if len(vec.Values) == 0 {
			s.rollbackAddedVectors(addedIDs)
			return ErrInvalidValues
		}
		if len(vec.Values) > s.maxVectorDim {
			s.rollbackAddedVectors(addedIDs)
			return fmt.Errorf("%w (%d)", ErrVectorDimTooHigh, s.maxVectorDim)
		}
		// Index first so duplicate IDs return conflict without mutating the vector store.
		if err := s.index.AddVector(index.Vector{ID: vec.ID, Values: vec.Values}); err != nil {
			s.rollbackAddedVectors(addedIDs)
			return err
		}
		addedIDs = append(addedIDs, vec.ID)
	}

	if err := s.upsertVectors(vectors); err != nil {
		s.rollbackAddedVectors(addedIDs)
		return err
	}

	for _, vec := range vectors {
		internalID := s.idResolver.Assign(vec.ID)
		if err := s.addANNVector(internalID, vec.Values); err != nil {
			_ = s.vectorStore.DeleteVector(vec.ID)
			_ = s.index.DeleteVector(vec.ID)
			s.rollbackAddedVectors(addedIDs)
			return err
		}
	}

	ops := make([]walOp, 0, len(vectors))
	for _, vec := range vectors {
		ops = append(ops, walOp{Op: "upsert", ID: vec.ID, Values: vec.Values})
	}
	if err := s.appendWALBatch(ops); err != nil {
		s.rollbackAddedVectors(addedIDs)
		return err
	}
	return s.maybeSnapshot()
}

func (s *Service) GetVector(id string) (index.Vector, error) {
	s.ensureRuntimeDeps()
	if strings.TrimSpace(id) == "" {
		return index.Vector{}, ErrInvalidID
	}
	return s.vectorStore.GetVector(id)
}

func (s *Service) ListVectors() []index.Vector {
	s.ensureRuntimeDeps()
	vecs := s.vectorStore.ListVectors()
	sort.Slice(vecs, func(i, j int) bool { return vecs[i].ID < vecs[j].ID })
	return vecs
}

func (s *Service) DeleteVector(id string) error {
	s.ensureRuntimeDeps()
	if strings.TrimSpace(id) == "" {
		return ErrInvalidID
	}

	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	vec, err := s.vectorStore.GetVector(id)
	if err != nil {
		return err
	}
	if err := s.index.DeleteVector(id); err != nil {
		return err
	}
	if err := s.vectorStore.DeleteVector(id); err != nil {
		_ = s.index.AddVector(vec)
		return err
	}
	internalID := s.idResolver.Assign(id)
	s.deleteANNVector(internalID)
	s.idResolver.Remove(id)

	if err := s.appendWAL(walOp{Op: "delete", ID: id}); err != nil {
		_ = s.vectorStore.UpsertVector(vec)
		_ = s.index.AddVector(vec)
		s.idResolver.Assign(id)
		s.rebuildANNLocked()
		return err
	}
	s.maybeCompactANNAfterDeleteLocked()
	return s.maybeSnapshot()
}

func (s *Service) Search(values []float64, k int) ([]SearchResult, error) {
	s.ensureRuntimeDeps()
	if err := s.validateSearchRequest(values, k); err != nil {
		return nil, err
	}
	s.stats.searchRequestsTotal.Add(1)

	if s.searchMode == "ann" {
		results, ok := s.searchANN(values, k)
		if ok {
			s.maybeEvaluateANN(values, k, results)
			return results, nil
		}
		s.stats.annSearchFallbacks.Add(1)
	}
	s.stats.exactSearchesTotal.Add(1)
	return s.searchExact(values, k), nil
}

func (s *Service) SearchBatch(queries []BatchSearchQuery) ([]BatchSearchResult, error) {
	s.ensureRuntimeDeps()
	if len(queries) == 0 {
		return nil, ErrInvalidValues
	}

	preparedBuf := s.getPreparedBatchBuffer(len(queries))
	defer s.putPreparedBatchBuffer(preparedBuf)
	prepared := (*preparedBuf)[:0]
	exactMode := s.searchMode != "ann"
	totalQueryValues := 0
	if exactMode {
		for _, query := range queries {
			totalQueryValues += len(query.Values)
		}
	}
	var query32Buf *[]float32
	var query32Block []float32
	if totalQueryValues > 0 {
		query32Buf = s.getBatchQuery32Buffer(totalQueryValues)
		query32Block = (*query32Buf)[:totalQueryValues]
		defer s.putBatchQuery32Buffer(query32Buf)
	}
	query32Offset := 0
	for i, query := range queries {
		if err := s.validateSearchRequest(query.Values, query.K); err != nil {
			return nil, err
		}
		queryID := strings.TrimSpace(query.ID)
		if queryID == "" {
			queryID = fmt.Sprintf("query-%d", i)
		}
		var query32 []float32
		if exactMode {
			query32 = query32Block[query32Offset : query32Offset+len(query.Values)]
			fillQuery32Buffer(query32, query.Values)
			query32Offset += len(query.Values)
		}
		prepared = append(prepared, preparedBatchQuery{
			id:     queryID,
			vals:   query.Values,
			vals32: query32,
			acc:    newTopKAccumulator(query.K),
		})
	}

	if s.searchMode == "ann" {
		return s.searchBatchANN(prepared)
	}
	if len(prepared) >= exactBatchDistanceWidth*2 && runtime.GOMAXPROCS(0) > 1 {
		return s.searchBatchExactParallel(prepared), nil
	}
	return s.searchBatchExactSerial(prepared), nil
}

func (s *Service) searchBatchExactParallel(prepared []preparedBatchQuery) []BatchSearchResult {
	workers := min(runtime.GOMAXPROCS(0), len(prepared)/exactBatchDistanceWidth)
	if workers <= 1 {
		return s.searchBatchExactSerial(prepared)
	}
	chunkSize := (len(prepared) + workers - 1) / workers
	if rem := chunkSize % exactBatchDistanceWidth; rem != 0 {
		chunkSize += exactBatchDistanceWidth - rem
	}

	var wg sync.WaitGroup
	for start := 0; start < len(prepared); start += chunkSize {
		end := min(start+chunkSize, len(prepared))
		wg.Add(1)
		go func(batch []preparedBatchQuery) {
			defer wg.Done()
			s.scanExactBatch(batch)
		}(prepared[start:end])
	}
	wg.Wait()
	return batchResultsFromPrepared(prepared)
}

func (s *Service) searchBatchExactSerial(prepared []preparedBatchQuery) []BatchSearchResult {
	s.scanExactBatch(prepared)
	return batchResultsFromPrepared(prepared)
}

func (s *Service) scanExactBatch(prepared []preparedBatchQuery) {
	s.rangeExactVectors(func(id string, values []float32) bool {
		for i := 0; i+exactBatchDistanceWidth <= len(prepared); i += exactBatchDistanceWidth {
			q0 := prepared[i].vals32
			q1 := prepared[i+1].vals32
			q2 := prepared[i+2].vals32
			q3 := prepared[i+3].vals32
			if len(q0) == len(values) && len(q1) == len(values) && len(q2) == len(values) && len(q3) == len(values) {
				d0, d1, d2, d3 := vector.SquaredEuclideanDistance32x4SameLen(q0, q1, q2, q3, values)
				prepared[i].acc.Add(SearchResult{ID: id, Distance: d0})
				prepared[i+1].acc.Add(SearchResult{ID: id, Distance: d1})
				prepared[i+2].acc.Add(SearchResult{ID: id, Distance: d2})
				prepared[i+3].acc.Add(SearchResult{ID: id, Distance: d3})
				continue
			}
			for j := 0; j < exactBatchDistanceWidth; j++ {
				dist := vector.SquaredEuclideanDistance32(prepared[i+j].vals32, values)
				if dist != dist {
					continue
				}
				prepared[i+j].acc.Add(SearchResult{ID: id, Distance: dist})
			}
		}
		for i := len(prepared) - len(prepared)%exactBatchDistanceWidth; i < len(prepared); i++ {
			dist := vector.SquaredEuclideanDistance32(prepared[i].vals32, values)
			if dist != dist {
				continue
			}
			prepared[i].acc.Add(SearchResult{ID: id, Distance: dist})
		}
		return true
	})
}

func batchResultsFromPrepared(prepared []preparedBatchQuery) []BatchSearchResult {
	results := make([]BatchSearchResult, len(prepared))
	for i, query := range prepared {
		results[i] = BatchSearchResult{
			ID:      query.id,
			Results: query.acc.Results(),
		}
	}
	return results
}

func (s *Service) searchBatchANN(prepared []preparedBatchQuery) ([]BatchSearchResult, error) {
	results := make([]BatchSearchResult, len(prepared))
	workers := min(len(prepared), runtime.GOMAXPROCS(0))
	if workers <= 1 {
		for i, query := range prepared {
			hits, err := s.Search(query.vals, query.acc.limit)
			if err != nil {
				return nil, err
			}
			results[i] = BatchSearchResult{ID: query.id, Results: hits}
		}
		return results, nil
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				query := prepared[i]
				hits, err := s.Search(query.vals, query.acc.limit)
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					continue
				}
				results[i] = BatchSearchResult{ID: query.id, Results: hits}
			}
		}()
	}
	for i := range prepared {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func (s *Service) validateSearchRequest(values []float64, k int) error {
	if len(values) == 0 {
		return ErrInvalidValues
	}
	if k <= 0 {
		return ErrInvalidK
	}
	if k > s.maxK {
		return fmt.Errorf("%w (%d)", ErrKTooHigh, s.maxK)
	}
	if len(values) > s.maxVectorDim {
		return fmt.Errorf("%w (%d)", ErrVectorDimTooHigh, s.maxVectorDim)
	}
	return nil
}

func (s *Service) searchExact(values []float64, k int) []SearchResult {
	query32 := s.getQuery32Buffer(values)
	defer s.putQuery32Buffer(query32)

	acc := newTopKAccumulator(k)
	s.rangeExactVectors(func(id string, vecValues []float32) bool {
		dist := vector.SquaredEuclideanDistance32SameLen(*query32, vecValues)
		if dist == dist {
			acc.Add(SearchResult{ID: id, Distance: dist})
		}
		return true
	})
	return acc.Results()
}

func (s *Service) rangeExactVectors(fn func(id string, values []float32) bool) {
	if ranger, ok := s.index.(vectorIndex32Ranger); ok {
		ranger.RangeVectors32(fn)
		return
	}
	s.index.RangeVectors(func(vec index.Vector) bool {
		return fn(vec.ID, vector.ToFloat32(vec.Values))
	})
}

func (s *Service) searchANN(values []float64, k int) ([]SearchResult, bool) {
	s.stats.annSearchesTotal.Add(1)
	annIndex := s.currentANNIndex()
	candidateBuf := s.getANNResultBuffer()
	candidates, err := annIndex.SearchWithDistancesInto(values, k, *candidateBuf)
	defer s.putANNResultBuffer(candidateBuf, candidates)
	if err != nil {
		s.stats.annSearchErrorsTotal.Add(1)
		return nil, false
	}
	s.stats.annCandidatesReturned.Add(uint64(len(candidates)))

	results := make([]SearchResult, 0, len(candidates))
	for _, candidate := range candidates {
		id, ok := s.idResolver.Lookup(candidate.ID)
		if !ok {
			continue
		}
		if candidate.Distance != candidate.Distance {
			continue
		}
		results = append(results, SearchResult{ID: id, Distance: candidate.Distance})
	}
	if len(results) == 0 {
		return nil, false
	}
	s.stats.annSearchHitsTotal.Add(1)
	for i := range results {
		results[i].Distance = sqrtDistance(results[i].Distance)
	}
	return results, true
}

func (s *Service) getANNResultBuffer() *[]ann.Result {
	got := s.annResultPool.Get()
	if got == nil {
		buf := make([]ann.Result, 0, minInt(maxIntOrOne(s.maxK), 64))
		return &buf
	}
	buf := got.(*[]ann.Result)
	*buf = (*buf)[:0]
	return buf
}

func (s *Service) putANNResultBuffer(buf *[]ann.Result, results []ann.Result) {
	results = results[:0]
	*buf = results
	s.annResultPool.Put(buf)
}

func (s *Service) getQuery32Buffer(values []float64) *[]float32 {
	got := s.query32Pool.Get()
	if got == nil {
		buf := make([]float32, len(values))
		fillQuery32Buffer(buf, values)
		return &buf
	}
	buf := got.(*[]float32)
	if cap(*buf) < len(values) {
		*buf = make([]float32, len(values))
	} else {
		*buf = (*buf)[:len(values)]
	}
	fillQuery32Buffer(*buf, values)
	return buf
}

func (s *Service) putQuery32Buffer(buf *[]float32) {
	*buf = (*buf)[:0]
	s.query32Pool.Put(buf)
}

func (s *Service) getBatchQuery32Buffer(size int) *[]float32 {
	got := s.batchQuery32Pool.Get()
	if got == nil {
		buf := make([]float32, size)
		return &buf
	}
	buf := got.(*[]float32)
	if cap(*buf) < size {
		*buf = make([]float32, size)
	} else {
		*buf = (*buf)[:size]
	}
	return buf
}

func (s *Service) putBatchQuery32Buffer(buf *[]float32) {
	*buf = (*buf)[:0]
	s.batchQuery32Pool.Put(buf)
}

func (s *Service) getPreparedBatchBuffer(size int) *[]preparedBatchQuery {
	got := s.batchPreparedPool.Get()
	if got == nil {
		buf := make([]preparedBatchQuery, 0, size)
		return &buf
	}
	buf := got.(*[]preparedBatchQuery)
	if cap(*buf) < size {
		*buf = make([]preparedBatchQuery, 0, size)
	} else {
		*buf = (*buf)[:0]
	}
	return buf
}

func (s *Service) putPreparedBatchBuffer(buf *[]preparedBatchQuery) {
	for i := range *buf {
		(*buf)[i] = preparedBatchQuery{}
	}
	*buf = (*buf)[:0]
	s.batchPreparedPool.Put(buf)
}

func fillQuery32Buffer(dst []float32, values []float64) {
	for i, value := range values {
		dst[i] = float32(value)
	}
}

func newTopKAccumulator(limit int) topKAccumulator {
	if limit <= 0 {
		return topKAccumulator{}
	}
	return topKAccumulator{
		limit: limit,
		items: make([]SearchResult, 0, limit),
	}
}

func (a *topKAccumulator) Add(item SearchResult) {
	if a.limit <= 0 {
		return
	}
	if len(a.items) < a.limit {
		a.push(item)
		return
	}
	if item.Distance >= a.items[0].Distance {
		return
	}
	a.replaceTop(item)
}

func (a *topKAccumulator) Results() []SearchResult {
	if len(a.items) == 0 {
		return nil
	}
	sort.Slice(a.items, func(i, j int) bool {
		return a.items[i].Distance < a.items[j].Distance
	})
	for i := range a.items {
		a.items[i].Distance = sqrtDistance(a.items[i].Distance)
	}
	return a.items
}

func (a *topKAccumulator) push(item SearchResult) {
	a.items = append(a.items, item)
	upSearchResultMax(a.items, len(a.items)-1)
}

func (a *topKAccumulator) replaceTop(item SearchResult) {
	a.items[0] = item
	downSearchResultMax(a.items, 0)
}

func upSearchResultMax(h []SearchResult, j int) {
	for {
		i := (j - 1) / 2
		if i == j || h[j].Distance <= h[i].Distance {
			break
		}
		h[i], h[j] = h[j], h[i]
		j = i
	}
}

func downSearchResultMax(h []SearchResult, i int) {
	for {
		left := 2*i + 1
		if left >= len(h) {
			break
		}
		child := left
		right := left + 1
		if right < len(h) && h[right].Distance > h[left].Distance {
			child = right
		}
		if h[i].Distance >= h[child].Distance {
			break
		}
		h[i], h[child] = h[child], h[i]
		i = child
	}
}

func normalizeSearchMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "ann" {
		return "exact"
	}
	return mode
}

func (s *Service) rollbackAddedVectors(ids []string) {
	for _, id := range ids {
		_ = s.vectorStore.DeleteVector(id)
		_ = s.index.DeleteVector(id)
	}
	s.rebuildANNLocked()
}

func (s *Service) upsertVectors(vectors []index.Vector) error {
	if len(vectors) == 0 {
		return nil
	}
	if writer, ok := s.vectorStore.(batchVectorStore); ok {
		return writer.UpsertVectors(vectors)
	}
	for _, vec := range vectors {
		if err := s.vectorStore.UpsertVector(index.Vector{ID: vec.ID, Values: vec.Values}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) restoreState() error {
	s.ensureRuntimeDeps()
	if s.usesPersistentVectorStore() {
		if err := s.loadVectorStoreState(); err != nil {
			return err
		}
		s.persistOps = 0
		return nil
	}
	if err := s.loadSnapshot(); err != nil {
		return err
	}
	if err := s.replayWAL(); err != nil {
		return err
	}
	if err := s.saveSnapshot(); err != nil {
		return err
	}
	if err := s.truncateWAL(); err != nil {
		return err
	}
	s.persistOps = 0
	return nil
}

func (s *Service) saveSnapshot() error {
	s.ensureRuntimeDeps()
	if s.usesPersistentVectorStore() {
		return nil
	}
	return s.persistenceBackend().SaveSnapshot(s.vectorStore.ListVectors())
}

func (s *Service) appendWAL(op walOp) error {
	return s.appendWALBatch([]walOp{op})
}

func (s *Service) appendWALBatch(ops []walOp) error {
	s.ensureRuntimeDeps()
	if len(ops) == 0 {
		return nil
	}
	if s.usesPersistentVectorStore() {
		return nil
	}
	backend := s.persistenceBackend()
	if batcher, ok := backend.(interface{ AppendWALBatch([]walOp) error }); ok {
		return batcher.AppendWALBatch(ops)
	}
	for _, op := range ops {
		if err := backend.AppendWAL(op); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) replayWAL() error {
	s.ensureRuntimeDeps()
	err := s.persistenceBackend().ReplayWAL(func(op walOp) error {
		switch op.Op {
		case "upsert":
			if op.ID == "" || len(op.Values) == 0 || len(op.Values) > s.maxVectorDim {
				return errSkipWALOp
			}
			vec := index.Vector{ID: op.ID, Values: op.Values}
			if err := s.vectorStore.UpsertVector(vec); err != nil {
				return err
			}
			if err := s.index.AddVector(vec); err != nil {
				if errors.Is(err, index.ErrVectorExists) {
					_ = s.vectorStore.UpsertVector(vec)
					_ = s.index.DeleteVector(op.ID)
					_ = s.index.AddVector(vec)
					return nil
				}
				return err
			}
		case "delete":
			if op.ID == "" {
				return errSkipWALOp
			}
			_ = s.vectorStore.DeleteVector(op.ID)
			_ = s.index.DeleteVector(op.ID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.rebuildANNLocked()
	return nil
}

func (s *Service) truncateWAL() error {
	s.ensureRuntimeDeps()
	if s.usesPersistentVectorStore() {
		return nil
	}
	return s.persistenceBackend().TruncateWAL()
}

func (s *Service) maybeSnapshot() error {
	s.ensureRuntimeDeps()
	if s.usesPersistentVectorStore() {
		s.persistOps = 0
		return nil
	}
	s.persistOps++
	if s.persistOps < s.snapshotEvery {
		return nil
	}
	if err := s.syncPersistence(); err != nil {
		return err
	}
	if err := s.saveSnapshot(); err != nil {
		return err
	}
	if err := s.truncateWAL(); err != nil {
		return err
	}
	s.persistOps = 0
	return nil
}

func (s *Service) loadSnapshot() error {
	s.ensureRuntimeDeps()
	payload, err := s.persistenceBackend().LoadSnapshot()
	if err != nil {
		return err
	}
	for id, values := range payload {
		if id == "" || len(values) == 0 || len(values) > s.maxVectorDim {
			continue
		}
		if err := s.vectorStore.UpsertVector(index.Vector{ID: id, Values: values}); err != nil {
			return err
		}
		if err := s.index.AddVector(index.Vector{ID: id, Values: values}); err != nil && !errors.Is(err, index.ErrVectorExists) {
			return err
		}
		internalID := s.idResolver.Assign(id)
		if err := s.addANNVector(internalID, values); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) rebuildANNLocked() {
	s.ensureRuntimeDeps()
	nextIndex := ann.NewAnnIndexWithOptions(s.annOptions)
	for _, vec := range s.index.ListVectors() {
		internalID := s.idResolver.Assign(vec.ID)
		_ = nextIndex.AddVector(internalID, vec.Values)
	}
	s.annMu.Lock()
	s.annIndex = nextIndex
	s.annMu.Unlock()
}

func (s *Service) loadVectorStoreState() error {
	s.ensureRuntimeDeps()
	for _, vec := range s.vectorStore.ListVectors() {
		if vec.ID == "" || len(vec.Values) == 0 || len(vec.Values) > s.maxVectorDim {
			continue
		}
		if err := s.index.AddVector(vec); err != nil && !errors.Is(err, index.ErrVectorExists) {
			return err
		}
		internalID := s.idResolver.Assign(vec.ID)
		if err := s.addANNVector(internalID, vec.Values); err != nil {
			continue
		}
	}
	return nil
}

func (s *Service) Stats() ServiceStats {
	annStats := s.currentANNIndex().Stats()
	stats := ServiceStats{
		SearchRequestsTotal:    s.stats.searchRequestsTotal.Load(),
		ExactSearchesTotal:     s.stats.exactSearchesTotal.Load(),
		ANNSearchesTotal:       s.stats.annSearchesTotal.Load(),
		ANNSearchHitsTotal:     s.stats.annSearchHitsTotal.Load(),
		ANNSearchFallbacks:     s.stats.annSearchFallbacks.Load(),
		ANNSearchErrorsTotal:   s.stats.annSearchErrorsTotal.Load(),
		ANNCandidatesReturned:  s.stats.annCandidatesReturned.Load(),
		ANNEvalSamplesTotal:    s.stats.annEvalSamplesTotal.Load(),
		ANNEvalTop1Matches:     s.stats.annEvalTop1Matches.Load(),
		ANNEvalOverlapResults:  s.stats.annEvalOverlapResults.Load(),
		ANNEvalComparedResults: s.stats.annEvalComparedResults.Load(),
		ANNNodes:               annStats.Nodes,
		ANNDeleted:             annStats.Deleted,
		ANNProfile:             normalizeANNProfile(s.annProfile),
		ANNM:                   s.annOptions.M,
		ANNEfConstruction:      s.annOptions.EfConstruction,
		ANNEfSearch:            s.annOptions.EfSearch,
	}
	if cacheStatsReader, ok := s.vectorStore.(interface{ Stats() CacheStats }); ok {
		cacheStats := cacheStatsReader.Stats()
		stats.CacheHitsTotal = cacheStats.Hits
		stats.CacheMissesTotal = cacheStats.Misses
		stats.CacheEvictionsTotal = cacheStats.Evictions
		stats.CacheItems = cacheStats.Items
		stats.CacheBytes = cacheStats.Bytes
	}
	if diskStatsReader, ok := s.vectorStore.(diskStatsReader); ok {
		diskStats := diskStatsReader.DiskStats()
		stats.DiskFileBytes = diskStats.FileBytes
		stats.DiskRecords = diskStats.Records
		stats.DiskStaleRecords = diskStats.StaleRecords
		stats.DiskCompactionsTotal = diskStats.Compactions
	}
	return stats
}

func (s *Service) Close() error {
	syncErr := s.syncPersistence()
	if closer, ok := s.vectorStore.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			return err
		}
	}
	return syncErr
}

func (s *Service) syncPersistence() error {
	if syncer, ok := s.persistence.(interface{ Sync() error }); ok {
		return syncer.Sync()
	}
	return nil
}

func (s *Service) ensureRuntimeDeps() {
	if s.index == nil {
		s.index = index.NewIndex()
	}
	if s.vectorStore == nil {
		s.vectorStore = newMemoryVectorStore()
	}
	_ = s.currentANNIndex()
	if s.idResolver == nil {
		s.idResolver = newMemoryIDResolver()
	}
	if s.persistence == nil {
		s.persistence = newSnapshotWALBackend(s.snapshotPath, s.walPath)
	}
}

func (s *Service) currentANNIndex() *ann.AnnIndex {
	s.annMu.RLock()
	idx := s.annIndex
	s.annMu.RUnlock()
	if idx != nil {
		return idx
	}

	s.annMu.Lock()
	defer s.annMu.Unlock()
	if s.annIndex == nil {
		s.annIndex = ann.NewAnnIndexWithOptions(s.annOptions)
	}
	return s.annIndex
}

func (s *Service) addANNVector(internalID int, values []float64) error {
	return s.currentANNIndex().AddVector(internalID, values)
}

func (s *Service) deleteANNVector(internalID int) {
	s.currentANNIndex().DeleteVector(internalID)
}

func (s *Service) maybeCompactANNAfterDeleteLocked() {
	stats := s.currentANNIndex().Stats()
	if stats.Deleted < annDeleteRebuildMinDeleted {
		return
	}
	if stats.Nodes == 0 || float64(stats.Deleted)/float64(stats.Nodes) < annDeleteRebuildRatio {
		return
	}
	s.rebuildANNLocked()
}

func (s *Service) persistenceBackend() PersistenceBackend {
	if backend, ok := s.persistence.(*snapshotWALBackend); ok {
		if backend.snapshotPath != s.snapshotPath || backend.walPath != s.walPath {
			s.persistence = newSnapshotWALBackendWithOptions(s.snapshotPath, s.walPath, backend.security, s.syncEvery)
		}
	}
	if s.persistence == nil {
		s.persistence = newSnapshotWALBackendWithOptions(s.snapshotPath, s.walPath, DefaultStorageSecurityOptions(), s.syncEvery)
	}
	return s.persistence
}

func (s *Service) usesPersistentVectorStore() bool {
	persistent, ok := s.vectorStore.(persistentVectorStore)
	return ok && persistent.IsPersistent()
}

func newDefaultVectorStore(mode, path string, security ...StorageSecurityOptions) VectorStore {
	storeSecurity := DefaultStorageSecurityOptions()
	if len(security) > 0 {
		storeSecurity = normalizeStorageSecurityOptions(security[0])
	}
	return newDefaultVectorStoreWithOptions(mode, path, storeSecurity, 1)
}

func newDefaultVectorStoreWithOptions(mode, path string, security StorageSecurityOptions, syncEvery int) VectorStore {
	storeSecurity := normalizeStorageSecurityOptions(security)
	syncEvery = normalizeSyncEvery(syncEvery)
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "disk", "file":
		if strings.TrimSpace(path) == "" {
			path = "./data/vectors"
		}
		return newFileVectorStoreWithOptions(path, storeSecurity, syncEvery)
	default:
		return newMemoryVectorStore()
	}
}

func normalizeSyncEvery(syncEvery int) int {
	if syncEvery <= 0 {
		return 1
	}
	return syncEvery
}

func storageSecurityOptionsFromStrings(strict bool, dirMode, fileMode string) StorageSecurityOptions {
	opts := DefaultStorageSecurityOptions()
	if strict {
		opts = StrictStorageSecurityOptions()
	}
	opts.DirMode = ParseFileMode(dirMode, opts.DirMode)
	opts.FileMode = ParseFileMode(fileMode, opts.FileMode)
	opts.StrictFilePermissions = strict
	return normalizeStorageSecurityOptions(opts)
}

func storagePathMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}

func normalizeANNProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "fast":
		return "fast"
	case "quality":
		return "quality"
	case "custom":
		return "custom"
	default:
		return "balanced"
	}
}

func clampPercent(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (s *Service) shouldEvaluateANN() bool {
	if s.annEvalSampleRate <= 0 {
		return false
	}
	n := s.stats.annSearchesTotal.Load()
	return int(n%100) < s.annEvalSampleRate
}

func (s *Service) maybeEvaluateANN(values []float64, k int, annResults []SearchResult) {
	if !s.shouldEvaluateANN() {
		return
	}
	exactResults := s.searchExact(values, k)
	s.stats.annEvalSamplesTotal.Add(1)
	if len(annResults) > 0 && len(exactResults) > 0 && annResults[0].ID == exactResults[0].ID {
		s.stats.annEvalTop1Matches.Add(1)
	}

	exactIDs := make(map[string]struct{}, len(exactResults))
	for _, result := range exactResults {
		exactIDs[result.ID] = struct{}{}
	}
	compared := minInt(len(annResults), len(exactResults))
	overlap := 0
	for _, result := range annResults {
		if _, ok := exactIDs[result.ID]; ok {
			overlap++
		}
	}
	s.stats.annEvalOverlapResults.Add(uint64(overlap))
	s.stats.annEvalComparedResults.Add(uint64(compared))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxIntOrOne(value int) int {
	if value < 1 {
		return 1
	}
	return value
}

func sqrtDistance(distance float64) float64 {
	return math.Sqrt(distance)
}
