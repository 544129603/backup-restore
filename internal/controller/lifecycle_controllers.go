// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/conditions"
	"github.com/example/backup-restore-operator/internal/retention"
)

// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppolicies;backuprecords,verbs=get;list;watch;update;patch;delete

type RetentionReconciler struct {
	client.Client
	Now        func() time.Time
	ClusterRef string
}

func (r *RetentionReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	policy := &protectionv1alpha1.BackupPolicy{}
	if err := r.Get(ctx, request.NamespacedName, policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.ClusterRef != "" && policy.Spec.ClusterRef != r.ClusterRef {
		return ctrl.Result{}, nil
	}
	if !policy.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	list := &protectionv1alpha1.BackupRecordList{}
	if err := r.List(ctx, list); err != nil {
		return ctrl.Result{}, err
	}
	records := make([]protectionv1alpha1.BackupRecord, 0)
	for i := range list.Items {
		if list.Items[i].Spec.PolicyRef.UID == string(policy.UID) {
			records = append(records, list.Items[i])
		}
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	for _, candidate := range retention.Select(records, policy.Spec.Retention, now) {
		current := &protectionv1alpha1.BackupRecord{}
		if err := r.Get(ctx, client.ObjectKey{Name: candidate.Name}, current); err != nil {
			continue
		}
		before := current.DeepCopy()
		if current.Annotations == nil {
			current.Annotations = map[string]string{}
		}
		current.Annotations[protectionv1alpha1.AnnotationDeleteMode] = protectionv1alpha1.DeleteModeRepositoryData
		current.Annotations[protectionv1alpha1.AnnotationDeleteConfirmed] = "true"
		if policy.Spec.Retention.DeleteSnapshots {
			current.Annotations[protectionv1alpha1.AnnotationDeleteMode] = protectionv1alpha1.DeleteModeRepositoryDataAndSnapshots
		}
		if err := r.Patch(ctx, current, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Delete(ctx, current); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: time.Hour}, nil
}

func (r *RetentionReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).Named("retention").For(&protectionv1alpha1.BackupPolicy{}).Complete(r)
}

// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppluginconfigs,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppluginconfigs/status,verbs=get;update;patch

type BackupPluginConfigReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Version string
}

func (r *BackupPluginConfigReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &protectionv1alpha1.BackupPluginConfig{}
	if err := r.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	payload, err := json.Marshal(object.Spec)
	if err != nil {
		return ctrl.Result{}, err
	}
	hash := sha256.Sum256(payload)
	before := object.DeepCopy()
	now := metav1.Now()
	object.Status.ObservedGeneration, object.Status.Phase, object.Status.Reason, object.Status.Message = object.Generation, "Ready", "ConfigApplied", "global defaults validated and observed"
	object.Status.EffectiveConfigHash, object.Status.LastAppliedAt, object.Status.OperatorVersion = hex.EncodeToString(hash[:]), &now, r.Version
	conditions.True(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "ConfigApplied", object.Status.Message)
	return ctrl.Result{}, statusPatch(ctx, r.Client, object, before)
}

func (r *BackupPluginConfigReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).Named("plugin-config").For(&protectionv1alpha1.BackupPluginConfig{}).Complete(r)
}

// +kubebuilder:rbac:groups=protection.platform.io,resources=backuptasks;restoretasks;backuppluginconfigs,verbs=get;list;watch;delete

type GarbageCollectionReconciler struct {
	client.Client
	Workspace  string
	Now        func() time.Time
	ClusterRef string
}

func (r *GarbageCollectionReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	config := &protectionv1alpha1.BackupPluginConfig{}
	if err := r.Get(ctx, request.NamespacedName, config); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	cutoff := now.Add(-time.Duration(config.Spec.GarbageCollection.TerminalTaskTTLDays) * 24 * time.Hour)
	backups := &protectionv1alpha1.BackupTaskList{}
	if err := r.List(ctx, backups); err != nil {
		return ctrl.Result{}, err
	}
	for i := range backups.Items {
		if (r.ClusterRef == "" || backups.Items[i].Spec.ClusterRef == r.ClusterRef) && terminalBackup(backups.Items[i].Status.Phase) && backups.Items[i].CreationTimestamp.Time.Before(cutoff) {
			if err := r.Delete(ctx, &backups.Items[i]); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
		}
	}
	restores := &protectionv1alpha1.RestoreTaskList{}
	if err := r.List(ctx, restores); err != nil {
		return ctrl.Result{}, err
	}
	for i := range restores.Items {
		if (r.ClusterRef == "" || restores.Items[i].Spec.ClusterRef == r.ClusterRef) && terminalRestore(restores.Items[i].Status.Phase) && restores.Items[i].CreationTimestamp.Time.Before(cutoff) {
			if err := r.Delete(ctx, &restores.Items[i]); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
		}
	}
	workspace := r.Workspace
	if workspace == "" {
		workspace = config.Spec.WorkspacePath
	}
	entries, _ := os.ReadDir(workspace)
	stagingCutoff := now.Add(-config.Spec.GarbageCollection.StagingGracePeriod.Duration)
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && info.IsDir() && info.ModTime().Before(stagingCutoff) {
			_ = os.RemoveAll(filepath.Join(workspace, entry.Name()))
		}
	}
	interval := config.Spec.GarbageCollection.Interval.Duration
	if interval <= 0 {
		interval = time.Hour
	}
	return ctrl.Result{RequeueAfter: interval}, nil
}

func (r *GarbageCollectionReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).Named("garbage-collection").For(&protectionv1alpha1.BackupPluginConfig{}).Complete(r)
}
