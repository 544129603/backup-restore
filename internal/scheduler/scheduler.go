// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type Schedule struct{ schedule cron.Schedule }

func Parse(expression, timezone string) (*Schedule, error) {
	if strings.TrimSpace(expression) == "" {
		return nil, fmt.Errorf("cron expression is required")
	}
	if strings.Contains(expression, "TZ=") {
		return nil, fmt.Errorf("timezone must be specified separately")
	}
	if len(strings.Fields(expression)) != 5 {
		return nil, fmt.Errorf("cron expression must contain exactly five fields")
	}
	if timezone == "" {
		timezone = "Etc/UTC"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", timezone, err)
	}
	parsed, err := parser.Parse("CRON_TZ=" + timezone + " " + expression)
	if err != nil {
		return nil, fmt.Errorf("parse five-field cron: %w", err)
	}
	return &Schedule{schedule: parsed}, nil
}

func (s *Schedule) Next(after time.Time) time.Time { return s.schedule.Next(after).UTC() }

func (s *Schedule) DueTimes(last, now time.Time, deadline time.Duration, missedPolicy string, maxCatchUp int) []time.Time {
	if maxCatchUp < 1 {
		maxCatchUp = 1
	}
	if deadline > 0 && last.Before(now.Add(-deadline)) {
		last = now.Add(-deadline)
	}
	all := make([]time.Time, 0, maxCatchUp)
	cursor := last
	for i := 0; i < 1000; i++ {
		next := s.Next(cursor)
		if next.After(now) {
			break
		}
		all = append(all, next)
		cursor = next
	}
	if len(all) == 0 {
		return nil
	}
	switch missedPolicy {
	case "Skip":
		latest := all[len(all)-1]
		if now.Sub(latest) <= time.Minute {
			return []time.Time{latest}
		}
		return nil
	case "RunOnce":
		return []time.Time{all[len(all)-1]}
	case "RunAll":
		if len(all) > maxCatchUp {
			return all[len(all)-maxCatchUp:]
		}
		return all
	default:
		return []time.Time{all[len(all)-1]}
	}
}

func DeterministicTaskName(policyName string, scheduledAt time.Time) string {
	suffix := fmt.Sprintf("-%d", scheduledAt.UTC().Unix())
	maxPrefix := 63 - len(suffix)
	prefix := strings.Trim(policyName, "-")
	if len(prefix) > maxPrefix {
		prefix = strings.TrimRight(prefix[:maxPrefix], "-")
	}
	if prefix == "" {
		hash := sha256.Sum256([]byte(policyName))
		prefix = "policy-" + hex.EncodeToString(hash[:])[:8]
	}
	return prefix + suffix
}

func ScheduledKey(policyUID string, scheduledAt time.Time) string {
	return policyUID + "/" + scheduledAt.UTC().Format(time.RFC3339)
}
