# Install

curfew runs inside the cluster only. Install it with the Helm chart in the repository's `helm/` folder, in its own namespace, labeled with the `restricted` Pod Security level:

```bash
git clone https://github.com/paloth/curfew.git && cd curfew
kubectl create namespace curfew
kubectl label namespace curfew pod-security.kubernetes.io/enforce=restricted
helm upgrade --install curfew helm/ --namespace curfew
```

From a checkout, `just helm-deploy` runs the last three steps.

!!! warning "Keep the namespace to admins"
    Anyone who can write in curfew's namespace can change the replica counts sleeping workloads get back, or stop the operator. See [Security](operations/security.md#why-curfew-needs-its-own-namespace).

## Values

Set them with `--set` or a values file (`helm/values.yaml` lists them all):

| Value | Default | What it does |
|---|---|---|
| `image.repository` / `image.tag` | `ghcr.io/paloth/curfew` / chart `appVersion` | Operator image. Use an immutable version tag. |
| `replicaCount` | `1` | Operator pods. Leader election is always on, so more than one is safe: only the leader acts. |
| `dev` | `false` | Debug logs |
| `dryRun` | `false` | Decide and log without changing any workload (see [Dry run](operations/dry-run.md)) |
| `extraKinds` | `[]` | More kinds to schedule, such as Argo Rollouts (see [Other kinds](usage/other-kinds.md)) |
| `watchNamespaces` | `[]` | Namespaces curfew may act in. Empty means the whole cluster. |
| `networkPolicy.enabled` | `true` | Restricts the operator pod's network traffic |
| `networkPolicy.apiServerPorts` | `[443, 6443]` | Ports the API server listens on |
| `metrics.enabled` | `false` | Prometheus metrics on port 8080, with a Service (see [Metrics](operations/metrics.md)) |
| `metrics.serviceMonitor` | `false` | Also create a ServiceMonitor (needs the Prometheus Operator) |
| `tracing.endpoint` | `""` | OTLP HTTP `host:port` for traces. Empty disables tracing (see [Tracing](operations/tracing.md)). |
| `tracing.insecure` | `false` | Send traces over plain HTTP instead of TLS |
| `resources` | 10m CPU, 64Mi request, 256Mi limit | The operator caches every workload it watches. Raise memory on big clusters. |

## Uninstall

```bash
helm uninstall curfew --namespace curfew
```

Sleeping workloads stay at 0. The `curfew-state` ConfigMap, which holds their replica counts, is created by the operator rather than by Helm, so it survives the uninstall. A reinstall picks it up and wakes the workloads on schedule.
