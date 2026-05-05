package schema

import (
	"time"

	"lumenvec/benchmarks/runner/internal/metrics"
)

type ResultFile struct {
	RunID       string               `json:"run_id"`
	GitCommit   string               `json:"git_commit"`
	StartedAt   time.Time            `json:"started_at"`
	EndedAt     time.Time            `json:"ended_at"`
	Env         Environment          `json:"environment"`
	Dataset     DatasetMeta          `json:"dataset"`
	Engine      EngineMeta           `json:"engine"`
	Workload    WorkloadMeta         `json:"workload"`
	Ingest      IngestResult         `json:"ingest"`
	IndexBuild  IndexBuildResult     `json:"index_build"`
	Search      SearchResult         `json:"search"`
	Recall      metrics.RecallResult `json:"recall"`
	BatchSearch BatchSearchResult    `json:"batch_search"`
	BatchRecall metrics.RecallResult `json:"batch_recall"`
	Resources   ResourceResult       `json:"resources"`
	Errors      []RunError           `json:"errors"`
}

type Environment struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	GoVersion   string `json:"go_version"`
	CPUCount    int    `json:"cpu_count"`
	MachineID   string `json:"machine_id"`
	MemoryBytes uint64 `json:"memory_bytes"`
}

type DatasetMeta struct {
	Name           string `json:"name"`
	VectorCount    int    `json:"vector_count"`
	Dimension      int    `json:"dimension"`
	QueryCount     int    `json:"query_count"`
	Seed           int64  `json:"seed"`
	DistanceMetric string `json:"distance_metric"`
}

type EngineMeta struct {
	Name      string         `json:"name"`
	Version   string         `json:"version"`
	Profile   string         `json:"profile"`
	Transport string         `json:"transport"`
	Config    map[string]any `json:"config"`
}

type WorkloadMeta struct {
	BatchSize         int `json:"batch_size"`
	SearchBatchSize   int `json:"search_batch_size"`
	SearchConcurrency int `json:"search_concurrency"`
	K                 int `json:"k"`
	WarmupQueries     int `json:"warmup_queries"`
	MeasuredQueries   int `json:"measured_queries"`
}

type IngestResult struct {
	TotalVectors    int                  `json:"total_vectors"`
	TotalDurationMS float64              `json:"total_duration_ms"`
	VectorsPerSec   float64              `json:"vectors_per_second"`
	BatchLatencyMS  metrics.LatencyStats `json:"batch_latency_ms"`
}

type IndexBuildResult struct {
	Built           bool    `json:"built"`
	TotalDurationMS float64 `json:"total_duration_ms"`
}

type SearchResult struct {
	TotalQueries    int                  `json:"total_queries"`
	TotalDurationMS float64              `json:"total_duration_ms"`
	QueriesPerSec   float64              `json:"queries_per_second"`
	LatencyMS       metrics.LatencyStats `json:"latency_ms"`
}

type BatchSearchResult struct {
	Supported       bool                 `json:"supported"`
	BatchSize       int                  `json:"batch_size"`
	TotalQueries    int                  `json:"total_queries"`
	TotalBatches    int                  `json:"total_batches"`
	TotalDurationMS float64              `json:"total_duration_ms"`
	QueriesPerSec   float64              `json:"queries_per_second"`
	BatchLatencyMS  metrics.LatencyStats `json:"batch_latency_ms"`
}

type ResourceResult struct {
	PeakMemoryBytes    uint64  `json:"peak_memory_bytes"`
	AverageMemoryBytes uint64  `json:"average_memory_bytes"`
	PeakCPUPercent     float64 `json:"peak_cpu_percent"`
	AverageCPUPercent  float64 `json:"average_cpu_percent"`
	DiskBytes          uint64  `json:"disk_bytes"`
	StartupMS          float64 `json:"startup_ms"`
	RestartRecoveryMS  float64 `json:"restart_recovery_ms"`
}

type RunError struct {
	Phase   string `json:"phase"`
	Count   int    `json:"count"`
	Message string `json:"message"`
}
