// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RepositoryType string
type LocalRepositoryMode string

const (
	RepositoryTypeLocal RepositoryType      = "Local"
	RepositoryTypeSFTP  RepositoryType      = "SFTP"
	LocalModeHostPath   LocalRepositoryMode = "HostPath"
	LocalModePVC        LocalRepositoryMode = "PVC"
)

const (
	RepositoryPhasePending  = "Pending"
	RepositoryPhaseChecking = "Checking"
	RepositoryPhaseReady    = "Ready"
	RepositoryPhaseDegraded = "Degraded"
	RepositoryPhaseFailed   = "Failed"
	RepositoryPhaseDeleting = "Deleting"
)

type LocalRepositorySpec struct {
	// +kubebuilder:validation:Enum=HostPath;PVC
	Mode         LocalRepositoryMode `json:"mode"`
	Path         string              `json:"path,omitempty"`
	NodeName     string              `json:"nodeName,omitempty"`
	NodeSelector map[string]string   `json:"nodeSelector,omitempty"`
	PVC          *LocalPVCReference  `json:"pvc,omitempty"`
	// +kubebuilder:default:=1000
	UID int64 `json:"uid,omitempty"`
	// +kubebuilder:default:=1000
	GID int64 `json:"gid,omitempty"`
}

type LocalPVCReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SubPath   string `json:"subPath,omitempty"`
}

type SFTPAuthSpec struct {
	// +kubebuilder:validation:Enum=Password;PrivateKey
	Type          string              `json:"type"`
	UsernameRef   SecretKeyReference  `json:"usernameRef"`
	PasswordRef   *SecretKeyReference `json:"passwordRef,omitempty"`
	PrivateKeyRef *SecretKeyReference `json:"privateKeyRef,omitempty"`
	PassphraseRef *SecretKeyReference `json:"passphraseRef,omitempty"`
}

type SFTPRepositorySpec struct {
	Host string `json:"host"`
	// +kubebuilder:default:=22
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port          int32               `json:"port,omitempty"`
	BasePath      string              `json:"basePath"`
	Auth          SFTPAuthSpec        `json:"auth"`
	KnownHostsRef *SecretKeyReference `json:"knownHostsRef,omitempty"`
	// +kubebuilder:default:=false
	InsecureSkipHostKeyCheck bool `json:"insecureSkipHostKeyCheck,omitempty"`
	// +kubebuilder:default:="10s"
	ConnectTimeout metav1.Duration `json:"connectTimeout,omitempty"`
	// +kubebuilder:default:="5m"
	OperationTimeout metav1.Duration `json:"operationTimeout,omitempty"`
	// +kubebuilder:default:="30s"
	KeepAliveInterval metav1.Duration `json:"keepAliveInterval,omitempty"`
	// +kubebuilder:default:=4
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=16
	MaxConnections int32 `json:"maxConnections,omitempty"`
}

type RepositoryCompressionSpec struct {
	// +kubebuilder:validation:Enum=None;Gzip
	// +kubebuilder:default:=Gzip
	Algorithm string `json:"algorithm,omitempty"`
	// +kubebuilder:default:=6
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=9
	Level int32 `json:"level,omitempty"`
}

type RepositoryEncryptionSpec struct {
	Enabled bool `json:"enabled,omitempty"`
	// +kubebuilder:validation:Enum=AES256GCM
	// +kubebuilder:default:=AES256GCM
	Algorithm string              `json:"algorithm,omitempty"`
	KeyRef    *SecretKeyReference `json:"keyRef,omitempty"`
}

type BackupRepositorySpec struct {
	ResourceIdentity `json:",inline"`
	// +kubebuilder:validation:Enum=Local;SFTP
	Type RepositoryType `json:"type"`
	// +kubebuilder:default:=true
	Enabled     bool                      `json:"enabled,omitempty"`
	Local       *LocalRepositorySpec      `json:"local,omitempty"`
	SFTP        *SFTPRepositorySpec       `json:"sftp,omitempty"`
	Compression RepositoryCompressionSpec `json:"compression,omitempty"`
	Encryption  RepositoryEncryptionSpec  `json:"encryption,omitempty"`
	// +kubebuilder:default:="30m"
	HealthCheckInterval metav1.Duration `json:"healthCheckInterval,omitempty"`
	// +kubebuilder:default:="30s"
	Timeout metav1.Duration `json:"timeout,omitempty"`
	// +kubebuilder:default:=3
	RetryCount int32 `json:"retryCount,omitempty"`
	// +kubebuilder:default:="10Gi"
	MinimumFreeBytes *resource.Quantity `json:"minimumFreeBytes,omitempty"`
	// +kubebuilder:default:=true
	DeletionProtection bool `json:"deletionProtection,omitempty"`
}

type RepositoryCapabilities struct {
	Read         bool `json:"read,omitempty"`
	Write        bool `json:"write,omitempty"`
	Delete       bool `json:"delete,omitempty"`
	AtomicRename bool `json:"atomicRename,omitempty"`
	Capacity     bool `json:"capacity,omitempty"`
}

type BackupRepositoryStatus struct {
	CommonStatus            `json:",inline"`
	Capabilities            RepositoryCapabilities            `json:"capabilities,omitempty"`
	AvailableBytes          int64                             `json:"availableBytes,omitempty"`
	TotalBytes              int64                             `json:"totalBytes,omitempty"`
	CapacityKnown           bool                              `json:"capacityKnown,omitempty"`
	LastCheckTime           *metav1.Time                      `json:"lastCheckTime,omitempty"`
	LastSuccessfulCheckTime *metav1.Time                      `json:"lastSuccessfulCheckTime,omitempty"`
	ResolvedNodeName        string                            `json:"resolvedNodeName,omitempty"`
	PVCPhase                corev1.PersistentVolumeClaimPhase `json:"pvcPhase,omitempty"`
	ActivePolicyCount       int32                             `json:"activePolicyCount,omitempty"`
	RecordCount             int32                             `json:"recordCount,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=brepo
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Available",type=integer,JSONPath=`.status.availableBytes`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BackupRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackupRepositorySpec   `json:"spec,omitempty"`
	Status            BackupRepositoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BackupRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupRepository `json:"items"`
}
