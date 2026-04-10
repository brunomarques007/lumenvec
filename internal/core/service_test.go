package core

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"lumenvec/internal/index"
	"lumenvec/internal/index/ann"
)

type stubPersistence struct {
	saveSnapshotFn func([]index.Vector) error
	loadSnapshotFn func() (map[string][]float64, error)
	appendWALFn    func(walOp) error
	replayWALFn    func(func(walOp) error) error
	truncateWALFn  func() error
}

func (s *stubPersistence) SaveSnapshot(vectors []index.Vector) error {
	if s.saveSnapshotFn != nil {
		return s.saveSnapshotFn(vectors)
	}
	return nil
}

type stubVectorStore struct {
	upsertFn func(index.Vector) error
	getFn    func(string) (index.Vector, error)
	deleteFn func(string) error
	listFn   func() []index.Vector
}

func (s *stubVectorStore) UpsertVector(vec index.Vector) error {
	if s.upsertFn != nil {
		return s.upsertFn(vec)
	}
	return nil
}

func (s *stubVectorStore) GetVector(id string) (index.Vector, error) {
	if s.getFn != nil {
		return s.getFn(id)
	}
	return index.Vector{}, index.ErrVectorNotFound
}

func (s *stubVectorStore) DeleteVector(id string) error {
	if s.deleteFn != nil {
		return s.deleteFn(id)
	}
	return nil
}

func (s *stubVectorStore) ListVectors() []index.Vector {
	if s.listFn != nil {
		return s.listFn()
	}
	return nil
}

func (s *stubPersistence) LoadSnapshot() (map[string][]float64, error) {
	if s.loadSnapshotFn != nil {
		return s.loadSnapshotFn()
	}
	return nil, nil
}

func (s *stubPersistence) AppendWAL(op walOp) error {
	if s.appendWALFn != nil {
		return s.appendWALFn(op)
	}
	return nil
}

func (s *stubPersistence) ReplayWAL(apply func(walOp) error) error {
	if s.replayWALFn != nil {
		return s.replayWALFn(apply)
	}
	return nil
}

func (s *stubPersistence) TruncateWAL() error {
	if s.truncateWALFn != nil {
		return s.truncateWALFn()
	}
	return nil
}

func newCoreService(t *testing.T, mode string) *Service {
	t.Helper()
	base := t.TempDir()
	return NewService(ServiceOptions{
		MaxVectorDim:  8,
		MaxK:          5,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    mode,
	})
}

func TestServiceAddGetDelete(t *testing.T) {
	svc := newCoreService(t, "exact")
	if err := svc.AddVector("a", []float64{1, 2, 3}); err != nil {
		t.Fatalf("AddVector() error = %v", err)
	}
	vec, err := svc.GetVector("a")
	if err != nil || vec.ID != "a" {
		t.Fatal("expected stored vector")
	}
	if err := svc.DeleteVector("a"); err != nil {
		t.Fatalf("DeleteVector() error = %v", err)
	}
	if _, err := svc.GetVector("a"); !errors.Is(err, index.ErrVectorNotFound) {
		t.Fatalf("expected ErrVectorNotFound, got %v", err)
	}
}

func TestServiceAddVectorsRollbackOnConflict(t *testing.T) {
	svc := newCoreService(t, "exact")
	_ = svc.AddVector("existing", []float64{1, 2, 3})
	err := svc.AddVectors([]index.Vector{
		{ID: "fresh", Values: []float64{1, 2, 3}},
		{ID: "existing", Values: []float64{4, 5, 6}},
	})
	if !errors.Is(err, index.ErrVectorExists) {
		t.Fatalf("expected ErrVectorExists, got %v", err)
	}
	if _, err := svc.GetVector("fresh"); !errors.Is(err, index.ErrVectorNotFound) {
		t.Fatal("expected rollback of fresh vector")
	}
}

func TestServiceSearchExactAndBatch(t *testing.T) {
	svc := newCoreService(t, "exact")
	_ = svc.AddVectors([]index.Vector{
		{ID: "a", Values: []float64{1, 2, 3}},
		{ID: "b", Values: []float64{1, 2, 4}},
	})
	results, err := svc.Search([]float64{1, 2, 3.1}, 1)
	if err != nil || len(results) != 1 || results[0].ID != "a" {
		t.Fatal("unexpected exact search results")
	}
	batch, err := svc.SearchBatch([]BatchSearchQuery{
		{ID: "q1", Values: []float64{1, 2, 3.1}, K: 1},
		{Values: []float64{1, 2, 3.9}, K: 1},
	})
	if err != nil || len(batch) != 2 || batch[0].ID != "q1" || batch[1].ID != "query-1" {
		t.Fatal("unexpected batch search results")
	}
}

func TestServiceSearchANNFallbackAndValidation(t *testing.T) {
	svc := newCoreService(t, "ann")
	_ = svc.AddVector("a", []float64{1, 2, 3})
	if _, err := svc.Search([]float64{}, 1); !errors.Is(err, ErrInvalidValues) {
		t.Fatalf("expected ErrInvalidValues, got %v", err)
	}
	if _, err := svc.Search([]float64{1, 2, 3}, 0); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("expected ErrInvalidK, got %v", err)
	}
	results, err := svc.Search([]float64{1, 2, 3}, 1)
	if err != nil || len(results) == 0 {
		t.Fatal("expected ANN search results")
	}
}

func TestTopKAccumulator(t *testing.T) {
	acc := newTopKAccumulator(2)
	acc.Add(SearchResult{ID: "a", Distance: 5})
	acc.Add(SearchResult{ID: "b", Distance: 3})
	acc.Add(SearchResult{ID: "c", Distance: 1})
	results := acc.Results()
	if len(results) != 2 || results[0].ID != "c" {
		t.Fatal("unexpected accumulator results")
	}
}

func TestServiceValidationAndHelpers(t *testing.T) {
	svc := newCoreService(t, "exact")
	if err := svc.AddVectors(nil); !errors.Is(err, ErrInvalidValues) {
		t.Fatalf("expected ErrInvalidValues, got %v", err)
	}
	if err := svc.AddVector("", []float64{1, 2, 3}); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
	if err := svc.AddVector("a", nil); !errors.Is(err, ErrInvalidValues) {
		t.Fatalf("expected ErrInvalidValues, got %v", err)
	}
	if _, err := svc.GetVector(""); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
	if err := svc.DeleteVector(""); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
	if _, err := svc.Search([]float64{1, 2, 3}, 6); !errors.Is(err, ErrKTooHigh) {
		t.Fatalf("expected ErrKTooHigh, got %v", err)
	}
	if _, err := svc.Search([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9}, 1); !errors.Is(err, ErrVectorDimTooHigh) {
		t.Fatalf("expected ErrVectorDimTooHigh, got %v", err)
	}
	if normalizeSearchMode("ANN") != "ann" || normalizeSearchMode("other") != "exact" {
		t.Fatal("unexpected normalizeSearchMode")
	}
	if _, err := svc.SearchBatch(nil); !errors.Is(err, ErrInvalidValues) {
		t.Fatalf("expected ErrInvalidValues, got %v", err)
	}
}

func TestServiceSearchBatchANNAndAccumulatorEdgeCases(t *testing.T) {
	svc := newCoreService(t, "ann")
	_ = svc.AddVector("a", []float64{1, 2, 3})
	got, err := svc.SearchBatch([]BatchSearchQuery{{Values: []float64{1, 2, 3}, K: 1}})
	if err != nil || len(got) != 1 {
		t.Fatal("expected ANN batch result")
	}

	acc := newTopKAccumulator(0)
	acc.Add(SearchResult{ID: "x", Distance: 1})
	if len(acc.Results()) != 0 {
		t.Fatal("expected empty accumulator")
	}
}

func TestServicePersistenceHelpers(t *testing.T) {
	svc := newCoreService(t, "exact")
	if err := svc.AddVector("a", []float64{1, 2, 3}); err != nil {
		t.Fatalf("AddVector() error = %v", err)
	}
	if err := svc.saveSnapshot(); err != nil {
		t.Fatalf("saveSnapshot() error = %v", err)
	}
	if err := svc.truncateWAL(); err != nil {
		t.Fatalf("truncateWAL() error = %v", err)
	}
	if err := svc.loadSnapshot(); err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}
	if err := svc.replayWAL(); err != nil {
		t.Fatalf("replayWAL() error = %v", err)
	}
	internalID := svc.idResolver.Assign("a")
	if internalID == 0 {
		t.Fatal("expected non-zero internal ID")
	}
	if got, ok := svc.idResolver.Lookup(internalID); !ok || got != "a" {
		t.Fatal("expected internal ID lookup to round-trip")
	}
}

func TestServicePersistenceEdgeCases(t *testing.T) {
	svc := newCoreService(t, "exact")

	if err := os.WriteFile(svc.snapshotPath, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.loadSnapshot(); err == nil {
		t.Fatal("expected loadSnapshot error")
	}

	if err := os.WriteFile(svc.walPath, []byte("{bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.replayWAL(); err == nil {
		t.Fatal("expected replayWAL error")
	}
}

func TestServiceMaybeSnapshotAndRestoreState(t *testing.T) {
	svc := newCoreService(t, "exact")
	svc.snapshotEvery = 1
	if err := svc.AddVector("a", []float64{1, 2, 3}); err != nil {
		t.Fatalf("AddVector() error = %v", err)
	}
	if svc.persistOps != 0 {
		t.Fatal("expected persistOps reset after snapshot")
	}

	restored := &Service{
		index:         index.NewIndex(),
		annIndex:      svc.annIndex,
		maxVectorDim:  svc.maxVectorDim,
		maxK:          svc.maxK,
		snapshotPath:  svc.snapshotPath,
		walPath:       svc.walPath,
		snapshotEvery: svc.snapshotEvery,
		searchMode:    svc.searchMode,
	}
	if err := restored.restoreState(); err != nil {
		t.Fatalf("restoreState() error = %v", err)
	}
}

func TestServiceJSONFailurePathsAndFallback(t *testing.T) {
	svc := newCoreService(t, "exact")
	if err := svc.index.AddVector(index.Vector{ID: "bad", Values: []float64{math.NaN()}}); err != nil {
		t.Fatalf("index.AddVector() error = %v", err)
	}
	if err := svc.vectorStore.UpsertVector(index.Vector{ID: "bad", Values: []float64{math.NaN()}}); err != nil {
		t.Fatalf("vectorStore.UpsertVector() error = %v", err)
	}
	if err := svc.saveSnapshot(); err == nil {
		t.Fatal("expected saveSnapshot error")
	}
	if err := svc.appendWAL(walOp{Op: "upsert", ID: "bad", Values: []float64{math.NaN()}}); err == nil {
		t.Fatal("expected appendWAL error")
	}

	svc2 := newCoreService(t, "ann")
	_ = svc2.AddVector("a", []float64{1, 2, 3})
	svc2.annIndex = ann.NewAnnIndex()
	results, err := svc2.Search([]float64{1, 2, 3}, 1)
	if err != nil || len(results) != 1 {
		t.Fatal("expected exact fallback when ANN path yields nothing")
	}
}

func TestServiceReplayWALBranches(t *testing.T) {
	svc := newCoreService(t, "exact")
	content := []byte(
		"{\"op\":\"upsert\",\"id\":\"a\",\"values\":[1,2,3]}\n" +
			"{\"op\":\"upsert\",\"id\":\"a\",\"values\":[4,5,6]}\n" +
			"{\"op\":\"delete\",\"id\":\"a\"}\n" +
			"{\"op\":\"upsert\",\"id\":\"\",\"values\":[1]}\n",
	)
	if err := os.WriteFile(svc.walPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.replayWAL(); err != nil {
		t.Fatalf("replayWAL() error = %v", err)
	}
	if _, err := svc.GetVector("a"); !errors.Is(err, index.ErrVectorNotFound) {
		t.Fatal("expected delete branch to be applied")
	}
}

func TestServiceMissingPersistenceFiles(t *testing.T) {
	svc := newCoreService(t, "exact")
	if err := svc.loadSnapshot(); err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}
	if err := svc.replayWAL(); err != nil {
		t.Fatalf("replayWAL() error = %v", err)
	}
}

func TestServiceAddVectorsValidationBranches(t *testing.T) {
	svc := newCoreService(t, "exact")
	err := svc.AddVectors([]index.Vector{{ID: "", Values: []float64{1}}})
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}

	err = svc.AddVectors([]index.Vector{{ID: "a", Values: []float64{}}})
	if !errors.Is(err, ErrInvalidValues) {
		t.Fatalf("expected ErrInvalidValues, got %v", err)
	}

	err = svc.AddVectors([]index.Vector{{ID: "a", Values: []float64{1, 2, 3, 4, 5, 6, 7, 8, 9}}})
	if !errors.Is(err, ErrVectorDimTooHigh) {
		t.Fatalf("expected ErrVectorDimTooHigh, got %v", err)
	}
}

func TestServiceFilesystemFailureBranches(t *testing.T) {
	svc := newCoreService(t, "exact")
	if err := svc.AddVector("a", []float64{1, 2, 3}); err != nil {
		t.Fatalf("AddVector() error = %v", err)
	}

	dir := t.TempDir()
	svc.snapshotPath = dir
	if err := svc.saveSnapshot(); err == nil {
		t.Fatal("expected saveSnapshot filesystem error")
	}

	svc.walPath = dir
	if err := svc.appendWAL(walOp{Op: "upsert", ID: "a", Values: []float64{1, 2, 3}}); err == nil {
		t.Fatal("expected appendWAL filesystem error")
	}
	if err := svc.truncateWAL(); err == nil {
		t.Fatal("expected truncateWAL filesystem error")
	}
}

func TestServiceDeleteRollbackAndHelpers(t *testing.T) {
	svc := newCoreService(t, "exact")
	if err := svc.AddVector("a", []float64{1, 2, 3}); err != nil {
		t.Fatalf("AddVector() error = %v", err)
	}
	svc.walPath = t.TempDir()
	if err := svc.DeleteVector("a"); err == nil {
		t.Fatal("expected delete persistence error")
	}
	if _, err := svc.GetVector("a"); err != nil {
		t.Fatal("expected rollback restore of vector")
	}

	acc := topKAccumulator{}
	acc.recomputeWorst()
	if acc.worstIndex != -1 {
		t.Fatal("expected empty accumulator worstIndex")
	}

	if err := svc.DeleteVector("missing"); !errors.Is(err, index.ErrVectorNotFound) {
		t.Fatalf("expected ErrVectorNotFound, got %v", err)
	}
}

func TestServiceSearchANNBranches(t *testing.T) {
	svc := newCoreService(t, "ann")
	if results, ok := svc.searchANN([]float64{1, 2, 3}, 1); ok || results != nil {
		t.Fatal("expected ANN miss on empty index")
	}

	_ = svc.AddVector("a", []float64{1, 2, 3})
	if results, ok := svc.searchANN([]float64{1, 2}, 1); ok || results != nil {
		t.Fatal("expected ANN miss on invalid dimension")
	}

	_ = svc.AddVector("b", []float64{1, 2, 4})
	results, ok := svc.searchANN([]float64{1, 2, 3.1}, 1)
	if !ok || len(results) != 1 {
		t.Fatal("expected ANN hit with truncation")
	}
}

func TestServiceStatsTrackANNHitAndFallback(t *testing.T) {
	svc := newCoreService(t, "ann")
	if err := svc.AddVector("a", []float64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Search([]float64{1, 2, 3}, 1); err != nil {
		t.Fatal(err)
	}

	svc.annIndex = ann.NewAnnIndex()
	if _, err := svc.Search([]float64{1, 2, 3}, 1); err != nil {
		t.Fatal(err)
	}

	stats := svc.Stats()
	if stats.SearchRequestsTotal != 2 {
		t.Fatalf("expected 2 search requests, got %d", stats.SearchRequestsTotal)
	}
	if stats.ANNSearchesTotal != 2 {
		t.Fatalf("expected 2 ANN searches, got %d", stats.ANNSearchesTotal)
	}
	if stats.ANNSearchHitsTotal != 1 {
		t.Fatalf("expected 1 ANN hit, got %d", stats.ANNSearchHitsTotal)
	}
	if stats.ANNSearchFallbacks != 1 {
		t.Fatalf("expected 1 ANN fallback, got %d", stats.ANNSearchFallbacks)
	}
	if stats.ExactSearchesTotal != 1 {
		t.Fatalf("expected 1 exact search, got %d", stats.ExactSearchesTotal)
	}
	if stats.ANNCandidatesReturned == 0 {
		t.Fatal("expected candidate accounting for ANN hit")
	}
}

func TestServiceStatsTrackANNError(t *testing.T) {
	svc := newCoreService(t, "ann")
	if err := svc.AddVector("a", []float64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}

	if _, ok := svc.searchANN([]float64{1, 2}, 1); ok {
		t.Fatal("expected ANN error path to return miss")
	}

	stats := svc.Stats()
	if stats.ANNSearchErrorsTotal != 1 {
		t.Fatalf("expected 1 ANN error, got %d", stats.ANNSearchErrorsTotal)
	}
	if stats.ANNSearchesTotal != 1 {
		t.Fatalf("expected 1 ANN search, got %d", stats.ANNSearchesTotal)
	}
}

func TestServiceStatsTrackANNEvaluationSample(t *testing.T) {
	svc := NewService(ServiceOptions{
		MaxVectorDim:      8,
		MaxK:              5,
		SnapshotPath:      filepath.Join(t.TempDir(), "snapshot.json"),
		WALPath:           filepath.Join(t.TempDir(), "wal.log"),
		SnapshotEvery:     2,
		SearchMode:        "ann",
		ANNEvalSampleRate: 100,
	})
	if err := svc.AddVectors([]index.Vector{
		{ID: "a", Values: []float64{1, 2, 3}},
		{ID: "b", Values: []float64{1, 2, 4}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Search([]float64{1, 2, 3.1}, 1); err != nil {
		t.Fatal(err)
	}

	stats := svc.Stats()
	if stats.ANNEvalSamplesTotal != 1 {
		t.Fatalf("expected 1 ann eval sample, got %d", stats.ANNEvalSamplesTotal)
	}
	if stats.ANNEvalTop1Matches != 1 {
		t.Fatalf("expected top1 match to be tracked, got %d", stats.ANNEvalTop1Matches)
	}
	if stats.ANNEvalComparedResults != 1 {
		t.Fatalf("expected compared results to be tracked, got %d", stats.ANNEvalComparedResults)
	}
	if stats.ANNEvalOverlapResults != 1 {
		t.Fatalf("expected overlap results to be tracked, got %d", stats.ANNEvalOverlapResults)
	}
}

func TestServiceRestoreStateFailureBranches(t *testing.T) {
	base := t.TempDir()
	svc := &Service{
		index:         index.NewIndex(),
		annIndex:      ann.NewAnnIndex(),
		maxVectorDim:  8,
		maxK:          5,
		snapshotPath:  filepath.Join(base, "snapshot.json"),
		walPath:       filepath.Join(base, "wal.log"),
		snapshotEvery: 2,
		searchMode:    "exact",
	}

	if err := os.WriteFile(svc.snapshotPath, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.restoreState(); err == nil {
		t.Fatal("expected restoreState loadSnapshot error")
	}

	if err := os.WriteFile(svc.snapshotPath, []byte(`{"a":[1,2,3]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.walPath, []byte("{bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.restoreState(); err == nil {
		t.Fatal("expected restoreState replayWAL error")
	}
}

func TestServiceMaybeSnapshotErrorBranch(t *testing.T) {
	svc := newCoreService(t, "exact")
	svc.snapshotEvery = 1
	svc.snapshotPath = t.TempDir()
	if err := svc.maybeSnapshot(); err == nil {
		t.Fatal("expected maybeSnapshot save error")
	}
}

func TestServiceLoadSnapshotBranches(t *testing.T) {
	svc := newCoreService(t, "exact")
	if err := svc.AddVector("dup", []float64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.snapshotPath, []byte(`{"dup":[1,2,3],"ok":[4,5,6],"bad":[],"too_big":[1,2,3,4,5,6,7,8,9]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.loadSnapshot(); err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}
	if _, err := svc.GetVector("ok"); err != nil {
		t.Fatal("expected valid snapshot vector")
	}
}

func TestServiceSearchBatchInvalidQuery(t *testing.T) {
	svc := newCoreService(t, "exact")
	if _, err := svc.SearchBatch([]BatchSearchQuery{{ID: "q", Values: nil, K: 1}}); !errors.Is(err, ErrInvalidValues) {
		t.Fatalf("expected ErrInvalidValues, got %v", err)
	}
}

func TestNewServiceWithDeps(t *testing.T) {
	idx := index.NewIndex()
	store := &stubVectorStore{}
	annIdx := ann.NewAnnIndex()
	resolver := newMemoryIDResolver()
	persistence := &stubPersistence{}

	svc := NewServiceWithDeps(ServiceOptions{
		MaxVectorDim:  8,
		MaxK:          5,
		SnapshotPath:  filepath.Join(t.TempDir(), "snapshot.json"),
		WALPath:       filepath.Join(t.TempDir(), "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "exact",
	}, ServiceDeps{
		Index:       idx,
		VectorStore: store,
		ANNIndex:    annIdx,
		IDResolver:  resolver,
		Persistence: persistence,
	})

	if svc.index != idx {
		t.Fatal("expected injected index to be used")
	}
	if svc.vectorStore != store {
		t.Fatal("expected injected vector store to be used")
	}
	if svc.idResolver != resolver {
		t.Fatal("expected injected ID resolver to be used")
	}
	if svc.persistence != persistence {
		t.Fatal("expected injected persistence to be used")
	}
	if annIdx == nil || svc.annIndex == nil {
		t.Fatal("expected ANN index instances to be available")
	}
}

func TestServiceGetVectorUsesVectorStore(t *testing.T) {
	svc := NewServiceWithDeps(ServiceOptions{
		MaxVectorDim:  8,
		MaxK:          5,
		SnapshotPath:  filepath.Join(t.TempDir(), "snapshot.json"),
		WALPath:       filepath.Join(t.TempDir(), "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "exact",
	}, ServiceDeps{
		Index: index.NewIndex(),
		VectorStore: &stubVectorStore{
			getFn: func(id string) (index.Vector, error) {
				return index.Vector{ID: id, Values: []float64{9, 8, 7}}, nil
			},
		},
		ANNIndex:    ann.NewAnnIndex(),
		IDResolver:  newMemoryIDResolver(),
		Persistence: &stubPersistence{},
	})

	vec, err := svc.GetVector("from-store")
	if err != nil {
		t.Fatal(err)
	}
	if vec.ID != "from-store" || len(vec.Values) != 3 || vec.Values[0] != 9 {
		t.Fatal("expected vector to come from vector store")
	}
}

func TestServiceSearchANNUsesVectorStoreForRescore(t *testing.T) {
	resolver := newMemoryIDResolver()

	svc := NewServiceWithDeps(ServiceOptions{
		MaxVectorDim:  8,
		MaxK:          5,
		SnapshotPath:  filepath.Join(t.TempDir(), "snapshot.json"),
		WALPath:       filepath.Join(t.TempDir(), "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "ann",
	}, ServiceDeps{
		Index: index.NewIndex(),
		VectorStore: &stubVectorStore{
			getFn: func(id string) (index.Vector, error) {
				if id != "ann-store" {
					t.Fatalf("unexpected vector store lookup %q", id)
				}
				return index.Vector{ID: id, Values: []float64{1, 2, 3}}, nil
			},
		},
		ANNIndex:    ann.NewAnnIndex(),
		IDResolver:  resolver,
		Persistence: &stubPersistence{},
	})
	internalID := resolver.Assign("ann-store")
	if err := svc.annIndex.AddVector(internalID, []float64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}

	results, ok := svc.searchANN([]float64{1, 2, 3}, 1)
	if !ok {
		t.Fatal("expected ANN search to succeed")
	}
	if len(results) != 1 || results[0].ID != "ann-store" {
		t.Fatal("expected ANN rescore to use vector store payload")
	}
}

func TestServiceWithDiskVectorStore(t *testing.T) {
	base := t.TempDir()
	svc := NewService(ServiceOptions{
		MaxVectorDim:  8,
		MaxK:          5,
		SnapshotPath:  filepath.Join(base, "snapshot.json"),
		WALPath:       filepath.Join(base, "wal.log"),
		SnapshotEvery: 2,
		SearchMode:    "ann",
		VectorStore:   "disk",
		VectorPath:    filepath.Join(base, "vectors"),
	})
	t.Cleanup(func() { _ = svc.Close() })

	if err := svc.AddVector("disk-1", []float64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	vec, err := svc.GetVector("disk-1")
	if err != nil || vec.ID != "disk-1" {
		t.Fatal("expected vector from disk-backed store")
	}
	results, err := svc.Search([]float64{1, 2, 3.1}, 1)
	if err != nil || len(results) != 1 || results[0].ID != "disk-1" {
		t.Fatal("expected search result from disk-backed store")
	}
}

func TestServiceWithDiskVectorStoreRestoresWithoutSnapshotWAL(t *testing.T) {
	base := t.TempDir()
	vectorPath := filepath.Join(base, "vectors")
	snapshotPath := filepath.Join(base, "snapshot.json")
	walPath := filepath.Join(base, "wal.log")

	svc := NewService(ServiceOptions{
		MaxVectorDim:  8,
		MaxK:          5,
		SnapshotPath:  snapshotPath,
		WALPath:       walPath,
		SnapshotEvery: 1,
		SearchMode:    "ann",
		VectorStore:   "disk",
		VectorPath:    vectorPath,
	})
	t.Cleanup(func() { _ = svc.Close() })
	if err := svc.AddVector("disk-restore", []float64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no snapshot file, got %v", err)
	}
	if _, err := os.Stat(walPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no wal file, got %v", err)
	}

	restored := NewService(ServiceOptions{
		MaxVectorDim:  8,
		MaxK:          5,
		SnapshotPath:  snapshotPath,
		WALPath:       walPath,
		SnapshotEvery: 1,
		SearchMode:    "ann",
		VectorStore:   "disk",
		VectorPath:    vectorPath,
	})
	t.Cleanup(func() { _ = restored.Close() })
	vec, err := restored.GetVector("disk-restore")
	if err != nil || vec.ID != "disk-restore" {
		t.Fatal("expected disk-backed vector after restart")
	}
	results, err := restored.Search([]float64{1, 2, 3.1}, 1)
	if err != nil || len(results) != 1 || results[0].ID != "disk-restore" {
		t.Fatal("expected ANN search result after disk-backed restore")
	}
}

func TestServiceConcurrentReadWrite(t *testing.T) {
	svc := newCoreService(t, "ann")
	var wg sync.WaitGroup

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("vec-%d", n%6)
			values := []float64{float64(n), float64(n + 1), float64(n + 2)}
			_ = svc.AddVector(id, values)
			_, _ = svc.GetVector(id)
			_, _ = svc.Search(values, 1)
		}(i)
	}

	wg.Wait()
}
