package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type VectorClient struct {
	baseURL    string
	httpClient *http.Client
}

type SearchResult struct {
	ID       string  `json:"id"`
	Distance float64 `json:"distance"`
}

type VectorPayload struct {
	ID     string    `json:"id"`
	Values []float64 `json:"values"`
}

type BatchSearchQuery struct {
	ID     string    `json:"id"`
	Values []float64 `json:"values"`
	K      int       `json:"k"`
}

type BatchSearchResult struct {
	ID      string         `json:"id"`
	Results []SearchResult `json:"results"`
}

func NewVectorClient(baseURL string) *VectorClient {
	return &VectorClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (vc *VectorClient) AddVector(vector []float64) error {
	return vc.AddVectorWithID(fmt.Sprintf("vec-%d", time.Now().UnixNano()), vector)
}

func (vc *VectorClient) AddVectorWithID(id string, vector []float64) error {
	return vc.AddVectors([]VectorPayload{{ID: id, Values: vector}})
}

func (vc *VectorClient) AddVectors(vectors []VectorPayload) error {
	url := fmt.Sprintf("%s/vectors/batch", vc.baseURL)
	body, err := json.Marshal(map[string]interface{}{
		"vectors": vectors,
	})
	if err != nil {
		return err
	}

	resp, err := vc.httpClient.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to add vectors: %s", resp.Status)
	}
	return nil
}

func (vc *VectorClient) SearchVector(vector []float64, k int) ([]SearchResult, error) {
	results, err := vc.SearchVectors([]BatchSearchQuery{{Values: vector, K: k}})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return []SearchResult{}, nil
	}
	return results[0].Results, nil
}

func (vc *VectorClient) SearchVectors(queries []BatchSearchQuery) ([]BatchSearchResult, error) {
	url := fmt.Sprintf("%s/vectors/search/batch", vc.baseURL)
	body, err := json.Marshal(map[string]interface{}{
		"queries": queries,
	})
	if err != nil {
		return nil, err
	}

	resp, err := vc.httpClient.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to search vectors: %s", resp.Status)
	}

	var results []BatchSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, err
	}
	return results, nil
}

func (vc *VectorClient) DeleteVector(id string) error {
	url := fmt.Sprintf("%s/vectors/%s", vc.baseURL, id)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := vc.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to delete vector: %s", resp.Status)
	}
	return nil
}
