// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	RestorePhasePending                      = "Pending"
	RestorePhaseValidatingBackup             = "ValidatingBackup"
	RestorePhaseDownloading                  = "Downloading"
	RestorePhaseGeneratingPlan               = "GeneratingPlan"
	RestorePhasePreChecking                  = "PreChecking"
	RestorePhaseRestoringNamespaces          = "RestoringNamespaces"
	RestorePhaseRestoringClusterResources    = "RestoringClusterResources"
	RestorePhaseRestoringPVC                 = "RestoringPVC"
	RestorePhaseRestoringNamespacedResources = "RestoringNamespacedResources"
	RestorePhaseVerifying                    = "Verifying"
	RestorePhaseCompleted                    = "Completed"
	RestorePhasePartiallyFailed              = "PartiallyFailed"
	RestorePhaseFailed                       = "Failed"
	RestorePhaseCancelling                   = "Cancelling"
	RestorePhaseCancelled                    = "Cancelled"
	ConflictSkip                             = "Skip"
	ConflictOverwrite                        = "Overwrite"
	ConflictRename                           = "Rename"
	ConflictFail                             = "Fail"
)

type RestoreResourceSelection struct {
	Include                 []string `json:"include,omitempty"`
	Exclude                 []string `json:"exclude,omitempty"`
	IncludeClusterResources bool     `json:"includeClusterResources,omitempty"`
}

type RestoreConflictPolicy struct {
	// +kubebuilder:validation:Enum=Skip;Overwrite;Rename;Fail
	// +kubebuilder:default:=Skip
	Default           string            `json:"default,omitempty"`
	PerResource       map[string]string `json:"perResource,omitempty"`
	AllowRecreate     bool              `json:"allowRecreate,omitempty"`
	HighRiskConfirmed bool              `json:"highRiskConfirmed,omitempty"`
}

type RestoreTaskSpec struct {
	ResourceIdentity `json:",inline"`
	// +kubebuilder:validation:Enum=Manual;Retry
	// +kubebuilder:default:=Manual
	Trigger          string           `json:"trigger,omitempty"`
	ParentTaskRef    *ObjectReference `json:"parentTaskRef,omitempty"`
	BackupRecordRef  ObjectReference  `json:"backupRecordRef"`
	TargetClusterRef string           `json:"targetClusterRef"`
	// +kubebuilder:validation:Enum=Original;NewNamespace;Mapping
	Mode                string                   `json:"mode,omitempty"`
	NamespaceMapping    map[string]string        `json:"namespaceMapping,omitempty"`
	ResourceSelection   RestoreResourceSelection `json:"resourceSelection,omitempty"`
	RestorePVC          bool                     `json:"restorePVC,omitempty"`
	MetadataOnly        bool                     `json:"metadataOnly,omitempty"`
	StorageClassMapping map[string]string        `json:"storageClassMapping,omitempty"`
	ConflictPolicy      RestoreConflictPolicy    `json:"conflictPolicy,omitempty"`
	DryRun              bool                     `json:"dryRun,omitempty"`
	// +kubebuilder:validation:Enum=FailFast;Continue
	// +kubebuilder:default:=Continue
	FailurePolicy string `json:"failurePolicy,omitempty"`
	// +kubebuilder:default:="4h"
	Timeout         metav1.Duration `json:"timeout,omitempty"`
	CancelRequested bool            `json:"cancelRequested,omitempty"`
	CancelReason    string          `json:"cancelReason,omitempty"`
	PlanHash        string          `json:"planHash,omitempty"`
}

type RestorePlanSummary struct {
	Reference     string       `json:"reference,omitempty"`
	Hash          string       `json:"hash,omitempty"`
	TotalObjects  int64        `json:"totalObjects,omitempty"`
	TotalPVCs     int64        `json:"totalPVCs,omitempty"`
	ConflictCount int64        `json:"conflictCount,omitempty"`
	BlockingCount int64        `json:"blockingCount,omitempty"`
	WarningCount  int64        `json:"warningCount,omitempty"`
	GeneratedAt   *metav1.Time `json:"generatedAt,omitempty"`
}

type RestoreProgress struct {
	Total      int64 `json:"total,omitempty"`
	Processed  int64 `json:"processed,omitempty"`
	Created    int64 `json:"created,omitempty"`
	Updated    int64 `json:"updated,omitempty"`
	Skipped    int64 `json:"skipped,omitempty"`
	Renamed    int64 `json:"renamed,omitempty"`
	Failed     int64 `json:"failed,omitempty"`
	TotalPVCs  int64 `json:"totalPVCs,omitempty"`
	BoundPVCs  int64 `json:"boundPVCs,omitempty"`
	FailedPVCs int64 `json:"failedPVCs,omitempty"`
}

type RestoreTaskStatus struct {
	CommonStatus      `json:",inline"`
	Step              string              `json:"step,omitempty"`
	Plan              RestorePlanSummary  `json:"plan,omitempty"`
	Progress          RestoreProgress     `json:"progress,omitempty"`
	LastHeartbeatTime *metav1.Time        `json:"lastHeartbeatTime,omitempty"`
	Checkpoints       []TaskCheckpoint    `json:"checkpoints,omitempty"`
	Errors            []ErrorDetail       `json:"errors,omitempty"`
	ResidualResources []ResourceObjectRef `json:"residualResources,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rtask
// +kubebuilder:printcolumn:name="Record",type=string,JSONPath=`.spec.backupRecordRef.name`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetClusterRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Processed",type=integer,JSONPath=`.status.progress.processed`
// +kubebuilder:printcolumn:name="Started",type=date,JSONPath=`.status.startedAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type RestoreTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RestoreTaskSpec   `json:"spec,omitempty"`
	Status            RestoreTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type RestoreTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RestoreTask `json:"items"`
}
