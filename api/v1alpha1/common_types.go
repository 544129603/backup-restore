// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	LabelCluster              = "protection.platform.io/cluster"
	LabelPolicyUID            = "protection.platform.io/policy-uid"
	LabelScheduledAt          = "protection.platform.io/scheduled-at"
	LabelTrigger              = "protection.platform.io/trigger"
	LabelTaskUID              = "protection.platform.io/task-uid"
	LabelRecordUID            = "protection.platform.io/record-uid"
	AnnotationCreator         = "protection.platform.io/creator"
	AnnotationRequest         = "protection.platform.io/request-id"
	AnnotationForceDel        = "protection.platform.io/force-delete"
	AnnotationProtected       = "protection.platform.io/protected"
	AnnotationDeleteMode      = "protection.platform.io/delete-mode"
	AnnotationDeleteConfirmed = "protection.platform.io/delete-confirmed"

	RepositoryFinalizer = "protection.platform.io/repository-protection"
	PolicyFinalizer     = "protection.platform.io/policy-protection"
	BackupTaskFinalizer = "protection.platform.io/backup-task-execution"
	RecordFinalizer     = "protection.platform.io/backup-record-assets"
	RestoreFinalizer    = "protection.platform.io/restore-task-execution"
)

const (
	ConditionReady               = "Ready"
	ConditionValid               = "Valid"
	ConditionRepositoryAvailable = "RepositoryAvailable"
	ConditionSelectionResolved   = "SelectionResolved"
	ConditionScheduled           = "Scheduled"
	ConditionResourcesCollected  = "ResourcesCollected"
	ConditionSnapshotsReady      = "SnapshotsReady"
	ConditionArchiveReady        = "ArchiveReady"
	ConditionUploaded            = "Uploaded"
	ConditionVerified            = "Verified"
	ConditionRecordCreated       = "RecordCreated"
	ConditionRestorePlanned      = "RestorePlanned"
	ConditionRestoreCompleted    = "RestoreCompleted"
	ConditionDegraded            = "Degraded"
	ConditionProgressing         = "Progressing"
	ConditionCancellable         = "Cancellable"
)

type ObjectReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	UID  string `json:"uid,omitempty"`
}

type SecretKeyReference struct {
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

type ResourceIdentity struct {
	// +kubebuilder:validation:MinLength=1
	ClusterRef string `json:"clusterRef"`
}

type CommonStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Phase              string             `json:"phase,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	CreatedAt          *metav1.Time       `json:"createdAt,omitempty"`
	StartedAt          *metav1.Time       `json:"startedAt,omitempty"`
	CompletedAt        *metav1.Time       `json:"completedAt,omitempty"`
	Reason             string             `json:"reason,omitempty"`
	Message            string             `json:"message,omitempty"`
	ErrorCode          string             `json:"errorCode,omitempty"`
}

type RetryPolicy struct {
	// +kubebuilder:default:=3
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=20
	MaxAttempts int32 `json:"maxAttempts,omitempty"`
	// +kubebuilder:default:="30s"
	Backoff metav1.Duration `json:"backoff,omitempty"`
	// +kubebuilder:default:="10m"
	MaxBackoff     metav1.Duration `json:"maxBackoff,omitempty"`
	RetryableCodes []string        `json:"retryableCodes,omitempty"`
}

type ExecutionProgress struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Percent            int32 `json:"percent,omitempty"`
	TotalResources     int64 `json:"totalResources,omitempty"`
	ProcessedResources int64 `json:"processedResources,omitempty"`
	SucceededResources int64 `json:"succeededResources,omitempty"`
	FailedResources    int64 `json:"failedResources,omitempty"`
	TotalPVCs          int64 `json:"totalPVCs,omitempty"`
	ProcessedPVCs      int64 `json:"processedPVCs,omitempty"`
	SucceededSnapshots int64 `json:"succeededSnapshots,omitempty"`
	FailedSnapshots    int64 `json:"failedSnapshots,omitempty"`
	BytesEstimated     int64 `json:"bytesEstimated,omitempty"`
	BytesProcessed     int64 `json:"bytesProcessed,omitempty"`
	BytesUploaded      int64 `json:"bytesUploaded,omitempty"`
}

type ErrorDetail struct {
	Code      string             `json:"code"`
	Message   string             `json:"message"`
	Retryable bool               `json:"retryable,omitempty"`
	ObjectRef *ResourceObjectRef `json:"objectRef,omitempty"`
	At        metav1.Time        `json:"at"`
}

type ResourceObjectRef struct {
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

type SnapshotResult struct {
	PVCNamespace              string       `json:"pvcNamespace"`
	PVCName                   string       `json:"pvcName"`
	StorageClass              string       `json:"storageClass,omitempty"`
	VolumeSnapshotName        string       `json:"volumeSnapshotName,omitempty"`
	VolumeSnapshotContentName string       `json:"volumeSnapshotContentName,omitempty"`
	SnapshotHandle            string       `json:"snapshotHandle,omitempty"`
	SnapshotClass             string       `json:"snapshotClass,omitempty"`
	Driver                    string       `json:"driver,omitempty"`
	ReadyToUse                bool         `json:"readyToUse,omitempty"`
	RestoreSize               int64        `json:"restoreSize,omitempty"`
	CreationTime              *metav1.Time `json:"creationTime,omitempty"`
	Phase                     string       `json:"phase,omitempty"`
	Error                     string       `json:"error,omitempty"`
}
