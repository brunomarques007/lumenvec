package core

import (
	"errors"
	"fmt"
	"os"
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
}

type VectorIndex interface {
	AddVector(vec index.Vector) error
	SearchVector(id string) (index.Vector, error)
	DeleteVector(id string) error
	ListVectors() []index.Vector
	RangeVectors(fn func(index.Vector) bool)
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
	vectorStore       VectorStore
	idResolver        IDResolver
	persistence       PersistenceBackend
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
	id   string
	vals []float64
	acc  topKAccumulator
}

type topKAccumulator struct {
	limit      int
	items      []SearchResult
	worstIndex int
}

func NewService(opts ServiceOptions) *Service {
	return NewServiceWithDeps(opts, ServiceDeps{})
}

func NewServiceWithDeps(opts ServiceOptions, deps ServiceDeps) *Service {
	if deps.Index == nil {
		deps.Index = index.NewIndex()
	}
	if deps.VectorStore == nil {
		deps.VectorStore = newDefaultVectorStore(opts.VectorStore, opts.VectorPath, opts.StorageSecurity)
	}
	deps.VectorStore = newCachedVectorStore(deps.VectorStore, opts.Cache)
	if deps.ANNIndex == nil {
		deps.ANNIndex = ann.NewAnnIndexWithOptions(opts.ANNOptions)
	}
	if deps.IDResolver == nil {
		deps.IDResolver = newMemoryIDResolver()
	}
	if deps.Persistence == nil {
		deps.Persistence = newSnapshotWALBackendWithSecurity(opts.SnapshotPath, opts.WALPath, opts.StorageSecurity)
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
		vectorStore:       deps.VectorStore,
		idResolver:        deps.IDResolver,
		persistence:       deps.Persistence,
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
		if err := s.vectorStore.UpsertVector(index.Vector{ID: vec.ID, Values: vec.Values}); err != nil {
			_ = s.index.DeleteVector(vec.ID)
			s.rollbackAddedVectors(addedIDs)
			return err
		}
		internalID := s.idResolver.Assign(vec.ID)
		if err := s.addANNVector(internalID, vec.Values); err != nil {
			_ = s.vectorStore.DeleteVector(vec.ID)
			_ = s.index.DeleteVector(vec.ID)
			s.rollbackAddedVectors(addedIDs)
			return err
		}
		addedIDs = append(addedIDs, vec.ID)
	}

	for _, vec := range vectors {
		if err := s.appendWAL(walOp{Op: "upsert", ID: vec.ID, Values: vec.Values}); err != nil {
			s.rollbackAddedVectors(addedIDs)
			return err
		}
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
	s.idResolver.Remove(id)
	s.rebuildANNLocked()

	if err := s.appendWAL(walOp{Op: "delete", ID: id}); err != nil {
		_ = s.vectorStore.UpsertVector(vec)
		_ = s.index.AddVector(vec)
		s.rebuildANNLocked()
		return err
	}
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

	prepared := make([]preparedBatchQuery, 0, len(queries))
	for i, query := range queries {
		if err := s.validateSearchRequest(query.Values, query.K); err != nil {
			return nil, err
		}
		queryID := strings.TrimSpace(query.ID)
		if queryID == "" {
			queryID = fmt.Sprintf("query-%d", i)
		}
		prepared = append(prepared, preparedBatchQuery{
			id:   queryID,
			vals: query.Values,
			acc:  newTopKAccumulator(query.K),
		})
	}

	if s.searchMode == "ann" {
		results := make([]BatchSearchResult, 0, len(prepared))
		for _, query := range prepared {
			hits, err := s.Search(query.vals, query.acc.limit)
			if err != nil {
				return nil, err
			}
			results = append(results, BatchSearchResult{ID: query.id, Results: hits})
		}
		return results, nil
	}

	s.index.RangeVectors(func(vec index.Vector) bool {
		for i := range prepared {
			dist := vector.EuclideanDistance(prepared[i].vals, vec.Values)
			if dist != dist {
				continue
			}
			prepared[i].acc.Add(SearchResult{ID: vec.ID, Distance: dist})
		}
		return true
	})

	results := make([]BatchSearchResult, 0, len(prepared))
	for _, query := range prepared {
		results = append(results, BatchSearchResult{
			ID:      query.id,
			Results: query.acc.Results(),
		})
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
	acc := newTopKAccumulator(k)
	s.index.RangeVectors(func(vec index.Vector) bool {
		dist := vector.EuclideanDistance(values, vec.Values)
		if dist == dist {
			acc.Add(SearchResult{ID: vec.ID, Distance: dist})
		}
		return true
	})
	return acc.Results()
}

func (s *Service) searchANN(values []float64, k int) ([]SearchResult, bool) {
	s.stats.annSearchesTotal.Add(1)
	annIndex := s.currentANNIndex()
	ids, err := annIndex.Search(values, k)
	if err != nil {
		s.stats.annSearchErrorsTotal.Add(1)
		return nil, false
	}
	s.stats.annCandidatesReturned.Add(uint64(len(ids)))

	results := make([]SearchResult, 0, len(ids))
	for _, internalID := range ids {
		id, ok := s.idResolver.Lookup(internalID)
		if !ok {
			continue
		}
		vec, err := s.getVectorForRead(id)
		if err != nil {
			continue
		}
		dist := vector.EuclideanDistance(values, vec.Values)
		if dist != dist {
			continue
		}
		results = append(results, SearchResult{ID: vec.ID, Distance: dist})
	}
	if len(results) == 0 {
		return nil, false
	}
	s.stats.annSearchHitsTotal.Add(1)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})
	if k < len(results) {
		results = results[:k]
	}
	return results, true
}

func (s *Service) getVectorForRead(id string) (index.Vector, error) {
	if reader, ok := s.vectorStore.(readOnlyVectorReader); ok {
		return reader.GetVectorReadOnly(id)
	}
	return s.vectorStore.GetVector(id)
}

func newTopKAccumulator(limit int) topKAccumulator {
	return topKAccumulator{limit: limit, worstIndex: -1}
}

func (a *topKAccumulator) Add(item SearchResult) {
	if a.limit <= 0 {
		return
	}
	if len(a.items) < a.limit {
		a.items = append(a.items, item)
		a.recomputeWorst()
		return
	}
	if item.Distance >= a.items[a.worstIndex].Distance {
		return
	}
	a.items[a.worstIndex] = item
	a.recomputeWorst()
}

func (a *topKAccumulator) Results() []SearchResult {
	out := make([]SearchResult, len(a.items))
	copy(out, a.items)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Distance < out[j].Distance
	})
	return out
}

func (a *topKAccumulator) recomputeWorst() {
	if len(a.items) == 0 {
		a.worstIndex = -1
		return
	}
	worst := 0
	for i := 1; i < len(a.items); i++ {
		if a.items[i].Distance > a.items[worst].Distance {
			worst = i
		}
	}
	a.worstIndex = worst
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
	s.ensureRuntimeDeps()
	if s.usesPersistentVectorStore() {
		return nil
	}
	return s.persistenceBackend().AppendWAL(op)
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
	if closer, ok := s.vectorStore.(interface{ Close() error }); ok {
		return closer.Close()
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

func (s *Service) persistenceBackend() PersistenceBackend {
	if backend, ok := s.persistence.(*snapshotWALBackend); ok {
		if backend.snapshotPath != s.snapshotPath || backend.walPath != s.walPath {
			s.persistence = newSnapshotWALBackendWithSecurity(s.snapshotPath, s.walPath, backend.security)
		}
	}
	if s.persistence == nil {
		return newSnapshotWALBackend(s.snapshotPath, s.walPath)
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
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "disk", "file":
		if strings.TrimSpace(path) == "" {
			path = "./data/vectors"
		}
		return newFileVectorStoreWithSecurity(path, storeSecurity)
	default:
		return newMemoryVectorStore()
	}
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
