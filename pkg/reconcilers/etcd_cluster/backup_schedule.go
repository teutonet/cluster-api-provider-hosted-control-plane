package etcd_cluster

import (
	"fmt"
	"hash/fnv"

	"github.com/robfig/cron/v3"
)

// resolveBackupSchedule converts a schedule string to a concrete 5-field cron expression.
// Vague terms like "@daily" are expanded to a deterministic time within an 8-hour window
// around midnight (20:00–03:59), spread by hashing the cluster's namespace and name so that
// multiple clusters do not all back up at the same moment.
func resolveBackupSchedule(schedule, namespace, name string) (cron.Schedule, error) {
	resolvedSchedule := schedule
	if resolvedSchedule == "@daily" {
		resolvedSchedule = dailyScheduleFor(namespace, name)
	}

	if cronSchedule, err := cron.ParseStandard(resolvedSchedule); err != nil {
		return nil, fmt.Errorf("failed to parse schedule: %w", err)
	} else {
		return cronSchedule, nil
	}
}

// dailyScheduleFor returns a deterministic cron expression within the 8-hour window
// 20:00–03:59 (480 minutes centered on midnight) for the given cluster identity.
func dailyScheduleFor(namespace, name string) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s/%s", namespace, name)
	offset := int(h.Sum32() % 480) // 0–479 minutes into the window
	totalMinutes := 20*60 + offset // 1200 = 20:00; wraps through midnight up to 03:59
	hour := (totalMinutes / 60) % 24
	minute := totalMinutes % 60
	return fmt.Sprintf("%d %d * * *", minute, hour)
}
