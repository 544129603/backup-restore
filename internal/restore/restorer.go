// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package restore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
)

type ApplyResult struct {
	Action          string
	TargetName      string
	TargetNamespace string
}

type ResourceRestorer struct {
	Dynamic   dynamic.Interface
	Mapper    meta.RESTMapper
	Resolver  ConflictResolver
	RenameMap map[string]string
	Transform func(*unstructured.Unstructured, PlanItem) error
}

func (r *ResourceRestorer) Apply(ctx context.Context, root string, item PlanItem) (ApplyResult, error) {
	var object *unstructured.Unstructured
	var err error
	if item.Kind == "Namespace" && item.File == "" {
		object = &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]interface{}{"name": item.TargetName}}}
	} else {
		object, err = loadObject(root, item.File)
		if err != nil {
			return ApplyResult{}, err
		}
	}
	object.SetNamespace(item.TargetNamespace)
	object.SetName(item.TargetName)
	rewriteReferences(object, r.RenameMap, item.TargetNamespace)
	if r.Transform != nil {
		if err = r.Transform(object, item); err != nil {
			return ApplyResult{}, err
		}
	}
	gv, err := schema.ParseGroupVersion(object.GetAPIVersion())
	if err != nil {
		return ApplyResult{}, err
	}
	mapping, err := r.Mapper.RESTMapping(gv.WithKind(object.GetKind()).GroupKind(), gv.Version)
	if err != nil {
		return ApplyResult{}, err
	}
	resourceClient := r.Dynamic.Resource(mapping.Resource).Namespace(item.TargetNamespace)
	existing, err := resourceClient.Get(ctx, item.TargetName, metav1.GetOptions{})
	exists := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return ApplyResult{}, err
	}
	action, resolveErr := r.Resolver.Resolve(item.SourceGVR, item.Kind, exists)
	if resolveErr != nil {
		return ApplyResult{Action: "Fail", TargetName: item.TargetName, TargetNamespace: item.TargetNamespace}, resolveErr
	}
	switch action {
	case "Create":
		object.SetResourceVersion("")
		_, err = resourceClient.Create(ctx, object, metav1.CreateOptions{})
	case "Skip":
		return ApplyResult{Action: "Skip", TargetName: item.TargetName, TargetNamespace: item.TargetNamespace}, nil
	case "Overwrite":
		object.SetResourceVersion(existing.GetResourceVersion())
		_, err = resourceClient.Update(ctx, object, metav1.UpdateOptions{})
		if err != nil && r.Resolver.Policy.AllowRecreate && r.Resolver.Policy.HighRiskConfirmed && RecreateSupported(item.Kind) {
			if deleteErr := resourceClient.Delete(ctx, item.TargetName, metav1.DeleteOptions{}); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				return ApplyResult{Action: "Recreate", TargetName: item.TargetName, TargetNamespace: item.TargetNamespace}, deleteErr
			}
			if waitErr := wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
				_, getErr := resourceClient.Get(ctx, item.TargetName, metav1.GetOptions{})
				return apierrors.IsNotFound(getErr), clientError(getErr)
			}); waitErr != nil {
				return ApplyResult{Action: "Recreate", TargetName: item.TargetName, TargetNamespace: item.TargetNamespace}, waitErr
			}
			object.SetResourceVersion("")
			_, err = resourceClient.Create(ctx, object, metav1.CreateOptions{})
			action = "Recreate"
		}
	case "Rename":
		targetName, findErr := r.availableName(ctx, resourceClient, item.TargetName+"-restored")
		if findErr != nil {
			return ApplyResult{}, findErr
		}
		object.SetName(targetName)
		object.SetResourceVersion("")
		_, err = resourceClient.Create(ctx, object, metav1.CreateOptions{})
		if err == nil {
			if r.RenameMap == nil {
				r.RenameMap = map[string]string{}
			}
			r.RenameMap[renameKey(item.Kind, item.TargetNamespace, item.TargetName)] = targetName
		}
		item.TargetName = targetName
	}
	if err != nil {
		return ApplyResult{Action: action, TargetName: item.TargetName, TargetNamespace: item.TargetNamespace}, err
	}
	return ApplyResult{Action: action, TargetName: item.TargetName, TargetNamespace: item.TargetNamespace}, nil
}

func (r *ResourceRestorer) availableName(ctx context.Context, client dynamic.ResourceInterface, base string) (string, error) {
	for i := 0; i < 10; i++ {
		candidate := base
		if i > 0 {
			candidate += "-" + strconv.Itoa(i+1)
		}
		_, err := client.Get(ctx, candidate, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no available renamed target for %s", base)
}

func loadObject(root, file string) (*unstructured.Unstructured, error) {
	clean := filepath.Clean(filepath.FromSlash(file))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("unsafe resource file %q", file)
	}
	full := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("resource file escapes root")
	}
	handle, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	object := &unstructured.Unstructured{}
	if err = json.NewDecoder(handle).Decode(&object.Object); err != nil {
		return nil, err
	}
	return object, nil
}

func renameKey(kind, namespace, name string) string { return kind + "/" + namespace + "/" + name }

func RecreateSupported(kind string) bool {
	switch kind {
	case "Service", "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "Ingress", "HorizontalPodAutoscaler", "PodDisruptionBudget", "NetworkPolicy", "Role", "RoleBinding", "ConfigMap":
		return true
	default:
		return false
	}
}

func clientError(err error) error {
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func rewriteReferences(object *unstructured.Unstructured, renames map[string]string, namespace string) {
	if len(renames) == 0 {
		return
	}
	rewriteValue(object.Object, renames, namespace, "")
}
func rewriteValue(value interface{}, renames map[string]string, namespace, parent string) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if name, ok := child.(string); ok {
				kind := ""
				switch key {
				case "serviceAccountName":
					kind = "ServiceAccount"
				case "serviceName":
					kind = "Service"
				case "claimName":
					kind = "PersistentVolumeClaim"
				case "secretName":
					kind = "Secret"
				case "name":
					switch parent {
					case "configMap", "configMapRef":
						kind = "ConfigMap"
					case "secret", "secretRef":
						kind = "Secret"
					case "service":
						kind = "Service"
					case "scaleTargetRef":
						kind = "Deployment"
					}
				}
				if kind != "" {
					if renamed := renames[renameKey(kind, namespace, name)]; renamed != "" {
						typed[key] = renamed
					}
				}
			}
			rewriteValue(child, renames, namespace, key)
		}
	case []interface{}:
		for _, child := range typed {
			rewriteValue(child, renames, namespace, parent)
		}
	}
}
