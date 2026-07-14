// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerpkg "sigs.k8s.io/controller-runtime/pkg/controller"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/archive"
	"github.com/example/backup-restore-operator/internal/checksum"
	"github.com/example/backup-restore-operator/internal/collector"
	"github.com/example/backup-restore-operator/internal/conditions"
	"github.com/example/backup-restore-operator/internal/encryption"
	opererrors "github.com/example/backup-restore-operator/internal/errors"
	"github.com/example/backup-restore-operator/internal/metrics"
	repofactory "github.com/example/backup-restore-operator/internal/repository/factory"
	"github.com/example/backup-restore-operator/internal/snapshot"
)

const backupFormatVersion = "1.0"

// +kubebuilder:rbac:groups=protection.platform.io,resources=backuptasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuptasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuptasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprepositories;backupscopes;backuppolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprecords,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims;storageclasses;namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots;volumesnapshotcontents;volumesnapshotclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type BackupTaskReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Recorder      record.EventRecorder
	Factory       repofactory.Factory
	Resolver      *collector.Resolver
	Collector     *collector.Collector
	Snapshots     *snapshot.Manager
	Workspace     string
	Version       string
	ClusterRef    string
	MaxConcurrent int
}

type backupMetadata struct {
	BackupID      string                              `json:"backupID"`
	TaskName      string                              `json:"taskName"`
	ClusterRef    string                              `json:"clusterRef"`
	ProjectRef    string                              `json:"projectRef"`
	CreatedAt     time.Time                           `json:"createdAt"`
	FormatVersion string                              `json:"formatVersion"`
	Snapshots     []protectionv1alpha1.SnapshotResult `json:"snapshots,omitempty"`
}

func (r *BackupTaskReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &protectionv1alpha1.BackupTask{}
	if err := r.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	ctrl.LoggerFrom(ctx).Info("reconciling backup task", "task", object.Name, "phase", object.Status.Phase, "clusterRef", object.Spec.ClusterRef, "requestID", object.Annotations[protectionv1alpha1.AnnotationRequest])
	if r.ClusterRef != "" && object.Spec.ClusterRef != r.ClusterRef {
		return ctrl.Result{}, nil
	}
	if !object.DeletionTimestamp.IsZero() {
		if !terminalBackup(object.Status.Phase) {
			return r.cancel(ctx, object, "task was deleted")
		}
		if containsString(object.Finalizers, protectionv1alpha1.BackupTaskFinalizer) {
			before := object.DeepCopy()
			object.Finalizers = removeString(object.Finalizers, protectionv1alpha1.BackupTaskFinalizer)
			return ctrl.Result{}, r.Patch(ctx, object, client.MergeFrom(before))
		}
		return ctrl.Result{}, nil
	}
	if !containsString(object.Finalizers, protectionv1alpha1.BackupTaskFinalizer) {
		before := object.DeepCopy()
		object.Finalizers = append(object.Finalizers, protectionv1alpha1.BackupTaskFinalizer)
		if err := r.Patch(ctx, object, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	if terminalBackup(object.Status.Phase) {
		return ctrl.Result{}, nil
	}
	if object.Spec.CancelRequested && !committedBackup(object.Status.Phase) {
		return r.cancel(ctx, object, object.Spec.CancelReason)
	}
	if timedOut(object.Status.StartedAt, object.Spec.Timeout, time.Now()) {
		return r.fail(ctx, object, opererrors.New(opererrors.CodeInternal, "backup task timed out", false, nil))
	}
	if object.Status.StartedAt != nil && object.Spec.Timeout.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, object.Status.StartedAt.Add(object.Spec.Timeout.Duration))
		defer cancel()
	}
	if object.Status.Phase == "" {
		return r.transition(ctx, object, protectionv1alpha1.BackupPhasePending, 0, "Queued", "backup task accepted")
	}
	var result ctrl.Result
	var err error
	switch object.Status.Phase {
	case protectionv1alpha1.BackupPhasePending:
		result, err = r.validate(ctx, object)
	case protectionv1alpha1.BackupPhaseValidating:
		result, err = r.prepare(ctx, object)
	case protectionv1alpha1.BackupPhasePreparing:
		result, err = r.collect(ctx, object)
	case protectionv1alpha1.BackupPhaseCollectingResources:
		result, err = r.preHooks(ctx, object)
	case protectionv1alpha1.BackupPhaseRunningPreHooks:
		result, err = r.createSnapshots(ctx, object)
	case protectionv1alpha1.BackupPhaseCreatingSnapshots:
		result, err = r.packageBackup(ctx, object)
	case protectionv1alpha1.BackupPhasePackaging:
		result, err = r.upload(ctx, object)
	case protectionv1alpha1.BackupPhaseUploading:
		result, err = r.verify(ctx, object)
	case protectionv1alpha1.BackupPhaseVerifying:
		result, err = r.generateRecord(ctx, object)
	case protectionv1alpha1.BackupPhaseGeneratingRecord:
		result, err = r.waitForRecord(ctx, object)
	default:
		err = opererrors.New(opererrors.CodeInternal, "unknown backup phase "+object.Status.Phase, false, nil)
	}
	if err != nil {
		return r.fail(ctx, object, err)
	}
	return result, nil
}

func (r *BackupTaskReconciler) validate(ctx context.Context, task *protectionv1alpha1.BackupTask) (ctrl.Result, error) {
	repository, err := getRepository(ctx, r.Client, task.Spec.RepositoryRef.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if repository.Status.Phase != protectionv1alpha1.RepositoryPhaseReady {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRepoConnect, "repository is not Ready", true, nil)
	}
	scope := &protectionv1alpha1.BackupScope{}
	if err = r.Get(ctx, client.ObjectKey{Name: task.Spec.ScopeRef.Name}, scope); err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeScopeInvalid, "scope does not exist", false, err)
	}
	if scope.Spec.ClusterRef != task.Spec.ClusterRef || repository.Spec.ClusterRef != task.Spec.ClusterRef {
		return ctrl.Result{}, opererrors.New(opererrors.CodePermissionDenied, "task references another cluster", false, nil)
	}
	if scope.Spec.IncludeSecrets && !repository.Spec.Encryption.Enabled {
		return ctrl.Result{}, opererrors.New(opererrors.CodePermissionDenied, "scopes containing Secrets require repository encryption", false, nil)
	}
	if task.Spec.ScopeSnapshot == nil {
		before := task.DeepCopy()
		snapshotSpec := protectionv1alpha1.BackupScopeSpec{}
		scope.Spec.DeepCopyInto(&snapshotSpec)
		task.Spec.ScopeSnapshot = &snapshotSpec
		task.Spec.ScopeGeneration, task.Spec.RepositoryGeneration = scope.Generation, repository.Generation
		if err = r.Patch(ctx, task, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.transition(ctx, task, protectionv1alpha1.BackupPhaseValidating, 5, "Validated", "references and permissions validated")
}

func (r *BackupTaskReconciler) prepare(ctx context.Context, task *protectionv1alpha1.BackupTask) (ctrl.Result, error) {
	dir, err := taskDirectory(r.Workspace, string(task.UID))
	if err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupCollect, "prepare workspace", true, err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "payload"), 0o700); err != nil {
		return ctrl.Result{}, err
	}
	return r.transition(ctx, task, protectionv1alpha1.BackupPhasePreparing, 10, "Prepared", "workspace prepared")
}

func (r *BackupTaskReconciler) collect(ctx context.Context, task *protectionv1alpha1.BackupTask) (ctrl.Result, error) {
	dir, err := taskDirectory(r.Workspace, string(task.UID))
	if err != nil {
		return ctrl.Result{}, err
	}
	payload := filepath.Join(dir, "payload")
	if err = os.RemoveAll(payload); err != nil {
		return ctrl.Result{}, err
	}
	if err = os.MkdirAll(payload, 0o700); err != nil {
		return ctrl.Result{}, err
	}
	scope := &protectionv1alpha1.BackupScope{Spec: *task.Spec.ScopeSnapshot}
	resources, _, err := r.Resolver.ResolveTypes(ctx, scope)
	if err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupCollect, "resolve resource types", true, err)
	}
	collected, err := r.Collector.Collect(ctx, scope, resources, payload)
	if err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupCollect, "collect resources", true, err)
	}
	if err = writeJSON(filepath.Join(payload, "index.json"), collected.Index); err != nil {
		return ctrl.Result{}, err
	}
	before := task.DeepCopy()
	task.Status.Phase, task.Status.Step = protectionv1alpha1.BackupPhaseCollectingResources, protectionv1alpha1.BackupPhaseCollectingResources
	task.Status.Progress.Percent, task.Status.Progress.TotalResources, task.Status.Progress.ProcessedResources, task.Status.Progress.SucceededResources = 35, collected.ResourceCount, collected.ResourceCount, collected.ResourceCount
	task.Status.Progress.TotalPVCs = collected.PVCCount
	task.Status.Warnings = int32(len(collected.Warnings))
	task.Status.Checkpoints = appendCheckpoint(task.Status.Checkpoints, protectionv1alpha1.BackupPhaseCollectingResources, "resources-collected")
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionResourcesCollected, "Collected", fmt.Sprintf("collected %d resources", collected.ResourceCount))
	return ctrl.Result{Requeue: true}, statusPatch(ctx, r.Client, task, before)
}

func (r *BackupTaskReconciler) preHooks(ctx context.Context, task *protectionv1alpha1.BackupTask) (ctrl.Result, error) {
	// Hook fields are reserved, but v1alpha1 executes only CrashConsistent backups.
	return r.transition(ctx, task, protectionv1alpha1.BackupPhaseRunningPreHooks, 40, "HooksSkipped", "CrashConsistent mode has no pre-hooks")
}

func (r *BackupTaskReconciler) createSnapshots(ctx context.Context, task *protectionv1alpha1.BackupTask) (ctrl.Result, error) {
	if task.Spec.ScopeSnapshot == nil || !task.Spec.ScopeSnapshot.PVC.Enabled || task.Status.Progress.TotalPVCs == 0 {
		return r.transition(ctx, task, protectionv1alpha1.BackupPhaseCreatingSnapshots, 55, "SnapshotsSkipped", "PVC snapshots are disabled or no PVC was selected")
	}
	dir, err := taskDirectory(r.Workspace, string(task.UID))
	if err != nil {
		return ctrl.Result{}, err
	}
	index, err := readIndex(filepath.Join(dir, "payload", "index.json"))
	if err != nil {
		return ctrl.Result{}, err
	}
	results := make([]protectionv1alpha1.SnapshotResult, 0)
	pending := false
	failed := int64(0)
	for _, entry := range index.Entries {
		if entry.Kind != "PersistentVolumeClaim" {
			continue
		}
		if !pvcNameSelected(task.Spec.ScopeSnapshot.PVC, entry.Namespace, entry.Name) {
			continue
		}
		capability, detectErr := r.Snapshots.Detect(ctx, entry.Namespace, entry.Name, task.Spec.ScopeSnapshot.PVC.SnapshotClassName, task.Spec.ScopeSnapshot.PVC.SnapshotClassMapping)
		if detectErr != nil {
			results = append(results, protectionv1alpha1.SnapshotResult{PVCNamespace: entry.Namespace, PVCName: entry.Name, Phase: "Failed", Error: detectErr.Error()})
			failed++
			continue
		}
		if task.Spec.ScopeSnapshot.PVC.LabelSelector != nil {
			selector, selectorErr := metav1.LabelSelectorAsSelector(task.Spec.ScopeSnapshot.PVC.LabelSelector)
			if selectorErr != nil {
				return ctrl.Result{}, selectorErr
			}
			if !selector.Matches(labels.Set(capability.PVC.Labels)) {
				continue
			}
		}
		result, ready, snapshotErr := r.Snapshots.EnsureSnapshot(ctx, string(task.UID), capability)
		results = append(results, result)
		if snapshotErr != nil {
			failed++
			continue
		}
		pending = pending || !ready
	}
	before := task.DeepCopy()
	task.Status.Snapshots = results
	task.Status.Progress.ProcessedPVCs = int64(len(results))
	task.Status.Progress.TotalPVCs = int64(len(results))
	task.Status.Progress.FailedSnapshots = failed
	task.Status.Progress.SucceededSnapshots = int64(len(results)) - failed
	if failed > 0 {
		metrics.SnapshotCreateFailed.WithLabelValues(opererrors.CodeSnapshotCreate).Add(float64(failed))
	}
	if err = statusPatch(ctx, r.Client, task, before); err != nil {
		return ctrl.Result{}, err
	}
	if pending {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if failed > 0 && task.Spec.ScopeSnapshot.PVC.FailurePolicy == "FailFast" {
		return ctrl.Result{}, opererrors.New(opererrors.CodeSnapshotCreate, fmt.Sprintf("%d PVC snapshots failed", failed), false, nil)
	}
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionSnapshotsReady, "SnapshotsProcessed", fmt.Sprintf("%d ready, %d failed", len(results)-int(failed), failed))
	return r.transition(ctx, task, protectionv1alpha1.BackupPhaseCreatingSnapshots, 60, "SnapshotsProcessed", "snapshot stage completed")
}

func (r *BackupTaskReconciler) packageBackup(ctx context.Context, task *protectionv1alpha1.BackupTask) (ctrl.Result, error) {
	dir, err := taskDirectory(r.Workspace, string(task.UID))
	if err != nil {
		return ctrl.Result{}, err
	}
	payload := filepath.Join(dir, "payload")
	metadata := backupMetadata{BackupID: string(task.UID), TaskName: task.Name, ClusterRef: task.Spec.ClusterRef, ProjectRef: task.Spec.ProjectRef, CreatedAt: task.CreationTimestamp.Time.UTC(), FormatVersion: backupFormatVersion, Snapshots: task.Status.Snapshots}
	if err = writeJSON(filepath.Join(payload, "metadata.json"), metadata); err != nil {
		return ctrl.Result{}, err
	}
	if err = writeJSON(filepath.Join(payload, "snapshots.json"), task.Status.Snapshots); err != nil {
		return ctrl.Result{}, err
	}
	archivePath := filepath.Join(dir, "resources.tar.gz")
	handle, err := os.OpenFile(archivePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = archive.Create(ctx, payload, handle, 6)
	closeErr := handle.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupPackage, "create deterministic archive", true, err)
	}
	repositoryCR, err := getRepository(ctx, r.Client, task.Spec.RepositoryRef.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if repositoryCR.Spec.Encryption.Enabled {
		key, keyErr := r.Factory.SecretValue(ctx, repositoryCR.Spec.Encryption.KeyRef)
		if keyErr != nil {
			return ctrl.Result{}, opererrors.New(opererrors.CodeBackupPackage, "read encryption key", false, keyErr)
		}
		plain, openErr := os.Open(archivePath)
		if openErr != nil {
			return ctrl.Result{}, openErr
		}
		encryptedPath := archivePath + ".encrypted"
		encrypted, createErr := os.OpenFile(encryptedPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if createErr != nil {
			_ = plain.Close()
			return ctrl.Result{}, createErr
		}
		encryptErr := encryption.Encrypt(ctx, key, encrypted, plain)
		closeEncryptedErr := encrypted.Close()
		_ = plain.Close()
		if encryptErr == nil {
			encryptErr = closeEncryptedErr
		}
		if encryptErr != nil {
			return ctrl.Result{}, opererrors.New(opererrors.CodeBackupPackage, "encrypt archive", false, encryptErr)
		}
		if err = os.Remove(archivePath); err != nil {
			return ctrl.Result{}, err
		}
		if err = os.Rename(encryptedPath, archivePath); err != nil {
			return ctrl.Result{}, err
		}
	}
	handle, err = os.Open(archivePath)
	if err != nil {
		return ctrl.Result{}, err
	}
	sum, size, sumErr := checksum.Sum(ctx, handle)
	_ = handle.Close()
	if sumErr != nil {
		return ctrl.Result{}, sumErr
	}
	before := task.DeepCopy()
	task.Status.Phase, task.Status.Step, task.Status.ArchivePath, task.Status.ArchiveChecksum, task.Status.BackupBytes = protectionv1alpha1.BackupPhasePackaging, protectionv1alpha1.BackupPhasePackaging, archivePath, sum, size
	task.Status.Checkpoints = appendCheckpoint(task.Status.Checkpoints, protectionv1alpha1.BackupPhasePackaging, sum)
	task.Status.Progress.Percent, task.Status.Progress.BytesProcessed = 70, size
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionArchiveReady, "Packaged", fmt.Sprintf("archive contains %d bytes", size))
	return ctrl.Result{Requeue: true}, statusPatch(ctx, r.Client, task, before)
}

func (r *BackupTaskReconciler) upload(ctx context.Context, task *protectionv1alpha1.BackupTask) (ctrl.Result, error) {
	repositoryCR, err := getRepository(ctx, r.Client, task.Spec.RepositoryRef.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	adapter, err := r.Factory.New(ctx, repositoryCR)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer adapter.Close()
	dir, _ := taskDirectory(r.Workspace, string(task.UID))
	payload := filepath.Join(dir, "payload")
	files := map[string]string{"resources.tar.gz": filepath.Join(dir, "resources.tar.gz"), "metadata.json": filepath.Join(payload, "metadata.json"), "index.json": filepath.Join(payload, "index.json"), "snapshots.json": filepath.Join(payload, "snapshots.json")}
	manifest := map[string]string{}
	remoteRoot := backupRemotePath(task)
	// A retry invalidates any marker left after a process crash. Readers never
	// consider the directory committed while files are being replaced.
	if err = adapter.Delete(ctx, remoteRoot+"/.done"); err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupUpload, "clear stale completion marker", true, err)
	}
	for name, localPath := range files {
		handle, openErr := os.Open(localPath)
		if openErr != nil {
			return ctrl.Result{}, openErr
		}
		sum, _, sumErr := checksum.Sum(ctx, handle)
		_ = handle.Close()
		if sumErr != nil {
			return ctrl.Result{}, sumErr
		}
		manifest[name] = sum
		handle, openErr = os.Open(localPath)
		if openErr != nil {
			return ctrl.Result{}, openErr
		}
		putErr := adapter.Put(ctx, remoteRoot+"/"+name, handle)
		_ = handle.Close()
		if putErr != nil {
			return ctrl.Result{}, opererrors.New(opererrors.CodeBackupUpload, "upload "+name, true, putErr)
		}
	}
	manifestText := checksum.Manifest(manifest)
	if err = adapter.Put(ctx, remoteRoot+"/sha256sum.txt", strings.NewReader(manifestText)); err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupUpload, "upload checksum manifest", true, err)
	}
	// .done is the commit point. It is intentionally written last.
	if err = adapter.Put(ctx, remoteRoot+"/.done", strings.NewReader(task.Status.ArchiveChecksum+"\n")); err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupUpload, "write completion marker", true, err)
	}
	before := task.DeepCopy()
	task.Status.Phase, task.Status.Step = protectionv1alpha1.BackupPhaseUploading, protectionv1alpha1.BackupPhaseUploading
	task.Status.Checkpoints = appendCheckpoint(task.Status.Checkpoints, protectionv1alpha1.BackupPhaseUploading, task.Status.ArchiveChecksum)
	task.Status.Progress.Percent, task.Status.Progress.BytesUploaded = 85, task.Status.BackupBytes
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionUploaded, "Committed", "all files uploaded and .done marker committed")
	conditions.False(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionCancellable, "CommitPointPassed", "backup is committed and must finish record generation")
	return ctrl.Result{Requeue: true}, statusPatch(ctx, r.Client, task, before)
}

func (r *BackupTaskReconciler) verify(ctx context.Context, task *protectionv1alpha1.BackupTask) (ctrl.Result, error) {
	repositoryCR, err := getRepository(ctx, r.Client, task.Spec.RepositoryRef.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	adapter, err := r.Factory.New(ctx, repositoryCR)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer adapter.Close()
	root := backupRemotePath(task)
	done, err := adapter.Get(ctx, root+"/.done")
	if err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupChecksum, "completion marker missing", true, err)
	}
	doneValue, doneErr := io.ReadAll(done)
	_ = done.Close()
	if doneErr != nil || !strings.EqualFold(strings.TrimSpace(string(doneValue)), task.Status.ArchiveChecksum) {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupChecksum, "completion marker does not match archive checksum", false, doneErr)
	}
	manifestReader, err := adapter.Get(ctx, root+"/sha256sum.txt")
	if err != nil {
		return ctrl.Result{}, err
	}
	manifest, err := checksum.ParseManifest(manifestReader)
	_ = manifestReader.Close()
	if err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupChecksum, "invalid checksum manifest", false, err)
	}
	if !strings.EqualFold(manifest["resources.tar.gz"], task.Status.ArchiveChecksum) {
		return ctrl.Result{}, opererrors.New(opererrors.CodeBackupChecksum, "archive checksum is not anchored to task status", false, nil)
	}
	for name, expected := range manifest {
		reader, getErr := adapter.Get(ctx, root+"/"+name)
		if getErr != nil {
			return ctrl.Result{}, getErr
		}
		ok, _, verifyErr := checksum.Verify(ctx, reader, expected)
		_ = reader.Close()
		if verifyErr != nil || !ok {
			return ctrl.Result{}, opererrors.New(opererrors.CodeBackupChecksum, "checksum mismatch for "+name, false, verifyErr)
		}
	}
	before := task.DeepCopy()
	task.Status.Phase, task.Status.Step, task.Status.Progress.Percent = protectionv1alpha1.BackupPhaseVerifying, protectionv1alpha1.BackupPhaseVerifying, 95
	task.Status.Checkpoints = appendCheckpoint(task.Status.Checkpoints, protectionv1alpha1.BackupPhaseVerifying, task.Status.ArchiveChecksum)
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionVerified, "ChecksumMatched", "remote package passed checksum verification")
	return ctrl.Result{Requeue: true}, statusPatch(ctx, r.Client, task, before)
}

func (r *BackupTaskReconciler) generateRecord(ctx context.Context, task *protectionv1alpha1.BackupTask) (ctrl.Result, error) {
	recordName := recordName(task)
	record := &protectionv1alpha1.BackupRecord{}
	err := r.Get(ctx, client.ObjectKey{Name: recordName}, record)
	if apierrors.IsNotFound(err) {
		now := metav1.Now()
		expires := metav1.NewTime(now.Add(30 * 24 * time.Hour))
		var policyRef *protectionv1alpha1.ObjectReference
		if task.Spec.PolicyRef != nil {
			copyRef := *task.Spec.PolicyRef
			policyRef = &copyRef
			policy := &protectionv1alpha1.BackupPolicy{}
			if getErr := r.Get(ctx, client.ObjectKey{Name: copyRef.Name}, policy); getErr == nil && policy.Spec.Retention.MaxAgeDays > 0 {
				expires = metav1.NewTime(now.Add(time.Duration(policy.Spec.Retention.MaxAgeDays) * 24 * time.Hour))
			}
		}
		content := "Complete"
		if task.Status.Progress.FailedSnapshots > 0 || task.Status.Progress.FailedResources > 0 {
			content = "Partial"
		}
		repositoryCR, getRepositoryErr := getRepository(ctx, r.Client, task.Spec.RepositoryRef.Name)
		if getRepositoryErr != nil {
			return ctrl.Result{}, getRepositoryErr
		}
		encryptionSpec := protectionv1alpha1.RecordEncryptionSpec{Enabled: repositoryCR.Spec.Encryption.Enabled, Algorithm: repositoryCR.Spec.Encryption.Algorithm, KeyRef: repositoryCR.Spec.Encryption.KeyRef}
		namespaces, namespaceErr := r.sourceNamespaces(task)
		if namespaceErr != nil {
			return ctrl.Result{}, namespaceErr
		}
		source := protectionv1alpha1.BackupSource{ClusterRef: task.Spec.ClusterRef, ScopeMode: string(task.Spec.ScopeSnapshot.Mode), Namespaces: namespaces}
		if serverVersion, versionErr := r.Resolver.Discovery.ServerVersion(); versionErr == nil {
			source.KubernetesVersion = serverVersion.GitVersion
		}
		clusterNamespace := &corev1.Namespace{}
		if getErr := r.Get(ctx, client.ObjectKey{Name: "kube-system"}, clusterNamespace); getErr == nil {
			source.ClusterUID = string(clusterNamespace.UID)
		}
		record = &protectionv1alpha1.BackupRecord{ObjectMeta: metav1.ObjectMeta{Name: recordName, Labels: map[string]string{protectionv1alpha1.LabelTaskUID: string(task.UID), protectionv1alpha1.LabelCluster: task.Spec.ClusterRef, protectionv1alpha1.LabelProject: task.Spec.ProjectRef}}, Spec: protectionv1alpha1.BackupRecordSpec{ResourceIdentity: task.Spec.ResourceIdentity, BackupID: string(task.UID), SourceTaskRef: protectionv1alpha1.ObjectReference{Name: task.Name, UID: string(task.UID)}, PolicyRef: policyRef, RepositoryRef: task.Spec.RepositoryRef, Source: source, BackupPath: backupRemotePath(task), Checksum: task.Status.ArchiveChecksum, ChecksumAlgorithm: "SHA-256", FormatVersion: backupFormatVersion, OperatorVersion: r.Version, Encryption: encryptionSpec, Inventory: protectionv1alpha1.BackupInventory{ResourceCount: task.Status.Progress.SucceededResources, NamespaceCount: int64(len(namespaces)), PVCCount: task.Status.Progress.TotalPVCs, SnapshotCount: task.Status.Progress.SucceededSnapshots, FailedResourceCount: task.Status.Progress.FailedResources, FailedSnapshotCount: task.Status.Progress.FailedSnapshots, BackupBytes: task.Status.BackupBytes}, Snapshots: task.Status.Snapshots, ContentCompleteness: content, SnapshotLifecycle: task.Spec.ScopeSnapshot.PVC.Lifecycle, ExpiresAt: &expires}}
		if policyRef != nil {
			record.Labels[protectionv1alpha1.LabelPolicyUID] = policyRef.UID
		}
		if err = r.Create(ctx, record); err != nil && !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}
	before := task.DeepCopy()
	task.Status.Phase, task.Status.Step, task.Status.RecordRef = protectionv1alpha1.BackupPhaseGeneratingRecord, protectionv1alpha1.BackupPhaseGeneratingRecord, &protectionv1alpha1.ObjectReference{Name: recordName, UID: string(record.UID)}
	task.Status.Checkpoints = appendCheckpoint(task.Status.Checkpoints, protectionv1alpha1.BackupPhaseGeneratingRecord, recordName)
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionRecordCreated, "RecordCreated", "backup record created and awaiting independent verification")
	return ctrl.Result{RequeueAfter: 2 * time.Second}, statusPatch(ctx, r.Client, task, before)
}

func (r *BackupTaskReconciler) waitForRecord(ctx context.Context, task *protectionv1alpha1.BackupTask) (ctrl.Result, error) {
	if task.Status.RecordRef == nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeInternal, "record reference is missing", true, nil)
	}
	record := &protectionv1alpha1.BackupRecord{}
	if err := r.Get(ctx, client.ObjectKey{Name: task.Status.RecordRef.Name}, record); err != nil {
		return ctrl.Result{}, err
	}
	switch record.Status.Phase {
	case protectionv1alpha1.RecordPhaseAvailable, protectionv1alpha1.RecordPhasePartiallyAvailable:
		before := task.DeepCopy()
		now := metav1.Now()
		task.Status.CompletedAt, task.Status.Progress.Percent = &now, 100
		if record.Status.Phase == protectionv1alpha1.RecordPhasePartiallyAvailable {
			task.Status.Phase, task.Status.Reason, task.Status.Message = protectionv1alpha1.BackupPhasePartiallyFailed, "PartialBackup", "record is restorable but has partial failures"
		} else {
			task.Status.Phase, task.Status.Reason, task.Status.Message = protectionv1alpha1.BackupPhaseCompleted, "BackupCompleted", "backup and independent record verification completed"
		}
		metrics.BackupTaskTotal.WithLabelValues(task.Spec.Trigger, task.Status.Phase).Inc()
		metrics.BackupTaskBytes.Add(float64(task.Status.BackupBytes))
		metrics.BackupTaskObjects.Add(float64(task.Status.Progress.SucceededResources))
		if task.Status.StartedAt != nil {
			metrics.BackupTaskDuration.WithLabelValues(task.Spec.Trigger, task.Status.Phase).Observe(time.Since(task.Status.StartedAt.Time).Seconds())
		}
		return ctrl.Result{}, statusPatch(ctx, r.Client, task, before)
	case protectionv1alpha1.RecordPhaseBroken:
		return ctrl.Result{}, opererrors.New(opererrors.CodeRecordBroken, "generated backup record is broken", false, nil)
	case protectionv1alpha1.RecordPhaseSnapshotMissing:
		if record.Status.Restorable {
			before := task.DeepCopy()
			now := metav1.Now()
			task.Status.Phase, task.Status.CompletedAt, task.Status.Progress.Percent = protectionv1alpha1.BackupPhasePartiallyFailed, &now, 100
			task.Status.Reason, task.Status.Message = "SnapshotMissing", "resource package is valid but one or more snapshots are missing"
			return ctrl.Result{}, statusPatch(ctx, r.Client, task, before)
		}
		return ctrl.Result{}, opererrors.New(opererrors.CodeRecordBroken, "required snapshot is missing", false, nil)
	default:
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
}

func (r *BackupTaskReconciler) transition(ctx context.Context, task *protectionv1alpha1.BackupTask, phase string, percent int32, reason, message string) (ctrl.Result, error) {
	before := task.DeepCopy()
	now := metav1.Now()
	if task.Status.StartedAt == nil && phase != protectionv1alpha1.BackupPhasePending {
		task.Status.StartedAt = &now
	}
	task.Status.ObservedGeneration, task.Status.Phase, task.Status.Step, task.Status.Reason, task.Status.Message = task.Generation, phase, phase, reason, message
	task.Status.Progress.Percent = percent
	task.Status.LastHeartbeatTime = &now
	task.Status.ExecutionNode = os.Getenv("NODE_NAME")
	task.Status.WorkerName = os.Getenv("POD_NAME")
	task.Status.Checkpoints = appendCheckpoint(task.Status.Checkpoints, phase, reason)
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionProgressing, reason, message)
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionCancellable, "Cancellable", "task can be cancelled")
	return ctrl.Result{Requeue: true}, statusPatch(ctx, r.Client, task, before)
}

func (r *BackupTaskReconciler) fail(ctx context.Context, task *protectionv1alpha1.BackupTask, err error) (ctrl.Result, error) {
	before := task.DeepCopy()
	task.Status.Attempt++
	task.Status.ErrorCode, task.Status.Reason, task.Status.Message = opererrors.Code(err), "StepFailed", err.Error()
	task.Status.Errors = append(task.Status.Errors, errorDetail(err))
	if len(task.Status.Errors) > 50 {
		task.Status.Errors = task.Status.Errors[len(task.Status.Errors)-50:]
	}
	if opererrors.Retryable(err) && task.Status.Attempt < task.Spec.RetryPolicy.MaxAttempts {
		if patchErr := statusPatch(ctx, r.Client, task, before); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		delay := task.Spec.RetryPolicy.Backoff.Duration * time.Duration(1<<min32(task.Status.Attempt-1, 6))
		if max := task.Spec.RetryPolicy.MaxBackoff.Duration; max > 0 && delay > max {
			delay = max
		}
		return ctrl.Result{RequeueAfter: delay}, nil
	}
	now := metav1.Now()
	task.Status.Phase, task.Status.CompletedAt = protectionv1alpha1.BackupPhaseFailed, &now
	conditions.False(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionProgressing, "Failed", err.Error())
	metrics.BackupTaskFailed.WithLabelValues(task.Status.ErrorCode).Inc()
	metrics.BackupTaskTotal.WithLabelValues(task.Spec.Trigger, "Failed").Inc()
	if task.Status.StartedAt != nil {
		metrics.BackupTaskDuration.WithLabelValues(task.Spec.Trigger, "Failed").Observe(time.Since(task.Status.StartedAt.Time).Seconds())
	}
	if r.Recorder != nil {
		r.Recorder.Event(task, corev1.EventTypeWarning, "BackupFailed", err.Error())
	}
	return ctrl.Result{}, statusPatch(ctx, r.Client, task, before)
}

func (r *BackupTaskReconciler) cancel(ctx context.Context, task *protectionv1alpha1.BackupTask, reason string) (ctrl.Result, error) {
	before := task.DeepCopy()
	if reason == "" {
		reason = "cancel requested"
	}
	task.Status.Phase = protectionv1alpha1.BackupPhaseCancelling
	for _, result := range task.Status.Snapshots {
		_ = r.Snapshots.Delete(ctx, result)
	}
	if dir, err := taskDirectory(r.Workspace, string(task.UID)); err == nil {
		_ = os.RemoveAll(dir)
	}
	now := metav1.Now()
	task.Status.Phase, task.Status.Reason, task.Status.Message, task.Status.ErrorCode, task.Status.CompletedAt = protectionv1alpha1.BackupPhaseCancelled, "Cancelled", reason, opererrors.CodeBackupCancelled, &now
	conditions.False(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionProgressing, "Cancelled", reason)
	return ctrl.Result{}, statusPatch(ctx, r.Client, task, before)
}

func backupRemotePath(task *protectionv1alpha1.BackupTask) string {
	return "backups/" + string(task.UID)
}

func recordName(task *protectionv1alpha1.BackupTask) string {
	uid := strings.ToLower(string(task.UID))
	if len(uid) > 36 {
		uid = uid[:36]
	}
	return "backup-" + uid
}

func pvcNameSelected(spec protectionv1alpha1.PVCSelectionSpec, namespace, name string) bool {
	matches := func(values []string) bool {
		for _, value := range values {
			if value == "*" || value == name || value == namespace+"/"+name {
				return true
			}
		}
		return false
	}
	if matches(spec.Exclude) {
		return false
	}
	return len(spec.Include) == 0 || matches(spec.Include)
}

func (r *BackupTaskReconciler) sourceNamespaces(task *protectionv1alpha1.BackupTask) ([]string, error) {
	if task.Spec.ScopeSnapshot == nil {
		return nil, fmt.Errorf("scope snapshot is missing")
	}
	if task.Spec.ScopeSnapshot.Mode == protectionv1alpha1.BackupScopeModeNamespace {
		return collector.IncludedNamespaces(*task.Spec.ScopeSnapshot), nil
	}
	dir, err := taskDirectory(r.Workspace, string(task.UID))
	if err != nil {
		return nil, err
	}
	index, err := readIndex(filepath.Join(dir, "payload", "index.json"))
	if err != nil {
		return nil, err
	}
	set := map[string]struct{}{}
	for _, entry := range index.Entries {
		if entry.Namespace != "" {
			set[entry.Namespace] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for namespace := range set {
		result = append(result, namespace)
	}
	sort.Strings(result)
	return result, nil
}

func writeJSON(path string, value interface{}) error {
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(handle)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(value)
	closeErr := handle.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func readIndex(path string) (collector.Index, error) {
	handle, err := os.Open(path)
	if err != nil {
		return collector.Index{}, err
	}
	defer handle.Close()
	var result collector.Index
	err = json.NewDecoder(handle).Decode(&result)
	return result, err
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func (r *BackupTaskReconciler) SetupWithManager(manager ctrl.Manager) error {
	r.Factory.Client = r.Client
	maxConcurrent := r.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 3
	}
	return ctrl.NewControllerManagedBy(manager).For(&protectionv1alpha1.BackupTask{}).WithOptions(controllerpkg.Options{MaxConcurrentReconciles: maxConcurrent}).Complete(r)
}

var _ io.Reader
