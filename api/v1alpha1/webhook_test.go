package v1alpha1

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPVCSnapshotDisabledSurvivesScopeFreezeSerialization(t *testing.T) {
	scope := BackupScopeSpec{ResourceIdentity: ResourceIdentity{ClusterRef: "c1"}, Mode: BackupScopeModeNamespace, IncludeNamespaces: []string{"app"}, PVC: PVCSelectionSpec{Enabled: false}}
	payload, err := json.Marshal(scope)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"enabled":false`)

	decoded := BackupScopeSpec{}
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.False(t, decoded.PVC.Enabled)
}

func TestControllerRuntimeWebhookAdapters(t *testing.T) {
	repository := &BackupRepository{Spec: BackupRepositorySpec{
		ResourceIdentity: ResourceIdentity{ClusterRef: "cluster-a"},
		Type:             RepositoryTypeLocal,
		Local:            &LocalRepositorySpec{Mode: LocalModeHostPath, Path: "/repository", NodeName: "worker-a"},
	}}
	if err := (legacyDefaulterAdapter{}).Default(context.Background(), repository); err != nil {
		t.Fatalf("default through controller-runtime adapter: %v", err)
	}
	if repository.Spec.HealthCheckInterval.Duration == 0 {
		t.Fatal("expected webhook adapter to apply repository defaults")
	}
	if _, err := (legacyValidatorAdapter{}).ValidateCreate(context.Background(), repository); err != nil {
		t.Fatalf("validate through controller-runtime adapter: %v", err)
	}
}

func TestRepositoryValidationRejectsPlainOrUnsafeSFTP(t *testing.T) {
	repository := &BackupRepository{Spec: BackupRepositorySpec{ResourceIdentity: ResourceIdentity{ClusterRef: "c1"}, Type: RepositoryTypeSFTP, SFTP: &SFTPRepositorySpec{Host: "sftp.example", Port: 22, BasePath: "/backup", Auth: SFTPAuthSpec{Type: "Password", UsernameRef: SecretKeyReference{Namespace: "backup-system", Name: "sftp", Key: "username"}, PasswordRef: &SecretKeyReference{Namespace: "backup-system", Name: "sftp", Key: "password"}}}}}
	repository.Default()
	_, err := repository.ValidateCreate()
	require.ErrorContains(t, err, "knownHostsRef")
	repository.Spec.SFTP.InsecureSkipHostKeyCheck = true
	_, err = repository.ValidateCreate()
	require.NoError(t, err)
	repository.Spec.SFTP.BasePath = "/backup/../escape"
	_, err = repository.ValidateCreate()
	require.Error(t, err)
}

func TestTaskOneTimeScopeFreeze(t *testing.T) {
	oldTask := &BackupTask{Spec: BackupTaskSpec{ResourceIdentity: ResourceIdentity{ClusterRef: "c1"}, Trigger: BackupTriggerManual, ScopeRef: ObjectReference{Name: "scope"}, RepositoryRef: ObjectReference{Name: "repo"}, Timeout: metav1.Duration{Duration: time.Hour}}, Status: BackupTaskStatus{CommonStatus: CommonStatus{Phase: BackupPhasePending}}}
	newTask := oldTask.DeepCopy()
	newTask.Spec.ScopeSnapshot = &BackupScopeSpec{ResourceIdentity: ResourceIdentity{ClusterRef: "c1"}, Mode: BackupScopeModeNamespace, IncludeNamespaces: []string{"app"}}
	newTask.Spec.ScopeGeneration = 3
	_, err := newTask.ValidateUpdate(oldTask)
	require.NoError(t, err)
	oldTask = newTask.DeepCopy()
	oldTask.Status.Phase = BackupPhasePreparing
	newTask = oldTask.DeepCopy()
	newTask.Spec.ScopeSnapshot.IncludeNamespaces = []string{"other"}
	_, err = newTask.ValidateUpdate(oldTask)
	require.ErrorContains(t, err, "immutable")
}

func TestRestoreHighRiskRequiresConfirmation(t *testing.T) {
	task := &RestoreTask{Spec: RestoreTaskSpec{ResourceIdentity: ResourceIdentity{ClusterRef: "c1"}, BackupRecordRef: ObjectReference{Name: "record"}, TargetClusterRef: "c1", RestorePVC: true, ConflictPolicy: RestoreConflictPolicy{Default: ConflictOverwrite, AllowRecreate: true}}}
	task.Default()
	_, err := task.ValidateCreate()
	require.ErrorContains(t, err, "highRiskConfirmed")
	task.Spec.ConflictPolicy.HighRiskConfirmed = true
	_, err = task.ValidateCreate()
	require.NoError(t, err)
}

func TestRecordDeleteRequiresExplicitModeAndConfirmation(t *testing.T) {
	record := &BackupRecord{}
	_, err := record.ValidateDelete()
	require.ErrorContains(t, err, AnnotationDeleteConfirmed)
	record.Annotations = map[string]string{AnnotationDeleteConfirmed: "true", AnnotationDeleteMode: DeleteModeRepositoryData}
	_, err = record.ValidateDelete()
	require.NoError(t, err)
}
