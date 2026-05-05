package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumenvec/benchmarks/runner/internal/dataset"
	"lumenvec/benchmarks/runner/internal/engines"
	"lumenvec/benchmarks/runner/internal/metrics"
	"lumenvec/benchmarks/runner/internal/schema"
	"lumenvec/internal/core"
)

type config struct {
	engine                 string
	vectors                int
	dim                    int
	queries                int
	seed                   int64
	batchSize              int
	searchBatchSize        int
	concurrency            int
	k                      int
	warmup                 int
	output                 string
	machineID              string
	vectorIDPrefix         string
	qdrantURL              string
	chromaURL              string
	weaviateURL            string
	lumenvecURL            string
	lumenvecGRPCAddress    string
	pgvectorDSN            string
	collection             string
	dockerContainer        string
	dockerVolume           string
	resourceSampleInterval time.Duration
	skipDockerResources    bool
}

func main() {
	cfg := parseFlags()
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "benchmark failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	cfg := config{}
	flag.StringVar(&cfg.engine, "engine", "lumenvec-exact", "engine to run: lumenvec-exact, lumenvec-ann, lumenvec-http-exact, lumenvec-http-ann, lumenvec-http-ann-fast, lumenvec-http-ann-quality, lumenvec-grpc-exact, lumenvec-grpc-ann, lumenvec-grpc-ann-fast, lumenvec-grpc-ann-quality, qdrant, chroma, weaviate, pgvector, pgvector-hnsw, or pgvector-ivfflat")
	flag.IntVar(&cfg.vectors, "vectors", 10000, "number of vectors to ingest")
	flag.IntVar(&cfg.dim, "dim", 384, "vector dimension")
	flag.IntVar(&cfg.queries, "queries", 1000, "number of measured search queries")
	flag.Int64Var(&cfg.seed, "seed", 42, "deterministic random seed")
	flag.IntVar(&cfg.batchSize, "batch-size", 1000, "ingest batch size")
	flag.IntVar(&cfg.searchBatchSize, "search-batch-size", 100, "batch size for batch-search measurement")
	flag.IntVar(&cfg.concurrency, "concurrency", 1, "search concurrency")
	flag.IntVar(&cfg.k, "k", 10, "search top-k")
	flag.IntVar(&cfg.warmup, "warmup", 100, "warmup query count")
	flag.StringVar(&cfg.output, "output", "", "optional JSON output path")
	flag.StringVar(&cfg.machineID, "machine-id", "local", "machine identifier stored in the result")
	flag.StringVar(&cfg.vectorIDPrefix, "vector-id-prefix", "vec", "prefix used for generated vector IDs")
	flag.StringVar(&cfg.qdrantURL, "qdrant-url", "http://localhost:6333", "Qdrant REST URL")
	flag.StringVar(&cfg.chromaURL, "chroma-url", "http://localhost:18000", "Chroma REST URL")
	flag.StringVar(&cfg.weaviateURL, "weaviate-url", "http://localhost:18080", "Weaviate REST URL")
	flag.StringVar(&cfg.lumenvecURL, "lumenvec-url", "http://localhost:19290", "LumenVec HTTP URL")
	flag.StringVar(&cfg.lumenvecGRPCAddress, "lumenvec-grpc-address", "localhost:19390", "LumenVec gRPC address")
	flag.StringVar(&cfg.pgvectorDSN, "pgvector-dsn", "postgres://postgres:postgres@localhost:15432/postgres?sslmode=disable", "pgvector PostgreSQL DSN")
	flag.StringVar(&cfg.collection, "collection", "", "optional collection name for external engines")
	flag.StringVar(&cfg.dockerContainer, "docker-container", "", "optional Docker container name used for resource collection")
	flag.StringVar(&cfg.dockerVolume, "docker-volume", "", "optional Docker volume name used for disk-size collection")
	flag.DurationVar(&cfg.resourceSampleInterval, "resource-sample-interval", 500*time.Millisecond, "Docker CPU and memory sample interval")
	flag.BoolVar(&cfg.skipDockerResources, "skip-docker-resources", false, "skip Docker stats and volume-size collection")
	flag.Parse()
	return cfg
}

func run(ctx context.Context, cfg config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}

	started := time.Now().UTC()
	vectors := dataset.Generate(cfg.vectors, cfg.dim, cfg.seed, cfg.vectorIDPrefix)
	queries := dataset.Generate(cfg.queries+cfg.warmup, cfg.dim, cfg.seed+1, "query")
	groundTruth := dataset.ExactGroundTruth(vectors, queries[cfg.warmup:], cfg.k)

	eng, err := engines.New(engines.Config{
		Name:         cfg.engine,
		Dimension:    cfg.dim,
		MaxK:         cfg.k,
		QdrantURL:    cfg.qdrantURL,
		ChromaURL:    cfg.chromaURL,
		WeaviateURL:  cfg.weaviateURL,
		LumenVecURL:  cfg.lumenvecURL,
		LumenVecGRPC: cfg.lumenvecGRPCAddress,
		PGVectorDSN:  cfg.pgvectorDSN,
		Collection:   collectionName(started, cfg),
	})
	if err != nil {
		return err
	}
	defer func() { _ = eng.Close() }()

	setupStart := time.Now()
	if err := eng.Setup(ctx); err != nil {
		return err
	}
	startupMS := metrics.Milliseconds(time.Since(setupStart))

	container, volume := "", ""
	var resourceSampler *dockerResourceSampler
	if !cfg.skipDockerResources {
		container, volume = dockerResourceTarget(cfg)
		if container != "" {
			resourceSampler = startDockerResourceSampler(ctx, container, cfg.resourceSampleInterval)
			defer resourceSampler.cancel()
		}
	}

	ingest, err := measureIngest(ctx, eng, vectors, cfg.batchSize)
	if err != nil {
		return err
	}

	indexBuild, err := measureIndexBuild(ctx, eng)
	if err != nil {
		return err
	}

	for _, query := range queries[:cfg.warmup] {
		if _, err := eng.Search(ctx, query.Values, cfg.k); err != nil {
			return err
		}
	}

	search, results, searchErrors := measureSearch(ctx, eng, queries[cfg.warmup:], cfg.k, cfg.concurrency)
	recall := metrics.CalculateRecall(results, groundTruth, cfg.k)
	batchSearch, batchResults, batchErrors := measureBatchSearch(ctx, eng, queries[cfg.warmup:], cfg.k, cfg.searchBatchSize)
	var batchRecall metrics.RecallResult
	if batchSearch.Supported {
		batchRecall = metrics.CalculateRecall(batchResults, groundTruth, cfg.k)
	}
	searchErrors = append(searchErrors, batchErrors...)

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	resources := schema.ResourceResult{
		PeakMemoryBytes:    mem.Sys,
		AverageMemoryBytes: mem.Sys,
		StartupMS:          startupMS,
	}
	if !cfg.skipDockerResources {
		if container != "" || volume != "" {
			dockerResources, resourceErrors := collectDockerResources(ctx, resourceSampler, volume)
			dockerResources.StartupMS = startupMS
			if dockerResources.PeakMemoryBytes > 0 || dockerResources.PeakCPUPercent > 0 || dockerResources.DiskBytes > 0 {
				resources = dockerResources
			}
			searchErrors = append(searchErrors, resourceErrors...)
		}
	}

	out := schema.ResultFile{
		RunID:     runID(started, cfg),
		GitCommit: gitCommit(),
		StartedAt: started,
		EndedAt:   time.Now().UTC(),
		Env: schema.Environment{
			OS:          runtime.GOOS,
			Arch:        runtime.GOARCH,
			GoVersion:   runtime.Version(),
			CPUCount:    runtime.NumCPU(),
			MachineID:   cfg.machineID,
			MemoryBytes: mem.Sys,
		},
		Dataset: schema.DatasetMeta{
			Name:           "synthetic-uniform",
			VectorCount:    cfg.vectors,
			Dimension:      cfg.dim,
			QueryCount:     cfg.queries,
			Seed:           cfg.seed,
			DistanceMetric: "l2",
		},
		Engine: schema.EngineMeta{
			Name:      eng.Name(),
			Version:   "git",
			Profile:   eng.Profile(),
			Transport: eng.Transport(),
			Config: map[string]any{
				"search_mode":              cfg.engine,
				"batch_size":               cfg.batchSize,
				"search_batch_size":        cfg.searchBatchSize,
				"resource_sample_interval": cfg.resourceSampleInterval.String(),
				"qdrant_url":               cfg.qdrantURL,
				"chroma_url":               cfg.chromaURL,
				"weaviate_url":             cfg.weaviateURL,
				"lumenvec_url":             cfg.lumenvecURL,
				"lumenvec_grpc_address":    cfg.lumenvecGRPCAddress,
				"pgvector_dsn":             redactDSN(cfg.pgvectorDSN),
				"collection":               collectionName(started, cfg),
				"vector_id_prefix":         cfg.vectorIDPrefix,
			},
		},
		Workload: schema.WorkloadMeta{
			BatchSize:         cfg.batchSize,
			SearchBatchSize:   cfg.searchBatchSize,
			SearchConcurrency: cfg.concurrency,
			K:                 cfg.k,
			WarmupQueries:     cfg.warmup,
			MeasuredQueries:   cfg.queries,
		},
		Ingest:      ingest,
		IndexBuild:  indexBuild,
		Search:      search,
		Recall:      recall,
		BatchSearch: batchSearch,
		BatchRecall: batchRecall,
		Resources:   resources,
		Errors:      searchErrors,
	}

	return writeResult(out, cfg.output)
}

func dockerResourceTarget(cfg config) (string, string) {
	if cfg.dockerContainer != "" || cfg.dockerVolume != "" {
		return cfg.dockerContainer, cfg.dockerVolume
	}
	switch cfg.engine {
	case "lumenvec-http-exact":
		return "benchmarks-lumenvec-exact-1", "benchmarks_lumenvec_exact_data"
	case "lumenvec-http-ann":
		return "benchmarks-lumenvec-ann-1", "benchmarks_lumenvec_ann_data"
	case "lumenvec-http-ann-fast":
		return "benchmarks-lumenvec-ann-fast-1", "benchmarks_lumenvec_ann_fast_data"
	case "lumenvec-http-ann-quality":
		return "benchmarks-lumenvec-ann-quality-1", "benchmarks_lumenvec_ann_quality_data"
	case "lumenvec-grpc-exact":
		return "benchmarks-lumenvec-grpc-exact-1", "benchmarks_lumenvec_grpc_exact_data"
	case "lumenvec-grpc-ann":
		return "benchmarks-lumenvec-grpc-ann-1", "benchmarks_lumenvec_grpc_ann_data"
	case "lumenvec-grpc-ann-fast":
		return "benchmarks-lumenvec-grpc-ann-fast-1", "benchmarks_lumenvec_grpc_ann_fast_data"
	case "lumenvec-grpc-ann-quality":
		return "benchmarks-lumenvec-grpc-ann-quality-1", "benchmarks_lumenvec_grpc_ann_quality_data"
	case "qdrant":
		return "benchmarks-qdrant-1", "benchmarks_qdrant_data"
	case "chroma":
		return "benchmarks-chroma-1", "benchmarks_chroma_data"
	case "weaviate":
		return "benchmarks-weaviate-1", "benchmarks_weaviate_data"
	case "pgvector", "pgvector-hnsw", "pgvector-ivfflat":
		return "benchmarks-pgvector-1", "benchmarks_pgvector_data"
	default:
		return "", ""
	}
}

func redactDSN(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	if at := strings.LastIndex(value, "@"); at >= 0 {
		prefix := value[:at]
		if scheme := strings.Index(prefix, "://"); scheme >= 0 {
			return prefix[:scheme+3] + "redacted@" + value[at+1:]
		}
	}
	return value
}

func collectDockerResources(ctx context.Context, sampler *dockerResourceSampler, volume string) (schema.ResourceResult, []schema.RunError) {
	var resources schema.ResourceResult
	var errors []schema.RunError
	if sampler != nil {
		stats, samplerErrors := sampler.stop()
		resources.PeakCPUPercent = stats.PeakCPUPercent
		resources.AverageCPUPercent = stats.AverageCPUPercent
		resources.PeakMemoryBytes = stats.PeakMemoryBytes
		resources.AverageMemoryBytes = stats.AverageMemoryBytes
		for _, err := range samplerErrors {
			errors = append(errors, schema.RunError{Phase: "resources", Count: 1, Message: err})
		}
	}
	if volume != "" {
		diskBytes, err := dockerVolumeBytes(ctx, volume)
		if err != nil {
			errors = append(errors, schema.RunError{Phase: "resources", Count: 1, Message: err.Error()})
		} else {
			resources.DiskBytes = diskBytes
		}
	}
	return resources, errors
}

type dockerStatsResult struct {
	CPUPercent  float64
	MemoryBytes uint64
}

type dockerResourceSampler struct {
	container string
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	interval  time.Duration

	mu      sync.Mutex
	samples []dockerStatsResult
	errors  []string
}

type dockerResourceStats struct {
	PeakCPUPercent     float64
	AverageCPUPercent  float64
	PeakMemoryBytes    uint64
	AverageMemoryBytes uint64
}

func startDockerResourceSampler(ctx context.Context, container string, interval time.Duration) *dockerResourceSampler {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	sampleCtx, cancel := context.WithCancel(ctx)
	sampler := &dockerResourceSampler{
		container: container,
		ctx:       sampleCtx,
		cancel:    cancel,
		done:      make(chan struct{}),
		interval:  interval,
	}
	go sampler.run()
	return sampler
}

func (s *dockerResourceSampler) run() {
	defer close(s.done)
	s.sample()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.sample()
		}
	}
}

func (s *dockerResourceSampler) sample() {
	s.sampleWith(s.ctx)
}

func (s *dockerResourceSampler) sampleWith(ctx context.Context) {
	stats, err := dockerStats(ctx, s.container)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if len(s.errors) < 5 {
			s.errors = append(s.errors, err.Error())
		}
		return
	}
	s.samples = append(s.samples, stats)
}

func (s *dockerResourceSampler) stop() (dockerResourceStats, []string) {
	s.sampleWith(context.Background())
	s.cancel()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.samples) == 0 {
		if len(s.errors) == 0 {
			s.errors = append(s.errors, "docker resource sampler collected no samples")
		}
		return dockerResourceStats{}, append([]string(nil), s.errors...)
	}
	var cpuTotal float64
	var memTotal uint64
	var out dockerResourceStats
	for _, sample := range s.samples {
		cpuTotal += sample.CPUPercent
		memTotal += sample.MemoryBytes
		if sample.CPUPercent > out.PeakCPUPercent {
			out.PeakCPUPercent = sample.CPUPercent
		}
		if sample.MemoryBytes > out.PeakMemoryBytes {
			out.PeakMemoryBytes = sample.MemoryBytes
		}
	}
	out.AverageCPUPercent = cpuTotal / float64(len(s.samples))
	out.AverageMemoryBytes = memTotal / uint64(len(s.samples))
	return out, append([]string(nil), s.errors...)
}

func dockerStats(ctx context.Context, container string) (dockerStatsResult, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "docker", "stats", "--no-stream", "--format", "{{json .}}", container)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return dockerStatsResult{}, fmt.Errorf("docker stats %s: %w: %s", container, err, strings.TrimSpace(stderr.String()))
	}
	var raw struct {
		CPUPerc  string
		MemUsage string
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &raw); err != nil {
		return dockerStatsResult{}, fmt.Errorf("parse docker stats %s: %w", container, err)
	}
	memoryField := strings.TrimSpace(strings.Split(raw.MemUsage, "/")[0])
	memoryBytes, err := parseBytes(memoryField)
	if err != nil {
		return dockerStatsResult{}, fmt.Errorf("parse docker memory %q: %w", raw.MemUsage, err)
	}
	cpuPercent, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(raw.CPUPerc), "%"), 64)
	if err != nil {
		return dockerStatsResult{}, fmt.Errorf("parse docker cpu %q: %w", raw.CPUPerc, err)
	}
	return dockerStatsResult{CPUPercent: cpuPercent, MemoryBytes: memoryBytes}, nil
}

func dockerVolumeBytes(ctx context.Context, volume string) (uint64, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "docker", "run", "--rm", "-v", volume+":/data", "debian:bookworm-slim", "sh", "-c", "du -sk /data | awk '{print $1 * 1024}'")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("docker volume size %s: %w: %s", volume, err, strings.TrimSpace(stderr.String()))
	}
	value := strings.TrimSpace(stdout.String())
	bytesFloat, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("parse docker volume size %q: %w", value, err)
	}
	return uint64(bytesFloat), nil
}

func parseBytes(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty value")
	}
	units := []struct {
		suffix string
		scale  float64
	}{
		{"GiB", 1024 * 1024 * 1024},
		{"MiB", 1024 * 1024},
		{"KiB", 1024},
		{"GB", 1000 * 1000 * 1000},
		{"MB", 1000 * 1000},
		{"KB", 1000},
		{"B", 1},
	}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			number := strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			parsed, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return 0, err
			}
			return uint64(parsed * unit.scale), nil
		}
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	return uint64(parsed), nil
}

func validateConfig(cfg config) error {
	if cfg.vectors <= 0 {
		return fmt.Errorf("vectors must be positive")
	}
	if cfg.dim <= 0 {
		return fmt.Errorf("dim must be positive")
	}
	if cfg.queries <= 0 {
		return fmt.Errorf("queries must be positive")
	}
	if cfg.batchSize <= 0 {
		return fmt.Errorf("batch-size must be positive")
	}
	if cfg.searchBatchSize <= 0 {
		return fmt.Errorf("search-batch-size must be positive")
	}
	if cfg.concurrency <= 0 {
		return fmt.Errorf("concurrency must be positive")
	}
	if cfg.k <= 0 {
		return fmt.Errorf("k must be positive")
	}
	if cfg.k > cfg.vectors {
		return fmt.Errorf("k must be less than or equal to vectors")
	}
	if cfg.warmup < 0 {
		return fmt.Errorf("warmup must be non-negative")
	}
	return nil
}

func measureIngest(ctx context.Context, eng engines.Engine, vectors []dataset.Vector, batchSize int) (schema.IngestResult, error) {
	latencies := make([]float64, 0, (len(vectors)+batchSize-1)/batchSize)
	start := time.Now()
	for offset := 0; offset < len(vectors); offset += batchSize {
		end := offset + batchSize
		if end > len(vectors) {
			end = len(vectors)
		}
		batchStart := time.Now()
		if err := eng.Insert(ctx, vectors[offset:end]); err != nil {
			return schema.IngestResult{}, err
		}
		latencies = append(latencies, metrics.Milliseconds(time.Since(batchStart)))
	}
	total := time.Since(start)
	return schema.IngestResult{
		TotalVectors:    len(vectors),
		TotalDurationMS: metrics.Milliseconds(total),
		VectorsPerSec:   metrics.PerSecond(len(vectors), total),
		BatchLatencyMS:  metrics.Summarize(latencies),
	}, nil
}

func measureIndexBuild(ctx context.Context, eng engines.Engine) (schema.IndexBuildResult, error) {
	start := time.Now()
	built, err := eng.BuildIndex(ctx)
	if err != nil {
		return schema.IndexBuildResult{}, err
	}
	duration := time.Since(start)
	if !built {
		return schema.IndexBuildResult{Built: false}, nil
	}
	return schema.IndexBuildResult{
		Built:           true,
		TotalDurationMS: metrics.Milliseconds(duration),
	}, nil
}

func measureBatchSearch(ctx context.Context, eng engines.Engine, queries []dataset.Vector, k, batchSize int) (schema.BatchSearchResult, [][]core.SearchResult, []schema.RunError) {
	result := schema.BatchSearchResult{
		Supported:    false,
		BatchSize:    batchSize,
		TotalQueries: len(queries),
	}
	allResults := make([][]core.SearchResult, len(queries))
	latencies := make([]float64, 0, (len(queries)+batchSize-1)/batchSize)
	start := time.Now()
	totalBatches := 0
	for offset := 0; offset < len(queries); offset += batchSize {
		end := offset + batchSize
		if end > len(queries) {
			end = len(queries)
		}
		batchStart := time.Now()
		batchResults, supported, err := eng.SearchBatch(ctx, queries[offset:end], k)
		if !supported {
			return result, nil, nil
		}
		result.Supported = true
		if err != nil {
			return result, allResults, []schema.RunError{{Phase: "batch_search", Count: 1, Message: err.Error()}}
		}
		latencies = append(latencies, metrics.Milliseconds(time.Since(batchStart)))
		for i, hits := range batchResults {
			if offset+i < len(allResults) {
				allResults[offset+i] = hits
			}
		}
		totalBatches++
	}
	total := time.Since(start)
	result.TotalBatches = totalBatches
	result.TotalDurationMS = metrics.Milliseconds(total)
	result.QueriesPerSec = metrics.PerSecond(len(queries), total)
	result.BatchLatencyMS = metrics.Summarize(latencies)
	return result, allResults, nil
}

func measureSearch(ctx context.Context, eng engines.Engine, queries []dataset.Vector, k, concurrency int) (schema.SearchResult, [][]core.SearchResult, []schema.RunError) {
	type job struct {
		index int
		query dataset.Vector
	}
	type itemResult struct {
		index   int
		results []core.SearchResult
		latency float64
		err     error
	}

	jobs := make(chan job)
	out := make(chan itemResult, len(queries))
	workerCount := min(concurrency, len(queries))
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				start := time.Now()
				results, err := eng.Search(ctx, job.query.Values, k)
				out <- itemResult{
					index:   job.index,
					results: results,
					latency: metrics.Milliseconds(time.Since(start)),
					err:     err,
				}
			}
		}()
	}

	start := time.Now()
	go func() {
		for i, query := range queries {
			jobs <- job{index: i, query: query}
		}
		close(jobs)
		wg.Wait()
		close(out)
	}()

	latencies := make([]float64, 0, len(queries))
	results := make([][]core.SearchResult, len(queries))
	errorCount := 0
	var firstErr string
	for item := range out {
		if item.err != nil {
			errorCount++
			if firstErr == "" {
				firstErr = item.err.Error()
			}
			continue
		}
		latencies = append(latencies, item.latency)
		results[item.index] = item.results
	}
	total := time.Since(start)
	search := schema.SearchResult{
		TotalQueries:    len(queries),
		TotalDurationMS: metrics.Milliseconds(total),
		QueriesPerSec:   metrics.PerSecond(len(queries), total),
		LatencyMS:       metrics.Summarize(latencies),
	}
	if errorCount == 0 {
		return search, results, nil
	}
	return search, results, []schema.RunError{{Phase: "search", Count: errorCount, Message: firstErr}}
}

func writeResult(result schema.ResultFile, output string) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) == "" {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	return os.WriteFile(output, append(data, '\n'), 0o644)
}

func runID(started time.Time, cfg config) string {
	return fmt.Sprintf("%s-%s-%dk-%d", started.Format("20060102-150405"), cfg.machineID, cfg.vectors/1000, cfg.dim)
}

func collectionName(started time.Time, cfg config) string {
	if strings.TrimSpace(cfg.collection) != "" {
		return cfg.collection
	}
	name := strings.ToLower(strings.ReplaceAll(runID(started, cfg), "_", "-"))
	name = strings.ReplaceAll(name, ":", "-")
	return "lumenvec-bench-" + name
}

func gitCommit() string {
	return strings.TrimSpace(os.Getenv("GIT_COMMIT"))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
