// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	ConcurrencyAllow    = "Allow"
	ConcurrencyForbid   = "Forbid"
	ConcurrencyReplace  = "Replace"
	MissedRunSkip       = "Skip"
	MissedRunOnce       = "RunOnce"
	MissedRunAll        = "RunAll"
	PolicyPhasePending  = "Pending"
	PolicyPhaseReady    = "Ready"
	PolicyPhasePaused   = "Paused"
	PolicyPhaseInvalid  = "Invalid"
	PolicyPhaseDegraded = "Degraded"
	PolicyPhaseDeleting = "Deleting"
)

type BackupScheduleSpec struct {
	Cron string `json:"cron"`
	// +kubebuilder:default:="Etc/UTC"
	Timezone string `json:"timezone,omitempty"`
}

type RetentionSpec struct {
	// +kubebuilder:default:=7
	// +kubebuilder:validation:Minimum=1
	MaxCopies int32 `json:"maxCopies,omitempty"`
	// +kubebuilder:default:=1
	// +kubebuilder:validation:Minimum=0
	MinCopies int32 `json:"minCopies,omitempty"`
	// +kubebuilder:default:=30
	// +kubebuilder:validation:Minimum=1
	MaxAgeDays int32 `json:"maxAgeDays,omitempty"`
	// +kubebuilder:default:=false
	DeleteSnapshots bool `json:"deleteSnapshots,omitempty"`
}

type BackupPolicySpec struct {
	ResourceIdentity `json:",inline"`
	ScopeRef         ObjectReference    `json:"scopeRef"`
	RepositoryRef    ObjectReference    `json:"repositoryRef"`
	Schedule         BackupScheduleSpec `json:"schedule"`
	Enabled          bool               `json:"enabled,omitempty"`
	Suspend          bool               `json:"suspend,omitempty"`
	// +kubebuilder:validation:Enum=Allow;Forbid;Replace
	// +kubebuilder:default:=Forbid
	ConcurrencyPolicy string `json:"concurrencyPolicy,omitempty"`
	// +kubebuilder:validation:Enum=Skip;RunOnce;RunAll
	// +kubebuilder:default:=Skip
	MissedRunPolicy string `json:"missedRunPolicy,omitempty"`
	// +kubebuilder:default:="1h"
	StartingDeadline metav1.Duration `json:"startingDeadline,omitempty"`
	// +kubebuilder:default:=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	MaxCatchUpRuns int32         `json:"maxCatchUpRuns,omitempty"`
	Retention      RetentionSpec `json:"retention,omitempty"`
	RetryPolicy    RetryPolicy   `json:"retryPolicy,omitempty"`
	// +kubebuilder:default:="4h"
	Timeout metav1.Duration `json:"timeout,omitempty"`
}

type SkippedRun struct {
	ScheduledAt metav1.Time `json:"scheduledAt"`
	Reason      string      `json:"reason"`
}

type BackupPolicyStatus struct {
	CommonStatus              `json:",inline"`
	ResolvedScopeUID          string            `json:"resolvedScopeUID,omitempty"`
	ResolvedRepositoryUID     string            `json:"resolvedRepositoryUID,omitempty"`
	LastScheduleTime          *metav1.Time      `json:"lastScheduleTime,omitempty"`
	LastSuccessfulTime        *metav1.Time      `json:"lastSuccessfulTime,omitempty"`
	LastEvaluatedScheduleTime *metav1.Time      `json:"lastEvaluatedScheduleTime,omitempty"`
	NextScheduleTime          *metav1.Time      `json:"nextScheduleTime,omitempty"`
	ActiveTasks               []ObjectReference `json:"activeTasks,omitempty"`
	SkippedRuns               []SkippedRun      `json:"skippedRuns,omitempty"`
	ConsecutiveFailures       int32             `json:"consecutiveFailures,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=bpolicy
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule.cron`
// +kubebuilder:printcolumn:name="Timezone",type=string,JSONPath=`.spec.schedule.timezone`
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Next",type=date,JSONPath=`.status.nextScheduleTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BackupPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackupPolicySpec   `json:"spec,omitempty"`
	Status            BackupPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BackupPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupPolicy `json:"items"`
}
