package curfew

import (
	"context"
	"fmt"
	"strings"
	"time"

	"curfew/internal/values"

	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func hasSchedule(o metav1.Object) bool {
	_, ok := o.GetAnnotations()[values.SleepAnnotation]
	return ok
}

// scheduleFor returns the annotations holding obj's schedule: its own when it sets curfew/sleep,
// else its namespace's unless the namespace excludes it by kind or label. nil when none applies.
func scheduleFor(obj metav1.Object, kind string, ns *corev1.Namespace) map[string]string {
	switch {
	case strings.HasPrefix(obj.GetNamespace(), values.NsExcludePrefix):
		return nil
	case hasSchedule(obj):
		return obj.GetAnnotations()
	case ns == nil || !hasSchedule(ns) || excluded(ns.GetAnnotations(), kind, obj.GetLabels()):
		return nil
	}
	return ns.GetAnnotations()
}

// excluded reads curfew/exclude-kinds ("Deployment,StatefulSet") and curfew/exclude-labels
// ("tier=data,keep-alive": key=value, or key alone for any value); any match excludes.
func excluded(annotations map[string]string, kind string, labels map[string]string) bool {
	for k := range strings.SplitSeq(annotations[values.ExcludeKindsAnnotation], ",") {
		if strings.EqualFold(strings.TrimSpace(k), kind) {
			return true
		}
	}
	for entry := range strings.SplitSeq(annotations[values.ExcludeLabelsAnnotation], ",") {
		key, want, hasValue := strings.Cut(strings.TrimSpace(entry), "=")
		if v, ok := labels[key]; key != "" && ok && (!hasValue || v == want) {
			return true
		}
	}
	return false
}

// scheduled reports whether obj may follow a schedule, its own or its namespace's.
// Exclusions are left to Reconcile, so a workload that becomes excluded still gets woken up.
func scheduled(ctx context.Context, c client.Reader, obj client.Object) bool {
	if strings.HasPrefix(obj.GetNamespace(), values.NsExcludePrefix) {
		return false
	}
	if hasSchedule(obj) {
		return true
	}
	ns := &corev1.Namespace{}
	return c.Get(ctx, client.ObjectKey{Name: obj.GetNamespace()}, ns) == nil && hasSchedule(ns)
}

// desired reports whether obj should be asleep at now and when that next changes, from its schedule
// and curfew/awake-until. No schedule means awake, with a zero next.
func desired(obj metav1.Object, kind string, ns *corev1.Namespace, now time.Time) (bool, time.Time, error) {
	sched := scheduleFor(obj, kind, ns)
	if sched == nil {
		return false, time.Time{}, nil
	}
	asleep, next, err := evaluate(sched, now)
	if err != nil {
		return false, time.Time{}, err
	}
	until, err := awakeUntil(obj, ns)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("%s: %w", values.AwakeUntilAnnotation, err)
	}
	if now.Before(until) {
		asleep = false
		if until.Before(next) {
			next = until
		}
	}
	return asleep, next, nil
}

// awakeUntil reads curfew/awake-until (RFC 3339) from obj, else from its namespace; zero when unset.
// A namespace's applies to all its workloads, those with their own schedule included.
func awakeUntil(obj metav1.Object, ns *corev1.Namespace) (time.Time, error) {
	v, ok := obj.GetAnnotations()[values.AwakeUntilAnnotation]
	if !ok && ns != nil {
		v, ok = ns.GetAnnotations()[values.AwakeUntilAnnotation]
	}
	if !ok {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(v))
}

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
