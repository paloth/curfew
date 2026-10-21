# curfew

A Kubernetes operator that scales Deployments and StatefulSets to zero on a cron schedule and back up again. Use it to switch off non-production workloads at night and on weekends.

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
| `watchNamespaces` | `[]` | Namespaces curfew may act in. Empty means the whole cluster. |
| `networkPolicy.enabled` | `true` | Restricts the operator pod's network traffic |
| `networkPolicy.apiServerPorts` | `[443, 6443]` | Ports the API server listens on |
| `tracing.endpoint` | `""` | OTLP HTTP `host:port` for traces. Empty disables tracing. |
| `tracing.insecure` | `false` | Send traces over plain HTTP instead of TLS |
| `resources` | 10m CPU, 64Mi request, 256Mi limit | The operator caches every Deployment and StatefulSet it watches. Raise memory on big clusters. |

## Usage

Add the annotations to a Deployment or StatefulSet:

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

Both schedules are read in the given time zone.

The example above sleeps from Friday 20:00 until Monday 08:00. The next wake time is Monday, so the workload stays down all weekend.

### How it decides

At any moment curfew looks at which comes next, the next sleep or the next wake:

- **Wake comes first:** the workload should be asleep now.
- **Sleep comes first:** the workload should be awake now.

It then schedules its next check for exactly that time. Adding the annotations during a sleep window scales the workload down right away.

### Namespace schedule

Put the same annotations on a namespace to apply them to every Deployment and StatefulSet in it:

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
2. **Excluded:** the namespace's rule is skipped for a workload whose kind is listed in `curfew/exclude-kinds` (`Deployment`, `StatefulSet`), or whose labels match an entry of `curfew/exclude-labels`. An entry is `key=value` (that exact value) or `key` (any value); one matching entry is enough.
3. **The namespace's rule.**

Changing or removing the namespace annotations applies at once. Workloads that are no longer covered, because the schedule was removed or they became excluded, are woken up.

## Behavior

- **Original replicas** are stored in the ConfigMap `curfew-state` in the operator's namespace, with one key per workload: `<kind>.<namespace>.<name>`. To see what is asleep:
  ```bash
  kubectl get configmap curfew-state -n curfew -o yaml
  ```
- **Order of writes:** on sleep, the count is saved before scaling to 0. On wake, the workload is scaled up before its entry is removed. A failure in between is retried and never loses the original count.
- **Removing the annotations** wakes the workload immediately if it was asleep.
- **Deleting a sleeping workload** removes its entry.
- **Scaling by hand during sleep:** if you scale a sleeping workload up, curfew saves the new count and scales it back to 0. Remove the annotations to keep it up.
- **Already at 0 at sleep time:** nothing is stored, and the workload stays at 0 on wake.
- **Excluded namespaces:** workloads in namespaces starting with `kube-` are ignored, even when annotated, and so are those namespaces' own annotations.
- **Invalid annotations** (a bad cron expression or time zone) are logged as errors and the workload is left as it is. Fixing the annotation triggers a new check.
- **Invalid stored counts** (negative, not a number, or too large) are logged and dropped. The workload is not scaled.
- **Uninstalling the operator** leaves sleeping workloads at 0. The `curfew-state` ConfigMap is created by the operator, not by Helm, so it survives `helm uninstall`. A reinstall picks the entries up and wakes the workloads on schedule.

## Permissions

The chart creates:

- **A ClusterRole:**
  - `get`, `list`, `watch` on Deployments and StatefulSets
  - `get`, `update`, `patch` on their `/scale` subresource only

  curfew cannot change anything on your workloads except the replica count.
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
- removing the annotations wakes a workload right away.

It runs in a throwaway `curfew-e2e` namespace. `KUBE_CONTEXT` (default `colima`) selects another cluster.

Code layout:

- `cmd/root.go`: CLI entry point, logging and tracing setup
- `internal/curfew/`: controller setup, reconcile loop (`curfew.go`), and which rule applies and when (`schedule.go`)
- `internal/values/`: annotation names and constants
- `internal/telemetry/`: OpenTelemetry tracer setup
- `helm/`: the chart
- `mocks/`: sample annotated manifests for manual testing
- `test/e2e.sh`: end-to-end test against a running cluster

## License

MIT, see [LICENSE](LICENSE).
