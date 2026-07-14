// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	BackupTriggerManual            = "Manual"
	BackupTriggerSchedule          = "Schedule"
	BackupTriggerRetry             = "Retry"
	BackupTriggerClone             = "Clone"
	BackupPhasePending             = "Pending"
	BackupPhaseValidating          = "Validating"
	BackupPhasePreparing           = "Preparing"
	BackupPhaseCollectingResources = "CollectingResources"
	BackupPhaseRunningPreHooks     = "RunningPreHooks"
	BackupPhaseCreatingSnapshots   = "CreatingSnapshots"
	BackupPhasePackaging           = "Packaging"
	BackupPhaseUploading           = "Uploading"
	BackupPhaseVerifying           = "Verifying"
	BackupPhaseGeneratingRecord    = "GeneratingRecord"
	BackupPhaseCompleted           = "Completed"
	BackupPhasePartiallyFailed     = "PartiallyFailed"
	BackupPhaseFailed              = "Failed"
	BackupPhaseCancelling          = "Cancelling"
	BackupPhaseCancelled           = "Cancelled"
)

type BackupTaskSpec struct {
	ResourceIdentity `json:",inline"`
	// +kubebuilder:validation:Enum=Manual;Schedule;Retry;Clone
	Trigger       string           `json:"trigger"`
	PolicyRef     *ObjectReference `json:"policyRef,omitempty"`
	ParentTaskRef *ObjectReference `json:"parentTaskRef,omitempty"`
	ScheduledAt   *metav1.Time     `json:"scheduledAt,omitempty"`
	ScopeRef      ObjectReference  `json:"scopeRef"`
	RepositoryRef ObjectReference  `json:"repositoryRef"`
	// ScopeSnapshot freezes selection semantics for reproducible execution. The
	// policy controller populates it; manual tasks may omit it and resolve once.
	ScopeSnapshot        *BackupScopeSpec `json:"scopeSnapshot,omitempty"`
	ScopeGeneration      int64            `json:"scopeGeneration,omitempty"`
	RepositoryGeneration int64            `json:"repositoryGeneration,omitempty"`
	// +kubebuilder:default:="4h"
	Timeout     metav1.Duration `json:"timeout,omitempty"`
	RetryPolicy RetryPolicy     `json:"retryPolicy,omitempty"`
	// +kubebuilder:validation:Enum=FailFast;Continue
	// +kubebuilder:default:=Continue
	FailurePolicy string `json:"failurePolicy,omitempty"`
	// +kubebuilder:default:=true
	AllowPartialRecord bool   `json:"allowPartialRecord,omitempty"`
	CancelRequested    bool   `json:"cancelRequested,omitempty"`
	CancelReason       string `json:"cancelReason,omitempty"`
	IdempotencyKey     string `json:"idempotencyKey,omitempty"`
}

type TaskCheckpoint struct {
	Step       string      `json:"step"`
	Key        string      `json:"key"`
	ExternalID string      `json:"externalID,omitempty"`
	Completed  bool        `json:"completed,omitempty"`
	UpdatedAt  metav1.Time `json:"updatedAt"`
}

type BackupTaskStatus struct {
	CommonStatus      `json:",inline"`
	Step              string            `json:"step,omitempty"`
	BackupID          string            `json:"backupID,omitempty"`
	Progress          ExecutionProgress `json:"progress,omitempty"`
	Attempt           int32             `json:"attempt,omitempty"`
	WorkerName        string            `json:"workerName,omitempty"`
	ExecutionNode     string            `json:"executionNode,omitempty"`
	LastHeartbeatTime *metav1.Time      `json:"lastHeartbeatTime,omitempty"`
	Checkpoints       []TaskCheckpoint  `json:"checkpoints,omitempty"`
	Errors            []ErrorDetail     `json:"errors,omitempty"`
	Warnings          int32             `json:"warnings,omitempty"`
	Snapshots         []SnapshotResult  `json:"snapshots,omitempty"`
	ArchivePath       string            `json:"archivePath,omitempty"`
	ArchiveChecksum   string            `json:"archiveChecksum,omitempty"`
	BackupBytes       int64             `json:"backupBytes,omitempty"`
	RecordRef         *ObjectReference  `json:"recordRef,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=btask
// +kubebuilder:printcolumn:name="Trigger",type=string,JSONPath=`.spec.trigger`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Progress",type=integer,JSONPath=`.status.progress.percent`
// +kubebuilder:printcolumn:name="Record",type=string,JSONPath=`.status.recordRef.name`
// +kubebuilder:printcolumn:name="Started",type=date,JSONPath=`.status.startedAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BackupTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackupTaskSpec   `json:"spec,omitempty"`
	Status            BackupTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BackupTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupTask `json:"items"`
}
