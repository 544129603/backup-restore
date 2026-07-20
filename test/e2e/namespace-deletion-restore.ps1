param(
    [string]$ClusterRef = "docker-desktop",
    [string]$Repository = "docker-desktop-local",
    [string]$Prefix = "e2e-ns-delete",
    [switch]$EmptyNamespace,
    [switch]$Keep
)

$ErrorActionPreference = "Stop"
$runID = Get-Date -Format "yyyyMMddHHmmss"
$sourceNamespace = "$Prefix-$runID"
$policyName = "$Prefix-policy-$runID"
$backupTaskName = "$Prefix-backup-$runID"
$restoreTaskName = "$Prefix-restore-$runID"
$configMapName = "restore-marker"
$secretName = "restore-secret"
$marker = "namespace-deletion-$runID"
$secretValue = "secret-$marker"
$recordName = ""
$sourceNamespaceUID = ""

function Invoke-Native {
    param([scriptblock]$Command, [string]$Description)
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE"
    }
}

function Get-NativeValue {
    param([scriptblock]$Command, [string]$Description)
    $value = & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE"
    }
    return ($value | Out-String).Trim()
}

try {
    $repositoryJSON = Get-NativeValue { kubectl get backuprepository $Repository -o json } "Read BackupRepository"
    $repositoryObject = $repositoryJSON | ConvertFrom-Json
    if ($repositoryObject.spec.clusterRef -ne $ClusterRef) {
        throw "Repository $Repository belongs to clusterRef $($repositoryObject.spec.clusterRef), expected $ClusterRef"
    }
    if ($repositoryObject.status.phase -ne "Ready") {
        throw "Repository $Repository is not Ready: $($repositoryObject.status.phase)"
    }

    if ($EmptyNamespace) {
        $fixture = @"
apiVersion: v1
kind: Namespace
metadata:
  name: $sourceNamespace
  labels:
    protection.platform.io/e2e: empty-namespace-deletion
"@
        $resourceInclude = "secrets"
    }
    else {
        $fixture = @"
apiVersion: v1
kind: Namespace
metadata:
  name: $sourceNamespace
  labels:
    protection.platform.io/e2e: namespace-deletion
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: $configMapName
  namespace: $sourceNamespace
data:
  marker: $marker
---
apiVersion: v1
kind: Secret
metadata:
  name: $secretName
  namespace: $sourceNamespace
type: Opaque
stringData:
  payload: $secretValue
"@
        $resourceInclude = "configmaps, secrets"
    }
    Invoke-Native { $fixture | kubectl apply -f - | Out-Host } "Create source fixture"
    $sourceNamespaceUID = Get-NativeValue { kubectl get namespace $sourceNamespace -o jsonpath='{.metadata.uid}' } "Read source Namespace UID"

    $policy = @"
apiVersion: protection.platform.io/v1alpha1
kind: BackupPolicy
metadata:
  name: $policyName
spec:
  clusterRef: $ClusterRef
  repositoryRef: {name: $Repository}
  selection:
    mode: Namespace
    includeNamespaces: [$sourceNamespace]
    resources:
      include: [$resourceInclude]
    includeClusterResources: false
    includeSecrets: true
    includeCRDs: false
    includeCustomResources: false
    pvc:
      enabled: false
      failurePolicy: ContinueAndMarkPartial
      lifecycle: RetainAfterRecordDeletion
    consistencyMode: CrashConsistent
  schedule: {cron: "0 2 * * *", timezone: Etc/UTC}
  enabled: false
  retention: {maxCopies: 2, minCopies: 1, maxAgeDays: 7}
  timeout: 10m
"@
    Invoke-Native { $policy | kubectl apply -f - | Out-Host } "Create BackupPolicy"
    Invoke-Native { kubectl wait backuppolicy/$policyName --for=jsonpath='{.status.phase}'=Paused --timeout=120s | Out-Host } "Wait for BackupPolicy preview"

    $backupTask = @"
apiVersion: protection.platform.io/v1alpha1
kind: BackupTask
metadata:
  name: $backupTaskName
spec:
  clusterRef: $ClusterRef
  trigger: Manual
  source:
    type: Policy
    policyRef: {name: $policyName}
  idempotencyKey: $ClusterRef/$backupTaskName
"@
    Invoke-Native { $backupTask | kubectl apply -f - | Out-Host } "Create BackupTask"
    Invoke-Native { kubectl wait backuptask/$backupTaskName --for=jsonpath='{.status.phase}'=Completed --timeout=300s | Out-Host } "Wait for BackupTask Completed"

    $recordName = Get-NativeValue { kubectl get backuptask $backupTaskName -o jsonpath='{.status.recordRef.name}' } "Read generated BackupRecord"
    if (-not $recordName) {
        throw "BackupTask did not generate a BackupRecord"
    }
    Invoke-Native { kubectl wait backuprecord/$recordName --for=jsonpath='{.status.phase}'=Available --timeout=120s | Out-Host } "Wait for BackupRecord Available"

    $recordBeforeDeletion = (Get-NativeValue { kubectl get backuprecord $recordName -o json } "Read BackupRecord before Namespace deletion") | ConvertFrom-Json
    $recordResourceCount = if ($null -eq $recordBeforeDeletion.spec.inventory.resourceCount) { 0 } else { [int64]$recordBeforeDeletion.spec.inventory.resourceCount }
    if ($recordBeforeDeletion.spec.source.namespaces -notcontains $sourceNamespace) {
        throw "BackupRecord source does not contain $sourceNamespace"
    }
    if ($EmptyNamespace -and $recordResourceCount -ne 0) {
        throw "Empty Namespace backup unexpectedly contains $recordResourceCount resources"
    }

    Invoke-Native { kubectl delete namespace $sourceNamespace --wait=true --timeout=120s | Out-Host } "Delete source Namespace"
    $deletedNamespace = kubectl get namespace $sourceNamespace --ignore-not-found -o name
    if ($LASTEXITCODE -ne 0 -or $deletedNamespace) {
        throw "Source Namespace still exists after deletion"
    }
    $recordPhaseAfterDeletion = Get-NativeValue { kubectl get backuprecord $recordName -o jsonpath='{.status.phase}' } "Verify BackupRecord after Namespace deletion"
    if ($recordPhaseAfterDeletion -ne "Available") {
        throw "BackupRecord changed to $recordPhaseAfterDeletion after source Namespace deletion"
    }

    $restoreTask = @"
apiVersion: protection.platform.io/v1alpha1
kind: RestoreTask
metadata:
  name: $restoreTaskName
spec:
  clusterRef: $ClusterRef
  trigger: Manual
  backupRecordRef: {name: $recordName}
  targetClusterRef: $ClusterRef
  mode: Original
  resourceSelection:
    include: [$resourceInclude]
    includeClusterResources: false
  restorePVC: false
  metadataOnly: false
  conflictPolicy:
    default: Skip
  dryRun: false
  failurePolicy: Continue
  timeout: 10m
"@
    Invoke-Native { $restoreTask | kubectl apply -f - | Out-Host } "Create RestoreTask"
    Invoke-Native { kubectl wait restoretask/$restoreTaskName --for=jsonpath='{.status.phase}'=Completed --timeout=300s | Out-Host } "Wait for RestoreTask Completed"

    $restoredNamespaceUID = Get-NativeValue { kubectl get namespace $sourceNamespace -o jsonpath='{.metadata.uid}' } "Read restored Namespace UID"
    if ($restoredNamespaceUID -eq $sourceNamespaceUID) {
        throw "Restored Namespace UID unexpectedly matches the deleted Namespace UID"
    }
    $configMapVerified = $false
    $secretVerified = $false
    if (-not $EmptyNamespace) {
        $restoredMarker = Get-NativeValue { kubectl get configmap $configMapName -n $sourceNamespace -o jsonpath='{.data.marker}' } "Read restored ConfigMap"
        if ($restoredMarker -ne $marker) {
            throw "Restored ConfigMap marker mismatch: $restoredMarker"
        }
        $encodedSecret = Get-NativeValue { kubectl get secret $secretName -n $sourceNamespace -o jsonpath='{.data.payload}' } "Read restored Secret"
        $restoredSecret = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($encodedSecret))
        if ($restoredSecret -ne $secretValue) {
            throw "Restored Secret payload mismatch"
        }
        $configMapVerified = $true
        $secretVerified = $true
    }

    $restoreObject = (Get-NativeValue { kubectl get restoretask $restoreTaskName -o json } "Read completed RestoreTask") | ConvertFrom-Json
    $completedCondition = @($restoreObject.status.conditions) | Where-Object { $_.type -eq "RestoreCompleted" -and $_.status -eq "True" }
    if (-not $completedCondition) {
        throw "RestoreCompleted=True condition is missing"
    }
    if ($EmptyNamespace -and [int64]$restoreObject.status.plan.totalObjects -ne 1) {
        throw "Empty Namespace restore plan should contain exactly one Namespace action"
    }

    Write-Host "Namespace deletion restore E2E PASSED" -ForegroundColor Green
    [pscustomobject]@{
        SourceNamespace = $sourceNamespace
        DeletedNamespaceUID = $sourceNamespaceUID
        RestoredNamespaceUID = $restoredNamespaceUID
        BackupRecord = $recordName
        RestoreTask = $restoreTaskName
        RestorePhase = $restoreObject.status.phase
        EmptyNamespace = [bool]$EmptyNamespace
        RecordResources = $recordResourceCount
        PlanObjects = $restoreObject.status.plan.totalObjects
        Created = $restoreObject.status.progress.created
        Skipped = $restoreObject.status.progress.skipped
        ConfigMapVerified = $configMapVerified
        SecretVerified = $secretVerified
    } | Format-List
}
catch {
    Write-Host "Namespace deletion restore E2E FAILED: $($_.Exception.Message)" -ForegroundColor Red
    kubectl get backuppolicy/$policyName --ignore-not-found -o yaml | Out-Host
    kubectl get backuptask/$backupTaskName --ignore-not-found -o yaml | Out-Host
    kubectl get restoretask/$restoreTaskName --ignore-not-found -o yaml | Out-Host
    if ($recordName) {
        kubectl get backuprecord/$recordName --ignore-not-found -o yaml | Out-Host
    }
    throw
}
finally {
    if ($Keep) {
        Write-Host "Keeping E2E resources: namespace=$sourceNamespace policy=$policyName backupTask=$backupTaskName record=$recordName restoreTask=$restoreTaskName"
    }
    else {
        kubectl delete restoretask/$restoreTaskName --ignore-not-found --wait=true --timeout=120s | Out-Null
        kubectl delete backuptask/$backupTaskName --ignore-not-found --wait=true --timeout=120s | Out-Null
        if ($recordName) {
            kubectl annotate backuprecord/$recordName protection.platform.io/delete-confirmed=true protection.platform.io/delete-mode=RepositoryData --overwrite | Out-Null
            kubectl delete backuprecord/$recordName --ignore-not-found --wait=true --timeout=120s | Out-Null
        }
        kubectl delete backuppolicy/$policyName --ignore-not-found --wait=true --timeout=120s | Out-Null
        kubectl delete namespace/$sourceNamespace --ignore-not-found --wait=true --timeout=120s | Out-Null
    }
}
