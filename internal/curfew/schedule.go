package curfew

import (
	"fmt"
	"time"

	"curfew/internal/values"

	"github.com/robfig/cron/v3"
)

// evaluate reports whether the workload should be asleep at now, and when that next changes.
func evaluate(annotations map[string]string, now time.Time) (bool, time.Time, error) {
	tz := annotations[values.TimezoneAnnotation]
	if tz == "" {
		tz = "UTC"
	}

	next := map[string]time.Time{}
	for _, key := range []string{values.SleepAnnotation, values.WakeAnnotation} {
		sched, err := cron.ParseStandard("CRON_TZ=" + tz + " " + annotations[key])
		if err != nil {
			return false, time.Time{}, fmt.Errorf("%s: %w", key, err)
		}
		if next[key] = sched.Next(now); next[key].IsZero() {
			return false, time.Time{}, fmt.Errorf("%s: never fires", key)
		}
	}

	// Whichever fires first tells which window we are in: wake first means we are asleep.
	sleepAt, wakeAt := next[values.SleepAnnotation], next[values.WakeAnnotation]
	if wakeAt.Before(sleepAt) {
		return true, wakeAt, nil
	}
	return false, sleepAt, nil
}
