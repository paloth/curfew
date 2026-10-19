package helpers

import (
	"strings"

	"curfew/internal/logs"
	"curfew/internal/values"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Skip a kubernetes object if it does not match criteria like excluded namespaces or curfew annotations.
// It runs on every Deployment and StatefulSet event in the cluster, so it only logs the excluded-namespace case.
func SkipObject(meta metav1.Object) bool {
	if _, ok := meta.GetAnnotations()[values.SleepAnnotation]; !ok {
		return true
	}

	if strings.HasPrefix(meta.GetNamespace(), values.NsExcludePrefix) {
		logs.Logger.Debug("Annotated object is in an excluded namespace", "namespace", meta.GetNamespace(), "name", meta.GetName())
		return true
	}

	return false
}
