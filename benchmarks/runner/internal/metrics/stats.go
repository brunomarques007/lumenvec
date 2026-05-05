package metrics

import (
	"math"
	"sort"
	"time"

	"lumenvec/internal/core"
)

type LatencyStats struct {
	Min  float64 `json:"min"`
	Mean float64 `json:"mean"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Max  float64 `json:"max"`
}

type RecallResult struct {
	RecallAt1  *float64 `json:"recall_at_1,omitempty"`
	RecallAt5  *float64 `json:"recall_at_5,omitempty"`
	RecallAt10 *float64 `json:"recall_at_10,omitempty"`
}

func Summarize(values []float64) LatencyStats {
	if len(values) == 0 {
		return LatencyStats{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	var sum float64
	for _, value := range sorted {
		sum += value
	}
	return LatencyStats{
		Min:  sorted[0],
		Mean: sum / float64(len(sorted)),
		P50:  Percentile(sorted, 0.50),
		P95:  Percentile(sorted, 0.95),
		P99:  Percentile(sorted, 0.99),
		Max:  sorted[len(sorted)-1],
	}
}

func Percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := p * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return sorted[lower]
	}
	weight := rank - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func CalculateRecall(results, truth [][]core.SearchResult, k int) RecallResult {
	recallAt := func(target int) *float64 {
		if k < target {
			return nil
		}
		var total float64
		var compared int
		for i := range truth {
			if len(results[i]) < target || len(truth[i]) < target {
				continue
			}
			expected := make(map[string]struct{}, target)
			for _, result := range truth[i][:target] {
				expected[result.ID] = struct{}{}
			}
			var hits int
			for _, result := range results[i][:target] {
				if _, ok := expected[result.ID]; ok {
					hits++
				}
			}
			total += float64(hits) / float64(target)
			compared++
		}
		if compared == 0 {
			return nil
		}
		value := total / float64(compared)
		return &value
	}
	return RecallResult{
		RecallAt1:  recallAt(1),
		RecallAt5:  recallAt(5),
		RecallAt10: recallAt(10),
	}
}

func Milliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}

func PerSecond(count int, duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(count) / duration.Seconds()
}
