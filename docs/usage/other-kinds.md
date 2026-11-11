# Other kinds

Any kind with a `/scale` subresource can be scheduled too, such as Argo Rollouts. List it in the chart's values:

```yaml
extraKinds:
  - group: argoproj.io
    version: v1alpha1
    kind: Rollout
    resource: rollouts   # the plural name, used for permissions
```

curfew then reads the same annotations on them, and [namespace schedules](namespaces.md), `curfew/exclude-kinds` (`Rollout`) and [waves](waves.md) cover them too.

## Details

- The chart grants `get`, `list`, `watch` on the kind, and `get`, `update`, `patch` on its `/scale` only.
- In waves, curfew waits for the pods matching the selector that `/scale` reports. Readiness on wake is `status.readyReplicas` when the kind has it, else the replica count `/scale` reports.
- They are not counted in the `curfew_freed_*` [metrics](../operations/metrics.md).

!!! warning "Install the CRD first"
    The kind's CustomResourceDefinition must exist before curfew starts. If it doesn't, curfew fails to start.

KEDA `ScaledObject`s are not supported this way: see [Other controllers](other-controllers.md#keda).
