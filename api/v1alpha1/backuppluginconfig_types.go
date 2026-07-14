// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type ConcurrencyConfig struct {
	// +kubebuilder:default:=3
	MaxBackupTasks int32 `json:"maxBackupTasks,omitempty"`
	// +kubebuilder:default:=1
	MaxRestoreTasks int32 `json:"maxRestoreTasks,omitempty"`
	// +kubebuilder:default:=10
	MaxSnapshotsPerTask int32 `json:"maxSnapshotsPerTask,omitempty"`
	// +kubebuilder:default:=4
	MaxRepositoryOperations int32 `json:"maxRepositoryOperations,omitempty"`
}

type KubernetesClientConfig struct {
	// +kubebuilder:default:=20
	QPS int32 `json:"qps,omitempty"`
	// +kubebuilder:default:=40
	Burst int32 `json:"burst,omitempty"`
	// +kubebuilder:default:=500
	PageSize int64 `json:"pageSize,omitempty"`
}

type SecurityConfig struct {
	// +kubebuilder:default:={"backup-system"}
	AllowedSecretNamespaces []string `json:"allowedSecretNamespaces,omitempty"`
	// +kubebuilder:default:=true
	RequireEncryptionForSecrets bool `json:"requireEncryptionForSecrets,omitempty"`
	AllowInsecureSFTP           bool `json:"allowInsecureSFTP,omitempty"`
	HookExecutionEnabled        bool `json:"hookExecutionEnabled,omitempty"`
}

type GarbageCollectionConfig struct {
	// +kubebuilder:default:="1h"
	Interval metav1.Duration `json:"interval,omitempty"`
	// +kubebuilder:default:="24h"
	StagingGracePeriod metav1.Duration `json:"stagingGracePeriod,omitempty"`
	// +kubebuilder:default:=90
	TerminalTaskTTLDays int32 `json:"terminalTaskTTLDays,omitempty"`
}

type BackupPluginConfigSpec struct {
	// +kubebuilder:default:="Etc/UTC"
	DefaultTimezone string `json:"defaultTimezone,omitempty"`
	// +kubebuilder:default:="4h"
	DefaultBackupTimeout metav1.Duration `json:"defaultBackupTimeout,omitempty"`
	// +kubebuilder:default:="4h"
	DefaultRestoreTimeout metav1.Duration `json:"defaultRestoreTimeout,omitempty"`
	// +kubebuilder:default:="10m"
	DefaultSnapshotTimeout metav1.Duration         `json:"defaultSnapshotTimeout,omitempty"`
	Concurrency            ConcurrencyConfig       `json:"concurrency,omitempty"`
	KubernetesClient       KubernetesClientConfig  `json:"kubernetesClient,omitempty"`
	Security               SecurityConfig          `json:"security,omitempty"`
	GarbageCollection      GarbageCollectionConfig `json:"garbageCollection,omitempty"`
	WorkspacePath          string                  `json:"workspacePath,omitempty"`
	LogLevel               string                  `json:"logLevel,omitempty"`
}

type BackupPluginConfigStatus struct {
	CommonStatus        `json:",inline"`
	EffectiveConfigHash string       `json:"effectiveConfigHash,omitempty"`
	LastAppliedAt       *metav1.Time `json:"lastAppliedAt,omitempty"`
	OperatorVersion     string       `json:"operatorVersion,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=bpconfig
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Backups",type=integer,JSONPath=`.spec.concurrency.maxBackupTasks`
// +kubebuilder:printcolumn:name="Restores",type=integer,JSONPath=`.spec.concurrency.maxRestoreTasks`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BackupPluginConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackupPluginConfigSpec   `json:"spec,omitempty"`
	Status            BackupPluginConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BackupPluginConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupPluginConfig `json:"items"`
}
