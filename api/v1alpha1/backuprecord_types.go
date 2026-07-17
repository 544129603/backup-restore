// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	RecordPhasePending                   = "Pending"
	RecordPhaseAvailable                 = "Available"
	RecordPhasePartiallyAvailable        = "PartiallyAvailable"
	RecordPhaseVerifying                 = "Verifying"
	RecordPhaseBroken                    = "Broken"
	RecordPhaseSnapshotMissing           = "SnapshotMissing"
	RecordPhaseRepoUnavailable           = "RepoUnavailable"
	RecordPhaseExpired                   = "Expired"
	RecordPhaseDeleting                  = "Deleting"
	RecordPhaseDeleted                   = "Deleted"
	DeleteModeRecordOnly                 = "RecordOnly"
	DeleteModeRepositoryData             = "RepositoryData"
	DeleteModeRepositoryDataAndSnapshots = "RepositoryDataAndSnapshots"
)

type BackupSource struct {
	ClusterRef        string   `json:"clusterRef"`
	ClusterUID        string   `json:"clusterUID,omitempty"`
	KubernetesVersion string   `json:"kubernetesVersion,omitempty"`
	Namespaces        []string `json:"namespaces,omitempty"`
	ScopeMode         string   `json:"scopeMode,omitempty"`
}

type BackupInventory struct {
	ResourceCount       int64 `json:"resourceCount,omitempty"`
	NamespaceCount      int64 `json:"namespaceCount,omitempty"`
	PVCCount            int64 `json:"pvcCount,omitempty"`
	SnapshotCount       int64 `json:"snapshotCount,omitempty"`
	FailedResourceCount int64 `json:"failedResourceCount,omitempty"`
	FailedSnapshotCount int64 `json:"failedSnapshotCount,omitempty"`
	BackupBytes         int64 `json:"backupBytes,omitempty"`
}

type RecordEncryptionSpec struct {
	Enabled   bool                `json:"enabled,omitempty"`
	Algorithm string              `json:"algorithm,omitempty"`
	KeyRef    *SecretKeyReference `json:"keyRef,omitempty"`
}

type BackupRecordSpec struct {
	ResourceIdentity    `json:",inline"`
	BackupID            string               `json:"backupID"`
	SourceTaskRef       ObjectReference      `json:"sourceTaskRef"`
	PolicyRef           ObjectReference      `json:"policyRef"`
	RepositoryRef       ObjectReference      `json:"repositoryRef"`
	Source              BackupSource         `json:"source"`
	BackupPath          string               `json:"backupPath"`
	Checksum            string               `json:"checksum"`
	ChecksumAlgorithm   string               `json:"checksumAlgorithm,omitempty"`
	FormatVersion       string               `json:"formatVersion"`
	OperatorVersion     string               `json:"operatorVersion,omitempty"`
	Encryption          RecordEncryptionSpec `json:"encryption,omitempty"`
	Inventory           BackupInventory      `json:"inventory,omitempty"`
	Snapshots           []SnapshotResult     `json:"snapshots,omitempty"`
	ContentCompleteness string               `json:"contentCompleteness,omitempty"`
	SnapshotLifecycle   string               `json:"snapshotLifecycle,omitempty"`
	ExpiresAt           *metav1.Time         `json:"expiresAt,omitempty"`
}

type RecordDeletionStatus struct {
	Mode                  string       `json:"mode,omitempty"`
	RepositoryDataDeleted bool         `json:"repositoryDataDeleted,omitempty"`
	SnapshotsDeleted      int32        `json:"snapshotsDeleted,omitempty"`
	StartedAt             *metav1.Time `json:"startedAt,omitempty"`
}

type BackupRecordStatus struct {
	CommonStatus     `json:",inline"`
	Restorable       bool                 `json:"restorable,omitempty"`
	LastVerifiedAt   *metav1.Time         `json:"lastVerifiedAt,omitempty"`
	VerifiedFiles    int64                `json:"verifiedFiles,omitempty"`
	MissingSnapshots []string             `json:"missingSnapshots,omitempty"`
	RestoreCount     int64                `json:"restoreCount,omitempty"`
	LastRestoreTime  *metav1.Time         `json:"lastRestoreTime,omitempty"`
	Protected        bool                 `json:"protected,omitempty"`
	Deletion         RecordDeletionStatus `json:"deletion,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=brecord
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.source.clusterRef`
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=`.spec.repositoryRef.name`
// +kubebuilder:printcolumn:name="Resources",type=integer,JSONPath=`.spec.inventory.resourceCount`
// +kubebuilder:printcolumn:name="PVCs",type=integer,JSONPath=`.spec.inventory.pvcCount`
// +kubebuilder:printcolumn:name="Bytes",type=integer,JSONPath=`.spec.inventory.backupBytes`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.spec.expiresAt`
type BackupRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackupRecordSpec   `json:"spec,omitempty"`
	Status            BackupRecordStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BackupRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupRecord `json:"items"`
}
