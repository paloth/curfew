package curfew

import (
	"strings"

	"github.com/go-logr/logr"
)

// fightWarning is how many scale-ups in one sleep window it takes to blame another controller.
const fightWarning = 3

// scaledUpWhileAsleep counts a sleeping workload found scaled up again, and warns once it happens
// often enough to be another controller putting the replicas back, rather than a person.
func (r *reconciler) scaledUpWhileAsleep(log logr.Logger, namespace, key string) {
	scaledUpTotal.WithLabelValues(namespace, strings.ToLower(r.kind)).Inc()
	r.scaledUp[key]++
	if r.scaledUp[key] == fightWarning {
		log.Info("Scaled up again while asleep, several times: another controller is likely fighting curfew "+
			"(Argo CD self-heal, KEDA, a CI job). See \"Other controllers\" in the README.", "times", fightWarning)
	}
}
