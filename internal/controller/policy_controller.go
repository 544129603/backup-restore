// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/conditions"
	opererrors "github.com/example/backup-restore-operator/internal/errors"
	"github.com/example/backup-restore-operator/internal/scheduler"
)

// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprepositories;backupscopes,verbs=get;list;watch
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuptasks,verbs=get;list;watch;create;update;patch

type PolicyReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Now        func() time.Time
	ClusterRef string
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

	scope := &protectionv1alpha1.BackupScope{}
	repository := &protectionv1alpha1.BackupRepository{}
	if err := r.Get(ctx, client.ObjectKey{Name: object.Spec.ScopeRef.Name}, scope); err != nil {
		return r.invalid(ctx, object, "ScopeUnavailable", err)
	}
	if err := r.Get(ctx, client.ObjectKey{Name: object.Spec.RepositoryRef.Name}, repository); err != nil {
		return r.invalid(ctx, object, "RepositoryUnavailable", err)
	}
	if scope.Spec.ClusterRef != object.Spec.ClusterRef || repository.Spec.ClusterRef != object.Spec.ClusterRef {
		return r.invalid(ctx, object, "ReferenceBoundaryMismatch", opererrors.New(opererrors.CodePermissionDenied, "scope or repository is outside policy cluster boundary", false, nil))
	}
	if scope.Spec.IncludeSecrets && !repository.Spec.Encryption.Enabled {
		return r.invalid(ctx, object, "EncryptionRequired", opererrors.New(opererrors.CodePermissionDenied, "scopes containing Secrets require repository encryption", false, nil))
	}
	parsed, err := scheduler.Parse(object.Spec.Schedule.Cron, object.Spec.Schedule.Timezone)
	if err != nil {
		return r.invalid(ctx, object, "InvalidSchedule", opererrors.New(opererrors.CodePolicyCron, "invalid schedule", false, err))
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	before := object.DeepCopy()
	object.Status.ObservedGeneration = object.Generation
	object.Status.ResolvedScopeUID, object.Status.ResolvedRepositoryUID = string(scope.UID), string(repository.UID)
	if !object.Spec.Enabled || object.Spec.Suspend {
		object.Status.Phase, object.Status.Reason, object.Status.Message = protectionv1alpha1.PolicyPhasePaused, "Suspended", "policy scheduling is suspended"
		object.Status.NextScheduleTime = nil
		conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionScheduled, "Suspended", object.Status.Message)
		return ctrl.Result{}, statusPatch(ctx, r.Client, object, before)
	}
	if scope.Status.Phase != protectionv1alpha1.ScopePhaseReady || repository.Status.Phase != protectionv1alpha1.RepositoryPhaseReady {
		object.Status.Phase, object.Status.Reason, object.Status.Message = protectionv1alpha1.PolicyPhaseDegraded, "DependencyNotReady", "scope and repository must both be Ready"
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
		task := &protectionv1alpha1.BackupTask{ObjectMeta: metav1.ObjectMeta{Name: scheduler.DeterministicTaskName(object.Name, scheduledAt), Labels: map[string]string{protectionv1alpha1.LabelPolicyUID: string(object.UID), protectionv1alpha1.LabelScheduledAt: strconv.FormatInt(scheduledAt.Unix(), 10), protectionv1alpha1.LabelTrigger: protectionv1alpha1.BackupTriggerSchedule, protectionv1alpha1.LabelCluster: object.Spec.ClusterRef}}, Spec: protectionv1alpha1.BackupTaskSpec{ResourceIdentity: object.Spec.ResourceIdentity, Trigger: protectionv1alpha1.BackupTriggerSchedule, PolicyRef: &protectionv1alpha1.ObjectReference{Name: object.Name, UID: string(object.UID)}, ScheduledAt: &when, ScopeRef: object.Spec.ScopeRef, RepositoryRef: object.Spec.RepositoryRef, ScopeSnapshot: scope.Spec.DeepCopy(), ScopeGeneration: scope.Generation, RepositoryGeneration: repository.Generation, Timeout: object.Spec.Timeout, RetryPolicy: object.Spec.RetryPolicy, FailurePolicy: "Continue", AllowPartialRecord: true, IdempotencyKey: scheduler.ScheduledKey(string(object.UID), scheduledAt)}}
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
	conditions.True(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "DependenciesReady", "scope and repository are ready")
	if err := statusPatch(ctx, r.Client, object, before); err != nil {
		return ctrl.Result{}, err
	}
	delay := time.Until(next.Time)
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
	if !ok || task.Spec.PolicyRef == nil {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: task.Spec.PolicyRef.Name}}}
}

func (r *PolicyReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).For(&protectionv1alpha1.BackupPolicy{}).Watches(&protectionv1alpha1.BackupTask{}, handler.EnqueueRequestsFromMapFunc(r.taskToPolicy)).Complete(r)
}
