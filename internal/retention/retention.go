// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"sort"
	"time"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
)

func Select(records []protectionv1alpha1.BackupRecord, policy protectionv1alpha1.RetentionSpec, now time.Time) []protectionv1alpha1.BackupRecord {
	eligible := make([]protectionv1alpha1.BackupRecord, 0, len(records))
	for _, record := range records {
		if protected(record) || record.Status.Phase == protectionv1alpha1.RecordPhaseVerifying || record.Status.Phase == protectionv1alpha1.RecordPhaseDeleting {
			continue
		}
		eligible = append(eligible, record)
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].CreationTimestamp.Before(&eligible[j].CreationTimestamp) })
	keep := int(policy.MinCopies)
	if keep < 0 {
		keep = 0
	}
	deleteSet := map[string]struct{}{}
	if policy.MaxCopies > 0 && len(eligible) > int(policy.MaxCopies) {
		for _, record := range eligible[:len(eligible)-int(policy.MaxCopies)] {
			deleteSet[record.Name] = struct{}{}
		}
	}
	if policy.MaxAgeDays > 0 {
		cutoff := now.Add(-time.Duration(policy.MaxAgeDays) * 24 * time.Hour)
		for i, record := range eligible {
			if len(eligible)-i <= keep {
				continue
			}
			if record.CreationTimestamp.Time.Before(cutoff) {
				deleteSet[record.Name] = struct{}{}
			}
		}
	}
	result := make([]protectionv1alpha1.BackupRecord, 0, len(deleteSet))
	for _, record := range eligible {
		if _, ok := deleteSet[record.Name]; ok && len(eligible)-len(result) > keep {
			result = append(result, record)
		}
	}
	return result
}

func protected(record protectionv1alpha1.BackupRecord) bool {
	return record.Status.Protected || record.Annotations[protectionv1alpha1.AnnotationProtected] == "true"
}
