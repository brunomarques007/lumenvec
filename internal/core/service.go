package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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
	MaxVectorDim  int
	MaxK          int
	SnapshotPath  string
	WALPath       string
	SnapshotEvery int
	SearchMode    string
}

type Service struct {
	index         *index.Index
	annIndex      *ann.AnnIndex
	maxVectorDim  int
	maxK          int
	snapshotPath  string
	walPath       string
	snapshotEvery int
	searchMode    string
	persistOps    int
	persistMu     sync.Mutex
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
	svc := &Service{
		index:         index.NewIndex(),
		annIndex:      ann.NewAnnIndex(),
		maxVectorDim:  opts.MaxVectorDim,
		maxK:          opts.MaxK,
		snapshotPath:  opts.SnapshotPath,
		walPath:       opts.WALPath,
		snapshotEvery: opts.SnapshotEvery,
		searchMode:    normalizeSearchMode(opts.SearchMode),
	}

	_ = svc.restoreState()
	return svc
}

func (s *Service) AddVector(id string, values []float64) error {
	return s.AddVectors([]index.Vector{{ID: id, Values: values}})
}

func (s *Service) AddVectors(vectors []index.Vector) error {
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

		if err := s.index.AddVector(index.Vector{ID: vec.ID, Values: vec.Values}); err != nil {
			s.rollbackAddedVectors(addedIDs)
			return err
		}
		_ = s.annIndex.AddVector(hashID(vec.ID), vec.Values)
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
	if strings.TrimSpace(id) == "" {
		return index.Vector{}, ErrInvalidID
	}
	return s.index.SearchVector(id)
}

func (s *Service) DeleteVector(id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrInvalidID
	}

	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	vec, err := s.index.SearchVector(id)
	if err != nil {
		return err
	}
	if err := s.index.DeleteVector(id); err != nil {
		return err
	}
	s.rebuildANNLocked()

	if err := s.appendWAL(walOp{Op: "delete", ID: id}); err != nil {
		_ = s.index.AddVector(vec)
		s.rebuildANNLocked()
		return err
	}
	return s.maybeSnapshot()
}

func (s *Service) Search(values []float64, k int) ([]SearchResult, error) {
	if err := s.validateSearchRequest(values, k); err != nil {
		return nil, err
	}

	if s.searchMode == "ann" {
		results, ok := s.searchANN(values, k)
		if ok {
			return results, nil
		}
	}
	return s.searchExact(values, k), nil
}

func (s *Service) SearchBatch(queries []BatchSearchQuery) ([]BatchSearchResult, error) {
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
	idToVector := make(map[int]index.Vector)
	s.index.RangeVectors(func(vec index.Vector) bool {
		idToVector[hashID(vec.ID)] = vec
		return true
	})

	ids, err := s.annIndex.Search(values, k)
	if err != nil {
		return nil, false
	}

	results := make([]SearchResult, 0, len(ids))
	for _, hid := range ids {
		vec, ok := idToVector[hid]
		if !ok {
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
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})
	if k < len(results) {
		results = results[:k]
	}
	return results, true
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
		_ = s.index.DeleteVector(id)
	}
	s.rebuildANNLocked()
}

func (s *Service) restoreState() error {
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
	all := s.index.ListVectors()
	payload := make(map[string][]float64, len(all))
	for _, vec := range all {
		payload[vec.ID] = append([]float64(nil), vec.Values...)
	}

	if err := os.MkdirAll(filepath.Dir(s.snapshotPath), 0o755); err != nil {
		return err
	}
	tmp := s.snapshotPath + ".tmp"
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.snapshotPath)
}

func (s *Service) appendWAL(op walOp) error {
	if err := os.MkdirAll(filepath.Dir(s.walPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Service) replayWAL() error {
	f, err := os.Open(s.walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var op walOp
		if err := json.Unmarshal([]byte(line), &op); err != nil {
			return err
		}
		switch op.Op {
		case "upsert":
			if op.ID == "" || len(op.Values) == 0 || len(op.Values) > s.maxVectorDim {
				continue
			}
			vec := index.Vector{ID: op.ID, Values: op.Values}
			if err := s.index.AddVector(vec); err != nil {
				if errors.Is(err, index.ErrVectorExists) {
					_ = s.index.DeleteVector(op.ID)
					_ = s.index.AddVector(vec)
					continue
				}
				return err
			}
		case "delete":
			if op.ID == "" {
				continue
			}
			_ = s.index.DeleteVector(op.ID)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	s.rebuildANNLocked()
	return nil
}

func (s *Service) truncateWAL() error {
	if err := os.MkdirAll(filepath.Dir(s.walPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.walPath, []byte{}, 0o644)
}

func (s *Service) maybeSnapshot() error {
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
	data, err := os.ReadFile(s.snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload map[string][]float64
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	for id, values := range payload {
		if id == "" || len(values) == 0 || len(values) > s.maxVectorDim {
			continue
		}
		if err := s.index.AddVector(index.Vector{ID: id, Values: values}); err != nil && !errors.Is(err, index.ErrVectorExists) {
			return err
		}
		_ = s.annIndex.AddVector(hashID(id), values)
	}
	return nil
}

func hashID(id string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return int(h.Sum32())
}

func (s *Service) rebuildANNLocked() {
	s.annIndex = ann.NewAnnIndex()
	for _, vec := range s.index.ListVectors() {
		_ = s.annIndex.AddVector(hashID(vec.ID), vec.Values)
	}
}
