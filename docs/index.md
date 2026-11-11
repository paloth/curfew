# curfew

curfew is a Kubernetes operator that puts workloads to sleep on a cron schedule, then brings them back. Use it to switch off non-production workloads at night and on weekends.

- **Deployments, StatefulSets** and any other kind with a `/scale` subresource are scaled to 0.
- **CronJobs** are suspended.
- The replica count before sleep is remembered and restored on wake-up.

## In one example

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

This Deployment sleeps every weekday night and all weekend, from Friday 20:00 until Monday 08:00.

## What else it does

| | |
|---|---|
| [Namespace schedules](usage/namespaces.md) | One schedule for every workload in a namespace, with exclusions by kind or label |
| [Holidays](usage/holidays.md) | Skip sleep and wake on listed dates, so a long weekend stays asleep |
| [Keep awake](usage/keep-awake.md) | Keep a workload or a namespace up until a given time, without touching its schedule |
| [Waves](usage/waves.md) | Stop workloads in order, such as an app before its database, and wake them in reverse |
| [Other kinds](usage/other-kinds.md) | Schedule Argo Rollouts or any custom resource with a `/scale` subresource |
| [Dry run](operations/dry-run.md) | See what curfew would do on a cluster before letting it act |
| [Metrics](operations/metrics.md) | Prometheus metrics, including the CPU and memory freed while workloads sleep |

## Get started

[Install curfew](install.md) with its Helm chart, then [add a schedule](usage/schedules.md) to a workload.
