package curfew

import (
	"context"
	"testing"

	"curfew/internal/logs"

	"github.com/prometheus/client_golang/prometheus/testutil"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileMetrics(t *testing.T) {
	logs.InitLogger(false)
	ctx := context.Background()

	requests := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("64Mi"),
	}
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "shop", Annotations: schedule},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(3)),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "app", Resources: corev1.ResourceRequirements{Requests: requests}},
				{Name: "sidecar", Resources: corev1.ResourceRequirements{Requests: requests}},
			}}},
		},
	}
	c := fake.NewClientBuilder().WithObjects(deploy).Build()
	now := paris(t, "2026-10-19 22:00")
	r := newTestReconciler(c, &now)
	sleeps := testutil.ToFloat64(scaleTotal.WithLabelValues("deployment", "sleep", "ok"))

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(sleepingWorkloads.WithLabelValues("shop", "deployment")); got != 1 {
		t.Errorf("sleeping = %v, want 1", got)
	}
	if got := testutil.ToFloat64(scaleTotal.WithLabelValues("deployment", "sleep", "ok")) - sleeps; got != 1 {
		t.Errorf("sleep scales = %v, want 1", got)
	}
	// 3 replicas of 2 containers at 100m and 64Mi each.
	if got := testutil.ToFloat64(freedCPU.WithLabelValues("shop", "deployment", "app")); got < 0.5999 || got > 0.6001 {
		t.Errorf("freed cpu = %v, want 0.6", got)
	}
	if got := testutil.ToFloat64(freedMemory.WithLabelValues("shop", "deployment", "app")); got != 6*64*1024*1024 {
		t.Errorf("freed memory = %v, want %d", got, 6*64*1024*1024)
	}

	now = paris(t, "2026-10-20 08:00")
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if n := testutil.CollectAndCount(sleepingWorkloads); n != 0 {
		t.Errorf("sleeping series = %d after wake, want 0", n)
	}
	if n := testutil.CollectAndCount(freedCPU) + testutil.CollectAndCount(freedMemory); n != 0 {
		t.Errorf("freed series = %d after wake, want 0", n)
	}
}

func TestReconcileCountsScaleUpsWhileAsleep(t *testing.T) {
	logs.InitLogger(false)
	ctx := context.Background()

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "shop", Annotations: schedule},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(3))},
	}
	c := fake.NewClientBuilder().WithObjects(deploy).Build()
	now := paris(t, "2026-10-19 22:00")
	r := newTestReconciler(c, &now)
	counted := func() float64 { return testutil.ToFloat64(scaledUpTotal.WithLabelValues("shop", "deployment")) }
	before := counted()

	reconcile := func() {
		t.Helper()
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	reconcile() // goes to sleep: not a fight
	for range fightWarning {
		// Something like Argo CD self-heal puts the replicas back.
		if err := c.Get(ctx, req.NamespacedName, deploy); err != nil {
			t.Fatal(err)
		}
		deploy.Spec.Replicas = ptr.To(int32(3))
		if err := c.Update(ctx, deploy); err != nil {
			t.Fatal(err)
		}
		reconcile()
	}
	if got := counted() - before; got != fightWarning {
		t.Errorf("counted %v scale-ups, want %d", got, fightWarning)
	}
	if got := r.scaledUp["deployment.shop.app"]; got != fightWarning {
		t.Errorf("scaledUp = %d, want %d", got, fightWarning)
	}

	now = paris(t, "2026-10-20 08:00")
	reconcile()
	if len(r.scaledUp) != 0 {
		t.Errorf("scaledUp = %v after wake, want empty", r.scaledUp)
	}
}
