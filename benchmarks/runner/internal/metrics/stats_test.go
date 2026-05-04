package metrics

import (
	"testing"
	"time"

	"lumenvec/internal/core"
)

func TestSummarizeAndRates(t *testing.T) {
	stats := Summarize([]float64{4, 1, 2, 3})
	if stats.Min != 1 || stats.Max != 4 || stats.P50 != 2.5 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if Summarize(nil) != (LatencyStats{}) {
		t.Fatal("expected empty stats for nil input")
	}
	if PerSecond(10, time.Second) != 10 {
		t.Fatal("unexpected per-second calculation")
	}
	if PerSecond(10, 0) != 0 {
		t.Fatal("expected zero rate for zero duration")
	}
}

func TestCalculateRecall(t *testing.T) {
	results := [][]core.SearchResult{{
		{ID: "a"},
		{ID: "b"},
		{ID: "x"},
		{ID: "d"},
		{ID: "e"},
	}}
	truth := [][]core.SearchResult{{
		{ID: "a"},
		{ID: "b"},
		{ID: "c"},
		{ID: "d"},
		{ID: "e"},
	}}
	recall := CalculateRecall(results, truth, 5)
	if recall.RecallAt1 == nil || *recall.RecallAt1 != 1 {
		t.Fatalf("unexpected recall@1: %+v", recall.RecallAt1)
	}
	if recall.RecallAt5 == nil || *recall.RecallAt5 != 0.8 {
		t.Fatalf("unexpected recall@5: %+v", recall.RecallAt5)
	}
	if recall.RecallAt10 != nil {
		t.Fatal("did not expect recall@10 when k=5")
	}
}
