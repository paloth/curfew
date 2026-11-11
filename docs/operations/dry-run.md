# Dry run

Set `dryRun=true` to try curfew on a cluster before letting it act:

```bash
helm upgrade --install curfew helm/ -n curfew --set dryRun=true
```

- Every scale and suspend is sent to the API server with `dryRun=All`: it is checked, permissions included, but not applied. Log lines carry `"dryRun":true`.
- `curfew-state` is still written, so the logs show the wake-ups too.
- [Waves](../usage/waves.md) don't wait, since nothing really stops. curfew logs `Would wait for wave` and goes on.

Turning dry run off mid-sleep is safe: the stored count matches the running replicas, and curfew scales them down.

To follow what curfew would do:

```bash
kubectl -n curfew logs deploy/curfew -f | grep -E 'Scaling down|Waking up'
```
