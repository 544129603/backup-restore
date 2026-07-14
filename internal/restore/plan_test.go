package restore

import (
	"testing"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/collector"
	"github.com/stretchr/testify/require"
)

func TestPlanOrderAndNamespaceMapping(t *testing.T) {
	index := collector.Index{Entries: []collector.IndexEntry{{GVR: "deployments.apps", APIVersion: "apps/v1", Kind: "Deployment", Namespace: "source", Name: "web", File: "web.json"}, {GVR: "configmaps", APIVersion: "v1", Kind: "ConfigMap", Namespace: "source", Name: "config", File: "config.json"}, {GVR: "namespaces", APIVersion: "v1", Kind: "Namespace", Name: "source", File: "ns.json"}}}
	plan, err := BuildPlan(index, protectionv1alpha1.RestoreTaskSpec{NamespaceMapping: map[string]string{"source": "target"}, ResourceSelection: protectionv1alpha1.RestoreResourceSelection{IncludeClusterResources: true}})
	require.NoError(t, err)
	require.Equal(t, "Namespace", plan.Items[0].Kind)
	require.Equal(t, "target", plan.Items[0].TargetName)
	require.Equal(t, "target", plan.Items[1].TargetNamespace)
	require.NotEmpty(t, plan.Hash)
}

func TestConflictPolicies(t *testing.T) {
	resolver := ConflictResolver{Policy: protectionv1alpha1.RestoreConflictPolicy{Default: protectionv1alpha1.ConflictSkip, PerResource: map[string]string{"configmaps": protectionv1alpha1.ConflictOverwrite}}}
	action, err := resolver.Resolve("configmaps", "ConfigMap", true)
	require.NoError(t, err)
	require.Equal(t, "Overwrite", action)
	_, err = ConflictResolver{Policy: protectionv1alpha1.RestoreConflictPolicy{Default: protectionv1alpha1.ConflictRename}}.Resolve("persistentvolumeclaims", "PersistentVolumeClaim", true)
	require.Error(t, err)
}

func TestPlanSynthesizesMappedNamespace(t *testing.T) {
	index := collector.Index{Entries: []collector.IndexEntry{{GVR: "configmaps", APIVersion: "v1", Kind: "ConfigMap", Namespace: "source", Name: "config", File: "config.json"}}}
	plan, err := BuildPlan(index, protectionv1alpha1.RestoreTaskSpec{NamespaceMapping: map[string]string{"source": "target"}})
	require.NoError(t, err)
	require.Len(t, plan.Items, 2)
	require.Equal(t, "Namespace", plan.Items[0].Kind)
	require.Equal(t, "target", plan.Items[0].TargetName)
	require.Empty(t, plan.Items[0].File)
}
