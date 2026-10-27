# Keep awake

Working late? Keep a workload, or a whole namespace, awake without touching its schedule:

```bash
kubectl annotate deployment my-app curfew/awake-until=2026-10-28T08:00:00+01:00
kubectl annotate namespace staging curfew/awake-until=2026-10-28T08:00:00+01:00
```

- Until that time the workload counts as awake: a sleeping one wakes right away, and nothing goes to sleep. After it, the schedule applies again.
- A namespace's `curfew/awake-until` covers all its scheduled workloads, those with their own schedule included. A workload's own `curfew/awake-until` wins over the namespace's.
- The time is [RFC 3339](https://www.rfc-editor.org/rfc/rfc3339) and needs a zone: `Z` or an offset such as `+01:00`.
- An invalid value is logged and the workload is left as it is, as with other invalid annotations.
- An expired annotation does nothing. curfew does not remove it, so you can leave it or delete it:

```bash
kubectl annotate deployment my-app curfew/awake-until-
```
