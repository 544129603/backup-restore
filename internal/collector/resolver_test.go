package collector

import (
	"testing"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestResourceFiltering(t *testing.T) {
	spec := protectionv1alpha1.BackupSelectionSpec{Resources: protectionv1alpha1.ResourceSelection{Include: []string{"deployments.apps", "configmaps"}, Exclude: []string{"configmaps"}}}
	deployment := ResourceType{GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, Namespaced: true}
	configMap := ResourceType{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Namespaced: true}
	require.True(t, IncludeResource(spec, deployment))
	require.False(t, IncludeResource(spec, configMap))
}

func TestNamespaceExcludeWins(t *testing.T) {
	spec := protectionv1alpha1.BackupSelectionSpec{Mode: protectionv1alpha1.BackupSelectionModeNamespace, IncludeNamespaces: []string{"a", "b"}, ExcludeNamespaces: []string{"b"}}
	require.Equal(t, []string{"a"}, IncludedNamespaces(spec))
}
