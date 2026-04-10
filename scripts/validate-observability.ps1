param(
    [switch]$Start
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

function Invoke-JsonGet {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Url,
        [hashtable]$Headers
    )

    if ($Headers) {
        return Invoke-RestMethod -Uri $Url -Method Get -Headers $Headers
    }

    return Invoke-RestMethod -Uri $Url -Method Get
}

function Wait-Until {
    param(
        [Parameter(Mandatory = $true)]
        [scriptblock]$Condition,
        [Parameter(Mandatory = $true)]
        [string]$Description,
        [int]$Attempts = 20,
        [int]$DelaySeconds = 2
    )

    for ($i = 0; $i -lt $Attempts; $i++) {
        try {
            if (& $Condition) {
                return
            }
        } catch {
        }

        Start-Sleep -Seconds $DelaySeconds
    }

    throw "Timed out while waiting for $Description."
}

function Assert-HealthyTarget {
    param(
        [Parameter(Mandatory = $true)]
        [object]$TargetsResponse
    )

    $target = $TargetsResponse.data.activeTargets | Where-Object { $_.labels.job -eq "lumenvec" } | Select-Object -First 1
    if (-not $target) {
        throw "Prometheus target 'lumenvec' was not found."
    }

    if ($target.health -ne "up") {
        throw "Prometheus target 'lumenvec' is not healthy. Current state: $($target.health)"
    }
}

function Assert-DashboardProvisioned {
    param(
        [Parameter(Mandatory = $true)]
        [object]$SearchResponse
    )

    $dashboard = $SearchResponse | Where-Object { $_.uid -eq "lumenvec-overview" } | Select-Object -First 1
    if (-not $dashboard) {
        throw "Grafana dashboard 'lumenvec-overview' was not provisioned."
    }
}

if ($Start) {
    Write-Host "Starting docker compose stack..."
    docker compose up --build -d | Out-Host
}

Write-Host "Checking Docker Compose services..."
$composePs = docker compose ps --format json | ConvertFrom-Json
if (-not $composePs) {
    throw "No Docker Compose services are running."
}

$requiredServices = @("lumenvec", "prometheus", "grafana")
foreach ($service in $requiredServices) {
    $entry = $composePs | Where-Object { $_.Service -eq $service } | Select-Object -First 1
    if (-not $entry) {
        throw "Service '$service' is missing from docker compose."
    }

    if ($entry.State -ne "running") {
        throw "Service '$service' is not running. Current state: $($entry.State)"
    }
}

Write-Host "Checking LumenVec health..."
$health = $null
Wait-Until -Description "LumenVec health endpoint" -Condition {
    $script:health = Invoke-WebRequest -Uri "http://localhost:19190/health" -UseBasicParsing
    return $script:health.Content.Trim() -eq "ok"
}
if ($health.Content.Trim() -ne "ok") {
    throw "Unexpected LumenVec /health response: $($health.Content)"
}

Write-Host "Checking LumenVec metrics..."
$metrics = Invoke-WebRequest -Uri "http://localhost:19190/metrics" -UseBasicParsing
if ($metrics.Content -notmatch "lumenvec_core_ann_config_info") {
    throw "LumenVec metrics are missing 'lumenvec_core_ann_config_info'."
}

Write-Host "Checking Prometheus health and target scraping..."
$promHealthy = $null
Wait-Until -Description "Prometheus health endpoint" -Condition {
    $script:promHealthy = Invoke-WebRequest -Uri "http://localhost:9090/-/healthy" -UseBasicParsing
    return $script:promHealthy.Content.Trim() -match "Healthy"
}
if ($promHealthy.Content.Trim() -notmatch "Healthy") {
    throw "Unexpected Prometheus /-/healthy response: $($promHealthy.Content)"
}

$targets = $null
Wait-Until -Description "Prometheus scraping the lumenvec target" -Condition {
    $script:targets = Invoke-JsonGet -Url "http://localhost:9090/api/v1/targets"
    $target = $script:targets.data.activeTargets | Where-Object { $_.labels.job -eq "lumenvec" } | Select-Object -First 1
    return $null -ne $target -and $target.health -eq "up"
}
Assert-HealthyTarget -TargetsResponse $targets

Write-Host "Checking Grafana health and dashboard provisioning..."
$grafanaAuth = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes("admin:admin"))
$grafanaHeaders = @{ Authorization = "Basic $grafanaAuth" }
$grafanaHealth = $null
Wait-Until -Description "Grafana health endpoint" -Condition {
    $script:grafanaHealth = Invoke-JsonGet -Url "http://localhost:3000/api/health" -Headers $grafanaHeaders
    return $script:grafanaHealth.database -eq "ok"
}
if ($grafanaHealth.database -ne "ok") {
    throw "Grafana database health is not ok."
}

$grafanaSearch = $null
Wait-Until -Description "Grafana dashboard provisioning" -Condition {
    $script:grafanaSearch = Invoke-JsonGet -Url "http://localhost:3000/api/search?query=LumenVec" -Headers $grafanaHeaders
    return $null -ne ($script:grafanaSearch | Where-Object { $_.uid -eq "lumenvec-overview" } | Select-Object -First 1)
}
Assert-DashboardProvisioned -SearchResponse $grafanaSearch

Write-Host "Observability stack validation passed."
