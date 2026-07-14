// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package restore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/collector"
)

type PlanItem struct {
	Order            int      `json:"order"`
	SourceGVR        string   `json:"sourceGVR"`
	SourceNamespace  string   `json:"sourceNamespace,omitempty"`
	TargetNamespace  string   `json:"targetNamespace,omitempty"`
	SourceName       string   `json:"sourceName"`
	TargetName       string   `json:"targetName"`
	Kind             string   `json:"kind"`
	APIVersion       string   `json:"apiVersion"`
	File             string   `json:"file"`
	Action           string   `json:"action"`
	Conflict         string   `json:"conflict,omitempty"`
	Transformations  []string `json:"transformations,omitempty"`
	Dependencies     []string `json:"dependencies,omitempty"`
	ValidationResult string   `json:"validationResult,omitempty"`
}

type Plan struct {
	Version     string     `json:"version"`
	GeneratedAt time.Time  `json:"generatedAt"`
	Items       []PlanItem `json:"items"`
	Hash        string     `json:"hash"`
}

func BuildPlan(index collector.Index, spec protectionv1alpha1.RestoreTaskSpec) (*Plan, error) {
	include := toSet(spec.ResourceSelection.Include)
	exclude := toSet(spec.ResourceSelection.Exclude)
	items := make([]PlanItem, 0, len(index.Entries))
	namespaceTargets := map[string]string{}
	for _, entry := range index.Entries {
		if entry.Namespace != "" {
			target := entry.Namespace
			if mapped, ok := spec.NamespaceMapping[entry.Namespace]; ok {
				target = mapped
			}
			namespaceTargets[entry.Namespace] = target
		}
		if entry.Kind == "PersistentVolumeClaim" && (!spec.RestorePVC || spec.MetadataOnly) {
			continue
		}
		if len(include) > 0 && !matchesSet(include, entry.GVR) {
			continue
		}
		if matchesSet(exclude, entry.GVR) {
			continue
		}
		targetNamespace := entry.Namespace
		targetName := entry.Name
		if mapped, ok := spec.NamespaceMapping[entry.Namespace]; ok {
			targetNamespace = mapped
		}
		if entry.Kind == "Namespace" {
			if mapped, ok := spec.NamespaceMapping[entry.Name]; ok {
				targetName = mapped
			}
		}
		if entry.Namespace == "" && entry.Kind != "Namespace" && !spec.ResourceSelection.IncludeClusterResources {
			continue
		}
		dependencies := make([]string, 0, len(entry.Dependencies))
		for _, dependency := range entry.Dependencies {
			dependencies = append(dependencies, strings.Join([]string{dependency.Kind, dependency.Namespace, dependency.Name}, "/"))
		}
		items = append(items, PlanItem{Order: restoreOrder(entry.Kind), SourceGVR: entry.GVR, SourceNamespace: entry.Namespace, TargetNamespace: targetNamespace, SourceName: entry.Name, TargetName: targetName, Kind: entry.Kind, APIVersion: entry.APIVersion, File: entry.File, Action: "CreateOrResolve", Dependencies: dependencies, ValidationResult: "Pending"})
	}
	existingNamespaces := map[string]struct{}{}
	for _, item := range items {
		if item.Kind == "Namespace" {
			existingNamespaces[item.TargetName] = struct{}{}
		}
	}
	for source, target := range namespaceTargets {
		if _, exists := existingNamespaces[target]; exists {
			continue
		}
		items = append(items, PlanItem{Order: restoreOrder("Namespace"), SourceGVR: "namespaces", SourceName: source, TargetName: target, Kind: "Namespace", APIVersion: "v1", Action: "CreateOrResolve", ValidationResult: "Synthetic"})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Order != items[j].Order {
			return items[i].Order < items[j].Order
		}
		a := strings.Join([]string{items[i].SourceGVR, items[i].TargetNamespace, items[i].TargetName}, "/")
		b := strings.Join([]string{items[j].SourceGVR, items[j].TargetNamespace, items[j].TargetName}, "/")
		return a < b
	})
	plan := &Plan{Version: "1.0", GeneratedAt: time.Now().UTC(), Items: items}
	payload, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(payload)
	plan.Hash = hex.EncodeToString(hash[:])
	return plan, nil
}

func restoreOrder(kind string) int {
	switch kind {
	case "Namespace":
		return 10
	case "CustomResourceDefinition":
		return 20
	case "StorageClass", "VolumeSnapshotClass", "PriorityClass", "ClusterRole", "ClusterRoleBinding":
		return 30
	case "ServiceAccount":
		return 40
	case "Secret":
		return 50
	case "ConfigMap":
		return 60
	case "Role":
		return 70
	case "RoleBinding":
		return 80
	case "PersistentVolumeClaim":
		return 90
	case "Service":
		return 100
	case "Deployment":
		return 110
	case "StatefulSet":
		return 120
	case "DaemonSet":
		return 130
	case "Job":
		return 140
	case "CronJob":
		return 150
	case "Ingress":
		return 160
	case "NetworkPolicy":
		return 170
	case "PodDisruptionBudget":
		return 180
	case "HorizontalPodAutoscaler":
		return 190
	default:
		return 200
	}
}

func toSet(values []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func matchesSet(set map[string]struct{}, value string) bool {
	if _, ok := set["*"]; ok {
		return true
	}
	if _, ok := set[value]; ok {
		return true
	}
	short := strings.SplitN(value, ".", 2)[0]
	_, ok := set[short]
	return ok
}

func ValidateNamespaceMapping(mapping map[string]string) error {
	seen := map[string]string{}
	for source, target := range mapping {
		if source == "" || target == "" {
			return fmt.Errorf("namespace mapping cannot contain empty names")
		}
		if previous, exists := seen[target]; exists && previous != source {
			return fmt.Errorf("namespace %s and %s both map to %s", previous, source, target)
		}
		seen[target] = source
	}
	return nil
}
