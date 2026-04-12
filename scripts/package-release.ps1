param(
    [string]$Version = "",
    [string]$Goos = "",
    [string]$Goarch = "",
    [string]$DistDir = "dist/release"
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

if ([string]::IsNullOrWhiteSpace($Version)) {
    $latestTag = git describe --tags --match "v*" --abbrev=0 2>$null
    if ($null -ne $latestTag) {
        $latestTag = $latestTag.Trim()
    }
    if ([string]::IsNullOrWhiteSpace($latestTag)) {
        $Version = "dev"
    } else {
        $Version = $latestTag
    }
}
if ([string]::IsNullOrWhiteSpace($Goos)) {
    $Goos = (go env GOOS).Trim()
}
if ([string]::IsNullOrWhiteSpace($Goarch)) {
    $Goarch = (go env GOARCH).Trim()
}

New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

function Build-Bundle {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Transport,
        [Parameter(Mandatory = $true)]
        [string]$ConfigFile
    )

    $bundleName = "lumenvec-$Version-$Goos-$Goarch-$Transport"
    $bundleDir = Join-Path $DistDir $bundleName
    if (Test-Path $bundleDir) {
        Remove-Item -Recurse -Force $bundleDir
    }
    New-Item -ItemType Directory -Force -Path $bundleDir | Out-Null

    $binaryName = "lumenvec"
    if ($Goos -eq "windows") {
        $binaryName = "lumenvec.exe"
    }

    Write-Host "Building $Transport bundle for $Goos/$Goarch..."
    $env:CGO_ENABLED = "0"
    $env:GOOS = $Goos
    $env:GOARCH = $Goarch
    go build -o (Join-Path $bundleDir $binaryName) ./cmd/server

    Copy-Item $ConfigFile (Join-Path $bundleDir "config.yaml")
    Copy-Item README.md (Join-Path $bundleDir "README.md")
    Copy-Item LICENSE (Join-Path $bundleDir "LICENSE")
    Copy-Item CHANGELOG.md (Join-Path $bundleDir "CHANGELOG.md")
    @(
        "LumenVec release bundle"
        "Version: $Version"
        "Platform: $Goos/$Goarch"
        "Transport: $Transport"
        "Binary: $binaryName"
        "Config file: config.yaml"
    ) | Set-Content (Join-Path $bundleDir "BUILD_INFO.txt")

    Compress-Archive -Path $bundleDir -DestinationPath (Join-Path $DistDir "$bundleName.zip") -Force
}

Build-Bundle -Transport "http" -ConfigFile "configs/config.yaml"
Build-Bundle -Transport "grpc" -ConfigFile "configs/config.grpc.yaml"

Write-Host "Release artifacts written to $DistDir"
