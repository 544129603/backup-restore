param([string]$KubernetesVersion = "1.32.0")
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$bin = Join-Path $root "bin"
New-Item -ItemType Directory -Force $bin | Out-Null
if (-not $env:KUBEBUILDER_ASSETS) {
  if ($IsWindows -or $env:OS -eq "Windows_NT") {
    $cache = Join-Path $env:USERPROFILE ".cache\envtest\$KubernetesVersion"
    $assets = Join-Path $cache "controller-tools\envtest"
    $archive = Join-Path $cache "envtest.tar.gz"
    New-Item -ItemType Directory -Force $cache | Out-Null
    if (-not (Test-Path (Join-Path $assets "kube-apiserver.exe"))) {
      $url = "https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v$KubernetesVersion/envtest-v$KubernetesVersion-windows-amd64.tar.gz"
      Invoke-WebRequest -Uri $url -OutFile $archive -UseBasicParsing
      tar -xzf $archive -C $cache
    }
    $env:KUBEBUILDER_ASSETS = $assets
  } else {
    $tool = Join-Path $bin "setup-envtest"
    if (-not (Test-Path $tool)) {
      $env:GOBIN = $bin
      go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.20
      if ($LASTEXITCODE -ne 0) { throw "setup-envtest installation failed" }
    }
    $env:KUBEBUILDER_ASSETS = (& $tool use -p path $KubernetesVersion).Trim()
    if ($LASTEXITCODE -ne 0) { throw "envtest asset download failed" }
  }
}
$env:RUN_ENVTEST = "1"
try {
  go test ./test/integration -count=1 -v
  if ($LASTEXITCODE -ne 0) { throw "integration tests failed" }
} finally {
  # controller-runtime v0.20 cannot send POSIX SIGTERM to Windows processes.
  if ($IsWindows -or $env:OS -eq "Windows_NT") {
    Get-Process kube-apiserver,etcd -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
  }
}
