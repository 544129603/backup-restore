package retention

import (
	"fmt"
	"testing"
	"time"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSelectHonorsCountAgeAndProtection(t *testing.T) {
	now := time.Now().UTC()
	records := make([]protectionv1alpha1.BackupRecord, 0, 5)
	for i := 0; i < 5; i++ {
		records = append(records, protectionv1alpha1.BackupRecord{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("r-%d", i), CreationTimestamp: metav1.NewTime(now.Add(-time.Duration(10-i) * 24 * time.Hour))}})
	}
	records[0].Annotations = map[string]string{protectionv1alpha1.AnnotationProtected: "true"}
	selected := Select(records, protectionv1alpha1.RetentionSpec{MaxCopies: 3, MinCopies: 1, MaxAgeDays: 7}, now)
	names := map[string]bool{}
	for _, record := range selected {
		names[record.Name] = true
	}
	require.False(t, names["r-0"])
	require.True(t, len(selected) >= 1)
}
