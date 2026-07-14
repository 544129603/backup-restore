// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/checksum"
	"github.com/example/backup-restore-operator/internal/conditions"
	opererrors "github.com/example/backup-restore-operator/internal/errors"
	"github.com/example/backup-restore-operator/internal/metrics"
	repofactory "github.com/example/backup-restore-operator/internal/repository/factory"
	"github.com/example/backup-restore-operator/internal/snapshot"
)

// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprecords,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprecords/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprecords/finalizers,verbs=update
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprepositories;restoretasks,verbs=get;list;watch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots;volumesnapshotcontents,verbs=get;list;watch;delete

type BackupRecordReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Factory    repofactory.Factory
	Snapshots  *snapshot.Manager
	ClusterRef string
}

func (r *BackupRecordReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &protectionv1alpha1.BackupRecord{}
	if err := r.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.ClusterRef != "" && object.Spec.ClusterRef != r.ClusterRef {
		return ctrl.Result{}, nil
	}
	if !object.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, object)
	}
	if !containsString(object.Finalizers, protectionv1alpha1.RecordFinalizer) {
		before := object.DeepCopy()
		object.Finalizers = append(object.Finalizers, protectionv1alpha1.RecordFinalizer)
		if err := r.Patch(ctx, object, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	if object.Spec.ExpiresAt != nil && time.Now().After(object.Spec.ExpiresAt.Time) {
		before := object.DeepCopy()
		object.Status.ObservedGeneration, object.Status.Phase, object.Status.Restorable, object.Status.Reason, object.Status.Message = object.Generation, protectionv1alpha1.RecordPhaseExpired, false, "RetentionExpired", "backup record exceeded its retention deadline"
		conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "RetentionExpired", object.Status.Message)
		return ctrl.Result{RequeueAfter: time.Hour}, statusPatch(ctx, r.Client, object, before)
	}
	return r.verify(ctx, object)
}

func (r *BackupRecordReconciler) verify(ctx context.Context, record *protectionv1alpha1.BackupRecord) (ctrl.Result, error) {
	before := record.DeepCopy()
	record.Status.ObservedGeneration, record.Status.Phase, record.Status.Restorable = record.Generation, protectionv1alpha1.RecordPhaseVerifying, false
	conditions.Unknown(&record.Status.Conditions, record.Generation, protectionv1alpha1.ConditionVerified, "Verifying", "verifying repository package")
	repositoryCR, err := getRepository(ctx, r.Client, record.Spec.RepositoryRef.Name)
	if err != nil {
		record.Status.Phase, record.Status.Reason, record.Status.Message, record.Status.ErrorCode = protectionv1alpha1.RecordPhaseRepoUnavailable, "RepositoryUnavailable", err.Error(), opererrors.CodeRepoConnect
		conditions.False(&record.Status.Conditions, record.Generation, protectionv1alpha1.ConditionRepositoryAvailable, "RepositoryUnavailable", err.Error())
		_ = statusPatch(ctx, r.Client, record, before)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	adapter, err := r.Factory.New(ctx, repositoryCR)
	if err != nil {
		record.Status.Phase, record.Status.Reason, record.Status.Message, record.Status.ErrorCode = protectionv1alpha1.RecordPhaseRepoUnavailable, "RepositoryUnavailable", err.Error(), opererrors.CodeRepoConnect
		_ = statusPatch(ctx, r.Client, record, before)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	defer adapter.Close()
	done, err := adapter.Get(ctx, record.Spec.BackupPath+"/.done")
	if err != nil {
		return r.markBroken(ctx, record, before, "completion marker is missing: "+err.Error())
	}
	doneValue, doneErr := io.ReadAll(done)
	_ = done.Close()
	if doneErr != nil || !strings.EqualFold(strings.TrimSpace(string(doneValue)), record.Spec.Checksum) {
		message := "completion marker does not match immutable BackupRecord checksum"
		if doneErr != nil {
			message += ": " + doneErr.Error()
		}
		return r.markBroken(ctx, record, before, message)
	}
	manifestReader, err := adapter.Get(ctx, record.Spec.BackupPath+"/sha256sum.txt")
	if err != nil {
		return r.markBroken(ctx, record, before, err.Error())
	}
	manifest, err := checksum.ParseManifest(manifestReader)
	_ = manifestReader.Close()
	if err != nil {
		return r.markBroken(ctx, record, before, err.Error())
	}
	if !strings.EqualFold(manifest["resources.tar.gz"], record.Spec.Checksum) {
		return r.markBroken(ctx, record, before, "archive checksum is not anchored to immutable BackupRecord spec")
	}
	verified := int64(0)
	for name, expected := range manifest {
		reader, getErr := adapter.Get(ctx, record.Spec.BackupPath+"/"+name)
		if getErr != nil {
			return r.markBroken(ctx, record, before, getErr.Error())
		}
		ok, _, checkErr := checksum.Verify(ctx, reader, expected)
		_ = reader.Close()
		if checkErr != nil || !ok {
			return r.markBroken(ctx, record, before, "checksum mismatch for "+name)
		}
		verified++
	}
	missing := make([]string, 0)
	for _, result := range record.Spec.Snapshots {
		if result.VolumeSnapshotName == "" || !result.ReadyToUse {
			continue
		}
		_, getErr := r.Snapshots.Dynamic.Resource(snapshot.VolumeSnapshotGVR).Namespace(result.PVCNamespace).Get(ctx, result.VolumeSnapshotName, metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			missing = append(missing, result.PVCNamespace+"/"+result.VolumeSnapshotName)
		} else if getErr != nil {
			return ctrl.Result{}, getErr
		}
	}
	now := metav1.Now()
	record.Status.LastVerifiedAt, record.Status.VerifiedFiles, record.Status.MissingSnapshots = &now, verified, missing
	conditions.True(&record.Status.Conditions, record.Generation, protectionv1alpha1.ConditionVerified, "ChecksumMatched", fmt.Sprintf("verified %d files", verified))
	if len(missing) > 0 {
		record.Status.Phase, record.Status.Reason, record.Status.Message = protectionv1alpha1.RecordPhaseSnapshotMissing, "SnapshotMissing", strings.Join(missing, ", ")
		if record.Spec.ContentCompleteness == "Complete" && record.Spec.Inventory.PVCCount > 0 {
			record.Status.Restorable = false
		} else {
			record.Status.Restorable = true
		}
	} else if record.Spec.ContentCompleteness == "Partial" {
		record.Status.Phase, record.Status.Restorable, record.Status.Reason, record.Status.Message = protectionv1alpha1.RecordPhasePartiallyAvailable, true, "VerifiedPartial", "metadata package is valid; some backup items failed"
	} else {
		record.Status.Phase, record.Status.Restorable, record.Status.Reason, record.Status.Message = protectionv1alpha1.RecordPhaseAvailable, true, "Verified", "backup package and snapshots are available"
	}
	if err = statusPatch(ctx, r.Client, record, before); err != nil {
		return ctrl.Result{}, err
	}
	r.updateMetrics(ctx)
	return ctrl.Result{RequeueAfter: 6 * time.Hour}, nil
}

func (r *BackupRecordReconciler) updateMetrics(ctx context.Context) {
	list := &protectionv1alpha1.BackupRecordList{}
	if err := r.List(ctx, list); err != nil {
		return
	}
	metrics.BackupRecordTotal.Reset()
	var bytes int64
	counts := map[string]int{}
	for i := range list.Items {
		if r.ClusterRef != "" && list.Items[i].Spec.ClusterRef != r.ClusterRef {
			continue
		}
		counts[list.Items[i].Status.Phase]++
		bytes += list.Items[i].Spec.Inventory.BackupBytes
	}
	for phase, count := range counts {
		metrics.BackupRecordTotal.WithLabelValues(phase).Set(float64(count))
	}
	metrics.BackupRecordBytes.Set(float64(bytes))
}

func (r *BackupRecordReconciler) markBroken(ctx context.Context, record *protectionv1alpha1.BackupRecord, before *protectionv1alpha1.BackupRecord, message string) (ctrl.Result, error) {
	record.Status.Phase, record.Status.Restorable, record.Status.ErrorCode, record.Status.Reason, record.Status.Message = protectionv1alpha1.RecordPhaseBroken, false, opererrors.CodeRecordBroken, "IntegrityCheckFailed", message
	conditions.False(&record.Status.Conditions, record.Generation, protectionv1alpha1.ConditionVerified, "IntegrityCheckFailed", message)
	return ctrl.Result{RequeueAfter: time.Hour}, statusPatch(ctx, r.Client, record, before)
}

func (r *BackupRecordReconciler) finalize(ctx context.Context, record *protectionv1alpha1.BackupRecord) (ctrl.Result, error) {
	if !containsString(record.Finalizers, protectionv1alpha1.RecordFinalizer) {
		return ctrl.Result{}, nil
	}
	if record.Annotations[protectionv1alpha1.AnnotationProtected] == "true" && record.Annotations[protectionv1alpha1.AnnotationForceDel] != "true" {
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	mode := record.Annotations[protectionv1alpha1.AnnotationDeleteMode]
	if mode == "" {
		mode = protectionv1alpha1.DeleteModeRecordOnly
	}
	if mode != protectionv1alpha1.DeleteModeRecordOnly && mode != protectionv1alpha1.DeleteModeRepositoryData && mode != protectionv1alpha1.DeleteModeRepositoryDataAndSnapshots {
		return ctrl.Result{}, fmt.Errorf("invalid deletion mode %q", mode)
	}
	beforeStatus := record.DeepCopy()
	now := metav1.Now()
	record.Status.Phase = protectionv1alpha1.RecordPhaseDeleting
	record.Status.Deletion.Mode, record.Status.Deletion.StartedAt = mode, &now
	_ = statusPatch(ctx, r.Client, record, beforeStatus)
	if mode != protectionv1alpha1.DeleteModeRecordOnly {
		repositoryCR, err := getRepository(ctx, r.Client, record.Spec.RepositoryRef.Name)
		if err != nil {
			return ctrl.Result{}, err
		}
		adapter, err := r.Factory.New(ctx, repositoryCR)
		if err != nil {
			return ctrl.Result{}, err
		}
		defer adapter.Close()
		for _, name := range []string{".done", "sha256sum.txt", "resources.tar.gz", "metadata.json", "index.json", "snapshots.json"} {
			if err = adapter.Delete(ctx, record.Spec.BackupPath+"/"+name); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		if err = adapter.Delete(ctx, record.Spec.BackupPath); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		record.Status.Deletion.RepositoryDataDeleted = true
	}
	if mode == protectionv1alpha1.DeleteModeRepositoryDataAndSnapshots {
		for _, result := range record.Spec.Snapshots {
			if err := r.Snapshots.Delete(ctx, result); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			record.Status.Deletion.SnapshotsDeleted++
		}
	}
	before := record.DeepCopy()
	record.Finalizers = removeString(record.Finalizers, protectionv1alpha1.RecordFinalizer)
	return ctrl.Result{}, r.Patch(ctx, record, client.MergeFrom(before))
}

func (r *BackupRecordReconciler) SetupWithManager(manager ctrl.Manager) error {
	r.Factory.Client = r.Client
	return ctrl.NewControllerManagedBy(manager).For(&protectionv1alpha1.BackupRecord{}).Complete(r)
}
