package snapshot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDetectAndCreateDeterministically(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, storagev1.AddToScheme(scheme))
	className := "fast"
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "app"}, Spec: corev1.PersistentVolumeClaimSpec{StorageClassName: &className}}
	sc := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "fast"}, Provisioner: "csi.example.io"}
	snapshotClass := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshotClass", "metadata": map[string]interface{}{"name": "snap", "annotations": map[string]interface{}{"snapshot.storage.kubernetes.io/is-default-class": "true"}}, "driver": "csi.example.io", "deletionPolicy": "Retain"}}
	dynamicClient := fake.NewSimpleDynamicClient(runtime.NewScheme(), snapshotClass)
	manager := Manager{Client: clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(pvc, sc).Build(), Dynamic: dynamicClient}
	capability, err := manager.Detect(context.Background(), "app", "data", "", nil)
	require.NoError(t, err)
	first, ready, err := manager.EnsureSnapshot(context.Background(), "task-uid", capability)
	require.NoError(t, err)
	require.False(t, ready)
	second, _, err := manager.EnsureSnapshot(context.Background(), "task-uid", capability)
	require.NoError(t, err)
	require.Equal(t, first.VolumeSnapshotName, second.VolumeSnapshotName)
}
