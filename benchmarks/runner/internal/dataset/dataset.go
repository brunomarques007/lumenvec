package dataset

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"lumenvec/internal/core"
)

type Vector struct {
	ID     string
	Values []float64
}

func Generate(count, dim int, seed int64, prefix string) []Vector {
	rng := rand.New(rand.NewSource(seed))
	out := make([]Vector, count)
	for i := range out {
		values := make([]float64, dim)
		for j := range values {
			values[j] = rng.Float64()
		}
		out[i] = Vector{
			ID:     fmt.Sprintf("%s-%09d", prefix, i+1),
			Values: values,
		}
	}
	return out
}

func ExactGroundTruth(vectors, queries []Vector, k int) [][]core.SearchResult {
	out := make([][]core.SearchResult, len(queries))
	for i, query := range queries {
		results := make([]core.SearchResult, 0, len(vectors))
		for _, vector := range vectors {
			results = append(results, core.SearchResult{
				ID:       vector.ID,
				Distance: L2(query.Values, vector.Values),
			})
		}
		sort.Slice(results, func(i, j int) bool {
			if results[i].Distance == results[j].Distance {
				return results[i].ID < results[j].ID
			}
			return results[i].Distance < results[j].Distance
		})
		out[i] = results[:k]
	}
	return out
}

func L2(a, b []float64) float64 {
	var sum float64
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return math.Sqrt(sum)
}
