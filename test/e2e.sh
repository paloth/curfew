#!/usr/bin/env bash
# End-to-end test against a deployed curfew (see `just e2e`). About 5 minutes:
#   1. a Deployment and a StatefulSet sleep and wake on real cron times, a few minutes from now
#   2. a sleeping Deployment wakes up as soon as its annotations are removed
set -euo pipefail

CTX="${KUBE_CONTEXT:-colima}"
OPERATOR_NS="${OPERATOR_NS:-curfew}"
NS=curfew-e2e

k() { kubectl --context "$CTX" "$@"; }

# "minute hour" in UTC, $1 minutes from now (BSD date on macOS, GNU date elsewhere).
at() { date -u -v+"$1"M '+%M %H' 2>/dev/null || date -u -d "+$1 min" '+%M %H'; }

replicas() { k -n "$NS" get "$1" -o jsonpath='{.spec.replicas}'; }

# Stored replicas for a state key such as deployment.curfew-e2e.app, empty when absent.
stored() { k -n "$OPERATOR_NS" get configmap curfew-state -o jsonpath="{.data.${1//./\\.}}" 2>/dev/null || true; }

# wait_for <description> <timeout seconds> <command...>
wait_for() {
  local desc=$1 timeout=$2 start=$SECONDS
  shift 2
  printf '  waiting: %s ' "$desc"
  until "$@"; do
    if (( SECONDS - start > timeout )); then
      echo "TIMEOUT"
      k -n "$OPERATOR_NS" logs deploy/curfew --tail=30 || true
      exit 1
    fi
    printf '.'
    sleep 5
  done
  echo " ok ($((SECONDS - start))s)"
}

expect() {
  if [[ "$2" != "$3" ]]; then
    echo "  FAIL: $1 = '$2', want '$3'"
    exit 1
  fi
  echo "  ok: $1 = '$2'"
}

both_at() { [[ "$(replicas deployment/app)" == "$1" && "$(replicas statefulset/db)" == "$1" ]]; }
app_at() { [[ "$(replicas deployment/app)" == "$1" ]]; }

trap 'k delete namespace "$NS" --ignore-not-found --wait=false >/dev/null' EXIT

read -r sleep_m sleep_h < <(at 2)
read -r wake_m wake_h < <(at 4)
echo "Schedule (UTC): sleep at $sleep_h:$sleep_m, wake at $wake_h:$wake_m"

k create namespace "$NS" --dry-run=client -o yaml | k apply -f - >/dev/null
k apply -f - >/dev/null <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: $NS
  annotations:
    curfew/sleep: "$sleep_m $sleep_h * * *"
    curfew/wake: "$wake_m $wake_h * * *"
spec:
  replicas: 2
  selector:
    matchLabels: {app: app}
  template:
    metadata:
      labels: {app: app}
    spec:
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.10
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: db
  namespace: $NS
  annotations:
    curfew/sleep: "$sleep_m $sleep_h * * *"
    curfew/wake: "$wake_m $wake_h * * *"
spec:
  replicas: 2
  serviceName: db
  selector:
    matchLabels: {app: db}
  template:
    metadata:
      labels: {app: db}
    spec:
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.10
EOF

echo "1. Scheduled sleep and wake"
wait_for "both scaled to 0 at sleep time" 240 both_at 0
expect "stored deployment" "$(stored "deployment.$NS.app")" 2
expect "stored statefulset" "$(stored "statefulset.$NS.db")" 2
wait_for "both back to 2 at wake time" 240 both_at 2
expect "stored deployment after wake" "$(stored "deployment.$NS.app")" ""
expect "stored statefulset after wake" "$(stored "statefulset.$NS.db")" ""

echo "2. Removing the annotations wakes a sleeping workload"
# Wake on Jan 1 comes before sleep on Jan 2, so this is "asleep" right now (except on Jan 1).
k -n "$NS" annotate deployment/app --overwrite \
  curfew/sleep="0 0 2 1 *" curfew/wake="0 0 1 1 *" >/dev/null
wait_for "app scaled to 0" 60 app_at 0
k -n "$NS" annotate deployment/app curfew/sleep- curfew/wake- >/dev/null
wait_for "app back to 2" 60 app_at 2
expect "stored deployment after removal" "$(stored "deployment.$NS.app")" ""

echo "3. Namespace schedule, with StatefulSets excluded"
k -n "$NS" annotate statefulset/db curfew/sleep- curfew/wake- >/dev/null
k annotate namespace "$NS" --overwrite \
  curfew/sleep="0 0 2 1 *" curfew/wake="0 0 1 1 *" curfew/exclude-kinds=StatefulSet >/dev/null
wait_for "app follows the namespace and scales to 0" 60 app_at 0
expect "stored deployment" "$(stored "deployment.$NS.app")" 2
sleep 5
expect "db (excluded kind) replicas" "$(replicas statefulset/db)" 2
k annotate namespace "$NS" curfew/sleep- curfew/wake- curfew/exclude-kinds- >/dev/null
wait_for "app back to 2 once the namespace schedule is removed" 60 app_at 2

echo "PASS"
