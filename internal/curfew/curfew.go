// Package that contains the core of the controller
package curfew

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"curfew/internal/logs"
	"curfew/internal/values"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Run starts the controllers for Deployments, StatefulSets and CronJobs and blocks until SIGINT/SIGTERM.
func Run() error {
	ctrl.SetLogger(logr.FromSlogHandler(logs.Logger.Handler()))

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		return errors.New("POD_NAMESPACE is not set")
	}

	config, err := ctrl.GetConfig()
	if err != nil {
		return err
	}

	extra, err := extraKinds(os.Getenv("EXTRA_KINDS"))
	if err != nil {
		return err
	}
	kinds := append(slices.Clone(builtinKinds), extra...)

	opts := ctrl.Options{
		// Extra kinds are unstructured; without this, every Get on them would skip the cache.
		Client:                 client.Options{Cache: &client.CacheOptions{Unstructured: true}},
		HealthProbeBindAddress: ":9090",
		// Off unless METRICS_ADDR is set (Helm value metrics.enabled).
		Metrics:                       metricsserver.Options{BindAddress: cmp.Or(os.Getenv("METRICS_ADDR"), "0")},
		LeaderElection:                true,
		LeaderElectionID:              "curfew-leader",
		LeaderElectionNamespace:       namespace,
		LeaderElectionReleaseOnCancel: true,
	}
	// Only watch these namespaces; RBAC then grants access there only (Helm value watchNamespaces).
	if watch := watchNamespaces(os.Getenv("WATCH_NAMESPACES")); watch != nil {
		opts.Cache.DefaultNamespaces = watch
	}

	mgr, err := ctrl.NewManager(config, opts)
	if err != nil {
		return err
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return err
	}

	for _, w := range kinds {
		r := &reconciler{
			client:       mgr.GetClient(),
			reader:       mgr.GetAPIReader(),
			namespace:    namespace,
			workloadKind: w,
			kinds:        kinds,
			now:          time.Now,
			waitingSince: map[string]time.Time{},
			scaledUp:     map[string]int{},
			dryRun:       os.Getenv("DRY_RUN") == "true",
		}
		err := ctrl.NewControllerManagedBy(mgr).
			For(w.newObj(), builder.WithPredicates(
				watched(mgr.GetClient()),
				predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}, predicate.LabelChangedPredicate{}),
			)).
			Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(r.workloadsIn),
				builder.WithPredicates(namespaceScheduleChanged)).
			Complete(r)
		if err != nil {
			return err
		}
	}

	return mgr.Start(ctrl.SetupSignalHandler())
}

// watchNamespaces parses a comma-separated list; nil means the whole cluster.
func watchNamespaces(list string) map[string]cache.Config {
	var watch map[string]cache.Config
	for ns := range strings.SplitSeq(list, ",") {
		if ns = strings.TrimSpace(ns); ns != "" {
			if watch == nil {
				watch = map[string]cache.Config{}
			}
			watch[ns] = cache.Config{}
		}
	}
	return watch
}

var tracer = otel.Tracer("curfew/internal/curfew")

// watched passes workloads that follow a schedule, their own or their namespace's, including
// updates that remove it, so a sleeping workload gets woken up.
func watched(c client.Reader) predicate.Funcs {
	f := func(o client.Object) bool { return scheduled(context.Background(), c, o) }
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return f(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool { return f(e.ObjectOld) || f(e.ObjectNew) },
		DeleteFunc: func(e event.DeleteEvent) bool { return f(e.Object) },
	}
}

// namespaceScheduleChanged passes namespaces whose annotations change while they have, or had, a
// schedule or a curfew/awake-until. Workloads created later bring their own events.
var namespaceScheduleChanged = predicate.Funcs{
	CreateFunc: func(event.CreateEvent) bool { return false },
	UpdateFunc: func(e event.UpdateEvent) bool {
		matters := func(o client.Object) bool {
			_, override := o.GetAnnotations()[values.AwakeUntilAnnotation]
			return hasSchedule(o) || override
		}
		return (matters(e.ObjectOld) || matters(e.ObjectNew)) &&
			!maps.Equal(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations())
	},
	DeleteFunc:  func(event.DeleteEvent) bool { return false },
	GenericFunc: func(event.GenericEvent) bool { return false },
}

// workloadsIn lists this controller's workloads in a namespace whose schedule changed.
func (r *reconciler) workloadsIn(ctx context.Context, ns client.Object) []reconcile.Request {
	list := r.newList()
	if err := r.client.List(ctx, list, client.InNamespace(ns.GetName())); err != nil {
		// e.g. a namespace outside watchNamespaces
		ctrl.LoggerFrom(ctx).Error(err, "Cannot list workloads", "namespace", ns.GetName())
		return nil
	}
	var reqs []reconcile.Request
	_ = meta.EachListItem(list, func(o runtime.Object) error {
		if obj, ok := o.(client.Object); ok {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
		}
		return nil
	})
	return reqs
}

// workloadKind is a kind curfew schedules.
type workloadKind struct {
	kind    string
	newObj  func() client.Object
	newList func() client.ObjectList
}

var builtinKinds = []workloadKind{
	{"Deployment", func() client.Object { return &appsv1.Deployment{} }, func() client.ObjectList { return &appsv1.DeploymentList{} }},
	{"StatefulSet", func() client.Object { return &appsv1.StatefulSet{} }, func() client.ObjectList { return &appsv1.StatefulSetList{} }},
	{"CronJob", func() client.Object { return &batchv1.CronJob{} }, func() client.ObjectList { return &batchv1.CronJobList{} }},
}

// extraKinds parses EXTRA_KINDS ("Rollout.v1alpha1.argoproj.io,..."): kinds with a /scale
// subresource, read as unstructured objects (Helm value extraKinds).
func extraKinds(list string) ([]workloadKind, error) {
	var kinds []workloadKind
	for s := range strings.SplitSeq(list, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		gvk, _ := schema.ParseKindArg(s)
		if gvk == nil || gvk.Group == "" {
			return nil, fmt.Errorf("EXTRA_KINDS: %q is not Kind.version.group", s)
		}
		kinds = append(kinds, workloadKind{
			kind: gvk.Kind,
			newObj: func() client.Object {
				u := &unstructured.Unstructured{}
				u.SetGroupVersionKind(*gvk)
				return u
			},
			newList: func() client.ObjectList {
				l := &unstructured.UnstructuredList{}
				l.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
				return l
			},
		})
	}
	return kinds, nil
}

type reconciler struct {
	client client.Client
	// reader is uncached, so the state ConfigMap needs no cluster-wide ConfigMap watch.
	reader    client.Reader
	namespace string
	workloadKind
	// kinds are all the kinds curfew schedules, for waves.
	kinds []workloadKind
	now   func() time.Time
	// waitingSince is when each workload started waiting for another wave; one reconcile runs at a time.
	// shortcut: in memory, a leader change restarts the wave timeout; persist it in curfew-state if that matters.
	waitingSince map[string]time.Time
	// scaledUp counts, per sleeping workload, how often it was found scaled up again (in memory).
	scaledUp map[string]int
	// dryRun sends workload writes with dryRun=All: checked by the API server, never applied.
	// curfew-state is still written, so wakes are decided and logged too.
	dryRun bool
}

func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	ctx, span := tracer.Start(ctx, "Reconcile", trace.WithAttributes(
		attribute.String("kind", r.kind),
		attribute.String("namespace", req.Namespace),
		attribute.String("name", req.Name),
	))
	defer func() { endSpan(span, err) }()

	log := ctrl.LoggerFrom(ctx)
	if r.dryRun {
		log = log.WithValues("dryRun", true)
	}
	key := fmt.Sprintf("%s.%s.%s", strings.ToLower(r.kind), req.Namespace, req.Name)

	state, err := r.loadState(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	stored, sleeping := state.Data[key]

	obj := r.newObj()
	if err := r.client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) && sleeping {
			observeFreed(r.kind, req.Namespace, req.Name, nil, "")
			delete(r.scaledUp, key)
			delete(state.Data, key)
			return ctrl.Result{}, r.saveState(ctx, state)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ns := &corev1.Namespace{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: req.Namespace}, ns); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		ns = nil
	}

	// No schedule (none set, or excluded): stay awake, which restores the replicas if it was sleeping.
	asleep, next, err := desired(obj, r.kind, ns, r.now())
	if err != nil {
		// The next annotation edit triggers a new reconcile.
		log.Error(err, "Invalid schedule")
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid schedule")
		return ctrl.Result{}, nil
	}

	span.SetAttributes(attribute.Bool("asleep", asleep), attribute.Bool("sleeping", sleeping))

	current, set, err := r.current(ctx, obj)
	if err != nil {
		return ctrl.Result{}, err
	}

	if asleep && current > 0 || !asleep && sleeping {
		blocker, err := r.waveBlocker(ctx, obj, ns, asleep, state.Data)
		if err != nil {
			return ctrl.Result{}, err
		}
		if r.waiting(log, key, blocker) {
			return ctrl.Result{RequeueAfter: wavePoll}, nil
		}
	} else {
		delete(r.waitingSince, key)
	}

	switch {
	case asleep && current > 0:
		if sleeping {
			r.scaledUpWhileAsleep(log, req.Namespace, key)
		}
		// Save before scaling: a failed scale is retried, a failed save changes nothing.
		state.Data[key] = strconv.Itoa(int(current))
		if err := r.saveState(ctx, state); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Scaling down", "replicas", current)
		if err := r.scaleTo(ctx, set, current, 0); err != nil {
			return ctrl.Result{}, err
		}
	case !asleep && sleeping:
		delete(r.scaledUp, key)
		replicas, err := strconv.ParseInt(stored, 10, 32)
		if err == nil && replicas < 0 {
			err = errors.New("negative replicas")
		}
		if err != nil {
			log.Error(err, "Invalid stored replicas, dropping it", "value", stored)
		} else {
			// Scale before forgetting: a failed delete just repeats the same scale.
			log.Info("Waking up", "replicas", replicas)
			if err := r.scaleTo(ctx, set, current, int32(replicas)); err != nil {
				return ctrl.Result{}, err
			}
		}
		delete(state.Data, key)
		if err := r.saveState(ctx, state); err != nil {
			return ctrl.Result{}, err
		}
	}

	observeFreed(r.kind, req.Namespace, req.Name, obj, state.Data[key])

	if next.IsZero() {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: next.Sub(r.now())}, nil
}

// loadState returns the state ConfigMap, or an unsaved empty one if it does not exist yet.
// shortcut: one ConfigMap holds every sleeping workload (1 MiB, about 15k entries), shard it past that.
func (r *reconciler) loadState(ctx context.Context) (_ *corev1.ConfigMap, err error) {
	ctx, span := tracer.Start(ctx, "LoadState")
	defer func() { endSpan(span, err) }()

	state := &corev1.ConfigMap{}
	err = r.reader.Get(ctx, types.NamespacedName{Namespace: r.namespace, Name: values.StateConfigMap}, state)
	if apierrors.IsNotFound(err) {
		state.ObjectMeta = metav1.ObjectMeta{Namespace: r.namespace, Name: values.StateConfigMap}
		err = nil
	}
	if state.Data == nil {
		state.Data = map[string]string{}
	}
	if err == nil {
		observeState(state.Data)
	}
	return state, err
}

// saveState writes the ConfigMap; a concurrent write fails on resourceVersion and the reconcile is retried.
func (r *reconciler) saveState(ctx context.Context, state *corev1.ConfigMap) (err error) {
	ctx, span := tracer.Start(ctx, "SaveState", trace.WithAttributes(attribute.Int("entries", len(state.Data))))
	defer func() { endSpan(span, err) }()

	if state.ResourceVersion == "" {
		err = r.client.Create(ctx, state)
	} else {
		err = r.client.Update(ctx, state)
	}
	if err == nil {
		observeState(state.Data)
	}
	return err
}

// current returns how many replicas obj runs, and a function that sets them. A CronJob counts as
// 1 while it runs and 0 while suspended; Deployments and StatefulSets go through /scale.
func (r *reconciler) current(ctx context.Context, obj client.Object) (int32, func(context.Context, int32) error, error) {
	writer := r.client
	if r.dryRun {
		writer = client.NewDryRunClient(r.client)
	}
	if cj, ok := obj.(*batchv1.CronJob); ok {
		running := int32(1)
		if ptr.Deref(cj.Spec.Suspend, false) {
			running = 0
		}
		return running, func(ctx context.Context, replicas int32) error {
			patch := client.MergeFrom(cj.DeepCopy())
			cj.Spec.Suspend = ptr.To(replicas == 0)
			return writer.Patch(ctx, cj, patch)
		}, nil
	}

	if u, ok := obj.(*unstructured.Unstructured); ok {
		scale, err := r.unstructuredScale(ctx, u)
		if err != nil {
			return 0, nil, err
		}
		return scale.Spec.Replicas, func(ctx context.Context, replicas int32) error {
			scale.Spec.Replicas = replicas
			body, err := runtime.DefaultUnstructuredConverter.ToUnstructured(scale)
			if err != nil {
				return err
			}
			return writer.SubResource("scale").Update(ctx, u, client.WithSubResourceBody(&unstructured.Unstructured{Object: body}))
		}, nil
	}

	scale := &autoscalingv1.Scale{}
	if err := r.client.SubResource("scale").Get(ctx, obj, scale); err != nil {
		return 0, nil, err
	}
	return scale.Spec.Replicas, func(ctx context.Context, replicas int32) error {
		scale.Spec.Replicas = replicas
		return writer.SubResource("scale").Update(ctx, obj, client.WithSubResourceBody(scale))
	}, nil
}

// unstructuredScale reads an extra kind's /scale. The client takes only an unstructured body for an
// unstructured object, so it is converted to autoscalingv1.Scale.
func (r *reconciler) unstructuredScale(ctx context.Context, u *unstructured.Unstructured) (*autoscalingv1.Scale, error) {
	body := &unstructured.Unstructured{}
	body.SetGroupVersionKind(autoscalingv1.SchemeGroupVersion.WithKind("Scale"))
	if err := r.client.SubResource("scale").Get(ctx, u, body); err != nil {
		return nil, err
	}
	scale := &autoscalingv1.Scale{}
	return scale, runtime.DefaultUnstructuredConverter.FromUnstructured(body.Object, scale)
}

func (r *reconciler) scaleTo(ctx context.Context, set func(context.Context, int32) error, from, to int32) (err error) {
	ctx, span := tracer.Start(ctx, "Scale", trace.WithAttributes(
		attribute.Int("from", int(from)),
		attribute.Int("to", int(to)),
	))
	defer func() {
		observeScale(r.kind, to, err)
		endSpan(span, err)
	}()

	return set(ctx, to)
}

// endSpan marks the span as failed when err is set, then ends it.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
