package dataset

import "testing"

func TestGenerateIsDeterministic(t *testing.T) {
	first := Generate(2, 3, 42, "vec")
	second := Generate(2, 3, 42, "vec")
	if len(first) != 2 || first[0].ID != "vec-000000001" {
		t.Fatalf("unexpected generated vectors: %+v", first)
	}
	for i := range first {
		for j := range first[i].Values {
			if first[i].Values[j] != second[i].Values[j] {
				t.Fatal("expected deterministic generated values")
			}
		}
	}
}

func TestExactGroundTruth(t *testing.T) {
	vectors := []Vector{
		{ID: "a", Values: []float64{0, 0}},
		{ID: "b", Values: []float64{2, 0}},
		{ID: "c", Values: []float64{1, 0}},
	}
	queries := []Vector{{ID: "q", Values: []float64{0, 0}}}
	truth := ExactGroundTruth(vectors, queries, 2)
	if len(truth) != 1 || len(truth[0]) != 2 {
		t.Fatalf("unexpected truth: %+v", truth)
	}
	if truth[0][0].ID != "a" || truth[0][1].ID != "c" {
		t.Fatalf("unexpected nearest order: %+v", truth[0])
	}
}
