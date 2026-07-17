param(
    [string]$Version = "dev-local-12",
    [string]$Repository = "backup-restore-operator",
    [string]$Builder = "desktop-linux"
)

$ErrorActionPreference = "Stop"

$deployDir = $PSScriptRoot
$projectRoot = (Resolve-Path (Join-Path $deployDir "..")).Path
$artifacts = @(
    @{
        Platform = "linux/amd64"
        Suffix = "amd64"
        Tag = "${Repository}:${Version}-amd64"
        Path = Join-Path $deployDir "${Repository}-${Version}-linux-amd64.tar"
    },
    @{
        Platform = "linux/arm64"
        Suffix = "arm64"
        Tag = "${Repository}:${Version}-arm64"
        Path = Join-Path $deployDir "${Repository}-${Version}-linux-arm64.tar"
    }
)
$ociPath = Join-Path $deployDir "${Repository}-${Version}-multiarch.oci.tar"
$checksumPath = Join-Path $deployDir "${Repository}-${Version}-SHA256SUMS.txt"

docker buildx inspect $Builder *> $null
if ($LASTEXITCODE -ne 0) {
    throw "Docker Buildx builder '$Builder' is unavailable."
}

foreach ($artifact in $artifacts) {
    if (Test-Path -LiteralPath $artifact.Path) {
        Remove-Item -LiteralPath $artifact.Path -Force
    }

    Write-Host "Building $($artifact.Tag) for $($artifact.Platform)..."
    docker buildx build `
        --builder $Builder `
        --platform $artifact.Platform `
        --build-arg "VERSION=$Version" `
        --tag $artifact.Tag `
        --provenance=false `
        --sbom=false `
        --output "type=docker,dest=$($artifact.Path)" `
        $projectRoot
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build $($artifact.Platform) Docker archive."
    }
}

if (Test-Path -LiteralPath $ociPath) {
    Remove-Item -LiteralPath $ociPath -Force
}

Write-Host "Building combined multi-architecture OCI archive..."
docker buildx build `
    --builder $Builder `
    --platform "linux/amd64,linux/arm64" `
    --build-arg "VERSION=$Version" `
    --tag "${Repository}:${Version}" `
    --provenance=false `
    --sbom=false `
    --output "type=oci,dest=$ociPath" `
    $projectRoot
if ($LASTEXITCODE -ne 0) {
    throw "Failed to build the multi-architecture OCI archive."
}

$allPaths = @($artifacts.Path) + @($ociPath)
$hashLines = foreach ($path in $allPaths) {
    $hash = Get-FileHash -Algorithm SHA256 -LiteralPath $path
    "{0}  {1}" -f $hash.Hash.ToLowerInvariant(), (Split-Path -Leaf $path)
}
$hashLines | Set-Content -LiteralPath $checksumPath -Encoding ascii

Write-Host "Offline image artifacts:"
Get-Item -LiteralPath ($allPaths + @($checksumPath)) |
    Select-Object Name, Length, LastWriteTime |
    Format-Table -AutoSize
