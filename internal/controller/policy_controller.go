// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/collector"
	"github.com/example/backup-restore-operator/internal/conditions"
	opererrors "github.com/example/backup-restore-operator/internal/errors"
	"github.com/example/backup-restore-operator/internal/scheduler"
	"github.com/example/backup-restore-operator/internal/snapshot"
)

// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuptasks,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch

type PolicyReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Now        func() time.Time
	ClusterRef string
	Resolver   *collector.Resolver
	Snapshots  *snapshot.Manager
}

func (r *PolicyReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &protectionv1alpha1.BackupPolicy{}
	if err := r.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.ClusterRef != "" && object.Spec.ClusterRef != r.ClusterRef {
		return ctrl.Result{}, nil
	}
	if !object.DeletionTimestamp.IsZero() {
		if containsString(object.Finalizers, protectionv1alpha1.PolicyFinalizer) {
			before := object.DeepCopy()
			object.Finalizers = removeString(object.Finalizers, protectionv1alpha1.PolicyFinalizer)
			return ctrl.Result{}, r.Patch(ctx, object, client.MergeFrom(before))
		}
		return ctrl.Result{}, nil
	}
	if !containsString(object.Finalizers, protectionv1alpha1.PolicyFinalizer) {
		before := object.DeepCopy()
		object.Finalizers = append(object.Finalizers, protectionv1alpha1.PolicyFinalizer)
		if err := r.Patch(ctx, object, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}

	repository := &protectionv1alpha1.BackupRepository{}
	if err := r.Get(ctx, client.ObjectKey{Name: object.Spec.RepositoryRef.Name}, repository); err != nil {
		return r.invalid(ctx, object, "RepositoryUnavailable", err)
	}
	if repository.Spec.ClusterRef != object.Spec.ClusterRef {
		return r.invalid(ctx, object, "ReferenceBoundaryMismatch", opererrors.New(opererrors.CodePermissionDenied, "repository is outside policy cluster boundary", false, nil))
	}
	if object.Spec.Selection.IncludeSecrets && !repository.Spec.Encryption.Enabled {
		return r.invalid(ctx, object, "EncryptionRequired", opererrors.New(opererrors.CodePermissionDenied, "selections containing Secrets require repository encryption", false, nil))
	}
	before := object.DeepCopy()
	if err := r.refreshSelectionPreview(ctx, object); err != nil {
		return r.invalid(ctx, object, "SelectionPreviewFailed", opererrors.New(opererrors.CodeSelectionDiscovery, "preview selection", true, err))
	}
	parsed, err := scheduler.Parse(object.Spec.Schedule.Cron, object.Spec.Schedule.Timezone)
	if err != nil {
		return r.invalid(ctx, object, "InvalidSchedule", opererrors.New(opererrors.CodePolicyCron, "invalid schedule", false, err))
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	object.Status.ObservedGeneration = object.Generation
	object.Status.ResolvedRepositoryUID = string(repository.UID)
	if !object.Spec.Enabled || object.Spec.Suspend {
		object.Status.Phase, object.Status.Reason, object.Status.Message = protectionv1alpha1.PolicyPhasePaused, "Suspended", "policy scheduling is suspended"
		object.Status.NextScheduleTime = nil
		conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionScheduled, "Suspended", object.Status.Message)
		return ctrl.Result{}, statusPatch(ctx, r.Client, object, before)
	}
	if repository.Status.Phase != protectionv1alpha1.RepositoryPhaseReady {
		object.Status.Phase, object.Status.Reason, object.Status.Message = protectionv1alpha1.PolicyPhaseDegraded, "DependencyNotReady", "repository must be Ready"
		conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "DependencyNotReady", object.Status.Message)
		if err := statusPatch(ctx, r.Client, object, before); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	active, err := r.activeTasks(ctx, object)
	if err != nil {
		return ctrl.Result{}, err
	}
	object.Status.ActiveTasks = active
	last := object.CreationTimestamp.Time
	if object.Status.LastEvaluatedScheduleTime != nil {
		last = object.Status.LastEvaluatedScheduleTime.Time
	}
	due := parsed.DueTimes(last, now, object.Spec.StartingDeadline.Duration, object.Spec.MissedRunPolicy, int(object.Spec.MaxCatchUpRuns))
	for _, scheduledAt := range due {
		if len(active) > 0 && object.Spec.ConcurrencyPolicy == protectionv1alpha1.ConcurrencyForbid {
			object.Status.SkippedRuns = appendLimitedSkipped(object.Status.SkippedRuns, scheduledAt, "ConcurrencyForbid")
			continue
		}
		if len(active) > 0 && object.Spec.ConcurrencyPolicy == protectionv1alpha1.ConcurrencyReplace {
			if err := r.cancelActive(ctx, active); err != nil {
				return ctrl.Result{}, err
			}
		}
		when := metav1.NewTime(scheduledAt)
		backupSpec := policyExecutionSpec(object)
		backupSpec.RepositoryRef.UID = string(repository.UID)
		backupSpecHash, hashErr := backupExecutionSpecHash(backupSpec)
		if hashErr != nil {
			return ctrl.Result{}, hashErr
		}
		task := &protectionv1alpha1.BackupTask{
			ObjectMeta: metav1.ObjectMeta{
				Name: scheduler.DeterministicTaskName(object.Name, scheduledAt),
				Labels: map[string]string{
					protectionv1alpha1.LabelPolicyUID:   string(object.UID),
					protectionv1alpha1.LabelScheduledAt: strconv.FormatInt(scheduledAt.Unix(), 10),
					protectionv1alpha1.LabelTrigger:     protectionv1alpha1.BackupTriggerSchedule,
					protectionv1alpha1.LabelCluster:     object.Spec.ClusterRef,
				},
			},
			Spec: protectionv1alpha1.BackupTaskSpec{
				ResourceIdentity:     object.Spec.ResourceIdentity,
				Trigger:              protectionv1alpha1.BackupTriggerSchedule,
				Source:               protectionv1alpha1.BackupTaskSource{Type: protectionv1alpha1.BackupTaskSourcePolicy, PolicyRef: &protectionv1alpha1.ObjectReference{Name: object.Name, UID: string(object.UID)}},
				ScheduledAt:          &when,
				BackupSpec:           backupSpec,
				BackupSpecHash:       backupSpecHash,
				PolicyGeneration:     object.Generation,
				RepositoryGeneration: repository.Generation,
				IdempotencyKey:       scheduler.ScheduledKey(string(object.UID), scheduledAt),
			},
		}
		createErr := r.Create(ctx, task)
		if createErr != nil && !apierrors.IsAlreadyExists(createErr) {
			return ctrl.Result{}, createErr
		}
		if apierrors.IsAlreadyExists(createErr) {
			existing := &protectionv1alpha1.BackupTask{}
			if getErr := r.Get(ctx, client.ObjectKey{Name: task.Name}, existing); getErr != nil {
				return ctrl.Result{}, getErr
			}
			if !terminalBackup(existing.Status.Phase) {
				active = append(active, protectionv1alpha1.ObjectReference{Name: existing.Name, UID: string(existing.UID)})
			}
		} else {
			active = append(active, protectionv1alpha1.ObjectReference{Name: task.Name, UID: string(task.UID)})
		}
		object.Status.LastScheduleTime = &when
	}
	evaluated := metav1.NewTime(now)
	next := metav1.NewTime(parsed.Next(now))
	object.Status.LastEvaluatedScheduleTime, object.Status.NextScheduleTime = &evaluated, &next
	object.Status.Phase, object.Status.Reason, object.Status.Message = protectionv1alpha1.PolicyPhaseReady, "ScheduleActive", fmt.Sprintf("next run at %s", next.UTC().Format(time.RFC3339))
	conditions.True(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionScheduled, "ScheduleActive", object.Status.Message)
	conditions.True(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "DependenciesReady", "selection and repository are ready")
	if err := statusPatch(ctx, r.Client, object, before); err != nil {
		return ctrl.Result{}, err
	}
	delay := time.Until(next.Time)
	if delay > 10*time.Minute {
		delay = 10 * time.Minute
	}
	if delay < time.Second {
		delay = time.Second
	}
	return ctrl.Result{RequeueAfter: delay}, nil
}

func (r *PolicyReconciler) invalid(ctx context.Context, object *protectionv1alpha1.BackupPolicy, reason string, err error) (ctrl.Result, error) {
	before := object.DeepCopy()
	object.Status.ObservedGeneration, object.Status.Phase, object.Status.ErrorCode, object.Status.Reason, object.Status.Message = object.Generation, protectionv1alpha1.PolicyPhaseInvalid, opererrors.Code(err), reason, err.Error()
	conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, reason, err.Error())
	if patchErr := statusPatch(ctx, r.Client, object, before); patchErr != nil {
		return ctrl.Result{}, patchErr
	}
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *PolicyReconciler) activeTasks(ctx context.Context, policy *protectionv1alpha1.BackupPolicy) ([]protectionv1alpha1.ObjectReference, error) {
	list := &protectionv1alpha1.BackupTaskList{}
	if err := r.List(ctx, list, client.MatchingLabels{protectionv1alpha1.LabelPolicyUID: string(policy.UID)}); err != nil {
		return nil, err
	}
	result := make([]protectionv1alpha1.ObjectReference, 0)
	for i := range list.Items {
		if !terminalBackup(list.Items[i].Status.Phase) {
			result = append(result, protectionv1alpha1.ObjectReference{Name: list.Items[i].Name, UID: string(list.Items[i].UID)})
		}
	}
	return result, nil
}

func (r *PolicyReconciler) cancelActive(ctx context.Context, active []protectionv1alpha1.ObjectReference) error {
	for _, ref := range active {
		task := &protectionv1alpha1.BackupTask{}
		if err := r.Get(ctx, client.ObjectKey{Name: ref.Name}, task); err != nil {
			return client.IgnoreNotFound(err)
		}
		before := task.DeepCopy()
		task.Spec.CancelRequested, task.Spec.CancelReason = true, "replaced by a newer scheduled run"
		if err := r.Patch(ctx, task, client.MergeFrom(before)); err != nil {
			return err
		}
	}
	return nil
}

func appendLimitedSkipped(values []protectionv1alpha1.SkippedRun, when time.Time, reason string) []protectionv1alpha1.SkippedRun {
	values = append(values, protectionv1alpha1.SkippedRun{ScheduledAt: metav1.NewTime(when), Reason: reason})
	if len(values) > 20 {
		values = values[len(values)-20:]
	}
	return values
}

func (r *PolicyReconciler) taskToPolicy(_ context.Context, object client.Object) []reconcile.Request {
	task, ok := object.(*protectionv1alpha1.BackupTask)
	if !ok || task.Spec.Source.PolicyRef == nil || task.Spec.Source.PolicyRef.Name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: task.Spec.Source.PolicyRef.Name}}}
}

func (r *PolicyReconciler) refreshSelectionPreview(ctx context.Context, object *protectionv1alpha1.BackupPolicy) error {
	if r.Resolver == nil {
		return nil
	}
	if object.Status.ObservedGeneration == object.Generation && object.Status.SelectionPreview.GeneratedAt != nil && time.Since(object.Status.SelectionPreview.GeneratedAt.Time) < 10*time.Minute {
		return nil
	}
	preview, _, err := r.Resolver.Preview(ctx, object.Spec.Selection)
	if err != nil {
		return err
	}
	if object.Spec.Selection.PVC.Enabled && preview.PVCCount > 0 && r.Snapshots != nil {
		preview.SnapshotCapablePVCCount, preview.UnsupportedPVCCount = r.previewSnapshotCapabilities(ctx, object.Spec.Selection)
	}
	now := metav1.Now()
	payload, _ := json.Marshal(object.Spec.Selection)
	hash := sha256.Sum256(payload)
	object.Status.SelectionPreview = protectionv1alpha1.SelectionPreviewStatus{
		NamespaceCount: preview.NamespaceCount, ResourceTypeCount: preview.ResourceTypeCount,
		ResourceObjectCount: preview.ResourceObjectCount, PVCCount: preview.PVCCount,
		SnapshotCapablePVCCount: preview.SnapshotCapablePVCCount, UnsupportedPVCCount: preview.UnsupportedPVCCount,
		RiskCount: int64(len(preview.Warnings)), GeneratedAt: &now, ResolvedHash: hex.EncodeToString(hash[:]),
	}
	conditions.True(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionSelectionResolved, "Resolved", fmt.Sprintf("resolved %d resource types and %d objects", preview.ResourceTypeCount, preview.ResourceObjectCount))
	return nil
}

func (r *PolicyReconciler) previewSnapshotCapabilities(ctx context.Context, selection protectionv1alpha1.BackupSelectionSpec) (int64, int64) {
	selector := ""
	if selection.PVC.LabelSelector != nil {
		if parsed, err := metav1.LabelSelectorAsSelector(selection.PVC.LabelSelector); err == nil {
			selector = parsed.String()
		}
	} else if selection.LabelSelector != nil {
		if parsed, err := metav1.LabelSelectorAsSelector(selection.LabelSelector); err == nil {
			selector = parsed.String()
		}
	}
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	var capable, unsupported int64
	for _, namespace := range collector.IncludedNamespaces(selection) {
		list, err := r.Snapshots.Dynamic.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			continue
		}
		for i := range list.Items {
			if !pvcNameSelected(selection.PVC, list.Items[i].GetNamespace(), list.Items[i].GetName()) {
				continue
			}
			_, err = r.Snapshots.Detect(ctx, list.Items[i].GetNamespace(), list.Items[i].GetName(), selection.PVC.SnapshotClassName, selection.PVC.SnapshotClassMapping)
			if err != nil {
				unsupported++
			} else {
				capable++
			}
		}
	}
	return capable, unsupported
}

func (r *PolicyReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).For(&protectionv1alpha1.BackupPolicy{}).Watches(&protectionv1alpha1.BackupTask{}, handler.EnqueueRequestsFromMapFunc(r.taskToPolicy)).Complete(r)
}
