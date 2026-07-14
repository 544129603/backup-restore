// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/collector"
	"github.com/example/backup-restore-operator/internal/conditions"
	opererrors "github.com/example/backup-restore-operator/internal/errors"
	"github.com/example/backup-restore-operator/internal/snapshot"
)

// +kubebuilder:rbac:groups=protection.platform.io,resources=backupscopes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=protection.platform.io,resources=backupscopes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=protection.platform.io,resources=backupscopes/finalizers,verbs=update
// +kubebuilder:rbac:groups=protection.platform.io,resources=backuppolicies,verbs=get;list;watch

type ScopeReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Resolver   *collector.Resolver
	ClusterRef string
	Snapshots  *snapshot.Manager
}

func (r *ScopeReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &protectionv1alpha1.BackupScope{}
	if err := r.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.ClusterRef != "" && object.Spec.ClusterRef != r.ClusterRef {
		return ctrl.Result{}, nil
	}
	if !object.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, object)
	}
	if !containsString(object.Finalizers, protectionv1alpha1.ScopeFinalizer) {
		before := object.DeepCopy()
		object.Finalizers = append(object.Finalizers, protectionv1alpha1.ScopeFinalizer)
		if err := r.Patch(ctx, object, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	before := object.DeepCopy()
	object.Status.ObservedGeneration = object.Generation
	object.Status.Phase = protectionv1alpha1.ScopePhaseResolving
	policies := &protectionv1alpha1.BackupPolicyList{}
	if r.List(ctx, policies) == nil {
		object.Status.ReferencedByPolicies = 0
		for i := range policies.Items {
			if policies.Items[i].Spec.ScopeRef.Name == object.Name {
				object.Status.ReferencedByPolicies++
			}
		}
	}
	conditions.Unknown(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionScopeResolved, "Resolving", "discovering and counting selected resources")
	preview, _, err := r.Resolver.Preview(ctx, object)
	if err != nil {
		object.Status.Phase, object.Status.ErrorCode, object.Status.Reason, object.Status.Message = protectionv1alpha1.ScopePhaseInvalid, opererrors.CodeScopeDiscovery, "PreviewFailed", err.Error()
		conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionScopeResolved, "PreviewFailed", err.Error())
	} else {
		if object.Spec.PVC.Enabled && preview.PVCCount > 0 && r.Snapshots != nil {
			capable, unsupported := r.previewSnapshotCapabilities(ctx, object)
			preview.SnapshotCapablePVCCount, preview.UnsupportedPVCCount = capable, unsupported
		}
		now := metav1.Now()
		payload, _ := json.Marshal(object.Spec)
		hash := sha256.Sum256(payload)
		object.Status.Preview = protectionv1alpha1.ScopePreviewStatus{NamespaceCount: preview.NamespaceCount, ResourceTypeCount: preview.ResourceTypeCount, ResourceObjectCount: preview.ResourceObjectCount, PVCCount: preview.PVCCount, SnapshotCapablePVCCount: preview.SnapshotCapablePVCCount, UnsupportedPVCCount: preview.UnsupportedPVCCount, RiskCount: int64(len(preview.Warnings)), GeneratedAt: &now, ResolvedHash: hex.EncodeToString(hash[:])}
		object.Status.Phase, object.Status.ErrorCode, object.Status.Reason = protectionv1alpha1.ScopePhaseReady, "", "Resolved"
		object.Status.Message = fmt.Sprintf("resolved %d resource types and %d objects", preview.ResourceTypeCount, preview.ResourceObjectCount)
		conditions.True(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionScopeResolved, "Resolved", object.Status.Message)
	}
	if err = statusPatch(ctx, r.Client, object, before); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 10 * time.Minute}, nil
}

func (r *ScopeReconciler) previewSnapshotCapabilities(ctx context.Context, scope *protectionv1alpha1.BackupScope) (int64, int64) {
	selector := ""
	if scope.Spec.PVC.LabelSelector != nil {
		if parsed, err := metav1.LabelSelectorAsSelector(scope.Spec.PVC.LabelSelector); err == nil {
			selector = parsed.String()
		}
	} else if scope.Spec.LabelSelector != nil {
		if parsed, err := metav1.LabelSelectorAsSelector(scope.Spec.LabelSelector); err == nil {
			selector = parsed.String()
		}
	}
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	var capable, unsupported int64
	for _, namespace := range collector.IncludedNamespaces(scope.Spec) {
		list, err := r.Snapshots.Dynamic.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			continue
		}
		for i := range list.Items {
			if !pvcNameSelected(scope.Spec.PVC, list.Items[i].GetNamespace(), list.Items[i].GetName()) {
				continue
			}
			_, err = r.Snapshots.Detect(ctx, list.Items[i].GetNamespace(), list.Items[i].GetName(), scope.Spec.PVC.SnapshotClassName, scope.Spec.PVC.SnapshotClassMapping)
			if err != nil {
				unsupported++
			} else {
				capable++
			}
		}
	}
	return capable, unsupported
}

func (r *ScopeReconciler) finalize(ctx context.Context, object *protectionv1alpha1.BackupScope) (ctrl.Result, error) {
	if !containsString(object.Finalizers, protectionv1alpha1.ScopeFinalizer) {
		return ctrl.Result{}, nil
	}
	list := &protectionv1alpha1.BackupPolicyList{}
	if err := r.List(ctx, list); err != nil {
		return ctrl.Result{}, err
	}
	refs := 0
	for i := range list.Items {
		if list.Items[i].Spec.ScopeRef.Name == object.Name && list.Items[i].Spec.Enabled && !list.Items[i].Spec.Suspend {
			refs++
		}
	}
	if refs > 0 {
		before := object.DeepCopy()
		object.Status.Phase, object.Status.Reason, object.Status.Message = protectionv1alpha1.ScopePhaseDeleting, "DeletionBlocked", fmt.Sprintf("scope is referenced by %d enabled policies", refs)
		object.Status.ReferencedByPolicies = int32(refs)
		conditions.False(&object.Status.Conditions, object.Generation, protectionv1alpha1.ConditionReady, "DeletionBlocked", object.Status.Message)
		_ = statusPatch(ctx, r.Client, object, before)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	before := object.DeepCopy()
	object.Finalizers = removeString(object.Finalizers, protectionv1alpha1.ScopeFinalizer)
	return ctrl.Result{}, r.Patch(ctx, object, client.MergeFrom(before))
}

func (r *ScopeReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).For(&protectionv1alpha1.BackupScope{}).Complete(r)
}
