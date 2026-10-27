package curfew

import (
	"strconv"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Only the leader reconciles, so only the leader reports these.
var (
	sleepingWorkloads = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "curfew_sleeping_workloads",
		Help: "Workloads asleep, from the curfew-state ConfigMap.",
	}, []string{"namespace", "kind"})
	scaleTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "curfew_scale_total",
		Help: "Scale and suspend requests, by direction (sleep or wake) and result (ok or error).",
	}, []string{"kind", "direction", "result"})
	scaledUpTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "curfew_scaled_up_while_asleep_total",
		Help: "Sleeping workloads found scaled up again, by someone or another controller, and scaled back to 0.",
	}, []string{"namespace", "kind"})
	freedCPU = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "curfew_freed_cpu_cores",
		Help: "CPU requests freed by a sleeping workload: stored replicas times its pod's requests.",
	}, []string{"namespace", "kind", "name"})
	freedMemory = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "curfew_freed_memory_bytes",
		Help: "Memory requests freed by a sleeping workload: stored replicas times its pod's requests.",
	}, []string{"namespace", "kind", "name"})
)

func init() {
	metrics.Registry.MustRegister(sleepingWorkloads, scaleTotal, scaledUpTotal, freedCPU, freedMemory)
}

// observeStateMu serializes recounts: every controller recounts, and two interleaved ones would
// both add to the same reset gauge.
var observeStateMu sync.Mutex

// observeState counts the curfew-state entries (<kind>.<namespace>.<name>) per namespace and kind.
func observeState(data map[string]string) {
	counts := map[[2]string]float64{}
	for key := range data {
		if kind, rest, ok := strings.Cut(key, "."); ok {
			ns, _, _ := strings.Cut(rest, ".")
			counts[[2]string{ns, kind}]++
		}
	}

	observeStateMu.Lock()
	defer observeStateMu.Unlock()
	sleepingWorkloads.Reset()
	for labels, n := range counts {
		sleepingWorkloads.WithLabelValues(labels[0], labels[1]).Set(n)
	}
}

func observeScale(kind string, to int32, err error) {
	direction, result := "wake", "ok"
	if to == 0 {
		direction = "sleep"
	}
	if err != nil {
		result = "error"
	}
	scaleTotal.WithLabelValues(strings.ToLower(kind), direction, result).Inc()
}

// observeFreed sets what a sleeping workload frees, from its stored replicas; stored is "" once awake.
// obj is nil once deleted. CronJobs free nothing between runs, so they are left out.
func observeFreed(kind, namespace, name string, obj client.Object, stored string) {
	labels := prometheus.Labels{"namespace": namespace, "kind": strings.ToLower(kind), "name": name}
	var tmpl *corev1.PodTemplateSpec
	switch o := obj.(type) {
	case *appsv1.Deployment:
		tmpl = &o.Spec.Template
	case *appsv1.StatefulSet:
		tmpl = &o.Spec.Template
	}
	replicas, err := strconv.Atoi(stored)
	if tmpl == nil || err != nil {
		freedCPU.Delete(labels)
		freedMemory.Delete(labels)
		return
	}

	var cpu, memory resource.Quantity
	for _, c := range tmpl.Spec.Containers {
		cpu.Add(c.Resources.Requests[corev1.ResourceCPU])
		memory.Add(c.Resources.Requests[corev1.ResourceMemory])
	}
	freedCPU.With(labels).Set(cpu.AsApproximateFloat64() * float64(replicas))
	freedMemory.With(labels).Set(memory.AsApproximateFloat64() * float64(replicas))
}
