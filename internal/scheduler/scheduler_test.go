package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseAndNextWithTimezone(t *testing.T) {
	s, err := Parse("0 2 * * *", "Asia/Shanghai")
	require.NoError(t, err)
	next := s.Next(time.Date(2026, 7, 12, 18, 1, 0, 0, time.UTC))
	require.Equal(t, time.Date(2026, 7, 13, 18, 0, 0, 0, time.UTC), next)
}

func TestDueTimesPolicies(t *testing.T) {
	s, err := Parse("* * * * *", "Etc/UTC")
	require.NoError(t, err)
	last := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	now := last.Add(5*time.Minute + 10*time.Second)
	require.Len(t, s.DueTimes(last, now, time.Hour, "RunOnce", 1), 1)
	require.Len(t, s.DueTimes(last, now, time.Hour, "RunAll", 3), 3)
	require.Len(t, s.DueTimes(last, now, time.Hour, "Skip", 3), 1)
	hourly, err := Parse("0 * * * *", "Etc/UTC")
	require.NoError(t, err)
	require.Empty(t, hourly.DueTimes(last, time.Date(2026, 7, 13, 1, 2, 0, 0, time.UTC), time.Minute, "Skip", 3))
}

func TestDeterministicName(t *testing.T) {
	when := time.Unix(1234567890, 0)
	name := DeterministicTaskName("a-very-long-policy-name-that-needs-to-be-truncated-because-dns-labels-are-short", when)
	require.LessOrEqual(t, len(name), 63)
	require.Equal(t, name, DeterministicTaskName("a-very-long-policy-name-that-needs-to-be-truncated-because-dns-labels-are-short", when))
}

func TestRejectsDescriptorAndEmbeddedTimezone(t *testing.T) {
	_, err := Parse("@daily", "Etc/UTC")
	require.ErrorContains(t, err, "five fields")
	_, err = Parse("TZ=UTC 0 1 * * *", "Etc/UTC")
	require.ErrorContains(t, err, "separately")
}
