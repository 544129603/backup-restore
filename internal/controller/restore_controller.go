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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerpkg "sigs.k8s.io/controller-runtime/pkg/controller"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/archive"
	"github.com/example/backup-restore-operator/internal/checksum"
	"github.com/example/backup-restore-operator/internal/conditions"
	"github.com/example/backup-restore-operator/internal/encryption"
	opererrors "github.com/example/backup-restore-operator/internal/errors"
	"github.com/example/backup-restore-operator/internal/metrics"
	repofactory "github.com/example/backup-restore-operator/internal/repository/factory"
	"github.com/example/backup-restore-operator/internal/restore"
	"github.com/example/backup-restore-operator/internal/snapshot"
)

// +kubebuilder:rbac:groups=protection.platform.io,resources=restoretasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=protection.platform.io,resources=restoretasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=protection.platform.io,resources=restoretasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprecords;backuprepositories,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=namespaces;serviceaccounts;secrets;configmaps;persistentvolumeclaims;services,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs;cronjobs,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses;networkpolicies,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings;clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots;volumesnapshotcontents,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="*",resources="*",verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps;services,verbs=delete
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets,verbs=delete
// +kubebuilder:rbac:groups=batch,resources=jobs;cronjobs,verbs=delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses;networkpolicies,verbs=delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=delete

type RestoreTaskReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Dynamic       dynamic.Interface
	Mapper        meta.RESTMapper
	Factory       repofactory.Factory
	Snapshots     *snapshot.Manager
	Workspace     string
	ClusterRef    string
	MaxConcurrent int
}

func (r *RestoreTaskReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &protectionv1alpha1.RestoreTask{}
	if err := r.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	ctrl.LoggerFrom(ctx).Info("reconciling restore task", "task", object.Name, "phase", object.Status.Phase, "clusterRef", object.Spec.ClusterRef, "requestID", object.Annotations[protectionv1alpha1.AnnotationRequest])
	if r.ClusterRef != "" && (object.Spec.ClusterRef != r.ClusterRef || object.Spec.TargetClusterRef != r.ClusterRef) {
		return ctrl.Result{}, nil
	}
	if !object.DeletionTimestamp.IsZero() {
		if !terminalRestore(object.Status.Phase) {
			return r.cancel(ctx, object, "task was deleted")
		}
		if containsString(object.Finalizers, protectionv1alpha1.RestoreFinalizer) {
			before := object.DeepCopy()
			object.Finalizers = removeString(object.Finalizers, protectionv1alpha1.RestoreFinalizer)
			return ctrl.Result{}, r.Patch(ctx, object, client.MergeFrom(before))
		}
		return ctrl.Result{}, nil
	}
	if !containsString(object.Finalizers, protectionv1alpha1.RestoreFinalizer) {
		before := object.DeepCopy()
		object.Finalizers = append(object.Finalizers, protectionv1alpha1.RestoreFinalizer)
		if err := r.Patch(ctx, object, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	if terminalRestore(object.Status.Phase) {
		return ctrl.Result{}, nil
	}
	if object.Spec.CancelRequested {
		return r.cancel(ctx, object, object.Spec.CancelReason)
	}
	if timedOut(object.Status.StartedAt, object.Spec.Timeout, time.Now()) {
		return r.fail(ctx, object, opererrors.New(opererrors.CodeInternal, "restore task timed out", false, nil))
	}
	if object.Status.StartedAt != nil && object.Spec.Timeout.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, object.Status.StartedAt.Add(object.Spec.Timeout.Duration))
		defer cancel()
	}
	if object.Status.Phase == "" {
		return r.transition(ctx, object, protectionv1alpha1.RestorePhasePending, "Queued", "restore task accepted")
	}
	var result ctrl.Result
	var err error
	switch object.Status.Phase {
	case protectionv1alpha1.RestorePhasePending:
		result, err = r.validateBackup(ctx, object)
	case protectionv1alpha1.RestorePhaseValidatingBackup:
		result, err = r.download(ctx, object)
	case protectionv1alpha1.RestorePhaseDownloading:
		result, err = r.generatePlan(ctx, object)
	case protectionv1alpha1.RestorePhaseGeneratingPlan:
		result, err = r.precheck(ctx, object)
	case protectionv1alpha1.RestorePhasePreChecking:
		if object.Spec.DryRun {
			result, err = r.complete(ctx, object, "DryRunCompleted", "restore plan passed precheck; no resources were changed")
		} else {
			result, err = r.applyStage(ctx, object, protectionv1alpha1.RestorePhaseRestoringNamespaces)
		}
	case protectionv1alpha1.RestorePhaseRestoringNamespaces:
		result, err = r.applyStage(ctx, object, protectionv1alpha1.RestorePhaseRestoringClusterResources)
	case protectionv1alpha1.RestorePhaseRestoringClusterResources:
		result, err = r.applyStage(ctx, object, protectionv1alpha1.RestorePhaseRestoringPVC)
	case protectionv1alpha1.RestorePhaseRestoringPVC:
		result, err = r.applyStage(ctx, object, protectionv1alpha1.RestorePhaseRestoringNamespacedResources)
	case protectionv1alpha1.RestorePhaseRestoringNamespacedResources:
		result, err = r.transition(ctx, object, protectionv1alpha1.RestorePhaseVerifying, "ResourcesApplied", "all restore plan stages were applied")
	case protectionv1alpha1.RestorePhaseVerifying:
		result, err = r.verifyRestore(ctx, object)
	default:
		err = opererrors.New(opererrors.CodeInternal, "unknown restore phase "+object.Status.Phase, false, nil)
	}
	if err != nil {
		return r.fail(ctx, object, err)
	}
	return result, nil
}

func (r *RestoreTaskReconciler) validateBackup(ctx context.Context, task *protectionv1alpha1.RestoreTask) (ctrl.Result, error) {
	record := &protectionv1alpha1.BackupRecord{}
	if err := r.Get(ctx, client.ObjectKey{Name: task.Spec.BackupRecordRef.Name}, record); err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRecordNotFound, "backup record does not exist", false, err)
	}
	if !record.Status.Restorable || record.Status.Phase == protectionv1alpha1.RecordPhaseBroken || record.Status.Phase == protectionv1alpha1.RecordPhaseRepoUnavailable {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRestorePrecheck, "backup record is not restorable", false, nil)
	}
	if task.Spec.TargetClusterRef != record.Spec.Source.ClusterRef {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRestorePrecheck, "v1alpha1 supports same-cluster restore only", false, nil)
	}
	if err := restore.ValidateNamespaceMapping(task.Spec.NamespaceMapping); err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRestorePrecheck, "invalid namespace mapping", false, err)
	}
	return r.transition(ctx, task, protectionv1alpha1.RestorePhaseValidatingBackup, "BackupValidated", "backup record is independently verified and restorable")
}

func (r *RestoreTaskReconciler) download(ctx context.Context, task *protectionv1alpha1.RestoreTask) (ctrl.Result, error) {
	record, err := r.record(ctx, task)
	if err != nil {
		return ctrl.Result{}, err
	}
	dir, err := taskDirectory(r.Workspace, string(task.UID))
	if err != nil {
		return ctrl.Result{}, err
	}
	if err = os.RemoveAll(filepath.Join(dir, "payload")); err != nil {
		return ctrl.Result{}, err
	}
	if err = os.MkdirAll(filepath.Join(dir, "payload"), 0o700); err != nil {
		return ctrl.Result{}, err
	}
	repositoryCR, err := getRepository(ctx, r.Client, record.Spec.RepositoryRef.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	adapter, err := r.Factory.New(ctx, repositoryCR)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer adapter.Close()
	reader, err := adapter.Get(ctx, record.Spec.BackupPath+"/resources.tar.gz")
	if err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRestorePrecheck, "download backup archive", true, err)
	}
	archivePath := filepath.Join(dir, "resources.tar.gz")
	handle, err := os.OpenFile(archivePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		_ = reader.Close()
		return ctrl.Result{}, err
	}
	_, err = copyWithContext(ctx, handle, reader)
	closeErr := handle.Close()
	_ = reader.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	handle, err = os.Open(archivePath)
	if err != nil {
		return ctrl.Result{}, err
	}
	ok, _, err := checksum.Verify(ctx, handle, record.Spec.Checksum)
	_ = handle.Close()
	if err != nil || !ok {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRecordBroken, "downloaded archive checksum mismatch", false, err)
	}
	if record.Spec.Encryption.Enabled {
		key, keyErr := r.Factory.SecretValue(ctx, record.Spec.Encryption.KeyRef)
		if keyErr != nil {
			return ctrl.Result{}, opererrors.New(opererrors.CodeRestorePrecheck, "read frozen encryption key reference", false, keyErr)
		}
		encrypted, openErr := os.Open(archivePath)
		if openErr != nil {
			return ctrl.Result{}, openErr
		}
		plainPath := filepath.Join(dir, "resources.decrypted.tar.gz")
		plain, createErr := os.OpenFile(plainPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if createErr != nil {
			_ = encrypted.Close()
			return ctrl.Result{}, createErr
		}
		decryptErr := encryption.Decrypt(ctx, key, plain, encrypted)
		closeErr = plain.Close()
		_ = encrypted.Close()
		if decryptErr == nil {
			decryptErr = closeErr
		}
		if decryptErr != nil {
			return ctrl.Result{}, opererrors.New(opererrors.CodeRestorePrecheck, "decrypt backup archive", false, decryptErr)
		}
		archivePath = plainPath
	}
	handle, err = os.Open(archivePath)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = archive.Extract(ctx, handle, filepath.Join(dir, "payload"), 20<<30)
	_ = handle.Close()
	if err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRestorePrecheck, "extract backup archive", false, err)
	}
	return r.transition(ctx, task, protectionv1alpha1.RestorePhaseDownloading, "Downloaded", "archive downloaded, verified, and safely extracted")
}

func (r *RestoreTaskReconciler) generatePlan(ctx context.Context, task *protectionv1alpha1.RestoreTask) (ctrl.Result, error) {
	dir, err := taskDirectory(r.Workspace, string(task.UID))
	if err != nil {
		return ctrl.Result{}, err
	}
	index, err := readIndex(filepath.Join(dir, "payload", "index.json"))
	if err != nil {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRestorePrecheck, "read backup index", false, err)
	}
	plan, err := restore.BuildPlan(index, task.Spec)
	if err != nil {
		return ctrl.Result{}, err
	}
	if task.Spec.PlanHash != "" && task.Spec.PlanHash != plan.Hash {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRestorePrecheck, "submitted planHash does not match generated plan", false, nil)
	}
	if err = writeJSON(filepath.Join(dir, "plan.json"), plan); err != nil {
		return ctrl.Result{}, err
	}
	before := task.DeepCopy()
	now := metav1.Now()
	task.Status.Phase, task.Status.Step = protectionv1alpha1.RestorePhaseGeneratingPlan, protectionv1alpha1.RestorePhaseGeneratingPlan
	task.Status.Plan = protectionv1alpha1.RestorePlanSummary{Reference: filepath.Join(dir, "plan.json"), Hash: plan.Hash, TotalObjects: int64(len(plan.Items)), GeneratedAt: &now}
	task.Status.Checkpoints = appendCheckpoint(task.Status.Checkpoints, protectionv1alpha1.RestorePhaseGeneratingPlan, plan.Hash)
	task.Status.Progress.Total = int64(len(plan.Items))
	for _, item := range plan.Items {
		if item.Kind == "PersistentVolumeClaim" {
			task.Status.Plan.TotalPVCs++
			task.Status.Progress.TotalPVCs++
		}
	}
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionRestorePlanned, "PlanGenerated", fmt.Sprintf("generated %d restore actions", len(plan.Items)))
	return ctrl.Result{Requeue: true}, statusPatch(ctx, r.Client, task, before)
}

func (r *RestoreTaskReconciler) precheck(ctx context.Context, task *protectionv1alpha1.RestoreTask) (ctrl.Result, error) {
	plan, err := r.loadPlan(task)
	if err != nil {
		return ctrl.Result{}, err
	}
	before := task.DeepCopy()
	conflicts, blocking := int64(0), int64(0)
	hasCRD := false
	for _, item := range plan.Items {
		hasCRD = hasCRD || item.Kind == "CustomResourceDefinition"
	}
	resolver := restore.ConflictResolver{Policy: task.Spec.ConflictPolicy}
	for _, item := range plan.Items {
		gv, parseErr := schema.ParseGroupVersion(item.APIVersion)
		if parseErr != nil {
			return ctrl.Result{}, parseErr
		}
		mapping, mapErr := r.Mapper.RESTMapping(gv.WithKind(item.Kind).GroupKind(), gv.Version)
		if mapErr != nil {
			if hasCRD && meta.IsNoMatchError(mapErr) {
				task.Status.Plan.WarningCount++
				continue
			}
			return ctrl.Result{}, opererrors.New(opererrors.CodeRestorePrecheck, "API mapping unavailable for "+item.Kind, false, mapErr)
		}
		_, getErr := r.Dynamic.Resource(mapping.Resource).Namespace(item.TargetNamespace).Get(ctx, item.TargetName, metav1.GetOptions{})
		if getErr == nil {
			conflicts++
			if _, resolveErr := resolver.Resolve(item.SourceGVR, item.Kind, true); resolveErr != nil {
				blocking++
			}
		} else if !apierrors.IsNotFound(getErr) {
			return ctrl.Result{}, getErr
		}
	}
	task.Status.Phase, task.Status.Step = protectionv1alpha1.RestorePhasePreChecking, protectionv1alpha1.RestorePhasePreChecking
	task.Status.Checkpoints = appendCheckpoint(task.Status.Checkpoints, protectionv1alpha1.RestorePhasePreChecking, task.Status.Plan.Hash)
	task.Status.Plan.ConflictCount, task.Status.Plan.BlockingCount = conflicts, blocking
	if blocking > 0 {
		return ctrl.Result{}, opererrors.New(opererrors.CodeRestoreConflict, fmt.Sprintf("%d conflicts are blocked by Fail policy", blocking), false, nil)
	}
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionReady, "PrecheckPassed", fmt.Sprintf("precheck passed with %d resolvable conflicts", conflicts))
	return ctrl.Result{Requeue: true}, statusPatch(ctx, r.Client, task, before)
}

func (r *RestoreTaskReconciler) applyStage(ctx context.Context, task *protectionv1alpha1.RestoreTask, stage string) (ctrl.Result, error) {
	plan, err := r.loadPlan(task)
	if err != nil {
		return ctrl.Result{}, err
	}
	record, err := r.record(ctx, task)
	if err != nil {
		return ctrl.Result{}, err
	}
	dir, _ := taskDirectory(r.Workspace, string(task.UID))
	restorer := &restore.ResourceRestorer{Dynamic: r.Dynamic, Mapper: r.Mapper, Resolver: restore.ConflictResolver{Policy: task.Spec.ConflictPolicy}}
	if stage == protectionv1alpha1.RestorePhaseRestoringPVC {
		restorer.Transform = r.pvcTransform(ctx, task, record)
	}
	before := task.DeepCopy()
	for _, item := range plan.Items {
		if !stageIncludes(stage, item) {
			continue
		}
		result, applyErr := restorer.Apply(ctx, filepath.Join(dir, "payload"), item)
		task.Status.Progress.Processed++
		if applyErr != nil {
			task.Status.Progress.Failed++
			task.Status.Errors = append(task.Status.Errors, errorDetail(opererrors.New(opererrors.CodeRestoreResource, item.Kind+" "+item.TargetName, false, applyErr)))
			if task.Spec.FailurePolicy == "FailFast" {
				_ = statusPatch(ctx, r.Client, task, before)
				return ctrl.Result{}, applyErr
			}
			continue
		}
		switch result.Action {
		case "Create":
			task.Status.Progress.Created++
			task.Status.ResidualResources = append(task.Status.ResidualResources, protectionv1alpha1.ResourceObjectRef{Resource: item.SourceGVR, Namespace: result.TargetNamespace, Name: result.TargetName})
		case "Overwrite", "Recreate":
			task.Status.Progress.Updated++
			if result.Action == "Recreate" {
				task.Status.ResidualResources = append(task.Status.ResidualResources, protectionv1alpha1.ResourceObjectRef{Resource: item.SourceGVR, Namespace: result.TargetNamespace, Name: result.TargetName})
			}
		case "Skip":
			task.Status.Progress.Skipped++
		case "Rename":
			task.Status.Progress.Renamed++
			task.Status.ResidualResources = append(task.Status.ResidualResources, protectionv1alpha1.ResourceObjectRef{Resource: item.SourceGVR, Namespace: result.TargetNamespace, Name: result.TargetName})
		}
	}
	if len(task.Status.Errors) > 50 {
		task.Status.Errors = task.Status.Errors[len(task.Status.Errors)-50:]
	}
	if len(task.Status.ResidualResources) > 200 {
		task.Status.ResidualResources = task.Status.ResidualResources[len(task.Status.ResidualResources)-200:]
	}
	task.Status.Phase, task.Status.Step = stage, stage
	task.Status.Checkpoints = appendCheckpoint(task.Status.Checkpoints, stage, fmt.Sprintf("processed-%d", task.Status.Progress.Processed))
	if err = statusPatch(ctx, r.Client, task, before); err != nil {
		return ctrl.Result{}, err
	}
	if stage == protectionv1alpha1.RestorePhaseRestoringClusterResources {
		if resettable, ok := r.Mapper.(meta.ResettableRESTMapper); ok {
			resettable.Reset()
		}
	}
	return ctrl.Result{Requeue: true}, nil
}

func (r *RestoreTaskReconciler) verifyRestore(ctx context.Context, task *protectionv1alpha1.RestoreTask) (ctrl.Result, error) {
	plan, err := r.loadPlan(task)
	if err != nil {
		return ctrl.Result{}, err
	}
	before := task.DeepCopy()
	bound := int64(0)
	for _, item := range plan.Items {
		gv, parseErr := schema.ParseGroupVersion(item.APIVersion)
		if parseErr != nil {
			return ctrl.Result{}, parseErr
		}
		mapping, mapErr := r.Mapper.RESTMapping(gv.WithKind(item.Kind).GroupKind(), gv.Version)
		if mapErr != nil {
			return ctrl.Result{}, mapErr
		}
		object, getErr := r.Dynamic.Resource(mapping.Resource).Namespace(item.TargetNamespace).Get(ctx, item.TargetName, metav1.GetOptions{})
		if getErr != nil {
			if apierrors.IsNotFound(getErr) {
				task.Status.Progress.Failed++
				task.Status.Errors = append(task.Status.Errors, errorDetail(opererrors.New(opererrors.CodeRestoreResource, item.Kind+" "+item.TargetName+" is missing after restore", false, getErr)))
				continue
			}
			return ctrl.Result{}, getErr
		}
		if item.Kind == "PersistentVolumeClaim" {
			phase, _, _ := unstructured.NestedString(object.Object, "status", "phase")
			if phase != "Bound" {
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
			bound++
		}
	}
	task.Status.Progress.BoundPVCs = bound
	if err = statusPatch(ctx, r.Client, task, before); err != nil {
		return ctrl.Result{}, err
	}
	return r.complete(ctx, task, "RestoreCompleted", "restored objects exist and all restored PVCs are Bound")
}

func (r *RestoreTaskReconciler) pvcTransform(ctx context.Context, task *protectionv1alpha1.RestoreTask, record *protectionv1alpha1.BackupRecord) func(*unstructured.Unstructured, restore.PlanItem) error {
	byPVC := map[string]protectionv1alpha1.SnapshotResult{}
	for _, result := range record.Spec.Snapshots {
		byPVC[snapshot.SnapshotKey(result.PVCNamespace, result.PVCName)] = result
	}
	return func(object *unstructured.Unstructured, item restore.PlanItem) error {
		storageClass, _, _ := unstructured.NestedString(object.Object, "spec", "storageClassName")
		if mapped := task.Spec.StorageClassMapping[storageClass]; mapped != "" {
			_ = unstructured.SetNestedField(object.Object, mapped, "spec", "storageClassName")
		}
		result, ok := byPVC[snapshot.SnapshotKey(item.SourceNamespace, item.SourceName)]
		if !ok || !result.ReadyToUse {
			return opererrors.New(opererrors.CodeRestorePVC, "ready snapshot not found for PVC "+item.SourceName, false, nil)
		}
		snapshotName := result.VolumeSnapshotName
		if item.TargetNamespace != result.PVCNamespace {
			var err error
			snapshotName, err = r.Snapshots.PrepareRestoreSnapshot(ctx, result, item.TargetNamespace, item.TargetName)
			if err != nil {
				return err
			}
		}
		dataSource := map[string]interface{}{"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": snapshotName}
		return unstructured.SetNestedMap(object.Object, dataSource, "spec", "dataSource")
	}
}

func (r *RestoreTaskReconciler) complete(ctx context.Context, task *protectionv1alpha1.RestoreTask, reason, message string) (ctrl.Result, error) {
	before := task.DeepCopy()
	now := metav1.Now()
	task.Status.CompletedAt, task.Status.Reason, task.Status.Message = &now, reason, message
	if task.Status.Progress.Failed > 0 {
		task.Status.Phase = protectionv1alpha1.RestorePhasePartiallyFailed
	} else {
		task.Status.Phase = protectionv1alpha1.RestorePhaseCompleted
	}
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionRestoreCompleted, reason, message)
	if err := statusPatch(ctx, r.Client, task, before); err != nil {
		return ctrl.Result{}, err
	}
	record, err := r.record(ctx, task)
	if err == nil {
		beforeRecord := record.DeepCopy()
		record.Status.RestoreCount++
		record.Status.LastRestoreTime = &now
		_ = statusPatch(ctx, r.Client, record, beforeRecord)
	}
	metrics.RestoreTaskTotal.WithLabelValues(task.Status.Phase).Inc()
	if task.Status.StartedAt != nil {
		metrics.RestoreTaskDuration.WithLabelValues(task.Status.Phase).Observe(time.Since(task.Status.StartedAt.Time).Seconds())
	}
	return ctrl.Result{}, nil
}

func (r *RestoreTaskReconciler) fail(ctx context.Context, task *protectionv1alpha1.RestoreTask, err error) (ctrl.Result, error) {
	before := task.DeepCopy()
	now := metav1.Now()
	task.Status.Phase, task.Status.ErrorCode, task.Status.Reason, task.Status.Message, task.Status.CompletedAt = protectionv1alpha1.RestorePhaseFailed, opererrors.Code(err), "RestoreFailed", err.Error(), &now
	task.Status.Errors = append(task.Status.Errors, errorDetail(err))
	conditions.False(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionRestoreCompleted, "RestoreFailed", err.Error())
	metrics.RestoreTaskFailed.WithLabelValues(task.Status.ErrorCode).Inc()
	if task.Status.StartedAt != nil {
		metrics.RestoreTaskDuration.WithLabelValues("Failed").Observe(time.Since(task.Status.StartedAt.Time).Seconds())
	}
	return ctrl.Result{}, statusPatch(ctx, r.Client, task, before)
}

func (r *RestoreTaskReconciler) transition(ctx context.Context, task *protectionv1alpha1.RestoreTask, phase, reason, message string) (ctrl.Result, error) {
	before := task.DeepCopy()
	now := metav1.Now()
	if task.Status.StartedAt == nil && phase != protectionv1alpha1.RestorePhasePending {
		task.Status.StartedAt = &now
	}
	task.Status.ObservedGeneration, task.Status.Phase, task.Status.Step, task.Status.Reason, task.Status.Message, task.Status.LastHeartbeatTime = task.Generation, phase, phase, reason, message, &now
	task.Status.Checkpoints = appendCheckpoint(task.Status.Checkpoints, phase, reason)
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionProgressing, reason, message)
	conditions.True(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionCancellable, "Cancellable", "task can be cancelled")
	return ctrl.Result{Requeue: true}, statusPatch(ctx, r.Client, task, before)
}

func (r *RestoreTaskReconciler) cancel(ctx context.Context, task *protectionv1alpha1.RestoreTask, reason string) (ctrl.Result, error) {
	before := task.DeepCopy()
	now := metav1.Now()
	if reason == "" {
		reason = "cancel requested"
	}
	task.Status.Phase, task.Status.Reason, task.Status.Message, task.Status.CompletedAt = protectionv1alpha1.RestorePhaseCancelled, "Cancelled", reason, &now
	conditions.False(&task.Status.Conditions, task.Generation, protectionv1alpha1.ConditionProgressing, "Cancelled", "already-created resources are retained")
	return ctrl.Result{}, statusPatch(ctx, r.Client, task, before)
}

func (r *RestoreTaskReconciler) record(ctx context.Context, task *protectionv1alpha1.RestoreTask) (*protectionv1alpha1.BackupRecord, error) {
	record := &protectionv1alpha1.BackupRecord{}
	if err := r.Get(ctx, client.ObjectKey{Name: task.Spec.BackupRecordRef.Name}, record); err != nil {
		return nil, err
	}
	return record, nil
}

func (r *RestoreTaskReconciler) loadPlan(task *protectionv1alpha1.RestoreTask) (*restore.Plan, error) {
	dir, err := taskDirectory(r.Workspace, string(task.UID))
	if err != nil {
		return nil, err
	}
	handle, err := os.Open(filepath.Join(dir, "plan.json"))
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	plan := &restore.Plan{}
	err = json.NewDecoder(handle).Decode(plan)
	return plan, err
}

func stageIncludes(stage string, item restore.PlanItem) bool {
	switch stage {
	case protectionv1alpha1.RestorePhaseRestoringNamespaces:
		return item.Kind == "Namespace"
	case protectionv1alpha1.RestorePhaseRestoringClusterResources:
		return item.TargetNamespace == "" && item.Kind != "Namespace"
	case protectionv1alpha1.RestorePhaseRestoringPVC:
		return item.Kind == "PersistentVolumeClaim"
	case protectionv1alpha1.RestorePhaseRestoringNamespacedResources:
		return item.TargetNamespace != "" && item.Kind != "PersistentVolumeClaim"
	default:
		return false
	}
}

func copyWithContext(ctx context.Context, dst *os.File, src io.Reader) (int64, error) {
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		n, err := src.Read(buffer)
		if n > 0 {
			written, writeErr := dst.Write(buffer[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
		}
		if err != nil {
			if err == io.EOF {
				return total, nil
			}
			return total, err
		}
	}
}

func (r *RestoreTaskReconciler) SetupWithManager(manager ctrl.Manager) error {
	r.Factory.Client = r.Client
	maxConcurrent := r.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return ctrl.NewControllerManagedBy(manager).For(&protectionv1alpha1.RestoreTask{}).WithOptions(controllerpkg.Options{MaxConcurrentReconciles: maxConcurrent}).Complete(r)
}
