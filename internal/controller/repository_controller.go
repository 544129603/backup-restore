// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/conditions"
	opererrors "github.com/example/backup-restore-operator/internal/errors"
	"github.com/example/backup-restore-operator/internal/metrics"
	repofactory "github.com/example/backup-restore-operator/internal/repository/factory"
)

// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuprepositories/finalizers,verbs=update
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppolicies;backuprecords,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets;persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type RepositoryReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Recorder   record.EventRecorder
	Factory    repofactory.Factory
	ClusterRef string
}

func (r *RepositoryReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	metrics.ReconcileTotal.WithLabelValues("repository", "attempt").Inc()
	object := &protectionv1alpha1.BackupRepository{}
	if err := r.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	ctrl.LoggerFrom(ctx).Info("checking backup repository", "repository", object.Name, "type", object.Spec.Type, "clusterRef", object.Spec.ClusterRef)
	if r.ClusterRef != "" && object.Spec.ClusterRef != r.ClusterRef {
		return ctrl.Result{}, nil
	}
	if !object.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, object)
	}
	if !containsString(object.Finalizers, protectionv1alpha1.RepositoryFinalizer) {
		before := object.DeepCopy()
		object.Finalizers = append(object.Finalizers, protectionv1alpha1.RepositoryFinalizer)
		if err := r.Patch(ctx, object, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}

	before := object.DeepCopy()
	now := metav1.Now()
	object.Status.ObservedGeneration = object.Generation
	object.Status.LastCheckTime = &now
	object.Status.Phase = protectionv1alpha1.RepositoryPhaseChecking
	policies, records := &protectionv1alpha1.BackupPolicyList{}, &protectionv1alpha1.BackupRecordList{}
	if r.List(ctx, policies) == nil {
		object.Status.ActivePolicyCount = 0
		for i := range policies.Items {
			if policies.Items[i].Spec.RepositoryRef.Name == object.Name && policies.Items[i].Spec.Enabled && !policies.Items[i].Spec.Suspend {
				object.Status.ActivePolicyCount++
			}
		}
	}
	if r.List(ctx, records) == nil {
		object.Status.RecordCount = 0
		for i := range records.Items {
			if records.Items[i].Spec.RepositoryRef.Name == object.Name {
				object.Status.RecordCount++
			}
		}
	}
	conditions.Unknown(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "Checking", "repository read/write health check is running")
	if !object.Spec.Enabled {
		object.Status.Phase = protectionv1alpha1.RepositoryPhaseDegraded
		conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "Disabled", "repository is disabled")
		if err := statusPatch(ctx, r.Client, object, before); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: object.Spec.HealthCheckInterval.Duration}, nil
	}

	var err error
	if object.Spec.Type == protectionv1alpha1.RepositoryTypeLocal && object.Spec.Local != nil {
		if object.Spec.Local.Mode == protectionv1alpha1.LocalModeHostPath {
			object.Status.ResolvedNodeName = object.Spec.Local.NodeName
			if currentNode := os.Getenv("NODE_NAME"); object.Spec.Local.NodeName != "" && currentNode != "" && currentNode != object.Spec.Local.NodeName {
				err = opererrors.New(opererrors.CodeRepoPath, fmt.Sprintf("repository is pinned to node %s but Operator runs on %s", object.Spec.Local.NodeName, currentNode), false, nil)
			}
		} else if object.Spec.Local.Mode == protectionv1alpha1.LocalModePVC && object.Spec.Local.PVC != nil {
			pvc := &corev1.PersistentVolumeClaim{}
			if getErr := r.Get(ctx, types.NamespacedName{Namespace: object.Spec.Local.PVC.Namespace, Name: object.Spec.Local.PVC.Name}, pvc); getErr != nil {
				err = opererrors.New(opererrors.CodeRepoPath, "read Local repository PVC", true, getErr)
			} else {
				object.Status.PVCPhase = pvc.Status.Phase
				if pvc.Status.Phase != corev1.ClaimBound {
					err = opererrors.New(opererrors.CodeRepoPath, "Local repository PVC is not Bound", true, nil)
				}
			}
		}
	}
	var adapter interface {
		Check(context.Context) error
		AvailableBytes(context.Context) (int64, bool, error)
		SupportsAtomicRename() bool
		Close() error
	}
	if err == nil {
		adapter, err = r.Factory.New(ctx, object)
	}
	if err == nil {
		defer adapter.Close()
		err = adapter.Check(ctx)
	}
	if err != nil {
		code := opererrors.Code(err)
		if code == opererrors.CodeInternal {
			code = opererrors.CodeRepoConnect
		}
		object.Status.Phase, object.Status.ErrorCode, object.Status.Reason, object.Status.Message = protectionv1alpha1.RepositoryPhaseFailed, code, "HealthCheckFailed", err.Error()
		conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "HealthCheckFailed", err.Error())
		metrics.RepositoryAvailable.WithLabelValues(string(object.Spec.Type)).Set(0)
		metrics.RepositoryCheckTotal.WithLabelValues(string(object.Spec.Type), "failed").Inc()
		metrics.RepositoryCheckFailed.WithLabelValues(string(object.Spec.Type), code).Inc()
		if r.Recorder != nil {
			r.Recorder.Event(object, corev1.EventTypeWarning, "RepositoryUnavailable", err.Error())
		}
	} else {
		available, known, capacityErr := adapter.AvailableBytes(ctx)
		object.Status.CapacityKnown, object.Status.AvailableBytes = known, available
		if known {
			metrics.RepositoryAvailableBytes.WithLabelValues(string(object.Spec.Type)).Set(float64(available))
		}
		if capacityErr != nil {
			object.Status.Message = capacityErr.Error()
		}
		if known && object.Spec.MinimumFreeBytes != nil && available < object.Spec.MinimumFreeBytes.Value() {
			err = opererrors.New(opererrors.CodeRepoNoSpace, fmt.Sprintf("available bytes %d below required %d", available, object.Spec.MinimumFreeBytes.Value()), true, nil)
			object.Status.Phase, object.Status.ErrorCode, object.Status.Reason, object.Status.Message = protectionv1alpha1.RepositoryPhaseDegraded, opererrors.CodeRepoNoSpace, "InsufficientCapacity", err.Error()
			conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "InsufficientCapacity", err.Error())
			metrics.RepositoryAvailable.WithLabelValues(string(object.Spec.Type)).Set(0)
		} else {
			object.Status.Phase, object.Status.ErrorCode, object.Status.Reason, object.Status.Message = protectionv1alpha1.RepositoryPhaseReady, "", "HealthCheckSucceeded", "repository is readable and writable"
			object.Status.LastSuccessfulCheckTime = &now
			object.Status.Capabilities = protectionv1alpha1.RepositoryCapabilities{Read: true, Write: true, Delete: true, AtomicRename: adapter.SupportsAtomicRename(), Capacity: known}
			conditions.True(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "HealthCheckSucceeded", object.Status.Message)
			metrics.RepositoryAvailable.WithLabelValues(string(object.Spec.Type)).Set(1)
		}
		metrics.RepositoryCheckTotal.WithLabelValues(string(object.Spec.Type), "succeeded").Inc()
	}
	if patchErr := statusPatch(ctx, r.Client, object, before); patchErr != nil {
		return ctrl.Result{}, patchErr
	}
	interval := object.Spec.HealthCheckInterval.Duration
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	return ctrl.Result{RequeueAfter: interval}, nil
}

func (r *RepositoryReconciler) finalize(ctx context.Context, object *protectionv1alpha1.BackupRepository) (ctrl.Result, error) {
	if !containsString(object.Finalizers, protectionv1alpha1.RepositoryFinalizer) {
		return ctrl.Result{}, nil
	}
	policies := &protectionv1alpha1.BackupPolicyList{}
	records := &protectionv1alpha1.BackupRecordList{}
	if err := r.List(ctx, policies); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.List(ctx, records); err != nil {
		return ctrl.Result{}, err
	}
	refs := 0
	for i := range policies.Items {
		if policies.Items[i].Spec.RepositoryRef.Name == object.Name {
			refs++
		}
	}
	for i := range records.Items {
		if records.Items[i].Spec.RepositoryRef.Name == object.Name {
			refs++
		}
	}
	if refs > 0 {
		before := object.DeepCopy()
		object.Status.Phase, object.Status.Reason, object.Status.Message = protectionv1alpha1.RepositoryPhaseDeleting, "DeletionBlocked", fmt.Sprintf("repository is referenced by %d objects", refs)
		conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "DeletionBlocked", object.Status.Message)
		_ = statusPatch(ctx, r.Client, object, before)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	if object.Spec.DeletionProtection && object.Annotations[protectionv1alpha1.AnnotationForceDel] != "true" {
		before := object.DeepCopy()
		object.Status.Phase, object.Status.Reason, object.Status.Message = protectionv1alpha1.RepositoryPhaseDeleting, "DeletionProtectionEnabled", "set force-delete=true only after confirming this unreferenced repository can be removed"
		_ = statusPatch(ctx, r.Client, object, before)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	before := object.DeepCopy()
	object.Finalizers = removeString(object.Finalizers, protectionv1alpha1.RepositoryFinalizer)
	return ctrl.Result{}, r.Patch(ctx, object, client.MergeFrom(before))
}

func (r *RepositoryReconciler) secretToRepositories(ctx context.Context, object client.Object) []reconcile.Request {
	secret, ok := object.(*corev1.Secret)
	if !ok {
		return nil
	}
	list := &protectionv1alpha1.BackupRepositoryList{}
	if err := r.List(ctx, list); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0)
	for i := range list.Items {
		if repositoryUsesSecret(&list.Items[i], secret.Namespace, secret.Name) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Name: list.Items[i].Name}})
		}
	}
	return requests
}

func repositoryUsesSecret(repository *protectionv1alpha1.BackupRepository, namespace, name string) bool {
	refs := []*protectionv1alpha1.SecretKeyReference{repository.Spec.Encryption.KeyRef}
	if repository.Spec.SFTP != nil {
		refs = append(refs, &repository.Spec.SFTP.Auth.UsernameRef, repository.Spec.SFTP.Auth.PasswordRef, repository.Spec.SFTP.Auth.PrivateKeyRef, repository.Spec.SFTP.Auth.PassphraseRef, repository.Spec.SFTP.KnownHostsRef)
	}
	for _, ref := range refs {
		if ref != nil && ref.Namespace == namespace && ref.Name == name {
			return true
		}
	}
	return false
}

func (r *RepositoryReconciler) SetupWithManager(manager ctrl.Manager) error {
	r.Factory.Client = r.Client
	return ctrl.NewControllerManagedBy(manager).
		For(&protectionv1alpha1.BackupRepository{}, builder.WithPredicates()).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.secretToRepositories)).
		Complete(r)
}

var _ = apierrors.IsNotFound
