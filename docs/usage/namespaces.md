# Namespace schedules

Put the [schedule annotations](schedules.md) on a namespace to apply them to every workload in it:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: staging
  annotations:
    curfew/sleep: "0 20 * * 1-5"
    curfew/wake: "0 8 * * 1-5"
    curfew/timezone: "Europe/Paris"
    curfew/exclude-kinds: "StatefulSet"           # optional
    curfew/exclude-labels: "tier=data,keep-alive" # optional
```

## Which rule a workload follows

A workload in that namespace follows the first rule that applies:

1. **Its own rule:** a workload with its own `curfew/sleep` uses its own `curfew/sleep`, `curfew/wake`, `curfew/timezone` and `curfew/skip-dates`, and ignores the namespace's.
2. **Excluded:** the namespace's rule is skipped for a workload whose kind is listed in `curfew/exclude-kinds`, or whose labels match an entry of `curfew/exclude-labels`.
3. **The namespace's rule.**

| Annotation | Format |
|---|---|
| `curfew/exclude-kinds` | Comma-separated kinds, any case: `Deployment`, `StatefulSet`, `CronJob`, or one of your [other kinds](other-kinds.md) |
| `curfew/exclude-labels` | Comma-separated entries: `key=value` (that exact value) or `key` (any value). One matching entry is enough. |

## Changes apply at once

Changing or removing the namespace annotations re-checks every workload in it. Workloads that are no longer covered, because the schedule was removed or they became excluded, are woken up.

Namespaces starting with `kube-` are ignored, along with the workloads in them.

[`curfew/awake-until`](keep-awake.md) on a namespace works even without a namespace schedule: it keeps every scheduled workload in it awake, those with their own schedule included.
