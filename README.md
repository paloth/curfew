# curfew

A Kubernetes operator that puts workloads to sleep on a cron schedule, then brings them back. Use it to switch off non-production workloads at night and on weekends.

Deployments, StatefulSets and any other kind with a `/scale` subresource are scaled to 0, and CronJobs are suspended. curfew remembers the replica count before sleep and restores it on wake-up.

**Documentation: <https://paloth.github.io/curfew/>**

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

It also does [namespace-wide schedules](https://paloth.github.io/curfew/usage/namespaces/), [holidays](https://paloth.github.io/curfew/usage/holidays/), [keep-awake overrides](https://paloth.github.io/curfew/usage/keep-awake/), [ordered shutdown in waves](https://paloth.github.io/curfew/usage/waves/), [Argo Rollouts and other kinds](https://paloth.github.io/curfew/usage/other-kinds/), a [dry run](https://paloth.github.io/curfew/operations/dry-run/) and [Prometheus metrics](https://paloth.github.io/curfew/operations/metrics/).

## Install

curfew runs inside the cluster only. Install it in its own namespace, labeled with the `restricted` Pod Security level:

```bash
kubectl create namespace curfew
kubectl label namespace curfew pod-security.kubernetes.io/enforce=restricted
helm upgrade --install curfew helm/ --namespace curfew
```

See [Install](https://paloth.github.io/curfew/install/) for the chart's values, and [Security](https://paloth.github.io/curfew/operations/security/) for why the namespace matters.

## Development

Requirements: Go 1.27, [just](https://github.com/casey/just), Docker and Helm.

```bash
just check   # fmt, vet, lint, test, vuln
just e2e     # end-to-end test on a local Colima cluster
just docs-serve   # this documentation, at http://127.0.0.1:8000
```

More in [Development](https://paloth.github.io/curfew/development/). The documentation lives in `docs/`.

## License

MIT, see [LICENSE](LICENSE).
