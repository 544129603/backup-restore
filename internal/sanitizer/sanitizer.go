// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package sanitizer

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Dependency struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

type Result struct {
	Object       *unstructured.Unstructured
	SourceUID    string
	Dependencies []Dependency
}

func Sanitize(input *unstructured.Unstructured) (*Result, error) {
	if input == nil {
		return nil, fmt.Errorf("object is nil")
	}
	object := input.DeepCopy()
	sourceUID := string(object.GetUID())
	dependencies := make([]Dependency, 0, len(object.GetOwnerReferences()))
	for _, owner := range object.GetOwnerReferences() {
		dependencies = append(dependencies, Dependency{APIVersion: owner.APIVersion, Kind: owner.Kind, Namespace: object.GetNamespace(), Name: owner.Name})
	}
	metadata, _, _ := unstructured.NestedMap(object.Object, "metadata")
	for _, key := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "deletionTimestamp", "deletionGracePeriodSeconds", "managedFields", "selfLink", "ownerReferences", "finalizers"} {
		delete(metadata, key)
	}
	if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
		delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		delete(annotations, "pv.kubernetes.io/bind-completed")
		delete(annotations, "pv.kubernetes.io/bound-by-controller")
		delete(annotations, "volume.kubernetes.io/selected-node")
	}
	object.Object["metadata"] = metadata
	delete(object.Object, "status")
	switch object.GetKind() {
	case "Service":
		sanitizeService(object)
	case "PersistentVolumeClaim":
		unstructured.RemoveNestedField(object.Object, "spec", "volumeName")
	case "PersistentVolume":
		unstructured.RemoveNestedField(object.Object, "spec", "claimRef")
	case "Job":
		unstructured.RemoveNestedField(object.Object, "spec", "selector")
		unstructured.RemoveNestedField(object.Object, "spec", "manualSelector")
	}
	return &Result{Object: object, SourceUID: sourceUID, Dependencies: dependencies}, nil
}

func sanitizeService(object *unstructured.Unstructured) {
	for _, field := range []string{"clusterIP", "clusterIPs", "healthCheckNodePort", "ipFamilies", "ipFamilyPolicy"} {
		unstructured.RemoveNestedField(object.Object, "spec", field)
	}
	ports, found, _ := unstructured.NestedSlice(object.Object, "spec", "ports")
	if !found {
		return
	}
	for i := range ports {
		if port, ok := ports[i].(map[string]interface{}); ok {
			delete(port, "nodePort")
			ports[i] = port
		}
	}
	_ = unstructured.SetNestedSlice(object.Object, ports, "spec", "ports")
}
