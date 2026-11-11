# Security

## Permissions

The chart creates:

- **A ClusterRole:**
    - `get`, `list`, `watch` on Deployments and StatefulSets
    - `get`, `update`, `patch` on their `/scale` subresource only
    - `get`, `list`, `watch`, `patch` on CronJobs
    - `list` on Pods, to know when an earlier [wave](../usage/waves.md) is fully stopped
    - for each of the [`extraKinds`](../usage/other-kinds.md): `get`, `list`, `watch` on it, and `get`, `update`, `patch` on its `/scale`

    For Deployments and StatefulSets, curfew can change nothing but the replica count. CronJobs have no subresource for `suspend`, so curfew needs `patch` on the CronJob itself; it only ever patches `spec.suspend`.

- **The binding for that role:**
    - `watchNamespaces` empty: a ClusterRoleBinding, so curfew acts in the whole cluster.
    - `watchNamespaces` set: one RoleBinding per listed namespace, so curfew acts only there. It also watches only those namespaces. Each one must exist before you install.
- **A ClusterRole to read namespaces**, for [namespace schedules](../usage/namespaces.md), in both modes.
- **A Role in the operator's namespace:**
    - `create` ConfigMaps, and `get`/`update` on `curfew-state` only
    - Leases, for leader election
    - Events

## Why curfew needs its own namespace

- Anyone who can edit the `curfew-state` ConfigMap decides which replica count a sleeping workload gets back.
- Anyone who can edit the `curfew-leader` Lease can stop the operator.
- In a shared namespace such as `default`, that hands these powers to everyone with write access there.

## Pod hardening

- Non-root numeric user (65532), read-only root filesystem, no privilege escalation, all Linux capabilities dropped, and the `RuntimeDefault` seccomp profile. This passes the `restricted` Pod Security level.
- The NetworkPolicy allows incoming traffic only on the health port (9090), plus the metrics port (8080) when [metrics](metrics.md) are on. Outgoing traffic is allowed only to the API server ports, DNS (port 53) and the trace collector's port. It only takes effect if your network plugin enforces NetworkPolicy, as k3s and Calico do.

## Supply chain

- Base images in the Dockerfile are pinned by digest, and GitHub Actions by commit SHA.
- `just vuln` runs govulncheck on the dependencies; CI runs it on every pull request.
