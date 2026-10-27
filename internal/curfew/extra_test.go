package curfew

import (
	"context"
	"maps"
	"testing"
	"time"

	"curfew/internal/logs"
	"curfew/internal/values"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestExtraKinds(t *testing.T) {
	kinds, err := extraKinds("Rollout.v1alpha1.argoproj.io, Widget.v1.example.com,")
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 2 || kinds[0].kind != "Rollout" || kinds[1].kind != "Widget" {
		t.Fatalf("kinds = %+v", kinds)
	}
	if gvk := kinds[0].newObj().GetObjectKind().GroupVersionKind(); gvk.Group != "argoproj.io" || gvk.Version != "v1alpha1" {
		t.Errorf("Rollout GVK = %v", gvk)
	}
	if gvk := kinds[1].newList().GetObjectKind().GroupVersionKind(); gvk.Kind != "WidgetList" {
		t.Errorf("Widget list GVK = %v", gvk)
	}
	for _, bad := range []string{"Rollout", "Rollout.v1"} {
		if _, err := extraKinds(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

// fakeScale stands in for the API server's /scale on a custom resource, which the fake client lacks.
var fakeScale = interceptor.Funcs{
	SubResourceGet: func(ctx context.Context, c client.Client, sub string, obj, body client.Object, opts ...client.SubResourceGetOption) error {
		if _, ok := obj.(*unstructured.Unstructured); !ok {
			return c.SubResource(sub).Get(ctx, obj, body, opts...)
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		replicas, _, _ := unstructured.NestedInt64(obj.(*unstructured.Unstructured).Object, "spec", "replicas")
		scale := body.(*unstructured.Unstructured)
		scale.Object["spec"] = map[string]any{"replicas": replicas}
		scale.Object["status"] = map[string]any{"replicas": replicas, "selector": "app=widget"}
		return nil
	},
	SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
		if _, ok := obj.(*unstructured.Unstructured); !ok {
			return c.SubResource(sub).Update(ctx, obj, opts...)
		}
		o := &client.SubResourceUpdateOptions{}
		o.ApplyOptions(opts)
		replicas, _, _ := unstructured.NestedInt64(o.SubResourceBody.(*unstructured.Unstructured).Object, "spec", "replicas")
		u := obj.(*unstructured.Unstructured)
		if err := c.Get(ctx, client.ObjectKeyFromObject(u), u); err != nil {
			return err
		}
		_ = unstructured.SetNestedField(u.Object, replicas, "spec", "replicas")
		return c.Update(ctx, u)
	},
}

func TestReconcileExtraKind(t *testing.T) {
	logs.InitLogger(false)
	ctx := context.Background()

	extra, err := extraKinds("Widget.v1.example.com")
	if err != nil {
		t.Fatal(err)
	}
	widgetKind := extra[0]
	widget := widgetKind.newObj().(*unstructured.Unstructured)
	widget.SetNamespace("shop")
	widget.SetName("w")
	annotations := maps.Clone(schedule)
	annotations[values.WaveAnnotation] = "-1"
	widget.SetAnnotations(annotations)
	_ = unstructured.SetNestedField(widget.Object, int64(2), "spec", "replicas")

	app := &appsv1.Deployment{}
	app.Name, app.Namespace, app.Annotations = "app", "shop", schedule
	app.Spec.Replicas = ptr.To(int32(1))
	pod := &corev1.Pod{}
	pod.Name, pod.Namespace, pod.Labels = "w-0", "shop", map[string]string{"app": "widget"}

	c := fake.NewClientBuilder().WithObjects(widget, app, pod).WithInterceptorFuncs(fakeScale).Build()
	now := paris(t, "2026-10-19 22:00")
	kinds := append([]workloadKind{widgetKind}, builtinKinds...)
	rWidget := newTestReconciler(c, &now)
	rWidget.workloadKind, rWidget.kinds = widgetKind, kinds
	rApp := newTestReconciler(c, &now)
	rApp.kinds = kinds

	replicas := func() int64 {
		t.Helper()
		got := widgetKind.newObj().(*unstructured.Unstructured)
		if err := c.Get(ctx, client.ObjectKeyFromObject(widget), got); err != nil {
			t.Fatal(err)
		}
		n, _, _ := unstructured.NestedInt64(got.Object, "spec", "replicas")
		return n
	}
	reconcile := func(r *reconciler, name string) time.Duration {
		t.Helper()
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "shop", Name: name}})
		if err != nil {
			t.Fatal(err)
		}
		return res.RequeueAfter
	}

	if reconcile(rApp, "app") != wavePoll {
		t.Error("app did not wait for the widget (wave -1)")
	}
	reconcile(rWidget, "w")
	if n := replicas(); n != 0 {
		t.Fatalf("widget replicas = %d, want 0", n)
	}
	if v, _ := storedEntryFor(t, c, "widget.shop.w"); v != "2" {
		t.Errorf("stored = %q, want 2", v)
	}
	if reconcile(rApp, "app") != wavePoll {
		t.Error("app did not wait for the widget's pod, found through /scale's selector")
	}
	if err := c.Delete(ctx, pod); err != nil {
		t.Fatal(err)
	}
	if reconcile(rApp, "app") == wavePoll {
		t.Error("app still waiting once the widget is stopped")
	}

	now = paris(t, "2026-10-20 08:00")
	if reconcile(rWidget, "w") != wavePoll {
		t.Error("widget did not wait for app (wave 0) to wake first")
	}
	reconcile(rApp, "app")
	if err := c.Get(ctx, client.ObjectKeyFromObject(app), app); err != nil {
		t.Fatal(err)
	}
	app.Status.ReadyReplicas = 1
	if err := c.Status().Update(ctx, app); err != nil {
		t.Fatal(err)
	}
	reconcile(rWidget, "w")
	if n := replicas(); n != 2 {
		t.Errorf("widget replicas after wake = %d, want 2", n)
	}
}

func storedEntryFor(t *testing.T, c client.Client, key string) (string, bool) {
	t.Helper()
	state := &corev1.ConfigMap{}
	if err := c.Get(context.Background(), stateKey, state); err != nil {
		t.Fatal(err)
	}
	v, ok := state.Data[key]
	return v, ok
}
