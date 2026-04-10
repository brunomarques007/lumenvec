package core

import (
	"errors"
	"os"
	"path/filepath"
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
