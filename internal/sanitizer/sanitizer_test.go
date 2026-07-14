package sanitizer

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestSanitizeService(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1", "kind": "Service", "metadata": map[string]interface{}{"name": "web", "namespace": "app", "uid": "old", "resourceVersion": "3", "finalizers": []interface{}{"x"}},
		"spec": map[string]interface{}{"clusterIP": "10.0.0.1", "ports": []interface{}{map[string]interface{}{"port": int64(80), "nodePort": int64(30080)}}, "selector": map[string]interface{}{"app": "web"}}, "status": map[string]interface{}{"loadBalancer": map[string]interface{}{}},
	}}
	obj.SetUID(types.UID("old"))
	result, err := Sanitize(obj)
	require.NoError(t, err)
	require.Equal(t, "old", result.SourceUID)
	require.Empty(t, result.Object.GetUID())
	require.Nil(t, result.Object.Object["status"])
	_, found, _ := unstructured.NestedString(result.Object.Object, "spec", "clusterIP")
	require.False(t, found)
	ports, _, _ := unstructured.NestedSlice(result.Object.Object, "spec", "ports")
	require.NotContains(t, ports[0].(map[string]interface{}), "nodePort")
}

func TestSanitizePVC(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": map[string]interface{}{"name": "data"}, "spec": map[string]interface{}{"volumeName": "pv-a"}}}
	result, err := Sanitize(obj)
	require.NoError(t, err)
	_, found, _ := unstructured.NestedString(result.Object.Object, "spec", "volumeName")
	require.False(t, found)
}
