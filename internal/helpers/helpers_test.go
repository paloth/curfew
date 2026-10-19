package helpers

import (
	"testing"

	"curfew/internal/logs"
	"curfew/internal/values"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func deploy(name, ns string, annotated bool) *appsv1.Deployment {
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
	if annotated {
		d.Annotations = map[string]string{values.SleepAnnotation: "0 20 * * 1-5"}
	}
	return d
}

func TestSkipObject(t *testing.T) {
	logs.InitLogger(false)

	tests := []struct {
		name string
		obj  metav1.Object
		want bool
	}{
		{"annotated", deploy("app", "shop", true), false},
		{"annotated named curfew", deploy("curfew", "shop", true), false},
		{"no annotation", deploy("app", "shop", false), true},
		{"excluded namespace", deploy("app", "kube-system", true), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SkipObject(tt.obj); got != tt.want {
				t.Errorf("SkipObject() = %v, want %v", got, tt.want)
			}
		})
	}
}
