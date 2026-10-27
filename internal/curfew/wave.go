package curfew

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"curfew/internal/values"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Pod and readiness changes are not watched, so a waiting workload polls.
	wavePoll    = 10 * time.Second
	waveTimeout = 10 * time.Minute
)

// wave reads curfew/wave, default 0. Like Argo CD sync waves, negative values go before the default.
func wave(o metav1.Object) (int, error) {
	v, ok := o.GetAnnotations()[values.WaveAnnotation]
	if !ok {
		return 0, nil
	}
	return strconv.Atoi(strings.TrimSpace(v))
}

// waveBlocker names a workload in obj's namespace that must finish first, "" when none: on sleep,
// lower waves going to sleep must be fully stopped; on wake, higher waves waking up must be ready.
func (r *reconciler) waveBlocker(ctx context.Context, obj client.Object, ns *corev1.Namespace, asleep bool, state map[string]string) (string, error) {
	mine, err := wave(obj)
	if err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Invalid wave, using 0")
	}

	var blocker string
	for _, list := range []client.ObjectList{&appsv1.DeploymentList{}, &appsv1.StatefulSetList{}, &batchv1.CronJobList{}} {
		if err := r.client.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
			return "", err
		}
		err := meta.EachListItem(list, func(o runtime.Object) error {
			peer, ok := o.(client.Object)
			if !ok || blocker != "" {
				return nil
			}
			kind := kindOf(peer)
			if !r.goesBefore(peer, kind, ns, mine, asleep) {
				return nil
			}
			done, err := r.peerDone(ctx, peer, kind, asleep, state)
			if err == nil && !done {
				blocker = kind + "/" + peer.GetName()
			}
			return err
		})
		if err != nil || blocker != "" {
			return blocker, err
		}
	}
	return "", nil
}

// goesBefore reports whether peer is moving the same way (asleep) and must finish before a workload
// of wave mine. A broken wave or schedule never holds others back.
func (r *reconciler) goesBefore(peer client.Object, kind string, ns *corev1.Namespace, mine int, asleep bool) bool {
	w, err := wave(peer)
	if err != nil || asleep && w >= mine || !asleep && w <= mine {
		return false
	}
	sched := scheduleFor(peer, kind, ns)
	if sched == nil {
		return false
	}
	peerAsleep, _, err := evaluate(sched, r.now())
	return err == nil && peerAsleep == asleep
}

func kindOf(o client.Object) string {
	switch o.(type) {
	case *appsv1.Deployment:
		return "Deployment"
	case *appsv1.StatefulSet:
		return "StatefulSet"
	}
	return "CronJob"
}

// peerDone reports whether peer finished going to sleep (no pods left, terminating ones included,
// or no running Jobs) or waking up (back from curfew-state and ready).
func (r *reconciler) peerDone(ctx context.Context, peer client.Object, kind string, asleep bool, state map[string]string) (bool, error) {
	if !asleep {
		if _, sleeping := state[fmt.Sprintf("%s.%s.%s", strings.ToLower(kind), peer.GetNamespace(), peer.GetName())]; sleeping {
			return false, nil
		}
	}

	var replicas, ready int32
	var selector *metav1.LabelSelector
	switch p := peer.(type) {
	case *batchv1.CronJob:
		// Back from curfew-state is enough on wake: one suspended by its owner stays suspended.
		return !asleep || ptr.Deref(p.Spec.Suspend, false) && len(p.Status.Active) == 0, nil
	case *appsv1.Deployment:
		replicas, ready, selector = ptr.Deref(p.Spec.Replicas, 1), p.Status.ReadyReplicas, p.Spec.Selector
		if p.Status.ObservedGeneration < p.Generation {
			return false, nil
		}
	case *appsv1.StatefulSet:
		replicas, ready, selector = ptr.Deref(p.Spec.Replicas, 1), p.Status.ReadyReplicas, p.Spec.Selector
		if p.Status.ObservedGeneration < p.Generation {
			return false, nil
		}
	}

	if !asleep {
		return ready >= replicas, nil
	}
	if replicas > 0 {
		return false, nil
	}
	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return false, err
	}
	// Uncached, so curfew keeps no cluster-wide Pod cache.
	pods := &corev1.PodList{}
	if err := r.reader.List(ctx, pods, client.InNamespace(peer.GetNamespace()), client.MatchingLabelsSelector{Selector: sel}, client.Limit(1)); err != nil {
		return false, err
	}
	return len(pods.Items) == 0, nil
}

// waiting records how long key has been held back by blocker and says whether to keep waiting.
func (r *reconciler) waiting(log logr.Logger, key, blocker string) bool {
	since, ok := r.waitingSince[key]
	switch {
	case blocker == "":
	case r.dryRun:
		// Nothing really stops in dry-run, so waiting would always time out.
		log.Info("Would wait for wave", "blocker", blocker)
	case !ok:
		log.Info("Waiting for wave", "blocker", blocker)
		r.waitingSince[key] = r.now()
		return true
	case r.now().Sub(since) < waveTimeout:
		return true
	default:
		log.Info("Wave timeout, proceeding", "blocker", blocker, "waited", r.now().Sub(since))
	}
	delete(r.waitingSince, key)
	return false
}
