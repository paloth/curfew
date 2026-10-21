package curfew

import (
	"maps"
	"testing"

	"curfew/internal/values"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestScheduleFor(t *testing.T) {
	own := map[string]string{values.SleepAnnotation: "own"}
	nsSchedule := map[string]string{
		values.SleepAnnotation:         "ns",
		values.ExcludeKindsAnnotation:  "statefulset",
		values.ExcludeLabelsAnnotation: "tier=data, keep-alive",
	}
	ns := func(name string, annotations map[string]string) *corev1.Namespace {
		return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations}}
	}
	deploy := func(namespace string, labels, annotations map[string]string) *appsv1.Deployment {
		return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: namespace, Labels: labels, Annotations: annotations}}
	}

	tests := []struct {
		name string
		obj  metav1.Object
		kind string
		ns   *corev1.Namespace
		want map[string]string
	}{
		{"own rule", deploy("shop", nil, own), "Deployment", ns("shop", nil), own},
		{"own rule wins over the namespace", deploy("shop", nil, own), "Deployment", ns("shop", nsSchedule), own},
		{"own rule ignores namespace exclusions", deploy("shop", map[string]string{"keep-alive": ""}, own), "Deployment", ns("shop", nsSchedule), own},
		{"namespace rule", deploy("shop", nil, nil), "Deployment", ns("shop", nsSchedule), nsSchedule},
		{"no rule anywhere", deploy("shop", nil, nil), "Deployment", ns("shop", nil), nil},
		{"namespace not found", deploy("shop", nil, nil), "Deployment", nil, nil},
		{"excluded kind, any case", deploy("shop", nil, nil), "StatefulSet", ns("shop", nsSchedule), nil},
		{"excluded label value", deploy("shop", map[string]string{"tier": "data"}, nil), "Deployment", ns("shop", nsSchedule), nil},
		{"other label value", deploy("shop", map[string]string{"tier": "web"}, nil), "Deployment", ns("shop", nsSchedule), nsSchedule},
		{"excluded label key, any value", deploy("shop", map[string]string{"keep-alive": "no"}, nil), "Deployment", ns("shop", nsSchedule), nil},
		{"kube- namespace, own rule", deploy("kube-system", nil, own), "Deployment", ns("kube-system", nil), nil},
		{"kube- namespace, namespace rule", deploy("kube-system", nil, nil), "Deployment", ns("kube-system", nsSchedule), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scheduleFor(tt.obj, tt.kind, tt.ns); !maps.Equal(got, tt.want) || (got == nil) != (tt.want == nil) {
				t.Errorf("scheduleFor() = %v, want %v", got, tt.want)
			}
		})
	}
}
