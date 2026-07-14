// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	opererrors "github.com/example/backup-restore-operator/internal/errors"
)

var (
	VolumeSnapshotGVR        = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshots"}
	VolumeSnapshotContentGVR = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotcontents"}
	VolumeSnapshotClassGVR   = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotclasses"}
)

type Manager struct {
	Client  client.Client
	Dynamic dynamic.Interface
}

type Capability struct {
	PVC            *corev1.PersistentVolumeClaim
	StorageClass   *storagev1.StorageClass
	Driver         string
	SnapshotClass  string
	DeletionPolicy string
}

func (m *Manager) Detect(ctx context.Context, namespace, pvcName, requestedClass string, mapping map[string]string) (*Capability, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	if err := m.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: pvcName}, pvc); err != nil {
		return nil, opererrors.New(opererrors.CodeSnapshotUnsupported, "read PVC", false, err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName == "" {
		return nil, opererrors.New(opererrors.CodeSnapshotUnsupported, "PVC has no StorageClass", false, nil)
	}
	storageClass := &storagev1.StorageClass{}
	if err := m.Client.Get(ctx, types.NamespacedName{Name: *pvc.Spec.StorageClassName}, storageClass); err != nil {
		return nil, opererrors.New(opererrors.CodeSnapshotUnsupported, "read StorageClass", false, err)
	}
	driver := storageClass.Provisioner
	selected := requestedClass
	if mapped := mapping[driver]; mapped != "" {
		selected = mapped
	}
	classes, err := m.Dynamic.Resource(VolumeSnapshotClassGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, opererrors.New(opererrors.CodeSnapshotUnsupported, "VolumeSnapshot API is unavailable", false, err)
	}
	matches := make([]unstructured.Unstructured, 0)
	for i := range classes.Items {
		class := classes.Items[i]
		classDriver, _, _ := unstructured.NestedString(class.Object, "driver")
		if classDriver != driver {
			continue
		}
		if selected != "" {
			if class.GetName() == selected {
				matches = []unstructured.Unstructured{class}
				break
			}
			continue
		}
		if class.GetAnnotations()["snapshot.storage.kubernetes.io/is-default-class"] == "true" {
			matches = append(matches, class)
		}
	}
	if len(matches) != 1 {
		return nil, opererrors.New(opererrors.CodeSnapshotUnsupported, fmt.Sprintf("expected exactly one VolumeSnapshotClass for driver %s, found %d", driver, len(matches)), false, nil)
	}
	deletionPolicy, _, _ := unstructured.NestedString(matches[0].Object, "deletionPolicy")
	return &Capability{PVC: pvc, StorageClass: storageClass, Driver: driver, SnapshotClass: matches[0].GetName(), DeletionPolicy: deletionPolicy}, nil
}

func DeterministicName(taskUID, namespace, pvc string) string {
	hash := sha256.Sum256([]byte(taskUID + "/" + namespace + "/" + pvc))
	return "bs-" + hex.EncodeToString(hash[:])[:24]
}

func (m *Manager) EnsureSnapshot(ctx context.Context, taskUID string, capability *Capability) (protectionv1alpha1.SnapshotResult, bool, error) {
	name := DeterministicName(taskUID, capability.PVC.Namespace, capability.PVC.Name)
	resourceClient := m.Dynamic.Resource(VolumeSnapshotGVR).Namespace(capability.PVC.Namespace)
	snapshot, err := resourceClient.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		snapshot = &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshot", "metadata": map[string]interface{}{"name": name, "namespace": capability.PVC.Namespace, "labels": map[string]interface{}{protectionv1alpha1.LabelTaskUID: taskUID}}, "spec": map[string]interface{}{"volumeSnapshotClassName": capability.SnapshotClass, "source": map[string]interface{}{"persistentVolumeClaimName": capability.PVC.Name}}}}
		snapshot, err = resourceClient.Create(ctx, snapshot, metav1.CreateOptions{})
	}
	if err != nil {
		return protectionv1alpha1.SnapshotResult{}, false, opererrors.New(opererrors.CodeSnapshotCreate, "create or get VolumeSnapshot", true, err)
	}
	result := protectionv1alpha1.SnapshotResult{PVCNamespace: capability.PVC.Namespace, PVCName: capability.PVC.Name, StorageClass: *capability.PVC.Spec.StorageClassName, VolumeSnapshotName: name, SnapshotClass: capability.SnapshotClass, Driver: capability.Driver, Phase: "Pending"}
	ready, _, _ := unstructured.NestedBool(snapshot.Object, "status", "readyToUse")
	result.ReadyToUse = ready
	contentName, _, _ := unstructured.NestedString(snapshot.Object, "status", "boundVolumeSnapshotContentName")
	result.VolumeSnapshotContentName = contentName
	if restoreSize, found, _ := unstructured.NestedString(snapshot.Object, "status", "restoreSize"); found {
		if quantity, parseErr := resource.ParseQuantity(restoreSize); parseErr == nil {
			result.RestoreSize = quantity.Value()
		}
	}
	if creation, found, _ := unstructured.NestedString(snapshot.Object, "status", "creationTime"); found {
		if parsed, parseErr := time.Parse(time.RFC3339, creation); parseErr == nil {
			value := metav1.NewTime(parsed.UTC())
			result.CreationTime = &value
		}
	}
	if statusError, found, _ := unstructured.NestedMap(snapshot.Object, "status", "error"); found {
		result.Phase = "Failed"
		result.Error = fmt.Sprint(statusError["message"])
		return result, false, opererrors.New(opererrors.CodeSnapshotCreate, "VolumeSnapshot reported an error", false, fmt.Errorf("%s", result.Error))
	}
	if !ready {
		return result, false, nil
	}
	result.Phase = "Ready"
	if contentName != "" {
		content, contentErr := m.Dynamic.Resource(VolumeSnapshotContentGVR).Get(ctx, contentName, metav1.GetOptions{})
		if contentErr == nil {
			result.SnapshotHandle, _, _ = unstructured.NestedString(content.Object, "status", "snapshotHandle")
		}
	}
	return result, true, nil
}

func (m *Manager) Delete(ctx context.Context, result protectionv1alpha1.SnapshotResult) error {
	if result.VolumeSnapshotName == "" {
		return nil
	}
	err := m.Dynamic.Resource(VolumeSnapshotGVR).Namespace(result.PVCNamespace).Delete(ctx, result.VolumeSnapshotName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (m *Manager) PrepareRestoreSnapshot(ctx context.Context, result protectionv1alpha1.SnapshotResult, targetNamespace, targetName string) (string, error) {
	if result.SnapshotHandle == "" || result.Driver == "" {
		return "", opererrors.New(opererrors.CodeRestorePVC, "snapshot handle or driver is missing", false, nil)
	}
	hash := sha256.Sum256([]byte(result.SnapshotHandle + "/" + targetNamespace + "/" + targetName))
	suffix := hex.EncodeToString(hash[:])[:20]
	contentName := "restore-vsc-" + suffix
	snapshotName := "restore-vs-" + suffix
	content := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshotContent", "metadata": map[string]interface{}{"name": contentName}, "spec": map[string]interface{}{"deletionPolicy": "Retain", "driver": result.Driver, "source": map[string]interface{}{"snapshotHandle": result.SnapshotHandle}, "volumeSnapshotRef": map[string]interface{}{"name": snapshotName, "namespace": targetNamespace}}}}
	_, err := m.Dynamic.Resource(VolumeSnapshotContentGVR).Get(ctx, contentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = m.Dynamic.Resource(VolumeSnapshotContentGVR).Create(ctx, content, metav1.CreateOptions{})
	}
	if err != nil {
		return "", opererrors.New(opererrors.CodeRestorePVC, "prepare static VolumeSnapshotContent", true, err)
	}
	snapshot := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshot", "metadata": map[string]interface{}{"name": snapshotName, "namespace": targetNamespace}, "spec": map[string]interface{}{"source": map[string]interface{}{"volumeSnapshotContentName": contentName}}}}
	_, err = m.Dynamic.Resource(VolumeSnapshotGVR).Namespace(targetNamespace).Get(ctx, snapshotName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = m.Dynamic.Resource(VolumeSnapshotGVR).Namespace(targetNamespace).Create(ctx, snapshot, metav1.CreateOptions{})
	}
	if err != nil {
		return "", opererrors.New(opererrors.CodeRestorePVC, "prepare target VolumeSnapshot", true, err)
	}
	return snapshotName, nil
}

func SnapshotKey(namespace, name string) string {
	return strings.Trim(namespace, "/") + "/" + strings.Trim(name, "/")
}
