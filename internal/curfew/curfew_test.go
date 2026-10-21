package curfew

import (
	"context"
	"maps"
	"testing"
	"time"

	"curfew/internal/logs"
	"curfew/internal/values"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

var schedule = map[string]string{
	values.SleepAnnotation:    "0 20 * * 1-5",
	values.WakeAnnotation:     "0 8 * * 1-5",
	values.TimezoneAnnotation: "Europe/Paris",
}

func paris(t *testing.T, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name       string
		now        string
		wantAsleep bool
		wantNext   string
	}{
		// 2026-10-19 is a Monday.
		{"working hours", "2026-10-19 12:00", false, "2026-10-19 20:00"},
		{"weeknight", "2026-10-19 22:00", true, "2026-10-20 08:00"},
		{"exactly at sleep", "2026-10-19 20:00", true, "2026-10-20 08:00"},
		{"friday night sleeps through the weekend", "2026-10-23 21:00", true, "2026-10-26 08:00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asleep, next, err := evaluate(schedule, paris(t, tt.now))
			if err != nil {
				t.Fatal(err)
			}
			if asleep != tt.wantAsleep || !next.Equal(paris(t, tt.wantNext)) {
				t.Errorf("evaluate() = %v, %v; want %v, %s", asleep, next, tt.wantAsleep, tt.wantNext)
			}
		})
	}

	for name, annotations := range map[string]map[string]string{
		"missing wake": {values.SleepAnnotation: "0 20 * * *"},
		"bad cron":     {values.SleepAnnotation: "nope", values.WakeAnnotation: "0 8 * * *"},
		"bad timezone": {values.SleepAnnotation: "0 20 * * *", values.WakeAnnotation: "0 8 * * *", values.TimezoneAnnotation: "Mars/Olympus"},
	} {
		if _, _, err := evaluate(annotations, time.Now()); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestReconcileSleepAndWake(t *testing.T) {
	logs.InitLogger(false)
	ctx := context.Background()

	spans := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)))

	replicas := int32(3)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "shop", Annotations: schedule},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	c := fake.NewClientBuilder().WithObjects(deploy).Build()

	now := paris(t, "2026-10-19 22:00")
	r := newTestReconciler(c, &now)

	check := func(wantReplicas int32, wantStored string) {
		t.Helper()
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		got := &appsv1.Deployment{}
		if err := c.Get(ctx, req.NamespacedName, got); err != nil {
			t.Fatal(err)
		}
		if *got.Spec.Replicas != wantReplicas {
			t.Errorf("replicas = %d, want %d", *got.Spec.Replicas, wantReplicas)
		}
		state := &corev1.ConfigMap{}
		if err := c.Get(ctx, stateKey, state); err != nil {
			t.Fatal(err)
		}
		if state.Data["deployment.shop.app"] != wantStored {
			t.Errorf("stored = %q, want %q", state.Data["deployment.shop.app"], wantStored)
		}
	}

	check(0, "3")
	check(0, "3") // a second reconcile at night must not overwrite the stored count with 0

	now = paris(t, "2026-10-20 08:00")
	check(3, "")

	scaled := 0
	for _, s := range spans.Ended() {
		if s.Name() == "Scale" {
			scaled++
		}
	}
	if scaled != 2 {
		t.Errorf("Scale spans = %d, want 2 (sleep and wake)", scaled)
	}
}

var (
	req      = ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "shop", Name: "app"}}
	stateKey = types.NamespacedName{Namespace: "curfew", Name: values.StateConfigMap}
)

func newTestReconciler(c client.Client, now *time.Time) *reconciler {
	return &reconciler{
		client:    c,
		reader:    c,
		namespace: "curfew",
		kind:      "Deployment",
		newObj:    func() client.Object { return &appsv1.Deployment{} },
		newList:   func() client.ObjectList { return &appsv1.DeploymentList{} },
		now:       func() time.Time { return *now },
	}
}

// State for shop/app sleeping, with 3 replicas to restore.
func sleepingState() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: stateKey.Namespace, Name: stateKey.Name},
		Data:       map[string]string{"deployment.shop.app": "3"},
	}
}

func storedEntry(t *testing.T, c client.Client) (string, bool) {
	t.Helper()
	state := &corev1.ConfigMap{}
	if err := c.Get(context.Background(), stateKey, state); err != nil {
		t.Fatal(err)
	}
	v, ok := state.Data["deployment.shop.app"]
	return v, ok
}

func TestReconcileWakesWhenAnnotationsRemoved(t *testing.T) {
	logs.InitLogger(false)
	ctx := context.Background()

	// Night time, but the annotations are gone: the workload must not stay at 0.
	zero := int32(0)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "shop"},
		Spec:       appsv1.DeploymentSpec{Replicas: &zero},
	}
	c := fake.NewClientBuilder().WithObjects(deploy, sleepingState()).Build()
	now := paris(t, "2026-10-19 22:00")

	res, err := newTestReconciler(c, &now).Reconcile(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want none for an unscheduled workload", res.RequeueAfter)
	}

	got := &appsv1.Deployment{}
	if err := c.Get(ctx, req.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if *got.Spec.Replicas != 3 {
		t.Errorf("replicas = %d, want 3", *got.Spec.Replicas)
	}
	if v, ok := storedEntry(t, c); ok {
		t.Errorf("entry still stored: %q", v)
	}
}

func TestReconcileForgetsDeletedWorkload(t *testing.T) {
	logs.InitLogger(false)

	c := fake.NewClientBuilder().WithObjects(sleepingState()).Build()
	now := paris(t, "2026-10-19 22:00")

	if _, err := newTestReconciler(c, &now).Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if v, ok := storedEntry(t, c); ok {
		t.Errorf("entry still stored: %q", v)
	}
}

func TestWatchedPassesAnnotationRemoval(t *testing.T) {
	logs.InitLogger(false)

	annotated := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "shop", Annotations: schedule}}
	plain := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "shop"}}
	inScheduledNs := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "office"}}
	c := fake.NewClientBuilder().WithObjects(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "office", Annotations: schedule}},
	).Build()

	tests := []struct {
		name     string
		old, new client.Object
		want     bool
	}{
		{"annotations removed", annotated, plain, true},
		{"annotations added", plain, annotated, true},
		{"never annotated", plain, plain, false},
		{"namespace has a schedule", inScheduledNs, inScheduledNs, true},
	}
	for _, tt := range tests {
		if got := watched(c).Update(event.UpdateEvent{ObjectOld: tt.old, ObjectNew: tt.new}); got != tt.want {
			t.Errorf("%s: watched.Update = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestReconcileDropsInvalidStoredReplicas(t *testing.T) {
	logs.InitLogger(false)
	ctx := context.Background()

	for _, bad := range []string{"-5", "lots", "99999999999"} {
		one := int32(1)
		deploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "shop"},
			Spec:       appsv1.DeploymentSpec{Replicas: &one},
		}
		state := sleepingState()
		state.Data["deployment.shop.app"] = bad
		c := fake.NewClientBuilder().WithObjects(deploy, state).Build()
		now := paris(t, "2026-10-19 12:00")

		if _, err := newTestReconciler(c, &now).Reconcile(ctx, req); err != nil {
			t.Fatalf("%q: %v", bad, err)
		}
		got := &appsv1.Deployment{}
		if err := c.Get(ctx, req.NamespacedName, got); err != nil {
			t.Fatal(err)
		}
		if *got.Spec.Replicas != 1 {
			t.Errorf("%q: replicas = %d, want 1 (untouched)", bad, *got.Spec.Replicas)
		}
		if v, ok := storedEntry(t, c); ok {
			t.Errorf("%q: entry still stored: %q", bad, v)
		}
	}
}

func TestWatchNamespaces(t *testing.T) {
	if got := watchNamespaces(""); got != nil {
		t.Errorf("empty list = %v, want nil (whole cluster)", got)
	}
	if got := watchNamespaces(" , "); got != nil {
		t.Errorf("blank entries = %v, want nil (whole cluster)", got)
	}
	got := watchNamespaces("dev, staging,,")
	if _, ok := got["dev"]; !ok || len(got) != 2 {
		t.Errorf("watchNamespaces = %v, want dev and staging", got)
	}
	if _, ok := got["staging"]; !ok {
		t.Errorf("watchNamespaces = %v, want staging", got)
	}
}

func TestReconcileFollowsNamespaceSchedule(t *testing.T) {
	logs.InitLogger(false)
	ctx := context.Background()

	nsAnnotations := map[string]string{values.ExcludeLabelsAnnotation: "keep-alive"}
	maps.Copy(nsAnnotations, schedule)
	three := int32(3)
	deploy := func(name string, labels, annotations map[string]string) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop", Labels: labels, Annotations: annotations},
			Spec:       appsv1.DeploymentSpec{Replicas: &three},
		}
	}
	// 22:00 is inside the namespace's sleep window; the own rule here is awake all evening.
	ownRule := map[string]string{values.SleepAnnotation: "0 23 * * *", values.WakeAnnotation: "0 21 * * *", values.TimezoneAnnotation: "Europe/Paris"}
	c := fake.NewClientBuilder().WithObjects(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop", Annotations: nsAnnotations}},
		deploy("app", nil, nil),
		deploy("db", map[string]string{"keep-alive": "yes"}, nil),
		deploy("own", nil, ownRule),
	).Build()
	now := paris(t, "2026-10-19 22:00")
	r := newTestReconciler(c, &now)

	want := map[string]int32{"app": 0, "db": 3, "own": 3}
	for name, replicas := range want {
		key := types.NamespacedName{Namespace: "shop", Name: name}
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		got := &appsv1.Deployment{}
		if err := c.Get(ctx, key, got); err != nil {
			t.Fatal(err)
		}
		if *got.Spec.Replicas != replicas {
			t.Errorf("%s: replicas = %d, want %d", name, *got.Spec.Replicas, replicas)
		}
	}

	// Removing the namespace schedule enqueues its workloads, and app wakes up.
	reqs := r.workloadsIn(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop"}})
	if len(reqs) != 3 {
		t.Fatalf("workloadsIn = %d requests, want 3", len(reqs))
	}
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: "shop"}, ns); err != nil {
		t.Fatal(err)
	}
	ns.Annotations = nil
	if err := c.Update(ctx, ns); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "shop", Name: "app"}}); err != nil {
		t.Fatal(err)
	}
	got := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "shop", Name: "app"}, got); err != nil {
		t.Fatal(err)
	}
	if *got.Spec.Replicas != 3 {
		t.Errorf("app after namespace schedule removed: replicas = %d, want 3", *got.Spec.Replicas)
	}
}

func TestNamespaceScheduleChanged(t *testing.T) {
	plain := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop"}}
	scheduledNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop", Annotations: schedule}}
	relabelled := scheduledNs.DeepCopy()
	relabelled.Labels = map[string]string{"team": "a"}

	tests := []struct {
		name     string
		old, new client.Object
		want     bool
	}{
		{"schedule added", plain, scheduledNs, true},
		{"schedule removed", scheduledNs, plain, true},
		{"only labels changed", scheduledNs, relabelled, false},
		{"never scheduled", plain, plain, false},
	}
	for _, tt := range tests {
		if got := namespaceScheduleChanged.Update(event.UpdateEvent{ObjectOld: tt.old, ObjectNew: tt.new}); got != tt.want {
			t.Errorf("%s: = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestReconcileSuspendsCronJob(t *testing.T) {
	logs.InitLogger(false)
	ctx := context.Background()

	cronJob := func(name string, suspend bool) *batchv1.CronJob {
		return &batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop", Annotations: schedule},
			Spec:       batchv1.CronJobSpec{Schedule: "*/5 * * * *", Suspend: ptr.To(suspend)},
		}
	}
	c := fake.NewClientBuilder().WithObjects(cronJob("report", false), cronJob("paused", true)).Build()
	now := paris(t, "2026-10-19 22:00")
	r := newTestReconciler(c, &now)
	r.kind, r.newObj = "CronJob", func() client.Object { return &batchv1.CronJob{} }

	check := func(name string, wantSuspend bool, wantStored string) {
		t.Helper()
		key := types.NamespacedName{Namespace: "shop", Name: name}
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		got := &batchv1.CronJob{}
		if err := c.Get(ctx, key, got); err != nil {
			t.Fatal(err)
		}
		if ptr.Deref(got.Spec.Suspend, false) != wantSuspend {
			t.Errorf("%s: suspend = %v, want %v", name, ptr.Deref(got.Spec.Suspend, false), wantSuspend)
		}
		state := &corev1.ConfigMap{}
		_ = c.Get(ctx, stateKey, state)
		if v := state.Data["cronjob.shop."+name]; v != wantStored {
			t.Errorf("%s: stored = %q, want %q", name, v, wantStored)
		}
	}

	check("report", true, "1")
	check("paused", true, "") // already suspended by its owner: nothing to restore later

	now = paris(t, "2026-10-20 08:00")
	check("report", false, "")
	check("paused", true, "")
}
