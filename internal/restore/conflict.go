// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package restore

import (
	"fmt"
	"strings"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
)

type ConflictResolver struct {
	Policy protectionv1alpha1.RestoreConflictPolicy
}

func (r ConflictResolver) PolicyFor(gvr string) string {
	if policy := r.Policy.PerResource[gvr]; policy != "" {
		return policy
	}
	if short := strings.SplitN(gvr, ".", 2)[0]; r.Policy.PerResource[short] != "" {
		return r.Policy.PerResource[short]
	}
	if r.Policy.Default == "" {
		return protectionv1alpha1.ConflictSkip
	}
	return r.Policy.Default
}

func (r ConflictResolver) Resolve(gvr, kind string, exists bool) (string, error) {
	if !exists {
		return "Create", nil
	}
	switch policy := r.PolicyFor(gvr); policy {
	case protectionv1alpha1.ConflictSkip:
		return "Skip", nil
	case protectionv1alpha1.ConflictFail:
		return "Fail", fmt.Errorf("target %s already exists", kind)
	case protectionv1alpha1.ConflictOverwrite:
		return "Overwrite", nil
	case protectionv1alpha1.ConflictRename:
		if !RenameSupported(kind) {
			return "Fail", fmt.Errorf("Rename is not supported for %s", kind)
		}
		return "Rename", nil
	default:
		return "Fail", fmt.Errorf("unsupported conflict policy %q", policy)
	}
}

func RenameSupported(kind string) bool {
	switch kind {
	case "ConfigMap", "Secret", "ServiceAccount", "Service", "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "Ingress", "Role", "RoleBinding":
		return true
	default:
		return false
	}
}
