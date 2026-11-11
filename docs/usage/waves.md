# Waves

Some workloads must stop in order, such as an app that needs to shut down cleanly before its database stops. Give them a wave, like Argo CD sync waves:

```yaml
metadata:
  annotations:
    curfew/wave: "1"   # any integer, negative included; default 0
```

Wave `1` on the database is enough: the app (wave 0) stops first, and the database comes back first.

## How it works

Waves only order workloads of the same namespace.

- **Sleep goes up the waves:** `-1`, then `0` (workloads without the annotation), then `1`, and so on. A workload waits until every lower-wave workload that is also going to sleep is fully stopped:
    - Deployments, StatefulSets and [other kinds](other-kinds.md): at 0 replicas with no pods left, terminating ones included.
    - CronJobs: suspended, with no running Jobs.
- **Wake goes down the waves.** A workload waits until every higher-wave workload that is also waking up is back and ready.

Workloads moving the other way, or not scheduled, don't hold anyone back.

## Waiting

- A waiting workload is checked again every 10 seconds, and logs `Waiting for wave` with the workload it waits for.
- After 10 minutes it stops waiting, logs `Wave timeout, proceeding`, and goes ahead, so one stuck pod can't keep the rest up all night.
- If the operator restarts or changes leader, the 10 minutes start over.
- In [dry run](../operations/dry-run.md), nothing really stops, so waves don't wait: curfew logs `Would wait for wave` and goes on.
