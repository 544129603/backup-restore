// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func durationOr(current metav1.Duration, fallback time.Duration) metav1.Duration {
	if current.Duration <= 0 {
		return metav1.Duration{Duration: fallback}
	}
	return current
}

func defaultRetryPolicy(p *RetryPolicy) {
	if p.MaxAttempts == 0 {
		p.MaxAttempts = 3
	}
	p.Backoff = durationOr(p.Backoff, 30*time.Second)
	p.MaxBackoff = durationOr(p.MaxBackoff, 10*time.Minute)
}

func defaultRetention(retention *RetentionSpec) {
	if retention.MaxCopies == 0 {
		retention.MaxCopies = 7
	}
	if retention.MinCopies == 0 {
		retention.MinCopies = 1
	}
	if retention.MaxAgeDays == 0 {
		retention.MaxAgeDays = 30
	}
}

func defaultBackupExecution(spec *BackupExecutionSpec) {
	defaultSelection(&spec.Selection)
	defaultRetention(&spec.Retention)
	spec.Timeout = durationOr(spec.Timeout, 4*time.Hour)
	defaultRetryPolicy(&spec.RetryPolicy)
	if spec.FailurePolicy == "" {
		spec.FailurePolicy = "Continue"
	}
}

// +kubebuilder:webhook:path=/mutate-protection-platform-io-v1alpha1-backuprepository,mutating=true,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=backuprepositories,verbs=create;update,versions=v1alpha1,name=mbackuprepository.protection.platform.io,admissionReviewVersions=v1
func (r *BackupRepository) Default() {
	r.Spec.HealthCheckInterval = durationOr(r.Spec.HealthCheckInterval, 30*time.Minute)
	r.Spec.Timeout = durationOr(r.Spec.Timeout, 30*time.Second)
	if r.Spec.RetryCount == 0 {
		r.Spec.RetryCount = 3
	}
	if r.Spec.Compression.Algorithm == "" {
		r.Spec.Compression.Algorithm = "Gzip"
	}
	if r.Spec.Compression.Level == 0 {
		r.Spec.Compression.Level = 6
	}
	if r.Spec.Encryption.Algorithm == "" {
		r.Spec.Encryption.Algorithm = "AES256GCM"
	}
	if r.Spec.SFTP != nil {
		if r.Spec.SFTP.Port == 0 {
			r.Spec.SFTP.Port = 22
		}
		r.Spec.SFTP.ConnectTimeout = durationOr(r.Spec.SFTP.ConnectTimeout, 10*time.Second)
		r.Spec.SFTP.OperationTimeout = durationOr(r.Spec.SFTP.OperationTimeout, 5*time.Minute)
		r.Spec.SFTP.KeepAliveInterval = durationOr(r.Spec.SFTP.KeepAliveInterval, 30*time.Second)
		if r.Spec.SFTP.MaxConnections == 0 {
			r.Spec.SFTP.MaxConnections = 4
		}
	}
}

func defaultSelection(selection *BackupSelectionSpec) {
	if selection.PVC.FailurePolicy == "" {
		selection.PVC.FailurePolicy = "ContinueAndMarkPartial"
	}
	if selection.PVC.Lifecycle == "" {
		selection.PVC.Lifecycle = "RetainAfterRecordDeletion"
	}
	selection.PVC.SnapshotTimeout = durationOr(selection.PVC.SnapshotTimeout, 10*time.Minute)
	if selection.ConsistencyMode == "" {
		selection.ConsistencyMode = "CrashConsistent"
	}
}

// +kubebuilder:webhook:path=/mutate-protection-platform-io-v1alpha1-backuppolicy,mutating=true,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=backuppolicies,verbs=create;update,versions=v1alpha1,name=mbackuppolicy.protection.platform.io,admissionReviewVersions=v1
func (p *BackupPolicy) Default() {
	defaultSelection(&p.Spec.Selection)
	if p.Spec.Schedule.Timezone == "" {
		p.Spec.Schedule.Timezone = "Etc/UTC"
	}
	if p.Spec.ConcurrencyPolicy == "" {
		p.Spec.ConcurrencyPolicy = ConcurrencyForbid
	}
	if p.Spec.MissedRunPolicy == "" {
		p.Spec.MissedRunPolicy = MissedRunSkip
	}
	p.Spec.StartingDeadline = durationOr(p.Spec.StartingDeadline, time.Hour)
	if p.Spec.MaxCatchUpRuns == 0 {
		p.Spec.MaxCatchUpRuns = 1
	}
	defaultRetention(&p.Spec.Retention)
	defaultRetryPolicy(&p.Spec.RetryPolicy)
	p.Spec.Timeout = durationOr(p.Spec.Timeout, 4*time.Hour)
}

// +kubebuilder:webhook:path=/mutate-protection-platform-io-v1alpha1-backuptask,mutating=true,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=backuptasks,verbs=create;update,versions=v1alpha1,name=mbackuptask.protection.platform.io,admissionReviewVersions=v1
func (t *BackupTask) Default() {
	if t.Spec.Trigger == "" {
		t.Spec.Trigger = BackupTriggerManual
	}
	if t.Spec.Source.Type == "" {
		if t.Spec.Source.PolicyRef != nil {
			t.Spec.Source.Type = BackupTaskSourcePolicy
		} else if t.Spec.BackupSpec != nil {
			t.Spec.Source.Type = BackupTaskSourceOneTime
		}
	}
	if t.Spec.BackupSpec != nil {
		if t.Spec.Source.Type == BackupTaskSourceOneTime && t.Spec.BackupSpec.Retention.MaxCopies == 0 {
			t.Spec.BackupSpec.Retention.MaxCopies = 1
		}
		defaultBackupExecution(t.Spec.BackupSpec)
	}
}

// +kubebuilder:webhook:path=/mutate-protection-platform-io-v1alpha1-restoretask,mutating=true,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=restoretasks,verbs=create;update,versions=v1alpha1,name=mrestoretask.protection.platform.io,admissionReviewVersions=v1
func (r *RestoreTask) Default() {
	if r.Spec.Trigger == "" {
		r.Spec.Trigger = "Manual"
	}
	if r.Spec.Mode == "" {
		r.Spec.Mode = "Original"
	}
	if r.Spec.ConflictPolicy.Default == "" {
		r.Spec.ConflictPolicy.Default = ConflictSkip
	}
	if r.Spec.FailurePolicy == "" {
		r.Spec.FailurePolicy = "Continue"
	}
	r.Spec.Timeout = durationOr(r.Spec.Timeout, 4*time.Hour)
}

// +kubebuilder:webhook:path=/mutate-protection-platform-io-v1alpha1-backuppluginconfig,mutating=true,failurePolicy=fail,sideEffects=None,groups=protection.platform.io,resources=backuppluginconfigs,verbs=create;update,versions=v1alpha1,name=mbackuppluginconfig.protection.platform.io,admissionReviewVersions=v1
func (c *BackupPluginConfig) Default() {
	c.Spec.DefaultTimezone = defaultString(c.Spec.DefaultTimezone, "Etc/UTC")
	c.Spec.DefaultBackupTimeout = durationOr(c.Spec.DefaultBackupTimeout, 4*time.Hour)
	c.Spec.DefaultRestoreTimeout = durationOr(c.Spec.DefaultRestoreTimeout, 4*time.Hour)
	c.Spec.DefaultSnapshotTimeout = durationOr(c.Spec.DefaultSnapshotTimeout, 10*time.Minute)
	if c.Spec.Concurrency.MaxBackupTasks == 0 {
		c.Spec.Concurrency.MaxBackupTasks = 3
	}
	if c.Spec.Concurrency.MaxRestoreTasks == 0 {
		c.Spec.Concurrency.MaxRestoreTasks = 1
	}
	if c.Spec.Concurrency.MaxSnapshotsPerTask == 0 {
		c.Spec.Concurrency.MaxSnapshotsPerTask = 10
	}
	if c.Spec.Concurrency.MaxRepositoryOperations == 0 {
		c.Spec.Concurrency.MaxRepositoryOperations = 4
	}
	if c.Spec.KubernetesClient.QPS == 0 {
		c.Spec.KubernetesClient.QPS = 20
	}
	if c.Spec.KubernetesClient.Burst == 0 {
		c.Spec.KubernetesClient.Burst = 40
	}
	if c.Spec.KubernetesClient.PageSize == 0 {
		c.Spec.KubernetesClient.PageSize = 500
	}
	if len(c.Spec.Security.AllowedSecretNamespaces) == 0 {
		c.Spec.Security.AllowedSecretNamespaces = []string{"backup-system"}
	}
	c.Spec.GarbageCollection.Interval = durationOr(c.Spec.GarbageCollection.Interval, time.Hour)
	c.Spec.GarbageCollection.StagingGracePeriod = durationOr(c.Spec.GarbageCollection.StagingGracePeriod, 24*time.Hour)
	if c.Spec.GarbageCollection.TerminalTaskTTLDays == 0 {
		c.Spec.GarbageCollection.TerminalTaskTTLDays = 90
	}
	c.Spec.WorkspacePath = defaultString(c.Spec.WorkspacePath, "/workspace")
	c.Spec.LogLevel = defaultString(c.Spec.LogLevel, "info")
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
