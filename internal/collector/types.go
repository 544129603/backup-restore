// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package collector

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/example/backup-restore-operator/internal/sanitizer"
)

type ResourceType struct {
	GVR        schema.GroupVersionResource `json:"gvr"`
	Kind       string                      `json:"kind"`
	Namespaced bool                        `json:"namespaced"`
}

func (r ResourceType) QualifiedName() string {
	if r.GVR.Group == "" {
		return r.GVR.Resource
	}
	return r.GVR.Resource + "." + r.GVR.Group
}

type IndexEntry struct {
	GVR          string                 `json:"gvr"`
	APIVersion   string                 `json:"apiVersion"`
	Kind         string                 `json:"kind"`
	Namespace    string                 `json:"namespace,omitempty"`
	Name         string                 `json:"name"`
	SourceUID    string                 `json:"sourceUID,omitempty"`
	File         string                 `json:"file"`
	Dependencies []sanitizer.Dependency `json:"dependencies,omitempty"`
}

type Index struct {
	Version   string       `json:"version"`
	CreatedAt time.Time    `json:"createdAt"`
	Entries   []IndexEntry `json:"entries"`
}

type Result struct {
	Index         Index
	ResourceCount int64
	PVCCount      int64
	Warnings      []string
}
