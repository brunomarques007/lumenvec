package main

import (
	"fmt"

	"lumenvec/pkg/client"
)

func main() {
	c := client.NewVectorClient("http://localhost:19190")

	vectors := map[string][]float64{
		"doc-1": {1.0, 2.0, 3.0},
		"doc-2": {1.1, 2.1, 2.9},
		"doc-3": {9.0, 8.5, 7.5},
	}

	for id, v := range vectors {
		if err := c.AddVectorWithID(id, v); err != nil {
			panic(fmt.Sprintf("failed to ingest vector %s: %v", id, err))
		}
	}

	results, err := c.SearchVector([]float64{1.0, 2.0, 3.1}, 2)
	if err != nil {
		panic(fmt.Sprintf("failed to search vectors: %v", err))
	}

	fmt.Println("Top 2 nearest vectors:")
	for _, r := range results {
		fmt.Printf("- %s (distance=%.4f)\n", r.ID, r.Distance)
	}
}
