# Other controllers

curfew changes the replica count. Anything else that sets it puts the workload back up, curfew scales it down again, and the two loop all night.

curfew notices: after 3 scale-ups in one sleep window it logs `another controller is likely fighting curfew`, and the `curfew_scaled_up_while_asleep_total` [metric](../operations/metrics.md) counts every one. The count is kept in memory, so a restart of the operator starts it over. A scale that keeps failing and being retried counts too.

## Argo CD

With self-heal on, Argo CD resets `replicas` to the value in Git. Tell it to leave the field alone:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
spec:
  ignoreDifferences:
    - group: apps
      kind: Deployment
      jsonPointers: [/spec/replicas]
  syncPolicy:
    syncOptions: [RespectIgnoreDifferences=true]
```

Or leave `replicas` out of the manifest.

## KEDA

KEDA owns the replica count of the workloads it scales. Don't schedule those with curfew: pause them through KEDA instead, with the `autoscaling.keda.sh/paused-replicas: "0"` annotation on the `ScaledObject`.

## HorizontalPodAutoscaler

Nothing to do: an HPA does nothing while its target is at 0 replicas, and takes over again once curfew restores it.
