package integration

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/controller"
	"github.com/stretchr/testify/require"
)

func TestRepositoryControllerWithRealAPIServer(t *testing.T) {
	if os.Getenv("RUN_ENVTEST") != "1" {
		t.Skip("set RUN_ENVTEST=1 and KUBEBUILDER_ASSETS to run integration tests")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	testEnvironment := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join(root, "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
	config, err := testEnvironment.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		if stopErr := testEnvironment.Stop(); stopErr != nil && goruntime.GOOS != "windows" {
			t.Errorf("stop EnvTest: %v", stopErr)
		}
	})
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, protectionv1alpha1.AddToScheme(scheme))
	manager, err := ctrl.NewManager(config, ctrl.Options{Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0", LeaderElection: false})
	require.NoError(t, err)
	reconciler := &controller.RepositoryReconciler{Client: manager.GetClient(), Scheme: scheme}
	require.NoError(t, reconciler.SetupWithManager(manager))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = manager.Start(ctx) }()
	require.True(t, manager.GetCache().WaitForCacheSync(ctx))
	repository := &protectionv1alpha1.BackupRepository{ObjectMeta: metav1.ObjectMeta{Name: "envtest-local"}, Spec: protectionv1alpha1.BackupRepositorySpec{ResourceIdentity: protectionv1alpha1.ResourceIdentity{ClusterRef: "envtest"}, Type: protectionv1alpha1.RepositoryTypeLocal, Enabled: true, Local: &protectionv1alpha1.LocalRepositorySpec{Mode: protectionv1alpha1.LocalModeHostPath, Path: t.TempDir(), NodeName: "envtest"}, HealthCheckInterval: metav1.Duration{Duration: time.Minute}}}
	require.NoError(t, manager.GetClient().Create(ctx, repository))
	require.Eventually(t, func() bool {
		current := &protectionv1alpha1.BackupRepository{}
		return manager.GetClient().Get(ctx, client.ObjectKey{Name: repository.Name}, current) == nil && current.Status.Phase == protectionv1alpha1.RepositoryPhaseReady
	}, 20*time.Second, 250*time.Millisecond)
}
