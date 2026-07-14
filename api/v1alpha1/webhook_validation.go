// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func SetupWebhooksWithManager(mgr ctrl.Manager) error {
	objects := []runtime.Object{
		&BackupRepository{}, &BackupScope{}, &BackupPolicy{}, &BackupTask{},
		&BackupRecord{}, &RestoreTask{}, &BackupPluginConfig{},
	}
	for _, obj := range objects {
		builder := ctrl.NewWebhookManagedBy(mgr).
			For(obj).
			WithValidator(legacyValidatorAdapter{})
		if _, ok := obj.(legacyDefaulter); ok {
			builder = builder.WithDefaulter(legacyDefaulterAdapter{})
		}
		if err := builder.Complete(); err != nil {
			return err
		}
	}
	return nil
}

// controller-runtime v0.20 requires explicit CustomDefaulter and
// CustomValidator implementations. These adapters retain the object-level
// validation methods, which are also convenient for unit tests.
type legacyDefaulter interface {
	Default()
}

type legacyValidator interface {
	ValidateCreate() (admission.Warnings, error)
	ValidateUpdate(runtime.Object) (admission.Warnings, error)
	ValidateDelete() (admission.Warnings, error)
}

type legacyDefaulterAdapter struct{}

func (legacyDefaulterAdapter) Default(_ context.Context, obj runtime.Object) error {
	defaulter, ok := obj.(legacyDefaulter)
	if !ok {
		return fmt.Errorf("%T does not implement the legacy defaulter contract", obj)
	}
	defaulter.Default()
	return nil
}

type legacyValidatorAdapter struct{}

func (legacyValidatorAdapter) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	validator, ok := obj.(legacyValidator)
	if !ok {
		return nil, fmt.Errorf("%T does not implement the legacy validator contract", obj)
	}
	return validator.ValidateCreate()
}

func (legacyValidatorAdapter) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	validator, ok := newObj.(legacyValidator)
	if !ok {
		return nil, fmt.Errorf("%T does not implement the legacy validator contract", newObj)
	}
	return validator.ValidateUpdate(oldObj)
}

func (legacyValidatorAdapter) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	validator, ok := obj.(legacyValidator)
	if !ok {
		return nil, fmt.Errorf("%T does not implement the legacy validator contract", obj)
	}
	return validator.ValidateDelete()
}

var _ admission.CustomDefaulter = legacyDefaulterAdapter{}
var _ admission.CustomValidator = legacyValidatorAdapter{}

func validateIdentity(i ResourceIdentity) error {
	if i.ClusterRef == "" {
		return fmt.Errorf("spec.clusterRef is required")
	}
	if i.ProjectRef == "" {
		return fmt.Errorf("spec.projectRef is required")
	}
	return nil
}

func validateSecretRef(ref *SecretKeyReference, field string) error {
	if ref == nil || ref.Namespace == "" || ref.Name == "" || ref.Key == "" {
		return fmt.Errorf("%s must include namespace, name and key", field)
	}
	if ref.Namespace != "backup-system" {
		return fmt.Errorf("%s namespace must be backup-system in v1alpha1", field)
	}
	return nil
}

func safeAbsolutePath(value string) bool {
	if value == "" || !path.IsAbs(value) || value == "/" {
		return false
	}
	clean := path.Clean(value)
	if clean != value {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// +kubebuilder:webhook:path=/validate-protection-platform-io-v1alpha1-backuprepository,mutating=false,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=backuprepositories,verbs=create;update,versions=v1alpha1,name=vbackuprepository.protection.platform.io,admissionReviewVersions=v1
func (r *BackupRepository) ValidateCreate() (admission.Warnings, error) { return nil, r.validate() }
func (r *BackupRepository) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	previous, ok := old.(*BackupRepository)
	if !ok {
		return nil, fmt.Errorf("expected BackupRepository")
	}
	if r.Spec.ClusterRef != previous.Spec.ClusterRef || r.Spec.ProjectRef != previous.Spec.ProjectRef || r.Spec.Type != previous.Spec.Type {
		return nil, fmt.Errorf("clusterRef, projectRef and type are immutable")
	}
	return nil, r.validate()
}
func (r *BackupRepository) ValidateDelete() (admission.Warnings, error) { return nil, nil }
func (r *BackupRepository) validate() error {
	if err := validateIdentity(r.Spec.ResourceIdentity); err != nil {
		return err
	}
	switch r.Spec.Type {
	case RepositoryTypeLocal:
		if r.Spec.Local == nil || r.Spec.SFTP != nil {
			return fmt.Errorf("exactly local configuration is required")
		}
		if r.Spec.Local.Mode == LocalModeHostPath {
			if !safeAbsolutePath(r.Spec.Local.Path) {
				return fmt.Errorf("local.path must be a safe absolute path and cannot be root")
			}
			if r.Spec.Local.NodeName == "" && len(r.Spec.Local.NodeSelector) == 0 {
				return fmt.Errorf("hostPath repository requires nodeName or nodeSelector")
			}
		} else if r.Spec.Local.Mode == LocalModePVC {
			if r.Spec.Local.PVC == nil || r.Spec.Local.PVC.Namespace == "" || r.Spec.Local.PVC.Name == "" || !safeAbsolutePath(r.Spec.Local.PVC.MountPath) {
				return fmt.Errorf("PVC repository requires namespace, name and a safe absolute mountPath")
			}
		} else {
			return fmt.Errorf("unsupported local mode %q", r.Spec.Local.Mode)
		}
	case RepositoryTypeSFTP:
		if r.Spec.SFTP == nil || r.Spec.Local != nil {
			return fmt.Errorf("exactly sftp configuration is required")
		}
		s := r.Spec.SFTP
		if s.Host == "" || s.Port < 1 || s.Port > 65535 || !safeAbsolutePath(s.BasePath) {
			return fmt.Errorf("SFTP host, port and safe absolute basePath are required")
		}
		if err := validateSecretRef(&s.Auth.UsernameRef, "sftp.auth.usernameRef"); err != nil {
			return err
		}
		switch s.Auth.Type {
		case "Password":
			if err := validateSecretRef(s.Auth.PasswordRef, "sftp.auth.passwordRef"); err != nil {
				return err
			}
			if s.Auth.PrivateKeyRef != nil {
				return fmt.Errorf("password and private key authentication are mutually exclusive")
			}
		case "PrivateKey":
			if err := validateSecretRef(s.Auth.PrivateKeyRef, "sftp.auth.privateKeyRef"); err != nil {
				return err
			}
			if s.Auth.PasswordRef != nil {
				return fmt.Errorf("password and private key authentication are mutually exclusive")
			}
		default:
			return fmt.Errorf("unsupported SFTP auth type %q", s.Auth.Type)
		}
		if !s.InsecureSkipHostKeyCheck {
			if err := validateSecretRef(s.KnownHostsRef, "sftp.knownHostsRef"); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported repository type %q", r.Spec.Type)
	}
	if r.Spec.Encryption.Enabled {
		return validateSecretRef(r.Spec.Encryption.KeyRef, "encryption.keyRef")
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-protection-platform-io-v1alpha1-backupscope,mutating=false,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=backupscopes,verbs=create;update,versions=v1alpha1,name=vbackupscope.protection.platform.io,admissionReviewVersions=v1
func (s *BackupScope) ValidateCreate() (admission.Warnings, error) { return nil, s.validate() }
func (s *BackupScope) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	previous, ok := old.(*BackupScope)
	if !ok {
		return nil, fmt.Errorf("expected BackupScope")
	}
	if s.Spec.ClusterRef != previous.Spec.ClusterRef || s.Spec.ProjectRef != previous.Spec.ProjectRef || s.Spec.Mode != previous.Spec.Mode {
		return nil, fmt.Errorf("clusterRef, projectRef and mode are immutable")
	}
	return nil, s.validate()
}
func (s *BackupScope) ValidateDelete() (admission.Warnings, error) { return nil, nil }
func (s *BackupScope) validate() error {
	if err := validateIdentity(s.Spec.ResourceIdentity); err != nil {
		return err
	}
	if s.Spec.Mode == BackupScopeModeNamespace && len(s.Spec.IncludeNamespaces) == 0 {
		return fmt.Errorf("namespace mode requires includeNamespaces")
	}
	if s.Spec.Mode == BackupScopeModeCluster && len(s.Spec.IncludeNamespaces) > 0 {
		return fmt.Errorf("cluster mode cannot set includeNamespaces")
	}
	if s.Spec.Mode != BackupScopeModeCluster && s.Spec.Mode != BackupScopeModeNamespace {
		return fmt.Errorf("unsupported scope mode %q", s.Spec.Mode)
	}
	if overlap(s.Spec.IncludeNamespaces, s.Spec.ExcludeNamespaces) {
		return fmt.Errorf("includeNamespaces and excludeNamespaces overlap")
	}
	if overlap(s.Spec.Resources.Include, s.Spec.Resources.Exclude) || overlap(s.Spec.Resources.IncludeCluster, s.Spec.Resources.ExcludeCluster) {
		return fmt.Errorf("resource include and exclude rules overlap")
	}
	if s.Spec.LabelSelector != nil {
		if _, err := metav1LabelSelector(s.Spec.LabelSelector); err != nil {
			return fmt.Errorf("invalid labelSelector: %w", err)
		}
	}
	if s.Spec.ConsistencyMode != "" && s.Spec.ConsistencyMode != "CrashConsistent" {
		return fmt.Errorf("only CrashConsistent is supported in v1alpha1 MVP")
	}
	if len(s.Spec.Hooks.Pre) > 0 || len(s.Spec.Hooks.Post) > 0 {
		return fmt.Errorf("resource hooks are reserved for v1.1 and must be empty in v1alpha1 MVP")
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-protection-platform-io-v1alpha1-backuppolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=backuppolicies,verbs=create;update,versions=v1alpha1,name=vbackuppolicy.protection.platform.io,admissionReviewVersions=v1
func (p *BackupPolicy) ValidateCreate() (admission.Warnings, error) { return nil, p.validate() }
func (p *BackupPolicy) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	previous, ok := old.(*BackupPolicy)
	if !ok {
		return nil, fmt.Errorf("expected BackupPolicy")
	}
	if p.Spec.ClusterRef != previous.Spec.ClusterRef || p.Spec.ProjectRef != previous.Spec.ProjectRef {
		return nil, fmt.Errorf("clusterRef and projectRef are immutable")
	}
	return nil, p.validate()
}
func (p *BackupPolicy) ValidateDelete() (admission.Warnings, error) { return nil, nil }
func (p *BackupPolicy) validate() error {
	if err := validateIdentity(p.Spec.ResourceIdentity); err != nil {
		return err
	}
	if p.Spec.ScopeRef.Name == "" || p.Spec.RepositoryRef.Name == "" {
		return fmt.Errorf("scopeRef and repositoryRef are required")
	}
	if len(strings.Fields(p.Spec.Schedule.Cron)) != 5 {
		return fmt.Errorf("cron must contain exactly five fields")
	}
	if _, err := cron.ParseStandard(p.Spec.Schedule.Cron); err != nil {
		return fmt.Errorf("invalid five-field cron: %w", err)
	}
	if _, err := time.LoadLocation(p.Spec.Schedule.Timezone); err != nil {
		return fmt.Errorf("invalid IANA timezone: %w", err)
	}
	if p.Spec.Timeout.Duration <= 0 || p.Spec.Retention.MaxCopies < 1 || p.Spec.Retention.MinCopies > p.Spec.Retention.MaxCopies || p.Spec.Retention.MaxAgeDays < 1 {
		return fmt.Errorf("timeout and retention values are invalid")
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-protection-platform-io-v1alpha1-backuptask,mutating=false,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=backuptasks,verbs=create;update,versions=v1alpha1,name=vbackuptask.protection.platform.io,admissionReviewVersions=v1
func (t *BackupTask) ValidateCreate() (admission.Warnings, error) { return nil, t.validate() }
func (t *BackupTask) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	previous, ok := old.(*BackupTask)
	if !ok {
		return nil, fmt.Errorf("expected BackupTask")
	}
	oldSpec, newSpec := previous.Spec, t.Spec
	oldSpec.CancelRequested, newSpec.CancelRequested = false, false
	oldSpec.CancelReason, newSpec.CancelReason = "", ""
	// A manual task may resolve and freeze its scope exactly once before work
	// starts. Subsequent updates remain immutable.
	if previous.Spec.ScopeSnapshot == nil && t.Spec.ScopeSnapshot != nil && (previous.Status.Phase == "" || previous.Status.Phase == BackupPhasePending) {
		oldSpec.ScopeSnapshot = newSpec.ScopeSnapshot
		oldSpec.ScopeGeneration = newSpec.ScopeGeneration
		oldSpec.RepositoryGeneration = newSpec.RepositoryGeneration
	}
	if !reflect.DeepEqual(oldSpec, newSpec) {
		return nil, fmt.Errorf("BackupTask spec is immutable except cancelRequested and cancelReason")
	}
	if previous.Spec.CancelRequested && !t.Spec.CancelRequested {
		return nil, fmt.Errorf("cancelRequested cannot change from true to false")
	}
	return nil, t.validate()
}
func (t *BackupTask) ValidateDelete() (admission.Warnings, error) { return nil, nil }
func (t *BackupTask) validate() error {
	if err := validateIdentity(t.Spec.ResourceIdentity); err != nil {
		return err
	}
	if t.Spec.ScopeRef.Name == "" || t.Spec.RepositoryRef.Name == "" {
		return fmt.Errorf("scopeRef and repositoryRef are required")
	}
	if t.Spec.Trigger == BackupTriggerSchedule && t.Spec.ScheduledAt == nil {
		return fmt.Errorf("scheduled task requires scheduledAt")
	}
	if t.Spec.Trigger == BackupTriggerRetry && t.Spec.ParentTaskRef == nil {
		return fmt.Errorf("retry task requires parentTaskRef")
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-protection-platform-io-v1alpha1-backuprecord,mutating=false,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=backuprecords,verbs=create;update;delete,versions=v1alpha1,name=vbackuprecord.protection.platform.io,admissionReviewVersions=v1
func (r *BackupRecord) ValidateCreate() (admission.Warnings, error) {
	if err := validateIdentity(r.Spec.ResourceIdentity); err != nil {
		return nil, err
	}
	if r.Spec.BackupID == "" || r.Spec.SourceTaskRef.Name == "" || r.Spec.RepositoryRef.Name == "" || !safeRelativePath(r.Spec.BackupPath) || r.Spec.Checksum == "" {
		return nil, fmt.Errorf("backupID, references, safe backupPath and checksum are required")
	}
	return nil, nil
}
func (r *BackupRecord) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	previous, ok := old.(*BackupRecord)
	if !ok {
		return nil, fmt.Errorf("expected BackupRecord")
	}
	if !reflect.DeepEqual(previous.Spec, r.Spec) {
		return nil, fmt.Errorf("BackupRecord spec is immutable")
	}
	return nil, nil
}
func (r *BackupRecord) ValidateDelete() (admission.Warnings, error) {
	if r.Annotations[AnnotationDeleteConfirmed] != "true" {
		return nil, fmt.Errorf("BackupRecord deletion requires annotation %s=true after explicit confirmation", AnnotationDeleteConfirmed)
	}
	switch r.Annotations[AnnotationDeleteMode] {
	case DeleteModeRecordOnly, DeleteModeRepositoryData, DeleteModeRepositoryDataAndSnapshots:
		return nil, nil
	default:
		return nil, fmt.Errorf("BackupRecord deletion requires a valid %s annotation", AnnotationDeleteMode)
	}
}

// +kubebuilder:webhook:path=/validate-protection-platform-io-v1alpha1-restoretask,mutating=false,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=restoretasks,verbs=create;update,versions=v1alpha1,name=vrestoretask.protection.platform.io,admissionReviewVersions=v1
func (r *RestoreTask) ValidateCreate() (admission.Warnings, error) { return nil, r.validate() }
func (r *RestoreTask) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	previous, ok := old.(*RestoreTask)
	if !ok {
		return nil, fmt.Errorf("expected RestoreTask")
	}
	oldSpec, newSpec := previous.Spec, r.Spec
	oldSpec.CancelRequested, newSpec.CancelRequested = false, false
	oldSpec.CancelReason, newSpec.CancelReason = "", ""
	if !reflect.DeepEqual(oldSpec, newSpec) {
		return nil, fmt.Errorf("RestoreTask spec is immutable except cancelRequested and cancelReason")
	}
	if previous.Spec.CancelRequested && !r.Spec.CancelRequested {
		return nil, fmt.Errorf("cancelRequested cannot change from true to false")
	}
	return nil, r.validate()
}
func (r *RestoreTask) ValidateDelete() (admission.Warnings, error) { return nil, nil }
func (r *RestoreTask) validate() error {
	if err := validateIdentity(r.Spec.ResourceIdentity); err != nil {
		return err
	}
	if r.Spec.BackupRecordRef.Name == "" || r.Spec.TargetClusterRef == "" {
		return fmt.Errorf("backupRecordRef and targetClusterRef are required")
	}
	seen := map[string]string{}
	for source, target := range r.Spec.NamespaceMapping {
		if source == "" || target == "" {
			return fmt.Errorf("namespace mapping source and target cannot be empty")
		}
		if previous, exists := seen[target]; exists && previous != source {
			return fmt.Errorf("multiple source namespaces map to target %q", target)
		}
		seen[target] = source
	}
	if r.Spec.MetadataOnly && r.Spec.RestorePVC {
		return fmt.Errorf("metadataOnly and restorePVC cannot both be true")
	}
	if r.Spec.ConflictPolicy.AllowRecreate && !r.Spec.ConflictPolicy.HighRiskConfirmed {
		return fmt.Errorf("allowRecreate requires highRiskConfirmed")
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-protection-platform-io-v1alpha1-backuppluginconfig,mutating=false,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=backuppluginconfigs,verbs=create;update,versions=v1alpha1,name=vbackuppluginconfig.protection.platform.io,admissionReviewVersions=v1
func (c *BackupPluginConfig) ValidateCreate() (admission.Warnings, error) { return nil, c.validate() }
func (c *BackupPluginConfig) ValidateUpdate(runtime.Object) (admission.Warnings, error) {
	return nil, c.validate()
}
func (c *BackupPluginConfig) ValidateDelete() (admission.Warnings, error) { return nil, nil }
func (c *BackupPluginConfig) validate() error {
	if c.Name != "cluster" {
		return fmt.Errorf("BackupPluginConfig singleton must be named cluster")
	}
	if _, err := time.LoadLocation(c.Spec.DefaultTimezone); err != nil {
		return fmt.Errorf("invalid default timezone: %w", err)
	}
	if c.Spec.Concurrency.MaxBackupTasks < 1 || c.Spec.Concurrency.MaxRestoreTasks < 1 || c.Spec.KubernetesClient.QPS < 1 || c.Spec.KubernetesClient.Burst < 1 {
		return fmt.Errorf("concurrency, QPS and burst must be positive")
	}
	if len(c.Spec.Security.AllowedSecretNamespaces) != 1 || c.Spec.Security.AllowedSecretNamespaces[0] != "backup-system" {
		return fmt.Errorf("v1alpha1 permits repository credential Secrets only in backup-system")
	}
	return nil
}

func overlap(a, b []string) bool {
	set := make(map[string]struct{}, len(a))
	for _, value := range a {
		set[value] = struct{}{}
	}
	for _, value := range b {
		if _, ok := set[value]; ok {
			return true
		}
	}
	return false
}

func safeRelativePath(value string) bool {
	if value == "" || path.IsAbs(value) || path.Clean(value) != value {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == "" {
			return false
		}
	}
	return true
}
