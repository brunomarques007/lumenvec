package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseScenariosUsesDefaultSearchBatchSize(t *testing.T) {
	scenarios, err := parseScenarios("500,100,500", 25, "")
	if err != nil {
		t.Fatalf("parseScenarios returned error: %v", err)
	}
	want := []scenario{
		{batchSize: 100, searchBatchSize: 25},
		{batchSize: 500, searchBatchSize: 25},
	}
	if len(scenarios) != len(want) {
		t.Fatalf("scenario count = %d, want %d", len(scenarios), len(want))
	}
	for i := range want {
		if scenarios[i] != want[i] {
			t.Fatalf("scenario[%d] = %+v, want %+v", i, scenarios[i], want[i])
		}
	}
}

func TestParseScenariosExpandsSearchBatchSizes(t *testing.T) {
	scenarios, err := parseScenarios("100,500", 100, "50,10,50")
	if err != nil {
		t.Fatalf("parseScenarios returned error: %v", err)
	}
	want := []scenario{
		{batchSize: 100, searchBatchSize: 10},
		{batchSize: 100, searchBatchSize: 50},
		{batchSize: 500, searchBatchSize: 10},
		{batchSize: 500, searchBatchSize: 50},
	}
	if len(scenarios) != len(want) {
		t.Fatalf("scenario count = %d, want %d", len(scenarios), len(want))
	}
	for i := range want {
		if scenarios[i] != want[i] {
			t.Fatalf("scenario[%d] = %+v, want %+v", i, scenarios[i], want[i])
		}
	}
}

func TestParseScenariosRejectsInvalidValues(t *testing.T) {
	if _, err := parseScenarios("100,0", 100, ""); err == nil {
		t.Fatal("expected invalid ingest batch size error")
	}
	if _, err := parseScenarios("100", 100, "25,-1"); err == nil {
		t.Fatal("expected invalid search batch size error")
	}
}

func TestFilterEnginesKeepsRequestedOrderAndDeduplicates(t *testing.T) {
	engines, err := filterEngines(allEngineCases(), "weaviate,pgvector,lumenvec-http-exact,pgvector")
	if err != nil {
		t.Fatalf("filterEngines returned error: %v", err)
	}
	want := []string{"weaviate", "pgvector", "lumenvec-http-exact"}
	if len(engines) != len(want) {
		t.Fatalf("engine count = %d, want %d", len(engines), len(want))
	}
	for i := range want {
		if engines[i].name != want[i] {
			t.Fatalf("engine[%d] = %q, want %q", i, engines[i].name, want[i])
		}
	}
}

func TestFilterEnginesRejectsUnknownEngine(t *testing.T) {
	if _, err := filterEngines(allEngineCases(), "missing"); err == nil {
		t.Fatal("expected unknown engine error")
	}
}

func TestResultFilesFromDirFindsOnlyJSON(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.json", "a.json", "report.md", "aggregate.csv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "charts"), 0o755); err != nil {
		t.Fatalf("mkdir charts: %v", err)
	}
	files, err := resultFilesFromDir(dir)
	if err != nil {
		t.Fatalf("resultFilesFromDir returned error: %v", err)
	}
	want := []string{filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")}
	if len(files) != len(want) {
		t.Fatalf("file count = %d, want %d: %v", len(files), len(want), files)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Fatalf("files[%d] = %q, want %q", i, files[i], want[i])
		}
	}
}

func TestWriteOutputsCreatesCSVReportAndCharts(t *testing.T) {
	dir := t.TempDir()
	cfg := config{
		runs:            3,
		vectors:         10000,
		dim:             128,
		queries:         500,
		warmup:          100,
		concurrent:      4,
		searchBatchSize: 100,
		k:               10,
		batchSizes:      "100,500",
		outputDir:       dir,
	}
	rows := []aggregateRow{
		{
			Engine:           "LumenVec",
			Profile:          "exact",
			Transport:        "http",
			BatchSize:        100,
			SearchBatchSize:  100,
			Runs:             3,
			IngestMedian:     1600.12,
			IndexBuildMedian: 0,
			QPSMedian:        1200.34,
			BatchQPSMedian:   1800.45,
			BatchP95Median:   80.1,
			P50Median:        3.2,
			P95Median:        4.6,
			P99Median:        6.1,
			Recall1Median:    1,
			Recall5Median:    1,
			Recall10Median:   1,
			MemoryMedian:     50.5,
			CPUMedian:        120.25,
			DiskMedian:       24.2,
		},
		{
			Engine:          "Chroma",
			Profile:         "default",
			Transport:       "rest",
			BatchSize:       100,
			SearchBatchSize: 100,
			Runs:            3,
			IngestMedian:    3000,
			QPSMedian:       450,
			BatchQPSMedian:  1700,
			BatchP95Median:  70,
			P50Median:       8,
			P95Median:       11,
			P99Median:       14,
			Recall1Median:   0.88,
			Recall5Median:   0.84,
			Recall10Median:  0.82,
			MemoryMedian:    40,
			CPUMedian:       150,
			DiskMedian:      11,
		},
	}

	if err := writeCSV(cfg, rows); err != nil {
		t.Fatalf("writeCSV returned error: %v", err)
	}
	if err := writeCharts(cfg, rows); err != nil {
		t.Fatalf("writeCharts returned error: %v", err)
	}
	if err := writeReport(cfg, rows); err != nil {
		t.Fatalf("writeReport returned error: %v", err)
	}

	csvData := readTestFile(t, filepath.Join(dir, "aggregate.csv"))
	if !strings.Contains(csvData, "median_ingest_vectors_per_second") {
		t.Fatalf("aggregate.csv missing header: %s", csvData)
	}
	if !strings.Contains(csvData, "LumenVec,exact,http,100,100,3,1600.12") {
		t.Fatalf("aggregate.csv missing LumenVec row: %s", csvData)
	}

	report := readTestFile(t, filepath.Join(dir, "report.md"))
	if !strings.Contains(report, "![Ingest throughput](charts/ingest_vectors_per_second.svg)") {
		t.Fatalf("report missing chart link: %s", report)
	}
	if !strings.Contains(report, "## Rankings") {
		t.Fatalf("report missing rankings: %s", report)
	}
	for _, path := range []string{
		"charts/ingest_vectors_per_second.svg",
		"charts/search_qps.svg",
		"charts/recall10_vs_search_qps.svg",
		"charts/resource_usage.svg",
	} {
		content := readTestFile(t, filepath.Join(dir, filepath.FromSlash(path)))
		if !strings.Contains(content, "<svg") {
			t.Fatalf("%s is not an SVG: %s", path, content)
		}
	}
}

func TestTopRowsFiltersRecallAndSorts(t *testing.T) {
	rows := []aggregateRow{
		{Engine: "LumenVec", Profile: "ann-fast", BatchSize: 100, SearchBatchSize: 100, QPSMedian: 5000, Recall10Median: 0.2},
		{Engine: "LumenVec", Profile: "ann-quality", BatchSize: 100, SearchBatchSize: 100, QPSMedian: 2500, Recall10Median: 0.76},
		{Engine: "LumenVec", Profile: "exact", BatchSize: 100, SearchBatchSize: 100, QPSMedian: 1200, Recall10Median: 1.0},
	}
	top := topRows(rows, 2, func(row aggregateRow) bool {
		return row.Recall10Median >= 0.75
	}, func(row aggregateRow) float64 {
		return row.QPSMedian
	}, true)
	if len(top) != 2 {
		t.Fatalf("top row count = %d, want 2", len(top))
	}
	if top[0].Profile != "ann-quality" {
		t.Fatalf("top[0] profile = %q, want ann-quality", top[0].Profile)
	}
	if top[1].Profile != "exact" {
		t.Fatalf("top[1] profile = %q, want exact", top[1].Profile)
	}
}

func TestCompareRowsFlagsSearchRegression(t *testing.T) {
	baseline := []aggregateRow{{
		Engine:          "LumenVec",
		Profile:         "exact",
		Transport:       "http",
		BatchSize:       100,
		SearchBatchSize: 100,
		IngestMedian:    1000,
		QPSMedian:       1000,
		BatchQPSMedian:  1500,
		P95Median:       5,
		P99Median:       8,
		Recall10Median:  1,
		MemoryMedian:    50,
		DiskMedian:      20,
	}}
	candidate := []aggregateRow{{
		Engine:          "LumenVec",
		Profile:         "exact",
		Transport:       "http",
		BatchSize:       100,
		SearchBatchSize: 100,
		IngestMedian:    1200,
		QPSMedian:       930,
		BatchQPSMedian:  1400,
		P95Median:       5.4,
		P99Median:       7,
		Recall10Median:  1,
		MemoryMedian:    55,
		DiskMedian:      20,
	}}
	rows := compareRows(baseline, candidate)
	if len(rows) != 1 {
		t.Fatalf("comparison row count = %d, want 1", len(rows))
	}
	if rows[0].Status != "regression" {
		t.Fatalf("status = %q, want regression", rows[0].Status)
	}
	if !strings.Contains(rows[0].Notes, "search QPS below -5%") {
		t.Fatalf("notes missing QPS regression: %q", rows[0].Notes)
	}
	if rows[0].IngestDeltaPct <= 0 {
		t.Fatalf("expected positive ingest delta, got %.2f", rows[0].IngestDeltaPct)
	}
}

func TestWriteComparisonOutputs(t *testing.T) {
	dir := t.TempDir()
	cfg := config{outputDir: dir, compareDir: filepath.Join(dir, "baseline")}
	rows := []comparisonRow{{
		Status:          "regression",
		Engine:          "LumenVec",
		Profile:         "exact",
		Transport:       "http",
		BatchSize:       100,
		SearchBatchSize: 100,
		IngestDeltaPct:  10,
		QPSDeltaPct:     -6,
		P95DeltaPct:     7,
		Recall10Delta:   0,
		Notes:           "search QPS below -5%",
	}}
	if err := writeComparisonCSV(cfg, rows); err != nil {
		t.Fatalf("writeComparisonCSV returned error: %v", err)
	}
	if err := writeComparisonReport(cfg, rows); err != nil {
		t.Fatalf("writeComparisonReport returned error: %v", err)
	}
	csvData := readTestFile(t, filepath.Join(dir, "comparison.csv"))
	if !strings.Contains(csvData, "search_qps_delta_pct") {
		t.Fatalf("comparison.csv missing header: %s", csvData)
	}
	report := readTestFile(t, filepath.Join(dir, "comparison.md"))
	if !strings.Contains(report, "Regressions: `1`") {
		t.Fatalf("comparison.md missing summary: %s", report)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
