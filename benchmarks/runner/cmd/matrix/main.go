package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lumenvec/benchmarks/runner/internal/schema"
)

type config struct {
	runs             int
	vectors          int
	dim              int
	queries          int
	warmup           int
	k                int
	concurrent       int
	searchBatchSize  int
	searchBatchSizes string
	engines          string
	outputDir        string
	compareDir       string
	batchSizes       string
	skipDocker       bool
	aggregateOnly    bool
	sessionID        string
}

type engineCase struct {
	name        string
	urlFlag     string
	url         string
	serviceName string
}

type scenario struct {
	batchSize       int
	searchBatchSize int
}

type aggregateRow struct {
	Engine           string
	Profile          string
	Transport        string
	BatchSize        int
	SearchBatchSize  int
	Runs             int
	IngestMedian     float64
	IndexBuildMedian float64
	QPSMedian        float64
	BatchQPSMedian   float64
	BatchP95Median   float64
	P50Median        float64
	P95Median        float64
	P99Median        float64
	Recall1Median    float64
	Recall5Median    float64
	Recall10Median   float64
	MemoryMedian     float64
	CPUMedian        float64
	DiskMedian       float64
}

type comparisonRow struct {
	Key              string
	Engine           string
	Profile          string
	Transport        string
	BatchSize        int
	SearchBatchSize  int
	Status           string
	Notes            string
	IngestDeltaPct   float64
	QPSDeltaPct      float64
	BatchQPSDeltaPct float64
	P95DeltaPct      float64
	P99DeltaPct      float64
	Recall10Delta    float64
	MemoryDeltaPct   float64
	DiskDeltaPct     float64
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "matrix failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	cfg := config{}
	flag.IntVar(&cfg.runs, "runs", 3, "repetitions per engine and scenario")
	flag.IntVar(&cfg.vectors, "vectors", 10000, "number of vectors")
	flag.IntVar(&cfg.dim, "dim", 128, "vector dimension")
	flag.IntVar(&cfg.queries, "queries", 500, "measured query count")
	flag.IntVar(&cfg.warmup, "warmup", 100, "warmup query count")
	flag.IntVar(&cfg.k, "k", 10, "top-k")
	flag.IntVar(&cfg.concurrent, "concurrency", 4, "search concurrency")
	flag.IntVar(&cfg.searchBatchSize, "search-batch-size", 100, "batch size for batch-search measurement")
	flag.StringVar(&cfg.searchBatchSizes, "search-batch-sizes", "", "optional comma-separated batch-search sizes; overrides --search-batch-size when set")
	flag.StringVar(&cfg.engines, "engines", "", "optional comma-separated engine names; defaults to all matrix engines")
	flag.StringVar(&cfg.outputDir, "output-dir", "benchmarks/results/matrix-10k-128-c4-k10", "output directory")
	flag.StringVar(&cfg.compareDir, "compare-dir", "", "optional baseline result directory to compare against after aggregation")
	flag.StringVar(&cfg.batchSizes, "batch-sizes", "100,500,1000,2000", "comma-separated ingest batch sizes")
	flag.BoolVar(&cfg.skipDocker, "skip-docker", false, "do not run docker compose down/up between cases")
	flag.BoolVar(&cfg.aggregateOnly, "aggregate-only", false, "only regenerate aggregate outputs from existing JSON files in --output-dir")
	flag.Parse()
	cfg.sessionID = time.Now().UTC().Format("20060102-150405")
	return cfg
}

func run(cfg config) error {
	if cfg.runs <= 0 {
		return fmt.Errorf("runs must be positive")
	}
	if err := os.MkdirAll(cfg.outputDir, 0o755); err != nil {
		return err
	}
	scenarios, err := parseScenarios(cfg.batchSizes, cfg.searchBatchSize, cfg.searchBatchSizes)
	if err != nil {
		return err
	}

	engines, err := filterEngines(allEngineCases(), cfg.engines)
	if err != nil {
		return err
	}
	var resultFiles []string
	if cfg.aggregateOnly {
		resultFiles, err = resultFilesFromDir(cfg.outputDir)
		if err != nil {
			return err
		}
	} else {
		for _, scenario := range scenarios {
			for runIndex := 1; runIndex <= cfg.runs; runIndex++ {
				if !cfg.skipDocker {
					if err := resetDocker(engines); err != nil {
						return err
					}
				}
				for _, engine := range engines {
					output := filepath.Join(cfg.outputDir, fmt.Sprintf("%s-b%d-sb%d-run%d.json", engine.name, scenario.batchSize, scenario.searchBatchSize, runIndex))
					if err := runOne(cfg, scenario, engine, runIndex, output); err != nil {
						return err
					}
					resultFiles = append(resultFiles, output)
				}
			}
		}
	}
	if len(resultFiles) == 0 {
		return fmt.Errorf("no result JSON files found")
	}

	rows, err := aggregate(resultFiles)
	if err != nil {
		return err
	}
	if err := writeCSV(cfg, rows); err != nil {
		return err
	}
	if err := writeCharts(cfg, rows); err != nil {
		return err
	}
	if cfg.compareDir != "" {
		baselineFiles, err := resultFilesFromDir(cfg.compareDir)
		if err != nil {
			return err
		}
		if len(baselineFiles) == 0 {
			return fmt.Errorf("no baseline JSON files found in %s", cfg.compareDir)
		}
		baselineRows, err := aggregate(baselineFiles)
		if err != nil {
			return err
		}
		comparison := compareRows(baselineRows, rows)
		if err := writeComparisonCSV(cfg, comparison); err != nil {
			return err
		}
		if err := writeComparisonReport(cfg, comparison); err != nil {
			return err
		}
	}
	return writeReport(cfg, rows)
}

func resultFilesFromDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(files)
	return files, nil
}

func allEngineCases() []engineCase {
	return []engineCase{
		{name: "lumenvec-http-exact", urlFlag: "--lumenvec-url", url: "http://localhost:19290", serviceName: "lumenvec-exact"},
		{name: "lumenvec-http-ann", urlFlag: "--lumenvec-url", url: "http://localhost:19291", serviceName: "lumenvec-ann"},
		{name: "lumenvec-http-ann-fast", urlFlag: "--lumenvec-url", url: "http://localhost:19292", serviceName: "lumenvec-ann-fast"},
		{name: "lumenvec-http-ann-quality", urlFlag: "--lumenvec-url", url: "http://localhost:19293", serviceName: "lumenvec-ann-quality"},
		{name: "lumenvec-grpc-exact", urlFlag: "--lumenvec-grpc-address", url: "localhost:19390", serviceName: "lumenvec-grpc-exact"},
		{name: "lumenvec-grpc-ann", urlFlag: "--lumenvec-grpc-address", url: "localhost:19391", serviceName: "lumenvec-grpc-ann"},
		{name: "lumenvec-grpc-ann-fast", urlFlag: "--lumenvec-grpc-address", url: "localhost:19392", serviceName: "lumenvec-grpc-ann-fast"},
		{name: "lumenvec-grpc-ann-quality", urlFlag: "--lumenvec-grpc-address", url: "localhost:19393", serviceName: "lumenvec-grpc-ann-quality"},
		{name: "qdrant", urlFlag: "--qdrant-url", url: "http://localhost:6333", serviceName: "qdrant"},
		{name: "chroma", urlFlag: "--chroma-url", url: "http://localhost:18000", serviceName: "chroma"},
		{name: "weaviate", urlFlag: "--weaviate-url", url: "http://localhost:18080", serviceName: "weaviate"},
		{name: "pgvector", urlFlag: "--pgvector-dsn", url: "postgres://postgres:postgres@localhost:15432/postgres?sslmode=disable", serviceName: "pgvector"},
		{name: "pgvector-hnsw", urlFlag: "--pgvector-dsn", url: "postgres://postgres:postgres@localhost:15432/postgres?sslmode=disable", serviceName: "pgvector"},
		{name: "pgvector-ivfflat", urlFlag: "--pgvector-dsn", url: "postgres://postgres:postgres@localhost:15432/postgres?sslmode=disable", serviceName: "pgvector"},
	}
}

func filterEngines(all []engineCase, value string) ([]engineCase, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return all, nil
	}
	byName := make(map[string]engineCase, len(all))
	for _, engine := range all {
		byName[engine.name] = engine
	}
	parts := strings.Split(value, ",")
	seen := make(map[string]bool, len(parts))
	out := make([]engineCase, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		engine, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown engine %q", name)
		}
		seen[name] = true
		out = append(out, engine)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one engine is required")
	}
	return out, nil
}

func parseScenarios(batchSizes string, defaultSearchBatchSize int, searchBatchSizes string) ([]scenario, error) {
	ingestBatchSizes, err := parsePositiveList(batchSizes, "batch size")
	if err != nil {
		return nil, err
	}
	searchSizes := []int{defaultSearchBatchSize}
	if searchBatchSizes != "" {
		searchSizes, err = parsePositiveList(searchBatchSizes, "search batch size")
		if err != nil {
			return nil, err
		}
	}
	if defaultSearchBatchSize <= 0 {
		return nil, fmt.Errorf("search batch size must be positive: %d", defaultSearchBatchSize)
	}
	scenarios := make([]scenario, 0, len(ingestBatchSizes)*len(searchSizes))
	for _, batchSize := range ingestBatchSizes {
		for _, searchBatchSize := range searchSizes {
			scenarios = append(scenarios, scenario{batchSize: batchSize, searchBatchSize: searchBatchSize})
		}
	}
	sort.Slice(scenarios, func(i, j int) bool {
		if scenarios[i].batchSize != scenarios[j].batchSize {
			return scenarios[i].batchSize < scenarios[j].batchSize
		}
		return scenarios[i].searchBatchSize < scenarios[j].searchBatchSize
	})
	return scenarios, nil
}

func parsePositiveList(value, label string) ([]int, error) {
	parts := strings.Split(value, ",")
	seen := make(map[int]bool, len(parts))
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid %s %q", label, part)
		}
		if value <= 0 {
			return nil, fmt.Errorf("%s must be positive: %d", label, value)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one %s is required", label)
	}
	sort.Ints(values)
	return values, nil
}

func resetDocker(engines []engineCase) error {
	if err := command("docker", "compose", "-f", "benchmarks/docker-compose.yml", "down", "-v"); err != nil {
		return err
	}
	services := []string{"compose", "-f", "benchmarks/docker-compose.yml", "up", "-d"}
	seen := make(map[string]bool, len(engines))
	for _, engine := range engines {
		if seen[engine.serviceName] {
			continue
		}
		seen[engine.serviceName] = true
		services = append(services, engine.serviceName)
	}
	if err := command("docker", services...); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	return nil
}

func runOne(cfg config, scenario scenario, engine engineCase, runIndex int, output string) error {
	args := []string{
		"run", "./benchmarks/runner",
		"--engine", engine.name,
		engine.urlFlag, engine.url,
		"--vectors", fmt.Sprint(cfg.vectors),
		"--dim", fmt.Sprint(cfg.dim),
		"--queries", fmt.Sprint(cfg.queries),
		"--warmup", fmt.Sprint(cfg.warmup),
		"--batch-size", fmt.Sprint(scenario.batchSize),
		"--search-batch-size", fmt.Sprint(scenario.searchBatchSize),
		"--concurrency", fmt.Sprint(cfg.concurrent),
		"--k", fmt.Sprint(cfg.k),
		"--collection", fmt.Sprintf("bench-%s-%s-b%d-sb%d-run%d", cfg.sessionID, strings.ReplaceAll(engine.name, "_", "-"), scenario.batchSize, scenario.searchBatchSize, runIndex),
		"--vector-id-prefix", fmt.Sprintf("vec-%s-%s-b%d-sb%d-run%d", cfg.sessionID, strings.ReplaceAll(engine.name, "_", "-"), scenario.batchSize, scenario.searchBatchSize, runIndex),
		"--output", output,
	}
	return command("go", args...)
}

func command(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

func aggregate(paths []string) ([]aggregateRow, error) {
	groups := make(map[string][]schema.ResultFile)
	for _, path := range paths {
		var result schema.ResultFile
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		key := fmt.Sprintf("%s|%s|%s|%d|%d", result.Engine.Name, result.Engine.Profile, result.Engine.Transport, result.Workload.BatchSize, result.Workload.SearchBatchSize)
		groups[key] = append(groups[key], result)
	}

	rows := make([]aggregateRow, 0, len(groups))
	for _, results := range groups {
		first := results[0]
		rows = append(rows, aggregateRow{
			Engine:           displayEngine(first.Engine.Name),
			Profile:          first.Engine.Profile,
			Transport:        first.Engine.Transport,
			BatchSize:        first.Workload.BatchSize,
			SearchBatchSize:  first.Workload.SearchBatchSize,
			Runs:             len(results),
			IngestMedian:     medianFrom(results, func(r schema.ResultFile) float64 { return r.Ingest.VectorsPerSec }),
			IndexBuildMedian: medianFrom(results, func(r schema.ResultFile) float64 { return r.IndexBuild.TotalDurationMS }),
			QPSMedian:        medianFrom(results, func(r schema.ResultFile) float64 { return r.Search.QueriesPerSec }),
			BatchQPSMedian: medianFrom(results, func(r schema.ResultFile) float64 {
				return supportedBatchValue(r, func(r schema.ResultFile) float64 { return r.BatchSearch.QueriesPerSec })
			}),
			BatchP95Median: medianFrom(results, func(r schema.ResultFile) float64 {
				return supportedBatchValue(r, func(r schema.ResultFile) float64 { return r.BatchSearch.BatchLatencyMS.P95 })
			}),
			P50Median:      medianFrom(results, func(r schema.ResultFile) float64 { return r.Search.LatencyMS.P50 }),
			P95Median:      medianFrom(results, func(r schema.ResultFile) float64 { return r.Search.LatencyMS.P95 }),
			P99Median:      medianFrom(results, func(r schema.ResultFile) float64 { return r.Search.LatencyMS.P99 }),
			Recall1Median:  medianPtrFrom(results, func(r schema.ResultFile) *float64 { return r.Recall.RecallAt1 }),
			Recall5Median:  medianPtrFrom(results, func(r schema.ResultFile) *float64 { return r.Recall.RecallAt5 }),
			Recall10Median: medianPtrFrom(results, func(r schema.ResultFile) *float64 { return r.Recall.RecallAt10 }),
			MemoryMedian:   medianFrom(results, func(r schema.ResultFile) float64 { return bytesToMiB(r.Resources.PeakMemoryBytes) }),
			CPUMedian:      medianFrom(results, func(r schema.ResultFile) float64 { return r.Resources.PeakCPUPercent }),
			DiskMedian:     medianFrom(results, func(r schema.ResultFile) float64 { return bytesToMiB(r.Resources.DiskBytes) }),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].BatchSize != rows[j].BatchSize {
			return rows[i].BatchSize < rows[j].BatchSize
		}
		if rows[i].SearchBatchSize != rows[j].SearchBatchSize {
			return rows[i].SearchBatchSize < rows[j].SearchBatchSize
		}
		if rows[i].Engine != rows[j].Engine {
			return rows[i].Engine < rows[j].Engine
		}
		return rows[i].Profile < rows[j].Profile
	})
	return rows, nil
}

func writeReport(cfg config, rows []aggregateRow) error {
	path := filepath.Join(cfg.outputDir, "report.md")
	var b strings.Builder
	fmt.Fprintf(&b, "# Benchmark Matrix Report: %dk / %dd / c%d / k%d\n\n", cfg.vectors/1000, cfg.dim, cfg.concurrent, cfg.k)
	b.WriteString("This report aggregates repeated local Docker-service benchmark runs using the median value per row.\n\n")
	b.WriteString("## Scenario\n\n")
	fmt.Fprintf(&b, "- Runs per row: `%d`\n", cfg.runs)
	fmt.Fprintf(&b, "- Vectors: `%d`\n", cfg.vectors)
	fmt.Fprintf(&b, "- Dimensions: `%d`\n", cfg.dim)
	fmt.Fprintf(&b, "- Queries: `%d`\n", cfg.queries)
	fmt.Fprintf(&b, "- Warmup queries: `%d`\n", cfg.warmup)
	fmt.Fprintf(&b, "- Search concurrency: `%d`\n", cfg.concurrent)
	if cfg.searchBatchSizes != "" {
		fmt.Fprintf(&b, "- Search batch sizes: `%s`\n", cfg.searchBatchSizes)
	} else {
		fmt.Fprintf(&b, "- Search batch size: `%d`\n", cfg.searchBatchSize)
	}
	fmt.Fprintf(&b, "- Top-k: `%d`\n", cfg.k)
	fmt.Fprintf(&b, "- Batch sizes: `%s`\n", cfg.batchSizes)
	b.WriteString("- Isolation model: Docker services\n")
	b.WriteString("- Storage model: Docker-managed persistent volumes\n\n")
	b.WriteString("## Median Results\n\n")
	b.WriteString("| Engine | Profile | Transport | Ingest batch | Search batch | Runs | Median ingest vectors/s | Median index build ms | Median search QPS | Median batch search QPS | Median batch p95 ms | Median p50 ms | Median p95 ms | Median p99 ms | Median Recall@1 | Median Recall@5 | Median Recall@10 | Median memory MiB | Median CPU % | Median disk MiB |\n")
	b.WriteString("|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %d | %.2f | %.3f | %.2f | %.2f | %.3f | %.3f | %.3f | %.3f | %.4f | %.4f | %.4f | %.2f | %.2f | %.2f |\n",
			row.Engine,
			row.Profile,
			row.Transport,
			row.BatchSize,
			row.SearchBatchSize,
			row.Runs,
			row.IngestMedian,
			row.IndexBuildMedian,
			row.QPSMedian,
			row.BatchQPSMedian,
			row.BatchP95Median,
			row.P50Median,
			row.P95Median,
			row.P99Median,
			row.Recall1Median,
			row.Recall5Median,
			row.Recall10Median,
			row.MemoryMedian,
			row.CPUMedian,
			row.DiskMedian)
	}
	writeRankings(&b, rows)
	b.WriteString("\n## Charts\n\n")
	b.WriteString("The same aggregated median rows are exported as `aggregate.csv` and rendered into SVG charts under `charts/`.\n\n")
	b.WriteString("![Ingest throughput](charts/ingest_vectors_per_second.svg)\n\n")
	b.WriteString("![Search throughput](charts/search_qps.svg)\n\n")
	b.WriteString("![Recall versus search QPS](charts/recall10_vs_search_qps.svg)\n\n")
	b.WriteString("![Resource usage](charts/resource_usage.svg)\n")
	if cfg.compareDir != "" {
		b.WriteString("\n## Baseline Comparison\n\n")
		b.WriteString("This run was compared against the configured baseline directory. See `comparison.md` and `comparison.csv` for row-level deltas and regression flags.\n")
	}
	b.WriteString("\n## Notes\n\n")
	b.WriteString("- Each run resets Docker volumes before executing the configured engines for that repetition and scenario unless `--skip-docker` is set.\n")
	b.WriteString("- Docker CPU and memory metrics are sampled during each run with repeated `docker stats --no-stream`; disk is measured after the run.\n")
	b.WriteString("- Raw JSON files are stored next to this report.\n")
	b.WriteString("- ANN latency should always be interpreted alongside recall.\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeRankings(b *strings.Builder, rows []aggregateRow) {
	const recallThreshold = 0.75
	b.WriteString("\n## Rankings\n\n")
	fmt.Fprintf(b, "Search rankings require `recall@10 >= %.2f` so low-recall ANN profiles are not treated as equivalent to exact or higher-quality ANN results.\n\n", recallThreshold)
	writeRankingTable(b, "Top ingest throughput", topRows(rows, 5, func(row aggregateRow) bool {
		return true
	}, func(row aggregateRow) float64 {
		return row.IngestMedian
	}, true), "vectors/s", func(row aggregateRow) float64 {
		return row.IngestMedian
	})
	writeRankingTable(b, fmt.Sprintf("Top search QPS with recall@10 >= %.2f", recallThreshold), topRows(rows, 5, func(row aggregateRow) bool {
		return row.Recall10Median >= recallThreshold
	}, func(row aggregateRow) float64 {
		return row.QPSMedian
	}, true), "queries/s", func(row aggregateRow) float64 {
		return row.QPSMedian
	})
	writeRankingTable(b, fmt.Sprintf("Top native batch-search QPS with recall@10 >= %.2f", recallThreshold), topRows(rows, 5, func(row aggregateRow) bool {
		return row.Recall10Median >= recallThreshold && row.BatchQPSMedian > 0
	}, func(row aggregateRow) float64 {
		return row.BatchQPSMedian
	}, true), "queries/s", func(row aggregateRow) float64 {
		return row.BatchQPSMedian
	})
	writeRankingTable(b, fmt.Sprintf("Lowest p95 latency with recall@10 >= %.2f", recallThreshold), topRows(rows, 5, func(row aggregateRow) bool {
		return row.Recall10Median >= recallThreshold
	}, func(row aggregateRow) float64 {
		return row.P95Median
	}, false), "ms", func(row aggregateRow) float64 {
		return row.P95Median
	})
	writeRankingTable(b, "Lowest memory usage", topRows(rows, 5, func(row aggregateRow) bool {
		return row.MemoryMedian > 0
	}, func(row aggregateRow) float64 {
		return row.MemoryMedian
	}, false), "MiB", func(row aggregateRow) float64 {
		return row.MemoryMedian
	})
	writeRankingTable(b, "Lowest disk usage", topRows(rows, 5, func(row aggregateRow) bool {
		return row.DiskMedian > 0
	}, func(row aggregateRow) float64 {
		return row.DiskMedian
	}, false), "MiB", func(row aggregateRow) float64 {
		return row.DiskMedian
	})
}

func writeRankingTable(b *strings.Builder, title string, rows []aggregateRow, unit string, valueFn func(aggregateRow) float64) {
	fmt.Fprintf(b, "### %s\n\n", title)
	if len(rows) == 0 {
		b.WriteString("No rows matched this ranking.\n\n")
		return
	}
	b.WriteString("| Rank | Engine | Profile | Transport | Ingest batch | Search batch | Value | Recall@10 |\n")
	b.WriteString("|---:|---|---|---|---:|---:|---:|---:|\n")
	for i, row := range rows {
		fmt.Fprintf(b,
			"| %d | %s | %s | %s | %d | %d | %.2f %s | %.4f |\n",
			i+1,
			row.Engine,
			row.Profile,
			row.Transport,
			row.BatchSize,
			row.SearchBatchSize,
			valueFn(row),
			unit,
			row.Recall10Median)
	}
	b.WriteString("\n")
}

func topRows(rows []aggregateRow, limit int, keep func(aggregateRow) bool, score func(aggregateRow) float64, descending bool) []aggregateRow {
	out := make([]aggregateRow, 0, len(rows))
	for _, row := range rows {
		if keep(row) {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left := score(out[i])
		right := score(out[j])
		if left != right {
			if descending {
				return left > right
			}
			return left < right
		}
		return aggregateKey(out[i]) < aggregateKey(out[j])
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func writeCSV(cfg config, rows []aggregateRow) error {
	path := filepath.Join(cfg.outputDir, "aggregate.csv")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	writer := csv.NewWriter(f)
	defer writer.Flush()
	if err := writer.Write([]string{
		"engine",
		"profile",
		"transport",
		"ingest_batch",
		"search_batch",
		"runs",
		"median_ingest_vectors_per_second",
		"median_index_build_ms",
		"median_search_qps",
		"median_batch_search_qps",
		"median_batch_p95_ms",
		"median_p50_ms",
		"median_p95_ms",
		"median_p99_ms",
		"median_recall_at_1",
		"median_recall_at_5",
		"median_recall_at_10",
		"median_memory_mib",
		"median_cpu_percent",
		"median_disk_mib",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.Engine,
			row.Profile,
			row.Transport,
			strconv.Itoa(row.BatchSize),
			strconv.Itoa(row.SearchBatchSize),
			strconv.Itoa(row.Runs),
			formatFloat(row.IngestMedian, 2),
			formatFloat(row.IndexBuildMedian, 3),
			formatFloat(row.QPSMedian, 2),
			formatFloat(row.BatchQPSMedian, 2),
			formatFloat(row.BatchP95Median, 3),
			formatFloat(row.P50Median, 3),
			formatFloat(row.P95Median, 3),
			formatFloat(row.P99Median, 3),
			formatFloat(row.Recall1Median, 4),
			formatFloat(row.Recall5Median, 4),
			formatFloat(row.Recall10Median, 4),
			formatFloat(row.MemoryMedian, 2),
			formatFloat(row.CPUMedian, 2),
			formatFloat(row.DiskMedian, 2),
		}); err != nil {
			return err
		}
	}
	if err := writer.Error(); err != nil {
		return err
	}
	return nil
}

func compareRows(baselineRows, candidateRows []aggregateRow) []comparisonRow {
	baselineByKey := make(map[string]aggregateRow, len(baselineRows))
	for _, row := range baselineRows {
		baselineByKey[aggregateKey(row)] = row
	}
	out := make([]comparisonRow, 0, len(candidateRows))
	for _, candidate := range candidateRows {
		key := aggregateKey(candidate)
		baseline, ok := baselineByKey[key]
		if !ok {
			out = append(out, comparisonRow{
				Key:             key,
				Engine:          candidate.Engine,
				Profile:         candidate.Profile,
				Transport:       candidate.Transport,
				BatchSize:       candidate.BatchSize,
				SearchBatchSize: candidate.SearchBatchSize,
				Status:          "new",
				Notes:           "no matching baseline row",
			})
			continue
		}
		row := comparisonRow{
			Key:              key,
			Engine:           candidate.Engine,
			Profile:          candidate.Profile,
			Transport:        candidate.Transport,
			BatchSize:        candidate.BatchSize,
			SearchBatchSize:  candidate.SearchBatchSize,
			IngestDeltaPct:   percentDelta(baseline.IngestMedian, candidate.IngestMedian),
			QPSDeltaPct:      percentDelta(baseline.QPSMedian, candidate.QPSMedian),
			BatchQPSDeltaPct: percentDelta(baseline.BatchQPSMedian, candidate.BatchQPSMedian),
			P95DeltaPct:      percentDelta(baseline.P95Median, candidate.P95Median),
			P99DeltaPct:      percentDelta(baseline.P99Median, candidate.P99Median),
			Recall10Delta:    candidate.Recall10Median - baseline.Recall10Median,
			MemoryDeltaPct:   percentDelta(baseline.MemoryMedian, candidate.MemoryMedian),
			DiskDeltaPct:     percentDelta(baseline.DiskMedian, candidate.DiskMedian),
		}
		row.Status, row.Notes = comparisonStatus(row)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return statusRank(out[i].Status) < statusRank(out[j].Status)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func writeComparisonCSV(cfg config, rows []comparisonRow) error {
	path := filepath.Join(cfg.outputDir, "comparison.csv")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	writer := csv.NewWriter(f)
	defer writer.Flush()
	if err := writer.Write([]string{
		"status",
		"engine",
		"profile",
		"transport",
		"ingest_batch",
		"search_batch",
		"ingest_delta_pct",
		"search_qps_delta_pct",
		"batch_search_qps_delta_pct",
		"p95_delta_pct",
		"p99_delta_pct",
		"recall10_delta",
		"memory_delta_pct",
		"disk_delta_pct",
		"notes",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.Status,
			row.Engine,
			row.Profile,
			row.Transport,
			strconv.Itoa(row.BatchSize),
			strconv.Itoa(row.SearchBatchSize),
			formatFloat(row.IngestDeltaPct, 2),
			formatFloat(row.QPSDeltaPct, 2),
			formatFloat(row.BatchQPSDeltaPct, 2),
			formatFloat(row.P95DeltaPct, 2),
			formatFloat(row.P99DeltaPct, 2),
			formatFloat(row.Recall10Delta, 4),
			formatFloat(row.MemoryDeltaPct, 2),
			formatFloat(row.DiskDeltaPct, 2),
			row.Notes,
		}); err != nil {
			return err
		}
	}
	if err := writer.Error(); err != nil {
		return err
	}
	return nil
}

func writeComparisonReport(cfg config, rows []comparisonRow) error {
	path := filepath.Join(cfg.outputDir, "comparison.md")
	var b strings.Builder
	b.WriteString("# Benchmark Baseline Comparison\n\n")
	fmt.Fprintf(&b, "- Baseline directory: `%s`\n", filepath.ToSlash(cfg.compareDir))
	fmt.Fprintf(&b, "- Candidate directory: `%s`\n", filepath.ToSlash(cfg.outputDir))
	b.WriteString("- Regression rule: search QPS or batch-search QPS below `-5%`, p95 or p99 above `+5%`, or recall@10 below baseline.\n\n")
	counts := make(map[string]int)
	for _, row := range rows {
		counts[row.Status]++
	}
	b.WriteString("## Summary\n\n")
	fmt.Fprintf(&b, "- Regressions: `%d`\n", counts["regression"])
	fmt.Fprintf(&b, "- Improvements or neutral rows: `%d`\n", counts["ok"])
	fmt.Fprintf(&b, "- New rows: `%d`\n\n", counts["new"])
	b.WriteString("## Rows\n\n")
	b.WriteString("| Status | Engine | Profile | Transport | Ingest batch | Search batch | Ingest delta % | Search QPS delta % | Batch QPS delta % | p95 delta % | p99 delta % | Recall@10 delta | Notes |\n")
	b.WriteString("|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %d | %.2f | %.2f | %.2f | %.2f | %.2f | %.4f | %s |\n",
			row.Status,
			row.Engine,
			row.Profile,
			row.Transport,
			row.BatchSize,
			row.SearchBatchSize,
			row.IngestDeltaPct,
			row.QPSDeltaPct,
			row.BatchQPSDeltaPct,
			row.P95DeltaPct,
			row.P99DeltaPct,
			row.Recall10Delta,
			row.Notes)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func aggregateKey(row aggregateRow) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d", row.Engine, row.Profile, row.Transport, row.BatchSize, row.SearchBatchSize)
}

func percentDelta(baseline, candidate float64) float64 {
	if baseline == 0 {
		if candidate == 0 {
			return 0
		}
		return 100
	}
	return (candidate - baseline) / baseline * 100
}

func comparisonStatus(row comparisonRow) (string, string) {
	var notes []string
	if row.QPSDeltaPct < -5 {
		notes = append(notes, "search QPS below -5%")
	}
	if row.BatchQPSDeltaPct < -5 {
		notes = append(notes, "batch search QPS below -5%")
	}
	if row.P95DeltaPct > 5 {
		notes = append(notes, "p95 above +5%")
	}
	if row.P99DeltaPct > 5 {
		notes = append(notes, "p99 above +5%")
	}
	if row.Recall10Delta < -0.0001 {
		notes = append(notes, "recall@10 below baseline")
	}
	if len(notes) > 0 {
		return "regression", strings.Join(notes, "; ")
	}
	return "ok", ""
}

func statusRank(status string) int {
	switch status {
	case "regression":
		return 0
	case "new":
		return 1
	default:
		return 2
	}
}

func writeCharts(cfg config, rows []aggregateRow) error {
	dir := filepath.Join(cfg.outputDir, "charts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	charts := []struct {
		name  string
		title string
		unit  string
		fn    func(aggregateRow) float64
	}{
		{name: "ingest_vectors_per_second.svg", title: "Median Ingest Throughput", unit: "vectors/s", fn: func(row aggregateRow) float64 { return row.IngestMedian }},
		{name: "search_qps.svg", title: "Median Search Throughput", unit: "queries/s", fn: func(row aggregateRow) float64 { return row.QPSMedian }},
	}
	for _, chart := range charts {
		if err := writeBarChart(filepath.Join(dir, chart.name), chart.title, chart.unit, rows, chart.fn); err != nil {
			return err
		}
	}
	if err := writeRecallScatter(filepath.Join(dir, "recall10_vs_search_qps.svg"), rows); err != nil {
		return err
	}
	return writeResourceChart(filepath.Join(dir, "resource_usage.svg"), rows)
}

func writeBarChart(path, title, unit string, rows []aggregateRow, valueFn func(aggregateRow) float64) error {
	width := maxInt(960, len(rows)*44+180)
	height := 560
	left := 86.0
	right := 34.0
	top := 58.0
	bottom := 170.0
	plotWidth := float64(width) - left - right
	plotHeight := float64(height) - top - bottom
	maxValue := maxRowValue(rows, valueFn)
	if maxValue <= 0 {
		maxValue = 1
	}
	barGap := 6.0
	barWidth := math.Max(10, (plotWidth/float64(maxInt(1, len(rows))))-barGap)

	var b strings.Builder
	writeSVGHeader(&b, width, height, title)
	writeChartFrame(&b, width, height, left, top, plotWidth, plotHeight, title, unit, maxValue)
	for i, row := range rows {
		value := valueFn(row)
		x := left + float64(i)*(barWidth+barGap) + barGap/2
		barHeight := value / maxValue * plotHeight
		y := top + plotHeight - barHeight
		fmt.Fprintf(&b, "<rect x=\"%.1f\" y=\"%.1f\" width=\"%.1f\" height=\"%.1f\" fill=\"%s\"/>\n", x, y, barWidth, barHeight, colorForRow(row))
		fmt.Fprintf(&b, "<text x=\"%.1f\" y=\"%.1f\" font-size=\"10\" text-anchor=\"end\" transform=\"rotate(-55 %.1f %.1f)\">%s</text>\n", x+barWidth/2, top+plotHeight+18, x+barWidth/2, top+plotHeight+18, html.EscapeString(rowChartLabel(row)))
	}
	b.WriteString("</svg>\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeRecallScatter(path string, rows []aggregateRow) error {
	width := 960
	height := 560
	left := 86.0
	right := 40.0
	top := 58.0
	bottom := 86.0
	plotWidth := float64(width) - left - right
	plotHeight := float64(height) - top - bottom
	maxQPS := maxRowValue(rows, func(row aggregateRow) float64 { return row.QPSMedian })
	if maxQPS <= 0 {
		maxQPS = 1
	}

	var b strings.Builder
	writeSVGHeader(&b, width, height, "Recall@10 vs Search QPS")
	writeChartFrame(&b, width, height, left, top, plotWidth, plotHeight, "Recall@10 vs Search QPS", "recall@10", 1)
	fmt.Fprintf(&b, "<text x=\"%.1f\" y=\"%.1f\" font-size=\"12\" text-anchor=\"middle\">search QPS</text>\n", left+plotWidth/2, float64(height)-22)
	for _, row := range rows {
		x := left + (row.QPSMedian/maxQPS)*plotWidth
		y := top + plotHeight - clampFloat(row.Recall10Median, 0, 1)*plotHeight
		fmt.Fprintf(&b, "<circle cx=\"%.1f\" cy=\"%.1f\" r=\"5\" fill=\"%s\"/>\n", x, y, colorForRow(row))
		fmt.Fprintf(&b, "<text x=\"%.1f\" y=\"%.1f\" font-size=\"10\">%s</text>\n", x+7, y-7, html.EscapeString(rowChartLabel(row)))
	}
	fmt.Fprintf(&b, "<text x=\"%.1f\" y=\"%.1f\" font-size=\"11\" text-anchor=\"middle\">0</text>\n", left, top+plotHeight+16)
	fmt.Fprintf(&b, "<text x=\"%.1f\" y=\"%.1f\" font-size=\"11\" text-anchor=\"middle\">%.0f</text>\n", left+plotWidth, top+plotHeight+16, maxQPS)
	b.WriteString("</svg>\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeResourceChart(path string, rows []aggregateRow) error {
	width := maxInt(960, len(rows)*54+180)
	height := 560
	left := 86.0
	right := 34.0
	top := 58.0
	bottom := 170.0
	plotWidth := float64(width) - left - right
	plotHeight := float64(height) - top - bottom
	maxValue := maxRowValue(rows, func(row aggregateRow) float64 { return math.Max(row.MemoryMedian, row.DiskMedian) })
	if maxValue <= 0 {
		maxValue = 1
	}
	groupGap := 8.0
	groupWidth := math.Max(20, (plotWidth/float64(maxInt(1, len(rows))))-groupGap)
	barWidth := groupWidth / 2

	var b strings.Builder
	writeSVGHeader(&b, width, height, "Median Resource Usage")
	writeChartFrame(&b, width, height, left, top, plotWidth, plotHeight, "Median Resource Usage", "MiB", maxValue)
	for i, row := range rows {
		x := left + float64(i)*(groupWidth+groupGap) + groupGap/2
		memHeight := row.MemoryMedian / maxValue * plotHeight
		diskHeight := row.DiskMedian / maxValue * plotHeight
		fmt.Fprintf(&b, "<rect x=\"%.1f\" y=\"%.1f\" width=\"%.1f\" height=\"%.1f\" fill=\"#2563eb\"/>\n", x, top+plotHeight-memHeight, barWidth, memHeight)
		fmt.Fprintf(&b, "<rect x=\"%.1f\" y=\"%.1f\" width=\"%.1f\" height=\"%.1f\" fill=\"#16a34a\"/>\n", x+barWidth, top+plotHeight-diskHeight, barWidth, diskHeight)
		fmt.Fprintf(&b, "<text x=\"%.1f\" y=\"%.1f\" font-size=\"10\" text-anchor=\"end\" transform=\"rotate(-55 %.1f %.1f)\">%s</text>\n", x+groupWidth/2, top+plotHeight+18, x+groupWidth/2, top+plotHeight+18, html.EscapeString(rowChartLabel(row)))
	}
	fmt.Fprintf(&b, "<rect x=\"%.1f\" y=\"20\" width=\"12\" height=\"12\" fill=\"#2563eb\"/><text x=\"%.1f\" y=\"31\" font-size=\"12\">memory</text>\n", float64(width)-168, float64(width)-150)
	fmt.Fprintf(&b, "<rect x=\"%.1f\" y=\"20\" width=\"12\" height=\"12\" fill=\"#16a34a\"/><text x=\"%.1f\" y=\"31\" font-size=\"12\">disk</text>\n", float64(width)-90, float64(width)-72)
	b.WriteString("</svg>\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeSVGHeader(b *strings.Builder, width, height int, title string) {
	fmt.Fprintf(b, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%d\" height=\"%d\" viewBox=\"0 0 %d %d\" role=\"img\" aria-label=\"%s\">\n", width, height, width, height, html.EscapeString(title))
	b.WriteString("<rect width=\"100%\" height=\"100%\" fill=\"#ffffff\"/>\n")
}

func writeChartFrame(b *strings.Builder, width, height int, left, top, plotWidth, plotHeight float64, title, unit string, maxValue float64) {
	fmt.Fprintf(b, "<text x=\"%.1f\" y=\"34\" font-size=\"20\" font-family=\"Arial, sans-serif\" font-weight=\"700\">%s</text>\n", left, html.EscapeString(title))
	fmt.Fprintf(b, "<line x1=\"%.1f\" y1=\"%.1f\" x2=\"%.1f\" y2=\"%.1f\" stroke=\"#111827\" stroke-width=\"1\"/>\n", left, top+plotHeight, left+plotWidth, top+plotHeight)
	fmt.Fprintf(b, "<line x1=\"%.1f\" y1=\"%.1f\" x2=\"%.1f\" y2=\"%.1f\" stroke=\"#111827\" stroke-width=\"1\"/>\n", left, top, left, top+plotHeight)
	for i := 0; i <= 4; i++ {
		ratio := float64(i) / 4
		y := top + plotHeight - ratio*plotHeight
		value := ratio * maxValue
		fmt.Fprintf(b, "<line x1=\"%.1f\" y1=\"%.1f\" x2=\"%.1f\" y2=\"%.1f\" stroke=\"#e5e7eb\"/>\n", left, y, left+plotWidth, y)
		fmt.Fprintf(b, "<text x=\"%.1f\" y=\"%.1f\" font-size=\"11\" text-anchor=\"end\">%.1f</text>\n", left-8, y+4, value)
	}
	fmt.Fprintf(b, "<text x=\"20\" y=\"%.1f\" font-size=\"12\" transform=\"rotate(-90 20 %.1f)\">%s</text>\n", top+plotHeight/2, top+plotHeight/2, html.EscapeString(unit))
}

func maxRowValue(rows []aggregateRow, fn func(aggregateRow) float64) float64 {
	var maxValue float64
	for _, row := range rows {
		if value := fn(row); value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}

func rowChartLabel(row aggregateRow) string {
	profile := row.Profile
	if profile == "" {
		profile = "default"
	}
	return fmt.Sprintf("%s %s b%d", row.Engine, profile, row.BatchSize)
}

func colorForRow(row aggregateRow) string {
	switch row.Engine {
	case "LumenVec":
		return "#2563eb"
	case "Qdrant":
		return "#dc2626"
	case "pgvector":
		return "#7c3aed"
	case "Chroma":
		return "#16a34a"
	default:
		return "#64748b"
	}
}

func formatFloat(value float64, decimals int) string {
	return strconv.FormatFloat(value, 'f', decimals, 64)
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func medianFrom(results []schema.ResultFile, fn func(schema.ResultFile) float64) float64 {
	values := make([]float64, 0, len(results))
	for _, result := range results {
		values = append(values, fn(result))
	}
	return median(values)
}

func medianPtrFrom(results []schema.ResultFile, fn func(schema.ResultFile) *float64) float64 {
	values := make([]float64, 0, len(results))
	for _, result := range results {
		value := fn(result)
		if value != nil {
			values = append(values, *value)
		}
	}
	return median(values)
}

func supportedBatchValue(result schema.ResultFile, fn func(schema.ResultFile) float64) float64 {
	if !result.BatchSearch.Supported {
		return 0
	}
	return fn(result)
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}

func displayEngine(name string) string {
	if name == "lumenvec" {
		return "LumenVec"
	}
	if name == "qdrant" {
		return "Qdrant"
	}
	if name == "chroma" {
		return "Chroma"
	}
	if name == "pgvector" {
		return "pgvector"
	}
	return name
}

func bytesToMiB(value uint64) float64 {
	return float64(value) / 1024 / 1024
}
