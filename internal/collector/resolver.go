// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package collector

import (
	"context"
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
)

var defaultExcluded = map[string]struct{}{
	"events": {}, "events.events.k8s.io": {}, "pods": {}, "endpoints": {},
	"endpointslices.discovery.k8s.io": {}, "leases.coordination.k8s.io": {},
	"tokenreviews.authentication.k8s.io": {}, "subjectaccessreviews.authorization.k8s.io": {},
	"selfsubjectaccessreviews.authorization.k8s.io": {}, "localsubjectaccessreviews.authorization.k8s.io": {},
	"bindings": {}, "nodes": {},
	"backuprepositories.protection.platform.io": {},
	"backuppolicies.protection.platform.io":     {}, "backuptasks.protection.platform.io": {},
	"backuprecords.protection.platform.io": {}, "restoretasks.protection.platform.io": {},
	"backuppluginconfigs.protection.platform.io": {},
}

var builtInGroups = map[string]struct{}{
	"": {}, "apps": {}, "batch": {}, "autoscaling": {}, "networking.k8s.io": {},
	"policy": {}, "rbac.authorization.k8s.io": {}, "storage.k8s.io": {},
	"coordination.k8s.io": {}, "discovery.k8s.io": {}, "scheduling.k8s.io": {},
	"admissionregistration.k8s.io": {}, "apiextensions.k8s.io": {},
}

type Resolver struct {
	Discovery discovery.DiscoveryInterface
	Dynamic   dynamic.Interface
	PageSize  int64
}

type Preview struct {
	NamespaceCount          int64
	ResourceTypeCount       int64
	ResourceObjectCount     int64
	PVCCount                int64
	SnapshotCapablePVCCount int64
	UnsupportedPVCCount     int64
	Warnings                []string
}

func (r *Resolver) ResolveTypes(_ context.Context, selection protectionv1alpha1.BackupSelectionSpec) ([]ResourceType, []string, error) {
	lists, err := r.Discovery.ServerPreferredResources()
	warnings := make([]string, 0)
	if err != nil {
		if discovery.IsGroupDiscoveryFailedError(err) {
			warnings = append(warnings, err.Error())
		} else {
			return nil, warnings, fmt.Errorf("discover API resources: %w", err)
		}
	}
	resolved := make([]ResourceType, 0)
	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			warnings = append(warnings, parseErr.Error())
			continue
		}
		for _, resource := range list.APIResources {
			if strings.Contains(resource.Name, "/") || !hasVerb(resource.Verbs, "list") {
				continue
			}
			typeInfo := ResourceType{GVR: gv.WithResource(resource.Name), Kind: resource.Kind, Namespaced: resource.Namespaced}
			if IncludeResource(selection, typeInfo) {
				resolved = append(resolved, typeInfo)
			}
		}
	}
	sort.Slice(resolved, func(i, j int) bool {
		return resolved[i].QualifiedName()+resolved[i].GVR.Version < resolved[j].QualifiedName()+resolved[j].GVR.Version
	})
	return resolved, warnings, nil
}

func IncludeResource(spec protectionv1alpha1.BackupSelectionSpec, resource ResourceType) bool {
	name := resource.QualifiedName()
	if _, excluded := defaultExcluded[name]; excluded && !MatchesResource(spec.Resources.Include, name, resource.GVR.Resource) {
		return false
	}
	if resource.GVR.Resource == "secrets" && !spec.IncludeSecrets {
		return false
	}
	if resource.GVR.Group == "apiextensions.k8s.io" && resource.GVR.Resource == "customresourcedefinitions" && !spec.IncludeCRDs {
		return false
	}
	if _, builtIn := builtInGroups[resource.GVR.Group]; !builtIn && !spec.IncludeCustomResources {
		return false
	}
	if !resource.Namespaced {
		if !spec.IncludeClusterResources {
			return false
		}
		if MatchesResource(spec.Resources.ExcludeCluster, name, resource.GVR.Resource) {
			return false
		}
		if len(spec.Resources.IncludeCluster) > 0 && !MatchesResource(spec.Resources.IncludeCluster, name, resource.GVR.Resource) {
			return false
		}
	}
	if MatchesResource(spec.Resources.Exclude, name, resource.GVR.Resource) {
		return false
	}
	return len(spec.Resources.Include) == 0 || MatchesResource(spec.Resources.Include, name, resource.GVR.Resource)
}

func MatchesResource(patterns []string, qualified, resource string) bool {
	for _, pattern := range patterns {
		if pattern == "*" || pattern == qualified || pattern == resource {
			return true
		}
	}
	return false
}

func IncludedNamespaces(spec protectionv1alpha1.BackupSelectionSpec) []string {
	if spec.Mode == protectionv1alpha1.BackupSelectionModeCluster {
		return []string{metav1.NamespaceAll}
	}
	excluded := map[string]struct{}{}
	for _, namespace := range spec.ExcludeNamespaces {
		excluded[namespace] = struct{}{}
	}
	result := make([]string, 0, len(spec.IncludeNamespaces))
	for _, namespace := range spec.IncludeNamespaces {
		if _, skip := excluded[namespace]; !skip {
			result = append(result, namespace)
		}
	}
	sort.Strings(result)
	return result
}

func NamespaceIncluded(spec protectionv1alpha1.BackupSelectionSpec, namespace string) bool {
	for _, excluded := range spec.ExcludeNamespaces {
		if namespace == excluded {
			return false
		}
	}
	if spec.Mode == protectionv1alpha1.BackupSelectionModeCluster {
		return true
	}
	for _, included := range spec.IncludeNamespaces {
		if namespace == included {
			return true
		}
	}
	return false
}

func (r *Resolver) Preview(ctx context.Context, selection protectionv1alpha1.BackupSelectionSpec) (*Preview, []ResourceType, error) {
	types, warnings, err := r.ResolveTypes(ctx, selection)
	if err != nil {
		return nil, nil, err
	}
	selector := labels.Everything()
	if selection.LabelSelector != nil {
		selector, err = metav1.LabelSelectorAsSelector(selection.LabelSelector)
		if err != nil {
			return nil, nil, err
		}
	}
	pageSize := r.PageSize
	if pageSize <= 0 {
		pageSize = 500
	}
	preview := &Preview{ResourceTypeCount: int64(len(types)), Warnings: warnings}
	seenNamespaces := map[string]struct{}{}
	namespaces := IncludedNamespaces(selection)
	if selection.Mode == protectionv1alpha1.BackupSelectionModeNamespace {
		preview.NamespaceCount = int64(len(namespaces))
	}
	for _, resource := range types {
		targets := []string{metav1.NamespaceAll}
		if resource.Namespaced {
			targets = namespaces
		}
		for _, namespace := range targets {
			continuation := ""
			for {
				options := metav1.ListOptions{Limit: pageSize, Continue: continuation, LabelSelector: selector.String()}
				list, listErr := r.Dynamic.Resource(resource.GVR).Namespace(namespace).List(ctx, options)
				if listErr != nil {
					preview.Warnings = append(preview.Warnings, fmt.Sprintf("%s: %v", resource.QualifiedName(), listErr))
					break
				}
				for i := range list.Items {
					if resource.Namespaced && !NamespaceIncluded(selection, list.Items[i].GetNamespace()) {
						continue
					}
					preview.ResourceObjectCount++
					if resource.Namespaced {
						seenNamespaces[list.Items[i].GetNamespace()] = struct{}{}
					}
					if resource.GVR.Group == "" && resource.GVR.Resource == "persistentvolumeclaims" {
						preview.PVCCount++
					}
				}
				continuation = list.GetContinue()
				if continuation == "" {
					break
				}
			}
		}
	}
	if selection.Mode == protectionv1alpha1.BackupSelectionModeCluster {
		preview.NamespaceCount = int64(len(seenNamespaces))
	}
	return preview, types, nil
}

func hasVerb(verbs metav1.Verbs, expected string) bool {
	for _, verb := range verbs {
		if verb == expected {
			return true
		}
	}
	return false
}
