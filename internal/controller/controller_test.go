package controller

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/checksum"
	repofactory "github.com/example/backup-restore-operator/internal/repository/factory"
	"github.com/example/backup-restore-operator/internal/snapshot"
	"github.com/stretchr/testify/require"
)

func testScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	require.NoError(t, protectionv1alpha1.AddToScheme(scheme))
	return scheme
}

func TestRepositoryReconcileLocalReady(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)
	repository := &protectionv1alpha1.BackupRepository{ObjectMeta: metav1.ObjectMeta{Name: "local"}, Spec: protectionv1alpha1.BackupRepositorySpec{ResourceIdentity: protectionv1alpha1.ResourceIdentity{ClusterRef: "c1"}, Type: protectionv1alpha1.RepositoryTypeLocal, Enabled: true, Local: &protectionv1alpha1.LocalRepositorySpec{Mode: protectionv1alpha1.LocalModeHostPath, Path: t.TempDir(), NodeName: "worker-1"}}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&protectionv1alpha1.BackupRepository{}).WithObjects(repository).Build()
	reconciler := &RepositoryReconciler{Client: kube, Factory: repofactory.Factory{Client: kube}}
	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: repository.Name}})
	require.NoError(t, err)
	actual := &protectionv1alpha1.BackupRepository{}
	require.NoError(t, kube.Get(ctx, client.ObjectKey{Name: repository.Name}, actual))
	require.Equal(t, protectionv1alpha1.RepositoryPhaseReady, actual.Status.Phase)
	require.Contains(t, actual.Finalizers, protectionv1alpha1.RepositoryFinalizer)
}

func TestPolicyReconcileCreatesOnlyOneDeterministicTask(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)
	uid := types.UID(uuid.NewUUID())
	created := metav1.NewTime(time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC))
	repository := &protectionv1alpha1.BackupRepository{ObjectMeta: metav1.ObjectMeta{Name: "repo", UID: types.UID(uuid.NewUUID())}, Spec: protectionv1alpha1.BackupRepositorySpec{ResourceIdentity: protectionv1alpha1.ResourceIdentity{ClusterRef: "c1"}}, Status: protectionv1alpha1.BackupRepositoryStatus{CommonStatus: protectionv1alpha1.CommonStatus{Phase: protectionv1alpha1.RepositoryPhaseReady}}}
	policy := &protectionv1alpha1.BackupPolicy{ObjectMeta: metav1.ObjectMeta{Name: "hourly", UID: uid, CreationTimestamp: created}, Spec: protectionv1alpha1.BackupPolicySpec{ResourceIdentity: protectionv1alpha1.ResourceIdentity{ClusterRef: "c1"}, Selection: protectionv1alpha1.BackupSelectionSpec{Mode: protectionv1alpha1.BackupSelectionModeNamespace, IncludeNamespaces: []string{"app"}}, RepositoryRef: protectionv1alpha1.ObjectReference{Name: repository.Name}, Schedule: protectionv1alpha1.BackupScheduleSpec{Cron: "* * * * *", Timezone: "Etc/UTC"}, Enabled: true, ConcurrencyPolicy: protectionv1alpha1.ConcurrencyForbid, MissedRunPolicy: protectionv1alpha1.MissedRunOnce, StartingDeadline: metav1.Duration{Duration: time.Hour}, MaxCatchUpRuns: 1, Timeout: metav1.Duration{Duration: time.Hour}}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&protectionv1alpha1.BackupPolicy{}, &protectionv1alpha1.BackupRepository{}, &protectionv1alpha1.BackupTask{}).WithObjects(repository, policy).Build()
	now := time.Date(2026, 7, 13, 0, 5, 10, 0, time.UTC)
	reconciler := &PolicyReconciler{Client: kube, Now: func() time.Time { return now }}
	request := reconcile.Request{NamespacedName: client.ObjectKey{Name: policy.Name}}
	_, err := reconciler.Reconcile(ctx, request)
	require.NoError(t, err)
	_, err = reconciler.Reconcile(ctx, request)
	require.NoError(t, err)
	list := &protectionv1alpha1.BackupTaskList{}
	require.NoError(t, kube.List(ctx, list))
	require.Len(t, list.Items, 1)
	require.Equal(t, protectionv1alpha1.BackupTriggerSchedule, list.Items[0].Spec.Trigger)
	require.NotNil(t, list.Items[0].Spec.SelectionSnapshot)
}

func TestManualBackupTaskResolvesMergedPolicyOnce(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)
	repository := &protectionv1alpha1.BackupRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "repo", UID: types.UID(uuid.NewUUID()), Generation: 3},
		Spec: protectionv1alpha1.BackupRepositorySpec{
			ResourceIdentity: protectionv1alpha1.ResourceIdentity{ClusterRef: "c1"},
		},
		Status: protectionv1alpha1.BackupRepositoryStatus{CommonStatus: protectionv1alpha1.CommonStatus{Phase: protectionv1alpha1.RepositoryPhaseReady}},
	}
	policy := &protectionv1alpha1.BackupPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "daily", UID: types.UID(uuid.NewUUID()), Generation: 7},
		Spec: protectionv1alpha1.BackupPolicySpec{
			ResourceIdentity: protectionv1alpha1.ResourceIdentity{ClusterRef: "c1"},
			Selection: protectionv1alpha1.BackupSelectionSpec{
				Mode: protectionv1alpha1.BackupSelectionModeNamespace, IncludeNamespaces: []string{"payments"},
			},
			RepositoryRef: protectionv1alpha1.ObjectReference{Name: repository.Name},
			Timeout:       metav1.Duration{Duration: 2 * time.Hour},
			RetryPolicy:   protectionv1alpha1.RetryPolicy{MaxAttempts: 5},
		},
	}
	task := &protectionv1alpha1.BackupTask{
		ObjectMeta: metav1.ObjectMeta{Name: "manual", UID: types.UID(uuid.NewUUID())},
		Spec: protectionv1alpha1.BackupTaskSpec{
			ResourceIdentity: protectionv1alpha1.ResourceIdentity{ClusterRef: "c1"},
			Trigger:          protectionv1alpha1.BackupTriggerManual, PolicyRef: protectionv1alpha1.ObjectReference{Name: policy.Name},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&protectionv1alpha1.BackupRepository{}, &protectionv1alpha1.BackupPolicy{}, &protectionv1alpha1.BackupTask{}).
		WithObjects(repository, policy, task).Build()
	reconciler := &BackupTaskReconciler{Client: kube, Workspace: t.TempDir()}
	request := reconcile.Request{NamespacedName: client.ObjectKey{Name: task.Name}}
	_, err := reconciler.Reconcile(ctx, request)
	require.NoError(t, err)
	_, err = reconciler.Reconcile(ctx, request)
	require.NoError(t, err)
	actual := &protectionv1alpha1.BackupTask{}
	require.NoError(t, kube.Get(ctx, client.ObjectKey{Name: task.Name}, actual))
	require.Equal(t, string(policy.UID), actual.Spec.PolicyRef.UID)
	require.Equal(t, string(repository.UID), actual.Spec.RepositoryRef.UID)
	require.Equal(t, int64(7), actual.Spec.PolicyGeneration)
	require.NotNil(t, actual.Spec.SelectionSnapshot)
	require.Equal(t, []string{"payments"}, actual.Spec.SelectionSnapshot.IncludeNamespaces)
	require.Equal(t, 2*time.Hour, actual.Spec.Timeout.Duration)
	require.Equal(t, int32(5), actual.Spec.RetryPolicy.MaxAttempts)
	require.Equal(t, protectionv1alpha1.BackupPhaseValidating, actual.Status.Phase)
}

func TestBackupTaskCancellationIsTerminal(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)
	task := &protectionv1alpha1.BackupTask{ObjectMeta: metav1.ObjectMeta{Name: "manual", UID: types.UID(uuid.NewUUID())}, Spec: protectionv1alpha1.BackupTaskSpec{ResourceIdentity: protectionv1alpha1.ResourceIdentity{ClusterRef: "c1"}, Trigger: protectionv1alpha1.BackupTriggerManual, PolicyRef: protectionv1alpha1.ObjectReference{Name: "policy"}, Timeout: metav1.Duration{Duration: time.Hour}}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&protectionv1alpha1.BackupTask{}).WithObjects(task).Build()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	reconciler := &BackupTaskReconciler{Client: kube, Snapshots: &snapshot.Manager{Client: kube, Dynamic: dynamicClient}, Workspace: t.TempDir()}
	request := reconcile.Request{NamespacedName: client.ObjectKey{Name: task.Name}}
	_, err := reconciler.Reconcile(ctx, request)
	require.NoError(t, err)
	actual := &protectionv1alpha1.BackupTask{}
	require.NoError(t, kube.Get(ctx, client.ObjectKey{Name: task.Name}, actual))
	before := actual.DeepCopy()
	actual.Spec.CancelRequested = true
	require.NoError(t, kube.Patch(ctx, actual, client.MergeFrom(before)))
	_, err = reconciler.Reconcile(ctx, request)
	require.NoError(t, err)
	require.NoError(t, kube.Get(ctx, client.ObjectKey{Name: task.Name}, actual))
	require.Equal(t, protectionv1alpha1.BackupPhaseCancelled, actual.Status.Phase)
	require.NotNil(t, actual.Status.CompletedAt)
}

func TestBackupRecordIndependentVerification(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)
	root := t.TempDir()
	backupPath := filepath.Join(root, "backups", "id")
	require.NoError(t, os.MkdirAll(backupPath, 0o700))
	payload := []byte("valid archive bytes")
	require.NoError(t, os.WriteFile(filepath.Join(backupPath, "resources.tar.gz"), payload, 0o600))
	sum, _, err := checksum.Sum(ctx, bytes.NewReader(payload))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(backupPath, "sha256sum.txt"), []byte(checksum.Manifest(map[string]string{"resources.tar.gz": sum})), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(backupPath, ".done"), []byte(sum+"\n"), 0o600))
	repository := &protectionv1alpha1.BackupRepository{ObjectMeta: metav1.ObjectMeta{Name: "repo"}, Spec: protectionv1alpha1.BackupRepositorySpec{ResourceIdentity: protectionv1alpha1.ResourceIdentity{ClusterRef: "c1"}, Type: protectionv1alpha1.RepositoryTypeLocal, Enabled: true, Local: &protectionv1alpha1.LocalRepositorySpec{Mode: protectionv1alpha1.LocalModeHostPath, Path: root, NodeName: "worker"}}}
	record := &protectionv1alpha1.BackupRecord{ObjectMeta: metav1.ObjectMeta{Name: "record"}, Spec: protectionv1alpha1.BackupRecordSpec{ResourceIdentity: protectionv1alpha1.ResourceIdentity{ClusterRef: "c1"}, BackupID: "id", SourceTaskRef: protectionv1alpha1.ObjectReference{Name: "task"}, PolicyRef: protectionv1alpha1.ObjectReference{Name: "policy"}, RepositoryRef: protectionv1alpha1.ObjectReference{Name: "repo"}, Source: protectionv1alpha1.BackupSource{ClusterRef: "c1"}, BackupPath: "backups/id", Checksum: sum, FormatVersion: "1.0", ContentCompleteness: "Complete"}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&protectionv1alpha1.BackupRecord{}).WithObjects(repository, record).Build()
	reconciler := &BackupRecordReconciler{Client: kube, Factory: repofactory.Factory{Client: kube}, Snapshots: &snapshot.Manager{Client: kube, Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())}}
	_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: record.Name}})
	require.NoError(t, err)
	actual := &protectionv1alpha1.BackupRecord{}
	require.NoError(t, kube.Get(ctx, client.ObjectKey{Name: record.Name}, actual))
	require.Equal(t, protectionv1alpha1.RecordPhaseAvailable, actual.Status.Phase)
	require.True(t, actual.Status.Restorable)
}
