param(
    [int]$Port = 8082
)

$ErrorActionPreference = "Stop"
$stateDir = Join-Path $env:TEMP "backup-restore-webui"
$pidFile = Join-Path $stateDir "port-forward.pid"
New-Item -ItemType Directory -Path $stateDir -Force | Out-Null

if (Test-Path $pidFile) {
    $existingPid = [int](Get-Content $pidFile -Raw)
    $existing = Get-CimInstance Win32_Process -Filter "ProcessId = $existingPid" -ErrorAction SilentlyContinue
    $isExpectedProcess = $existing -and $existing.Name -eq "kubectl.exe" -and `
        $existing.CommandLine -like "*port-forward*backup-restore-operator-webui*"
    if ($isExpectedProcess) {
        $listener = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | `
            Where-Object { $_.OwningProcess -eq $existingPid }
        if ($listener) {
            Write-Host "Web UI port-forward is already running (PID $existingPid)."
            Write-Host "Open http://localhost:$Port"
            exit 0
        }
        Stop-Process -Id $existingPid -Force
    }
    Remove-Item $pidFile -Force
}

$kubectl = (Get-Command kubectl -ErrorAction Stop).Source
$arguments = @(
    "-n", "backup-system",
    "port-forward", "service/backup-restore-operator-webui",
    "${Port}:80", "--address", "127.0.0.1"
)
$process = Start-Process -FilePath $kubectl -ArgumentList $arguments -WindowStyle Hidden -PassThru
$process.Id | Set-Content -Path $pidFile -Encoding ascii

$ready = $false
for ($attempt = 0; $attempt -lt 20; $attempt++) {
    Start-Sleep -Milliseconds 250
    if ($process.HasExited) { break }
    $listener = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | `
        Where-Object { $_.OwningProcess -eq $process.Id }
    if ($listener) {
        $ready = $true
        break
    }
}

if (-not $ready) {
    if (-not $process.HasExited) { Stop-Process -Id $process.Id -Force }
    Remove-Item $pidFile -Force -ErrorAction SilentlyContinue
    throw "Unable to start Web UI port-forward. Verify the Service and that local port $Port is available."
}

Write-Host "Web UI is ready: http://localhost:$Port"
Write-Host "Port-forward PID: $($process.Id)"
