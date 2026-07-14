// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/sanitizer"
)

type Collector struct {
	Dynamic  dynamic.Interface
	PageSize int64
}

func (c *Collector) Collect(ctx context.Context, scope *protectionv1alpha1.BackupScope, resources []ResourceType, root string) (*Result, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	selector := labels.Everything()
	var err error
	if scope.Spec.LabelSelector != nil {
		selector, err = metav1.LabelSelectorAsSelector(scope.Spec.LabelSelector)
		if err != nil {
			return nil, err
		}
	}
	pageSize := c.PageSize
	if pageSize <= 0 {
		pageSize = 500
	}
	result := &Result{Index: Index{Version: "1.0", CreatedAt: time.Now().UTC(), Entries: []IndexEntry{}}}
	namespaces := IncludedNamespaces(scope.Spec)
	for _, resource := range resources {
		targets := []string{metav1.NamespaceAll}
		if resource.Namespaced {
			targets = namespaces
		}
		for _, namespace := range targets {
			continuation := ""
			for {
				list, listErr := c.Dynamic.Resource(resource.GVR).Namespace(namespace).List(ctx, metav1.ListOptions{Limit: pageSize, Continue: continuation, LabelSelector: selector.String()})
				if listErr != nil {
					return nil, fmt.Errorf("list %s: %w", resource.QualifiedName(), listErr)
				}
				for i := range list.Items {
					if resource.Namespaced && !NamespaceIncluded(scope.Spec, list.Items[i].GetNamespace()) {
						continue
					}
					sanitized, sanitizeErr := sanitizer.Sanitize(&list.Items[i])
					if sanitizeErr != nil {
						result.Warnings = append(result.Warnings, sanitizeErr.Error())
						continue
					}
					file := resourceFile(resource, sanitized.Object)
					full := filepath.Join(root, filepath.FromSlash(file))
					if err = os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
						return nil, err
					}
					handle, createErr := os.OpenFile(full, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
					if createErr != nil {
						return nil, createErr
					}
					encoder := json.NewEncoder(handle)
					encoder.SetEscapeHTML(false)
					encodeErr := encoder.Encode(sanitized.Object.Object)
					closeErr := handle.Close()
					if encodeErr != nil {
						return nil, encodeErr
					}
					if closeErr != nil {
						return nil, closeErr
					}
					result.Index.Entries = append(result.Index.Entries, IndexEntry{GVR: resource.QualifiedName(), APIVersion: sanitized.Object.GetAPIVersion(), Kind: sanitized.Object.GetKind(), Namespace: sanitized.Object.GetNamespace(), Name: sanitized.Object.GetName(), SourceUID: sanitized.SourceUID, File: file, Dependencies: sanitized.Dependencies})
					result.ResourceCount++
					if resource.GVR.Group == "" && resource.GVR.Resource == "persistentvolumeclaims" {
						result.PVCCount++
					}
				}
				continuation = list.GetContinue()
				if continuation == "" {
					break
				}
			}
		}
	}
	sort.Slice(result.Index.Entries, func(i, j int) bool {
		a, b := result.Index.Entries[i], result.Index.Entries[j]
		return strings.Join([]string{a.GVR, a.Namespace, a.Name}, "/") < strings.Join([]string{b.GVR, b.Namespace, b.Name}, "/")
	})
	return result, nil
}

func resourceFile(resource ResourceType, object unstructuredLike) string {
	group := resource.GVR.Group
	if group == "" {
		group = "core"
	}
	typePath := group + "_" + resource.GVR.Version + "_" + resource.GVR.Resource
	if object.GetNamespace() == "" {
		return filepath.ToSlash(filepath.Join("resources", "cluster", typePath, object.GetName()+".json"))
	}
	return filepath.ToSlash(filepath.Join("resources", "namespaces", object.GetNamespace(), typePath, object.GetName()+".json"))
}

type unstructuredLike interface {
	GetNamespace() string
	GetName() string
}
