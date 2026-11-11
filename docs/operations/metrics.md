# Metrics

Set `metrics.enabled=true` to serve Prometheus metrics on port 8080, behind the `curfew-metrics` Service. Add `metrics.serviceMonitor=true` if you run the Prometheus Operator.

```bash
helm upgrade --install curfew helm/ -n curfew --set metrics.enabled=true --set metrics.serviceMonitor=true
```

| Metric | Labels | What it counts |
|---|---|---|
| `curfew_sleeping_workloads` | `namespace`, `kind` | Workloads asleep, from `curfew-state` |
| `curfew_scale_total` | `kind`, `direction` (`sleep`/`wake`), `result` (`ok`/`error`) | Scale and suspend requests, dry-run ones included |
| `curfew_scaled_up_while_asleep_total` | `namespace`, `kind` | Sleeping workloads found scaled up again and scaled back to 0 (see [Other controllers](../usage/other-controllers.md)) |
| `curfew_freed_cpu_cores` | `namespace`, `kind`, `name` | CPU requests a sleeping workload gives back: stored replicas × its pod's requests |
| `curfew_freed_memory_bytes` | `namespace`, `kind`, `name` | Same, for memory |

What curfew saves right now, cluster-wide:

```promql
sum(curfew_freed_cpu_cores)
sum(curfew_freed_memory_bytes) / 2^30   # GiB
```

## Good to know

- Only the leader reports `curfew_*` metrics. Other replicas expose controller-runtime's own metrics only.
- After a restart, the numbers come back as the first reconciles run, within seconds.
- Freed resources count regular containers only, not init containers. CronJobs and [other kinds](../usage/other-kinds.md) are not counted.

!!! warning "No authentication"
    The endpoint is plain HTTP without authentication, and the NetworkPolicy opens port 8080 to every namespace. The metrics show namespace and workload names.

To look at them by hand:

```bash
kubectl -n curfew port-forward svc/curfew-metrics 8080
curl -s localhost:8080/metrics | grep curfew_
```
