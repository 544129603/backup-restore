// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	opererrors "github.com/example/backup-restore-operator/internal/errors"
)

// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func removeString(values []string, expected string) []string {
	result := values[:0]
	for _, value := range values {
		if value != expected {
			result = append(result, value)
		}
	}
	return result
}

func statusPatch(ctx context.Context, c client.Client, object client.Object, before client.Object) error {
	return c.Status().Patch(ctx, object, client.MergeFrom(before))
}

func errorDetail(err error) protectionv1alpha1.ErrorDetail {
	code, retryable := opererrors.Code(err), opererrors.Retryable(err)
	return protectionv1alpha1.ErrorDetail{Code: code, Message: err.Error(), Retryable: retryable, At: metav1.Now()}
}

func taskDirectory(workspace, uid string) (string, error) {
	if workspace == "" {
		workspace = "/tmp/backup-restore"
	}
	if uid == "" {
		return "", errors.New("task UID is empty")
	}
	root := filepath.Join(workspace, uid)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

func getRepository(ctx context.Context, c client.Client, name string) (*protectionv1alpha1.BackupRepository, error) {
	repository := &protectionv1alpha1.BackupRepository{}
	if err := c.Get(ctx, client.ObjectKey{Name: name}, repository); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, opererrors.New(opererrors.CodeRecordNotFound, fmt.Sprintf("repository %s does not exist", name), false, err)
		}
		return nil, err
	}
	return repository, nil
}

func terminalBackup(phase string) bool {
	return phase == protectionv1alpha1.BackupPhaseCompleted || phase == protectionv1alpha1.BackupPhasePartiallyFailed || phase == protectionv1alpha1.BackupPhaseFailed || phase == protectionv1alpha1.BackupPhaseCancelled
}

func committedBackup(phase string) bool {
	return phase == protectionv1alpha1.BackupPhaseUploading || phase == protectionv1alpha1.BackupPhaseVerifying || phase == protectionv1alpha1.BackupPhaseGeneratingRecord || terminalBackup(phase)
}

func terminalRestore(phase string) bool {
	return phase == protectionv1alpha1.RestorePhaseCompleted || phase == protectionv1alpha1.RestorePhasePartiallyFailed || phase == protectionv1alpha1.RestorePhaseFailed || phase == protectionv1alpha1.RestorePhaseCancelled
}

func timedOut(start *metav1.Time, timeout metav1.Duration, now time.Time) bool {
	return start != nil && timeout.Duration > 0 && now.After(start.Add(timeout.Duration))
}

func appendCheckpoint(values []protectionv1alpha1.TaskCheckpoint, step, key string) []protectionv1alpha1.TaskCheckpoint {
	values = append(values, protectionv1alpha1.TaskCheckpoint{Step: step, Key: key, Completed: true, UpdatedAt: metav1.Now()})
	if len(values) > 30 {
		values = values[len(values)-30:]
	}
	return values
}
