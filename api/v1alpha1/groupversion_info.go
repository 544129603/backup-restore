// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

// Package v1alpha1 contains the protection.platform.io API types.
// +kubebuilder:object:generate=true
// +groupName=protection.platform.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion  = schema.GroupVersion{Group: "protection.platform.io", Version: "v1alpha1"}
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	AddToScheme   = SchemeBuilder.AddToScheme
)

func Kind(kind string) schema.GroupKind { return GroupVersion.WithKind(kind).GroupKind() }
func Resource(resource string) schema.GroupResource {
	return GroupVersion.WithResource(resource).GroupResource()
}

func init() {
	SchemeBuilder.Register(
		&BackupRepository{}, &BackupRepositoryList{},
		&BackupPolicy{}, &BackupPolicyList{},
		&BackupTask{}, &BackupTaskList{},
		&BackupRecord{}, &BackupRecordList{},
		&RestoreTask{}, &RestoreTaskList{},
		&BackupPluginConfig{}, &BackupPluginConfigList{},
	)
}
