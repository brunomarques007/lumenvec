param(
    [string]$BaselineDir = "benchmarks/baselines/matrix-10k-128-c4-k10",
    [string]$OutputDir = "benchmarks/results/pre-pr-regression-gate",
    [string]$Engines = "lumenvec-http-exact,lumenvec-http-ann-quality,lumenvec-grpc-exact,lumenvec-grpc-ann-quality",
    [string]$BatchSizes = "1000",
    [int]$Runs = 1,
    [int]$Vectors = 10000,
    [int]$Dim = 128,
    [int]$Queries = 500,
    [int]$Warmup = 100,
    [int]$Concurrency = 4,
    [int]$SearchBatchSize = 100,
    [int]$K = 10,
    [switch]$SkipCompare
)

$ErrorActionPreference = "Stop"

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw "docker is required for the benchmark regression gate"
}

$compareArgs = @()
if (-not $SkipCompare) {
    if (-not (Test-Path $BaselineDir)) {
        throw "baseline directory not found: $BaselineDir. Use -SkipCompare for a smoke run without regression comparison."
    }
    $compareArgs = @("--compare-dir", $BaselineDir)
}

go run ./benchmarks/runner/cmd/matrix `
    --runs $Runs `
    --engines $Engines `
    --vectors $Vectors `
    --dim $Dim `
    --queries $Queries `
    --warmup $Warmup `
    --concurrency $Concurrency `
    --search-batch-size $SearchBatchSize `
    --k $K `
    --batch-sizes $BatchSizes `
    --output-dir $OutputDir `
    @compareArgs

$comparisonPath = Join-Path $OutputDir "comparison.csv"
if (-not $SkipCompare -and (Test-Path $comparisonPath)) {
    $rows = Import-Csv $comparisonPath
    $regressions = @($rows | Where-Object { $_.status -eq "regression" })
    if ($regressions.Count -gt 0) {
        $regressions | Format-Table status, engine, profile, transport, ingest_batch, search_batch, notes -AutoSize | Out-String | Write-Host
        throw "benchmark regression gate failed with $($regressions.Count) regression row(s)"
    }
}

Write-Host "benchmark regression gate passed"
