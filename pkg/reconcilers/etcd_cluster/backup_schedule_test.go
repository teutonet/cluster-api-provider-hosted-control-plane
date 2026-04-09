package etcd_cluster

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestDailyScheduleFor(t *testing.T) {
	t.Run("result is within the 20:00–03:59 window", func(t *testing.T) {
		g := NewWithT(t)
		identities := [][2]string{
			{"ns-a", "cluster-1"},
			{"ns-b", "cluster-2"},
			{"production", "control-plane"},
			{"kube-system", "etcd-backup"},
			{"", "no-namespace"},
		}
		for _, id := range identities {
			expr := dailyScheduleFor(id[0], id[1])

			var minute, hour int
			_, parseErr := fmt.Sscanf(expr, "%d %d * * *", &minute, &hour)
			g.Expect(parseErr).NotTo(HaveOccurred(), "expression %q could not be parsed", expr)
			g.Expect(minute).To(BeNumerically(">=", 0))
			g.Expect(minute).To(BeNumerically("<=", 59))
			// Hour must be within the 8-hour window: 20, 21, 22, 23, 0, 1, 2, 3
			validHours := []int{20, 21, 22, 23, 0, 1, 2, 3}
			g.Expect(validHours).
				To(ContainElement(hour), "hour %d is outside the 8-hour window for %s/%s", hour, id[0], id[1])
		}
	})

	t.Run("is deterministic for the same cluster", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(dailyScheduleFor("default", "my-cluster")).To(Equal(dailyScheduleFor("default", "my-cluster")))
	})

	t.Run("produces different schedules for different clusters", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(dailyScheduleFor("ns", "cluster-a")).NotTo(Equal(dailyScheduleFor("ns", "cluster-b")))
	})
}

func TestResolveBackupSchedule(t *testing.T) {
	t.Run("@daily returns a usable schedule within the midnight window", func(t *testing.T) {
		g := NewWithT(t)
		schedule, err := resolveBackupSchedule("@daily", "default", "my-cluster")
		g.Expect(err).NotTo(HaveOccurred())

		// The resolved schedule must fire exactly once per day; its next-run from a known
		// base should be within 24h and land in the 20:00–03:59 window.
		base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		next := schedule.Next(base)
		g.Expect(next).NotTo(BeZero())
		hour := next.Hour()
		validHours := []int{20, 21, 22, 23, 0, 1, 2, 3}
		g.Expect(validHours).To(ContainElement(hour))
	})

	t.Run("@daily is deterministic for the same cluster", func(t *testing.T) {
		g := NewWithT(t)
		base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		s1, _ := resolveBackupSchedule("@daily", "default", "my-cluster")
		s2, _ := resolveBackupSchedule("@daily", "default", "my-cluster")
		g.Expect(s1.Next(base)).To(Equal(s2.Next(base)))
	})

	t.Run("@daily produces different next-run times for different clusters", func(t *testing.T) {
		g := NewWithT(t)
		base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		a, _ := resolveBackupSchedule("@daily", "ns", "cluster-a")
		b, _ := resolveBackupSchedule("@daily", "ns", "cluster-b")
		g.Expect(a.Next(base)).NotTo(Equal(b.Next(base)))
	})

	t.Run("standard cron parses successfully", func(t *testing.T) {
		g := NewWithT(t)
		schedule, err := resolveBackupSchedule("0 2 * * *", "ns", "cluster")
		g.Expect(err).NotTo(HaveOccurred())
		base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		g.Expect(schedule.Next(base).Hour()).To(Equal(2))
	})

	t.Run("invalid schedule returns an error", func(t *testing.T) {
		g := NewWithT(t)
		_, err := resolveBackupSchedule("invalid cron", "ns", "cluster")
		g.Expect(err).To(HaveOccurred())
	})
}
