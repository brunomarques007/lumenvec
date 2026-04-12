package core

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lumenvec/internal/index"
)

func TestFileVectorStoreCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors")
	store := newFileVectorStore(path)
	t.Cleanup(func() { _ = store.Close() })
	vec := index.Vector{ID: "doc-1", Values: []float64{1, 2, 3}}

	if err := store.UpsertVector(vec); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetVector("doc-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "doc-1" || len(got.Values) != 3 {
		t.Fatal("expected stored vector")
	}
	list := store.ListVectors()
	if len(list) != 1 || list[0].ID != "doc-1" {
		t.Fatal("expected listed vector")
	}
	if err := store.DeleteVector("doc-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetVector("doc-1"); !errors.Is(err, index.ErrVectorNotFound) {
		t.Fatal("expected vector to be deleted")
	}

	reopened := newFileVectorStore(path)
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.GetVector("doc-1"); !errors.Is(err, index.ErrVectorNotFound) {
		t.Fatal("expected deleted vector to stay deleted after reopen")
	}
}

func TestFileVectorStoreRebuildsFromAppendOnlyLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors")
	store := newFileVectorStore(path)
	t.Cleanup(func() { _ = store.Close() })

	if err := store.UpsertVector(index.Vector{ID: "doc-1", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertVector(index.Vector{ID: "doc-1", Values: []float64{4, 5, 6}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertVector(index.Vector{ID: "doc-2", Values: []float64{7, 8, 9}}); err != nil {
		t.Fatal(err)
	}

	reopened := newFileVectorStore(path)
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.GetVector("doc-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Values) != 3 || got.Values[0] != 4 {
		t.Fatal("expected latest upsert to survive reopen")
	}

	list := reopened.ListVectors()
	if len(list) != 2 {
		t.Fatal("expected both live vectors after rebuild")
	}
}

func TestNewDefaultVectorStoreDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors")
	store := newDefaultVectorStore("disk", path)
	if closer, ok := store.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	if err := store.UpsertVector(index.Vector{ID: "doc-1", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetVector("doc-1"); err != nil {
		t.Fatal(err)
	}
}

func TestFileVectorStoreCompactsStaleRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors")
	store := newFileVectorStore(path)
	t.Cleanup(func() { _ = store.Close() })

	for i := 0; i < 20; i++ {
		if err := store.UpsertVector(index.Vector{ID: "doc-1", Values: []float64{float64(i), 2, 3}}); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := newFileVectorStore(path)
	t.Cleanup(func() { _ = reopened.Close() })

	info, err := os.Stat(filepath.Join(path, fileVectorStoreDataFile))
	if err != nil {
		t.Fatal(err)
	}
	if got, wantMax := info.Size(), int64(84); got > wantMax {
		t.Fatalf("expected compacted data file, got size %d > %d", got, wantMax)
	}

	vec, err := reopened.GetVector("doc-1")
	if err != nil {
		t.Fatal(err)
	}
	if vec.Values[0] != 19 {
		t.Fatal("expected latest value after compaction and reopen")
	}
}

func TestFileVectorStoreStatsAndClosePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors")
	store := newFileVectorStore(path)
	if err := store.UpsertVector(index.Vector{ID: "doc-1", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertVector(index.Vector{ID: "doc-1", Values: []float64{4, 5, 6}}); err != nil {
		t.Fatal(err)
	}

	stats := store.DiskStats()
	if stats.FileBytes == 0 || stats.Records == 0 {
		t.Fatal("expected disk stats to be populated")
	}
	if !store.IsPersistent() {
		t.Fatal("expected persistent vector store")
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetVectorReadOnly("doc-1"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("expected os.ErrClosed, got %v", err)
	}
	if got := store.ListVectors(); got != nil {
		t.Fatal("expected nil vectors after close")
	}
}

func TestFileVectorStoreHelpersAndDecodeErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors")
	store := newFileVectorStore(path)
	t.Cleanup(func() { _ = store.Close() })

	record, err := encodeFileVectorRecord(fileVectorStoreOpPut, "doc-1", []float64{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeFileVectorPayload([]byte{1, 2, 3}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("expected short payload error")
	}
	if _, err := decodeFileVectorPayload(record[4 : len(record)-1]); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("expected truncated payload error")
	}

	file, err := os.OpenFile(filepath.Join(path, fileVectorStoreDataFile), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if _, err := file.Write(record[:len(record)-1]); err != nil {
		t.Fatal(err)
	}
	meta := fileVectorRecordMeta{recordOffset: 0, recordLength: uint32(len(record))}
	if _, err := readFileVectorRecordAt(file, meta); !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("expected eof, got %v", err)
	}
}

func TestFileVectorStoreReopenAndRebuildHelpers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors")
	store := newFileVectorStore(path)
	t.Cleanup(func() { _ = store.Close() })

	if err := store.UpsertVector(index.Vector{ID: "doc-1", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertVector(index.Vector{ID: "doc-2", Values: []float64{2, 3, 4}}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteVector("doc-1"); err != nil {
		t.Fatal(err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.ensureOpenLocked(); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(filepath.Join(path, fileVectorStoreDataFile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	offsets, stats, err := rebuildFileVectorStoreIndex(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(offsets) != 1 || offsets["doc-2"].recordLength == 0 {
		t.Fatal("expected rebuilt index to keep only live vector")
	}
	if stats.totalRecords == 0 || stats.staleRecords == 0 {
		t.Fatal("expected rebuilt stats to track total and stale records")
	}
}

func TestFileVectorStoreOpenLockedFailure(t *testing.T) {
	base := t.TempDir()
	filePath := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := &fileVectorStore{
		basePath: filePath,
		dataPath: filepath.Join(filePath, fileVectorStoreDataFile),
		offsets:  make(map[string]fileVectorRecordMeta),
	}
	if err := store.open(); err == nil {
		t.Fatal("expected open failure when base path is a file")
	}
}

func TestFileVectorStoreUsesConfiguredPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not reliable on Windows")
	}
	path := filepath.Join(t.TempDir(), "vectors")
	store := newFileVectorStoreWithSecurity(path, StrictStorageSecurityOptions())
	t.Cleanup(func() { _ = store.Close() })

	if err := store.UpsertVector(index.Vector{ID: "doc-1", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}

	dirMode, err := storagePathMode(path)
	if err != nil {
		t.Fatal(err)
	}
	if dirMode != 0o700 {
		t.Fatalf("expected dir mode 0700, got %o", dirMode)
	}

	fileMode, err := storagePathMode(filepath.Join(path, fileVectorStoreDataFile))
	if err != nil {
		t.Fatal(err)
	}
	if fileMode != 0o600 {
		t.Fatalf("expected file mode 0600, got %o", fileMode)
	}
}
