package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lumenvec/internal/index"
)

func TestSnapshotWALBackendRoundTrip(t *testing.T) {
	base := t.TempDir()
	backend := newSnapshotWALBackend(filepath.Join(base, "snapshot.json"), filepath.Join(base, "wal.log"))

	if err := backend.SaveSnapshot([]index.Vector{{ID: "a", Values: []float64{1, 2, 3}}}); err != nil {
		t.Fatal(err)
	}
	payload, err := backend.LoadSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || len(payload["a"]) != 3 {
		t.Fatal("expected snapshot payload")
	}

	if err := backend.AppendWAL(walOp{Op: "upsert", ID: "a", Values: []float64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	if err := backend.AppendWAL(walOp{Op: "delete", ID: "a"}); err != nil {
		t.Fatal(err)
	}

	seen := 0
	if err := backend.ReplayWAL(func(op walOp) error {
		seen++
		if op.ID == "" {
			t.Fatal("expected wal op id")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("expected 2 wal ops, got %d", seen)
	}

	if err := backend.TruncateWAL(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(base, "wal.log"))
	if err != nil || info.Size() != 0 {
		t.Fatal("expected truncated wal")
	}
}

func TestSnapshotWALBackendReplaySkipAndMissingFiles(t *testing.T) {
	base := t.TempDir()
	backend := newSnapshotWALBackend(filepath.Join(base, "snapshot.json"), filepath.Join(base, "wal.log"))

	payload, err := backend.LoadSnapshot()
	if err != nil || payload != nil {
		t.Fatal("expected nil snapshot for missing file")
	}

	if err := backend.ReplayWAL(func(walOp) error { return errSkipWALOp }); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(base, "wal.log"), []byte("{\"op\":\"upsert\",\"id\":\"a\",\"values\":[1]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := backend.ReplayWAL(func(walOp) error { return errSkipWALOp }); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotWALBackendReplayApplyError(t *testing.T) {
	base := t.TempDir()
	backend := newSnapshotWALBackend(filepath.Join(base, "snapshot.json"), filepath.Join(base, "wal.log"))
	if err := os.WriteFile(filepath.Join(base, "wal.log"), []byte("{\"op\":\"upsert\",\"id\":\"a\",\"values\":[1]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	want := errors.New("apply failed")
	if err := backend.ReplayWAL(func(walOp) error { return want }); !errors.Is(err, want) {
		t.Fatalf("expected apply failure, got %v", err)
	}
}
