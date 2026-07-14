// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	once                     sync.Once
	RepositoryAvailable      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "repository_available", Help: "Whether a repository is currently available."}, []string{"type"})
	RepositoryAvailableBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "repository_available_bytes", Help: "Available repository capacity when known."}, []string{"type"})
	RepositoryCheckTotal     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "backup_repository_check_total", Help: "Repository checks."}, []string{"type", "result"})
	RepositoryCheckFailed    = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "backup_repository_check_failed_total", Help: "Failed repository checks."}, []string{"type", "error_code"})
	BackupTaskTotal          = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "backup_task_total", Help: "Backup tasks."}, []string{"trigger", "result"})
	BackupTaskRunning        = prometheus.NewGauge(prometheus.GaugeOpts{Name: "backup_task_running", Help: "Running backup tasks."})
	BackupTaskDuration       = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "backup_task_duration_seconds", Help: "Backup task duration.", Buckets: prometheus.DefBuckets}, []string{"trigger", "result"})
	BackupTaskFailed         = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "backup_task_failed_total", Help: "Failed backup tasks."}, []string{"error_code"})
	BackupTaskBytes          = prometheus.NewCounter(prometheus.CounterOpts{Name: "backup_task_bytes", Help: "Bytes uploaded by backup tasks."})
	BackupTaskObjects        = prometheus.NewCounter(prometheus.CounterOpts{Name: "backup_task_resource_objects", Help: "Objects processed by backup tasks."})
	BackupTaskSnapshots      = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "backup_task_snapshots", Help: "Snapshots processed."}, []string{"result"})
	BackupRecordTotal        = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "backup_record_total", Help: "Backup records by phase."}, []string{"phase"})
	BackupRecordBytes        = prometheus.NewGauge(prometheus.GaugeOpts{Name: "backup_record_bytes", Help: "Bytes referenced by backup records."})
	RestoreTaskTotal         = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "restore_task_total", Help: "Restore tasks."}, []string{"result"})
	RestoreTaskRunning       = prometheus.NewGauge(prometheus.GaugeOpts{Name: "restore_task_running", Help: "Running restore tasks."})
	RestoreTaskDuration      = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "restore_task_duration_seconds", Help: "Restore task duration.", Buckets: prometheus.DefBuckets}, []string{"result"})
	RestoreTaskFailed        = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "restore_task_failed_total", Help: "Failed restore tasks."}, []string{"error_code"})
	SnapshotCreateTotal      = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "snapshot_create_total", Help: "Snapshot create operations."}, []string{"result"})
	SnapshotCreateFailed     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "snapshot_failed_total", Help: "Snapshot failures."}, []string{"error_code"})
	ReconcileTotal           = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "operator_reconcile_total", Help: "Controller reconciliations."}, []string{"controller", "result"})
	ReconcileErrors          = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "operator_reconcile_errors_total", Help: "Controller reconciliation errors."}, []string{"controller", "error_code"})
)

func Register() {
	once.Do(func() {
		ctrlmetrics.Registry.MustRegister(
			RepositoryAvailable, RepositoryAvailableBytes, RepositoryCheckTotal, RepositoryCheckFailed,
			BackupTaskTotal, BackupTaskRunning, BackupTaskDuration, BackupTaskFailed,
			BackupTaskBytes, BackupTaskObjects, BackupTaskSnapshots,
			BackupRecordTotal, BackupRecordBytes, RestoreTaskTotal, RestoreTaskRunning,
			RestoreTaskDuration, RestoreTaskFailed, SnapshotCreateTotal, SnapshotCreateFailed,
			ReconcileTotal, ReconcileErrors,
		)
	})
}
