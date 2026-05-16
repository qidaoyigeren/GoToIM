# GoIM Benchmark Runner (PowerShell)
# Usage: .\run.ps1 [-CometTcp host:port] [-LogicHttp host:port] [-Duration duration]

param(
    [string]$CometTcp = "localhost:3101",
    [string]$LogicHttp = "localhost:3111",
    [string]$Duration = "60s"
)

$ErrorActionPreference = "Stop"

$CometWs = "$($CometTcp.Split(':')[0]):3101"
$Timestamp = Get-Date -Format "yyyyMMdd_HHmmss"
$OutputDir = ".\benchmarks\results\$Timestamp"

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

Write-Host "============================================"
Write-Host "  GoIM Benchmark Suite"
Write-Host "============================================"
Write-Host "Comet TCP:  $CometTcp"
Write-Host "Comet WS:   $CometWs"
Write-Host "Logic HTTP: $LogicHttp"
Write-Host "Duration:   $Duration"
Write-Host "Output:     $OutputDir"
Write-Host ""

# Phase 1: Connection benchmarks
foreach ($Count in @(1000, 5000, 10000)) {
    Write-Host "=== Connection Benchmark: $Count connections ==="
    go run .\benchmarks\conn_bench `
        -host="$CometTcp" `
        -count=$Count `
        -ramp=30s `
        -duration="$Duration" `
        -output="$OutputDir\conn_$Count.json"
    Write-Host ""
}

# Phase 2: Push benchmarks
foreach ($Msgs in @(10000, 50000, 100000)) {
    Write-Host "=== Push Benchmark: $Msgs messages ==="
    go run .\benchmarks\push_bench `
        -logic-host="$LogicHttp" `
        -comet-host="$CometWs" `
        -receivers=100 `
        -rate=1000 `
        -duration="$Duration" `
        -output="$OutputDir\push_$Msgs.json"
    Write-Host ""
}

# Phase 3: Generate report
Write-Host "=== Generating HTML Report ==="
go run .\benchmarks\report_gen `
    -conn="$OutputDir\conn_1000.json" `
    -push="$OutputDir\push_10000.json" `
    -output="$OutputDir\report.html"

Write-Host ""
Write-Host "============================================"
Write-Host "  Benchmark Complete!"
Write-Host "  Results: $OutputDir\"
Write-Host "  Report:  $OutputDir\report.html"
Write-Host "============================================"
