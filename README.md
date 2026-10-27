# curfew

A Kubernetes operator that scales Deployments, StatefulSets and any other kind with a `/scale` subresource to zero, and suspends CronJobs, on a cron schedule, then brings them back. Use it to switch off non-production workloads at night and on weekends.

You opt a workload in with two annotations: when to sleep, and when to wake. curfew remembers the replica count before scaling down and restores it on wake-up.

## Install

curfew runs inside the cluster only. Install it in its own namespace, labeled with the `restricted` Pod Security level, using the Helm chart in `helm/`:

```bash
kubectl create namespace curfew
kubectl label namespace curfew pod-security.kubernetes.io/enforce=restricted
helm upgrade --install curfew helm/ --namespace curfew
```

`just helm-deploy` does all three steps.

Keep write access to this namespace for admins only (see [Permissions](#permissions)).

Main values (`helm/values.yaml`):

| Value | Default | What it does |
|---|---|---|
| `image.repository` / `image.tag` | `ghcr.io/paloth/curfew` / chart `appVersion` | Operator image. Use an immutable version tag. |
| `replicaCount` | `1` | Operator pods. Leader election is always on, so more than one is safe: only the leader acts. |
| `dev` | `false` | Debug logs |
| `dryRun` | `false` | Decide and log without changing any workload (see [Dry run](#dry-run)) |
| `extraKinds` | `[]` | More kinds to schedule, such as Argo Rollouts (see [Other kinds](#other-kinds)) |
| `watchNamespaces` | `[]` | Namespaces curfew may act in. Empty means the whole cluster. |
| `networkPolicy.enabled` | `true` | Restricts the operator pod's network traffic |
| `networkPolicy.apiServerPorts` | `[443, 6443]` | Ports the API server listens on |
| `metrics.enabled` | `false` | Prometheus metrics on port 8080, with a Service (see [Metrics](#metrics)) |
| `metrics.serviceMonitor` | `false` | Also create a ServiceMonitor (needs the Prometheus Operator) |
| `tracing.endpoint` | `""` | OTLP HTTP `host:port` for traces. Empty disables tracing. |
| `tracing.insecure` | `false` | Send traces over plain HTTP instead of TLS |
| `resources` | 10m CPU, 64Mi request, 256Mi limit | The operator caches every Deployment, StatefulSet and CronJob it watches. Raise memory on big clusters. |

## Usage

Add the annotations to a Deployment, StatefulSet or CronJob:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  annotations:
    curfew/sleep: "0 20 * * 1-5"     # scale to 0 at 20:00, Monday to Friday
    curfew/wake: "0 8 * * 1-5"       # restore at 08:00, Monday to Friday
    curfew/timezone: "Europe/Paris"  # optional, default UTC
```

| Annotation | Required | Format |
|---|---|---|
| `curfew/sleep` | yes | Standard 5-field cron (`minute hour day-of-month month day-of-week`) or a descriptor like `@daily` |
| `curfew/wake` | yes | Same as `sleep` |
| `curfew/timezone` | no | IANA time zone name, e.g. `America/New_York`. Default `UTC`. |
| `curfew/skip-dates` | no | Comma-separated dates (`YYYY-MM-DD`), e.g. `2026-12-24,2026-12-25`. No sleep or wake on those days (see [Holidays](#holidays)). |
| `curfew/awake-until` | no | RFC 3339 time, e.g. `2026-10-28T08:00:00+01:00`. Stay awake until then (see [Keep awake](#keep-awake)). |

Both schedules are read in the given time zone.

The example above sleeps from Friday 20:00 until Monday 08:00. The next wake time is Monday, so the workload stays down all weekend.

### How it decides

At any moment curfew looks at which comes next, the next sleep or the next wake:

- **Wake comes first:** the workload should be asleep now.
- **Sleep comes first:** the workload should be awake now.

It then schedules its next check for exactly that time. Adding the annotations during a sleep window scales the workload down right away.

### Namespace schedule

Put the same annotations on a namespace to apply them to every Deployment, StatefulSet and CronJob in it:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: staging
  annotations:
    curfew/sleep: "0 20 * * 1-5"
    curfew/wake: "0 8 * * 1-5"
    curfew/timezone: "Europe/Paris"
    curfew/exclude-kinds: "StatefulSet"          # optional
    curfew/exclude-labels: "tier=data,keep-alive" # optional
```

A workload in that namespace follows the first rule that applies:

1. **Its own rule:** a workload with its own `curfew/sleep` uses its own `curfew/sleep`, `curfew/wake` and `curfew/timezone`, and ignores the namespace.
2. **Excluded:** the namespace's rule is skipped for a workload whose kind is listed in `curfew/exclude-kinds` (`Deployment`, `StatefulSet`, `CronJob`), or whose labels match an entry of `curfew/exclude-labels`. An entry is `key=value` (that exact value) or `key` (any value); one matching entry is enough.
3. **The namespace's rule.**

Changing or removing the namespace annotations applies at once. Workloads that are no longer covered, because the schedule was removed or they became excluded, are woken up.

### Holidays

Cron can't say "not on public holidays". List those days instead:

```yaml
    curfew/skip-dates: "2026-12-24,2026-12-25,2027-01-01"
```

- No sleep or wake happens on a listed day, so the workload keeps the state it was in when the day started. With the example schedule (sleep at 20:00, wake at 08:00 on weekdays), a workload stays asleep from Wednesday 23 December at 20:00 until Monday 28 December at 08:00, and again over New Year's Day.
- Dates are read in the schedule's `curfew/timezone`.
- It belongs to the schedule: on a namespace, it applies to the workloads that follow the namespace's schedule. A workload with its own `curfew/sleep` needs its own `curfew/skip-dates`.
- Exact dates only: no ranges and no yearly repeats, so update the list each year. An invalid date is logged and the workload is left as it is.

### Keep awake

Working late? Keep a workload, or a whole namespace, awake without touching its schedule:

```bash
kubectl annotate deployment my-app curfew/awake-until=2026-10-28T08:00:00+01:00
kubectl annotate namespace staging curfew/awake-until=2026-10-28T08:00:00+01:00
```

- Until that time the workload counts as awake: a sleeping one wakes right away, and nothing goes to sleep. After it, the schedule applies again.
- A namespace's `curfew/awake-until` covers all its scheduled workloads, those with their own schedule included. A workload's own `curfew/awake-until` wins over the namespace's.
- The time needs a zone (`Z` or an offset). An invalid value is logged and the workload is left as it is, as with other invalid annotations.
- An expired annotation does nothing. curfew does not remove it.

### Waves

Some workloads must stop in order, such as an app that needs to shut down cleanly before its database stops. Give them a wave, like Argo CD sync waves:

```yaml
metadata:
  annotations:
    curfew/wave: "1"   # any integer, default 0
```

Waves only order workloads of the same namespace:

- **Sleep** goes up the waves: `-1`, then `0` (workloads without the annotation), then `1`, and so on. A workload waits until every lower-wave workload that is also going to sleep is fully stopped: no pods left, terminating ones included, and for a CronJob no running Jobs.
- **Wake** goes down the waves. A workload waits until every higher-wave workload that is also waking up is back and ready.
- A waiting workload is checked again every 10 seconds. After 10 minutes it stops waiting, logs `Wave timeout, proceeding`, and goes ahead, so one stuck pod can't keep the rest up all night. If the operator restarts or changes leader, the 10 minutes start over.

Wave `1` on the database is enough for the example: the app (wave 0) stops first, and the database comes back first.

### Other kinds

Any kind with a `/scale` subresource can be scheduled too, such as Argo Rollouts:

```yaml
# values.yaml
extraKinds:
  - group: argoproj.io
    version: v1alpha1
    kind: Rollout
    resource: rollouts   # the plural name, used for permissions
```

- curfew then reads the same annotations on them, and namespace schedules, `curfew/exclude-kinds` (`Rollout`) and waves cover them too.
- The chart grants `get`, `list`, `watch` on the kind, and `get`, `update`, `patch` on its `/scale` only.
- In waves, curfew waits for the pods matching the selector that `/scale` reports. Readiness on wake is `status.readyReplicas` when the kind has it, else the replica count `/scale` reports.
- The CRD must exist before curfew starts. If it doesn't, curfew fails to start.
- They are not counted in the `curfew_freed_*` metrics.
- KEDA `ScaledObject`s are not supported this way: KEDA would scale the workload back up.

### Other controllers

curfew changes the replica count. Anything else that sets it puts the workload back up, curfew scales it down again, and the two loop all night. curfew notices: after 3 scale-ups in one sleep window it logs `another controller is likely fighting curfew`, and `curfew_scaled_up_while_asleep_total` counts every one.

- **Argo CD with self-heal** resets `replicas` to the value in Git. Tell it to leave the field alone:
  ```yaml
  # Application
  spec:
    ignoreDifferences:
      - group: apps
        kind: Deployment
        jsonPointers: [/spec/replicas]
    syncPolicy:
      syncOptions: [RespectIgnoreDifferences=true]
  ```
  Or leave `replicas` out of the manifest.
- **KEDA** owns the replica count of the workloads it scales. Don't schedule those with curfew: pause them through KEDA instead, with the `autoscaling.keda.sh/paused-replicas: "0"` annotation on the `ScaledObject`.
- **HorizontalPodAutoscaler** is fine: it does nothing while its target is at 0 replicas, and takes over again once curfew restores it.

The count is kept in memory, so a restart of the operator starts it over. A scale that keeps failing and being retried counts too.

## Behavior

- **Original replicas** are stored in the ConfigMap `curfew-state` in the operator's namespace, with one key per workload: `<kind>.<namespace>.<name>`. To see what is asleep:
  ```bash
  kubectl get configmap curfew-state -n curfew -o yaml
  ```
- **Order of writes:** on sleep, the count is saved before scaling to 0. On wake, the workload is scaled up before its entry is removed. A failure in between is retried and never loses the original count.
- **Removing the annotations** wakes the workload immediately if it was asleep.
- **Deleting a sleeping workload** removes its entry.
- **Scaling by hand during sleep:** if you scale a sleeping workload up, curfew saves the new count and scales it back to 0. Remove the annotations, or set [`curfew/awake-until`](#keep-awake), to keep it up. If this happens 3 times in one sleep window, curfew logs that another controller is likely fighting it (see [Other controllers](#other-controllers)).
- **Already at 0 at sleep time:** nothing is stored, and the workload stays at 0 on wake.
- **CronJobs** are suspended (`spec.suspend: true`) instead of scaled, so they start no new Jobs while asleep; Jobs already running are not stopped. A CronJob counts as 1 replica while it runs and 0 while suspended, so one you suspended yourself stays suspended after wake.
- **Excluded namespaces:** workloads in namespaces starting with `kube-` are ignored, even when annotated, and so are those namespaces' own annotations.
- **Invalid annotations** (a bad cron expression or time zone) are logged as errors and the workload is left as it is. Fixing the annotation triggers a new check.
- **Invalid stored counts** (negative, not a number, or too large) are logged and dropped. The workload is not scaled.
- **Uninstalling the operator** leaves sleeping workloads at 0. The `curfew-state` ConfigMap is created by the operator, not by Helm, so it survives `helm uninstall`. A reinstall picks the entries up and wakes the workloads on schedule.

## Dry run

Set `dryRun=true` to try curfew on a cluster before letting it act:

```bash
helm upgrade --install curfew helm/ -n curfew --set dryRun=true
```

- Every scale and suspend is sent to the API server with `dryRun=All`: it is checked, permissions included, but not applied. Log lines carry `"dryRun":true`.
- `curfew-state` is still written, so the logs show the wake-ups too. Turning dry run off mid-sleep is safe: the stored count matches the running replicas, and curfew scales them down.
- Waves don't wait, since nothing really stops. curfew logs `Would wait for wave` and goes on.

## Permissions

The chart creates:

- **A ClusterRole:**
  - `get`, `list`, `watch` on Deployments and StatefulSets
  - `get`, `update`, `patch` on their `/scale` subresource only
  - `get`, `list`, `watch`, `patch` on CronJobs
  - `list` on Pods, to know when an earlier [wave](#waves) is fully stopped
  - for each of the `extraKinds`: `get`, `list`, `watch` on it, and `get`, `update`, `patch` on its `/scale`

  For Deployments and StatefulSets, curfew can change nothing but the replica count. CronJobs have no subresource for `suspend`, so curfew needs `patch` on the CronJob itself; it only ever patches `spec.suspend`.
- **The binding for that role:**
  - `watchNamespaces` empty: a ClusterRoleBinding, so curfew acts in the whole cluster.
  - `watchNamespaces` set: one RoleBinding per listed namespace, so curfew acts only there. It also watches only those namespaces. Each one must exist before you install.
- **A Role in the operator's namespace:**
  - `create` ConfigMaps, and `get`/`update` on `curfew-state` only
  - Leases, for leader election
  - Events

**Why the operator needs its own namespace:**
- Anyone who can edit the `curfew-state` ConfigMap decides which replica count a sleeping workload gets back.
- Anyone who can edit the `curfew-leader` Lease can stop the operator.
- In a shared namespace such as `default`, that hands these powers to everyone with write access there.

**Pod hardening:**
- Non-root numeric user (65532), read-only root filesystem, no privilege escalation, all Linux capabilities dropped, and the `RuntimeDefault` seccomp profile. This passes the `restricted` Pod Security level.
- The NetworkPolicy allows incoming traffic only on the health port (9090). Outgoing traffic is allowed only to the API server ports, DNS (port 53) and the trace collector's port. It only takes effect if your network plugin enforces NetworkPolicy, as k3s and Calico do.

**Supply chain:**
- Base images in the Dockerfile are pinned by digest.
- Run `just vuln` (govulncheck) to scan dependencies for known vulnerabilities. `just check` includes it.

## Metrics

Set `metrics.enabled=true` to serve Prometheus metrics on port 8080, behind the `curfew-metrics` Service. Add `metrics.serviceMonitor=true` if you run the Prometheus Operator.

| Metric | Labels | What it counts |
|---|---|---|
| `curfew_sleeping_workloads` | `namespace`, `kind` | Workloads asleep, from `curfew-state` |
| `curfew_scale_total` | `kind`, `direction` (`sleep`/`wake`), `result` (`ok`/`error`) | Scale and suspend requests, dry-run ones included |
| `curfew_scaled_up_while_asleep_total` | `namespace`, `kind` | Sleeping workloads found scaled up again and scaled back to 0 (see [Other controllers](#other-controllers)) |
| `curfew_freed_cpu_cores` | `namespace`, `kind`, `name` | CPU requests a sleeping workload gives back: stored replicas × its pod's requests |
| `curfew_freed_memory_bytes` | `namespace`, `kind`, `name` | Same, for memory |

What curfew saves right now, cluster-wide:

```promql
sum(curfew_freed_cpu_cores)
sum(curfew_freed_memory_bytes) / 2^30   # GiB
```

- Only the leader reports `curfew_*` metrics. Other replicas expose controller-runtime's own metrics only.
- After a restart, the numbers come back as the first reconciles run, within seconds.
- Freed resources count regular containers only, not init containers. CronJobs are not counted, since they use nothing between runs.
- The endpoint is plain HTTP without authentication, and the NetworkPolicy opens port 8080 to every namespace. The metrics show namespace and workload names.

## Tracing

Set `tracing.endpoint` to an OTLP HTTP collector, such as Jaeger on port 4318:

```bash
helm upgrade --install curfew helm/ -n curfew \
  --set tracing.endpoint=jaeger-collector.observability:4318
```

Each reconcile (one check-and-fix pass on a workload) creates a `Reconcile` span with the kind, namespace and name. It has child spans `LoadState`, `SaveState` and `Scale`.

Export uses TLS by default. For a collector without TLS, such as a plain in-cluster Jaeger, add `--set tracing.insecure=true`.

For local development, `just jaeger-up` starts Jaeger with Docker Compose. The UI is at http://localhost:16686. Your cluster must be able to reach your machine, which is often `host.docker.internal:4318` with kind or Docker Desktop. Set `tracing.insecure=true` for it.

## Development

Requirements: Go 1.27, [just](https://github.com/casey/just), Docker and Helm.

```bash
just test            # go test -v ./...
just test-race       # with the race detector
just lint            # golangci-lint v2
just vuln            # govulncheck: known vulnerabilities in dependencies
just check           # fmt, vet, lint, test, vuln
just install-tools   # install golangci-lint v2
just docker-build    # build the image (VERSION=x.y.z to set the version)
just docker-buildx   # multi-arch image (linux/amd64, linux/arm64)
just helm-deploy     # create the labeled namespace, install or upgrade the chart
just deploy          # uninstall, rebuild the image, reinstall
```

The namespace defaults to `curfew`. Set `OPERATOR_NS` to change it.

### Local cluster with Colima

[Colima](https://github.com/abiosoft/colima) runs a local Kubernetes (k3s). Images built with Colima's Docker are available to it without a registry.

```bash
just colima-up         # colima start --kubernetes
just e2e               # build, deploy, then run test/e2e.sh (about 5 minutes)
just colima-logs       # follow the operator logs
just colima-undeploy   # uninstall (the curfew-state ConfigMap is kept)
```

`test/e2e.sh` checks that:
- a Deployment and a StatefulSet sleep and wake on real cron times a few minutes ahead,
- their counts are stored and then cleared,
- the StatefulSet, in wave 1, waits for the Deployment's pods to be gone,
- `curfew/awake-until` wakes a sleeping workload, and removing the annotations wakes it right away,
- a `Widget` custom resource (`test/widget-crd.yaml`, enabled through `test/e2e-values.yaml`) sleeps and wakes through `/scale`.

It runs in a throwaway `curfew-e2e` namespace. `KUBE_CONTEXT` (default `colima`) selects another cluster.

Code layout:

- `cmd/root.go`: CLI entry point, logging and tracing setup
- `internal/curfew/`: controller setup, reconcile loop (`curfew.go`), and which rule applies and when (`schedule.go`)
- `internal/values/`: annotation names and constants
- `internal/telemetry/`: OpenTelemetry tracer setup
- `helm/`: the chart
- `test/e2e.sh`: end-to-end test against a running cluster

## License

MIT, see [LICENSE](LICENSE).
