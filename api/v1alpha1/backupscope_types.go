// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type BackupScopeMode string

const (
	BackupScopeModeCluster   BackupScopeMode = "Cluster"
	BackupScopeModeNamespace BackupScopeMode = "Namespace"
	ScopePhasePending                        = "Pending"
	ScopePhaseResolving                      = "Resolving"
	ScopePhaseReady                          = "Ready"
	ScopePhaseInvalid                        = "Invalid"
	ScopePhaseDeleting                       = "Deleting"
)

type ResourceSelection struct {
	Include        []string `json:"include,omitempty"`
	Exclude        []string `json:"exclude,omitempty"`
	IncludeCluster []string `json:"includeCluster,omitempty"`
	ExcludeCluster []string `json:"excludeCluster,omitempty"`
}

type PVCSelectionSpec struct {
	// Enabled is intentionally serialized even when false so a frozen
	// BackupTask scope cannot be defaulted back to snapshots enabled.
	Enabled              bool                  `json:"enabled"`
	Include              []string              `json:"include,omitempty"`
	Exclude              []string              `json:"exclude,omitempty"`
	LabelSelector        *metav1.LabelSelector `json:"labelSelector,omitempty"`
	SnapshotClassName    string                `json:"snapshotClassName,omitempty"`
	SnapshotClassMapping map[string]string     `json:"snapshotClassMapping,omitempty"`
	// +kubebuilder:default:="10m"
	SnapshotTimeout metav1.Duration `json:"snapshotTimeout,omitempty"`
	// +kubebuilder:validation:Enum=FailFast;ContinueAndMarkPartial
	// +kubebuilder:default:=ContinueAndMarkPartial
	FailurePolicy string `json:"failurePolicy,omitempty"`
	// +kubebuilder:validation:Enum=DeleteWithRecord;RetainAfterRecordDeletion
	// +kubebuilder:default:=RetainAfterRecordDeletion
	Lifecycle string `json:"lifecycle,omitempty"`
}

// HookSpec is versioned now so AppConsistent backups can be introduced without
// changing the scope model. The v1.0 webhook only permits empty hook lists.
type HookSpec struct {
	Pre  []ResourceHook `json:"pre,omitempty"`
	Post []ResourceHook `json:"post,omitempty"`
}

type ResourceHook struct {
	Name        string                `json:"name"`
	Namespace   string                `json:"namespace"`
	PodSelector *metav1.LabelSelector `json:"podSelector,omitempty"`
	Container   string                `json:"container,omitempty"`
	Command     []string              `json:"command"`
	Timeout     metav1.Duration       `json:"timeout,omitempty"`
	// +kubebuilder:validation:Enum=Fail;Continue
	OnError string `json:"onError,omitempty"`
}

type BackupScopeSpec struct {
	ResourceIdentity `json:",inline"`
	// +kubebuilder:validation:Enum=Cluster;Namespace
	Mode                    BackupScopeMode       `json:"mode"`
	IncludeNamespaces       []string              `json:"includeNamespaces,omitempty"`
	ExcludeNamespaces       []string              `json:"excludeNamespaces,omitempty"`
	Resources               ResourceSelection     `json:"resources,omitempty"`
	LabelSelector           *metav1.LabelSelector `json:"labelSelector,omitempty"`
	IncludeClusterResources bool                  `json:"includeClusterResources,omitempty"`
	IncludeSecrets          bool                  `json:"includeSecrets,omitempty"`
	IncludeCRDs             bool                  `json:"includeCRDs,omitempty"`
	IncludeCustomResources  bool                  `json:"includeCustomResources,omitempty"`
	PVC                     PVCSelectionSpec      `json:"pvc,omitempty"`
	// +kubebuilder:validation:Enum=CrashConsistent;AppConsistent;ManualQuiesce
	// +kubebuilder:default:=CrashConsistent
	ConsistencyMode string   `json:"consistencyMode,omitempty"`
	Hooks           HookSpec `json:"hooks,omitempty"`
}

type ScopePreviewStatus struct {
	NamespaceCount          int64        `json:"namespaceCount,omitempty"`
	ResourceTypeCount       int64        `json:"resourceTypeCount,omitempty"`
	ResourceObjectCount     int64        `json:"resourceObjectCount,omitempty"`
	PVCCount                int64        `json:"pvcCount,omitempty"`
	SnapshotCapablePVCCount int64        `json:"snapshotCapablePVCCount,omitempty"`
	UnsupportedPVCCount     int64        `json:"unsupportedPVCCount,omitempty"`
	RiskCount               int64        `json:"riskCount,omitempty"`
	GeneratedAt             *metav1.Time `json:"generatedAt,omitempty"`
	ResolvedHash            string       `json:"resolvedHash,omitempty"`
}

type BackupScopeStatus struct {
	CommonStatus         `json:",inline"`
	Preview              ScopePreviewStatus `json:"preview,omitempty"`
	ReferencedByPolicies int32              `json:"referencedByPolicies,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=bscope
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Objects",type=integer,JSONPath=`.status.preview.resourceObjectCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BackupScope struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackupScopeSpec   `json:"spec,omitempty"`
	Status            BackupScopeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BackupScopeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupScope `json:"items"`
}
