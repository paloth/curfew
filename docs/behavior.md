# Behavior

## Where replica counts are kept

Original replicas are stored in the ConfigMap `curfew-state` in the operator's namespace, with one key per workload: `<kind>.<namespace>.<name>`. To see what is asleep:

```bash
kubectl get configmap curfew-state -n curfew -o yaml
```

**Order of writes:** on sleep, the count is saved before scaling to 0. On wake, the workload is scaled up before its entry is removed. A failure in between is retried and never loses the original count.

## Edge cases

| What happens | What curfew does |
|---|---|
| Annotations removed from a sleeping workload | Wakes it right away |
| Sleeping workload deleted | Removes its entry |
| Sleeping workload scaled up by hand | Saves the new count and scales it back to 0. Remove the annotations, or set [`curfew/awake-until`](usage/keep-awake.md), to keep it up. 3 times in one sleep window points to [another controller](usage/other-controllers.md). |
| Workload already at 0 at sleep time | Stores nothing; it stays at 0 on wake |
| CronJob asleep | Suspended (`spec.suspend: true`): it starts no new Jobs, but Jobs already running are not stopped. A CronJob counts as 1 replica while it runs and 0 while suspended, so one you suspended yourself stays suspended after wake. |
| Namespace starting with `kube-` | Ignored, along with its workloads, even when annotated |
| Invalid annotation (cron, time zone, date) | Logged as an error; the workload is left as it is. Fixing the annotation triggers a new check. |
| Invalid stored count (negative, not a number, too large) | Logged and dropped; the workload is not scaled |
| Operator uninstalled | Sleeping workloads stay at 0. `curfew-state` survives `helm uninstall`, and a reinstall wakes them on schedule. |

## Running more than one replica

Leader election is always on: with `replicaCount` above 1, only the leader acts, and another replica takes over if it goes away.
