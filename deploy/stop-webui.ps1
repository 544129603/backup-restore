$ErrorActionPreference = "Stop"
$pidFile = Join-Path $env:TEMP "backup-restore-webui\port-forward.pid"
if (-not (Test-Path $pidFile)) {
    Write-Host "Web UI port-forward is not running."
    exit 0
}

$processId = [int](Get-Content $pidFile -Raw)
$process = Get-CimInstance Win32_Process -Filter "ProcessId = $processId" -ErrorAction SilentlyContinue
$isExpectedProcess = $process -and $process.Name -eq "kubectl.exe" -and `
    $process.CommandLine -like "*port-forward*backup-restore-operator-webui*"
if ($isExpectedProcess) {
    Stop-Process -Id $processId -Force
    Write-Host "Stopped Web UI port-forward (PID $processId)."
} elseif (-not $process) {
    Write-Host "The recorded Web UI port-forward process is no longer running."
} else {
    Write-Warning "PID $processId belongs to another process and was not stopped."
}
Remove-Item $pidFile -Force
