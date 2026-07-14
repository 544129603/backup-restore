param(
    [string]$Image = "backup-restore-operator:dev",
    [string]$ClusterRef = "e2e-cluster",
    [string]$Namespace = "backup-system",
    [string]$Release = "backup-restore-e2e",
    [switch]$DisableWebhook,
    [switch]$Keep
)

$ErrorActionPreference = "Stop"
$chart = Join-Path $PSScriptRoot "..\..\charts\backup-restore-operator"
$crds = @(
    "backuprepositories.protection.platform.io",
    "backupscopes.protection.platform.io",
    "backuppolicies.protection.platform.io",
    "backuptasks.protection.platform.io",
    "backuprecords.protection.platform.io",
    "restoretasks.protection.platform.io",
    "backuppluginconfigs.protection.platform.io"
)

function Invoke-Native {
    param([scriptblock]$Command, [string]$Description)
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE"
    }
}

if ($Image -notmatch "^(?<repository>.+):(?<tag>[^/:]+)$") {
    throw "-Image must include a tag, for example registry.example.com/platform/backup-restore-operator:e2e"
}
$repository = $Matches.repository
$tag = $Matches.tag

Invoke-Native { kubectl cluster-info | Out-Host } "Kubernetes cluster connectivity check"
Invoke-Native { helm lint $chart | Out-Host } "Helm lint"

$existingCRD = kubectl get crd $crds[0] --ignore-not-found -o name
if ($LASTEXITCODE -ne 0) {
    throw "Unable to check for an existing installation"
}
if ($existingCRD) {
    throw "An existing backup/restore installation was detected. This E2E script installs and removes cluster-scoped resources, so it only runs on a disposable cluster."
}

if (-not $DisableWebhook) {
    $certManagerCRD = kubectl get crd certificates.cert-manager.io --ignore-not-found -o name
    if ($LASTEXITCODE -ne 0 -or -not $certManagerCRD) {
        throw "cert-manager is required for the webhook E2E path; install it or pass -DisableWebhook"
    }
}

$installed = $false
try {
    $webhook = if ($DisableWebhook) { "false" } else { "true" }
    Invoke-Native {
        helm upgrade --install $Release $chart `
            --namespace $Namespace --create-namespace `
            --set-string image.repository=$repository `
            --set-string image.tag=$tag `
            --set-string clusterRef=$ClusterRef `
            --set webhook.enabled=$webhook `
            --wait --timeout 5m | Out-Host
    } "Operator installation"
    $installed = $true

    Invoke-Native {
        kubectl rollout status deployment/backup-restore-operator -n $Namespace --timeout=180s | Out-Host
    } "Operator rollout"

    foreach ($crd in $crds) {
        $scope = kubectl get crd $crd -o jsonpath="{.spec.scope}"
        if ($LASTEXITCODE -ne 0 -or $scope -ne "Cluster") {
            throw "CRD $crd is missing or is not Cluster-scoped"
        }
    }

    Invoke-Native {
        kubectl wait backuppluginconfig/cluster --for=jsonpath="{.status.phase}"=Ready --timeout=120s | Out-Host
    } "BackupPluginConfig readiness"

    if (-not $DisableWebhook) {
        $invalidRepository = @"
apiVersion: protection.platform.io/v1alpha1
kind: BackupRepository
metadata:
  name: e2e-invalid-repository
spec:
  clusterRef: $ClusterRef
  projectRef: _platform
  type: Local
  local: {}
"@
        $admissionOutput = $invalidRepository | kubectl create -f - 2>&1
        if ($LASTEXITCODE -eq 0) {
            kubectl delete backuprepository/e2e-invalid-repository --ignore-not-found | Out-Null
            throw "Admission webhook accepted an invalid Local repository"
        }
        Write-Host "Admission rejection verified: $admissionOutput"
    }

    Invoke-Native {
        kubectl get backuppluginconfig,backuprepository,backupscope,backuppolicy,backuptask,backuprecord,restoretask | Out-Host
    } "Cluster-scoped API smoke query"

    Write-Host "E2E smoke test passed."
}
finally {
    if ($installed -and -not $Keep) {
        helm uninstall $Release --namespace $Namespace --wait | Out-Host
        foreach ($crd in $crds) {
            kubectl delete crd $crd --ignore-not-found | Out-Host
        }
        kubectl delete namespace $Namespace --ignore-not-found --wait=false | Out-Host
    }
}
